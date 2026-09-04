package feishu

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type AuditLog struct{ dataRoot string }

func NewAuditLog(dataRoot string) AuditLog { return AuditLog{dataRoot: dataRoot} }

func (audit AuditLog) Record(direction string, fields map[string]any) error {
	now := time.Now().UTC()
	record := map[string]any{"direction": direction, "at": now.Format(time.RFC3339Nano)}
	for key, value := range fields {
		record[key] = redactAuditValue(value)
	}
	if err := appendPrivateJSONL(filepath.Join(audit.dataRoot, "logs", "messages.jsonl"), record); err != nil {
		return err
	}
	date := now.In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006-01-02")
	path := filepath.Join(audit.dataRoot, "logs", "audit", date+".md")
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := []string{fmt.Sprintf("## %s %s", direction, now.In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("15:04:05")), ""}
	for _, key := range keys {
		lines = append(lines, fmt.Sprintf("- %s：%v", key, redactAuditValue(fields[key])))
	}
	lines = append(lines, "")
	return withProcessFileLock(path+".lock", func() error {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			header := fmt.Sprintf("---\ntype: log\nstatus: active\ndomain: feishu-codex-bridge\nproject: feishu-codex-bridge\nsummary: 飞书桥接 Codex 的当日消息、任务和执行结果审计记录。\ncreated: %s\nupdated: %s\n---\n\n# 飞书-Codex 审计日志 %s\n\n", date, date, date)
			if err := ensurePrivateDirectory(filepath.Dir(path)); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(header), 0o600); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = file.WriteString(strings.Join(lines, "\n") + "\n")
		return err
	})
}

func AuditFingerprint(value string) string {
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])[:20]
}
func AuditContentDescriptor(value string) string {
	return fmt.Sprintf("length=%d, %s", len([]rune(value)), AuditFingerprint(value))
}
func AuditTargetDescriptor(target MessageTarget) string {
	return fmt.Sprintf("%s:%s", target.Type, AuditFingerprint(target.ID))
}

var auditSecretPattern = regexp.MustCompile(`(?i)(authorization\s*:\s*bearer\s+\S+|(?:api[_ -]?key|access[_ -]?token|password|secret)\s*[:=：]\s*\S+|\bsk-[A-Za-z0-9_-]{16,})`)

func redactAuditValue(value any) any {
	switch item := value.(type) {
	case string:
		text := auditSecretPattern.ReplaceAllString(item, "[REDACTED]")
		if len([]rune(text)) > 500 {
			text = string([]rune(text)[:500]) + "…"
		}
		return text
	case []string:
		result := make([]any, len(item))
		for index, child := range item {
			result[index] = redactAuditValue(child)
		}
		return result
	default:
		return value
	}
}
