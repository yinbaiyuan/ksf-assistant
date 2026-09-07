package usercommand

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type parsed struct {
	spec   specification
	flags  map[string]string
	path   []string
	local  bool
	values map[string][]string
}

func safeSchema(value string) bool {
	if len(value) > 160 || value == "" {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || strings.ContainsRune("._-", char)) {
			return false
		}
	}
	return true
}

func Evaluate(command Command) (Review, error) {
	var review Review
	if command.Version != Version {
		return review, errors.New("user_command_version_mismatch")
	}
	encoded, err := json.Marshal(command)
	if err != nil || len(encoded) > MaxRequestBytes {
		return review, errors.New("user_command_too_large")
	}
	parsed, err := parse(command.Args)
	if err != nil {
		return review, err
	}
	if parsed.local {
		if len(command.Stdin) > 0 || len(command.Files) > 0 || command.ArtifactPlan != nil {
			return review, errors.New("user_command_unexpected_input")
		}
		review.Identity, review.Risk, review.Action = "local", "read", "本地命令说明"
	} else {
		if err := ValidateIdentity(command.Identity, command.Identity); err != nil {
			return review, err
		}
		if parsed.flags["as"] == "user" && command.Identity.UserID == "" || parsed.flags["as"] == "bot" && command.Identity.UserID != "" {
			return review, errors.New("user_command_identity_mismatch")
		}
		if err := validateFrozen(command, parsed); err != nil {
			return review, err
		}
		review.Identity, review.Risk, review.Action = parsed.flags["as"], parsed.spec.risk, parsed.spec.action
		review.Effects = commandEffects(command, parsed)
		for _, effect := range review.Effects {
			if !contains(review.CapabilityIDs, effect.CapabilityID) {
				review.CapabilityIDs = append(review.CapabilityIDs, effect.CapabilityID)
			}
			if riskRank(effect.Risk) > riskRank(review.Risk) {
				review.Risk = effect.Risk
			}
		}
		if len(review.CapabilityIDs) > 0 {
			review.CapabilityID = review.CapabilityIDs[0]
		}
		switch review.Risk {
		case "read", "write", "high-impact-write", "remote-operation", "destructive":
		default:
			return Review{}, ErrUnsupported
		}
		review.NeedsApproval = review.Identity == "user" && review.Risk != "read"
		var preview string
		review.Target, review.Details, preview = describeParts(command, parsed)
		title := review.Action
		for _, entry := range legacySpecifications {
			if entry.path == parsed.spec.path {
				title = entry.action
				break
			}
		}
		if title == review.Action && parsed.path[0] != "api" {
			title = "执行飞书操作 · " + parsed.spec.path
		}
		label := "执行"
		for _, verb := range []string{"发送", "回复", "删除", "撤回", "更新", "修改", "创建", "添加", "移除", "上传", "移动", "复制", "提交", "取消", "退出", "转发"} {
			if strings.HasPrefix(title, verb) {
				label = verb
				break
			}
		}
		review.Preview = &ApprovalPreview{Title: title, Content: preview, ConfirmLabel: label, Destructive: review.Risk == "destructive"}
		if review.Target == "" {
			return Review{}, errors.New("user_command_target_unresolved")
		}
	}
	digest := sha256.Sum256(append([]byte(PolicyVersion+"\n"+ManifestDigest()+"\n"), encoded...))
	review.Digest = hex.EncodeToString(digest[:])
	return review, nil
}

func describe(command Command, parsed parsed) (string, string) {
	target, details, _ := describeParts(command, parsed)
	return target, details
}

