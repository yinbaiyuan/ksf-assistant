package taskruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMain(tests *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == "hold-task-lock" {
		err := withLock(context.Background(), os.Args[2], func() error {
			fmt.Println("locked")
			for {
				time.Sleep(time.Hour)
			}
		})
		if err != nil {
			os.Exit(10)
		}
		os.Exit(0)
	}
	if len(os.Args) > 1 && os.Args[1] == "route" {
		if os.Getenv("CODEX_THREAD_ID") != "" {
			os.Exit(9)
		}
		data, err := os.ReadFile("verifier-response.json")
		if err != nil {
			os.Exit(8)
		}
		var expected []string
		arguments, err := os.ReadFile("verifier-arguments.json")
		actual := os.Args[1:]
		if len(actual) < 2 || actual[len(actual)-2] != "--root" {
			os.Exit(6)
		}
		cwd, _ := os.Getwd()
		if actual[len(actual)-1] != cwd {
			os.Exit(5)
		}
		actual = actual[:len(actual)-2]
		if err != nil || json.Unmarshal(arguments, &expected) != nil || !bytes.Equal(canonicalJSON(expected), canonicalJSON(actual)) {
			os.Exit(7)
		}
		fmt.Printf("KSF_ROUTE_CONTEXT_VERIFIED %s\n", data)
		os.Exit(0)
	}
	os.Exit(tests.Run())
}

type fixture struct {
	store *Store
	root  string
	key   string
	now   time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "AGENTS.md"), []byte("root governance must remain untouched\n"), 0o600)
	if err := os.Mkdir(filepath.Join(root, ".agents"), 0o700); err != nil {
		t.Fatal(err)
	}
	value := &fixture{root: root, key: filepath.Join(t.TempDir(), "private"), now: time.Date(2026, 9, 6, 1, 2, 3, 0, time.UTC)}
	store, err := Open(root, Options{KeyDirectory: value.key, Now: func() time.Time { return value.now }})
	if err != nil {
		t.Fatal(err)
	}
	value.store = store
	return value
}

func writeTestFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}

func (value *fixture) receipt(t *testing.T, projects ...string) json.RawMessage {
	t.Helper()
	source := func(path string) Source {
		content := []byte("fixture source: " + path)
		writeTestFile(t, filepath.Join(value.root, filepath.FromSlash(path)), content, 0o600)
		return Source{Path: path, Bytes: int64(len(content)), SHA256: digest(content)}
	}
	receipt := Receipt{Version: 6, Protocol: "verified-route-projection-v6", Status: "verified", TaskIntent: "Implement independent task state", TaskRef: "sha256:" + digest([]byte("route")), SourceBindingDigest: "sha256:" + digest([]byte("binding")), CatalogSHA256: "sha256:" + digest([]byte("catalog")), AggregateSHA256: digest([]byte("aggregate")), ReceiptSHA256: digest([]byte("receipt")), DispatchableSkills: []Skill{}}
	receipt.Projection.Category = Category{Source: source("category.md"), CategoryID: "development", Name: "Development", ValidationStatus: "草案", ContextPolicy: "route-only"}
	receipt.Projection.Jobs = []Job{{Source: source("job.md"), JobID: "engineer", Name: "Engineer", Role: "main", ValidationStatus: "待验证", ContextPolicy: "progressive"}}
	receipt.Projection.Abilities = []Ability{{Source: source("ability.md"), AbilityID: "testing", Name: "Testing", JobID: "engineer", ResponsibilityID: "R1", ValidationStatus: "待验证", ContextPolicy: "progressive"}}
	receipt.Projection.ContextFiles = []Source{}
	for _, name := range projects {
		receipt.Projection.ContextFiles = append(receipt.Projection.ContextFiles, source("10项目/"+name+"/项目记忆卡.md"))
	}
	for _, entry := range receipt.sources() {
		receipt.ExpectedCount++
		receipt.SourceBytes += entry.Bytes
	}
	data := canonicalJSON(receipt)
	value.verifier(t, data)
	return data
}

func (value *fixture) verifier(t *testing.T, receipt json.RawMessage) {
	t.Helper()
	parsed, err := ParseReceipt(receipt)
	if err != nil {
		t.Fatal(err)
	}
	args, err := parsed.arguments()
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(value.root, "verifier-response.json"), receipt, 0o600)
	writeTestFile(t, filepath.Join(value.root, "verifier-arguments.json"), canonicalJSON(args), 0o600)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	name := "ksf-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	writeTestFile(t, filepath.Join(value.root, ".agents", "bin", name), data, 0o700)
}

