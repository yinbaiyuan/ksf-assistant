package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ksfassistant/core/internal/privatestore"
)

type ConfigurationReceipt struct {
	RecoveryVerifiedAt string `json:"recoveryVerifiedAt,omitempty"`
	PermissionRevision string `json:"permissionRevision,omitempty"`
	Title              string `json:"title,omitempty"`
	StatusText         string `json:"statusText,omitempty"`
	StageText          string `json:"stageText,omitempty"`
	ApplicationID      string `json:"applicationId,omitempty"`
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
			if authorizationEndedByLogout(receipt, s.loadConfigurationReceipts()) {
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
		if result.Code != "" {
			receipt.Code = result.Code
		}
		if result.Outcome == "completed" {
			receipt.Stage = "verified"
		} else if result.Outcome == "unknown" {
			receipt.Stage = "submitted"
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
	if authorizationEndedByLogout(r, s.loadConfigurationReceipts()) {
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

// A verified later logout ends that application's earlier login flow. This is
// lifecycle reconciliation, not proof that the old request succeeded; preserve
// original receipts and never resend their operations.
func authorizationEndedByLogout(r ConfigurationReceipt, all []ConfigurationReceipt) bool {
	if (r.Action != "start_auth" && r.Action != "finish_auth") || (r.Outcome != "pending" && r.Outcome != "unknown") || r.ApplicationID == "" {
		return false
	}
	started, err := time.Parse(time.RFC3339Nano, r.UpdatedAt)
	if err != nil {
		return false
	}
	for _, later := range all {
		if later.Action != "logout" || later.Outcome != "completed" || later.Stage != "verified" || later.ApplicationID != r.ApplicationID {
			continue
		}
		ended, err := time.Parse(time.RFC3339Nano, later.UpdatedAt)
		if err == nil && ended.After(started) {
			return true
		}
	}
	return false
}

func (s *Service) configurationFailures() []ConfigurationReceipt {
	all := s.loadConfigurationReceipts()
	result := make([]ConfigurationReceipt, 0, len(all))
	for _, r := range all {
		if authorizationEndedByLogout(r, all) {
			continue
		}
		r.Title = configurationOperationTitle(r.Action)
		r.StatusText = map[string]string{"completed": "成功", "failed": "明确失败", "pending": "执行中", "unknown": "结果待核实"}[r.Outcome]
		r.StageText = map[string]string{"validating": "提交前校验", "submitted": "已提交，核验回执", "verified": "核验完成"}[r.Stage]
		if r.Action == "restart" && r.RecoveryVerifiedAt != "" {
			r.Outcome, r.StatusText, r.Message = "resolved", "连接已恢复", recoveryVerifiedMessage
		}
		r.Digest, r.ApplicationID = "", ""
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

func (s *Service) reconcileConfigurationReceipts() {
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
	title := map[string]string{"start_auth": "用户授权", "finish_auth": "核验用户授权", "logout": "注销用户授权", "test_message": "向我发送测试消息", "connect_app": "接入应用", "create_app": "创建应用", "finish_app": "核验应用", "bind_operator": "绑定本人控制", "restart": "恢复连接", "cancel_flow": "取消授权等待"}[action]
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
