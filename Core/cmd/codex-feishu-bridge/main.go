package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"codexusagebar/core/internal/corebridge"
	"codexusagebar/core/internal/domain"
	"codexusagebar/core/internal/feishu"
	"codexusagebar/core/internal/privateipc"
)

const version = "0.10.0-preview.1"

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
	if len(arguments) > 0 && arguments[0] == "client" {
		return runClient(dataRoot, settings, arguments[1:])
	}
	if len(arguments) != 0 {
		return errors.New("unsupported codex-feishu-bridge argument")
	}
	instance, err := feishu.AcquireInstanceLock(dataRoot)
	if err != nil {
		return err
	}
	defer instance.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	executor := feishu.CapabilityExecutor{Binary: strings.TrimSpace(os.Getenv("LARK_CLI_BIN")), Profile: strings.TrimSpace(os.Getenv("LARK_CLI_PROFILE")), DataRoot: dataRoot, WorkingDirectory: dataRoot}
	serviceExecutor := feishu.UnifiedCapabilityExecutor{LongTail: executor, DataRoot: dataRoot}
	capabilityService := feishu.NewCapabilityService(dataRoot, serviceExecutor, nil)
	rpcServer := newBridgeRPCServer(dataRoot, settings, capabilityService)
	peer := privateipc.NewPeer(os.Stdin, os.Stdout, rpcServer)
	coreClient := newCoreCapabilityClient(peer)
	rpcServer.setRuntime(nil, nil, coreClient)
	parentClosed := make(chan error, 1)
	go func() { parentClosed <- peer.Serve(ctx) }()

	taskLinks := feishu.NewTaskLinkStore(dataRoot)
	_, _ = taskLinks.CleanupAt(time.Now().UTC())
	go runTaskLinkCleanup(ctx, taskLinks)
	go feishu.RunLifecycleMaintenance(ctx, dataRoot)
	var docbox *feishu.Docbox
	var outbox *feishu.Outbox
	_ = capabilityService.RecoverInterrupted(1000)
	if settings.Actionbox.Enabled {
		go runOperationReconciliation(ctx, capabilityService)
	}
	if settings.Docbox.Enabled {
		docbox = feishu.NewDocbox(dataRoot)
	}
	var credentials feishu.OfficialCredentials
	if settings.Profile == feishu.ProfilePrimary || settings.Outbound.Enabled {
		credentials, err = feishu.LoadOfficialCredentials()
	}
	var messageClient *feishu.OfficialMessageClient
	if err == nil && (settings.Profile == feishu.ProfilePrimary || settings.Outbound.Enabled) {
		messageClient, err = feishu.NewOfficialMessageClient(credentials.AppID, credentials.AppSecret)
	}
	rpcServer.setRuntime(messageClient, nil, coreClient)
	controlInbox := feishu.NewControlInbox(dataRoot)
	go runBridgeControlInbox(ctx, controlInbox, rpcServer)
	if settings.Outbound.Enabled && messageClient != nil {
		outbox = feishu.NewOutbox(dataRoot)
	}
	scheduler := feishu.NewWorkScheduler(dataRoot)
	if settings.Actionbox.Enabled {
		scheduler.RegisterCapabilityService(capabilityService)
	}
	if docbox != nil {
		scheduler.RegisterDocbox(docbox, executor, settings.Docbox.DryRun)
	}
	if outbox != nil {
		scheduler.RegisterOutbox(outbox, messageClient, settings.Outbound.DryRun)
	}
	go func() {
		if schedulerErr := scheduler.Run(ctx); schedulerErr != nil && !errors.Is(schedulerErr, context.Canceled) {
			_ = feishu.NewAuditLog(dataRoot).Record("work_scheduler_stopped", map[string]any{"error": schedulerErr.Error()})
		}
	}()
	wakeHandlers := map[string]func(){}
	if settings.Actionbox.Enabled {
		wakeHandlers["actionbox"] = scheduler.Wake
	}
	if docbox != nil {
		wakeHandlers["docbox"] = scheduler.Wake
	}
	if outbox != nil {
		wakeHandlers["outbox"] = scheduler.Wake
	}
	wakeServer, err := feishu.StartWakeServer(dataRoot, wakeHandlers)
	if err != nil {
		_ = feishu.NewAuditLog(dataRoot).Record("queue_wake_degraded", map[string]any{"error": err.Error()})
	}
	if wakeServer != nil {
		defer wakeServer.Close(context.Background())
	}
	var inbound *feishu.OfficialInbound
	if settings.Profile == feishu.ProfilePrimary && messageClient != nil {
		eventState := feishu.NewEventConsumerStateStore(dataRoot)
		_ = eventState.UpdateConnection(settings.Profile, "starting")
		defer eventState.UpdateConnection(settings.Profile, "disconnected")
		runtimeHandler, runtimeErr := newInboundRuntime(dataRoot, messageClient, coreClient)
		if runtimeErr == nil {
			rpcServer.setRuntime(messageClient, runtimeHandler, coreClient)
			defer runtimeHandler.Close()
			processor, processorErr := feishu.NewInboundProcessor(dataRoot, settings, runtimeHandler.HandleMessage, runtimeHandler.HandleCard)
			if processorErr == nil {
				if recoverErr := processor.Recover(ctx); recoverErr != nil {
					_ = feishu.NewAuditLog(dataRoot).Record("inbound_recovery_degraded", map[string]any{"error": recoverErr.Error()})
				}
				go runInboundRecovery(ctx, processor)
				go runTaskCardReconciliation(ctx, runtimeHandler)
				if resumeErr := runtimeHandler.ResumeActive(); resumeErr != nil {
					_ = feishu.NewAuditLog(dataRoot).Record("task_link_resume_degraded", map[string]any{"error": resumeErr.Error()})
				}
				inbound, err = feishu.NewOfficialInbound(credentials.AppID, credentials.AppSecret, func(_ context.Context, eventKey string, payload []byte) error {
					if eventKey == feishu.MailMessageReceivedEvent && !rpcServer.mailEventsEnabled() {
						return nil
					}
					if err := processor.Handle(context.Background(), eventKey, payload); err != nil {
						return err
					}
					_ = eventState.MarkReceived(eventKey)
					return nil
				}, func(state string) { _ = eventState.UpdateConnection(settings.Profile, state) })
				if err == nil {
					go func() {
						if inboundErr := inbound.Start(ctx); inboundErr != nil && !errors.Is(inboundErr, context.Canceled) {
							_ = eventState.UpdateConnection(settings.Profile, "disconnected")
							_ = feishu.NewAuditLog(dataRoot).Record("feishu_inbound_degraded", map[string]any{"error": inboundErr.Error()})
						}
					}()
					defer inbound.Close()
				}
			}
		} else {
			_ = feishu.NewAuditLog(dataRoot).Record("codex_workflow_degraded", map[string]any{"error": runtimeErr.Error()})
		}
	}
	_ = peer.Notify(context.Background(), corebridge.MethodBridgeSnapshotPush, rpcServer.snapshot(context.Background()))
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
			_ = peer.Notify(ctx, corebridge.MethodBridgeSnapshotPush, snapshot)
		}
	}
}