func reportRequest(identity, event string, revision uint64, scope string, receipt json.RawMessage) Request {
	state := &ReportedState{Scope: scope, ReportedStatus: "running", Receipt: receipt}
	if scope == "project" {
		state.ProjectCard = "10项目/Alpha/项目记忆卡.md"
	}
	return Request{Protocol: Protocol, Version: 1, Host: "codex", ThreadID: identity, EventID: event, ExpectedRevision: &revision, State: state}
}

func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	var failure *Error
	if !errors.As(err, &failure) || failure.Code != code {
		t.Fatalf("wanted %s, got %v", code, err)
	}
}

func TestStableIdentityAcrossRouteProjectAndProgressChanges(t *testing.T) {
	value := newFixture(t)
	ctx := context.Background()
	receipt := value.receipt(t)
	first, _, _, err := value.store.Report(ctx, reportRequest("private-thread-one", "event-1", 0, "ksf", receipt))
	if err != nil {
		t.Fatal(err)
	}
	receipt = value.receipt(t, "Alpha")
	secondRequest := reportRequest("private-thread-one", "event-2", 1, "project", receipt)
	percent := 30
	secondRequest.State.Progress = &Progress{Summary: "Added storage", Percent: &percent}
	value.now = value.now.Add(time.Minute)
	second, history, _, err := value.store.Report(ctx, secondRequest)
	if err != nil {
		t.Fatal(err)
	}
	third, _, _, err := value.store.Report(ctx, reportRequest("private-thread-one", "event-3", 2, "unresolved", nil))
	if err != nil {
		t.Fatal(err)
	}
	if first.TaskID != second.TaskID || third.TaskID != first.TaskID || len(history) != 2 || second.ProjectCard != "10项目/Alpha/项目记忆卡.md" || third.ProjectCard != "" || third.State.Progress != nil {
		t.Fatalf("unstable or lossy identity/state: %+v", third)
	}
	key, err := os.ReadFile(filepath.Join(value.key, "identity.key"))
	if err != nil {
		t.Fatal(err)
	}
	if first.TaskID != taskID(key, "codex", "private-thread-one") || strings.Contains(first.TaskID, digest([]byte("private-thread-one"))) {
		t.Fatal("task ID is not the host-scoped HMAC")
	}
	other, _, _, err := value.store.Report(ctx, reportRequest("private-thread-two", "event-1", 0, "unresolved", nil))
	if err != nil || other.TaskID == first.TaskID {
		t.Fatal("different tasks collided", err)
	}
	_ = filepath.WalkDir(filepath.Join(value.root, filepath.FromSlash(recordDirectory)), func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			data, _ := os.ReadFile(path)
			if strings.Contains(string(data), "private-thread-one") || strings.Contains(path, "private-thread-one") || bytes.Contains(data, key) {
				t.Errorf("private identity/key leaked to %s", path)
			}
		}
		return err
	})
	governance, _ := os.ReadFile(filepath.Join(value.root, "AGENTS.md"))
	if string(governance) != "root governance must remain untouched\n" {
		t.Fatal("root governance changed")
	}
}

func TestIdempotencyConflictsAndReplayAfterWorkspaceChanges(t *testing.T) {
	value := newFixture(t)
	ctx := context.Background()
	request := reportRequest("private-thread-one", "event-1", 0, "ksf", value.receipt(t))
	first, _, _, err := value.store.Report(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = value.store.Report(ctx, reportRequest("private-thread-one", "event-2", 1, "unresolved", nil))
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(value.root, "category.md"), []byte("changed source"), 0o600)
	replayed, history, replay, err := value.store.Report(ctx, request)
	if err != nil || !replay || replayed.Revision != 1 || len(history) != 1 || replayed.RouteFreshness != "stale" {
		t.Fatal("event retry was not stable", replayed, err)
	}
	current, _, err := value.store.Get(ctx, first.TaskID)
	if err != nil || current.Revision != 2 {
		t.Fatal("retry rolled back current snapshot")
	}
	changed := request
	revision := uint64(1)
	changed.ExpectedRevision = &revision
	_, _, _, err = value.store.Report(ctx, changed)
	requireCode(t, err, "event_conflict")
	_, _, _, err = value.store.Report(ctx, reportRequest("private-thread-one", "new-event", 0, "unresolved", nil))
	requireCode(t, err, "revision_conflict")
}

