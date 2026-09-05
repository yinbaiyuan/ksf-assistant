package feishu

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	diagnosticSegmentBytes = int64(1024 * 1024)
	diagnosticSegmentCount = 5
	diagnosticRetentionAge = 7 * 24 * time.Hour
)

type SupervisorDiagnostic struct {
	At          time.Time `json:"at"`
	Code        string    `json:"code"`
	Level       string    `json:"level,omitempty"`
	Component   string    `json:"component,omitempty"`
	Category    string    `json:"category,omitempty"`
	SafeSummary string    `json:"safeSummary"`
	Length      int       `json:"length,omitempty"`
	Fingerprint string    `json:"fingerprint,omitempty"`
}

type DiagnosticLog struct{ dataRoot string }

func NewDiagnosticLog(dataRoot string) DiagnosticLog { return DiagnosticLog{dataRoot: dataRoot} }

func (log DiagnosticLog) Record(record SupervisorDiagnostic) error {
	if strings.TrimSpace(log.dataRoot) == "" {
		return nil
	}
	if record.At.IsZero() {
		record.At = time.Now().UTC()
	}
	record.Code = diagnosticToken(record.Code, "diagnostic")
	record.Level = diagnosticToken(record.Level, "")
	record.Component = diagnosticToken(record.Component, "")
	record.Category = diagnosticToken(record.Category, "")
	if len(record.SafeSummary) > 240 {
		record.SafeSummary = record.SafeSummary[:240]
	}
	root := filepath.Join(log.dataRoot, "logs", "diagnostics")
	if err := ensurePrivateDirectory(root); err != nil {
		return err
	}
	return withProcessFileLock(filepath.Join(root, ".diagnostics.lock"), func() error {
		if err := rotateDiagnosticFiles(root, record.At); err != nil {
			return err
		}
		return appendPrivateJSONLUnlocked(filepath.Join(root, "feishu-child-0.jsonl"), record)
	})
}

func rotateDiagnosticFiles(root string, now time.Time) error {
	for index := 0; index < diagnosticSegmentCount; index++ {
		path := filepath.Join(root, fmt.Sprintf("feishu-child-%d.jsonl", index))
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("unsafe diagnostic log path")
		}
		if now.Sub(info.ModTime()) >= diagnosticRetentionAge {
			if err := os.Remove(path); err != nil {
				return err
			}
		}
	}
	current := filepath.Join(root, "feishu-child-0.jsonl")
	info, err := os.Lstat(current)
	if errors.Is(err, os.ErrNotExist) || err == nil && info.Size() < diagnosticSegmentBytes {
		return nil
	}
	if err != nil {
		return err
	}
	oldest := filepath.Join(root, fmt.Sprintf("feishu-child-%d.jsonl", diagnosticSegmentCount-1))
	if err := removePrivateRegular(oldest); err != nil {
		return err
	}
	for index := diagnosticSegmentCount - 2; index >= 0; index-- {
		source := filepath.Join(root, fmt.Sprintf("feishu-child-%d.jsonl", index))
		if _, err := os.Lstat(source); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		if err := os.Rename(source, filepath.Join(root, fmt.Sprintf("feishu-child-%d.jsonl", index+1))); err != nil {
			return err
		}
	}
	return nil
}

var diagnosticTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,80}$`)

func diagnosticToken(value, fallback string) string {
	value = strings.TrimSpace(value)
	if !diagnosticTokenPattern.MatchString(value) {
		return fallback
	}
	return value
}

func ParseSupervisorDiagnostic(line []byte) SupervisorDiagnostic {
	now := time.Now().UTC()
	var value map[string]any
	if json.Unmarshal(line, &value) == nil {
		code := diagnosticToken(fmt.Sprint(value["code"]), "child_structured_stderr")
		level := diagnosticToken(fmt.Sprint(value["level"]), "")
		component := diagnosticToken(fmt.Sprint(value["component"]), "")
		category := diagnosticToken(fmt.Sprint(value["category"]), "")
		parts := []string{code}
		for _, item := range []string{component, category, level} {
			if item != "" {
				parts = append(parts, item)
			}
		}
		return SupervisorDiagnostic{At: now, Code: code, Level: level, Component: component, Category: category, SafeSummary: strings.Join(parts, " · "), Length: len(line), Fingerprint: AuditFingerprint(string(line))}
	}
	code := classifyDiagnosticBytes(line)
	return SupervisorDiagnostic{At: now, Code: code, SafeSummary: fmt.Sprintf("%s · length=%d · %s", code, len(line), AuditFingerprint(string(line))), Length: len(line), Fingerprint: AuditFingerprint(string(line))}
}

func classifyDiagnosticBytes(line []byte) string {
	value := strings.ToLower(string(line))
	for _, item := range []struct{ contains, code string }{
		{"credential", "credential_unavailable"},
		{"permission", "permission_failure"},
		{"private ipc", "private_ipc_failure"},
		{"audit", "audit_failure"},
		{"migration", "migration_failure"},
		{"already running", "duplicate_instance"},
	} {
		if strings.Contains(value, item.contains) {
			return item.code
		}
	}
	return "child_unstructured_stderr"
}
