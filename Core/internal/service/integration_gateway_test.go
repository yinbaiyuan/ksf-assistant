package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ksfassistant/core/internal/desktop"
	"ksfassistant/core/internal/domain"
	"ksfassistant/core/internal/feishu"
	"ksfassistant/core/internal/feishucli"
	"ksfassistant/core/internal/feishuprotocol"
	"ksfassistant/core/internal/feishutypes"
	"ksfassistant/core/internal/integration"
	"ksfassistant/core/internal/localipc"
	"ksfassistant/core/internal/privateipc"
	"ksfassistant/core/internal/privatestore"
)

func TestIntegrationGatewayFixtureProcess(t *testing.T) {
	if os.Getenv("KSF_INTEGRATION_GATEWAY_FIXTURE") != "1" {
		return
	}
	peer := privateipc.NewPeer(os.Stdin, os.Stdout, privateipc.HandlerFunc(func(ctx context.Context, method string, raw json.RawMessage) (any, error) {
		switch method {
		case feishuprotocol.Initialize:
			return feishuprotocol.InitializeResult{Protocol: feishuprotocol.Protocol, Version: "gateway-fixture"}, nil
		case feishuprotocol.SnapshotRead:
			return feishuprotocol.Snapshot{Revision: 1, Configured: true, Availability: "ready", ProcessState: "running", TargetAliases: []string{"me"}}, nil
		case feishuprotocol.SettingsRead:
			settings := feishu.DefaultSettings()
			settings.Outbound.Enabled, settings.Outbound.DryRun = true, false
			return settings, nil
		case feishuprotocol.ConfigRead:
			config := feishutypes.DefaultClientConfig()
			config.MessageTargets["me"] = feishutypes.MessageTarget{Type: "open_id", ID: "fixture-user"}
			config.DirectAllowedAliases = []string{"me"}
			return config, nil
		case feishuprotocol.ClientExecute:
			var request feishucli.Request
			if err := json.Unmarshal(raw, &request); err != nil {
				return nil, err
			}
			return map[string]any{"status": "ok", "command": request.Command, "action": request.Action, "fixture": true}, nil
		default:
			return nil, privateipc.ErrMethodNotFound
		}
	}))
	_ = peer.Serve(context.Background())
	os.Exit(0)
}

func gatewayBridgeFixture(t *testing.T, configure func(*Service)) *Service {
	t.Helper()
	root := t.TempDir()
	service := &Service{home: root, feishuDataRoot: root, hostContextStore: integration.NewHostContextStore(root)}
	t.Cleanup(service.Close)
	if configure != nil {
		configure(service)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	connected := make(chan error, 1)
	service.managedFeishuSupervisor = feishu.NewSupervisor(feishu.SupervisorOptions{
		Executable: executable, DataRoot: root,
		Arguments:   []string{"-test.run=^TestIntegrationGatewayFixtureProcess$"},
		Environment: []string{"KSF_INTEGRATION_GATEWAY_FIXTURE=1"}, Handler: service,
		OnConnect: func(ctx context.Context, generation uint64) error {
			err := service.connectManagedBridge(ctx, generation)
			select {
			case connected <- err:
			default:
			}
			return err
		},
	})
	if err := service.managedFeishuSupervisor.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-connected:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("gateway fixture handshake timed out")
	}
	return service
}

type gatewayCorePort struct{ integration.CorePort }

