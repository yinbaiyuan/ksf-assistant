package taskruntime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestExplicitScopeAllowsReferenceProjectsWithoutBinding(t *testing.T) {
	value := newFixture(t)
	receipt := value.receipt(t, "Alpha", "Beta")
	for index, scope := range []string{"ksf", "unresolved", "project"} {
		request := reportRequest(fmt.Sprintf("private-thread-%d", index), "create", 0, scope, receipt)
		if scope == "project" {
			request.State.ProjectCard = "10项目/Beta/项目记忆卡.md"
		}
		view, _, _, err := value.store.Report(context.Background(), request)
		if err != nil {
			t.Fatalf("explicit %s rejected: %v", scope, err)
		}
		if scope == "project" && view.ProjectCard != request.State.ProjectCard || scope != "project" && view.ProjectCard != "" {
			t.Fatal("reference root inferred a binding")
		}
	}
	for _, project := range []string{"", "10项目/Other/项目记忆卡.md"} {
		request := reportRequest("private-thread-new", "create", 0, "project", receipt)
		request.State.ProjectCard = project
		_, _, _, err := value.store.Report(context.Background(), request)
		requireCode(t, err, "route_invalid")
	}
	request := reportRequest("private-thread-new", "create", 0, "ksf", receipt)
	request.State.ProjectCard = "10项目/Alpha/项目记忆卡.md"
	_, _, _, err := value.store.Report(context.Background(), request)
	requireCode(t, err, "route_invalid")
}

func TestConcurrentIdenticalRetriesCommitExactlyOnce(t *testing.T) {
	value := newFixture(t)
	request := reportRequest("private-thread-one", "same-event", 0, "unresolved", nil)
	var workers sync.WaitGroup
	results := make(chan bool, 24)
	errors := make(chan error, 24)
	for index := 0; index < 24; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, _, replayed, err := value.store.Report(context.Background(), request)
			errors <- err
			results <- replayed
		}()
	}
	workers.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	replays := 0
	for replayed := range results {
		if replayed {
			replays++
		}
	}
	if replays != 23 {
		t.Fatalf("wanted 23 replays, got %d", replays)
	}
	identity, _ := value.store.TaskID(context.Background(), "codex", request.ThreadID)
	view, changes, err := value.store.Get(context.Background(), identity)
	if err != nil || view.Revision != 1 || len(changes) != 1 {
		t.Fatal("duplicate event was committed twice", err)
	}
}

func TestConcurrentDifferentTasksShareOnePrivateKey(t *testing.T) {
	value := newFixture(t)
	var workers sync.WaitGroup
	results := make(chan error, 12)
	for index := 0; index < 12; index++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			store, err := Open(value.root, Options{KeyDirectory: value.key})
			if err == nil {
				_, _, _, err = store.Report(context.Background(), reportRequest(fmt.Sprintf("private-thread-%d", index), "create", 0, "unresolved", nil))
			}
			results <- err
		}(index)
	}
	workers.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	views, _, err := value.store.List(context.Background(), 25, "")
	if err != nil || len(views) != 12 {
		t.Fatal("independent reports were lost", err)
	}
	for index := 0; index < 12; index++ {
		identity, err := value.store.TaskID(context.Background(), "codex", fmt.Sprintf("private-thread-%d", index))
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := value.store.Get(context.Background(), identity); err != nil {
			t.Fatal("key creation race orphaned task", err)
		}
	}
}

func TestAtomicReadersNeverSeePartialSnapshotOrHistory(t *testing.T) {
	value := newFixture(t)
	first, _, _, err := value.store.Report(context.Background(), reportRequest("private-thread-one", "create", 0, "unresolved", nil))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		defer close(done)
		for revision := uint64(1); revision < 32; revision++ {
			if _, _, _, err := value.store.Report(context.Background(), reportRequest("private-thread-one", fmt.Sprintf("change-%d", revision), revision, "unresolved", nil)); err != nil {
				result <- err
				return
			}
		}
		result <- nil
	}()
	defer func() { <-done }()
	for {
		view, history, err := value.store.Get(context.Background(), first.TaskID)
		if err != nil || int(view.Revision) != len(history) {
			t.Fatal("reader observed torn snapshot/history", err)
		}
		select {
		case <-done:
			if err := <-result; err != nil {
				t.Fatal(err)
			}
			return
		default:
		}
	}
}

