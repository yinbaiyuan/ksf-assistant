package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"ksfassistant/core/internal/capabilitypolicy"
	"ksfassistant/core/internal/corebridge"
	"ksfassistant/core/internal/desktop"
	"ksfassistant/core/internal/integration"
)

type coreCapabilityClient struct {
	service *Service
	nextID  atomic.Uint64
	mu      sync.Mutex
	pending map[string]corebridge.PendingUserInput
	owners  map[string]string
}

var _ integration.SnapshotSubscriptionPort = (*coreCapabilityClient)(nil)

func newCoreCapabilityClient(service *Service) *coreCapabilityClient {
	return &coreCapabilityClient{service: service, pending: map[string]corebridge.PendingUserInput{}, owners: map[string]string{}}
}

func (client *coreCapabilityClient) Workspace(ctx context.Context) (string, error) {
	host, err := client.service.hostContextStore.Load()
	if err != nil {
		return "", fmt.Errorf("%w: %v", integration.ErrInvalidThreadLaunchContext, err)
	}
	result := host.KSF
	if result.State == integration.KSFNotConfigured {
		return "", nil
	}
	if result.State != integration.KSFReady || result.Root == "" {
		return "", fmt.Errorf("%w: KSF context is %s", integration.ErrInvalidThreadLaunchContext, result.State)
	}
	return result.Root, nil
}

func (client *coreCapabilityClient) ArchivedThreadIDs(ctx context.Context) ([]string, error) {
	if client.service.codex == nil {
		return nil, fmt.Errorf("Codex App Server unavailable")
	}
	return client.service.codex.FetchArchivedThreadIDs(ctx)
}

func (client *coreCapabilityClient) ReadThread(ctx context.Context, runtimeOwner, threadID, turnID string) (map[string]any, error) {
	result, err := client.service.privateProjection(ctx, corebridge.ProjectionRequest{RuntimeOwner: runtimeOwner, ThreadID: threadID, TurnID: turnID})
	if err != nil {
		return nil, err
	}
	client.mu.Lock()
	if result.PendingInput == nil {
		delete(client.pending, threadID)
	} else {
		client.pending[threadID] = *result.PendingInput
	}
	if result.OwnerClientID == "" {
		delete(client.owners, threadID)
	} else {
		client.owners[threadID] = result.OwnerClientID
	}
	client.mu.Unlock()
	return result.State, nil
}

