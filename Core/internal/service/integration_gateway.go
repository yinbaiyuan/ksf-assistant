package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"ksfassistant/core/internal/domain"
	"ksfassistant/core/internal/feishu"
	"ksfassistant/core/internal/feishucli"
	"ksfassistant/core/internal/feishuprotocol"
	"ksfassistant/core/internal/integration"
	"ksfassistant/core/internal/localipc"
	"ksfassistant/core/internal/privateipc"
)

func (service *Service) initializeBusinessIntegration() error {
	if service.integrationRuntime != nil {
		return nil
	}
	if service.localGateway == nil {
		gateway, err := localipc.Listen(service.feishuDataRoot, feishucli.NewUploadHandler(privateipc.HandlerFunc(service.handleLocalRPC), feishucli.MethodExecute))
		if err != nil {
			return err
		}
		service.localGateway = gateway
	}
	runtime, err := integration.NewRuntime(service.feishuDataRoot, integrationFeishuPort{service}, newCoreCapabilityClient(service))
	if err != nil {
		return err
	}
	service.integrationRuntime = runtime
	return nil
}

func (service *Service) connectManagedBridge(ctx context.Context, generation uint64) error {
	if service.managedFeishuSupervisor == nil || !service.managedFeishuSupervisor.IsCurrentGeneration(generation) {
		return feishu.ErrStaleGeneration
	}
	service.mu.Lock()
	if generation < service.feishuGeneration {
		service.mu.Unlock()
		return feishu.ErrStaleGeneration
	}
	service.feishuGeneration = generation
	service.lastFeishu = domain.FeishuSnapshot{}
	service.lastFeishuAt = time.Time{}
	service.mu.Unlock()
	if err := service.initializeManagedBridge(ctx); err != nil {
		return err
	}
	var snapshot feishuprotocol.Snapshot
	if err := service.managedFeishuSupervisor.Call(ctx, feishuprotocol.SnapshotRead, map[string]any{}, &snapshot); err != nil {
		return err
	}
	if !service.managedFeishuSupervisor.IsCurrentGeneration(generation) {
		return feishu.ErrStaleGeneration
	}
	service.managedFeishuSupervisor.SetConfigured(snapshot.Configured)
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	if _, err = service.HandlePrivateRPC(feishu.WithEpoch(ctx, generation), feishuprotocol.SnapshotPush, encoded); err != nil {
		return err
	}
	if service.integrationRuntime != nil {
		_ = service.integrationRuntime.ResumeActive()
	}
	return nil
}

func (service *Service) handleLocalRPC(ctx context.Context, method string, params json.RawMessage) (any, error) {
	if method != feishucli.MethodExecute {
		return nil, privateipc.ErrMethodNotFound
	}
	request, err := feishucli.DecodeRequest(params)
	if err != nil {
		return nil, err
	}
	if request.Command == "task-link" {
		return service.taskLinkCommand(ctx, request)
	}
	if request.Command == "snapshot" {
		return service.readFeishu(ctx, time.Now()), nil
	}
	var result json.RawMessage
	if service.managedFeishuSupervisor == nil {
		return nil, errors.New("KSFAssistant Feishu service unavailable")
	}
	ctx = feishu.WithEpoch(ctx, service.managedFeishuSupervisor.Generation())
	err = feishucli.CallRequest(ctx, service.managedFeishuSupervisor.Call, feishuprotocol.ClientExecute, request, &result)
	if err == nil && (request.Command == "profile" && request.Action == "set" || request.Command == "auth" && (request.Action == "configure-existing" || request.Action == "finish")) {
		err = service.restartFeishuSupervisor(ctx)
	}
	return result, err
}

