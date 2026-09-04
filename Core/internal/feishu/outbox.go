package feishu

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type MessageSender interface {
	Send(context.Context, MessageTarget, string, string, string) (string, error)
}

type OutboxRequest struct {
	ID                    string         `json:"id"`
	Type                  string         `json:"type"`
	Target                MessageTarget  `json:"target"`
	Text                  string         `json:"text,omitempty"`
	FilePath              string         `json:"filePath,omitempty"`
	ExplicitAuthorization bool           `json:"explicitAuthorization"`
	DryRun                bool           `json:"dryRun,omitempty"`
	Source                string         `json:"source"`
	Reason                string         `json:"reason,omitempty"`
	Trace                 map[string]any `json:"trace,omitempty"`
	CreatedAt             time.Time      `json:"createdAt"`
}

type OutboxResult struct {
	ID          string         `json:"id"`
	Status      string         `json:"status"`
	Target      MessageTarget  `json:"target,omitempty"`
	MessageIDs  []string       `json:"messageIds,omitempty"`
	PartCount   int            `json:"partCount,omitempty"`
	DryRun      bool           `json:"dryRun,omitempty"`
	Source      string         `json:"source,omitempty"`
	Trace       map[string]any `json:"trace,omitempty"`
	Error       string         `json:"error,omitempty"`
	CompletedAt time.Time      `json:"completedAt"`
}

type Outbox struct {
	root, dataRoot string
	mu             sync.Mutex
}

func NewOutbox(dataRoot string) *Outbox {
	return &Outbox{root: filepath.Join(dataRoot, "logs"), dataRoot: dataRoot}
}
func (box *Outbox) queuePath() string       { return filepath.Join(box.root, "outbox.jsonl") }
func (box *Outbox) resultPath() string      { return filepath.Join(box.root, "outbox-results.jsonl") }
func (box *Outbox) statePath() string       { return filepath.Join(box.root, "outbox-state.json") }
func (box *Outbox) processLockPath() string { return filepath.Join(box.root, ".outbox-process.lock") }

func (box *Outbox) Submit(request OutboxRequest) error {
	if err := box.validate(request); err != nil {
		return err
	}
	return appendPrivateJSONL(box.queuePath(), request)
}

func (box *Outbox) validate(request OutboxRequest) error {
	if request.ID == "" {
		return errors.New("missing_id")
	}
	if !contains([]string{"text", "markdown", "card", "image", "file"}, request.Type) {
		return errors.New("unsupported_type")
	}
	if request.Target.Type != "chat_id" && request.Target.Type != "open_id" {
		return errors.New("unsupported_target_type")
	}
	if request.Target.ID == "" {
		return errors.New("missing_target_id")
	}
	if !request.ExplicitAuthorization {
		return errors.New("explicit_authorization_required")
	}
	if request.Source == "" {
		return errors.New("missing_source")
	}
	if contains([]string{"text", "markdown", "card"}, request.Type) && request.Text == "" {
		return errors.New("missing_text")
	}
	if request.Type == "card" {
		var card map[string]any
		if json.Unmarshal([]byte(request.Text), &card) != nil {
			return errors.New("invalid_card_json")
		}
	}
	if contains([]string{"image", "file"}, request.Type) {
		if err := validatePrivateMediaPath(box.dataRoot, request.FilePath); err != nil {
			return err
		}
	}
	return nil
}

