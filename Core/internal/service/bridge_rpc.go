package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"ksfassistant/core/internal/corebridge"
	"ksfassistant/core/internal/desktop"
	"ksfassistant/core/internal/domain"
	managedfeishu "ksfassistant/core/internal/feishu"
	"ksfassistant/core/internal/feishuprotocol"
	"ksfassistant/core/internal/integration"
	"ksfassistant/core/internal/privateipc"
)

var privateControlIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,128}$`)

func (service *Service) HandlePrivateRPC(ctx context.Context, method string, params json.RawMessage) (any, error) {
	generation := managedfeishu.EpochFromContext(ctx)
	if service.managedFeishuSupervisor == nil || !service.managedFeishuSupervisor.IsCurrentGeneration(generation) {
		return nil, managedfeishu.ErrStaleGeneration
	}
	if strings.HasPrefix(method, "userApproval/") {
		return service.handleUserApproval(ctx, method, params)
	}
	switch method {
	case feishuprotocol.EventDeliver:
		var event feishuprotocol.Event
		if err := decodePrivateParams(params, &event); err != nil {
			return nil, err
		}
		if service.integrationRuntime == nil {
			return nil, errors.New("Core integration unavailable")
		}
		return service.integrationRuntime.AcceptEvent(ctx, event)
	case feishuprotocol.SnapshotPush:
		var wire feishuprotocol.Snapshot
		if err := decodePrivateParams(params, &wire); err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(wire)
		if err != nil {
			return nil, err
		}
		var snapshot domain.FeishuSnapshot
		if err := json.Unmarshal(encoded, &snapshot); err != nil {
			return nil, err
		}
		service.mu.Lock()
		defer service.mu.Unlock()
		if generation != service.feishuGeneration {
			return nil, managedfeishu.ErrStaleGeneration
		}
		if snapshot.Revision > service.lastFeishu.Revision {
			service.lastFeishu = normalizedFeishuSnapshot(snapshot)
			service.lastFeishuAt = time.Now()
		}
		return feishuprotocol.Accepted{Accepted: true}, nil
	default:
		return nil, privateipc.ErrMethodNotFound
	}
}

func (service *Service) privateCapabilities() corebridge.Capabilities {
	values := map[string]corebridge.Capability{
		"codexAppServer": {State: "unavailable"},
		"desktopIPC":     {State: "unavailable"},
		"ksfContext":     {State: "unavailable"},
	}
	if service.codex != nil {
		values["codexAppServer"] = corebridge.Capability{State: "ready"}
	}
	if service.desktop != nil {
		state := service.desktop.Snapshot(time.Now()).Availability
		if state == "available" {
			values["desktopIPC"] = corebridge.Capability{State: "ready"}
		} else if state != "" {
			values["desktopIPC"] = corebridge.Capability{State: "degraded", Detail: state}
		}
	}
	if value, err := service.hostContextStore.Load(); err == nil && value.KSF.State == "ready" {
		values["ksfContext"] = corebridge.Capability{State: "ready"}
	}
	return corebridge.Capabilities{Protocol: corebridge.Protocol, Capabilities: values}
}

func (service *Service) privateProjection(ctx context.Context, request corebridge.ProjectionRequest) (corebridge.Projection, error) {
	if strings.TrimSpace(request.ThreadID) == "" {
		return corebridge.Projection{}, errors.New("thread id is required")
	}
	if request.RuntimeOwner == "bridge" {
		if service.codex == nil {
			return corebridge.Projection{}, privateipc.NewError(-32051, "Codex App Server unavailable")
		}
		state, err := service.codex.ReadBridgeThread(ctx, request.ThreadID)
		if err != nil {
			return corebridge.Projection{}, err
		}
		result := corebridge.Projection{State: state}
		if pending, ok := service.codex.PendingBridgeUserInput(request.ThreadID); ok {
			result.PendingInput = &corebridge.PendingUserInput{ID: pending.ID, Method: pending.Method, ThreadID: pending.ThreadID, TurnID: pending.TurnID, Questions: pending.Questions}
		}
		return result, nil
	}
	if service.desktop == nil {
		return corebridge.Projection{}, privateipc.NewError(-32052, "Desktop IPC unavailable")
	}
	target, err := service.desktop.ReadConversationStateForUserInput(ctx, request.ThreadID)
	if err != nil {
		return corebridge.Projection{}, err
	}
	return corebridge.Projection{State: target.State, OwnerClientID: target.OwnerClientID, SnapshotRevision: target.SnapshotRevision}, nil
}

func (service *Service) privateControl(ctx context.Context, request corebridge.ControlRequest) (corebridge.ControlResult, error) {
	if request.Protocol != corebridge.Protocol || !privateControlIDPattern.MatchString(request.RequestID) || strings.TrimSpace(request.TaskKey) == "" {
		return corebridge.ControlResult{}, privateipc.NewError(-32602, "invalid codex control request")
	}
	bridgeOwned := request.RuntimeOwner == "bridge"
	switch request.Operation {
	case "task.create":
		if service.codex == nil {
			return corebridge.ControlResult{}, privateipc.NewError(-32051, "Codex App Server unavailable")
		}
		threadID, err := service.codex.StartBridgeThread(ctx, request.CWD, request.Title)
		return corebridge.ControlResult{ThreadID: threadID}, err
	case "turn.continue":
		cwd := strings.TrimSpace(request.CWD)
		if cwd == "" {
			cwd = service.controlWorkingDirectory(ctx, request.RuntimeOwner, request.ThreadID)
		}
		if cwd == "" {
			return corebridge.ControlResult{}, privateipc.NewError(-32053, "task working directory unavailable")
		}
		if bridgeOwned {
			if service.codex == nil {
				return corebridge.ControlResult{}, privateipc.NewError(-32051, "Codex App Server unavailable")
			}
			turnID, err := service.codex.StartBridgeTurnWithMode(ctx, request.ThreadID, cwd, request.Text, request.Mode)
			return corebridge.ControlResult{TurnID: turnID}, err
		}
		if service.desktop == nil {
			return corebridge.ControlResult{}, privateipc.NewError(-32052, "Desktop IPC unavailable")
		}
		turnID, err := service.desktop.StartBridgeTurn(ctx, request.ThreadID, cwd, request.Text, request.Mode)
		return corebridge.ControlResult{TurnID: turnID}, err
	case "turn.steer":
		if bridgeOwned {
			if service.codex == nil {
				return corebridge.ControlResult{}, privateipc.NewError(-32051, "Codex App Server unavailable")
			}
			turnID, err := service.codex.SteerBridgeTurn(ctx, request.ThreadID, request.TurnID, request.Text)
			return corebridge.ControlResult{TurnID: turnID}, err
		}
		if service.desktop == nil {
			return corebridge.ControlResult{}, privateipc.NewError(-32052, "Desktop IPC unavailable")
		}
		cwd := strings.TrimSpace(request.CWD)
		if cwd == "" {
			cwd = service.controlWorkingDirectory(ctx, request.RuntimeOwner, request.ThreadID)
		}
		if cwd == "" {
			return corebridge.ControlResult{}, privateipc.NewError(-32053, "task working directory unavailable")
		}
		turnID, err := service.desktop.SteerBridgeTurn(ctx, request.ThreadID, cwd, request.TurnID, request.Text)
		return corebridge.ControlResult{TurnID: turnID}, err
	case "turn.interrupt":
		var err error
		if bridgeOwned {
			if service.codex == nil {
				return corebridge.ControlResult{}, privateipc.NewError(-32051, "Codex App Server unavailable")
			}
			err = service.codex.InterruptBridgeTurn(ctx, request.ThreadID, request.TurnID)
		} else if service.desktop == nil {
			return corebridge.ControlResult{}, privateipc.NewError(-32052, "Desktop IPC unavailable")
		} else {
			err = service.desktop.InterruptBridgeTurn(ctx, request.ThreadID, request.TurnID)
		}
		return corebridge.ControlResult{TurnID: request.TurnID}, err
	case "question.answer":
		answers := map[string]any{request.QuestionID: map[string]any{"answers": []string{request.Answer}}}
		if bridgeOwned {
			if service.codex == nil {
				return corebridge.ControlResult{}, privateipc.NewError(-32051, "Codex App Server unavailable")
			}
			if err := service.codex.AnswerBridgeUserInput(request.ThreadID, answers); err != nil {
				return corebridge.ControlResult{}, err
			}
			return corebridge.ControlResult{RequestID: request.RequestTarget, RequestIDRaw: append(json.RawMessage(nil), request.RequestTargetRaw...)}, nil
		}
		return service.submitDesktopUserInput(ctx, request, answers)
	default:
		return corebridge.ControlResult{}, privateipc.NewError(-32602, "unsupported codex control operation")
	}
}

func (service *Service) controlWorkingDirectory(ctx context.Context, runtimeOwner, threadID string) string {
	var state map[string]any
	if runtimeOwner == "bridge" {
		if service.codex == nil {
			return ""
		}
		state, _ = service.codex.ReadBridgeThread(ctx, threadID)
	} else if service.desktop != nil {
		if cached, found := service.desktop.CachedConversationState(threadID); found {
			state = cached
		} else {
			state, _ = service.desktop.ReadConversationState(ctx, threadID)
		}
	}
	return recursiveControlString(state, "cwd", "workingDirectory", "workspaceRoot")
}

func recursiveControlString(value any, names ...string) string {
	switch item := value.(type) {
	case map[string]any:
		for _, name := range names {
			if text, ok := item[name].(string); ok && strings.TrimSpace(text) != "" {
				return text
			}
		}
		keys := make([]string, 0, len(item))
		for key := range item {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			child := item[key]
			if text := recursiveControlString(child, names...); text != "" {
				return text
			}
		}
	case []any:
		for _, child := range item {
			if text := recursiveControlString(child, names...); text != "" {
				return text
			}
		}
	}
	return ""
}

func (service *Service) submitDesktopUserInput(ctx context.Context, request corebridge.ControlRequest, answers map[string]any) (corebridge.ControlResult, error) {
	if service.desktop == nil {
		return corebridge.ControlResult{}, privateipc.NewError(-32052, "Desktop IPC unavailable")
	}
	rawRequestID := request.RequestTargetRaw
	if len(rawRequestID) == 0 && strings.TrimSpace(request.RequestTarget) != "" {
		rawRequestID, _ = json.Marshal(request.RequestTarget)
	}
	requestRef, err := desktop.ParseRequestRef(rawRequestID)
	if err != nil {
		return corebridge.ControlResult{}, privateipc.NewError(-32602, "invalid Desktop user-input request id")
	}
	session, err := service.desktop.OpenUserInputSession(ctx)
	if err != nil {
		return corebridge.ControlResult{}, fmt.Errorf("desktop user-input owner unavailable: %w", err)
	}
	backgroundVerification := false
	defer func() {
		if !backgroundVerification {
			session.Close()
		}
	}()
	target, err := session.Read(ctx, request.ThreadID, requestRef)
	if err != nil {
		return corebridge.ControlResult{}, err
	}
	definition, found := desktop.FindPendingUserInput(target.State, requestRef)
	if !found || len(definition.Questions) == 0 {
		return corebridge.ControlResult{}, errors.New("desktop_user_input_request_stale")
	}
	turnID := definition.TurnID
	if turnID == "" {
		turnID = request.TurnID
	}
	if request.TurnID == "" || turnID != request.TurnID || !desktopQuestionExists(definition.Questions, request.QuestionID) {
		return corebridge.ControlResult{}, errors.New("desktop_user_input_request_stale")
	}
	target.TurnID = turnID
	expectedRevision := integration.PendingQuestionRevisionScoped(request.TaskKey, turnID, target.OwnerClientID, requestRef.Raw, definition.Questions)
	if request.QuestionRevision == "" || request.QuestionRevision != expectedRevision {
		return corebridge.ControlResult{}, errors.New("desktop_user_input_request_stale")
	}
	_ = (integrationFeishuPort{service}).Record("desktop_user_input_submit_started", map[string]any{
		"task": managedfeishu.AuditFingerprint(request.TaskKey), "turn": managedfeishu.AuditFingerprint(turnID),
		"owner": managedfeishu.AuditFingerprint(target.OwnerClientID), "requestType": requestRef.Kind,
		"requestFingerprint": requestRef.Fingerprint(), "beforeRevision": managedfeishu.AuditFingerprint(target.SnapshotRevision),
	})
	if err := session.Submit(ctx, target, map[string]any{"answers": answers}); err != nil {
		_ = (integrationFeishuPort{service}).Record("desktop_user_input_submit_rejected", map[string]any{"task": managedfeishu.AuditFingerprint(request.TaskKey), "requestType": requestRef.Kind, "requestFingerprint": requestRef.Fingerprint(), "resultType": "error"})
		return corebridge.ControlResult{}, err
	}
	verifyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	verification, verifyErr := session.Verify(verifyCtx, target)
	cancel()
	if verifyErr != nil {
		backgroundVerification = true
		go service.continueDesktopUserInputVerification(session, target, request)
		_ = (integrationFeishuPort{service}).Record("desktop_user_input_verifying", map[string]any{"task": managedfeishu.AuditFingerprint(request.TaskKey), "requestType": requestRef.Kind, "requestFingerprint": requestRef.Fingerprint(), "resultType": "accepted", "beforeRevision": managedfeishu.AuditFingerprint(target.SnapshotRevision)})
		return corebridge.ControlResult{}, errors.New("desktop_user_input_outcome_unknown")
	}
	_ = (integrationFeishuPort{service}).Record("desktop_user_input_consumed", map[string]any{"task": managedfeishu.AuditFingerprint(request.TaskKey), "requestType": requestRef.Kind, "requestFingerprint": requestRef.Fingerprint(), "resultType": "accepted", "afterRevision": managedfeishu.AuditFingerprint(verification.SnapshotRevision), "requestConsumed": verification.RequestConsumed, "turnRunning": verification.TurnRunning})
	return corebridge.ControlResult{RequestID: requestRef.CompatibilityString(), RequestIDRaw: append(json.RawMessage(nil), requestRef.Raw...)}, nil
}

func desktopQuestionExists(questions []map[string]any, questionID string) bool {
	for _, question := range questions {
		if strings.TrimSpace(fmt.Sprint(question["id"])) == questionID {
			return true
		}
	}
	return false
}

func (service *Service) continueDesktopUserInputVerification(session *desktop.UserInputSession, target desktop.UserInputTarget, request corebridge.ControlRequest) {
	defer session.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	verification := desktop.UserInputVerification{}
	var err error
	for ctx.Err() == nil {
		attemptCtx, attemptCancel := context.WithTimeout(ctx, 5*time.Second)
		verification, err = session.Verify(attemptCtx, target)
		attemptCancel()
		if err == nil && verification.RequestConsumed {
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(250 * time.Millisecond):
		}
	}
	outcome := corebridge.InputOutcome{TaskKey: request.TaskKey, ThreadID: request.ThreadID, TurnID: request.TurnID, QuestionRevision: request.QuestionRevision, State: "outcome_unknown", ErrorCode: "desktop_user_input_not_consumed"}
	if err == nil && verification.RequestConsumed {
		outcome.State = "succeeded"
		outcome.ErrorCode = ""
	}
	_ = (integrationFeishuPort{service}).Record("desktop_user_input_"+outcome.State, map[string]any{
		"task": managedfeishu.AuditFingerprint(request.TaskKey), "requestType": target.RequestID.Kind,
		"requestFingerprint": target.RequestID.Fingerprint(), "beforeRevision": managedfeishu.AuditFingerprint(target.SnapshotRevision),
		"afterRevision": managedfeishu.AuditFingerprint(verification.SnapshotRevision), "requestConsumed": verification.RequestConsumed, "turnRunning": verification.TurnRunning,
	})
	if service.integrationRuntime != nil {
		_ = service.integrationRuntime.ApplyInputOutcome(context.Background(), outcome)
	}
}

func decodePrivateParams(raw json.RawMessage, target any) error {
	if err := privateipc.DecodeStrict(raw, target, true); err != nil {
		return privateipc.NewError(-32602, "invalid private IPC params")
	}
	return nil
}

func requireNoPrivateParams(raw json.RawMessage) error {
	if err := privateipc.RequireNoParams(raw); err != nil {
		return privateipc.NewError(-32602, "invalid private IPC params")
	}
	return nil
}
