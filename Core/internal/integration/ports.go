package integration

import (
	"context"
	"encoding/json"

	"ksfassistant/core/internal/corebridge"
	"ksfassistant/core/internal/feishutypes"
)

type MessageTarget = feishutypes.MessageTarget
type InboundMessage = feishutypes.InboundMessage
type InboundCardAction = feishutypes.InboundCardAction
type InboundCard = feishutypes.InboundCard
type ClientConfig = feishutypes.ClientConfig
type CorePort interface {
	Workspace(context.Context) (string, error)
	ReadThread(ctx context.Context, runtimeOwner, threadID, turnID string) (map[string]any, error)
	ProjectionOwner(threadID, runtimeOwner string) string
	PendingInput(threadID string) (corebridge.PendingUserInput, bool)
	StartThread(ctx context.Context, cwd, title string) (string, error)
	StartTurn(ctx context.Context, taskKey, runtimeOwner, threadID, cwd, text string, mode map[string]any) (string, error)
	SteerTurn(ctx context.Context, taskKey, runtimeOwner, threadID, turnID, cwd, text string) (string, error)
	InterruptTurn(ctx context.Context, taskKey, runtimeOwner, threadID, turnID string) error
	AnswerInput(ctx context.Context, taskKey, runtimeOwner, threadID, turnID string, expectedRequestID json.RawMessage, questionID, questionRevision, answer string) (json.RawMessage, error)
}

type FeishuPort interface {
	Send(ctx context.Context, target MessageTarget, format, content, idempotencyKey string) (string, error)
	Reply(ctx context.Context, messageID, format, content, idempotencyKey string) (string, error)
	PatchCard(ctx context.Context, messageID, card string) error
	ResolveMessageTarget(alias string) (MessageTarget, error)
	AliasForOpenID(openID string) string
	StageInbound(ctx context.Context, message InboundMessage, maxBytes int64) (StagedInboundMessage, error)
	CleanupInbound(ctx context.Context, directory string) error
}

type AuditPort interface {
	Record(event string, fields map[string]any) error
}

type CreateTaskLinkRequest struct {
	ThreadID    string `json:"threadId"`
	Title       string `json:"title"`
	ProjectName string `json:"projectName"`
	TargetAlias string `json:"targetAlias"`
	CWD         string `json:"cwd"`
}
