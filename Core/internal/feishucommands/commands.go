package feishucommands

import (
	"encoding/json"
	"errors"
	"fmt"
	"ksfassistant/core/internal/feishu"
	"ksfassistant/core/internal/feishuprotocol"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (call *invocation) runClient(dataRoot string, settings feishu.Settings, arguments []string, write clientJSONWriter) error {
	if len(arguments) == 0 {
		return errors.New("missing bridge client command")
	}
	authRunner := feishu.CapabilityExecutor{Binary: strings.TrimSpace(os.Getenv("LARK_CLI_BIN")), Profile: strings.TrimSpace(os.Getenv("LARK_CLI_PROFILE")), DataRoot: dataRoot, WorkingDirectory: dataRoot}
	capabilityService := call.service
	switch arguments[0] {
	case "help":
		return write(map[string]any{"usage": []string{"ksf-assistant-feishu-bridge client snapshot", "ksf-assistant-feishu-bridge client status|doctor", "ksf-assistant-feishu-bridge client targets init|list|set|remove", "ksf-assistant-feishu-bridge client send ...", "ksf-assistant-feishu-bridge client task-link protocol|list|create|status|interrupt|release", "ksf-assistant-feishu-bridge client capability catalog|get|read|write", "ksf-assistant-feishu-bridge client operation prepare|confirm|cancel|status", "ksf-assistant-feishu-bridge client policy read|update", "ksf-assistant-feishu-bridge client events catalog|status|recent|get", "ksf-assistant-feishu-bridge client result <outbox|docbox|actionbox> <id>", "ksf-assistant-feishu-bridge client recent <outbox|docbox|actionbox|messages|audit>"}})
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
			payload, err := call.clientPayload(arguments[2:])
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
			result, err := feishu.ConfigureExistingApp(call.ctx, authRunner, input.AppID, input.AppSecret, input.Brand, clientOptionalFlag(arguments[2:], "--profile"))
			if err != nil {
				return err
			}
			return write(result)
		case "start-config":
			result, err := feishu.StartAppConfiguration(call.ctx, authRunner, dataRoot, clientOptionalFlag(arguments[2:], "--profile"), clientHasFlag(arguments[2:], "--create-new"))
			if err != nil {
				return err
			}
			return write(result)
		case "start-user":
			result, err := feishu.StartUserAuth(call.ctx, authRunner, dataRoot, clientOptionalFlag(arguments[2:], "--scope"))
			if err != nil {
				return err
			}
			return write(result)
		case "finish-user":
			result, err := feishu.FinishUserAuthFlow(call.ctx, authRunner, dataRoot, clientOptionalFlag(arguments[2:], "--device-code"))
			if err != nil {
				return err
			}
			return write(result)
		case "ensure-current-user":
			result, err := feishu.EnsureCurrentUser(call.ctx, authRunner, feishu.NewClientConfigStore(dataRoot))
			if err != nil {
				return err
			}
			return write(result)
		default:
			return errors.New("unsupported auth action")
		}
	case "permissions":
		result, err := feishu.AuthPermissions(call.ctx, authRunner)
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
		value, err := call.clientAggregateSnapshot(dataRoot, settings)
		if err != nil {
			return err
		}
		return write(value)
	case "doctor":
		return write(call.nativeDoctor(dataRoot, settings, authRunner))
	case "targets":
		if len(arguments) < 2 {
			return errors.New("missing targets action")
		}
		configStore := feishu.NewClientConfigStore(dataRoot)
		config, err := configStore.Load()
		if err != nil {
			return err
		}
		switch arguments[1] {
		case "directory":
			if len(arguments) < 3 {
				return errors.New("missing directory action")
			}
			return call.runDirectoryClient(settings, capabilityService, feishu.PersonDirectory, arguments[2], arguments[3:], configStore, write)
		case "group-directory":
			if len(arguments) < 3 {
				return errors.New("missing group directory action")
			}
			return call.runDirectoryClient(settings, capabilityService, feishu.GroupDirectory, arguments[2], arguments[3:], configStore, write)
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
			value, err := call.clientPrivateValue(arguments[4:], "--value-file")
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
			resolution, resolveErr := feishu.NewDirectoryService(capabilityService).Resolve(call.ctx, kind, name, config)
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
			stagedMedia, err = call.stageMedia(dataRoot, id)
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
			payload, err := call.clientPrivateValue(arguments[1:], flag)
			if err != nil {
				return err
			}
			input["text"] = string(payload)
		}
		prepared, err := call.prepare(capabilityService, call.ctx, "im.sdk.message.send", input, fmt.Sprint(input["source"]))
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
			if err := call.wait(250 * time.Millisecond); err != nil {
				return err
			}
		}
		view, _ := capabilityService.Status(prepared.Operation.ID)
		return write(feishu.PublicResult(map[string]any{"status": "pending", "operation": view, "nextAction": operationPendingNextAction(view)}))
	case "doc":
		return call.runDocumentClient(dataRoot, capabilityService, arguments[1:], write)
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
		payload, err := call.clientPayload(arguments[3:])
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
			if err := feishu.ValidateCapabilityInput(definition.ID, call.validationInput(definition, input)); err != nil {
				return err
			}
			return write(map[string]any{"status": "dry_run", "capability": definition.ID, "submitted": false})
		}
		prepared, err := call.prepare(capabilityService, call.ctx, definition.ID, input, "codex")
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
			if err := call.wait(250 * time.Millisecond); err != nil {
				return err
			}
		}
		view, _ := capabilityService.Status(prepared.Operation.ID)
		return write(feishu.PublicResult(map[string]any{"status": "pending", "operation": view, "nextAction": operationPendingNextAction(view)}))
	case "operation":
		return call.runOperationClient(dataRoot, capabilityService, arguments[1:], write)
	case "policy":
		return call.runPolicyClient(capabilityService, arguments[1:], write)
	case "message", "knowledge", "calendar", "task", "sheets", "base", "meeting", "note", "minutes":
		return call.runConvenienceClient(dataRoot, settings, capabilityService, arguments[0], arguments[1:], write)
	case "workflow":
		return call.runWorkflowClient(dataRoot, capabilityService, arguments[1:], write)
	}
	return fmt.Errorf("unsupported bridge client command: %s", arguments[0])
}

