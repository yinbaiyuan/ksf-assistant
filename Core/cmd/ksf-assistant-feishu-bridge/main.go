package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"crypto/sha256"
	"encoding/hex"
	"ksfassistant/core/internal/feishu"
	"ksfassistant/core/internal/feishucli"
	"ksfassistant/core/internal/feishuprotocol"
	"ksfassistant/core/internal/privateipc"
)

const version = "0.11.0-preview.3"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) == 1 && arguments[0] == "--version" {
		fmt.Println(version)
		return nil
	}
	dataRoot, err := privateDataRoot()
	if err != nil {
		return err
	}
	if len(arguments) > 0 && arguments[0] == "client" {
		return feishucli.Run(context.Background(), dataRoot, arguments[1:], os.Stdin, os.Stdout)
	}
	settings, err := feishu.NewSettingsStore(dataRoot).Load()
	if err != nil {
		return fmt.Errorf("load Feishu settings: %w", err)
	}
	if len(arguments) == 1 && arguments[0] == "--check" {
		if err := settings.Validate(); err != nil {
			return err
		}
		fmt.Println("ok")
		return nil
	}
	if len(arguments) != 0 {
		return errors.New("unsupported ksf-assistant-feishu-bridge argument")
	}
	instance, err := feishu.AcquireInstanceLock(dataRoot)
	if err != nil {
		return err
	}
	defer instance.Close()

	if err := feishu.NewControlInbox(dataRoot).Retire(context.Background()); err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	approvalGate := &feishu.UserApprovalGate{}
	executor := feishu.CapabilityExecutor{Binary: strings.TrimSpace(os.Getenv("LARK_CLI_BIN")), Profile: strings.TrimSpace(os.Getenv("LARK_CLI_PROFILE")), DataRoot: dataRoot, WorkingDirectory: dataRoot, UserApproval: approvalGate}
	if executor.Profile == "" {
		executor.Profile = "default"
	}
	defer feishu.CancelUserAuthFlow(dataRoot)
	var messageClient *feishu.OfficialMessageClient
	if settings.Profile == feishu.ProfilePrimary || settings.Outbound.Enabled {
		if probe := feishu.ProbeLarkCLI(ctx, executor.Binary); probe.State != "ready" {
			err = errors.New("fixed_lark_cli_unavailable")
		} else {
			var identity map[string]any
			identity, err = executor.RunAuthJSON(ctx, []string{"auth", "status", "--json"}, nil, 5*time.Second)
			if err == nil {
				identities, _ := identity["identities"].(map[string]any)
				bot, _ := identities["bot"].(map[string]any)
				appID, _ := identity["appId"].(string)
				if bot["available"] != true || identity["brand"] != "feishu" {
					err = errors.New("official_cli_bot_identity_unavailable")
				} else {
					messageClient, err = feishu.NewOfficialMessageClient(appID, executor)
				}
			}
		}
	}
	serviceExecutor := feishu.UnifiedCapabilityExecutor{LongTail: executor, DataRoot: dataRoot}
	capabilityService := feishu.NewCapabilityService(dataRoot, serviceExecutor, nil)
	rpcServer := newBridgeRPCServer(dataRoot, settings, capabilityService)
	rpcServer.degrade(err)
	peer := privateipc.NewPeer(os.Stdin, os.Stdout, feishucli.NewUploadHandler(rpcServer, feishuprotocol.ClientExecute))
	approvalGate.SetCaller(peer.Call)
	rpcServer.setRuntime(messageClient)
	defer peer.Close()
	parentClosed := make(chan error, 1)
	go func() { parentClosed <- peer.Serve(ctx) }()

	go feishu.RunLifecycleMaintenance(ctx, dataRoot)
	var docbox *feishu.Docbox
	var outbox *feishu.Outbox
	if err := capabilityService.RecoverInterrupted(1000); err != nil {
		return err
	}
	if settings.Actionbox.Enabled {
		go runOperationReconciliation(ctx, capabilityService)
	}
	if settings.Docbox.Enabled {
		docbox = feishu.NewDocbox(dataRoot)
	}
	if settings.Outbound.Enabled && messageClient != nil {
		outbox = feishu.NewOutbox(dataRoot)
	}
	scheduler := feishu.NewWorkScheduler(dataRoot)
	rpcServer.mu.Lock()
	rpcServer.scheduler = scheduler
	rpcServer.mu.Unlock()
	if settings.Actionbox.Enabled {
		scheduler.RegisterCapabilityService(capabilityService)
	}
	if docbox != nil {
		scheduler.RegisterDocbox(docbox, executor, settings.Docbox.DryRun)
	}
	if outbox != nil {
		scheduler.RegisterOutbox(outbox, rpcServer.transport, settings.Outbound.DryRun)
	}
	schedulerDone := make(chan struct{})
	defer func() {
		cancel()
		select {
		case <-schedulerDone:
		case <-time.After(5 * time.Second):
			_ = feishu.NewAuditLog(dataRoot).Record("shutdown_incomplete", map[string]any{"component": "scheduler"})
		}
	}()
	go func() {
		defer close(schedulerDone)
		if schedulerErr := scheduler.Run(ctx); schedulerErr != nil && !errors.Is(schedulerErr, context.Canceled) {
			rpcServer.degrade(schedulerErr)
			_ = feishu.NewAuditLog(dataRoot).Record("work_scheduler_stopped", map[string]any{"error": schedulerErr.Error()})
		}
	}()
	var inbound *feishu.OfficialInbound
	if settings.Profile == feishu.ProfilePrimary && messageClient != nil {
		eventState := feishu.NewEventConsumerStateStore(dataRoot)
		_ = eventState.UpdateConnection(settings.Profile, "starting")
		defer eventState.UpdateConnection(settings.Profile, "disconnected")
		deliver := func(callCtx context.Context, kind, id string, value any) error {
			payload, err := json.Marshal(value)
			if err != nil {
				return err
			}
			if id == "" {
				digest := sha256.Sum256(payload)
				id = hex.EncodeToString(digest[:])
			}
			timeoutCtx, stop := context.WithTimeout(callCtx, 10*time.Second)
			defer stop()
			var accepted feishuprotocol.Accepted
			if err := peer.Call(timeoutCtx, feishuprotocol.EventDeliver, feishuprotocol.Event{ID: id, Kind: kind, Payload: payload}, &accepted); err != nil {
				return err
			}
			if !accepted.Accepted {
				return errors.New("Core did not durably accept event")
			}
			return nil
		}
		processor, processorErr := feishu.NewInboundProcessor(dataRoot, settings,
			func(callCtx context.Context, message feishu.InboundMessage) error {
				if err := rpcServer.transport.BindInbound(message); err != nil {
					return err
				}
				return deliver(callCtx, "message", message.EventID, message)
			},
			func(callCtx context.Context, card feishu.InboundCardAction) error {
				if err := rpcServer.transport.BindCard(card); err != nil {
					return err
				}
				return deliver(callCtx, "card", card.EventID, card)
			})
		if processorErr != nil {
			rpcServer.degrade(processorErr)
		} else {
			defer processor.Close()
			if recoverErr := processor.Recover(ctx); recoverErr != nil {
				rpcServer.degrade(recoverErr)
			}
			go runInboundRecovery(ctx, processor)
			inbound, err = feishu.NewOfficialInbound(executor, messageClient,
				func(callCtx context.Context, eventKey string, payload []byte) error {
					if eventKey == feishu.MailMessageReceivedEvent && !rpcServer.mailEventsEnabled() {
						return nil
					}
					if err := processor.Handle(callCtx, eventKey, payload); err != nil {
						return err
					}
					return eventState.MarkReceived(eventKey)
				}, func(state string) { _ = eventState.UpdateConnection(settings.Profile, state) })
			if err != nil {
				rpcServer.degrade(err)
			} else {
				go func() { rpcServer.degrade(inbound.Start(ctx)) }()
				defer inbound.Close()
			}
		}
	}

	_ = peer.Notify(context.Background(), feishuprotocol.MethodBridgeSnapshotPush, rpcServer.snapshot(context.Background()))
	go runBridgeSnapshotPublisher(ctx, peer, rpcServer)
	select {
	case <-ctx.Done():
	case peerErr := <-parentClosed:
		if peerErr != nil && !errors.Is(peerErr, context.Canceled) {
			return peerErr
		}
	}
	return nil
}

