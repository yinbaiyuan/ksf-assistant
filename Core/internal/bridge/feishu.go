package bridge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"codexusagebar/core/internal/domain"
)

type FeishuClient struct {
	Node string
}

func (runtime FeishuClient) Inspect(ctx context.Context, root string) (domain.FeishuSnapshot, error) {
	client, node, err := validateFeishuWithNode(root, runtime.Node)
	if err != nil {
		return domain.FeishuSnapshot{Availability: "notConfigured", TargetAliases: []string{}, Links: []domain.FeishuTaskLink{}}, err
	}
	statusData, err := run(ctx, root, node, []string{client, "status"}, nil)
	if err != nil {
		return domain.FeishuSnapshot{}, err
	}
	targetData, err := run(ctx, root, node, []string{client, "targets", "list"}, nil)
	if err != nil {
		return domain.FeishuSnapshot{}, err
	}
	protocolData, err := run(ctx, root, node, []string{client, "task-link", "protocol"}, nil)
	if err != nil {
		return domain.FeishuSnapshot{}, err
	}
	linkData, err := run(ctx, root, node, []string{client, "task-link", "list"}, nil)
	if err != nil {
		return domain.FeishuSnapshot{}, err
	}
	var status struct {
		PID struct {
			Alive bool `json:"alive"`
		} `json:"pid"`
		Outbound struct {
			Enabled bool `json:"enabled"`
			DryRun  bool `json:"dryRun"`
		} `json:"outbound"`
		Service struct {
			Running bool `json:"running"`
			Loaded  bool `json:"loaded"`
		} `json:"service"`
		Launchd struct {
			Running *bool `json:"running"`
		} `json:"launchd"`
		WindowsTask struct {
			Running *bool `json:"running"`
		} `json:"windowsTask"`
		EventConsumer struct {
			Profile           string `json:"profile"`
			ProfileValid      bool   `json:"profileValid"`
			DesiredConnection bool   `json:"desiredConnection"`
		} `json:"eventConsumer"`
	}
	var targets struct {
		Targets struct {
			Messages []struct {
				Alias            string `json:"alias"`
				TaskLinkEligible bool   `json:"taskLinkEligible"`
			} `json:"messages"`
		} `json:"targets"`
	}
	var protocol struct {
		Protocol  string `json:"protocol"`
		Version   int    `json:"version"`
		Readiness struct {
			Ready    bool     `json:"ready"`
			Blockers []string `json:"blockers"`
		} `json:"readiness"`
	}
	var links struct {
		Links []domain.FeishuTaskLink `json:"links"`
	}
	if json.Unmarshal(statusData, &status) != nil || json.Unmarshal(targetData, &targets) != nil || json.Unmarshal(protocolData, &protocol) != nil || json.Unmarshal(linkData, &links) != nil {
		return domain.FeishuSnapshot{}, fmt.Errorf("飞书桥返回了无法识别的数据")
	}
	aliases := []string{}
	for _, target := range targets.Targets.Messages {
		if target.TaskLinkEligible {
			aliases = append(aliases, target.Alias)
		}
	}
	sort.Strings(aliases)
	running := status.PID.Alive || status.Service.Running || (status.Launchd.Running != nil && *status.Launchd.Running) || (status.WindowsTask.Running != nil && *status.WindowsTask.Running)
	availability, message := "ready", ""
	if !status.Outbound.Enabled {
		availability, message = "unavailable", "飞书桥尚未启用主动出站。"
	} else if status.Outbound.DryRun {
		availability = "dryRun"
	} else if !running {
		availability = "stopped"
	} else if protocol.Protocol != "codex-feishu-task-link-v1" || protocol.Version < 2 {
		availability, message = "unavailable", "飞书桥任务控制协议需要 v2。"
	} else if !protocol.Readiness.Ready {
		availability, message = "unavailable", "飞书桥任务控制尚未就绪："+strings.Join(protocol.Readiness.Blockers, "、")
	}
	return domain.FeishuSnapshot{Availability: availability, Message: message, Profile: status.EventConsumer.Profile, ProfileValid: status.EventConsumer.ProfileValid, InboundConnection: status.EventConsumer.DesiredConnection, ProcessRunning: running, TargetAliases: aliases, TaskLinkProtocolVersion: protocol.Version, TaskLinkReady: protocol.Readiness.Ready, ReadinessBlockers: protocol.Readiness.Blockers, Links: links.Links}, nil
}

