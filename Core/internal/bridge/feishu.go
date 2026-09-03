package bridge

import (
	"context"
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

type FeishuClient struct{}

func (FeishuClient) Inspect(ctx context.Context, root string) (domain.FeishuSnapshot, error) {
	client, node, err := validateFeishu(root)
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
	return domain.FeishuSnapshot{Availability: availability, Message: message, TargetAliases: aliases, TaskLinkProtocolVersion: protocol.Version, TaskLinkReady: protocol.Readiness.Ready, ReadinessBlockers: protocol.Readiness.Blockers, Links: links.Links}, nil
}

func (FeishuClient) CreateTaskLink(ctx context.Context, root, threadID, title, projectName, targetAlias string) (domain.FeishuTaskLink, error) {
	payload, _ := json.Marshal(map[string]string{"threadId": threadID, "title": title, "projectName": projectName, "targetAlias": targetAlias})
	return mutateTaskLink(ctx, root, []string{"task-link", "create", "--payload-file", "-"}, payload)
}

func (FeishuClient) InterruptTaskLink(ctx context.Context, root, threadID string) (domain.FeishuTaskLink, error) {
	return mutateTaskLink(ctx, root, []string{"task-link", "interrupt", "--task-key", domain.PublicTaskKey(threadID)}, nil)
}

func (FeishuClient) ReleaseTaskLink(ctx context.Context, root, threadID string) (domain.FeishuTaskLink, error) {
	return mutateTaskLink(ctx, root, []string{"task-link", "release", "--task-key", domain.PublicTaskKey(threadID)}, nil)
}

func (FeishuClient) SendTest(ctx context.Context, root, targetAlias string) error {
	client, node, err := validateFeishu(root)
	if err != nil {
		return err
	}
	_, err = run(ctx, root, node, []string{client, "send", "--target", targetAlias, "--format", "text", "--content-file", "-", "--source", "codex-usage-bar", "--reason", "用户在 Codex Usage Bar 中手动发送连接测试消息"}, []byte("Codex Usage Bar 飞书桥连接测试成功"))
	return err
}

func mutateTaskLink(ctx context.Context, root string, args []string, input []byte) (domain.FeishuTaskLink, error) {
	client, node, err := validateFeishu(root)
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
	if strings.TrimSpace(root) == "" {
		return "", "", fmt.Errorf("未配置飞书桥目录")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", "", fmt.Errorf("所选目录不是飞书桥工程目录")
	}
	client := filepath.Join(root, "scripts", "bridge-client.js")
	info, err = os.Lstat(client)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", "", fmt.Errorf("所选目录缺少安全的 scripts/bridge-client.js")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o022 != 0 {
		return "", "", fmt.Errorf("飞书桥客户端不可被组或其他用户写入")
	}
	node, err := exec.LookPath("node")
	if err != nil && runtime.GOOS == "windows" {
		node, err = exec.LookPath("node.exe")
	}
	if err != nil {
		return "", "", fmt.Errorf("未找到 Node.js，无法运行飞书桥客户端")
	}
	return client, node, nil
}
