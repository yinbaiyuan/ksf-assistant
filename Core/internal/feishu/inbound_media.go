package feishu

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

const (
	defaultInboundMaxBytes     = 25 * 1024 * 1024
	defaultInboundMaxResources = 10
)

type InboundResource struct {
	FileKey, ResourceType, DisplayName string
}

type InboundAsset struct {
	MessageType, ResourceType, DisplayName, LocalPath string
	SizeBytes                                         int64
}

type StagedInboundMessage struct {
	MessageType string
	Text        string
	Assets      []InboundAsset
	CleanupDir  string
}

func inboundMessageDetails(message InboundMessage) (string, []InboundResource, error) {
	var content any
	rawMessage, _ := message.Raw["message"].(map[string]any)
	if err := json.Unmarshal([]byte(stringValue(rawMessage["content"])), &content); err != nil {
		content = map[string]any{}
	}
	switch message.MessageType {
	case "text":
		return message.Text, nil, nil
	case "image":
		value, _ := content.(map[string]any)
		key := stringValue(value["image_key"])
		if key == "" {
			return "", nil, errors.New("inbound_image_key_missing")
		}
		return "", []InboundResource{{FileKey: key, ResourceType: "image", DisplayName: "image"}}, nil
	case "file", "audio", "media":
		value, _ := content.(map[string]any)
		key := stringValue(value["file_key"])
		if key == "" {
			return "", nil, errors.New("inbound_file_key_missing")
		}
		return stringValue(value["text"]), []InboundResource{{FileKey: key, ResourceType: "file", DisplayName: safeInboundFileName(stringValue(value["file_name"]), message.MessageType+"-file")}}, nil
	case "post":
		text := []string{}
		resources := []InboundResource{}
		seen := map[string]bool{}
		var visit func(any)
		visit = func(value any) {
			switch item := value.(type) {
			case []any:
				for _, child := range item {
					visit(child)
				}
			case map[string]any:
				if item["tag"] == "text" && stringValue(item["text"]) != "" {
					text = append(text, stringValue(item["text"]))
				}
				if title := stringValue(item["title"]); title != "" {
					text = append(text, title)
				}
				for key, kind := range map[string]string{"image_key": "image", "file_key": "file"} {
					fileKey := stringValue(item[key])
					if fileKey != "" && !seen[fileKey] {
						seen[fileKey] = true
						resources = append(resources, InboundResource{FileKey: fileKey, ResourceType: kind, DisplayName: safeInboundFileName(stringValue(item["file_name"]), fmt.Sprintf("%s-%d", kind, len(resources)+1))})
					}
				}
				for _, child := range item {
					visit(child)
				}
			}
		}
		visit(content)
		return strings.TrimSpace(strings.Join(text, "\n")), resources, nil
	default:
		return "", nil, fmt.Errorf("unsupported inbound message type: %s", message.MessageType)
	}
}

func StageInboundMessage(ctx context.Context, runner CapabilityExecutor, message InboundMessage, maxBytes int64) (StagedInboundMessage, error) {
	text, resources, err := inboundMessageDetails(message)
	if err != nil {
		return StagedInboundMessage{}, err
	}
	if len(resources) > defaultInboundMaxResources {
		return StagedInboundMessage{}, errors.New("too_many_inbound_resources")
	}
	if len(resources) == 0 {
		return StagedInboundMessage{MessageType: message.MessageType, Text: text, Assets: []InboundAsset{}}, nil
	}
	if maxBytes <= 0 {
		maxBytes = defaultInboundMaxBytes
	}
	root := filepath.Join(runner.DataRoot, "private-cache", "inbound-assets")
	if err := ensurePrivateDirectory(root); err != nil {
		return StagedInboundMessage{}, err
	}
	digest := sha256.Sum256([]byte(message.MessageID))
	directory := filepath.Join(root, hex.EncodeToString(digest[:12]))
	if err := CleanupInboundAssets(runner.DataRoot, directory); err != nil {
		return StagedInboundMessage{}, err
	}
	if err := os.Mkdir(directory, 0o700); err != nil {
		return StagedInboundMessage{}, err
	}
	result := StagedInboundMessage{MessageType: message.MessageType, Text: text, Assets: []InboundAsset{}, CleanupDir: directory}
	failed := true
	defer func() {
		if failed {
			_ = CleanupInboundAssets(runner.DataRoot, directory)
		}
	}()
	var total int64
	for index, resource := range resources {
		name := fmt.Sprintf("%02d-%s", index+1, safeInboundFileName(resource.DisplayName, "resource"))
		output := filepath.Join(directory, name)
		if err := runner.DownloadMessageResource(ctx, message.MessageID, resource.FileKey, resource.ResourceType, output, 2*time.Minute); err != nil {
			return StagedInboundMessage{}, err
		}
		info, err := os.Lstat(output)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return StagedInboundMessage{}, errors.New("downloaded_resource_is_not_a_regular_file")
		}
		total += info.Size()
		if total > maxBytes {
			return StagedInboundMessage{}, errors.New("inbound_resources_exceed_size_limit")
		}
		if err := os.Chmod(output, 0o600); err != nil {
			return StagedInboundMessage{}, err
		}
		result.Assets = append(result.Assets, InboundAsset{MessageType: message.MessageType, ResourceType: resource.ResourceType, DisplayName: resource.DisplayName, LocalPath: output, SizeBytes: info.Size()})
	}
	failed = false
	return result, nil
}

func InboundAssetsPrompt(prompt string, staged StagedInboundMessage) string {
	if len(staged.Assets) == 0 {
		return prompt
	}
	lines := []string{strings.TrimSpace(prompt), "", "飞书服务已将本轮附件暂存为以下本机只读输入。请按用户意图读取这些文件；不要移动、改写或删除它们："}
	if lines[0] == "" {
		lines[0] = "请分析我发送的附件并给出有用的回复。"
	}
	for index, asset := range staged.Assets {
		lines = append(lines, fmt.Sprintf("%d. %s (%s/%s, %d bytes): %s", index+1, asset.DisplayName, asset.MessageType, asset.ResourceType, asset.SizeBytes, asset.LocalPath))
	}
	return strings.Join(lines, "\n")
}

func CleanupInboundAssets(dataRoot, directory string) error {
	if strings.TrimSpace(directory) == "" {
		return nil
	}
	root := filepath.Join(dataRoot, "private-cache", "inbound-assets")
	relative, err := filepath.Rel(root, directory)
	if err != nil || relative == "." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return errors.New("unsafe_inbound_asset_cleanup_path")
	}
	return os.RemoveAll(directory)
}

func safeInboundFileName(value, fallback string) string {
	name := filepath.Base(strings.TrimSpace(value))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`\\/:*?"<>|`, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimLeft(name, ".")
	name = strings.TrimSpace(name)
	if name == "" {
		name = fallback
	}
	runes := []rune(name)
	if len(runes) > 160 {
		name = string(runes[:160])
	}
	return name
}