func TestProcessDeathReleasesLockAndOrphanTempIsNotARecord(t *testing.T) {
	value := newFixture(t)
	first, _, _, err := value.store.Report(context.Background(), reportRequest("private-thread-one", "create", 0, "unresolved", nil))
	if err != nil {
		t.Fatal(err)
	}
	path, _ := value.store.dataPath(first.TaskID+".lock", false)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, executable, "hold-task-lock", path)
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "locked\n" {
		t.Fatal("child did not acquire lock", err)
	}
	writeTestFile(t, filepath.Join(filepath.Dir(path), ".task-interrupted.tmp"), []byte(`{"partial":`), 0o600)
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	views, _, err := value.store.List(ctx, 25, "")
	if err != nil || len(views) != 1 || views[0].Revision != 1 {
		t.Fatal("orphan temp became a task or changed snapshot", err)
	}
	updated, _, _, err := value.store.Report(ctx, reportRequest("private-thread-one", "after-crash", 1, "unresolved", nil))
	if err != nil || updated.Revision != 2 {
		t.Fatal("process death left a stale task lock", err)
	}
}

func TestConcurrentCreationCannotExceedWorkspaceCapacity(t *testing.T) {
	value := newFixture(t)
	first, _, _, err := value.store.Report(context.Background(), reportRequest("private-thread-seed", "create", 0, "unresolved", nil))
	if err != nil {
		t.Fatal(err)
	}
	path, _ := value.store.dataPath(first.TaskID+".json", false)
	for index := 0; index < maxRecords-2; index++ {
		writeTestFile(t, filepath.Join(filepath.Dir(path), fmt.Sprintf("task_%064x.json", index)), []byte(`{}`), 0o600)
	}
	var workers sync.WaitGroup
	results := make(chan error, 2)
	for _, identity := range []string{"private-thread-final-one", "private-thread-final-two"} {
		workers.Add(1)
		go func(identity string) {
			defer workers.Done()
			_, _, _, err := value.store.Report(context.Background(), reportRequest(identity, "create", 0, "unresolved", nil))
			results <- err
		}(identity)
	}
	workers.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else {
			requireCode(t, err, "limit_exceeded")
		}
	}
	if winners != 1 {
		t.Fatalf("capacity CAS had %d winners", winners)
	}
	ids, err := value.store.recordIDs()
	if err != nil || len(ids) != maxRecords {
		t.Fatal("workspace record cap exceeded", len(ids), err)
	}
}

func TestLockContentionHonorsCancellation(t *testing.T) {
	value := newFixture(t)
	first, _, _, err := value.store.Report(context.Background(), reportRequest("private-thread-one", "create", 0, "unresolved", nil))
	if err != nil {
		t.Fatal(err)
	}
	path, _ := value.store.dataPath(first.TaskID+".lock", false)
	err = withLock(context.Background(), path, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
		defer cancel()
		_, _, _, err := value.store.Report(ctx, reportRequest("private-thread-one", "next", 1, "unresolved", nil))
		requireCode(t, err, "busy")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	view, _, err := value.store.Get(context.Background(), first.TaskID)
	if err != nil || view.Revision != 1 {
		t.Fatal("cancelled writer committed")
	}
}

func TestSymlinksHardlinksAndUnsafeFilesFailClosed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows link privileges vary; CreateFile reparse guard is cross-compiled")
	}
	for _, target := range []string{"ancestor", "snapshot", "lock", "key", "hardlink", "source"} {
		t.Run(target, func(t *testing.T) {
			value := newFixture(t)
			request := reportRequest("private-thread-one", "create", 0, "unresolved", nil)
			view, _, _, err := value.store.Report(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			external := filepath.Join(t.TempDir(), "external")
			original := []byte("must not be changed")
			writeTestFile(t, external, original, 0o600)
			var victim string
			switch target {
			case "ancestor":
				victim = filepath.Join(value.root, ".agents", "runtime-data")
				if err := os.Rename(victim, victim+"-saved"); err != nil {
					t.Fatal(err)
				}
				external = filepath.Dir(external)
			case "snapshot", "hardlink":
				victim, _ = value.store.dataPath(view.TaskID+".json", false)
			case "lock":
				victim, _ = value.store.dataPath(view.TaskID+".lock", false)
			case "key":
				victim = filepath.Join(value.key, "identity.key")
			case "source":
				request.State.Scope, request.State.Receipt = "ksf", value.receipt(t)
				victim = filepath.Join(value.root, "category.md")
			}
			if target != "ancestor" {
				if err := os.Remove(victim); err != nil {
					t.Fatal(err)
				}
			}
			if target == "hardlink" {
				err = os.Link(external, victim)
			} else {
				err = os.Symlink(external, victim)
			}
			if err != nil {
				t.Fatal(err)
			}
			revision := uint64(1)
			request.ExpectedRevision, request.EventID = &revision, "next"
			_, _, _, err = value.store.Report(context.Background(), request)
			if err == nil {
				t.Fatal("unsafe path accepted")
			}
			if target != "ancestor" {
				after, _ := os.ReadFile(external)
				if !bytes.Equal(after, original) {
					t.Fatal("external file modified")
				}
			}
		})
	}
}

