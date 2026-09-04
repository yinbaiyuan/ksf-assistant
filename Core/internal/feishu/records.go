package feishu

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func QueueResults(dataRoot, kind, id string, limit int) ([]map[string]any, error) {
	if !contains([]string{"outbox", "docbox", "actionbox"}, kind) {
		return nil, errors.New("unsupported result kind")
	}
	values, err := readPublicJSONL(filepath.Join(dataRoot, "logs", kind+"-results.jsonl"), limit, func(value map[string]any) bool { return value["id"] == id })
	return values, err
}

func RecentRecords(dataRoot, kind string, limit int) ([]map[string]any, error) {
	if kind == "tasks" {
		kind = "actionbox"
	}
	path := ""
	switch kind {
	case "outbox", "docbox", "actionbox":
		path = filepath.Join(dataRoot, "logs", kind+"-results.jsonl")
	case "messages":
		path = filepath.Join(dataRoot, "logs", "messages.jsonl")
	case "audit":
		files, err := filepath.Glob(filepath.Join(dataRoot, "logs", "audit", "*.md"))
		if err != nil {
			return nil, err
		}
		sort.Sort(sort.Reverse(sort.StringSlice(files)))
		result := []map[string]any{}
		for _, file := range files {
			info, err := os.Stat(file)
			if err == nil {
				result = append(result, map[string]any{"file": filepath.Base(file), "sizeBytes": info.Size(), "updatedAt": info.ModTime().UTC().Format("2006-01-02T15:04:05Z")})
			}
			if len(result) >= limit {
				break
			}
		}
		return result, nil
	default:
		return nil, errors.New("unsupported recent kind")
	}
	return readPublicJSONL(path, limit, nil)
}

func readPublicJSONL(path string, limit int, accept func(map[string]any) bool) ([]map[string]any, error) {
	if limit < 1 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return []map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	values := []map[string]any{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var value map[string]any
		if json.Unmarshal(scanner.Bytes(), &value) == nil && (accept == nil || accept(value)) {
			values = append(values, redactPublicMap(value))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(values) > limit {
		values = values[len(values)-limit:]
	}
	for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
		values[i], values[j] = values[j], values[i]
	}
	return values, nil
}

var privateIdentifier = regexp.MustCompile(`^(?:ou|oc|om|od)_[A-Za-z0-9_-]+$|^(?:docx|wikcn|bascn|shtcn|obcn|tbl|rec)[A-Za-z0-9_-]{8,}$`)

func redactPublicMap(value map[string]any) map[string]any {
	result := map[string]any{}
	for key, item := range value {
		result[key] = redactPublic(item, key)
	}
	return result
}

func PublicResult(value any) any {
	payload, err := json.Marshal(value)
	if err != nil {
		return map[string]any{"status": "error", "error": "result_encoding_failed"}
	}
	var decoded any
	if json.Unmarshal(payload, &decoded) != nil {
		return map[string]any{"status": "error", "error": "result_encoding_failed"}
	}
	return redactPublic(decoded, "")
}
func redactPublic(value any, key string) any {
	switch item := value.(type) {
	case map[string]any:
		return redactPublicMap(item)
	case []any:
		result := make([]any, len(item))
		for i, child := range item {
			result[i] = redactPublic(child, key)
		}
		return result
	case string:
		lower := strings.ToLower(key)
		if privateIdentifier.MatchString(item) || strings.Contains(lower, "target") && (strings.Contains(item, "_") || len(item) > 16) {
			return AuditFingerprint(item)
		}
		if auditSecretPattern.MatchString(item) {
			return "[REDACTED]"
		}
		return item
	default:
		return item
	}
}
