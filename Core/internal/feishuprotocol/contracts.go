package feishuprotocol

import "encoding/json"

const Protocol = "ksfassistant-feishu-v2"

const (
	Initialize    = "bridge/initialize"
	SnapshotRead  = "bridge/snapshot/read"
	SnapshotPush  = "bridge/snapshot/push"
	EventDeliver  = "integration/event/accept"
	ClientExecute = "bridge/client/execute"
	MessageSend   = "bridge/message/send"
	MessageReply  = "bridge/message/reply"
	CardPatch     = "bridge/card/patch"
	MediaStage    = "bridge/media/stage"
	MediaCleanup  = "bridge/media/cleanup"
	ConfigRead    = "bridge/config/read"
	SettingsRead  = "bridge/settings/read"
	SettingsWrite = "bridge/settings/write"
	SetupRead     = "bridge/setup/read"
	SetupWrite    = "bridge/setup/write"
	AuditRecord   = "bridge/audit/record"
)

type InitializeRequest struct {
	Protocol     string        `json:"protocol"`
	CardBindings []CardBinding `json:"cardBindings,omitempty"`
}

type CardBinding struct {
	TargetType string `json:"targetType"`
	TargetID   string `json:"targetId"`
	MessageID  string `json:"messageId"`
}

type InitializeResult struct {
	Protocol string `json:"protocol"`
	Version  string `json:"version"`
}

type Event struct {
	ID      string          `json:"id"`
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

type Accepted struct {
	Accepted bool `json:"accepted"`
}

type AuditRequest struct {
	Event  string         `json:"event"`
	Fields map[string]any `json:"fields"`
}

type MessageRequest struct {
	TargetType     string `json:"targetType,omitempty"`
	TargetID       string `json:"targetId,omitempty"`
	MessageID      string `json:"messageId,omitempty"`
	Format         string `json:"format"`
	Content        string `json:"content"`
	IdempotencyKey string `json:"idempotencyKey"`
}

type MessageResult struct {
	MessageID string `json:"messageId"`
}

type CardRequest struct {
	MessageID string `json:"messageId"`
	Content   string `json:"content"`
}

type MediaRequest struct {
	Message  json.RawMessage `json:"message"`
	MaxBytes int64           `json:"maxBytes"`
}

type CapabilityHealth struct {
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

type QueueHealth struct {
	State     string `json:"state"`
	Revision  uint64 `json:"revision,omitempty"`
	Pending   int    `json:"pending"`
	Running   int    `json:"running,omitempty"`
	Terminal  int    `json:"terminal,omitempty"`
	Processed int    `json:"processed"`
	LastError string `json:"lastError,omitempty"`
}

type Snapshot struct {
	Revision          uint64                      `json:"revision"`
	RuntimeKind       string                      `json:"runtimeKind"`
	Availability      string                      `json:"availability"`
	Message           string                      `json:"message,omitempty"`
	ProcessState      string                      `json:"processState"`
	Configured        bool                        `json:"configured"`
	ProcessPID        int                         `json:"processPid"`
	ProcessRunning    bool                        `json:"processRunning"`
	Profile           string                      `json:"profile"`
	ProfileValid      bool                        `json:"profileValid"`
	InboundConnection bool                        `json:"inboundConnection"`
	TargetAliases     []string                    `json:"targetAliases"`
	ReadinessBlockers []string                    `json:"readinessBlockers"`
	Capabilities      map[string]CapabilityHealth `json:"capabilities"`
	Queues            map[string]QueueHealth      `json:"queues"`
}