func (gatewayCorePort) ReadThread(ctx context.Context, owner, threadID, turnID string) (map[string]any, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

type gatewayMessagePort struct {
	integration.FeishuPort
	calls atomic.Int32
	reply func(context.Context) error
}

func (port *gatewayMessagePort) Reply(ctx context.Context, messageID, format, content, key string) (string, error) {
	port.calls.Add(1)
	if port.reply != nil {
		return "fixture-reply", port.reply(ctx)
	}
	return "fixture-reply", nil
}

func gatewayRuntime(t *testing.T, root string, messages *gatewayMessagePort) *integration.Runtime {
	t.Helper()
	runtime, err := integration.NewRuntime(root, messages, gatewayCorePort{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	return runtime
}

func gatewayJSON(t *testing.T, value any) map[string]json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func gatewayExactKeys(t *testing.T, value map[string]json.RawMessage, expected string) {
	t.Helper()
	got := make([]string, 0, len(value))
	for key := range value {
		got = append(got, key)
	}
	want := strings.Fields(expected)
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("JSON keys = %v; want legacy keys %v", got, want)
	}
}

func gatewayTaskCommand(t *testing.T, service *Service, action, taskKey string) map[string]json.RawMessage {
	t.Helper()
	request := feishucli.Request{Command: "task-link", Action: action}
	if taskKey != "" {
		request.Options = map[string]string{"task-key": taskKey}
	}
	value, err := service.taskLinkCommand(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	result := gatewayJSON(t, value)
	if string(result["status"]) != `"ok"` || string(result["protocol"]) != `"codex-feishu-task-link-v1"` || string(result["version"]) != "2" {
		t.Errorf("legacy envelope changed: %s", mustGatewayJSON(t, value))
	}
	return result
}

func mustGatewayJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestIntegrationGatewayTaskLinkLegacyJSON(t *testing.T) {
	service := gatewayBridgeFixture(t, func(service *Service) {
		service.integrationRuntime = gatewayRuntime(t, service.feishuDataRoot, &gatewayMessagePort{})
	})
	t.Run("protocol", func(t *testing.T) {
		result := gatewayTaskCommand(t, service, "protocol", "")
		gatewayExactKeys(t, result, "status protocol version schemaVersion readiness")
		if string(result["schemaVersion"]) != "2" {
			t.Errorf("schemaVersion = %s, want 2", result["schemaVersion"])
		}
		var readiness map[string]json.RawMessage
		if err := json.Unmarshal(result["readiness"], &readiness); err != nil {
			t.Errorf("readiness missing or malformed: %v", err)
			return
		}
		gatewayExactKeys(t, readiness, "ready blockers")
		if string(readiness["ready"]) != "true" || string(readiness["blockers"]) != "[]" {
			t.Errorf("configured ready fixture readiness = %s", result["readiness"])
		}
	})
	t.Run("empty-list", func(t *testing.T) {
		result := gatewayTaskCommand(t, service, "list", "")
		gatewayExactKeys(t, result, "status protocol version links")
		if string(result["links"]) != "[]" {
			t.Errorf("empty links = %s, want []", result["links"])
		}
	})
	store := service.integrationRuntime.Store()
	link, err := store.Upsert("private-thread", "Task", "Project", "me")
	if err != nil {
		t.Fatal(err)
	}
	link, err = store.UpdateByID(link.ID, func(value *integration.TaskLink) {
		value.LinkState = "released"
		value.SetExtraString("privateFixture", "not-public")
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"list", "status"} {
		t.Run(action, func(t *testing.T) {
			result := gatewayTaskCommand(t, service, action, map[string]string{"status": link.TaskKey}[action])
			var public map[string]json.RawMessage
			if action == "list" {
				gatewayExactKeys(t, result, "status protocol version links")
				var links []map[string]json.RawMessage
				if err := json.Unmarshal(result["links"], &links); err != nil || len(links) != 1 {
					t.Fatalf("unexpected links: %s %v", result["links"], err)
				}
				public = links[0]
			} else {
				gatewayExactKeys(t, result, "status protocol version link")
				if err := json.Unmarshal(result["link"], &public); err != nil {
					t.Fatal(err)
				}
			}
			gatewayExactKeys(t, public, "taskKey title projectName targetAlias linkState turnState turnOwner actionRequired controls createdAt updatedAt expiresAt remainingSeconds hasPendingMessage detailAvailable phase detailSummary")
			if string(public["linkState"]) != `"released"` || string(public["taskKey"]) != `"`+link.TaskKey+`"` {
				t.Errorf("status selected wrong public record: %v", public)
			}
			if strings.Contains(string(mustGatewayJSON(t, result)), "private-thread") || strings.Contains(string(mustGatewayJSON(t, result)), "not-public") {
				t.Fatal("private task fields leaked")
			}
		})
	}
}

func TestIntegrationGatewayStoppedFeishuKeepsLocalReadsAndRejectsRemoteCalls(t *testing.T) {
	root := t.TempDir()
	service := &Service{feishuDataRoot: root, integrationRuntime: gatewayRuntime(t, root, &gatewayMessagePort{})}
	link, err := service.integrationRuntime.Store().Upsert("offline-thread", "Offline", "", "me")
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"protocol", "list", "status"} {
		t.Run(action, func(t *testing.T) {
			gatewayTaskCommand(t, service, action, map[string]string{"status": link.TaskKey}[action])
		})
	}
	value, err := service.taskLinkCommand(context.Background(), feishucli.Request{Command: "profile", Action: "show"})
	if err == nil {
		t.Fatalf("stopped daemon reported success: %#v", value)
	}
	if _, err := (integrationFeishuPort{service}).Send(context.Background(), integration.MessageTarget{}, "text", "test", "fixture"); err == nil {
		t.Fatal("stopped Feishu send succeeded")
	}
	if _, err := (integrationFeishuPort{service}).StageInbound(context.Background(), integration.InboundMessage{}, 1024); err == nil {
		t.Fatal("stopped Feishu staging succeeded")
	}
	if err := (integrationFeishuPort{service}).CleanupInbound(context.Background(), "fixture-only"); err == nil {
		t.Fatal("stopped Feishu cleanup succeeded")
	}
}

func TestIntegrationGatewayCloseRemovesLocalEndpoint(t *testing.T) {
	root := t.TempDir()
	service := &Service{feishuDataRoot: root}
	t.Cleanup(service.Close)
	if err := service.initializeBusinessIntegration(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var result json.RawMessage
	request := feishucli.Request{Command: "task-link", Action: "list"}
	if err := localipc.Call(ctx, root, feishucli.MethodExecute, request, &result); err == nil {
		t.Fatal("retired CLI endpoint was created")
	}
	service.Close()
	if err := localipc.Call(ctx, root, feishucli.MethodExecute, request, &result); err == nil {
		t.Fatal("stopped Core gateway still accepted CLI calls")
	}
	if service.integrationRuntime.Health().State != "stopped" {
		t.Fatal("service.Close did not close Runtime")
	}
}

func gatewayEvent(t *testing.T, id string) feishuprotocol.Event {
	t.Helper()
	message := integration.InboundMessage{EventID: id, MessageID: "message-" + id, ChatID: "fixture-chat", SenderOpenID: "fixture-user", MessageType: "text"}
	return feishuprotocol.Event{ID: id, Kind: "message", Payload: mustGatewayJSON(t, message)}
}

func waitGatewayEventState(t *testing.T, root, id, state string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var file struct {
			SchemaVersion int                          `json:"schemaVersion"`
			Events        []map[string]json.RawMessage `json:"events"`
		}
		missing, err := privatestore.ReadJSON(filepath.Join(root, "integration-events-v1.json"), &file)
		if !missing && err == nil {
			for _, record := range file.Events {
				var event feishuprotocol.Event
				if json.Unmarshal(record["event"], &event) == nil && event.ID == id && string(record["state"]) == `"`+state+`"` {
					return
				}
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("event %s did not durably reach %s", id, state)
}

func TestIntegrationGatewayEventACKPersistsBeforeCompletionAndDeduplicates(t *testing.T) {
	started, finish := make(chan struct{}), make(chan struct{})
	messages := &gatewayMessagePort{reply: func(ctx context.Context) error {
		close(started)
		select {
		case <-finish:
			return errors.New("remote result unknown")
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	service := gatewayBridgeFixture(t, func(service *Service) {
		service.integrationRuntime = gatewayRuntime(t, service.feishuDataRoot, messages)
	})
	ctx := feishu.WithEpoch(context.Background(), service.managedFeishuSupervisor.Generation())
	event := gatewayEvent(t, "original-event-id")
	params := mustGatewayJSON(t, event)
	for index := 0; index < 2; index++ {
		value, err := service.HandlePrivateRPC(ctx, feishuprotocol.EventDeliver, params)
		if err != nil {
			t.Fatal(err)
		}
		if string(mustGatewayJSON(t, value)) != `{"accepted":true}` {
			t.Fatalf("ACK contract changed: %#v", value)
		}
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("ACKed event not dispatched")
	}
	waitGatewayEventState(t, service.feishuDataRoot, event.ID, "running")
	close(finish)
	waitGatewayEventState(t, service.feishuDataRoot, event.ID, "outcome_unknown")
	if _, err := service.HandlePrivateRPC(ctx, feishuprotocol.EventDeliver, params); err != nil {
		t.Fatal(err)
	}
	if messages.calls.Load() != 1 {
		t.Fatal("duplicate ACK redelivery executed twice")
	}
	stale := feishu.WithEpoch(context.Background(), service.managedFeishuSupervisor.Generation()+1)
	if _, err := service.HandlePrivateRPC(stale, feishuprotocol.EventDeliver, mustGatewayJSON(t, gatewayEvent(t, "stale"))); !errors.Is(err, feishu.ErrStaleGeneration) {
		t.Fatalf("stale event epoch accepted: %v", err)
	}
	service.integrationRuntime.Close()
	if _, err := service.HandlePrivateRPC(ctx, feishuprotocol.EventDeliver, mustGatewayJSON(t, gatewayEvent(t, "closed"))); !errors.Is(err, integration.ErrRuntimeClosed) {
		t.Fatalf("closed integration ACKed event: %v", err)
	}
}

func seedGatewayPendingEvent(t *testing.T, root string, event feishuprotocol.Event) {
	t.Helper()
	var payload any
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	canonical := mustGatewayJSON(t, payload)
	digest := sha256.Sum256(append([]byte(event.Kind+"\x00"), canonical...))
	file := map[string]any{"schemaVersion": 1, "events": []any{map[string]any{
		"event": event, "digest": hex.EncodeToString(digest[:]), "partition": 0,
		"state": "pending", "attempts": 0, "acceptedAt": time.Now().UTC(),
	}}}
	if err := privatestore.WriteJSON(filepath.Join(root, "integration-events-v1.json"), file); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationGatewayHandshakeResumeActiveStartsPersistedEvents(t *testing.T) {
	messages := &gatewayMessagePort{}
	service := gatewayBridgeFixture(t, func(service *Service) {
		seedGatewayPendingEvent(t, service.feishuDataRoot, gatewayEvent(t, "restart-pending"))
		service.integrationRuntime = gatewayRuntime(t, service.feishuDataRoot, messages)
		if messages.calls.Load() != 0 {
			t.Fatal("NewRuntime dispatched before handshake")
		}
	})
	waitGatewayEventState(t, service.feishuDataRoot, "restart-pending", "completed")
	if err := service.integrationRuntime.ResumeEvents(); err != nil {
		t.Fatal(err)
	}
	if err := service.integrationRuntime.ResumeActive(); err != nil {
		t.Fatal(err)
	}
	if messages.calls.Load() != 1 {
		t.Fatal("startup/resume replayed completed event")
	}
}

func TestIntegrationGatewayResumeEventsStartsPendingWithoutNewDelivery(t *testing.T) {
	root := t.TempDir()
	messages := &gatewayMessagePort{}
	seedGatewayPendingEvent(t, root, gatewayEvent(t, "standalone-pending"))
	runtime := gatewayRuntime(t, root, messages)
	if messages.calls.Load() != 0 {
		t.Fatal("constructor dispatched without startup authorization")
	}
	if err := runtime.ResumeEvents(); err != nil {
		t.Fatal(err)
	}
	waitGatewayEventState(t, root, "standalone-pending", "completed")
	if messages.calls.Load() != 1 {
		t.Fatalf("pending event dispatch count: %d", messages.calls.Load())
	}
}

func TestIntegrationGatewayEventPersistenceFailureIsNotACKed(t *testing.T) {
	messages := &gatewayMessagePort{}
	service := gatewayBridgeFixture(t, func(service *Service) {
		service.integrationRuntime = gatewayRuntime(t, service.feishuDataRoot, messages)
		if err := os.Mkdir(filepath.Join(service.feishuDataRoot, "integration-events-v1.json"), 0o700); err != nil {
			t.Fatal(err)
		}
	})
	ctx := feishu.WithEpoch(context.Background(), service.managedFeishuSupervisor.Generation())
	result, err := service.HandlePrivateRPC(ctx, feishuprotocol.EventDeliver, mustGatewayJSON(t, gatewayEvent(t, "write-failed")))
	if err == nil {
		t.Fatal("failed durable write was ACKed")
	}
	if accepted, ok := result.(feishuprotocol.Accepted); ok && accepted.Accepted {
		t.Fatal("persistence error returned accepted=true")
	}
	if messages.calls.Load() != 0 || service.integrationRuntime.Health().State != "degraded" {
		t.Fatal("failed persistence dispatched work or hid health failure")
	}
}

func TestIntegrationGatewayCorruptInboxDoesNotExposeGenericCLI(t *testing.T) {
	service := gatewayBridgeFixture(t, func(service *Service) {
		if err := os.WriteFile(filepath.Join(service.feishuDataRoot, "integration-events-v1.json"), []byte(`{"broken"`), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := service.initializeBusinessIntegration(); err == nil {
			t.Fatal("corrupt inbox unexpectedly initialized")
		}
		if service.integrationRuntime != nil {
			t.Fatal("gateway was coupled to corrupt integration startup")
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var result json.RawMessage
	request := feishucli.Request{Command: "profile", Action: "show"}
	if err := localipc.Call(ctx, service.feishuDataRoot, feishucli.MethodExecute, request, &result); err == nil {
		t.Fatal("retired generic CLI remained available")
	}
	if _, err := service.taskLinkCommand(ctx, feishucli.Request{Command: "task-link", Action: "list"}); err == nil {
		t.Fatal("unavailable integration reported task-link success")
	}
	snapshot := service.composeIntegrationSnapshot(domain.FeishuSnapshot{Availability: "ready"})
	if snapshot.Availability != "ready" || snapshot.TaskLinkReady || snapshot.Capabilities["businessIntegration"].State != "unavailable" {
		t.Fatalf("corruption isolation lost: %#v", snapshot)
	}
}

func TestIntegrationGatewayCorruptTaskStoreDoesNotBreakHandshakeOrHideHealth(t *testing.T) {
	service := gatewayBridgeFixture(t, func(service *Service) {
		service.integrationRuntime = gatewayRuntime(t, service.feishuDataRoot, &gatewayMessagePort{})
		if err := os.WriteFile(filepath.Join(service.feishuDataRoot, "task-links-v1.json"), []byte(`{"broken"`), 0o600); err != nil {
			t.Fatal(err)
		}
	})
	if service.integrationRuntime.Health().State != "degraded" {
		t.Fatal("ResumeActive storage failure was not retained in Health")
	}
	result, err := service.taskLinkCommand(context.Background(), feishucli.Request{Command: "profile", Action: "show"})
	if err == nil || result != nil {
		t.Fatal("retired generic command unexpectedly succeeded")
	}
	snapshot := service.composeIntegrationSnapshot(domain.FeishuSnapshot{Availability: "ready"})
	health, present := snapshot.Capabilities["businessIntegration"]
	if snapshot.Availability != "ready" || snapshot.TaskLinkReady || !present || health.State != "degraded" || health.Detail == "" {
		t.Errorf("corrupt task store hid integration health: availability=%s ready=%v health=%#v present=%v", snapshot.Availability, snapshot.TaskLinkReady, health, present)
	}
}

func TestIntegrationGatewayConcurrentComposeDoesNotMutateCachedSnapshot(t *testing.T) {
	service := &Service{hostContextStore: integration.NewHostContextStore(t.TempDir())}
	blockerStorage := []string{"transport", "unused-1", "unused-2", "unused-3"}
	originalStorage := append([]string{}, blockerStorage...)
	genericHealth := domain.CapabilityHealth{State: "ready", Detail: "transport-owned"}
	cached := domain.FeishuSnapshot{
		Availability:      "ready",
		Capabilities:      map[string]domain.CapabilityHealth{"messageSend": genericHealth},
		ReadinessBlockers: blockerStorage[:1],
	}
	start := make(chan struct{})
	var workers sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			for iteration := 0; iteration < 32; iteration++ {
				snapshot := service.composeIntegrationSnapshot(cached)
				if snapshot.Availability != "ready" || snapshot.TaskLinkReady || snapshot.Capabilities["businessIntegration"].State != "unavailable" {
					t.Errorf("composition lost generic/business isolation: %#v", snapshot)
					return
				}
				if snapshot.Capabilities["messageSend"] != genericHealth || len(snapshot.Capabilities) != 5 {
					t.Errorf("another composition contaminated capabilities: %#v", snapshot.Capabilities)
					return
				}
				if !reflect.DeepEqual(snapshot.ReadinessBlockers, []string{"transport", "desktopIPC", "businessIntegration"}) {
					t.Errorf("another composition contaminated blockers: %#v", snapshot.ReadinessBlockers)
					return
				}
				snapshot.Capabilities["messageSend"] = domain.CapabilityHealth{State: "consumer-local"}
				snapshot.Capabilities["consumerOnly"] = domain.CapabilityHealth{State: "ready"}
				snapshot.ReadinessBlockers[0] = "consumer-local"
				snapshot.ReadinessBlockers[1] = "consumer-business"
				snapshot.ReadinessBlockers = append(snapshot.ReadinessBlockers, "consumer-only")
			}
		}()
	}
	close(start)
	workers.Wait()
	if !reflect.DeepEqual(cached.Capabilities, map[string]domain.CapabilityHealth{"messageSend": genericHealth}) {
		t.Errorf("composition mutated cached capabilities: %#v", cached.Capabilities)
	}
	if !reflect.DeepEqual(blockerStorage, originalStorage) {
		t.Errorf("composition mutated cached blocker backing storage: %#v", blockerStorage)
	}
	if cached.TaskLinkProtocolVersion != 0 || len(cached.ReadinessBlockers) != 1 {
		t.Errorf("composition mutated cached projection: %#v", cached)
	}
}

func TestTaskCardRuntimeWriteBlockersSurviveSnapshotComposition(t *testing.T) {
	service := gatewayBridgeFixture(t, func(service *Service) {
		service.integrationRuntime = gatewayRuntime(t, service.feishuDataRoot, &gatewayMessagePort{})
	})
	for _, blocker := range []string{"taskCardWriteDisabled", "taskCardWriteDryRun"} {
		snapshot := service.composeIntegrationSnapshot(domain.FeishuSnapshot{Availability: "ready", ReadinessBlockers: []string{blocker}})
		if snapshot.TaskLinkReady {
			t.Fatalf("write blocker %s ignored", blocker)
		}
	}
}

func TestTaskLinkReadinessRequiresAvailableDesktopIPC(t *testing.T) {
	root := t.TempDir()
	service := &Service{
		feishuDataRoot:   root,
		hostContextStore: integration.NewHostContextStore(root),
		desktop:          desktop.New(""),
	}
	service.integrationRuntime = gatewayRuntime(t, root, &gatewayMessagePort{})
	if err := service.desktop.Start(context.Background()); err == nil {
		t.Fatal("unconfigured Desktop IPC unexpectedly started")
	}

	snapshot := service.composeIntegrationSnapshot(domain.FeishuSnapshot{Availability: "ready"})
	if snapshot.TaskLinkReady {
		t.Fatal("task link reported ready while Desktop IPC was unavailable")
	}
	if snapshot.Capabilities["desktopIPC"].State == "ready" {
		t.Fatalf("Desktop IPC capability unexpectedly ready: %#v", snapshot.Capabilities["desktopIPC"])
	}
	found := false
	for _, blocker := range snapshot.ReadinessBlockers {
		if blocker == "desktopIPC" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Desktop IPC blocker missing: %#v", snapshot.ReadinessBlockers)
	}
}

func TestCoreCapabilityWorkspaceAllowsMissingKSFConfiguration(t *testing.T) {
	root := t.TempDir()
	store := integration.NewHostContextStore(root)
	if _, err := store.SaveKSFRoot(""); err != nil {
		t.Fatal(err)
	}
	service := &Service{hostContextStore: store}
	workspace, err := newCoreCapabilityClient(service).Workspace(context.Background())
	if err != nil || workspace != "" {
		t.Fatalf("not configured KSF must resolve to a projectless workspace: %q %v", workspace, err)
	}
}

func TestCoreCapabilityWorkspaceRejectsInvalidKSFConfiguration(t *testing.T) {
	root := t.TempDir()
	store := integration.NewHostContextStore(root)
	if _, err := store.SaveKSFRoot(filepath.Join(root, "missing")); err != nil {
		t.Fatal(err)
	}
	service := &Service{hostContextStore: store}
	_, err := newCoreCapabilityClient(service).Workspace(context.Background())
	if !errors.Is(err, integration.ErrInvalidThreadLaunchContext) {
		t.Fatalf("invalid KSF context must remain a visible launch error: %v", err)
	}
}

func TestApplyThreadLaunchScopesUsesTaskLinkMetadata(t *testing.T) {
	root := t.TempDir()
	runtime := gatewayRuntime(t, root, &gatewayMessagePort{})
	link, err := runtime.Store().Upsert("projectless", "Task", "", "me")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Store().UpdateByID(link.ID, func(value *integration.TaskLink) {
		value.SetExtraString("launchScope", integration.LaunchScopeProjectless)
	}); err != nil {
		t.Fatal(err)
	}
	service := &Service{integrationRuntime: runtime}
	threads := service.applyThreadLaunchScopes([]domain.CodexThread{{ID: "projectless"}, {ID: "ordinary"}})
	if threads[0].LaunchScope != integration.LaunchScopeProjectless || threads[1].LaunchScope != "" {
		t.Fatalf("unexpected launch scopes: %#v", threads)
	}
}