func (client FeishuClient) CreateTaskLink(ctx context.Context, root, threadID, title, projectName, targetAlias string) (domain.FeishuTaskLink, error) {
	payload, _ := json.Marshal(map[string]string{"threadId": threadID, "title": title, "projectName": projectName, "targetAlias": targetAlias})
	return mutateTaskLink(ctx, root, client.Node, []string{"task-link", "create", "--payload-file", "-"}, payload)
}

func (client FeishuClient) InterruptTaskLink(ctx context.Context, root, threadID string) (domain.FeishuTaskLink, error) {
	return mutateTaskLink(ctx, root, client.Node, []string{"task-link", "interrupt", "--task-key", domain.PublicTaskKey(threadID)}, nil)
}

func (client FeishuClient) ReleaseTaskLink(ctx context.Context, root, threadID string) (domain.FeishuTaskLink, error) {
	return mutateTaskLink(ctx, root, client.Node, []string{"task-link", "release", "--task-key", domain.PublicTaskKey(threadID)}, nil)
}

func (runtime FeishuClient) SendTest(ctx context.Context, root, targetAlias string) error {
	client, node, err := validateFeishuWithNode(root, runtime.Node)
	if err != nil {
		return err
	}
	_, err = run(ctx, root, node, []string{client, "send", "--target", targetAlias, "--format", "text", "--content-file", "-", "--source", "codex-usage-bar", "--reason", "用户在 CodexAssistant 中手动发送连接测试消息"}, []byte("CodexAssistant 飞书桥连接测试成功"))
	return err
}

func (client FeishuClient) Profile(ctx context.Context, root string) (map[string]any, error) {
	return client.command(ctx, root, []string{"profile", "show"}, nil)
}

func (client FeishuClient) SetProfile(ctx context.Context, root, profile string) (map[string]any, error) {
	if profile != "primary" && profile != "manual-only" {
		return nil, fmt.Errorf("不支持的飞书事件 profile")
	}
	return client.command(ctx, root, []string{"profile", "set", profile}, nil)
}

func (client FeishuClient) ControlService(ctx context.Context, root, action string) (map[string]any, error) {
	if action != "start" && action != "restart" {
		return nil, fmt.Errorf("不支持的飞书服务动作")
	}
	return client.command(ctx, root, []string{action}, nil)
}

func (client FeishuClient) ConfigureExisting(ctx context.Context, root, appID, appSecret string) error {
	payload, _ := json.Marshal(map[string]string{"appId": appID, "appSecret": appSecret, "brand": "feishu"})
	_, err := client.command(ctx, root, []string{"auth", "configure-existing", "--payload-file", "-"}, payload)
	return err
}

func (client FeishuClient) StartConfig(ctx context.Context, root string) (map[string]any, error) {
	result, err := client.command(ctx, root, []string{"auth", "start-config", "--create-new"}, nil)
	if err != nil {
		return nil, err
	}
	return attachPrivateQR(result)
}

func (client FeishuClient) StartUserAuth(ctx context.Context, root string) (map[string]any, error) {
	result, err := client.command(ctx, root, []string{"auth", "start-user", "--scope", "required"}, nil)
	if err != nil {
		return nil, err
	}
	return attachPrivateQR(result)
}

func attachPrivateQR(result map[string]any) (map[string]any, error) {
	qrPath, _ := result["qrPath"].(string)
	qrData, err := readPrivateQR(qrPath)
	if err != nil {
		return nil, err
	}
	public := map[string]any{}
	for _, key := range []string{"status", "flow", "profile", "verificationUrl", "userCode", "next"} {
		if value, ok := result[key]; ok {
			public[key] = value
		}
	}
	public["qrDataURL"] = "data:image/png;base64," + base64.StdEncoding.EncodeToString(qrData)
	return public, nil
}

func (client FeishuClient) FinishUserAuth(ctx context.Context, root string) error {
	_, err := client.command(ctx, root, []string{"auth", "finish-user"}, nil)
	return err
}

func (client FeishuClient) Permissions(ctx context.Context, root string) (map[string]any, error) {
	return client.command(ctx, root, []string{"permissions"}, nil)
}