func runTaskLinkCleanup(ctx context.Context, store feishu.TaskLinkStore) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			_, _ = store.CleanupAt(now.UTC())
		}
	}
}

func runBridgeControlInbox(ctx context.Context, inbox *feishu.ControlInbox, server *bridgeRPCServer) {
	process := func() {
		_ = inbox.Process(ctx, func(callCtx context.Context, request feishu.ControlRequest) error {
			_, err := server.interruptTaskLink(callCtx, request.TaskKey)
			return err
		})
	}
	process()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			process()
		}
	}
}

func clientAggregateSnapshot(dataRoot string, settings feishu.Settings) (domain.FeishuSnapshot, error) {
	present, alive, pid, _, err := feishu.InstanceStatus(dataRoot)
	if err != nil {
		return domain.FeishuSnapshot{}, err
	}
	processState := "stopped"
	if alive {
		processState = "running"
	} else if present {
		processState = "stale"
	}
	setup, setupErr := feishu.NewSetupStore(dataRoot).Load()
	configured := setupErr == nil && setup.Stage == feishu.SetupReady

	eventState, _ := feishu.NewEventConsumerStateStore(dataRoot).Read()
	connection, _ := eventState["connection"].(map[string]any)
	inboundConnected := stringValue(connection["state"]) == "connected"

	aliases := []string{}
	config, configErr := feishu.NewClientConfigStore(dataRoot).Load()
	if configErr == nil {
		for alias, target := range config.MessageTargets {
			if target.Type == "open_id" && containsString(config.DirectAllowedAliases, alias) {
				aliases = append(aliases, alias)
			}
		}
	}
	sort.Strings(aliases)

	links := []domain.FeishuTaskLink{}
	file, linksErr := feishu.NewTaskLinkStore(dataRoot).Load()
	if linksErr == nil {
		encoded, _ := json.Marshal(feishu.PublicLinks(file.Links))
		_ = json.Unmarshal(encoded, &links)
	}

	capabilities := map[string]domain.CapabilityHealth{
		"feishuInbound":  {State: "unavailable"},
		"feishuOutbound": {State: "unavailable"},
		"codexAppServer": {State: "unavailable", Detail: "requires managed CodexAssistant Core"},
		"desktopIPC":     {State: "unavailable", Detail: "requires managed CodexAssistant Core"},
		"ksfContext":     {State: "unavailable", Detail: "requires managed CodexAssistant Core"},
		"larkCLI":        {State: "disabled"},
		"outbox":         {State: switchState(settings.Outbound.Enabled)},
		"docbox":         {State: switchState(settings.Docbox.Enabled)},
		"actionbox":      {State: switchState(settings.Actionbox.Enabled)},
	}
	if settings.Profile != feishu.ProfilePrimary {
		capabilities["feishuInbound"] = domain.CapabilityHealth{State: "disabled"}
	} else if inboundConnected {
		capabilities["feishuInbound"] = domain.CapabilityHealth{State: "ready"}
	} else if alive {
		capabilities["feishuInbound"] = domain.CapabilityHealth{State: "degraded"}
	}
	if settings.Outbound.Enabled && alive {
		capabilities["feishuOutbound"] = domain.CapabilityHealth{State: "ready"}
	} else if settings.Outbound.Enabled {
		capabilities["feishuOutbound"] = domain.CapabilityHealth{State: "degraded"}
	} else {
		capabilities["feishuOutbound"] = domain.CapabilityHealth{State: "disabled"}
	}
	probe := feishu.ProbeLarkCLI(context.Background(), strings.TrimSpace(os.Getenv("LARK_CLI_BIN")))
	capabilities["larkCLI"] = domain.CapabilityHealth{State: probe.State, Detail: probe.Detail}

	availability := "stopped"
	message := "飞书服务未运行。"
	if alive {
		availability, message = "ready", ""
		if !settings.Outbound.Enabled {
			availability, message = "unavailable", "飞书服务尚未启用主动出站。"
		} else if settings.Outbound.DryRun {
			availability = "dryRun"
		}
	}
	ready := alive && settings.Outbound.Enabled && !settings.Outbound.DryRun
	return domain.FeishuSnapshot{
		RuntimeKind: "go", Availability: availability, Message: message,
		ProcessState: processState, Configured: configured, ProcessPID: pid, ProcessRunning: alive,
		Profile: settings.Profile, ProfileValid: settings.Profile == feishu.ProfilePrimary || settings.Profile == feishu.ProfileManualOnly,
		InboundConnection: inboundConnected, TargetAliases: aliases,
		TaskLinkProtocolVersion: 2, TaskLinkReady: ready, ReadinessBlockers: taskLinkBlockers(settings),
		Links: links, Capabilities: capabilities, Queues: publicQueueHealth(feishu.QueueHealthSnapshot(dataRoot, settings)),
	}, nil
}