func describeParts(command Command, parsed parsed) (string, string, string) {
	var targets, details, preview []string
	if parsed.path[0] == "api" {
		targets = append(targets, parsed.path[2])
	}
	keys := make([]string, 0, len(parsed.flags))
	for key := range parsed.flags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		flag, _ := descriptorFlag(*parsed.spec.descriptor, key)
		if flag.Role == "control" || flag.Role == "output-format" {
			continue
		}
		values := make([]string, 0, len(parsed.values[key]))
		for _, original := range parsed.values[key] {
			values = append(values, resolvedText(command, flag, original))
		}
		line := key + ": " + strings.Join(values, " | ")
		details = append(details, line)
		// Do not reparse flattened text: user content can itself look like metadata.
		// Targets remain visible separately. JSON bodies and every non-target
		// parameter stay in the preview, including permission/scope changes.
		if !includes(parsed.spec.targets, key) || includes("data params", key) {
			label := map[string]string{"text": "正文", "content": "内容", "data": "修改参数", "params": "作用范围", "command": "修改方式", "doc-format": "格式"}[key]
			if label == "" {
				label = key
			}
			preview = append(preview, label+"：\n"+strings.Join(values, " | "))
		}
		if repeatable(flag) || strings.HasSuffix(key, "-ids") {
			count := 0
			for _, value := range values {
				if includes("string_slice int_array", flag.Type) || strings.HasSuffix(key, "-ids") && !repeatable(flag) {
					entries, err := csv.NewReader(strings.NewReader(value)).Read()
					if err == nil {
						count += len(entries)
					}
				} else {
					count++
				}
			}
			details = append(details, fmt.Sprintf("%s 数量: %d", key, count))
		}
		if includes("data params", key) {
			for _, value := range values {
				var structured any
				if json.Unmarshal([]byte(value), &structured) == nil {
					details = append(details, arrayCounts(structured, key)...)
				}
			}
		}
		if includes(parsed.spec.targets, key) {
			targets = append(targets, line)
		}
		if key == "data" || key == "params" {
			targets = append(targets, line)
		}
	}
	for _, file := range command.Files {
		digest := sha256.Sum256(file.Data)
		name := file.DisplayName
		if name == "" {
			name = file.Name
		}
		details = append(details, fmt.Sprintf("附件 %s: %s bytes; SHA256 %x", name, strconv.Itoa(len(file.Data)), digest))
	}
	if len(command.Files) > 0 {
		details = append(details, fmt.Sprintf("附件数量: %d", len(command.Files)))
	}
	if len(targets) == 0 && parsed.spec.path == "docs +create" {
		targets = append(targets, "当前用户的默认文档位置")
	}
	if len(targets) == 0 && strings.HasPrefix(parsed.spec.path, "calendar +") {
		targets = append(targets, "当前身份的主日历")
	}
	if len(targets) == 0 && parsed.spec.risk == "read" {
		targets = append(targets, "当前授权范围（过滤条件见详情）")
	}
	if len(targets) == 0 {
		targets = append(targets, "命令 "+parsed.spec.path+"（明确参数及当前授权范围见详情）")
	}
	// Artifact effects below must stay visible.
	artifactStart := len(details)
	if parsed.spec.descriptor.Artifacts {
		details = append(details, "本地输出：执行成功后按冻结计划发布；冲突拒绝、不覆盖，失败或未知结果不发布。")
	}
	if plan := command.ArtifactPlan; plan != nil {
		details = append(details, "产物根目录: "+plan.Root)
		if plan.Directory != "" {
			details = append(details, "默认下载目标目录: "+plan.Directory)
		}
		for _, target := range plan.Targets {
			details = append(details, "明确下载目标: "+target.Path)
		}
	}
	preview = append(preview, details[artifactStart:]...)
	return strings.Join(targets, "\n"), strings.Join(details, "\n"), strings.Join(preview, "\n\n")
}

var dynamicDocumentResource = regexp.MustCompile(`(?is)!\s*\[|<\s*[/!?a-z]`)

func arrayCounts(value any, path string) []string {
	var result []string
	switch value := value.(type) {
	case []any:
		result = append(result, fmt.Sprintf("%s 数量: %d", path, len(value)))
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			result = append(result, arrayCounts(value[key], path+"."+key)...)
		}
	}
	return result
}

func RequiresCLIConfirmation(command Command) bool {
	parsed, err := parse(command.Args)
	return err == nil && parsed.spec.yes && !parsed.local
}