func (service *Service) taskLinkCommand(ctx context.Context, request feishucli.Request) (any, error) {
	runtime := service.integrationRuntime
	if runtime == nil {
		return nil, errors.New("Core business integration unavailable")
	}
	store := runtime.Store()
	switch request.Action {
	case "protocol":
		settings, err := (remoteSettingsStore{service}).Load()
		blockers := []string{}
		if err != nil {
			blockers = append(blockers, "feishuServiceUnavailable")
		} else if !settings.Outbound.Enabled {
			blockers = append(blockers, "outbound")
		}
		if settings.Outbound.DryRun {
			blockers = append(blockers, "dryRun")
		}
		return map[string]any{"status": "ok", "protocol": integration.TaskLinkProtocol, "version": 2, "schemaVersion": 2, "readiness": map[string]any{"ready": len(blockers) == 0, "blockers": blockers}}, nil
	case "list":
		file, err := store.Load()
		if err != nil {
			return nil, err
		}
		return map[string]any{"status": "ok", "protocol": integration.TaskLinkProtocol, "version": 2, "links": integration.PublicLinks(file.Links)}, nil
	case "create":
		var input integration.CreateTaskLinkRequest
		if err := privateipc.DecodeStrict(request.Payloads["payload-file"], &input, true); err != nil {
			return nil, err
		}
		link, err := runtime.CreateTaskLink(ctx, input)
		return map[string]any{"status": "ok", "protocol": integration.TaskLinkProtocol, "version": 2, "link": link}, err
	case "status", "sync":
		link, found, err := store.FindAnyByTaskKey(request.Options["task-key"])
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, errors.New("task link not found")
		}
		if request.Action == "sync" {
			err = integration.SyncTaskLinkCard(ctx, store, integrationFeishuPort{service}, link, "")
		}
		result := map[string]any{"status": "ok", "protocol": integration.TaskLinkProtocol, "version": 2, "link": integration.PublicLinks([]integration.TaskLink{link})[0]}
		if request.Action == "sync" {
			result["cardSync"] = cardSyncStatus(request.Action, err)
		}
		return result, err
	case "release":
		link, err := runtime.Release(ctx, request.Options["task-key"])
		return map[string]any{"status": "released", "protocol": integration.TaskLinkProtocol, "version": 2, "cardSync": cardSyncStatus("sync", err), "link": link}, err
	case "interrupt":
		link, err := runtime.Interrupt(ctx, request.Options["task-key"])
		return map[string]any{"status": "interrupted", "protocol": integration.TaskLinkProtocol, "version": 2, "cardSync": cardSyncStatus("sync", err), "link": link}, err
	default:
		return nil, errors.New("unsupported task-link action")
	}
}

func cardSyncStatus(action string, err error) string {
	if action != "sync" {
		return "not_required"
	}
	if err != nil {
		return "pending"
	}
	return "synced"
}

func (service *Service) fetchFeishuSnapshot(ctx context.Context) (domain.FeishuSnapshot, error) {
	generation := service.managedFeishuSupervisor.Generation()
	ctx = feishu.WithEpoch(ctx, generation)
	var wire feishuprotocol.Snapshot
	if err := service.managedFeishuSupervisor.Call(ctx, feishuprotocol.SnapshotRead, map[string]any{}, &wire); err != nil {
		return domain.FeishuSnapshot{}, err
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return domain.FeishuSnapshot{}, err
	}
	if _, err := service.HandlePrivateRPC(ctx, feishuprotocol.SnapshotPush, encoded); err != nil {
		return domain.FeishuSnapshot{}, err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	return service.lastFeishu, nil
}

func (service *Service) bridgeInitializeRequest() feishuprotocol.InitializeRequest {
	request := feishuprotocol.InitializeRequest{Protocol: feishuprotocol.Protocol}
	if service.integrationRuntime == nil {
		return request
	}
	file, err := service.integrationRuntime.Store().Load()
	if err != nil {
		return request
	}
	seen := map[string]bool{}
	for _, link := range file.Links {
		messages := []string{link.RootMessageID}
		for _, messageID := range messages {
			if messageID == "" || seen[messageID] {
				continue
			}
			seen[messageID] = true
			request.CardBindings = append(request.CardBindings, feishuprotocol.CardBinding{TargetType: link.Target.Type, TargetID: link.Target.ID, MessageID: messageID})
		}
	}
	return request
}
