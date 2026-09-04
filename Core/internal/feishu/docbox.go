package feishu

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type DocumentContent struct {
	Format string `json:"format"`
	Text   string `json:"text"`
}
type DocumentRequest struct {
	ID                    string          `json:"id"`
	Type                  string          `json:"type"`
	Action                string          `json:"action"`
	Identity              string          `json:"identity"`
	Target                *DocumentTarget `json:"target,omitempty"`
	Content               DocumentContent `json:"content"`
	Instruction           string          `json:"instruction"`
	ExplicitAuthorization bool            `json:"explicitAuthorization"`
	DryRun                bool            `json:"dryRun,omitempty"`
	Source                string          `json:"source"`
	Reason                string          `json:"reason,omitempty"`
	Trace                 map[string]any  `json:"trace,omitempty"`
	CreatedAt             time.Time       `json:"createdAt"`
	VersionPolicy         string          `json:"versionPolicy,omitempty"`
	UpdateMode            string          `json:"updateMode,omitempty"`
	NewTitle              string          `json:"newTitle,omitempty"`
	Selection             map[string]any  `json:"selection,omitempty"`
}
type DocumentResult struct {
	ID           string         `json:"id"`
	Status       string         `json:"status"`
	Action       string         `json:"action,omitempty"`
	Error        string         `json:"error,omitempty"`
	Response     map[string]any `json:"response,omitempty"`
	Preflight    map[string]any `json:"preflight,omitempty"`
	Verification map[string]any `json:"verification,omitempty"`
	Version      map[string]any `json:"version,omitempty"`
	Verified     bool           `json:"verified"`
	CompletedAt  time.Time      `json:"completedAt"`
}
type Docbox struct {
	root, dataRoot string
	mu             sync.Mutex
}

func NewDocbox(dataRoot string) *Docbox {
	return &Docbox{root: filepath.Join(dataRoot, "logs"), dataRoot: dataRoot}
}
func (box *Docbox) queuePath() string  { return filepath.Join(box.root, "docbox.jsonl") }
func (box *Docbox) resultPath() string { return filepath.Join(box.root, "docbox-results.jsonl") }
func (box *Docbox) statePath() string  { return filepath.Join(box.root, "docbox-state.json") }
func (box *Docbox) lockPath() string   { return filepath.Join(box.root, ".docbox-process.lock") }
func NewDocboxID() (string, error)     { return newQueueID("DOC") }

func (box *Docbox) Submit(request DocumentRequest) error {
	if err := validateDocumentRequest(request); err != nil {
		return err
	}
	return appendPrivateJSONL(box.queuePath(), request)
}
func validateDocumentRequest(request DocumentRequest) error {
	if request.ID == "" {
		return errors.New("missing_id")
	}
	if request.Type != "document_task" {
		return errors.New("unsupported_type")
	}
	if request.Action != "create_document" && request.Action != "update_document" {
		return errors.New("unsupported_action")
	}
	expected := "user"
	if request.Target != nil && (request.Target.Kind == "wiki_url" || request.Target.Kind == "wiki_token") {
		expected = "bot"
	}
	if request.Identity != expected {
		return errors.New("unsupported_identity")
	}
	if !request.ExplicitAuthorization {
		return errors.New("explicit_authorization_required")
	}
	if request.Source == "" || request.Instruction == "" {
		return errors.New("missing_source_or_instruction")
	}
	if request.Content.Format != "markdown" && request.Content.Format != "text" {
		return errors.New("unsupported_content_format")
	}
	if strings.TrimSpace(request.Content.Text) == "" {
		return errors.New("missing_content_text")
	}
	if request.Action == "update_document" && request.Target == nil {
		return errors.New("missing_target")
	}
	if request.Target != nil && (!contains([]string{"url", "docx_token", "wiki_url", "wiki_token", "folder_token"}, request.Target.Kind) || request.Target.Value == "") {
		return errors.New("invalid_target")
	}
	if request.VersionPolicy != "" && request.VersionPolicy != "official_before_update" {
		return errors.New("unsupported_version_policy")
	}
	if request.UpdateMode != "" && !contains([]string{"append", "overwrite", "str_replace"}, request.UpdateMode) {
		return errors.New("unsupported_update_mode")
	}
	return nil
}

