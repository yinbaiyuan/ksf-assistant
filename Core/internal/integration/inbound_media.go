package integration

import (
	"fmt"
	"ksfassistant/core/internal/feishutypes"
	"strings"
)

type InboundResource = feishutypes.InboundResource
type InboundAsset = feishutypes.InboundAsset
type StagedInboundMessage = feishutypes.StagedInboundMessage
type StageAssets = feishutypes.StageAssets

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
