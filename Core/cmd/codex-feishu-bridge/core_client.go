package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"codexusagebar/core/internal/corebridge"
	"codexusagebar/core/internal/privateipc"
)

type coreCapabilityClient struct {
	peer    *privateipc.Peer
	nextID  atomic.Uint64
	mu      sync.Mutex
	pending map[string]corebridge.PendingUserInput
	owners  map[string]string
}

func newCoreCapabilityClient(peer *privateipc.Peer) *coreCapabilityClient {
	return &coreCapabilityClient{peer: peer, pending: map[string]corebridge.PendingUserInput{}, owners: map[string]string{}}
}

func (client *coreCapabilityClient) capabilities(ctx context.Context) (corebridge.Capabilities, error) {
	var result corebridge.Capabilities
	err := client.peer.Call(ctx, corebridge.MethodCapabilitiesRead, map[string]any{}, &result)
	return result, err
}

func (client *coreCapabilityClient) workspace(ctx context.Context) (string, error) {
	var result corebridge.KSFContext
	if err := client.peer.Call(ctx, corebridge.MethodKSFContextRead, map[string]any{}, &result); err != nil {
		return "", err
	}
	if result.State != "ready" || result.Root == "" {
		return "", fmt.Errorf("KSF context is %s", result.State)
	}
	return result.Root, nil
}

func (client *coreCapabilityClient) readThread(ctx context.Context, runtimeOwner, threadID, turnID string) (map[string]any, error) {
	var result corebridge.Projection
	if err := client.peer.Call(ctx, corebridge.MethodProjectionRead, corebridge.ProjectionRequest{RuntimeOwner: runtimeOwner, ThreadID: threadID, TurnID: turnID}, &result); err != nil {
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

func (client *coreCapabilityClient) projectionOwner(threadID, runtimeOwner string) string {
	if runtimeOwner == "bridge" {
		return "bridge"
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.owners[threadID]
}

func (client *coreCapabilityClient) pendingInput(threadID string) (corebridge.PendingUserInput, bool) {
	client.mu.Lock()
	defer client.mu.Unlock()
	value, found := client.pending[threadID]
	return value, found
}

func (client *coreCapabilityClient) startThread(ctx context.Context, cwd, title string) (string, error) {
	result, err := client.control(ctx, corebridge.ControlRequest{Operation: "task.create", RuntimeOwner: "bridge", CWD: cwd, Title: title})
	return result.ThreadID, err
}

func (client *coreCapabilityClient) startTurn(ctx context.Context, taskKey, runtimeOwner, threadID, cwd, text string, mode map[string]any) (string, error) {
	result, err := client.control(ctx, corebridge.ControlRequest{Operation: "turn.continue", TaskKey: taskKey, RuntimeOwner: runtimeOwner, ThreadID: threadID, CWD: cwd, Text: text, Mode: mode})
	return result.TurnID, err
}

func (client *coreCapabilityClient) steerTurn(ctx context.Context, taskKey, runtimeOwner, threadID, turnID, cwd, text string) (string, error) {
	result, err := client.control(ctx, corebridge.ControlRequest{Operation: "turn.steer", TaskKey: taskKey, RuntimeOwner: runtimeOwner, ThreadID: threadID, TurnID: turnID, CWD: cwd, Text: text})
	return result.TurnID, err
}

func (client *coreCapabilityClient) interruptTurn(ctx context.Context, taskKey, runtimeOwner, threadID, turnID string) error {
	_, err := client.control(ctx, corebridge.ControlRequest{Operation: "turn.interrupt", TaskKey: taskKey, RuntimeOwner: runtimeOwner, ThreadID: threadID, TurnID: turnID})
	return err
}

func (client *coreCapabilityClient) answerInput(ctx context.Context, taskKey, runtimeOwner, threadID, turnID string, expectedRequestID json.RawMessage, questionID, questionRevision, answer string) (json.RawMessage, error) {
	result, err := client.control(ctx, corebridge.ControlRequest{Operation: "question.answer", TaskKey: taskKey, RuntimeOwner: runtimeOwner, ThreadID: threadID, TurnID: turnID, RequestTarget: pendingRequestID(expectedRequestID), RequestTargetRaw: expectedRequestID, QuestionID: questionID, QuestionRevision: questionRevision, Answer: answer})
	if len(result.RequestIDRaw) > 0 {
		return result.RequestIDRaw, err
	}
	fallback, _ := json.Marshal(result.RequestID)
	return fallback, err
}

func (client *coreCapabilityClient) control(ctx context.Context, request corebridge.ControlRequest) (corebridge.ControlResult, error) {
	request.Protocol = corebridge.Protocol
	request.RequestID = "bridge:" + strconv.FormatInt(time.Now().UnixMilli(), 10) + ":" + strconv.FormatUint(client.nextID.Add(1), 10)
	if request.TaskKey == "" {
		request.TaskKey = "task_create"
	}
	var result corebridge.ControlResult
	err := client.peer.Call(ctx, corebridge.MethodCodexControl, request, &result)
	return result, err
}

func pendingRequestID(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	return string(raw)
}