func (box *Docbox) Process(ctx context.Context, executor CapabilityExecutor, globalDryRun bool) error {
	box.mu.Lock()
	defer box.mu.Unlock()
	return withProcessFileLock(box.lockPath(), func() error { return box.processLocked(ctx, executor, globalDryRun) })
}
func (box *Docbox) processLocked(ctx context.Context, executor CapabilityExecutor, globalDryRun bool) error {
	state := defaultQueueState()
	if missing, err := readPrivateJSON(box.statePath(), &state); err != nil && !missing {
		return err
	}
	normalizeQueueState(&state)
	file, err := os.Open(box.queuePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 65536), 4*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		if line <= state.ProcessedLineCount {
			continue
		}
		var request DocumentRequest
		result := DocumentResult{CompletedAt: time.Now().UTC()}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			result.ID, result.Status, result.Error = "invalid-line", "invalid", "invalid_json"
		} else if _, done := state.ProcessedIDs[request.ID]; done {
			result.ID, result.Status, result.Error = request.ID, "duplicate", "duplicate_id"
		} else {
			result = executeDocumentRequest(ctx, executor, request, globalDryRun)
		}
		if err := appendPrivateJSONL(box.resultPath(), result); err != nil {
			return err
		}
		target := "-"
		if request.Target != nil {
			target = request.Target.Kind + ":" + AuditFingerprint(request.Target.Value)
		}
		_ = NewAuditLog(box.dataRoot).Record("docbox_result", map[string]any{"id": result.ID, "action": result.Action, "status": result.Status, "target": target, "content": AuditContentDescriptor(request.Content.Text), "error": result.Error})
		state.ProcessedLineCount = line
		if result.ID != "" {
			state.ProcessedIDs[result.ID] = result.Status
			trimProcessedIDs(state.ProcessedIDs, 5000)
		}
		state.LastProcessedAt = result.CompletedAt.Format(time.RFC3339)
		state.LastError = result.Error
		if err := writePrivateJSON(box.statePath(), state); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func executeDocumentRequest(ctx context.Context, executor CapabilityExecutor, request DocumentRequest, globalDryRun bool) DocumentResult {
	result := DocumentResult{ID: request.ID, Action: request.Action, CompletedAt: time.Now().UTC()}
	if err := validateDocumentRequest(request); err != nil {
		result.Status, result.Error = "invalid", err.Error()
		return result
	}
	if globalDryRun || request.DryRun {
		result.Status = "dry_run"
		return result
	}
	if request.Action == "create_document" {
		response, err := executor.documentCreate(ctx, request)
		if err != nil {
			result.Status, result.Error = "failed", safeCommandError(err.Error())
			return result
		}
		result.Status, result.Response = "completed", response
		return result
	}
	preflight, err := executor.documentFetch(ctx, *request.Target)
	if err != nil {
		result.Status, result.Error = "failed", safeCommandError(err.Error())
		return result
	}
	result.Preflight = preflight
	version, err := executor.documentVersion(ctx, *request.Target, preflight, request.ID)
	if err != nil {
		result.Status, result.Error = "failed", safeCommandError(err.Error())
		return result
	}
	result.Version = version
	response, err := executor.documentUpdate(ctx, request)
	if err != nil {
		result.Status, result.Error = "failed", safeCommandError(err.Error())
		return result
	}
	result.Response = response
	verification, err := executor.documentFetch(ctx, *request.Target)
	if err != nil {
		result.Status, result.Error = "failed", safeCommandError(err.Error())
		return result
	}
	result.Verification, result.Verified, result.Status = verification, true, "completed"
	return result
}

