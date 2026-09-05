package corebridge

import "encoding/json"

const Protocol = "ksfassistant-core-bridge-v1"

type Capability struct {
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

type Capabilities struct {
	Protocol     string                `json:"protocol"`
	Capabilities map[string]Capability `json:"capabilities"`
}

type KSFContext struct {
	State string `json:"state"`
	Root  string `json:"root,omitempty"`
}

type ProjectionRequest struct {
	RuntimeOwner string `json:"runtimeOwner"`
	ThreadID     string `json:"threadId"`
	TurnID       string `json:"turnId,omitempty"`
}

type PendingUserInput struct {
	ID        json.RawMessage  `json:"id"`
	Method    string           `json:"method"`
	ThreadID  string           `json:"threadId"`
	TurnID    string           `json:"turnId"`
	Questions []map[string]any `json:"questions"`
}

type Projection struct {
	State            map[string]any    `json:"state"`
	PendingInput     *PendingUserInput `json:"pendingInput,omitempty"`
	OwnerClientID    string            `json:"ownerClientId,omitempty"`
	SnapshotRevision string            `json:"snapshotRevision,omitempty"`
}

type ControlRequest struct {
	Protocol      string         `json:"protocol"`
	RequestID     string         `json:"requestId"`
	Operation     string         `json:"operation"`
	TaskKey       string         `json:"taskKey"`
	RuntimeOwner  string         `json:"runtimeOwner"`
	ThreadID      string         `json:"threadId,omitempty"`
	TurnID        string         `json:"turnId,omitempty"`
	CWD           string         `json:"cwd,omitempty"`
	Title         string         `json:"title,omitempty"`
	Text          string         `json:"text,omitempty"`
	Mode          map[string]any `json:"mode,omitempty"`
	RequestTarget string         `json:"requestTarget,omitempty"`
	// RequestTargetRaw preserves the Desktop JSON-RPC request ID type. The
	// string projection remains for compatibility and diagnostics only.
	RequestTargetRaw json.RawMessage `json:"requestTargetRaw,omitempty"`
	QuestionID       string          `json:"questionId,omitempty"`
	QuestionRevision string          `json:"questionRevision,omitempty"`
	Answer           string          `json:"answer,omitempty"`
}

type ControlResult struct {
	ThreadID     string          `json:"threadId,omitempty"`
	TurnID       string          `json:"turnId,omitempty"`
	RequestID    string          `json:"requestId,omitempty"`
	RequestIDRaw json.RawMessage `json:"requestIdRaw,omitempty"`
}

type InputOutcome struct {
	TaskKey          string `json:"taskKey"`
	ThreadID         string `json:"threadId"`
	TurnID           string `json:"turnId"`
	QuestionRevision string `json:"questionRevision"`
	State            string `json:"state"`
	ErrorCode        string `json:"errorCode,omitempty"`
}

type InitializeResult struct {
	Protocol string `json:"protocol"`
	Version  string `json:"version"`
}

type InitializeRequest struct {
	Protocol string `json:"protocol"`
}