func (client *coreCapabilityClient) ProjectionOwner(threadID, runtimeOwner string) string {
	if runtimeOwner == "bridge" {
		return "bridge"
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.owners[threadID]
}

func (client *coreCapabilityClient) PendingInput(threadID string) (corebridge.PendingUserInput, bool) {
	client.mu.Lock()
	defer client.mu.Unlock()
	value, found := client.pending[threadID]
	return value, found
}

func (client *coreCapabilityClient) StartThread(ctx context.Context, cwd, title string) (integration.StartedThread, error) {
	result, err := client.control(ctx, corebridge.ControlRequest{Operation: "task.create", RuntimeOwner: "bridge", CWD: cwd, Title: title})
	return integration.StartedThread{ThreadID: result.ThreadID, CWD: result.CWD, ProjectID: result.ProjectID}, err
}

func (client *coreCapabilityClient) StartTurn(ctx context.Context, taskKey, runtimeOwner, threadID, cwd, text string, mode map[string]any) (string, error) {
	result, err := client.control(ctx, corebridge.ControlRequest{Operation: "turn.continue", TaskKey: taskKey, RuntimeOwner: runtimeOwner, ThreadID: threadID, CWD: cwd, Text: text, Mode: mode})
	return result.TurnID, err
}

func (client *coreCapabilityClient) SteerTurn(ctx context.Context, taskKey, runtimeOwner, threadID, turnID, cwd, text string) (string, error) {
	result, err := client.control(ctx, corebridge.ControlRequest{Operation: "turn.steer", TaskKey: taskKey, RuntimeOwner: runtimeOwner, ThreadID: threadID, TurnID: turnID, CWD: cwd, Text: text})
	return result.TurnID, err
}

func (client *coreCapabilityClient) InterruptTurn(ctx context.Context, taskKey, runtimeOwner, threadID, turnID string) error {
	_, err := client.control(ctx, corebridge.ControlRequest{Operation: "turn.interrupt", TaskKey: taskKey, RuntimeOwner: runtimeOwner, ThreadID: threadID, TurnID: turnID})
	return err
}

func (client *coreCapabilityClient) CancelApproval(ctx context.Context, taskKey, runtimeOwner, threadID, turnID string, expectedRequestID json.RawMessage, method string) error {
	if runtimeOwner != "bridge" {
		return fmt.Errorf("Codex Desktop approvals must be handled in Desktop")
	}
	if err := capabilitypolicy.CheckSession(client.service.feishuDataRoot); err != nil {
		return err
	}
	if client.service.codex == nil {
		return fmt.Errorf("Codex App Server unavailable")
	}
	return client.service.codex.CancelBridgeApproval(ctx, threadID, turnID, expectedRequestID, method)
}

func (client *coreCapabilityClient) AnswerInput(ctx context.Context, taskKey, runtimeOwner, threadID, turnID string, expectedRequestID json.RawMessage, questionID, questionRevision, answer string) (json.RawMessage, error) {
	result, err := client.control(ctx, corebridge.ControlRequest{Operation: "question.answer", TaskKey: taskKey, RuntimeOwner: runtimeOwner, ThreadID: threadID, TurnID: turnID, RequestTarget: pendingRequestID(expectedRequestID), RequestTargetRaw: expectedRequestID, QuestionID: questionID, QuestionRevision: questionRevision, Answer: answer})
	if len(result.RequestIDRaw) > 0 {
		return result.RequestIDRaw, err
	}
	fallback, _ := json.Marshal(result.RequestID)
	return fallback, err
}

func (client *coreCapabilityClient) control(ctx context.Context, request corebridge.ControlRequest) (corebridge.ControlResult, error) {
	if err := capabilitypolicy.CheckSession(client.service.feishuDataRoot); err != nil {
		return corebridge.ControlResult{}, err
	}
	request.Protocol = corebridge.Protocol
	request.RequestID = "bridge:" + strconv.FormatInt(time.Now().UnixMilli(), 10) + ":" + strconv.FormatUint(client.nextID.Add(1), 10)
	if request.TaskKey == "" {
		request.TaskKey = "task_create"
	}
	return client.service.privateControl(ctx, request)
}

func pendingRequestID(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	return string(raw)
}

func (c *coreCapabilityClient) ObserveThread(ctx context.Context, owner, thread, turn string) (map[string]any, string, error) {
	if owner == "bridge" {
		s, e := c.ReadThread(ctx, owner, thread, turn)
		return s, "", e
	}
	if c.service.desktop == nil {
		return nil, "", fmt.Errorf("Desktop IPC unavailable")
	}
	t, e := c.service.desktop.ObserveConversationState(ctx, thread)
	if e != nil {
		return nil, "", e
	}
	c.mu.Lock()
	c.owners[thread] = t.OwnerClientID
	c.mu.Unlock()
	return t.State, desktop.ObservationVersion(t), nil
}

func (c *coreCapabilityClient) SubscribeThreadSnapshots(thread string) (<-chan struct{}, func()) {
	if c.service.desktop == nil {
		return nil, func() {}
	}
	return c.service.desktop.SubscribeConversation(thread)
}

func (c *coreCapabilityClient) ObserveDesktopThread(ctx context.Context, threadID string) (map[string]any, string, string, bool, error) {
	if c.service.desktop == nil {
		return nil, "", "", false, nil
	}
	target, found, err := c.service.desktop.ObserveKnownConversationState(ctx, threadID)
	if err != nil || !found {
		return nil, "", "", found, err
	}
	c.mu.Lock()
	c.owners[threadID] = target.OwnerClientID
	c.mu.Unlock()
	return target.State, target.OwnerClientID, desktop.ObservationVersion(target), true, nil
}
