package usercommand

import (
	"context"
	"encoding/json"
	"errors"
)

const Version = "1.0.93"
const PolicyVersion = "ksfassistant-user-command-v2"
const MaxRequestBytes = 3 * 1024 * 1024
const MaxContentBytes = 2 * 1024 * 1024

var ErrUnsupported = errors.New("user_command_unsupported")

type Identity struct {
	AppID           string `json:"appId"`
	ApplicationName string `json:"applicationName,omitempty"`
	UserID          string `json:"userId,omitempty"`
	UserName        string `json:"userName,omitempty"`
	Profile         string `json:"profile"`
	Brand           string `json:"brand"`
}

type File struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName,omitempty"`
	Data        []byte `json:"data"`
}

type Command struct {
	Version      string        `json:"version"`
	Args         []string      `json:"args"`
	Stdin        []byte        `json:"stdin,omitempty"`
	Files        []File        `json:"files,omitempty"`
	Identity     Identity      `json:"identity"`
	ArtifactPlan *ArtifactPlan `json:"artifactPlan,omitempty"`
}

// ApprovalPreview is display-only; Details and the frozen command remain complete.
type ApprovalPreview struct {
	Title        string `json:"-"`
	Content      string `json:"content"`
	ConfirmLabel string `json:"confirmLabel"`
	Destructive  bool   `json:"destructive"`
}

type Review struct {
	Preview       *ApprovalPreview `json:"-"`
	Effects       []Effect         `json:"effects,omitempty"`
	CapabilityID  string           `json:"capabilityId,omitempty"`
	CapabilityIDs []string         `json:"capabilityIds,omitempty"`
	Identity      string           `json:"identity"`
	Risk          string           `json:"risk"`
	Action        string           `json:"action"`
	Target        string           `json:"target"`
	Details       string           `json:"details"`
	Digest        string           `json:"digest"`
	NeedsApproval bool             `json:"needsApproval"`
}

type Effect struct {
	CapabilityID string `json:"capabilityId"`
	Risk         string `json:"risk"`
}

type CallerFunc func(context.Context, string, any, any) error

type RequestResult struct {
	ID string `json:"id"`
}
type StatusRequest struct {
	ID string `json:"id"`
}
type StatusResult struct {
	State string `json:"state"`
}
type ConsumeRequest struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
}
type ConsumeResult struct {
	Allowed bool `json:"allowed"`
}
type ResultRequest struct {
	ID      string `json:"id"`
	Outcome string `json:"outcome"`
}

func Decode(data json.RawMessage) (Command, error) {
	var command Command
	if len(data) > MaxRequestBytes {
		return command, errors.New("user_command_too_large")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return command, errors.New("user_command_json_invalid")
	}
	for key, value := range fields {
		if !includes("version args stdin files identity artifactPlan", key) || string(value) == "null" {
			return command, errors.New("user_command_json_invalid")
		}
	}
	if raw, ok := fields["artifactPlan"]; ok {
		if err := exactObjectFields(raw, "root directory targets"); err != nil {
			return command, err
		}
		var plan map[string]json.RawMessage
		_ = json.Unmarshal(raw, &plan)
		if targets, ok := plan["targets"]; ok {
			var entries []json.RawMessage
			if json.Unmarshal(targets, &entries) != nil {
				return command, errors.New("user_command_json_invalid")
			}
			for _, entry := range entries {
				if err := exactObjectFields(entry, "path directory"); err != nil {
					return command, err
				}
			}
		}
	}
	if raw, ok := fields["identity"]; ok {
		if err := exactObjectFields(raw, "appId applicationName userId userName profile brand"); err != nil {
			return command, err
		}
	}
	if raw, ok := fields["files"]; ok {
		var files []json.RawMessage
		if json.Unmarshal(raw, &files) != nil {
			return command, errors.New("user_command_json_invalid")
		}
		for _, file := range files {
			if err := exactObjectFields(file, "name displayName data"); err != nil {
				return command, err
			}
		}
	}
	if err := strictJSON(data, &command); err != nil {
		return command, err
	}
	_, err := Evaluate(command)
	return command, err
}

func exactObjectFields(data json.RawMessage, allowed string) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return errors.New("user_command_json_invalid")
	}
	for key, value := range fields {
		if !includes(allowed, key) || string(value) == "null" {
			return errors.New("user_command_json_invalid")
		}
	}
	return nil
}