func TestHistoryLimitPreservesAllPriorEvents(t *testing.T) {
	value := newFixture(t)
	request := reportRequest("private-thread-one", "create", 0, "unresolved", nil)
	view, _, _, err := value.store.Report(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	record, err := value.store.load(view.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	for index := 1; index < maxHistory; index++ {
		snapshot := record.Snapshot
		snapshot.Revision = uint64(index + 1)
		event := fmt.Sprintf("event-%d", index)
		record.History = append(record.History, Change{EventID: event, PayloadSHA256: payloadDigest("codex", event, uint64(index), snapshot.State), Snapshot: snapshot})
		record.Snapshot = snapshot
	}
	path, _ := value.store.dataPath(view.TaskID+".json", false)
	before := canonicalJSON(record)
	writeTestFile(t, path, before, 0o600)
	_, _, _, err = value.store.Report(context.Background(), reportRequest("private-thread-one", "overflow", maxHistory, "unresolved", nil))
	requireCode(t, err, "limit_exceeded")
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("history was pruned")
	}
	_, _, replayed, err := value.store.Report(context.Background(), request)
	if err != nil || !replayed {
		t.Fatal("history cap broke original event replay", err)
	}
}

func TestStrictJSONCLIContract(t *testing.T) {
	value := newFixture(t)
	t.Setenv("CODEX_THREAD_ID", "private-thread-one")
	for _, test := range []struct{ name, input, code string }{
		{"unknown", `{"protocol":"ksfassistant-task-runtime-v1","version":1,"extra":true}`, "invalid_request"},
		{"duplicate", `{"protocol":"ksfassistant-task-runtime-v1","version":1,"version":1}`, "invalid_request"},
		{"trailing", `{"protocol":"ksfassistant-task-runtime-v1","version":1}{}`, "invalid_request"},
		{"version", `{"protocol":"ksfassistant-task-runtime-v1","version":2}`, "unsupported_protocol"},
		{"nested", `{"protocol":"ksfassistant-task-runtime-v1","version":1,"event_id":"create","expected_revision":0,"state":{"scope":"unresolved","reported_status":"running","unexpected":true}}`, "invalid_request"},
		{"identity", `{"protocol":"ksfassistant-task-runtime-v1","version":1,"thread_id":"different-thread"}`, "invalid_identity"},
		{"utf8", "{\"bad\":\"\xff\"}", "invalid_request"},
		{"oversize", strings.Repeat(" ", MaxRequestBytes+1), "limit_exceeded"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			status := Run(context.Background(), []string{"report", "--root", value.root}, strings.NewReader(test.input), &output, Options{KeyDirectory: value.key})
			var response Response
			if err := json.Unmarshal(output.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if status != 1 || response.Error == nil || response.Error.Code != test.code {
				t.Fatalf("got %s", output.String())
			}
			if strings.Contains(output.String(), "private-thread-one") || strings.Contains(output.String(), "different-thread") {
				t.Fatal("error leaked raw identity")
			}
		})
	}
	var output bytes.Buffer
	status := Run(context.Background(), []string{"get", "--root", value.root, "--thread-id", "secret-thread"}, strings.NewReader(`{}`), &output, Options{KeyDirectory: value.key})
	if status != 1 || strings.Contains(output.String(), "secret-thread") {
		t.Fatal("argv identity accepted or echoed")
	}
}