func TestCurrentVerifierRejectsFabricatedReceiptsAndScope(t *testing.T) {
	value := newFixture(t)
	t.Setenv("CODEX_THREAD_ID", "never-forward-private-identity")
	receipt := value.receipt(t, "Alpha")
	_, _, _, err := value.store.Report(context.Background(), reportRequest("private-thread-one", "event-1", 0, "project", receipt))
	if err != nil {
		t.Fatal("current verifier should run without raw identity", err)
	}
	var fabricated map[string]any
	_ = json.Unmarshal(receipt, &fabricated)
	fabricated["catalog_sha256"] = "sha256:" + digest([]byte("fabricated"))
	fabricated["receipt_sha256"] = digest(canonicalJSON(fabricated))
	_, _, _, err = value.store.Report(context.Background(), reportRequest("private-thread-two", "event-1", 0, "project", canonicalJSON(fabricated)))
	requireCode(t, err, "route_invalid")
	_, _, _, err = value.store.Report(context.Background(), reportRequest("private-thread-two", "event-1", 0, "ksf", receipt))
	if err != nil {
		t.Fatal("reference project roots must not force project scope", err)
	}
	ambiguous := value.receipt(t, "Alpha", "Beta")
	view, _, _, err := value.store.Report(context.Background(), reportRequest("private-thread-three", "event-1", 0, "unresolved", ambiguous))
	if err != nil || view.ProjectCard != "" || view.State.Scope != "unresolved" {
		t.Fatal("ambiguous project was bound", err)
	}
}

func TestSourceFreshnessAndReportedTimeAreIndependent(t *testing.T) {
	value := newFixture(t)
	view, _, _, err := value.store.Report(context.Background(), reportRequest("private-thread-one", "event-1", 0, "ksf", value.receipt(t)))
	if err != nil {
		t.Fatal(err)
	}
	value.now = value.now.Add(time.Hour)
	current, _, err := value.store.Get(context.Background(), view.TaskID)
	if err != nil || current.ReportFreshness != "stale" || current.RouteFreshness != "current" || current.State.ReportedStatus != "running" {
		t.Fatal("freshness changed reported status", err)
	}
	writeTestFile(t, filepath.Join(value.root, "ability.md"), []byte("source changed"), 0o600)
	stale, _, err := value.store.Get(context.Background(), view.TaskID)
	if err != nil || stale.RouteFreshness != "stale" || !stale.ReportedAt.Equal(view.ReportedAt) || stale.Revision != 1 {
		t.Fatal("source changes rewrote reporting time", err)
	}
}

func TestConcurrentReportsUseExpectedRevisionAndAtomicHistory(t *testing.T) {
	value := newFixture(t)
	ctx := context.Background()
	initial, _, _, err := value.store.Report(ctx, reportRequest("private-thread-one", "create", 0, "unresolved", nil))
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	results := make(chan error, 16)
	for index := 0; index < 16; index++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			_, _, _, err := value.store.Report(ctx, reportRequest("private-thread-one", fmt.Sprintf("race-%d", index), 1, "unresolved", nil))
			results <- err
		}(index)
	}
	workers.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			requireCode(t, err, "revision_conflict")
		}
	}
	if successes != 1 {
		t.Fatalf("got %d successful competing writes", successes)
	}
	view, history, err := value.store.Get(ctx, initial.TaskID)
	if err != nil || view.Revision != 2 || len(history) != 2 {
		t.Fatal("snapshot/history mismatch", err)
	}
}