func runBridgeSnapshotPublisher(ctx context.Context, peer *privateipc.Peer, server *bridgeRPCServer) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	var previous string
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			snapshot := server.snapshot(ctx)
			comparable := snapshot
			comparable.Revision = 0
			encoded, _ := json.Marshal(comparable)
			if string(encoded) == previous {
				continue
			}
			previous = string(encoded)
			_ = peer.Notify(ctx, feishuprotocol.MethodBridgeSnapshotPush, snapshot)
		}
	}
}

func runOperationReconciliation(ctx context.Context, service *feishu.CapabilityService) {
	_ = service.ExpireAwaiting(100)
	_ = service.ReconcileUnknown(ctx, 20)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = service.ExpireAwaiting(100)
			_ = service.ReconcileUnknown(ctx, 20)
		}
	}
}

func runInboundRecovery(ctx context.Context, processor *feishu.InboundProcessor) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = processor.Recover(ctx)
		}
	}
}

func privateDataRoot() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("FEISHU_BRIDGE_DATA_DIR")); configured != "" {
		if !filepath.IsAbs(configured) {
			return "", errors.New("FEISHU_BRIDGE_DATA_DIR must be absolute")
		}
		return filepath.Clean(configured), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.New("unable to locate the current user home directory")
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(home, ".config", "feishu-bridge"), nil
	}
	return filepath.Join(home, ".config", "feishu-bridge"), nil
}
