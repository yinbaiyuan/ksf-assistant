package integration

import (
	"context"
	"encoding/json"
	"errors"

	"ksfassistant/core/internal/corebridge"
	"ksfassistant/core/internal/feishutypes"
)

type MessageTarget = feishutypes.MessageTarget
type InboundMessage = feishutypes.InboundMessage
type InboundCardAction = feishutypes.InboundCardAction
type InboundCard = feishutypes.InboundCard
type ClientConfig = feishutypes.ClientConfig

const (
	LaunchScopeKSF         = "ksf"
	LaunchScopeProjectless = "projectless"
)

var ErrInvalidThreadLaunchContext = errors.New("invalid thread launch context")

type StartedThread struct {
	ThreadID  string
	CWD       string
	ProjectID string
}

type CorePort interface {
	Workspace(context.Context) (string, error)
	ReadThread(ctx context.Context, runtimeOwner, threadID, turnID string) (map[string]any, error)
	ProjectionOwner(threadID, runtimeOwner string) string
	PendingInput(threadID string) (corebridge.PendingUserInput, bool)
	StartThread(ctx context.Context, cwd, title string) (StartedThread, error)
	StartTurn(ctx context.Context, taskKey, runtimeOwner, threadID, cwd, text string, mode map[string]any) (string, error)
	SteerTurn(ctx context.Context, taskKey, runtimeOwner, threadID, turnID, cwd, text string) (string, error)
	InterruptTurn(ctx context.Context, taskKey, runtimeOwner, threadID, turnID string) error
	CancelApproval(ctx context.Context, taskKey, runtimeOwner, threadID, turnID string, expectedRequestID json.RawMessage, method string) error
	AnswerInput(ctx context.Context, taskKey, runtimeOwner, threadID, turnID string, expectedRequestID json.RawMessage, questionID, questionRevision, answer string) (json.RawMessage, error)
	ObserveDesktopThread(ctx context.Context, threadID string) (state map[string]any, owner, revision string, found bool, err error)
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

// ObservationPort returns a display-only snapshot and a connection-scoped revision.
type ObservationPort interface {
	ObserveThread(context.Context, string, string, string) (map[string]any, string, error)
}

// Optional display capabilities must be forwarded explicitly by decorators;
// embedding the base interface does not retain the concrete port's method set.
type NativeTaskCardPort interface {
	NativeTaskCardsEnabled() bool
}

type SnapshotSubscriptionPort interface {
	SubscribeThreadSnapshots(string) (<-chan struct{}, func())
}