func TestRecordCorruptionIsNeverOverwritten(t *testing.T) {
	for _, variant := range []string{"unknown", "version", "snapshot", "history", "workspace"} {
		t.Run(variant, func(t *testing.T) {
			value := newFixture(t)
			view, _, _, err := value.store.Report(context.Background(), reportRequest("private-thread-one", "create", 0, "unresolved", nil))
			if err != nil {
				t.Fatal(err)
			}
			path, _ := value.store.dataPath(view.TaskID+".json", false)
			data, _ := os.ReadFile(path)
			var content map[string]any
			_ = json.Unmarshal(data, &content)
			switch variant {
			case "unknown":
				content["future_state"] = true
			case "version":
				content["version"] = 2
			case "snapshot":
				content["snapshot"].(map[string]any)["revision"] = 7
			case "history":
				content["history"].([]any)[0].(map[string]any)["payload_sha256"] = "tampered"
			case "workspace":
				content["snapshot"].(map[string]any)["workspace_digest"] = "other-root"
			}
			data = canonicalJSON(content)
			writeTestFile(t, path, data, 0o600)
			_, _, _, err = value.store.Report(context.Background(), reportRequest("private-thread-one", "next", 1, "unresolved", nil))
			if err == nil {
				t.Fatal("corrupt record overwritten")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(data, after) {
				t.Fatal("failure changed existing record")
			}
		})
	}
}

func TestUnknownReceiptFieldsAndSourceTraversalFail(t *testing.T) {
	value := newFixture(t)
	receipt := value.receipt(t)
	for _, variant := range []string{"unknown", "traversal", "memory-node", "version"} {
		var content map[string]any
		_ = json.Unmarshal(receipt, &content)
		projection := content["projection"].(map[string]any)
		switch variant {
		case "unknown":
			projection["category"].(map[string]any)["new_authority"] = true
		case "traversal":
			projection["category"].(map[string]any)["path"] = "../escape.md"
		case "memory-node":
			projection["context_files"] = []Source{{Path: "10项目/Alpha/项目记忆/node.md", SHA256: digest(nil)}}
		case "version":
			content["version"] = 7
		}
		_, err := ParseReceipt(canonicalJSON(content))
		if err == nil {
			t.Fatalf("accepted %s", variant)
		}
	}
}

func TestPrivateIdentityRejectedInAllPersistedFields(t *testing.T) {
	value := newFixture(t)
	for _, field := range []string{"progress", "event", "receipt"} {
		request := reportRequest("private-thread-one", "event-1", 0, "unresolved", nil)
		switch field {
		case "progress":
			request.State.Progress = &Progress{Summary: "Working on private-thread-one"}
		case "event":
			request.EventID = "event-private-thread-one"
		case "receipt":
			receipt := value.receipt(t)
			var parsed Receipt
			_ = json.Unmarshal(receipt, &parsed)
			parsed.TaskIntent = "Working on private-thread-one"
			request.State.Scope = "ksf"
			request.State.Receipt = canonicalJSON(parsed)
		}
		_, _, _, err := value.store.Report(context.Background(), request)
		requireCode(t, err, "invalid_identity")
	}
	if _, err := os.Stat(value.key); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid reports initialized the key")
	}
}

func TestReadOnlyOperationsDoNotInitializeFilesOrKey(t *testing.T) {
	value := newFixture(t)
	views, next, err := value.store.List(context.Background(), 25, "")
	if err != nil || len(views) != 0 || next != "" {
		t.Fatal(err)
	}
	doctor := value.store.Doctor(context.Background())
	if doctor.Records != 0 || doctor.Key != "missing" || doctor.Store != "missing" {
		t.Fatal(doctor)
	}
	_, err = os.Stat(filepath.Join(value.root, ".agents", "runtime-data"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read created workspace state")
	}
	_, err = os.Stat(value.key)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read created identity key")
	}
}

func TestListPaginationAndDistinctUserKeys(t *testing.T) {
	value := newFixture(t)
	for index := 0; index < 3; index++ {
		_, _, _, err := value.store.Report(context.Background(), reportRequest(fmt.Sprintf("private-thread-%d", index), "create", 0, "unresolved", nil))
		if err != nil {
			t.Fatal(err)
		}
	}
	first, next, err := value.store.List(context.Background(), 2, "")
	if err != nil || len(first) != 2 || next != first[1].TaskID {
		t.Fatal("bad first page", err)
	}
	last, next, err := value.store.List(context.Background(), 2, next)
	if err != nil || len(last) != 1 || next != "" || last[0].TaskID <= first[1].TaskID {
		t.Fatal("bad last page", err)
	}
	other := newFixture(t).store
	newTask, _, _, err := other.Report(context.Background(), reportRequest("private-thread-0", "create", 0, "unresolved", nil))
	if err != nil {
		t.Fatal(err)
	}
	originalID, err := value.store.TaskID(context.Background(), "codex", "private-thread-0")
	if err != nil || newTask.TaskID == originalID {
		t.Fatal("per-user keys did not isolate identity")
	}
}