func (client FeishuClient) EnsureCurrentUserTarget(ctx context.Context, root string) error {
	_, err := client.command(ctx, root, []string{"auth", "ensure-current-user"}, nil)
	return err
}

func (client FeishuClient) command(ctx context.Context, root string, args []string, input []byte) (map[string]any, error) {
	script, node, err := validateFeishuWithNode(root, client.Node)
	if err != nil {
		return nil, err
	}
	data, err := run(ctx, root, node, append([]string{script}, args...), input)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if json.Unmarshal(data, &result) != nil {
		return nil, fmt.Errorf("飞书桥返回了无法识别的数据")
	}
	return result, nil
}

func readPrivateQR(filePath string) ([]byte, error) {
	if strings.ToLower(filepath.Ext(filePath)) != ".png" {
		return nil, fmt.Errorf("飞书认证二维码格式无效")
	}
	dataRoot := strings.TrimSpace(os.Getenv("FEISHU_BRIDGE_DATA_DIR"))
	if dataRoot == "" {
		home, _ := os.UserHomeDir()
		dataRoot = filepath.Join(home, ".config", "feishu-bridge")
	}
	privateRoot, err := filepath.Abs(dataRoot)
	if err != nil {
		return nil, fmt.Errorf("飞书私有数据目录无效")
	}
	privateRoot, err = filepath.EvalSymlinks(privateRoot)
	if err != nil {
		return nil, fmt.Errorf("飞书私有数据目录无效")
	}
	info, err := os.Lstat(filePath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 2*1024*1024 {
		return nil, fmt.Errorf("飞书认证二维码文件无效")
	}
	resolvedPath, err := filepath.EvalSymlinks(filePath)
	if err != nil {
		return nil, fmt.Errorf("飞书认证二维码文件无效")
	}
	relative, err := filepath.Rel(privateRoot, resolvedPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return nil, fmt.Errorf("飞书认证二维码路径越界")
	}
	data, err := os.ReadFile(filePath)
	if err != nil || len(data) < 8 || string(data[:8]) != "\x89PNG\r\n\x1a\n" {
		return nil, fmt.Errorf("飞书认证二维码内容无效")
	}
	return data, nil
}

func mutateTaskLink(ctx context.Context, root, configuredNode string, args []string, input []byte) (domain.FeishuTaskLink, error) {
	client, node, err := validateFeishuWithNode(root, configuredNode)
	if err != nil {
		return domain.FeishuTaskLink{}, err
	}
	data, err := run(ctx, root, node, append([]string{client}, args...), input)
	if err != nil {
		return domain.FeishuTaskLink{}, err
	}
	var response struct {
		Link domain.FeishuTaskLink `json:"link"`
	}
	if json.Unmarshal(data, &response) != nil || response.Link.TaskKey == "" {
		return domain.FeishuTaskLink{}, fmt.Errorf("飞书桥返回了无法识别的数据")
	}
	return response.Link, nil
}

func validateFeishu(root string) (string, string, error) {
	return validateFeishuWithNode(root, "")
}

func validateFeishuWithNode(root, configuredNode string) (string, string, error) {
	if strings.TrimSpace(root) == "" {
		return "", "", fmt.Errorf("安装包内未找到飞书桥服务")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", "", fmt.Errorf("飞书桥服务目录无效")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", fmt.Errorf("无法解析飞书桥服务目录")
	}
	client := filepath.Join(root, "scripts", "bridge-client.js")
	info, err = os.Lstat(client)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", "", fmt.Errorf("所选目录缺少安全的 scripts/bridge-client.js")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o022 != 0 {
		return "", "", fmt.Errorf("飞书桥客户端不可被组或其他用户写入")
	}
	nodeName := strings.TrimSpace(configuredNode)
	if nodeName == "" {
		nodeName = "node"
	}
	node, err := exec.LookPath(nodeName)
	if err != nil && runtime.GOOS == "windows" {
		if nodeName == "node" {
			node, err = exec.LookPath("node.exe")
		}
	}
	if err != nil {
		return "", "", fmt.Errorf("安装包内未找到 Node.js 24 LTS 运行时")
	}
	nodeInfo, err := os.Lstat(node)
	if err != nil || !nodeInfo.Mode().IsRegular() || nodeInfo.Mode()&os.ModeSymlink != 0 {
		return "", "", fmt.Errorf("飞书桥 Node.js 运行时无效")
	}
	return client, node, nil
}
