package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	managedfeishu "ksfassistant/core/internal/feishu"
	"ksfassistant/core/internal/privatestore"
)

type ConfigurationReceipt struct {
	RecoveryVerifiedAt string `json:"recoveryVerifiedAt,omitempty"`
	PermissionRevision string `json:"permissionRevision,omitempty"`
	Title              string `json:"title,omitempty"`
	StatusText         string `json:"statusText,omitempty"`
	StageText          string `json:"stageText,omitempty"`
	ApplicationID      string `json:"applicationId,omitempty"`
	FlowID             string `json:"flowId,omitempty"`
	SchemaVersion      int    `json:"schemaVersion"`
	RequestID          string `json:"requestId"`
	Digest             string `json:"digest,omitempty"`
	Action             string `json:"action"`
	ContextRevision    string `json:"contextRevision"`
	Outcome            string `json:"outcome"`
	Stage              string `json:"stage"`
	Code               string `json:"code,omitempty"`
	Message            string `json:"message"`
	UpdatedAt          string `json:"updatedAt"`
}

func (s *Service) receiptPath(id string) string {
	return filepath.Join(s.feishuDataRoot, "configuration-receipts-v1", configurationHash(id)+".json")
}
func (s *Service) saveConfigurationReceipt(r ConfigurationReceipt) error {
	if r.Action == "logout" && r.Outcome == "completed" {
		r.ApplicationID, r.FlowID, r.PermissionRevision, r.ContextRevision, r.Digest = "", "", "", "", ""
	}
	r.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return privatestore.WriteJSON(s.receiptPath(r.RequestID), r)
}
func (s *Service) ApplyFeishuConfiguration(ctx context.Context, request ConfigurationActionRequest) (ConfigurationActionResult, error) {
	if s.feishuDataRoot == "" {
		return ConfigurationActionResult{}, errors.New("configuration_storage_unavailable")
	}
	if err := validateConfigurationAction(request); err != nil {
		return ConfigurationActionResult{Outcome: "failed", Snapshot: s.ReadFeishuConfiguration(ctx, false), Message: "本次操作尚未提交：" + err.Error()}, nil
	}
	var result ConfigurationActionResult
	err := privatestore.WithFileLock(s.receiptPath(request.RequestID)+".lock", func() error {
		var receipt ConfigurationReceipt
		missing, err := privatestore.ReadJSON(s.receiptPath(request.RequestID), &receipt)
		if err != nil {
			return err
		}
		if !missing {
			if receipt.Digest != configurationHash(request) {
				result = ConfigurationActionResult{Outcome: "failed", Snapshot: s.ReadFeishuConfiguration(ctx, false), Message: "请求内容与原记录不符，未执行。"}
				return nil
			}
			if configurationFlowEndedByLogout(receipt, s.loadConfigurationReceipts()) {
				result = ConfigurationActionResult{Outcome: "failed", Snapshot: s.ReadFeishuConfiguration(ctx, false), Message: "此登录流程已随之后的注销结束，请发起新的登录。"}
				return nil
			}
			result = ConfigurationActionResult{Outcome: receipt.Outcome, Snapshot: s.ReadFeishuConfiguration(ctx, false), Message: receipt.Message}
			return nil
		}
		for _, unresolved := range s.configurationFailures() {
			if unresolved.Action == request.Action && (unresolved.Outcome == "unknown" || unresolved.Outcome == "pending") {
				result = ConfigurationActionResult{Outcome: "failed", Snapshot: s.ReadFeishuConfiguration(ctx, false), Message: "此操作仍有待核实的原请求，请刷新查询结果；尚未再次执行。"}
				return nil
			}
		}
		receipt = ConfigurationReceipt{SchemaVersion: 1, RequestID: request.RequestID, Digest: configurationHash(request), Action: request.Action, ContextRevision: request.ContextRevision, Outcome: "pending", Stage: "validating", Message: "正在复核，尚未确认执行结果。"}
		if err := s.saveConfigurationReceipt(receipt); err != nil {
			return err
		}
		result, err = s.applyFeishuConfiguration(ctx, request)
		if err != nil {
			result = ConfigurationActionResult{Outcome: "failed", Snapshot: s.ReadFeishuConfiguration(ctx, false), Message: "操作尚未执行，请刷新状态后重试。"}
			receipt.Code = "configuration_preflight_failed"
		}
		var persisted ConfigurationReceipt
		if absent, e := privatestore.ReadJSON(s.receiptPath(request.RequestID), &persisted); e == nil && !absent {
			receipt.Stage = persisted.Stage
			receipt.ApplicationID = persisted.ApplicationID
			receipt.PermissionRevision = persisted.PermissionRevision
		}
		receipt.Outcome, receipt.Message = result.Outcome, result.Message
		if request.FlowID != "" {
			receipt.FlowID = request.FlowID
		} else if result.receiptFlowID != "" {
			receipt.FlowID = result.receiptFlowID
		} else if result.Snapshot.Flow != nil && (request.Action == "create_app" || request.Action == "start_auth") {
			receipt.FlowID = result.Snapshot.Flow.ID
		}
		if result.Code != "" {
			receipt.Code = result.Code
		}
		if result.Outcome == "completed" {
			receipt.Stage = "verified"
		} else if result.Outcome == "unknown" {
			receipt.Stage = "submitted"
		}
		if request.Action == "logout" && result.Outcome == "completed" {
			// Scrub the current receipt before walking older receipts. If any
			// later scrub fails, restore the fail-closed cleanup journal so the
			// desktop offers a safe, idempotent "continue cleanup" path.
			if e := s.saveConfigurationReceipt(receipt); e != nil {
				_ = managedfeishu.BeginLocalFeishuCleanup(s.feishuDataRoot)
				return e
			}
			if e := s.scrubConfigurationReceiptsAfterLogout(request.RequestID); e != nil {
				_ = managedfeishu.BeginLocalFeishuCleanup(s.feishuDataRoot)
				return e
			}
			return nil
		}
		if result.Outcome == "completed" && (request.Action == "finish_app" || request.Action == "finish_auth") {
			if e := s.saveConfigurationReceipt(receipt); e != nil {
				return e
			}
			origin := "create_app"
			if request.Action == "finish_auth" {
				origin = "start_auth"
			}
			return s.completeConfigurationFlowReceipt(origin, request.FlowID)
		}
		if e := s.saveConfigurationReceipt(receipt); e != nil {
			return e
		}
		return nil
	})
	if err != nil {
		return ConfigurationActionResult{Outcome: "unknown", Snapshot: s.ReadFeishuConfiguration(ctx, false), Message: "操作记录未能确认，请查询原请求结果；不会重复执行。"}, nil
	}
	return result, nil
}

