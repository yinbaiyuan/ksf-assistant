package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"ksfassistant/core/internal/corebridge"
	managedfeishu "ksfassistant/core/internal/feishu"
	"ksfassistant/core/internal/feishuprotocol"
	"ksfassistant/core/internal/integration"
	"ksfassistant/core/internal/privateipc"
)

type bridgeFixtureConnection struct {
	ctx        context.Context
	generation uint64
	err        error
}

func newServiceBridgeFixture(t *testing.T, mode string) (*Service, <-chan bridgeFixtureConnection) {
	t.Helper()
	root := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{home: root, feishuDataRoot: root, hostContextStore: integration.NewHostContextStore(root)}
	connected := make(chan bridgeFixtureConnection, 8)
	supervisor := managedfeishu.NewSupervisor(managedfeishu.SupervisorOptions{
		Executable: executable, DataRoot: root,
		Arguments:   []string{"-test.run=^TestServiceBridgeFixtureProcess$"},
		Environment: []string{"KSF_SERVICE_BRIDGE_FIXTURE=" + mode},
		Handler:     service,
		OnConnect: func(ctx context.Context, generation uint64) error {
			err := service.connectManagedBridge(ctx, generation)
			connected <- bridgeFixtureConnection{ctx: ctx, generation: generation, err: err}
			return err
		},
	})
	service.managedFeishuSupervisor = supervisor
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = supervisor.Stop(ctx)
	})
	if err := supervisor.Start(); err != nil {
		t.Fatal(err)
	}
	if mode == "normal" {
		connection := fixtureConnection(t, connected)
		if connection.err != nil {
			t.Fatal(connection.err)
		}
	}
	return service, connected
}

func fixtureConnection(t *testing.T, connected <-chan bridgeFixtureConnection) bridgeFixtureConnection {
	t.Helper()
	select {
	case connection := <-connected:
		return connection
	case <-time.After(4 * time.Second):
		t.Fatal("fixture connection initialization timed out")
		return bridgeFixtureConnection{}
	}
}

func TestServiceBridgeFixtureProcess(t *testing.T) {
	mode := os.Getenv("KSF_SERVICE_BRIDGE_FIXTURE")
	if mode == "" {
		return
	}
	var mutex sync.Mutex
	initialized := false
	settings := managedfeishu.DefaultSettings()
	setup := managedfeishu.DefaultSetupState()
	var peer *privateipc.Peer
	peer = privateipc.NewPeer(os.Stdin, os.Stdout, privateipc.HandlerFunc(func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		mutex.Lock()
		defer mutex.Unlock()
		if method == feishuprotocol.Initialize {
			var request feishuprotocol.InitializeRequest
			if err := privateipc.DecodeStrict(params, &request, true); err != nil {
				return nil, err
			}
			if request.Protocol != feishuprotocol.Protocol {
				return nil, errors.New("fixture requires v2 handshake")
			}
			protocol := feishuprotocol.Protocol
			if mode == "wrong-protocol" {
				protocol = "fixture-incompatible-protocol"
			}
			initialized = true
			return feishuprotocol.InitializeResult{Protocol: protocol, Version: "fixture"}, nil
		}
		if !initialized {
			return nil, errors.New("fixture is not initialized")
		}
		switch method {
		case "fixture/crash":
			os.Exit(7)
			return nil, nil
		case feishuprotocol.SnapshotRead:
			if err := privateipc.RequireNoParams(params); err != nil {
				return nil, err
			}
			return feishuprotocol.Snapshot{Revision: 1, Configured: true, Availability: "ready", RuntimeKind: "fixture", ProcessState: "running", TargetAliases: []string{"fixture"}}, nil
		case feishuprotocol.SettingsRead:
			return settings, privateipc.RequireNoParams(params)
		case feishuprotocol.SettingsWrite:
			if err := privateipc.DecodeStrict(params, &settings, true); err != nil {
				return nil, err
			}
			return settings, nil
		case feishuprotocol.SetupRead:
			return setup, privateipc.RequireNoParams(params)
		case feishuprotocol.SetupWrite:
			if err := privateipc.DecodeStrict(params, &setup, true); err != nil {
				return nil, err
			}
			return setup, nil
		case "fixture/push":
			var accepted feishuprotocol.Accepted
			err := peer.Call(ctx, feishuprotocol.SnapshotPush, params, &accepted)
			return accepted, err
		default:
			return nil, privateipc.ErrMethodNotFound
		}
	}))
	_ = peer.Serve(context.Background())
	os.Exit(0)
}