// runClient is the stable native replacement for `node scripts/bridge-client.js`.
// It deliberately emits the same JSON envelope for the host-facing control
// subset while the daemon remains the only long-lived process owner.
func runClient(dataRoot string, settings feishu.Settings, arguments []string) error {
	if len(arguments) == 0 {
		return errors.New("missing bridge client command")
	}
	write := func(value any) error { return json.NewEncoder(os.Stdout).Encode(value) }
	store := feishu.NewTaskLinkStore(dataRoot)
	authRunner := feishu.CapabilityExecutor{Binary: strings.TrimSpace(os.Getenv("LARK_CLI_BIN")), Profile: strings.TrimSpace(os.Getenv("LARK_CLI_PROFILE")), DataRoot: dataRoot, WorkingDirectory: dataRoot}
	serviceRunner := feishu.UnifiedCapabilityExecutor{LongTail: authRunner, DataRoot: dataRoot}
	capabilityService := feishu.NewCapabilityService(dataRoot, serviceRunner, nil)
	switch arguments[0] {
	case "help":
		return write(map[string]any{"usage": []string{"codex-feishu-bridge client snapshot", "codex-feishu-bridge client status|doctor", "codex-feishu-bridge client targets init|list|set|remove", "codex-feishu-bridge client send ...", "codex-feishu-bridge client task-link protocol|list|create|status|interrupt|release", "codex-feishu-bridge client capability catalog|get|read|write", "codex-feishu-bridge client operation prepare|confirm|cancel|status", "codex-feishu-bridge client policy read|update", "codex-feishu-bridge client events catalog|status|recent|get", "codex-feishu-bridge client result <outbox|docbox|actionbox> <id>", "codex-feishu-bridge client recent <outbox|docbox|actionbox|messages|audit>"}})
	case "profile":
		action := "show"
		if len(arguments) > 1 {
			action = arguments[1]
		}
		if action == "catalog" {
			return write(map[string]any{"status": "ok", "profiles": []any{map[string]any{"profile": feishu.ProfilePrimary}, map[string]any{"profile": feishu.ProfileManualOnly}}, "sharedAppRule": "exactly_one_primary"})
		}
		if action == "show" {
			return write(map[string]any{"status": "ok", "eventConsumer": map[string]any{"profile": settings.Profile, "profileValid": true, "desiredConnection": settings.Profile == feishu.ProfilePrimary}, "sharedAppRule": "exactly_one_primary"})
		}
		if action == "set" && len(arguments) > 2 {
			previous := settings.Profile
			settings.Profile = arguments[2]
			if err := feishu.NewSettingsStore(dataRoot).Save(settings); err != nil {
				return err
			}
			return write(map[string]any{"status": "updated", "previousProfile": previous, "profile": settings.Profile, "appliedByRunningBridge": "pending_realtime_reconcile", "sharedAppRule": "exactly_one_primary"})
		}
		return errors.New("profile action must be catalog, show, or set")
	case "auth":
		if len(arguments) < 2 {
			return errors.New("missing auth action")
		}
		action := arguments[1]
		switch action {
		case "configure-existing":
			payload, err := clientPayload(arguments[2:])
			if err != nil {
				return err
			}
			var input struct {
				AppID     string `json:"appId"`
				AppSecret string `json:"appSecret"`
				Brand     string `json:"brand"`
			}
			if json.Unmarshal(payload, &input) != nil {
				return errors.New("invalid auth payload")
			}
			result, err := feishu.ConfigureExistingApp(context.Background(), authRunner, input.AppID, input.AppSecret, input.Brand, clientOptionalFlag(arguments[2:], "--profile"))
			if err != nil {
				return err
			}
			return write(result)
		case "start-config":
			result, err := feishu.StartAppConfiguration(context.Background(), authRunner, dataRoot, clientOptionalFlag(arguments[2:], "--profile"), clientHasFlag(arguments[2:], "--create-new"))
			if err != nil {
				return err
			}
			return write(result)
		case "start-user":
			result, err := feishu.StartUserAuth(context.Background(), authRunner, dataRoot, clientOptionalFlag(arguments[2:], "--scope"))
			if err != nil {
				return err
			}
			return write(result)
		case "finish-user":
			result, err := feishu.FinishUserAuthFlow(context.Background(), authRunner, dataRoot, clientOptionalFlag(arguments[2:], "--device-code"))
			if err != nil {
				return err
			}
			return write(result)
		case "ensure-current-user":
			result, err := feishu.EnsureCurrentUser(context.Background(), authRunner, feishu.NewClientConfigStore(dataRoot))
			if err != nil {
				return err
			}
			return write(result)
		default:
			return errors.New("unsupported auth action")
		}
	case "permissions":
		result, err := feishu.AuthPermissions(context.Background(), authRunner)
		if err != nil {
			return err
		}
		return write(result)
	case "capabilities":
		manifest, err := feishu.LoadCapabilityManifest()
		if err != nil {
			return err
		}
		items := make([]any, 0, len(manifest.Capabilities))
		readCapabilities := []string{}
		queuedWriteCapabilities := []string{}
		riskCounts := map[string]int{}
		for _, definition := range manifest.Capabilities {
			if !feishu.CapabilityPublished(definition) {
				continue
			}
			items = append(items, publicCapability(definition))
			riskCounts[definition.Risk]++
			if definition.Risk == "read" {
				readCapabilities = append(readCapabilities, definition.ID)
			} else {
				queuedWriteCapabilities = append(queuedWriteCapabilities, definition.ID)
			}
		}
		scopes, err := feishu.RequiredPermissionScopes()
		if err != nil {
			return err
		}
		return write(map[string]any{"status": "ok", "capabilities": map[string]any{
			"bridgeVersion": "2.0.0", "packageVersion": "1.2.0", "capabilityVersion": "2.0.0", "stabilityBaselineVersion": "0.6.1", "queueStateSchemaVersion": 2, "skillCompatibility": "phase2_pending", "larkCliVersion": "1.0.92", "officialSdk": "go", "officialSdkVersion": "3.11.0",
			"identities": []string{"bot", "user"}, "eventTransport": "official-sdk", "singleInboundConnection": true, "events": feishu.FixedEventKeys, "fixedEventCatalog": true,
			"inboundMessageTypes": []string{"text", "image", "file", "audio", "media", "post"}, "outboundMessageFormats": []string{"text", "markdown", "card", "image", "file"},
			"readCapabilities": readCapabilities, "queuedWriteCapabilities": queuedWriteCapabilities,
			"registeredCapabilities": items, "registeredCapabilityCount": len(items), "riskCounts": riskCounts, "documentWrites": []string{"create_document", "append", "overwrite", "str_replace"},
			"intentionallyExcluded": []string{"application_management", "member_admin_role_permission_management", "credential_or_secret_management", "automation_configuration", "live_meeting_control", "urgent_phone_or_sms", "arbitrary_openapi", "background_full_crawl"},
			"requiredScopes":        map[string]any{"bot": scopes.Bot, "user": scopes.User},
		}})
	case "events":
		if len(arguments) < 2 {
			return errors.New("missing events action")
		}
		inbox := feishu.NewEventInbox(dataRoot)
		switch arguments[1] {
		case "catalog":
			return write(map[string]any{"status": "ok", "fixed": true, "eventCount": len(feishu.FixedEventKeys), "events": feishu.FixedEventKeys})
		case "status":
			value, err := inbox.Status()
			if err != nil {
				return err
			}
			return write(map[string]any{"status": "ok", "inbox": value, "transport": map[string]any{"profile": settings.Profile}})
		case "recent":
			value, err := inbox.Recent(clientLimit(arguments[2:], 20))
			if err != nil {
				return err
			}
			return write(map[string]any{"status": "ok", "events": value})
		case "get":
			value, found, err := inbox.Get(clientOptionalFlag(arguments[2:], "--fingerprint"))
			if err != nil {
				return err
			}
			return write(map[string]any{"status": "ok", "found": found, "event": value})
		default:
			return errors.New("events action must be catalog, status, recent, or get")
		}
	case "result":
		if len(arguments) < 3 {
			return errors.New("result requires kind and id")
		}
		records, err := feishu.QueueResults(dataRoot, arguments[1], arguments[2], 100)
		if err != nil {
			return err
		}
		return write(map[string]any{"id": arguments[2], "kind": arguments[1], "found": len(records) > 0, "records": records})
	case "recent":
		if len(arguments) < 2 {
			return errors.New("recent requires kind")
		}
		records, err := feishu.RecentRecords(dataRoot, arguments[1], clientLimit(arguments[2:], 10))
		if err != nil {
			return err
		}
		return write(map[string]any{"kind": arguments[1], "records": records})
	case "status":
		present, alive, _, startedAt, statusErr := feishu.InstanceStatus(dataRoot)
		if statusErr != nil {
			return statusErr
		}
		service := map[string]any{"running": alive, "loaded": present}
		eventState, _ := feishu.NewEventConsumerStateStore(dataRoot).Read()
		eventState["profile"] = settings.Profile
		eventState["profileValid"] = true
		eventState["desiredConnection"] = settings.Profile == feishu.ProfilePrimary
		eventState["expected"] = feishu.FixedEventKeys
		eventState["fixedCatalogCount"] = len(feishu.FixedEventKeys)
		return write(map[string]any{"status": "ok", "runtime": "go", "pid": map[string]any{"present": present, "alive": alive, "startedAt": startedAt}, "outbound": settings.Outbound, "service": service, "launchd": service, "eventConsumer": eventState})
	case "snapshot":
		value, err := clientAggregateSnapshot(dataRoot, settings)
		if err != nil {
			return err
		}
		return write(value)
	case "doctor":
		return write(nativeDoctor(dataRoot, settings, authRunner))
	case "targets":
		if len(arguments) < 2 {
			return errors.New("missing targets action")
		}
		configPath := clientOptionalFlag(arguments[2:], "--config")
		configStore := feishu.NewClientConfigStore(dataRoot)
		if configPath != "" {
			configStore = feishu.NewClientConfigStorePath(configPath)
		}
		config, err := configStore.Load()
		if err != nil {
			return err
		}
		switch arguments[1] {
		case "directory":
			if len(arguments) < 3 {
				return errors.New("missing directory action")
			}
			return runDirectoryClient(settings, capabilityService, feishu.PersonDirectory, arguments[2], arguments[3:], configStore, write)
		case "group-directory":
			if len(arguments) < 3 {
				return errors.New("missing group directory action")
			}
			return runDirectoryClient(settings, capabilityService, feishu.GroupDirectory, arguments[2], arguments[3:], configStore, write)
		case "init":
			if err := configStore.Save(config); err != nil {
				return err
			}
			return write(map[string]any{"status": "initialized", "configPath": configStore.Path(), "targets": feishu.PublicClientTargets(config)})
		case "list":
			return write(map[string]any{"status": "ok", "configPath": configStore.Path(), "targets": feishu.PublicClientTargets(config)})
		case "remove":
			if len(arguments) < 4 {
				return errors.New("target category and alias are required")
			}
			category, alias := arguments[2], arguments[3]
			if category == "message" {
				delete(config.MessageTargets, alias)
			} else if category == "document" {
				delete(config.DocumentTargets, alias)
			} else {
				return errors.New("target category must be message or document")
			}
			if err := configStore.Save(config); err != nil {
				return err
			}
			return write(map[string]any{"status": "removed", "category": category, "alias": alias})
		case "set":
			if len(arguments) < 4 {
				return errors.New("target category and alias are required")
			}
			category, alias := arguments[2], arguments[3]
			value, err := clientPrivateValue(arguments[4:], "--value-file")
			if err != nil {
				return err
			}
			if category == "message" {
				targetType, err := clientFlag(arguments[4:], "--type")
				if err != nil {
					return err
				}
				err = config.SetMessageTarget(alias, feishu.MessageTarget{Type: targetType, ID: strings.TrimSpace(string(value))})
				if err != nil {
					return err
				}
			} else if category == "document" {
				kind, err := clientFlag(arguments[4:], "--kind")
				if err != nil {
					return err
				}
				err = config.SetDocumentTarget(alias, feishu.DocumentTarget{Kind: kind, Value: strings.TrimSpace(string(value))})
				if err != nil {
					return err
				}
			} else {
				return errors.New("target category must be message or document")
			}
			if err := configStore.Save(config); err != nil {
				return err
			}
			return write(map[string]any{"status": "saved", "category": category, "alias": alias, "targets": feishu.PublicClientTargets(config)})
		default:
			return errors.New("targets action must be init, list, set, or remove")
		}
	case "send":
		if !settings.Outbound.Enabled {
			return errors.New("outbound messaging is disabled")
		}
		if !settings.Actionbox.Enabled {
			return errors.New("actionbox is required for governed outbound messaging")
		}
		config, err := feishu.NewClientConfigStore(dataRoot).Load()
		if err != nil {
			return err
		}
		flags := arguments[1:]
		targetValue := clientOptionalFlag(flags, "--target")
		targetName := clientOptionalFlag(flags, "--target-name")
		targetGroupName := clientOptionalFlag(flags, "--target-group-name")
		providedTargets := 0
		for _, value := range []string{targetValue, targetName, targetGroupName} {
			if strings.TrimSpace(value) != "" {
				providedTargets++
			}
		}
		if providedTargets != 1 {
			return errors.New("provide exactly one of --target, --target-name, or --target-group-name")
		}
		var target feishu.MessageTarget
		if targetName != "" || targetGroupName != "" {
			kind := feishu.PersonDirectory
			name := targetName
			enabled := settings.Directory.Enabled
			if targetGroupName != "" {
				kind, name, enabled = feishu.GroupDirectory, targetGroupName, settings.GroupDirectory.Enabled
			}
			if !enabled {
				return errors.New("requested directory feature is disabled")
			}
			resolution, resolveErr := feishu.NewDirectoryService(capabilityService).Resolve(context.Background(), kind, name, config)
			if resolveErr != nil {
				return resolveErr
			}
			if resolution.Status != "resolved" {
				return write(feishu.PublicResult(map[string]any{"status": resolution.Status, "submitted": false, "resolution": resolution}))
			}
			target = resolution.Target
		} else {
			target, err = config.ResolveMessageTarget(targetValue)
			if err != nil {
				return err
			}
		}
		format := clientOptionalFlag(arguments[1:], "--format")
		if format == "" {
			format = "text"
		}
		id := clientOptionalFlag(arguments[1:], "--id")
		if id == "" {
			id, err = feishu.NewOutboxID()
			if err != nil {
				return err
			}
		}
		input := map[string]any{"request-id": id, "target-type": target.Type, "target-id": target.ID, "format": format, "source": config.DefaultSource, "dry-run": clientHasFlag(arguments[1:], "--dry-run")}
		if source := clientOptionalFlag(arguments[1:], "--source"); source != "" {
			input["source"] = source
		}
		stagedMedia := ""
		if format == "image" || format == "file" {
			source, err := clientFlag(arguments[1:], "--media-file")
			if err != nil {
				return err
			}
			stagedMedia, err = feishu.StageOutboundMedia(dataRoot, id, source)
			if err != nil {
				return err
			}
			relative, relativeErr := filepath.Rel(dataRoot, stagedMedia)
			if relativeErr != nil || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
				_ = os.Remove(stagedMedia)
				return errors.New("staged media path is unsafe")
			}
			input["file-path"] = filepath.ToSlash(relative)
		} else {
			flag := "--content-file"
			if clientOptionalFlag(arguments[1:], flag) == "" {
				flag = "--text-file"
			}
			payload, err := clientPrivateValue(arguments[1:], flag)
			if err != nil {
				return err
			}
			input["text"] = string(payload)
		}
		prepared, err := capabilityService.Prepare(context.Background(), "im.sdk.message.send", input, fmt.Sprint(input["source"]))
		if err != nil {
			if stagedMedia != "" {
				_ = os.Remove(stagedMedia)
			}
			return writeCapabilityRejection(write, err)
		}
		if prepared.Submitted {
			_ = feishu.WakeQueue(dataRoot, "actionbox")
		}
		if prepared.Operation.Status == feishu.OperationAwaitingConfirmation || clientHasFlag(arguments[1:], "--async") {
			return write(feishu.PublicResult(map[string]any{"status": "ok", "operation": prepared.Operation, "challenge": prepared.Challenge, "submitted": prepared.Submitted}))
		}
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			view, statusErr := capabilityService.Status(prepared.Operation.ID)
			if statusErr != nil {
				return statusErr
			}
			if operationTerminal(view.Status) {
				return write(feishu.PublicResult(map[string]any{"status": "ok", "operation": view}))
			}
			time.Sleep(250 * time.Millisecond)
		}
		view, _ := capabilityService.Status(prepared.Operation.ID)
		return write(feishu.PublicResult(map[string]any{"status": "pending", "operation": view, "nextAction": operationPendingNextAction(view)}))
	case "doc":
		return runDocumentClient(dataRoot, capabilityService, arguments[1:], write)
	case "capability":
		if len(arguments) < 2 {
			return errors.New("missing capability action")
		}
		action := arguments[1]
		manifest, err := feishu.LoadCapabilityManifest()
		if err != nil {
			return err
		}
		if action == "catalog" {
			domain := clientOptionalFlag(arguments[2:], "--domain")
			items := []any{}
			for _, definition := range manifest.Capabilities {
				if feishu.CapabilityPublished(definition) && (domain == "" || definition.Domain == domain) {
					items = append(items, publicCapability(definition))
				}
			}
			return write(map[string]any{"status": "ok", "count": len(items), "capabilities": items})
		}
		if len(arguments) < 3 {
			return errors.New("missing capability id")
		}
		definition, ok := feishu.CapabilityByID(arguments[2])
		if !ok {
			return errors.New("unknown capability")
		}
		if action == "get" {
			return write(map[string]any{"status": "ok", "capability": publicCapability(definition)})
		}
		if action != "read" && action != "write" {
			return errors.New("capability action must be catalog, get, read, or write")
		}
		payload, err := clientPayload(arguments[3:])
		if err != nil {
			return err
		}
		var input map[string]any
		if json.Unmarshal(payload, &input) != nil {
			return errors.New("invalid capability payload")
		}
		if action == "read" && definition.Risk != "read" {
			return errors.New("write capability must enter actionbox")
		}
		if action == "write" && definition.Risk == "read" {
			return errors.New("read capability must not enter actionbox")
		}
		if clientHasFlag(arguments[3:], "--dry-run") {
			if err := feishu.ValidateCapabilityInput(definition.ID, input); err != nil {
				return err
			}
			return write(map[string]any{"status": "dry_run", "capability": definition.ID, "submitted": false})
		}
		prepared, err := capabilityService.Prepare(context.Background(), definition.ID, input, "codex")
		if err != nil {
			return writeCapabilityRejection(write, err)
		}
		if prepared.Submitted {
			_ = feishu.WakeQueue(dataRoot, "actionbox")
		}
		if prepared.Operation.Status == feishu.OperationAwaitingConfirmation || clientHasFlag(arguments[3:], "--async") || action == "read" {
			return write(feishu.PublicResult(map[string]any{"status": "ok", "operation": prepared.Operation, "challenge": prepared.Challenge, "submitted": prepared.Submitted, "result": prepared.Result}))
		}
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			view, statusErr := capabilityService.Status(prepared.Operation.ID)
			if statusErr != nil {
				return statusErr
			}
			if operationTerminal(view.Status) {
				return write(feishu.PublicResult(map[string]any{"status": "ok", "operation": view}))
			}
			time.Sleep(250 * time.Millisecond)
		}
		view, _ := capabilityService.Status(prepared.Operation.ID)
		return write(feishu.PublicResult(map[string]any{"status": "pending", "operation": view, "nextAction": operationPendingNextAction(view)}))
	case "operation":
		return runOperationClient(dataRoot, capabilityService, arguments[1:], write)
	case "policy":
		return runPolicyClient(capabilityService, arguments[1:], write)
	case "message", "knowledge", "calendar", "task", "sheets", "base", "meeting", "note", "minutes":
		return runConvenienceClient(dataRoot, settings, capabilityService, arguments[0], arguments[1:], write)
	case "workflow":
		return runWorkflowClient(dataRoot, capabilityService, arguments[1:], write)
	case "task-link":
		if len(arguments) < 2 {
			return errors.New("missing task-link action")
		}
		action := arguments[1]
		switch action {
		case "protocol":
			return write(map[string]any{"status": "ok", "protocol": feishu.TaskLinkProtocol, "version": 2, "schemaVersion": feishu.TaskLinkSchema, "readiness": map[string]any{"ready": settings.Outbound.Enabled && !settings.Outbound.DryRun, "blockers": taskLinkBlockers(settings)}})
		case "list":
			file, err := store.Load()
			if err != nil {
				return err
			}
			return write(map[string]any{"status": "ok", "protocol": feishu.TaskLinkProtocol, "version": 2, "links": feishu.PublicLinks(file.Links)})
		case "status":
			key, err := clientFlag(arguments[2:], "--task-key")
			if err != nil {
				return err
			}
			link, found, err := store.FindAnyByTaskKey(key)
			if err != nil {
				return err
			}
			if !found {
				return errors.New("task link not found")
			}
			return write(map[string]any{"status": "ok", "protocol": feishu.TaskLinkProtocol, "version": 2, "link": feishu.PublicLinks([]feishu.TaskLink{link})[0]})
		case "create":
			payload, err := clientPayload(arguments[2:])
			if err != nil {
				return err
			}
			var request struct {
				ThreadID    string `json:"threadId"`
				Title       string `json:"title"`
				ProjectName string `json:"projectName"`
				TargetAlias string `json:"targetAlias"`
			}
			if err := json.Unmarshal(payload, &request); err != nil {
				return errors.New("invalid task-link payload")
			}
			link, err := store.Upsert(request.ThreadID, request.Title, request.ProjectName, request.TargetAlias)
			if err != nil {
				return err
			}
			if link.RootMessageID == "" {
				config, err := feishu.NewClientConfigStore(dataRoot).Load()
				if err != nil {
					return err
				}
				target, err := config.ResolveMessageTarget(request.TargetAlias)
				if err != nil {
					return err
				}
				credentials, err := feishu.LoadOfficialCredentials()
				if err != nil {
					return err
				}
				messages, err := feishu.NewOfficialMessageClient(credentials.AppID, credentials.AppSecret)
				if err != nil {
					return err
				}
				card, err := feishu.TaskLinkCardJSON(link)
				if err != nil {
					return err
				}
				idempotencyKey, err := feishu.TaskLinkCardIdempotencyKey(link)
				if err != nil {
					return err
				}
				messageID, err := messages.Send(context.Background(), target, "card", card, idempotencyKey)
				if err != nil {
					return err
				}
				link, err = store.Update(link.TaskKey, func(value *feishu.TaskLink) {
					value.Target = target
					value.RootMessageID = messageID
					value.MessageIDs = append(value.MessageIDs, messageID)
				})
				if err != nil {
					return err
				}
			}
			return write(map[string]any{"status": "ok", "protocol": feishu.TaskLinkProtocol, "version": 2, "link": feishu.PublicLinks([]feishu.TaskLink{link})[0]})
		case "release", "interrupt", "sync":
			key, err := clientFlag(arguments[2:], "--task-key")
			if err != nil {
				return err
			}
			if action == "sync" {
				link, found, err := store.FindAnyByTaskKey(key)
				if err != nil {
					return err
				}
				if !found {
					return errors.New("task link not found")
				}
				link, err = store.UpdateByID(link.ID, func(value *feishu.TaskLink) { value.SetExtraValue("cardSyncPending", true) })
				if err != nil {
					return err
				}
				status := syncTaskLinkCardFromClient(context.Background(), store, link)
				return write(map[string]any{"status": "ok", "cardSync": status, "protocol": feishu.TaskLinkProtocol, "version": 2, "link": feishu.PublicLinks([]feishu.TaskLink{link})[0]})
			}
			if action == "interrupt" {
				if err := submitBridgeControl(dataRoot, "taskLink.interrupt", key); err != nil {
					return err
				}
			}
			var link feishu.TaskLink
			if action == "release" {
				link, err = store.Release(key)
			} else {
				link, err = store.Update(key, func(link *feishu.TaskLink) {
					link.TurnState = "interrupted"
					link.TurnOwner = "none"
					link.ActionRequired = "none"
					link.Phase = "已停止"
					link.Detail = "当前任务已标记为中断。"
				})
			}
			if err != nil {
				return err
			}
			status := "released"
			cardSync := "not_required"
			if action == "release" && feishu.TaskLinkCardMessageID(link) != "" {
				cardSync = syncTaskLinkCardFromClient(context.Background(), store, link)
			}
			if action == "interrupt" {
				status = "interrupted"
			}
			return write(map[string]any{"status": status, "cardSync": cardSync, "protocol": feishu.TaskLinkProtocol, "version": 2, "link": feishu.PublicLinks([]feishu.TaskLink{link})[0]})
		}
	}
	return fmt.Errorf("unsupported native bridge client command: %s", arguments[0])
}

