package taskruntime

import (
	"encoding/json"
	"time"
)

const Protocol = "ksfassistant-task-runtime-v1"
const SoftwareVersion = "0.11.0-preview.4"
const MaxRequestBytes = 256 << 10
const maxRecordBytes = 4 << 20
const maxHistory = 256
const maxRecords = 4096
const maxEntries = 16384

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (failure *Error) Error() string { return failure.Code + ": " + failure.Message }

func fail(code, message string) error { return &Error{Code: code, Message: message} }

type Request struct {
	Protocol         string         `json:"protocol"`
	Version          int            `json:"version"`
	Host             string         `json:"host,omitempty"`
	ThreadID         string         `json:"thread_id,omitempty"`
	TaskID           string         `json:"task_id,omitempty"`
	EventID          string         `json:"event_id,omitempty"`
	ExpectedRevision *uint64        `json:"expected_revision,omitempty"`
	State            *ReportedState `json:"state,omitempty"`
	Limit            int            `json:"limit,omitempty"`
	After            string         `json:"after,omitempty"`
}

type Progress struct {
	Summary string `json:"summary,omitempty"`
	Percent *int   `json:"percent,omitempty"`
}

type ReportedState struct {
	Scope          string          `json:"scope"`
	ProjectCard    string          `json:"project_card,omitempty"`
	ReportedStatus string          `json:"reported_status"`
	Receipt        json.RawMessage `json:"receipt,omitempty"`
	Progress       *Progress       `json:"progress,omitempty"`
}

type Snapshot struct {
	TaskID          string        `json:"task_id"`
	Host            string        `json:"host"`
	Revision        uint64        `json:"revision"`
	WorkspaceDigest string        `json:"workspace_digest"`
	CreatedAt       time.Time     `json:"created_at"`
	ReportedAt      time.Time     `json:"reported_at"`
	State           ReportedState `json:"state"`
	ProjectCard     string        `json:"project_card,omitempty"`
}

type Change struct {
	EventID       string   `json:"event_id"`
	PayloadSHA256 string   `json:"payload_sha256"`
	Snapshot      Snapshot `json:"snapshot"`
}

type Record struct {
	Protocol string   `json:"protocol"`
	Version  int      `json:"version"`
	Snapshot Snapshot `json:"snapshot"`
	History  []Change `json:"history"`
}

type View struct {
	Snapshot
	ReportFreshness string `json:"report_freshness"`
	RouteFreshness  string `json:"route_freshness"`
}

type Diagnostic struct {
	TaskID string `json:"task_id,omitempty"`
	Code   string `json:"code"`
}

type Doctor struct {
	Store       string       `json:"store"`
	Key         string       `json:"key"`
	Verifier    string       `json:"verifier"`
	Records     int          `json:"records"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

type Response struct {
	SoftwareVersion string   `json:"software_version,omitempty"`
	Protocol        string   `json:"protocol"`
	Version         int      `json:"version"`
	OK              bool     `json:"ok"`
	Error           *Error   `json:"error,omitempty"`
	Task            *View    `json:"task,omitempty"`
	History         []Change `json:"history,omitempty"`
	Replayed        bool     `json:"replayed,omitempty"`
	Tasks           []View   `json:"tasks,omitempty"`
	NextAfter       string   `json:"next_after,omitempty"`
	Doctor          *Doctor  `json:"doctor,omitempty"`
}
