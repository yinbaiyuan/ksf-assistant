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
	"ksfassistant/core/internal/capabilitypolicy"
	"ksfassistant/core/internal/feishu"
	"ksfassistant/core/internal/feishucli"
	"ksfassistant/core/internal/feishuprotocol"
	"ksfassistant/core/internal/privateipc"
)

const version = "0.11.0-preview.4"

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
	defer feishu.CancelAppConfiguration(dataRoot)
	var messageClient *feishu.OfficialMessageClient
	messageClient, err = managedMessageClient(ctx, executor)
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
	var outbox *feishu.Outbox
	if err := capabilityService.RecoverInterrupted(1000); err != nil {
		return err
	}
	go runOperationReconciliation(ctx, capabilityService)
	if messageClient != nil {
		outbox = feishu.NewOutbox(dataRoot)
	}
	scheduler := feishu.NewWorkScheduler(dataRoot)
	rpcServer.mu.Lock()
	rpcServer.scheduler = scheduler
	rpcServer.mu.Unlock()
	scheduler.RegisterCapabilityService(capabilityService)
	if outbox != nil {
		scheduler.RegisterOutbox(outbox, rpcServer.transport, false)
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
	if messageClient != nil {
		eventState := feishu.NewEventConsumerStateStore(dataRoot)
		_ = eventState.UpdateConnection("starting")
		defer eventState.UpdateConnection("disconnected")
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
					return &feishu.DeliveryError{Stage: "local_binding", Err: err}
				}
				return deliver(callCtx, "message", message.EventID, message)
			},
			func(callCtx context.Context, card feishu.InboundCardAction) error {
				if err := rpcServer.transport.BindCard(card); err != nil {
					return &feishu.DeliveryError{Stage: "local_binding", Err: err}
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
				}, func(state string) { _ = eventState.UpdateConnection(state) })
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

func managedMessageClient(ctx context.Context, executor feishu.CapabilityExecutor) (*feishu.OfficialMessageClient, error) {
	if probe := feishu.ProbeLarkCLI(ctx, executor.Binary); probe.State != "ready" {
		return nil, errors.New("fixed_lark_cli_unavailable")
	}
	identity, err := executor.RunAuthJSON(ctx, []string{"auth", "status", "--json"}, nil, 5*time.Second)
	if err != nil {
		return nil, err
	}
	identities, _ := identity["identities"].(map[string]any)
	bot, _ := identities["bot"].(map[string]any)
	user, _ := identities["user"].(map[string]any)
	if user["available"] == false && user["status"] == "missing" && user["verified"] != true {
		if err := capabilitypolicy.SignOut(executor.DataRoot); err != nil {
			return nil, err
		}
	}
	appID, _ := identity["appId"].(string)
	if bot["available"] != true || identity["brand"] != "feishu" {
		return nil, errors.New("official_cli_bot_identity_unavailable")
	}
	return feishu.NewOfficialMessageClient(appID, executor)
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
	ticker := time.NewTicker(time.Second)
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
	for ctx.Err() == nil {
		if processor.WaitRecovery(ctx) != nil {
			return
		}
		_ = processor.Recover(ctx)
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
