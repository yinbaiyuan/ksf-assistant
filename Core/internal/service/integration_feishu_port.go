package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"time"

	"ksfassistant/core/internal/domain"
	"ksfassistant/core/internal/feishu"
	"ksfassistant/core/internal/feishuprotocol"
	"ksfassistant/core/internal/feishutypes"
	"ksfassistant/core/internal/integration"
	"ksfassistant/core/internal/privateipc"
)

type integrationFeishuPort struct{ service *Service }

var _ integration.NativeTaskCardPort = integrationFeishuPort{}

// The switch applies only when establishing a new task link. Existing native
// bindings retain their transport through shutdown/restart and feature rollback.
func (port integrationFeishuPort) NativeTaskCardsEnabled() bool {
	return os.Getenv("KSFASSISTANT_DISABLE_CARDKIT") != "1"
}

func (port integrationFeishuPort) CleanupInbound(ctx context.Context, directory string) error {
	var result map[string]bool
	return port.call(ctx, feishuprotocol.MediaCleanup, map[string]string{"directory": directory}, &result)
}

func (port integrationFeishuPort) call(ctx context.Context, method string, request, result any) error {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if port.service.managedFeishuSupervisor == nil {
		return errors.New("KSFAssistant Feishu service unavailable")
	}
	err := port.service.managedFeishuSupervisor.Call(ctx, method, request, result)
	var rpc *privateipc.RPCError
	if errors.As(err, &rpc) && rpc.Code == -32064 {
		var data struct {
			RetryAfterMs int64 `json:"retryAfterMs"`
		}
		if json.Unmarshal(rpc.Data, &data) == nil && data.RetryAfterMs > 0 {
			return &cardRetryError{err: err, delay: time.Duration(data.RetryAfterMs) * time.Millisecond}
		}
	}
	return err
}

func (port integrationFeishuPort) Record(event string, fields map[string]any) error {
	var result map[string]bool
	return port.call(context.Background(), feishuprotocol.AuditRecord, feishuprotocol.AuditRequest{Event: event, Fields: fields}, &result)
}
func (port integrationFeishuPort) Send(ctx context.Context, target feishutypes.MessageTarget, format, content, key string) (string, error) {
	var result feishuprotocol.MessageResult
	err := port.call(ctx, feishuprotocol.MessageSend, feishuprotocol.MessageRequest{TargetType: target.Type, TargetID: target.ID, Format: format, Content: content, IdempotencyKey: key}, &result)
	return result.MessageID, err
}
func (port integrationFeishuPort) Reply(ctx context.Context, messageID, format, content, key string) (string, error) {
	var result feishuprotocol.MessageResult
	err := port.call(ctx, feishuprotocol.MessageReply, feishuprotocol.MessageRequest{MessageID: messageID, Format: format, Content: content, IdempotencyKey: key}, &result)
	return result.MessageID, err
}
func (port integrationFeishuPort) PatchCard(ctx context.Context, messageID, card string) error {
	var result map[string]bool
	return port.call(ctx, feishuprotocol.CardPatch, feishuprotocol.CardRequest{MessageID: messageID, Content: card}, &result)
}
func (port integrationFeishuPort) config() (feishutypes.ClientConfig, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var result feishutypes.ClientConfig
	err := port.call(ctx, feishuprotocol.ConfigRead, map[string]any{}, &result)
	return result, err
}
func (port integrationFeishuPort) ResolveMessageTarget(alias string) (feishutypes.MessageTarget, error) {
	config, err := port.config()
	if err != nil {
		return feishutypes.MessageTarget{}, err
	}
	return config.ResolveMessageTarget(alias)
}
func (port integrationFeishuPort) AliasForOpenID(openID string) string {
	config, err := port.config()
	if err != nil {
		return ""
	}
	for _, alias := range config.DirectAllowedAliases {
		if target := config.MessageTargets[alias]; target.Type == "open_id" && target.ID == openID {
			return alias
		}
	}
	return ""
}
func (port integrationFeishuPort) StageInbound(ctx context.Context, message integration.InboundMessage, maxBytes int64) (integration.StagedInboundMessage, error) {
	payload, err := json.Marshal(message)
	if err != nil {
		return integration.StagedInboundMessage{}, err
	}
	var result integration.StagedInboundMessage
	err = port.call(ctx, feishuprotocol.MediaStage, feishuprotocol.MediaRequest{Message: payload, MaxBytes: maxBytes}, &result)
	return result, err
}

type remoteSettingsStore struct{ service *Service }

