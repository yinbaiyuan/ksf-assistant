package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type DocumentContent struct {
	Format string `json:"format"`
	Text   string `json:"text"`
}
type DocumentRequest struct {
	ID                    string          `json:"id"`
	OperationID           string          `json:"operationId,omitempty"`
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
	ID                string         `json:"id"`
	OperationID       string         `json:"operationId,omitempty"`
	Status            string         `json:"status"`
	Action            string         `json:"action,omitempty"`
	Error             string         `json:"error,omitempty"`
	FailurePhase      string         `json:"failurePhase,omitempty"`
	Response          map[string]any `json:"response,omitempty"`
	Preflight         map[string]any `json:"preflight,omitempty"`
	Verification      map[string]any `json:"verification,omitempty"`
	VerificationState string         `json:"verificationState,omitempty"`
	Version           map[string]any `json:"version,omitempty"`
	Verified          bool           `json:"verified"`
	CompletedAt       time.Time      `json:"completedAt"`
}
type Docbox struct {
	root, dataRoot string
	repository     workRepository
}

func NewDocbox(dataRoot string) *Docbox {
	return &Docbox{root: filepath.Join(dataRoot, "logs"), dataRoot: dataRoot, repository: newWorkRepository(dataRoot, "docbox")}
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
	if request.OperationID != "" {
		operations := NewOperationService(box.dataRoot, NewCapabilityPolicyStore(box.dataRoot), nil)
		id, input := documentCapabilityInput(request)
		if _, err := boundQueueInput(operations, request.OperationID, id, input); err != nil {
			return err
		}
	}
	if err := box.repository.migrateLegacy(box.queuePath(), box.resultPath(), box.statePath()); err != nil {
		return err
	}
	capabilityID, input := documentCapabilityInput(request)
	definition, _ := CapabilityByID(capabilityID)
	conflictKey := workConflictKey(definition, input)
	return box.repository.enqueue(request.ID, request, conflictKey, "lark-cli", "standard", "never", request.CreatedAt)
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
	if (request.UpdateMode == "overwrite" || request.UpdateMode == "str_replace") && !operationIDPattern.MatchString(request.OperationID) {
		return errors.New("destructive_operation_requires_governance")
	}
	return nil
}

func (box *Docbox) Process(ctx context.Context, executor CapabilityExecutor, globalDryRun bool) error {
	for {
		processed, err := box.ProcessOne(ctx, executor, globalDryRun)
		if err != nil || !processed {
			return err
		}
	}
}

func (box *Docbox) ProcessOne(ctx context.Context, executor CapabilityExecutor, globalDryRun bool) (bool, error) {
	if err := box.repository.migrateLegacy(box.queuePath(), box.resultPath(), box.statePath()); err != nil {
		return false, err
	}
	item, found, err := box.repository.claim()
	if err != nil || !found {
		return found, err
	}
	return true, box.processClaimed(ctx, item, executor, globalDryRun)
}

func (box *Docbox) processClaimed(ctx context.Context, item WorkItemV3, executor CapabilityExecutor, globalDryRun bool) error {
	ctx = context.WithValue(ctx, executionWorkKey{}, executionWork{repository: box.repository, item: item})
	var request DocumentRequest
	result := DocumentResult{ID: item.ID, CompletedAt: time.Now().UTC()}
	if err := json.Unmarshal(item.Request, &request); err != nil {
		result.Status, result.Error = "invalid", "invalid_json"
	} else {
		result = box.executeGoverned(ctx, executor, request, globalDryRun)
	}
	if err := box.repository.finish(item, result, result.Error); err != nil {
		return err
	}
	target := "-"
	if request.Target != nil {
		target = request.Target.Kind + ":" + AuditFingerprint(request.Target.Value)
	}
	_ = NewAuditLog(box.dataRoot).Record("docbox_result", map[string]any{"id": result.ID, "action": result.Action, "status": result.Status, "target": target, "content": AuditContentDescriptor(request.Content.Text), "error": result.Error})
	return nil
}

func executeDocumentRequest(ctx context.Context, executor CapabilityExecutor, request DocumentRequest, globalDryRun bool) DocumentResult {
	return executeDocumentTransport(ctx, executor, request, globalDryRun)
}

func executeDocumentTransport(ctx context.Context, executor DocumentExecutionTransport, request DocumentRequest, globalDryRun bool) DocumentResult {
	result := DocumentResult{ID: request.ID, OperationID: request.OperationID, Action: request.Action, VerificationState: string(VerificationInconclusive), CompletedAt: time.Now().UTC()}
	if err := validateDocumentRequest(request); err != nil {
		result.Status, result.Error = "invalid", err.Error()
		return result
	}
	if globalDryRun || request.DryRun {
		result.Status = "dry_run"
		return result
	}
	if err := validateDocumentExecution(ctx, request); err != nil {
		result.Status, result.Error = "failed", err.Error()
		return result
	}
	if request.Action == "create_document" {
		if err := beforeRemoteWrite(ctx); err != nil {
			result.Status, result.Error = "failed", err.Error()
			return result
		}
		response, err := executor.DocumentCreate(ctx, request)
		if err != nil {
			result.Status, result.Error, result.FailurePhase = string(OperationOutcomeUnknown), safeCommandError(err.Error()), "write"
			return result
		}
		result.Status, result.Response = "completed", response
		return result
	}
	if err := checkExecutionBoundary(ctx); err != nil {
		result.Status, result.Error = "failed", err.Error()
		return result
	}
	preflight, err := executor.DocumentFetch(ctx, *request.Target)
	if err != nil {
		result.Status, result.Error = "failed", safeCommandError(err.Error())
		return result
	}
	result.Preflight = preflight
	if err := beforeRemoteWrite(ctx); err != nil {
		result.Status, result.Error = "failed", err.Error()
		return result
	}
	version, err := executor.DocumentVersion(ctx, *request.Target, preflight, request.ID)
	if err != nil {
		result.Status, result.Error, result.FailurePhase = string(OperationOutcomeUnknown), safeCommandError(err.Error()), "write"
		return result
	}
	result.Version = version
	if err := beforeRemoteWrite(ctx); err != nil {
		result.Status, result.Error, result.FailurePhase = string(OperationOutcomeUnknown), err.Error(), "write"
		return result
	}
	response, err := executor.DocumentUpdate(ctx, request)
	if err != nil {
		result.Status, result.Error, result.FailurePhase = string(OperationOutcomeUnknown), safeCommandError(err.Error()), "write"
		return result
	}
	result.Response = response
	verification, err := executor.DocumentFetch(ctx, *request.Target)
	if err != nil {
		result.Status, result.Error, result.FailurePhase = string(OperationOutcomeUnknown), safeCommandError(err.Error()), "verification"
		result.VerificationState = string(VerificationInconclusive)
		return result
	}
	result.Verification = verification
	result.VerificationState = string(VerificationInconclusive)
	result.Verified = false
	result.Status = "completed"
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
	if err := box.repository.migrateLegacy(box.queuePath(), box.resultPath(), box.statePath()); err != nil {
		return DocumentResult{}, false, err
	}
	var result DocumentResult
	found, err := box.repository.findResult(id, &result)
	return result, found, err
}