func (s *Service) scrubConfigurationReceiptsAfterLogout(currentRequestID string) error {
	root := filepath.Join(s.feishuDataRoot, "configuration-receipts-v1")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(root, entry.Name())
		var receipt ConfigurationReceipt
		missing, err := privatestore.ReadJSON(path, &receipt)
		if err != nil {
			return err
		}
		if missing {
			return errors.New("configuration_receipt_disappeared")
		}
		if receipt.RequestID == currentRequestID {
			continue
		}
		receipt.ApplicationID, receipt.FlowID, receipt.PermissionRevision, receipt.ContextRevision, receipt.Digest = "", "", "", "", ""
		if configurationFlowAction(receipt.Action) && (receipt.Outcome == "pending" || receipt.Outcome == "unknown") {
			receipt.Outcome, receipt.Stage, receipt.Code = "failed", "verified", "cancelled_by_logout"
			receipt.Message = "连接流程已随注销终止，不会重放。"
		}
		if err := privatestore.WriteJSON(path, receipt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) completeConfigurationFlowReceipt(originAction, flowID string) error {
	if (originAction != "create_app" && originAction != "start_auth") || strings.TrimSpace(flowID) == "" {
		return errors.New("configuration_flow_receipt_invalid")
	}
	root := filepath.Join(s.feishuDataRoot, "configuration-receipts-v1")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(root, entry.Name())
		var receipt ConfigurationReceipt
		missing, err := privatestore.ReadJSON(path, &receipt)
		if err != nil {
			return err
		}
		if missing {
			return errors.New("configuration_flow_receipt_missing")
		}
		if receipt.Action != originAction || receipt.FlowID != flowID || (receipt.Outcome != "pending" && receipt.Outcome != "unknown") {
			continue
		}
		receipt.Outcome, receipt.Stage, receipt.Code = "completed", "verified", ""
		receipt.Message = "后续核验已完成，本次连接流程不会再次执行。"
		if err := s.saveConfigurationReceipt(receipt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) ConfigurationResult(ctx context.Context, id string) (ConfigurationActionResult, error) {
	if id == "" || len(id) > 128 {
		return ConfigurationActionResult{}, errors.New("configuration_invalid_request")
	}
	var result ConfigurationActionResult
	err := privatestore.WithFileLock(s.receiptPath(id)+".lock", func() error { var err error; result, err = s.configurationResult(ctx, id); return err })
	return result, err
}
func (s *Service) configurationResult(ctx context.Context, id string) (ConfigurationActionResult, error) {
	if id == "" || len(id) > 128 {
		return ConfigurationActionResult{}, errors.New("configuration_invalid_request")
	}
	var r ConfigurationReceipt
	missing, err := privatestore.ReadJSON(s.receiptPath(id), &r)
	if err != nil {
		return ConfigurationActionResult{}, err
	}
	snapshot := s.ReadFeishuConfiguration(ctx, false)
	if missing {
		return ConfigurationActionResult{Outcome: "failed", Snapshot: snapshot, Message: "未找到本次操作的接收记录；不会自动重发。"}, nil
	}
	if configurationFlowEndedByLogout(r, s.loadConfigurationReceipts()) {
		return ConfigurationActionResult{Outcome: "failed", Snapshot: snapshot, Message: "此登录流程已随之后的注销结束，请发起新的登录。"}, nil
	}
	if (r.Outcome == "unknown" || r.Outcome == "pending") && r.Stage == "submitted" {
		if r.ApplicationID != "" && (r.Action == "logout" || r.Action == "start_auth" || r.Action == "finish_auth") {
			if evidence, err := s.readConfigurationEvidence(ctx); err == nil && evidence.ApplicationID == r.ApplicationID && evidence.Auth != nil {
				verified := r.Action == "logout" && evidence.Auth.Status == "unauthorized"
				if r.Action != "logout" {
					verified = confirmedFeishuUserAuth(evidence.Auth) && evidence.UserPermissions == "present" && evidence.ApplicationPermissions == "present"
				}
				if verified {
					r.Outcome, r.Stage, r.Message = "completed", "verified", "已重新核验本次操作对应的授权状态。"
					if err := s.saveConfigurationReceipt(r); err != nil {
						return ConfigurationActionResult{}, err
					}
				}
			}
		}
		// Query the same durable transport ledger; this never calls a write API.
		if r.Action == "test_message" && s.managedFeishuSupervisor != nil {
			var receipt struct {
				Outcome   string `json:"outcome"`
				MessageID string `json:"messageId"`
			}
			if err := s.managedFeishuSupervisor.Call(ctx, "bridge/message/test/result", map[string]any{"requestId": id}, &receipt); err == nil && (receipt.Outcome == "failed" || receipt.Outcome == "completed" && receipt.MessageID != "") {
				r.Outcome, r.Stage, r.Message = "completed", "verified", "测试消息已发送，已核对原请求的消息回执。"
				if receipt.Outcome == "failed" {
					r.Outcome = "failed"
					r.Message = "原请求已明确失败；请查看消息操作诊断。"
					r.Code = "transport_operation_failed"
				}
				if err := s.saveConfigurationReceipt(r); err != nil {
					return ConfigurationActionResult{}, err
				}
			}
		}
	}
	if r.Action == "restart" && r.RecoveryVerifiedAt != "" {
		return ConfigurationActionResult{Outcome: "completed", Snapshot: snapshot, Message: recoveryVerifiedMessage}, nil
	}
	if strings.TrimSpace(r.Action) == "" {
		return ConfigurationActionResult{}, errors.New("configuration_receipt_invalid")
	}
	return ConfigurationActionResult{Outcome: r.Outcome, Snapshot: snapshot, Message: r.Message}, nil
}

func (s *Service) loadConfigurationReceipts() []ConfigurationReceipt {
	entries, _ := os.ReadDir(filepath.Join(s.feishuDataRoot, "configuration-receipts-v1"))
	var result []ConfigurationReceipt
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		var r ConfigurationReceipt
		if _, err := privatestore.ReadJSON(filepath.Join(s.feishuDataRoot, "configuration-receipts-v1", e.Name()), &r); err == nil && r.SchemaVersion == 1 {
			result = append(result, r)
		}
	}
	return result
}

func configurationFlowAction(action string) bool {
	switch action {
	case "create_app", "finish_app", "start_auth", "finish_auth", "cancel_flow":
		return true
	default:
		return false
	}
}

// A verified later logout is a product-wide disconnect and therefore ends
// every earlier unresolved application or authorization flow. This does not
// prove the old request succeeded; it only makes the old operation terminal
// and non-replayable after all local credentials and bindings were removed.
func configurationFlowEndedByLogout(r ConfigurationReceipt, all []ConfigurationReceipt) bool {
	if !configurationFlowAction(r.Action) || (r.Outcome != "pending" && r.Outcome != "unknown") {
		return false
	}
	started, err := time.Parse(time.RFC3339Nano, r.UpdatedAt)
	if err != nil {
		return false
	}
	for _, later := range all {
		if later.Action != "logout" || later.Outcome != "completed" || later.Stage != "verified" {
			continue
		}
		ended, err := time.Parse(time.RFC3339Nano, later.UpdatedAt)
		if err == nil && ended.After(started) {
			return true
		}
	}
	return false
}

// Old builds did not persist the root flow ID. Configuration actions were
// serialized and a pending root blocked another root action, so exactly one
// verified matching finish within the session lifetime is sufficient legacy
// evidence. Multiple candidates remain unresolved instead of being guessed.
func configurationFlowCompletedByCheck(r ConfigurationReceipt, all []ConfigurationReceipt) (string, bool) {
	finishAction := ""
	switch r.Action {
	case "create_app":
		finishAction = "finish_app"
	case "start_auth":
		finishAction = "finish_auth"
	default:
		return "", false
	}
	if r.Outcome != "pending" && r.Outcome != "unknown" {
		return "", false
	}
	started, err := time.Parse(time.RFC3339Nano, r.UpdatedAt)
	if err != nil {
		return "", false
	}
	candidates := map[string]bool{}
	for _, later := range all {
		if later.Action != finishAction || later.Outcome != "completed" || later.Stage != "verified" || strings.TrimSpace(later.FlowID) == "" {
			continue
		}
		finished, err := time.Parse(time.RFC3339Nano, later.UpdatedAt)
		if err != nil || !finished.After(started) || finished.Sub(started) > 10*time.Minute {
			continue
		}
		if r.FlowID != "" && later.FlowID != r.FlowID {
			continue
		}
		candidates[later.FlowID] = true
	}
	if len(candidates) != 1 {
		return "", false
	}
	for flowID := range candidates {
		return flowID, true
	}
	return "", false
}

// A later verified operation against an application proves that the product
// has entered a new connected lifecycle after an unresolved logout.  It does
// not tell us whether the old logout itself succeeded, so keep that history as
// resolved rather than completed; it must no longer lock out a fresh,
// explicitly requested logout forever.
func configurationLogoutSupersededByConnection(r ConfigurationReceipt, all []ConfigurationReceipt) bool {
	if r.Action != "logout" || (r.Outcome != "pending" && r.Outcome != "unknown") {
		return false
	}
	started, err := time.Parse(time.RFC3339Nano, r.UpdatedAt)
	if err != nil {
		return false
	}
	for _, later := range all {
		if later.Outcome != "completed" || later.Stage != "verified" || strings.TrimSpace(later.ApplicationID) == "" {
			continue
		}
		switch later.Action {
		case "finish_app", "finish_auth", "test_message":
		default:
			continue
		}
		verified, err := time.Parse(time.RFC3339Nano, later.UpdatedAt)
		if err == nil && verified.After(started) {
			return true
		}
	}
	return false
}

func (s *Service) configurationFailures() []ConfigurationReceipt {
	all := s.loadConfigurationReceipts()
	result := make([]ConfigurationReceipt, 0, len(all))
	for _, r := range all {
		if configurationFlowEndedByLogout(r, all) || configurationLogoutSupersededByConnection(r, all) {
			continue
		}
		r.Title = configurationOperationTitle(r.Action)
		r.StatusText = map[string]string{"completed": "成功", "failed": "明确失败", "pending": "执行中", "unknown": "结果待核实"}[r.Outcome]
		r.StageText = map[string]string{"validating": "提交前校验", "submitted": "已提交，核验回执", "verified": "核验完成"}[r.Stage]
		if r.Action == "restart" && r.RecoveryVerifiedAt != "" {
			r.Outcome, r.StatusText, r.Message = "resolved", "连接已恢复", recoveryVerifiedMessage
		}
		r.Digest, r.ApplicationID, r.FlowID = "", "", ""
		result = append(result, r)
	}

	sort.Slice(result, func(i, j int) bool { return result[i].UpdatedAt > result[j].UpdatedAt })
	filtered := result[:0]
	seen := map[string]bool{}
	for _, r := range result {
		if seen[r.Action] {
			continue
		}
		seen[r.Action] = true
		if r.Outcome != "completed" {
			filtered = append(filtered, r)
		}
	}
	result = filtered
	if len(result) > 10 {
		result = result[:10]
	}
	return result
}

// Older builds could leave the root create_app/start_auth receipt pending after
// the matching flow completed and was later logged out.  The temporal logout
// projection above already keeps that stale receipt from blocking the UI; this
// migration also makes the durable record terminal, scrubbed and non-replayable
// so the correction survives future changes to presentation code.
func (s *Service) persistLegacyConfigurationFlowOutcomes() {
	all := s.loadConfigurationReceipts()
	for _, receipt := range all {
		endedByLogout := configurationFlowEndedByLogout(receipt, all)
		completedFlowID, completedByCheck := configurationFlowCompletedByCheck(receipt, all)
		if !endedByLogout && !completedByCheck {
			continue
		}
		path := s.receiptPath(receipt.RequestID)
		_ = privatestore.WithFileLock(path+".lock", func() error {
			var current ConfigurationReceipt
			missing, err := privatestore.ReadJSON(path, &current)
			if err != nil {
				return err
			}
			if missing {
				return errors.New("configuration_flow_receipt_missing")
			}
			currentReceipts := s.loadConfigurationReceipts()
			endedByLogout = configurationFlowEndedByLogout(current, currentReceipts)
			completedFlowID, completedByCheck = configurationFlowCompletedByCheck(current, currentReceipts)
			if !endedByLogout && !completedByCheck {
				return nil
			}
			current.ApplicationID, current.FlowID, current.PermissionRevision, current.ContextRevision, current.Digest = "", "", "", "", ""
			current.Stage = "verified"
			if endedByLogout {
				current.Outcome, current.Code = "failed", "cancelled_by_logout"
				current.Message = "连接流程已随注销终止，不会重放。"
			} else {
				current.Outcome, current.Code, current.FlowID = "completed", "", completedFlowID
				current.Message = "后续核验已完成，本次连接流程不会再次执行。"
			}
			return privatestore.WriteJSON(path, current)
		})
	}
}

func (s *Service) persistSupersededLogoutOutcomes() {
	all := s.loadConfigurationReceipts()
	for _, receipt := range all {
		if !configurationLogoutSupersededByConnection(receipt, all) {
			continue
		}
		path := s.receiptPath(receipt.RequestID)
		_ = privatestore.WithFileLock(path+".lock", func() error {
			var current ConfigurationReceipt
			missing, err := privatestore.ReadJSON(path, &current)
			if err != nil {
				return err
			}
			if missing {
				return errors.New("configuration_logout_receipt_missing")
			}
			if !configurationLogoutSupersededByConnection(current, s.loadConfigurationReceipts()) {
				return nil
			}
			current.ApplicationID, current.FlowID, current.PermissionRevision, current.ContextRevision, current.Digest = "", "", "", "", ""
			current.Outcome, current.Stage, current.Code = "resolved", "verified", "superseded_by_connection"
			current.Message = "后续连接已核验可用；原注销请求不再阻止再次注销。"
			return privatestore.WriteJSON(path, current)
		})
	}
}

// A configuration session lives only in the supervised Bridge process.  If
// that process restarts, a durable root receipt can survive while the session
// (and its device code) cannot.  Once a fresh Bridge read proves that there is
// no live flow and durable configuration evidence proves that the requested
// identity was not established, make the old receipt terminal.  This never
// replays the remote write: a new attempt still requires an explicit click.
func (s *Service) retireInterruptedConfigurationFlowReceipts(data configurationData) {
	if data.quickFailed || data.flowFailed || data.evidenceFailed || data.invalidated || data.flow != nil || data.quickAt.IsZero() || data.evidenceAt.IsZero() {
		return
	}
	for _, receipt := range s.loadConfigurationReceipts() {
		interrupted := receipt.Stage == "submitted" && (receipt.Outcome == "pending" || receipt.Outcome == "unknown") && strings.TrimSpace(receipt.FlowID) != ""
		switch receipt.Action {
		case "create_app":
			interrupted = interrupted && data.evidence.ApplicationState == "missing"
		case "start_auth":
			interrupted = interrupted && data.evidence.ApplicationState == "present" && data.evidence.OperatorState == "missing"
		default:
			interrupted = false
		}
		if !interrupted {
			continue
		}
		path := s.receiptPath(receipt.RequestID)
		_ = privatestore.WithFileLock(path+".lock", func() error {
			var current ConfigurationReceipt
			missing, err := privatestore.ReadJSON(path, &current)
			if err != nil || missing {
				return err
			}
			if current.Action != receipt.Action || current.Stage != "submitted" || (current.Outcome != "pending" && current.Outcome != "unknown") || strings.TrimSpace(current.FlowID) == "" {
				return nil
			}
			current.ApplicationID, current.FlowID, current.PermissionRevision, current.ContextRevision, current.Digest = "", "", "", "", ""
			current.Outcome, current.Stage, current.Code = "failed", "verified", "configuration_flow_interrupted"
			current.Message = "扫码会话已因程序重启结束，且未形成可用连接；可以重新扫码，不会自动重放。"
			return privatestore.WriteJSON(path, current)
		})
	}
}

func (s *Service) reconcileConfigurationReceipts() {
	s.persistLegacyConfigurationFlowOutcomes()
	s.persistSupersededLogoutOutcomes()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	for _, receipt := range s.configurationFailures() {
		if ctx.Err() != nil {
			return
		}
		if receipt.Outcome == "unknown" || receipt.Outcome == "pending" {
			_, _ = s.ConfigurationResult(ctx, receipt.RequestID)
		}
	}
}

func configurationOperationTitle(action string) string {
	title := map[string]string{"start_auth": "补充本人授权", "finish_auth": "核验用户授权", "logout": "注销并清除飞书", "test_message": "向我发送测试消息", "create_app": "扫码连接飞书", "finish_app": "核验应用", "restart": "恢复连接", "cancel_flow": "取消授权等待"}[action]
	if title == "" {
		return "配置操作"
	}
	return title
}

const recoveryVerifiedMessage = "已重新检查，连接已恢复；原恢复请求的执行结果仍保留在历史记录中。"

// Closing a recovery incident is not proof that the original restart succeeded.
// Preserve its outcome, error and timestamp, and only add observed recovery evidence.
func (s *Service) reconcileConnectionRecovery(data configurationData, snapshot ConfigurationSnapshot) {
	now := time.Now()
	if data.quickFailed || data.evidenceFailed || data.invalidated || data.quickAt.IsZero() || now.Sub(data.quickAt) > 10*time.Second || data.evidenceAt.IsZero() || now.Sub(data.evidenceAt) > 2*time.Minute || data.evidence.ApplicationID == "" {
		return
	}
	healthy := false
	for _, fact := range snapshot.Facts {
		if fact.ID == "taskConnection" && fact.State == "present" {
			healthy = true
		}
	}
	if !healthy {
		return
	}
	for _, r := range s.loadConfigurationReceipts() {
		if r.Action != "restart" || r.RecoveryVerifiedAt != "" || r.Stage != "submitted" || (r.Outcome != "unknown" && r.Outcome != "pending" && r.Outcome != "failed") || r.ApplicationID != data.evidence.ApplicationID {
			continue
		}
		submitted, err := time.Parse(time.RFC3339Nano, r.UpdatedAt)
		if err != nil || !data.quickAt.After(submitted) || !data.evidenceAt.After(submitted) {
			continue
		}
		_ = privatestore.WithFileLock(s.receiptPath(r.RequestID)+".lock", func() error {
			var current ConfigurationReceipt
			missing, err := privatestore.ReadJSON(s.receiptPath(r.RequestID), &current)
			if err != nil || missing {
				return err
			}
			if current.UpdatedAt != r.UpdatedAt || current.RecoveryVerifiedAt != "" {
				return nil
			}
			current.RecoveryVerifiedAt = data.quickAt.UTC().Format(time.RFC3339Nano)
			return privatestore.WriteJSON(s.receiptPath(r.RequestID), current)
		})
	}
}
