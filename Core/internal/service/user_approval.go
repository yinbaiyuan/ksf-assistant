package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"ksfassistant/core/internal/privateipc"
	"ksfassistant/core/internal/privatestore"
	"ksfassistant/core/internal/userapproval"
	"ksfassistant/core/internal/usercommand"
)

type approvalBinding struct {
	owner        context.Context
	identity     usercommand.Identity
	review       usercommand.Review
	policyDigest string
	boundDigest  string
}

type approvalState struct {
	mu             sync.Mutex
	broker         *userapproval.Broker
	bindings       map[string]approvalBinding
	verifyIdentity func(context.Context, usercommand.Identity) error
}

func (service *Service) approvals() *approvalState {
	service.approvalMu.Lock()
	defer service.approvalMu.Unlock()
	if service.userApprovals == nil {
		state := &approvalState{bindings: map[string]approvalBinding{}}
		state.verifyIdentity = service.verifyApprovalIdentity
		state.broker = userapproval.New(nil, func(event userapproval.AuditEvent) error {
			path := filepath.Join(service.feishuDataRoot, "user-approval-audit-v1", event.ID+".json")
			var events []userapproval.AuditEvent
			if _, err := privatestore.ReadJSON(path, &events); err != nil {
				return err
			}
			events = append(events, event)
			return privatestore.WriteJSON(path, events)
		})
		service.userApprovals = state
	}
	return service.userApprovals
}