func (runner CapabilityExecutor) documentCreate(ctx context.Context, request DocumentRequest) (map[string]any, error) {
	definition := CapabilityDefinition{ID: "docbox.create", Domain: "docs", Risk: "write", Identity: request.Identity, Command: []string{"docs", "+create"}}
	args := []string{"docs", "+create"}
	if request.Target != nil && request.Target.Kind == "folder_token" {
		args = append(args, "--parent-token", request.Target.Value)
	}
	args = append(args, "--doc-format", request.Content.Format, "--content", "-")
	return runner.run(ctx, definition, args, []byte(request.Content.Text), nil, 3*time.Minute)
}
func (runner CapabilityExecutor) documentFetch(ctx context.Context, target DocumentTarget) (map[string]any, error) {
	definition := CapabilityDefinition{ID: "docbox.fetch", Domain: "docs", Risk: "read", Identity: documentIdentity(target), Command: []string{"docs", "+fetch"}}
	args := []string{"docs", "+fetch", "--api-version", "v2", "--doc", target.Value, "--scope", "full", "--detail", "simple", "--doc-format", "markdown"}
	return runner.run(ctx, definition, args, nil, nil, 60*time.Second)
}
func (runner CapabilityExecutor) documentUpdate(ctx context.Context, request DocumentRequest) (map[string]any, error) {
	mode := request.UpdateMode
	if mode == "" {
		mode = "append"
	}
	if request.NewTitle != "" {
		return nil, errors.New("document title updates are not supported by lark-cli 1.0.92")
	}
	definition := CapabilityDefinition{ID: "docbox.update", Domain: "docs", Risk: "write", Identity: request.Identity, Command: []string{"docs", "+update"}}
	args := []string{"docs", "+update", "--api-version", "v2", "--doc", request.Target.Value, "--command", mode, "--content", "-", "--doc-format", request.Content.Format}
	if mode == "str_replace" {
		pattern := fmt.Sprint(request.Selection["withEllipsis"])
		if pattern == "" || pattern == "<nil>" {
			return nil, errors.New("str_replace requires an exact replacement pattern")
		}
		args = append(args, "--pattern", pattern)
	}
	return runner.run(ctx, definition, args, []byte(request.Content.Text), nil, 3*time.Minute)
}
func (runner CapabilityExecutor) documentVersion(ctx context.Context, target DocumentTarget, preflight map[string]any, name string) (map[string]any, error) {
	token := documentToken(target, preflight)
	if token == "" {
		return nil, errors.New("document_token_not_found_for_version")
	}
	definition := CapabilityDefinition{ID: "docbox.version", Domain: "drive", Risk: "write", Identity: documentIdentity(target), Transport: "raw", Command: []string{"api", "POST"}, APIPath: "/open-apis/drive/v1/files/{file-token}/versions", Flags: map[string]CapabilityField{"file-token": {Type: "string", Required: true, Path: true}, "data": {Type: "json", Body: ""}}}
	args := []string{"api", "POST", "/open-apis/drive/v1/files/" + token + "/versions", "--data", "__PRIVATE_VERSION__"}
	files := map[string][]byte{"__PRIVATE_VERSION__": []byte(`{"name":"` + name + `","obj_type":"docx"}`)}
	return runner.run(ctx, definition, args, nil, files, 60*time.Second)
}
func documentIdentity(target DocumentTarget) string {
	if target.Kind == "wiki_url" || target.Kind == "wiki_token" {
		return "bot"
	}
	return "user"
}
func documentToken(target DocumentTarget, response map[string]any) string {
	if target.Kind == "docx_token" {
		return target.Value
	}
	if match := regexp.MustCompile(`/docx/([A-Za-z0-9]+)`).FindStringSubmatch(target.Value); len(match) > 1 {
		return match[1]
	}
	for _, key := range []string{"file_token", "doc_token", "obj_token", "token"} {
		if value, ok := recursiveKey(response, key); ok && fmt.Sprint(value) != "" {
			return fmt.Sprint(value)
		}
	}
	return ""
}

func (box *Docbox) FindResult(id string) (DocumentResult, bool, error) {
	var result DocumentResult
	found := false
	err := withProcessFileLock(box.resultPath()+".lock", func() error {
		file, err := os.Open(box.resultPath())
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		defer file.Close()
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			var item DocumentResult
			if json.Unmarshal(scanner.Bytes(), &item) == nil && item.ID == id {
				result, found = item, true
			}
		}
		return scanner.Err()
	})
	return result, found, err
}