func validatePrivateMediaPath(dataRoot, path string) error {
	if path == "" {
		return errors.New("missing_file_path")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	mediaRoot := filepath.Join(dataRoot, "private-cache", "media")
	relative, err := filepath.Rel(mediaRoot, absolute)
	if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || filepath.IsAbs(relative) {
		return errors.New("media_file_outside_private_root")
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("unsafe_media_file")
	}
	return nil
}

func StageOutboundMedia(dataRoot, requestID, source string) (string, error) {
	info, err := os.Lstat(source)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > 30*1024*1024 {
		return "", errors.New("unsafe_media_file")
	}
	root := filepath.Join(dataRoot, "private-cache", "media")
	if err := ensurePrivateDirectory(root); err != nil {
		return "", err
	}
	extension := filepath.Ext(source)
	destination := filepath.Join(root, requestID+extension)
	input, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	_, copyErr := io.Copy(output, io.LimitReader(input, 30*1024*1024+1))
	closeErr := output.Close()
	if copyErr != nil {
		_ = os.Remove(destination)
		return "", copyErr
	}
	if closeErr != nil {
		_ = os.Remove(destination)
		return "", closeErr
	}
	return destination, nil
}

func (box *Outbox) Process(ctx context.Context, sender MessageSender, globalDryRun bool) error {
	box.mu.Lock()
	defer box.mu.Unlock()
	return withProcessFileLock(box.processLockPath(), func() error { return box.processLocked(ctx, sender, globalDryRun) })
}

func (box *Outbox) processLocked(ctx context.Context, sender MessageSender, globalDryRun bool) error {
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
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		if line <= state.ProcessedLineCount {
			continue
		}
		var request OutboxRequest
		result := OutboxResult{CompletedAt: time.Now().UTC()}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			result.ID, result.Status, result.Error = "invalid-line", "invalid", "invalid_json"
		} else if _, done := state.ProcessedIDs[request.ID]; done {
			result.ID, result.Status, result.Error = request.ID, "duplicate", "duplicate_id"
		} else {
			result = box.processRequest(ctx, sender, request, globalDryRun)
		}
		if err := appendPrivateJSONL(box.resultPath(), result); err != nil {
			return err
		}
		content := request.Text
		if request.FilePath != "" {
			if info, err := os.Stat(request.FilePath); err == nil {
				content = strconv.FormatInt(info.Size(), 10) + " bytes"
			}
		}
		_ = NewAuditLog(box.dataRoot).Record("outbox_result", map[string]any{"id": result.ID, "status": result.Status, "target": AuditTargetDescriptor(request.Target), "content": AuditContentDescriptor(content), "parts": result.PartCount, "error": result.Error})
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

func (box *Outbox) processRequest(ctx context.Context, sender MessageSender, request OutboxRequest, globalDryRun bool) OutboxResult {
	result := OutboxResult{ID: request.ID, Target: request.Target, Source: request.Source, Trace: request.Trace, CompletedAt: time.Now().UTC()}
	if err := box.validate(request); err != nil {
		result.Status, result.Error = "invalid", err.Error()
		return result
	}
	parts := []string{request.Text}
	if request.Type == "text" {
		parts = splitOutboundMessage(request.Text, 3200)
	}
	result.PartCount = len(parts)
	if globalDryRun || request.DryRun {
		result.Status, result.DryRun = "dry_run", true
		return result
	}
	if sender == nil {
		result.Status, result.Error = "failed", "message_sender_unavailable"
		return result
	}
	for index, part := range parts {
		value := part
		if request.Type == "image" || request.Type == "file" {
			value = request.FilePath
		}
		messageID, err := sender.Send(ctx, request.Target, request.Type, value, request.ID+"-"+strconv.Itoa(index+1))
		if err != nil {
			result.Error = safeCommandError(err.Error())
			break
		}
		result.MessageIDs = append(result.MessageIDs, messageID)
	}
	if result.Error == "" {
		result.Status = "sent"
	} else if len(result.MessageIDs) > 0 {
		result.Status = "partial_sent"
	} else {
		result.Status = "failed"
	}
	if request.Type == "image" || request.Type == "file" {
		_ = os.Remove(request.FilePath)
	}
	return result
}

func (box *Outbox) FindResult(id string) (OutboxResult, bool, error) {
	var result OutboxResult
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
			var item OutboxResult
			if json.Unmarshal(scanner.Bytes(), &item) == nil && item.ID == id {
				result, found = item, true
			}
		}
		return scanner.Err()
	})
	return result, found, err
}

func splitOutboundMessage(value string, limit int) []string {
	if len([]rune(value)) <= limit {
		return []string{value}
	}
	runes := []rune(value)
	result := []string{}
	for len(runes) > 0 {
		size := limit
		if len(runes) < size {
			size = len(runes)
		}
		result = append(result, string(runes[:size]))
		runes = runes[size:]
	}
	return result
}
