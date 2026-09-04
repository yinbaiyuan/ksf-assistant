package feishu

import (
	"errors"
	"regexp"
)

const CodexControlProtocol = "codex-control-v1"

type CodexControlAuthorization struct {
	Source   string `json:"source"`
	Explicit bool   `json:"explicit"`
}
type CodexControlRequest struct {
	Protocol      string                     `json:"protocol"`
	RequestID     string                     `json:"requestId"`
	Operation     string                     `json:"operation"`
	TaskKey       string                     `json:"taskKey"`
	Payload       map[string]any             `json:"payload"`
	Authorization *CodexControlAuthorization `json:"authorization,omitempty"`
}

var controlIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,128}$`)
var controlTaskPattern = regexp.MustCompile(`^task_[a-f0-9]{16,64}$`)

func ValidateCodexControlRequest(request CodexControlRequest) error {
	if request.Protocol != CodexControlProtocol {
		return errors.New("unsupported_control_protocol")
	}
	if !controlIDPattern.MatchString(request.RequestID) {
		return errors.New("invalid_control_request_id")
	}
	if !contains([]string{"task.create", "turn.continue", "turn.steer", "turn.interrupt", "question.answer", "attachment.attach", "card.callback"}, request.Operation) {
		return errors.New("unsupported_control_operation")
	}
	if !controlTaskPattern.MatchString(request.TaskKey) {
		return errors.New("invalid_control_task_key")
	}
	if request.Payload == nil || len(request.Payload) > 16 {
		return errors.New("invalid_control_payload")
	}
	if request.Authorization == nil || request.Authorization.Source != "feishu-bridge" || !request.Authorization.Explicit {
		return errors.New("control_authorization_required")
	}
	return nil
}