func submitBridgeControl(dataRoot, operation, taskKey string) error {
	id, err := feishu.NewControlID()
	if err != nil {
		return err
	}
	inbox := feishu.NewControlInbox(dataRoot)
	if err := inbox.Submit(feishu.ControlRequest{ID: id, Operation: operation, TaskKey: taskKey, CreatedAt: time.Now().UTC()}); err != nil {
		return err
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		result, found, err := inbox.TakeResult(id)
		if err != nil {
			return err
		}
		if found {
			if result.Status != "succeeded" {
				return errors.New("managed bridge rejected control request")
			}
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("managed bridge control timeout")
}

func nativeDoctor(dataRoot string, settings feishu.Settings, runner feishu.CapabilityExecutor) map[string]any {
	type check struct {
		Name     string `json:"name"`
		OK       bool   `json:"ok"`
		Severity string `json:"severity"`
		Detail   string `json:"detail"`
	}
	checks := []check{}
	add := func(name string, err error, detail string) {
		item := check{Name: name, OK: err == nil, Severity: "ok", Detail: detail}
		if err != nil {
			item.Severity = "error"
			item.Detail = err.Error()
		}
		checks = append(checks, item)
	}
	add("settings", settings.Validate(), feishu.SettingsFilename)
	_, configErr := feishu.NewClientConfigStore(dataRoot).Load()
	add("client_config", configErr, "client.json v4")
	_, linksErr := feishu.NewTaskLinkStore(dataRoot).Load()
	add("task_links", linksErr, "task links v2")
	present, alive, _, _, instanceErr := feishu.InstanceStatus(dataRoot)
	instanceDetail := "not running"
	if present && alive {
		instanceDetail = "running"
	}
	add("managed_process", instanceErr, instanceDetail)
	_, manifestErr := feishu.LoadCapabilityManifest()
	add("capability_registry", manifestErr, "versioned fixed capabilities")
	_, scopeErr := feishu.RequiredPermissionScopes()
	add("permission_contract", scopeErr, "frozen bot and user scopes")
	credentialErr := error(nil)
	if settings.Profile == feishu.ProfilePrimary || settings.Outbound.Enabled {
		_, credentialErr = feishu.LoadOfficialCredentials()
	}
	add("credentials", credentialErr, "secure platform credential store")
	larkProbe := feishu.ProbeLarkCLI(context.Background(), runner.Binary)
	var larkErr error
	if larkProbe.State != "ready" {
		larkErr = errors.New(larkProbe.Code)
	}
	add("lark_cli", larkErr, larkProbe.Detail)
	ok := true
	for _, item := range checks {
		if item.Severity == "error" {
			ok = false
			break
		}
	}
	health := "healthy"
	if !ok {
		health = "failed"
	}
	return map[string]any{"ok": ok, "health": health, "runtime": "go", "checks": checks, "status": map[string]any{"profile": settings.Profile, "outbound": settings.Outbound, "docbox": settings.Docbox, "actionbox": settings.Actionbox}}
}

// Kept for compatibility tests and explicit one-shot client operation. The
// production service uses WorkScheduler.Wake and never starts a second queue
// consumer.
func actionboxWakeHandler(ctx context.Context, service *feishu.CapabilityService) func() {
	return func() { _ = service.ProcessActions(ctx) }
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

func runTaskCardReconciliation(ctx context.Context, runtime *inboundRuntime) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		runtime.reconcileTaskLinkCards(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func syncTaskLinkCardFromClient(ctx context.Context, store feishu.TaskLinkStore, link feishu.TaskLink) string {
	credentials, err := feishu.LoadOfficialCredentials()
	if err != nil {
		return "pending"
	}
	messages, err := feishu.NewOfficialMessageClient(credentials.AppID, credentials.AppSecret)
	if err != nil {
		return "pending"
	}
	if err := feishu.SyncTaskLinkCard(ctx, store, messages, link, ""); err != nil {
		return "pending"
	}
	return "synced"
}

func publicCapability(definition feishu.CapabilityDefinition) map[string]any {
	fields := []map[string]any{}
	for _, name := range definition.FlagOrder {
		schema := definition.Flags[name]
		fields = append(fields, map[string]any{"name": name, "type": schema.Type, "required": schema.Required, "private": schema.Private})
	}
	queue := definition.Queue
	if definition.Risk == "read" {
		queue = "direct"
	}
	kinds := []string{}
	seen := map[string]bool{}
	for _, raw := range definition.ResultIdentifiers {
		var descriptor map[string]any
		if json.Unmarshal(raw, &descriptor) == nil {
			kind := strings.TrimSpace(fmt.Sprint(descriptor["kind"]))
			if kind != "" && kind != "<nil>" && !seen[kind] {
				seen[kind] = true
				kinds = append(kinds, kind)
			}
		}
	}
	var preflight, reread any
	if definition.Preflight != nil {
		preflight = definition.Preflight.ID
	}
	if definition.Reread != nil {
		reread = definition.Reread.ID
	}
	return map[string]any{"id": definition.ID, "domain": definition.Domain, "published": feishu.CapabilityPublished(definition), "identity": definition.Identity, "risk": definition.Risk, "backend": definition.Backend, "effect": definition.Effect, "reversibility": definition.Reversibility, "guardProfile": definition.GuardProfile, "postcondition": definition.Postcondition, "conflictKey": definition.ConflictKey, "retryClass": definition.RetryClass, "executionClass": definition.ExecutionClass, "requiredScopes": definition.RequiredScopes, "queue": queue, "inputFields": fields, "bounded": definition.Scope.Bounded, "preflight": preflight, "reread": reread, "savableResultKinds": kinds, "redaction": definition.Redaction}
}
func clientOptionalFlag(arguments []string, wanted string) string {
	value, _ := clientFlag(arguments, wanted)
	return value
}
func clientHasFlag(arguments []string, wanted string) bool {
	for _, value := range arguments {
		if value == wanted {
			return true
		}
	}
	return false
}
func clientLimit(arguments []string, fallback int) int {
	value := clientOptionalFlag(arguments, "--limit")
	if value == "" {
		return fallback
	}
	number, err := strconv.Atoi(value)
	if err != nil || number < 1 {
		return fallback
	}
	if number > 100 {
		return 100
	}
	return number
}

func taskLinkBlockers(settings feishu.Settings) []string {
	blockers := []string{}
	if !settings.Outbound.Enabled {
		blockers = append(blockers, "outbound")
	}
	if settings.Outbound.DryRun {
		blockers = append(blockers, "dryRun")
	}
	return blockers
}

func clientFlag(arguments []string, wanted string) (string, error) {
	for i := 0; i+1 < len(arguments); i++ {
		if arguments[i] == wanted && strings.TrimSpace(arguments[i+1]) != "" {
			return arguments[i+1], nil
		}
	}
	return "", fmt.Errorf("missing %s", wanted)
}

func clientPayload(arguments []string) ([]byte, error) {
	return clientPrivateValue(arguments, "--payload-file")
}

func clientPrivateValue(arguments []string, flag string) ([]byte, error) {
	value, err := clientFlag(arguments, flag)
	if err != nil {
		return nil, err
	}
	if value == "-" {
		data, err := io.ReadAll(io.LimitReader(os.Stdin, 4*1024*1024+1))
		if err != nil {
			return nil, err
		}
		if len(data) > 4*1024*1024 {
			return nil, errors.New("private input exceeds 4 MiB")
		}
		return data, nil
	}
	info, err := os.Lstat(value)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 4*1024*1024 {
		return nil, errors.New("unsafe private input file")
	}
	return os.ReadFile(value)
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