func TestServiceBridgeNormalV2HandshakeAndEpochReset(t *testing.T) {
	service, connected := newServiceBridgeFixture(t, "normal")
	supervisor := service.managedFeishuSupervisor
	first := supervisor.Generation()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	var accepted feishuprotocol.Accepted
	if err := supervisor.Call(ctx, "fixture/push", feishuprotocol.Snapshot{Revision: 99, Availability: "ready"}, &accepted); err != nil || !accepted.Accepted {
		t.Fatalf("snapshot push failed: %v", err)
	}
	service.mu.Lock()
	before := service.lastFeishu.Revision
	service.mu.Unlock()
	if before != 99 {
		t.Fatal("wrapper did not inject current generation")
	}
	if err := supervisor.Restart(ctx); err != nil {
		t.Fatal(err)
	}
	connection := fixtureConnection(t, connected)
	if connection.err != nil {
		t.Fatal(connection.err)
	}
	if connection.generation != first+1 {
		t.Fatal("restart epoch did not advance")
	}
	service.mu.Lock()
	revision, generation := service.lastFeishu.Revision, service.feishuGeneration
	service.mu.Unlock()
	if revision != 1 || generation != connection.generation {
		t.Fatalf("snapshot revision not reset per epoch: revision=%d generation=%d", revision, generation)
	}
	stale, _ := json.Marshal(feishuprotocol.Snapshot{Revision: 1000, Availability: "stale"})
	if _, err := service.HandlePrivateRPC(managedfeishu.WithEpoch(ctx, first), feishuprotocol.SnapshotPush, stale); !errors.Is(err, managedfeishu.ErrStaleGeneration) {
		t.Fatalf("old snapshot accepted: %v", err)
	}
	if _, err := service.HandlePrivateRPC(ctx, feishuprotocol.SnapshotPush, stale); !errors.Is(err, managedfeishu.ErrStaleGeneration) {
		t.Fatalf("unbound snapshot accepted: %v", err)
	}
	if err := service.connectManagedBridge(managedfeishu.WithEpoch(ctx, first), first); !errors.Is(err, managedfeishu.ErrStaleGeneration) {
		t.Fatalf("delayed old initialization accepted: %v", err)
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.feishuGeneration != connection.generation || service.lastFeishu.Revision != 1 {
		t.Fatalf("delayed old initialization erased current snapshot: generation=%d revision=%d", service.feishuGeneration, service.lastFeishu.Revision)
	}
}

func TestServiceBridgeRejectsWrongHandshakeProtocol(t *testing.T) {
	_, connected := newServiceBridgeFixture(t, "wrong-protocol")
	if connection := fixtureConnection(t, connected); connection.err == nil {
		t.Fatal("incompatible handshake accepted")
	}
}

func TestServiceBridgeAutomaticReconnectInitializesFreshSnapshot(t *testing.T) {
	service, connected := newServiceBridgeFixture(t, "normal")
	supervisor := service.managedFeishuSupervisor
	first := supervisor.Generation()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if err := supervisor.Call(ctx, "fixture/push", feishuprotocol.Snapshot{Revision: 99, Availability: "ready"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Call(ctx, "fixture/crash", nil, nil); err == nil {
		t.Fatal("crashing child returned success")
	}
	connection := fixtureConnection(t, connected)
	if connection.err != nil {
		t.Fatal(connection.err)
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if connection.generation != first+1 || service.lastFeishu.Revision != 1 || service.feishuGeneration != connection.generation {
		t.Fatalf("automatic reconnect reused old snapshot: generation=%d revision=%d", service.feishuGeneration, service.lastFeishu.Revision)
	}
}

func TestServicePrivateControlIsLocalNotBridgeWire(t *testing.T) {
	service, _ := newServiceBridgeFixture(t, "normal")
	ctx := managedfeishu.WithEpoch(context.Background(), service.managedFeishuSupervisor.Generation())
	for _, method := range []string{"core/codex/control", "core/codex/projection/read", "core/capabilities/read", "core/ksf/context/read"} {
		if _, err := service.HandlePrivateRPC(ctx, method, json.RawMessage(`{}`)); !errors.Is(err, privateipc.ErrMethodNotFound) {
			t.Fatalf("Core method exposed on bridge wire: %s: %v", method, err)
		}
	}
	_, err := service.privateControl(ctx, corebridge.ControlRequest{Protocol: corebridge.Protocol, RequestID: "fixture-request-1", TaskKey: "fixture-task", Operation: "task.create", RuntimeOwner: "bridge"})
	var remote *privateipc.RPCError
	if !errors.As(err, &remote) || remote.Code != -32051 {
		t.Fatalf("local control no longer reports missing Codex: %v", err)
	}
	if _, err := service.privateControl(ctx, corebridge.ControlRequest{}); !errors.As(err, &remote) || remote.Code != -32602 {
		t.Fatalf("invalid local control accepted: %v", err)
	}
}

func TestServiceEventProfileReadIsFixedAndDoesNotRewriteLegacySettings(t *testing.T) {
	service, _ := newServiceBridgeFixture(t, "normal")
	settings, err := service.FeishuSettings()
	if err != nil {
		t.Fatal(err)
	}
	settings.Profile = "manual-only"
	if err := (remoteSettingsStore{service}).Save(settings); err != nil {
		t.Fatal(err)
	}
	result, err := service.FeishuProfile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state := result["eventConsumer"].(map[string]any)
	if state["profile"] != "managed" || state["profileValid"] != true || state["configurable"] != false || state["desiredConnection"] != true {
		t.Fatalf("legacy role drove read projection: %+v", result)
	}
	loaded, err := service.FeishuSettings()
	if err != nil || loaded.Profile != "manual-only" {
		t.Fatalf("compatibility read rewrote settings: %+v %v", loaded, err)
	}
}

func TestServiceSettingsAndSetupUseRemoteBridgeWithoutCoreFiles(t *testing.T) {
	service, _ := newServiceBridgeFixture(t, "normal")
	settings, err := service.FeishuSettings()
	if err != nil {
		t.Fatal(err)
	}
	settings.Group.Enabled = true
	if err := (remoteSettingsStore{service}).Save(settings); err != nil {
		t.Fatal(err)
	}
	loaded, err := service.FeishuSettings()
	if err != nil || loaded.Group.Enabled != settings.Group.Enabled {
		t.Fatalf("remote settings: %#v %v", loaded, err)
	}
	setup, err := service.FeishuSetup()
	if err != nil {
		t.Fatal(err)
	}
	setup.Stage = managedfeishu.SetupAppPending
	if err := (remoteSetupStore{service}).Save(setup); err != nil {
		t.Fatal(err)
	}
	loadedSetup, err := service.FeishuSetup()
	if err != nil || loadedSetup.Stage != setup.Stage {
		t.Fatalf("remote setup: %#v %v", loadedSetup, err)
	}
	for _, name := range []string{managedfeishu.SettingsFilename, managedfeishu.SetupFilename} {
		if _, err := os.Stat(filepath.Join(service.feishuDataRoot, name)); !os.IsNotExist(err) {
			t.Fatalf("Core wrote remote-owned %s: %v", name, err)
		}
	}
}