func (store remoteSettingsStore) Load() (feishu.Settings, error) {
	var result feishu.Settings
	err := (integrationFeishuPort{store.service}).call(context.Background(), feishuprotocol.SettingsRead, map[string]any{}, &result)
	return result, err
}
func (store remoteSettingsStore) Save(value feishu.Settings) error {
	var result feishu.Settings
	return (integrationFeishuPort{store.service}).call(context.Background(), feishuprotocol.SettingsWrite, value, &result)
}

type remoteSetupStore struct{ service *Service }

func (store remoteSetupStore) Load() (feishu.SetupState, error) {
	var result feishu.SetupState
	err := (integrationFeishuPort{store.service}).call(context.Background(), feishuprotocol.SetupRead, map[string]any{}, &result)
	return result, err
}
func (store remoteSetupStore) Save(value feishu.SetupState) error {
	var result feishu.SetupState
	return (integrationFeishuPort{store.service}).call(context.Background(), feishuprotocol.SetupWrite, value, &result)
}

func publicIntegrationLink(value integration.PublicTaskLink) domain.FeishuTaskLink {
	var result domain.FeishuTaskLink
	encoded, _ := json.Marshal(value)
	_ = json.Unmarshal(encoded, &result)
	return result
}

func (service *Service) composeIntegrationSnapshot(snapshot domain.FeishuSnapshot) domain.FeishuSnapshot {
	snapshot = normalizedFeishuSnapshot(snapshot)
	capabilities := make(map[string]domain.CapabilityHealth, len(snapshot.Capabilities)+4)
	for key, value := range snapshot.Capabilities {
		capabilities[key] = value
	}
	snapshot.Capabilities = capabilities
	snapshot.ReadinessBlockers = append([]string{}, snapshot.ReadinessBlockers...)
	snapshot.TaskLinkProtocolVersion = 2
	snapshot.ConnectedTaskCount = nil
	if service.integrationRuntime != nil {
		file, err := service.integrationRuntime.Store().Load()
		if err != nil {
			snapshot.TaskLinkReady = false
			snapshot.ReadinessBlockers = append(snapshot.ReadinessBlockers, "taskLinks")
		} else {
			snapshot.Links = []domain.FeishuTaskLink{}
			count := 0
			for _, link := range integration.PublicLinks(file.Links) {
				snapshot.Links = append(snapshot.Links, publicIntegrationLink(link))
				if link.LinkState == "active" {
					count++
				}
			}
			snapshot.ConnectedTaskCount = &count
			snapshot.TaskLinkReady = snapshot.Availability == "ready"
		}
	}
	if snapshot.Capabilities == nil {
		snapshot.Capabilities = map[string]domain.CapabilityHealth{}
	}
	for key, value := range service.privateCapabilities().Capabilities {
		snapshot.Capabilities[key] = domain.CapabilityHealth{State: value.State, Detail: value.Detail}
	}
	if snapshot.Capabilities["desktopIPC"].State != "ready" {
		snapshot.TaskLinkReady = false
		blocked := false
		for _, blocker := range snapshot.ReadinessBlockers {
			if blocker == "desktopIPC" {
				blocked = true
				break
			}
		}
		if !blocked {
			snapshot.ReadinessBlockers = append(snapshot.ReadinessBlockers, "desktopIPC")
		}
	}
	if service.integrationRuntime != nil {
		health := service.integrationRuntime.Health()
		snapshot.Capabilities["businessIntegration"] = domain.CapabilityHealth{State: health.State, Detail: health.Detail}
		if !service.integrationRuntime.CanCreateTaskLink() {
			snapshot.TaskLinkReady = false
			snapshot.ReadinessBlockers = append(snapshot.ReadinessBlockers, "businessIntegration")
		}
	} else {
		snapshot.Capabilities["businessIntegration"] = domain.CapabilityHealth{State: "unavailable", Detail: "Core integration unavailable"}
		snapshot.TaskLinkReady = false
		snapshot.ReadinessBlockers = append(snapshot.ReadinessBlockers, "businessIntegration")
	}
	for _, blocker := range snapshot.ReadinessBlockers {
		if blocker == "taskCardWriteDisabled" || blocker == "taskCardWriteDryRun" {
			snapshot.TaskLinkReady = false
		}
	}
	return snapshot
}

type cardRetryError struct {
	err   error
	delay time.Duration
}

func (e *cardRetryError) Error() string             { return e.err.Error() }
func (e *cardRetryError) Unwrap() error             { return e.err }
func (e *cardRetryError) RetryDelay() time.Duration { return e.delay }