func (service *Service) verifyApprovalIdentity(ctx context.Context, identity usercommand.Identity) error {
	manager, err := service.desktopToolchain()
	if err != nil {
		return errors.New("approval_toolchain_unavailable")
	}
	verifier, ok := manager.(interface {
		ValidateIdentity(context.Context, usercommand.Identity) error
	})
	if !ok {
		return errors.New("approval_toolchain_unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := verifier.ValidateIdentity(ctx, identity); err != nil {
		return errors.New("approval_identity_changed_or_unavailable")
	}
	return nil
}

func (service *Service) approvalPolicy(review usercommand.Review) (string, error) {
	return usercommand.CheckPolicy(service.feishuDataRoot, review)
}

func (service *Service) UserApprovalPoll(interactive bool) any {
	state := service.approvals()
	request := state.broker.Poll(interactive)
	state.mu.Lock()
	for id, binding := range state.bindings {
		status, err := state.broker.Status(binding.owner, id)
		if err != nil || status != "pending" && status != "approved" && status != "executing" {
			delete(state.bindings, id)
		}
	}
	state.mu.Unlock()
	return struct {
		SchemaVersion int                  `json:"schemaVersion"`
		Request       *userapproval.Review `json:"request"`
	}{1, request}
}

func (service *Service) UserApprovalDecide(id string, approve bool) any {
	return struct {
		SchemaVersion int  `json:"schemaVersion"`
		Accepted      bool `json:"accepted"`
	}{1, service.approvals().broker.Decide(id, approve)}
}

func (service *Service) handleUserApproval(ctx context.Context, method string, params json.RawMessage) (any, error) {
	owner, ok := privateipc.ConnectionContext(ctx)
	if !ok {
		return nil, errors.New("approval_connection_required")
	}
	state := service.approvals()
	switch method {
	case "userApproval/request":
		command, err := usercommand.Decode(params)
		if err != nil {
			return nil, errors.New("approval_invalid_command")
		}
		review, err := usercommand.Evaluate(command)
		if err != nil || !review.NeedsApproval || review.Identity != "user" {
			return nil, errors.New("approval_invalid_command")
		}
		policy, err := service.approvalPolicy(review)
		if err != nil {
			return nil, err
		}
		if err := state.verifyIdentity(ctx, command.Identity); err != nil {
			return nil, err
		}
		digest := sha256.Sum256([]byte(review.Digest + ":" + policy))
		boundDigest := hex.EncodeToString(digest[:])
		application := command.Identity.ApplicationName
		if application == "" {
			appDigest := sha256.Sum256([]byte(command.Identity.AppID))
			application = "飞书应用 " + hex.EncodeToString(appDigest[:6])
		}
		user := command.Identity.UserName
		if user == "" {
			return nil, errors.New("approval_user_display_unavailable")
		}
		display := userapproval.Review{Title: "飞书用户身份操作批准", User: user, Application: application + " / " + command.Identity.Profile, Action: review.Action, Target: review.Target, Content: review.Details, Attachments: []userapproval.Attachment{}}
		if review.Preview != nil {
			display.Title = review.Action // Retain the exact reviewed command description in details.
			display.Action = review.Preview.Title
			display.Preview = &userapproval.Preview{Content: review.Preview.Content, ConfirmLabel: review.Preview.ConfirmLabel, Destructive: review.Preview.Destructive}
		}
		for _, file := range command.Files {
			checksum := sha256.Sum256(file.Data)
			name := file.DisplayName
			if name == "" {
				name = file.Name
			}
			display.Attachments = append(display.Attachments, userapproval.Attachment{Name: name, Size: int64(len(file.Data)), SHA256: hex.EncodeToString(checksum[:])})
		}
		id, err := state.broker.Request(owner, boundDigest, display)
		if err != nil {
			return nil, err
		}
		review.Details = ""
		review.Preview = nil
		state.mu.Lock()
		state.bindings[id] = approvalBinding{owner: owner, identity: command.Identity, review: review, policyDigest: policy, boundDigest: boundDigest}
		state.mu.Unlock()
		return usercommand.RequestResult{ID: id}, nil
	case "userApproval/status", "userApproval/cancel":
		var input usercommand.StatusRequest
		if err := userapproval.DecodeParams(params, &input, "id"); err != nil || input.ID == "" {
			return nil, errors.New("approval_invalid_request")
		}
		if method == "userApproval/cancel" {
			err := state.broker.Cancel(owner, input.ID)
			return map[string]bool{"cancelled": err == nil}, err
		}
		status, err := state.broker.Status(owner, input.ID)
		return usercommand.StatusResult{State: status}, err
	case "userApproval/consume":
		var input usercommand.ConsumeRequest
		if err := userapproval.DecodeParams(params, &input, "id", "digest"); err != nil || input.ID == "" || input.Digest == "" {
			return nil, errors.New("approval_invalid_request")
		}
		state.mu.Lock()
		binding, found := state.bindings[input.ID]
		state.mu.Unlock()
		if !found || binding.owner != owner {
			return nil, errors.New("approval_not_found")
		}
		status, err := state.broker.Status(owner, input.ID)
		if err != nil {
			return nil, err
		}
		if status != "approved" {
			return nil, errors.New("approval_" + status)
		}
		if input.Digest != binding.review.Digest {
			_ = state.broker.Cancel(owner, input.ID)
			return nil, errors.New("approval_request_changed")
		}
		if err := state.verifyIdentity(ctx, binding.identity); err != nil {
			_ = state.broker.Cancel(owner, input.ID)
			return nil, err
		}
		err = privatestore.WithFileLock(filepath.Join(service.feishuDataRoot, "feishu-capability-policy-v1.json.lock"), func() error {
			policy, err := service.approvalPolicy(binding.review)
			if err != nil || policy != binding.policyDigest {
				_ = state.broker.Cancel(owner, input.ID)
				return errors.New("approval_policy_changed")
			}
			return state.broker.Consume(owner, input.ID, binding.boundDigest)
		})
		if err != nil {
			return nil, err
		}
		return usercommand.ConsumeResult{Allowed: true}, nil
	case "userApproval/result":
		var input usercommand.ResultRequest
		if err := userapproval.DecodeParams(params, &input, "id", "outcome"); err != nil || input.ID == "" {
			return nil, errors.New("approval_invalid_request")
		}
		err := state.broker.Result(owner, input.ID, input.Outcome)
		return map[string]bool{"recorded": err == nil}, err
	default:
		return nil, privateipc.ErrMethodNotFound
	}
}
