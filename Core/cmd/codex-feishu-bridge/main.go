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
	"strconv"
	"strings"
	"syscall"
	"time"

	"codexusagebar/core/internal/feishu"
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
	var actionbox *feishu.Actionbox
	var docbox *feishu.Docbox
	var outbox *feishu.Outbox
	executor := feishu.CapabilityExecutor{Binary: strings.TrimSpace(os.Getenv("LARK_CLI_BIN")), Profile: strings.TrimSpace(os.Getenv("LARK_CLI_PROFILE")), DataRoot: dataRoot, WorkingDirectory: dataRoot}
	if settings.Actionbox.Enabled {
		actionbox = feishu.NewActionbox(dataRoot)
		go runActionbox(ctx, actionbox, executor)
	}
	if settings.Docbox.Enabled {
		docbox = feishu.NewDocbox(dataRoot)
		go runDocbox(ctx, docbox, executor, settings.Docbox.DryRun)
	}
	var credentials feishu.OfficialCredentials
	if settings.Profile == feishu.ProfilePrimary || settings.Outbound.Enabled {
		credentials, err = feishu.LoadOfficialCredentials()
		if err != nil {
			return fmt.Errorf("load Feishu credentials: %w", err)
		}
	}
	var messageClient *feishu.OfficialMessageClient
	if settings.Profile == feishu.ProfilePrimary || settings.Outbound.Enabled {
		messageClient, err = feishu.NewOfficialMessageClient(credentials.AppID, credentials.AppSecret)
		if err != nil {
			return err
		}
	}
	if settings.Outbound.Enabled {
		outbox = feishu.NewOutbox(dataRoot)
		go runOutbox(ctx, outbox, messageClient, settings.Outbound.DryRun)
	}
	wakeHandlers := map[string]func(){}
	if actionbox != nil {
		wakeHandlers["actionbox"] = func() { _ = actionbox.Process(ctx, executor) }
	}
	if docbox != nil {
		wakeHandlers["docbox"] = func() { _ = docbox.Process(ctx, executor, settings.Docbox.DryRun) }
	}
	if outbox != nil {
		wakeHandlers["outbox"] = func() { _ = outbox.Process(ctx, messageClient, settings.Outbound.DryRun) }
	}
	wakeServer, err := feishu.StartWakeServer(dataRoot, wakeHandlers)
	if err != nil {
		return fmt.Errorf("start queue wake server: %w", err)
	}
	if wakeServer != nil {
		defer wakeServer.Close(context.Background())
	}
	var inbound *feishu.OfficialInbound
	inboundErrors := make(chan error, 1)
	if settings.Profile == feishu.ProfilePrimary {
		eventState := feishu.NewEventConsumerStateStore(dataRoot)
		_ = eventState.UpdateConnection(settings.Profile, "starting")
		defer eventState.UpdateConnection(settings.Profile, "disconnected")
		runtimeHandler, err := newInboundRuntime(dataRoot, messageClient)
		if err != nil {
			return fmt.Errorf("initialize Codex inbound runtime: %w", err)
		}
		defer runtimeHandler.Close()
		processor, err := feishu.NewInboundProcessor(dataRoot, settings, runtimeHandler.HandleMessage, runtimeHandler.HandleCard)
		if err != nil {
			return err
		}
		if err := processor.Recover(ctx); err != nil {
			return fmt.Errorf("recover persisted Feishu inbound work: %w", err)
		}
		go runInboundRecovery(ctx, processor)
		go runTaskCardReconciliation(ctx, runtimeHandler)
		if err := runtimeHandler.ResumeActive(); err != nil {
			return fmt.Errorf("resume active Codex task links: %w", err)
		}
		inbound, err = feishu.NewOfficialInbound(credentials.AppID, credentials.AppSecret, func(_ context.Context, eventKey string, payload []byte) error {
			if err := processor.Handle(context.Background(), eventKey, payload); err != nil {
				return err
			}
			_ = eventState.MarkReceived(eventKey)
			return nil
		}, func(state string) { _ = eventState.UpdateConnection(settings.Profile, state) })
		if err != nil {
			return err
		}
		go func() { inboundErrors <- inbound.Start(ctx) }()
		defer inbound.Close()
	}
	parentClosed := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		close(parentClosed)
	}()
	select {
	case <-ctx.Done():
	case <-parentClosed:
	case err := <-inboundErrors:
		if err != nil && !errors.Is(err, context.Canceled) {
			return fmt.Errorf("Feishu inbound stopped: %w", err)
		}
	}
	return nil
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
	switch arguments[0] {
	case "help":
		return write(map[string]any{"usage": []string{"codex-feishu-bridge client status|doctor", "codex-feishu-bridge client targets init|list|set|remove", "codex-feishu-bridge client send ...", "codex-feishu-bridge client task-link protocol|list|create|status|interrupt|release", "codex-feishu-bridge client capability catalog|get|read|write", "codex-feishu-bridge client events catalog|status|recent|get", "codex-feishu-bridge client result <outbox|docbox|actionbox> <id>", "codex-feishu-bridge client recent <outbox|docbox|actionbox|messages|audit>"}})
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
		for _, definition := range manifest.Capabilities {
			items = append(items, publicCapability(definition))
		}
		scopes, err := feishu.RequiredPermissionScopes()
		if err != nil {
			return err
		}
		return write(map[string]any{"status": "ok", "capabilities": map[string]any{
			"bridgeVersion": "1.0.0", "packageVersion": "1.2.0", "capabilityVersion": "1.0.0", "stabilityBaselineVersion": "0.6.1", "queueStateSchemaVersion": 2, "skillCompatibility": "1.0.x", "larkCliVersion": "1.0.92", "officialSdk": "go", "officialSdkVersion": "3.11.0",
			"identities": []string{"bot", "user"}, "eventTransport": "official-sdk", "singleInboundConnection": true, "events": feishu.FixedEventKeys, "fixedEventCatalog": true,
			"inboundMessageTypes": []string{"text", "image", "file", "audio", "media", "post"}, "outboundMessageFormats": []string{"text", "markdown", "card", "image", "file"},
			"readCapabilities":        []string{"message.list", "message.search", "message.thread", "document.inspect", "knowledge.search", "knowledge.read", "comment.list", "calendar.agenda", "calendar.search", "calendar.get", "calendar.freebusy", "task.mine", "task.related", "task.search", "task.get", "task.tasklists", "sheets.inspect", "sheets.cells", "sheets.table", "sheets.search", "sheets.revision", "base.inspect", "base.schema", "base.records", "base.search", "base.get", "meeting.search", "meeting.active", "meeting.get", "meeting.detail", "meeting.events", "meeting.recording", "note.detail", "note.transcript", "minutes.search", "minutes.get", "minutes.detail", "minutes.transcript"},
			"queuedWriteCapabilities": []string{"base.create_records", "base.update_records", "calendar.create_event", "calendar.rsvp", "calendar.update_event", "capability.execute", "drive.add_comment", "minutes.mutate_todos", "minutes.replace_speaker", "minutes.replace_summary", "minutes.replace_words", "minutes.update_title", "minutes.upload", "sheets.append_table", "sheets.create_sheet", "sheets.set_cells", "task.assign", "task.complete", "task.create", "task.reminder", "task.reopen", "task.update"},
			"registeredCapabilities":  items, "registeredCapabilityCount": len(items), "documentWrites": []string{"create_document", "append", "overwrite", "str_replace"},
			"intentionallyExcluded": []string{"delete", "permission_mutation", "wiki_move", "arbitrary_openapi", "automatic_approval", "approval_api", "approval_event", "background_full_crawl", "live_meeting_control", "minutes_media_download", "minutes_permission_mutation", "message_delete", "chat_member_or_admin_mutation", "phone_or_sms_urgent", "sheet_clear_or_delete", "base_delete_share_permission_workflow_or_button_binding", "apps_access_member_role_secret_database_automation_cache_plugin_or_delete"},
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
		config, err := feishu.NewClientConfigStore(dataRoot).Load()
		if err != nil {
			return err
		}
		targetValue, err := clientFlag(arguments[1:], "--target")
		if err != nil {
			return err
		}
		target, err := config.ResolveMessageTarget(targetValue)
		if err != nil {
			return err
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
		request := feishu.OutboxRequest{ID: id, Type: format, Target: target, ExplicitAuthorization: true, DryRun: clientHasFlag(arguments[1:], "--dry-run"), Source: config.DefaultSource, CreatedAt: time.Now().UTC()}
		if source := clientOptionalFlag(arguments[1:], "--source"); source != "" {
			request.Source = source
		}
		if format == "image" || format == "file" {
			source, err := clientFlag(arguments[1:], "--media-file")
			if err != nil {
				return err
			}
			request.FilePath, err = feishu.StageOutboundMedia(dataRoot, id, source)
			if err != nil {
				return err
			}
		} else {
			flag := "--content-file"
			if clientOptionalFlag(arguments[1:], flag) == "" {
				flag = "--text-file"
			}
			payload, err := clientPrivateValue(arguments[1:], flag)
			if err != nil {
				return err
			}
			request.Text = string(payload)
		}
		box := feishu.NewOutbox(dataRoot)
		if err := box.Submit(request); err != nil {
			if request.FilePath != "" {
				_ = os.Remove(request.FilePath)
			}
			return err
		}
		_ = feishu.WakeQueue(dataRoot, "outbox")
		if clientHasFlag(arguments[1:], "--async") {
			return write(map[string]any{"status": "accepted", "id": id, "submitted": true})
		}
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			if result, found, err := box.FindResult(id); err != nil {
				return err
			} else if found {
				return write(feishu.PublicResult(result))
			}
			time.Sleep(250 * time.Millisecond)
		}
		return errors.New("outbox result timeout")
	case "doc":
		if len(arguments) < 2 || (arguments[1] != "create" && arguments[1] != "update") {
			return errors.New("doc action must be create or update")
		}
		if !settings.Docbox.Enabled {
			return errors.New("docbox is disabled")
		}
		config, err := feishu.NewClientConfigStore(dataRoot).Load()
		if err != nil {
			return err
		}
		content, err := clientPrivateValue(arguments[2:], "--content-file")
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(content)) == "" {
			return errors.New("document content is empty")
		}
		id, err := feishu.NewDocboxID()
		if err != nil {
			return err
		}
		format := clientOptionalFlag(arguments[2:], "--format")
		if format == "" {
			format = "markdown"
		}
		request := feishu.DocumentRequest{ID: id, Type: "document_task", Action: "create_document", Identity: "user", Content: feishu.DocumentContent{Format: format, Text: string(content)}, Instruction: "创建飞书文档", ExplicitAuthorization: true, DryRun: clientHasFlag(arguments[2:], "--dry-run"), Source: config.DefaultSource, CreatedAt: time.Now().UTC()}
		if targetValue := clientOptionalFlag(arguments[2:], "--target"); targetValue != "" {
			target, err := config.ResolveDocumentTarget(targetValue)
			if err != nil {
				return err
			}
			request.Target = &target
			if target.Kind == "wiki_url" || target.Kind == "wiki_token" {
				request.Identity = "bot"
			}
		}
		if arguments[1] == "update" {
			if request.Target == nil {
				return errors.New("missing --target")
			}
			request.Action = "update_document"
			request.Instruction = "先创建飞书官方版本，再更新并复读验证"
			request.VersionPolicy = "official_before_update"
			request.UpdateMode = clientOptionalFlag(arguments[2:], "--mode")
			if request.UpdateMode == "" {
				request.UpdateMode = "append"
			}
			if (request.UpdateMode == "overwrite" || request.UpdateMode == "str_replace") && !clientHasFlag(arguments[2:], "--confirm-high-impact") {
				return fmt.Errorf("%s requires --confirm-high-impact", request.UpdateMode)
			}
			if request.UpdateMode == "str_replace" {
				pattern, err := clientPrivateValue(arguments[2:], "--pattern-file")
				if err != nil {
					return err
				}
				request.Selection = map[string]any{"withEllipsis": string(pattern)}
			}
		}
		box := feishu.NewDocbox(dataRoot)
		if err := box.Submit(request); err != nil {
			return err
		}
		_ = feishu.WakeQueue(dataRoot, "docbox")
		if clientHasFlag(arguments[2:], "--async") {
			return write(map[string]any{"status": "accepted", "id": id, "submitted": true})
		}
		deadline := time.Now().Add(6 * time.Minute)
		for time.Now().Before(deadline) {
			if result, found, err := box.FindResult(id); err != nil {
				return err
			} else if found {
				return write(feishu.PublicResult(result))
			}
			time.Sleep(500 * time.Millisecond)
		}
		return errors.New("docbox result timeout")
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
				if domain == "" || definition.Domain == domain {
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
		runner := feishu.CapabilityExecutor{Binary: strings.TrimSpace(os.Getenv("LARK_CLI_BIN")), Profile: strings.TrimSpace(os.Getenv("LARK_CLI_PROFILE")), DataRoot: dataRoot, WorkingDirectory: dataRoot}
		if action == "read" {
			if definition.Risk != "read" {
				return errors.New("write capability must enter actionbox")
			}
			result, err := runner.Execute(context.Background(), definition.ID, input)
			if err != nil {
				return err
			}
			return write(feishu.PublicResult(map[string]any{"status": "ok", "capability": definition.ID, "result": result}))
		}
		if definition.Risk == "read" {
			return errors.New("read capability must not enter actionbox")
		}
		if clientHasFlag(arguments[3:], "--dry-run") {
			if err := feishu.ValidateCapabilityInput(definition.ID, input); err != nil {
				return err
			}
			return write(map[string]any{"status": "dry_run", "capability": definition.ID, "submitted": false})
		}
		if definition.Queue == "docbox" {
			if definition.ID != "docs.whiteboard.insert" {
				return errors.New("unsupported docbox capability")
			}
			if !settings.Docbox.Enabled {
				return errors.New("docbox is disabled")
			}
			config, err := feishu.NewClientConfigStore(dataRoot).Load()
			if err != nil {
				return err
			}
			target, err := config.ResolveDocumentTarget(fmt.Sprint(input["doc"]))
			if err != nil {
				return err
			}
			content, err := feishu.DocWhiteboardXML(input)
			if err != nil {
				return err
			}
			id, err := feishu.NewDocboxID()
			if err != nil {
				return err
			}
			identity := "user"
			if target.Kind == "wiki_url" || target.Kind == "wiki_token" {
				identity = "bot"
			}
			request := feishu.DocumentRequest{ID: id, Type: "document_task", Action: "update_document", Identity: identity, Target: &target, Content: feishu.DocumentContent{Format: "text", Text: content}, Instruction: "先读取目标文档并创建飞书官方版本，再追加 Whiteboard，完成后复读验证", VersionPolicy: "official_before_update", UpdateMode: "append", ExplicitAuthorization: true, Source: "codex", CreatedAt: time.Now().UTC()}
			box := feishu.NewDocbox(dataRoot)
			if err := box.Submit(request); err != nil {
				return err
			}
			_ = feishu.WakeQueue(dataRoot, "docbox")
			if clientHasFlag(arguments[3:], "--async") {
				return write(map[string]any{"status": "accepted", "id": id, "capability": definition.ID, "submitted": true})
			}
			deadline := time.Now().Add(6 * time.Minute)
			for time.Now().Before(deadline) {
				if result, found, err := box.FindResult(id); err != nil {
					return err
				} else if found {
					return write(feishu.PublicResult(result))
				}
				time.Sleep(500 * time.Millisecond)
			}
			return errors.New("docbox result timeout")
		}
		id, err := feishu.NewActionID()
		if err != nil {
			return err
		}
		request := feishu.ActionRequest{ID: id, Type: "feishu_capability", Domain: "capability", Action: "execute", CapabilityID: definition.ID, Identity: definition.Identity, Input: input, ExplicitAuthorization: true, ConfirmHighImpact: clientHasFlag(arguments[3:], "--confirm-high-impact"), Source: "codex", CreatedAt: time.Now().UTC()}
		if value := clientOptionalFlag(arguments[3:], "--remote-timeout-ms"); value != "" {
			request.RemoteTimeoutMS, err = strconv.Atoi(value)
			if err != nil {
				return errors.New("invalid remote timeout")
			}
		}
		if value := clientOptionalFlag(arguments[3:], "--remote-poll-ms"); value != "" {
			request.PollIntervalMS, err = strconv.Atoi(value)
			if err != nil {
				return errors.New("invalid remote poll interval")
			}
		}
		box := feishu.NewActionbox(dataRoot)
		if err := box.Submit(request); err != nil {
			return err
		}
		_ = feishu.WakeQueue(dataRoot, "actionbox")
		if clientHasFlag(arguments[3:], "--async") {
			return write(map[string]any{"status": "accepted", "id": id, "capability": definition.ID, "submitted": true})
		}
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			if result, found, err := box.FindResult(id); err != nil {
				return err
			} else if found {
				return write(feishu.PublicResult(result))
			}
			time.Sleep(250 * time.Millisecond)
		}
		return errors.New("actionbox result timeout")
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
				messageID, err := messages.Send(context.Background(), target, "card", card, "task-link-"+link.TaskKey)
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
				if err := interruptLinkedTurn(dataRoot, key); err != nil {
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
	add("capability_registry", manifestErr, "219 fixed capabilities")
	_, scopeErr := feishu.RequiredPermissionScopes()
	add("permission_contract", scopeErr, "frozen bot and user scopes")
	credentialErr := error(nil)
	if settings.Profile == feishu.ProfilePrimary || settings.Outbound.Enabled {
		_, credentialErr = feishu.LoadOfficialCredentials()
	}
	add("credentials", credentialErr, "secure platform credential store")
	larkErr := error(nil)
	if strings.TrimSpace(runner.Binary) == "" {
		larkErr = errors.New("LARK_CLI_BIN is not configured")
	} else if info, err := os.Lstat(runner.Binary); err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		if err != nil {
			larkErr = err
		} else {
			larkErr = errors.New("configured lark-cli is not an executable regular file")
		}
	}
	add("lark_cli", larkErr, "lark-cli 1.0.92")
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

func runActionbox(ctx context.Context, box *feishu.Actionbox, executor feishu.CapabilityExecutor) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		_ = box.Process(ctx, executor)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
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
	ticker := time.NewTicker(5 * time.Second)
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

func runOutbox(ctx context.Context, box *feishu.Outbox, sender feishu.MessageSender, dryRun bool) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		_ = box.Process(ctx, sender, dryRun)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func runDocbox(ctx context.Context, box *feishu.Docbox, executor feishu.CapabilityExecutor, dryRun bool) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		_ = box.Process(ctx, executor, dryRun)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
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
	return map[string]any{"id": definition.ID, "domain": definition.Domain, "identity": definition.Identity, "risk": definition.Risk, "queue": queue, "inputFields": fields, "bounded": definition.Scope.Bounded, "preflight": preflight, "reread": reread, "savableResultKinds": kinds, "redaction": definition.Redaction}
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