func (call *invocation) nativeDoctor(dataRoot string, settings feishu.Settings, runner feishu.CapabilityExecutor) map[string]any {
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
	larkProbe := feishu.ProbeLarkCLI(call.ctx, runner.Binary)
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

func (call *invocation) clientAggregateSnapshot(dataRoot string, settings feishu.Settings) (feishuprotocol.Snapshot, error) {
	present, alive, pid, _, err := feishu.InstanceStatus(dataRoot)
	if err != nil {
		return feishuprotocol.Snapshot{}, err
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

	capabilities := map[string]feishuprotocol.CapabilityHealth{
		"feishuInbound":  {State: "unavailable"},
		"feishuOutbound": {State: "unavailable"},
		"larkCLI":        {State: "disabled"},
		"outbox":         {State: switchState(settings.Outbound.Enabled)},
		"docbox":         {State: switchState(settings.Docbox.Enabled)},
		"actionbox":      {State: switchState(settings.Actionbox.Enabled)},
	}
	if settings.Profile != feishu.ProfilePrimary {
		capabilities["feishuInbound"] = feishuprotocol.CapabilityHealth{State: "disabled"}
	} else if inboundConnected {
		capabilities["feishuInbound"] = feishuprotocol.CapabilityHealth{State: "ready"}
	} else if alive {
		capabilities["feishuInbound"] = feishuprotocol.CapabilityHealth{State: "degraded"}
	}
	if settings.Outbound.Enabled && alive {
		capabilities["feishuOutbound"] = feishuprotocol.CapabilityHealth{State: "ready"}
	} else if settings.Outbound.Enabled {
		capabilities["feishuOutbound"] = feishuprotocol.CapabilityHealth{State: "degraded"}
	} else {
		capabilities["feishuOutbound"] = feishuprotocol.CapabilityHealth{State: "disabled"}
	}
	probe := feishu.ProbeLarkCLI(call.ctx, strings.TrimSpace(os.Getenv("LARK_CLI_BIN")))
	capabilities["larkCLI"] = feishuprotocol.CapabilityHealth{State: probe.State, Detail: probe.Detail}

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
	return feishuprotocol.Snapshot{
		RuntimeKind: "go", Availability: availability, Message: message,
		ProcessState: processState, Configured: configured, ProcessPID: pid, ProcessRunning: alive,
		Profile: settings.Profile, ProfileValid: settings.Profile == feishu.ProfilePrimary || settings.Profile == feishu.ProfileManualOnly,
		InboundConnection: inboundConnected, TargetAliases: aliases,
		Capabilities: capabilities, Queues: publicQueueHealth(feishu.QueueHealthSnapshot(dataRoot, settings)),
	}, nil
}

// runClient is the stable native replacement for `node scripts/bridge-client.js`.
// It deliberately emits the same JSON envelope for the host-facing control
// subset while the daemon remains the only long-lived process owner.

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

func clientFlag(arguments []string, wanted string) (string, error) {
	for index := 0; index+1 < len(arguments); index++ {
		if arguments[index] == wanted && strings.TrimSpace(arguments[index+1]) != "" {
			return arguments[index+1], nil
		}
	}
	return "", fmt.Errorf("missing %s", wanted)
}

func publicQueueHealth(values map[string]feishu.QueueHealth) map[string]feishuprotocol.QueueHealth {
	result := make(map[string]feishuprotocol.QueueHealth, len(values))
	for key, value := range values {
		result[key] = feishuprotocol.QueueHealth{State: value.State, Revision: value.Revision, Pending: value.Pending, Running: value.Running, Terminal: value.Terminal, Processed: value.Processed, LastError: value.LastError}
	}
	return result
}

func switchState(enabled bool) string {
	if enabled {
		return "ready"
	}
	return "disabled"
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}
func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
