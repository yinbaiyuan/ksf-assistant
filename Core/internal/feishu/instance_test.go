package feishu

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestInstanceLockPreventsSecondConsumerAndCleansUp(t *testing.T) {
	root := t.TempDir()
	first, err := AcquireInstanceLock(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	if _, err := AcquireInstanceLock(root); err == nil {
		t.Fatal("second consumer was accepted")
	}
	present, alive, _, _, err := InstanceStatus(root)
	if err != nil || !present || !alive {
		t.Fatalf("unexpected instance status: %v %v %v", present, alive, err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	present, _, _, _, err = InstanceStatus(root)
	if err != nil || present {
		t.Fatalf("instance lock was not removed: %v %v", present, err)
	}
	guardPath := filepath.Join(root, "logs", "bridge.instance.lock")
	before, err := os.Stat(guardPath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := AcquireInstanceLock(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(guardPath)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("lifetime lock file was replaced or removed: %v", err)
	}
}

func TestInstanceLockRejectsIsolatedLegacyPID(t *testing.T) {
	root := t.TempDir()
	legacy := startInstanceTestProcess(t, "legacy", root)
	record, err := json.Marshal(map[string]int{"pid": legacy.pid})
	if err != nil {
		t.Fatal(err)
	}
	path := writeInstanceTestRecord(t, root, string(record))
	if lock, err := AcquireInstanceLock(root); err == nil {
		_ = lock.Close()
		t.Fatal("active legacy PID without OS lock was accepted")
	}
	current, err := os.ReadFile(path)
	if err != nil || string(current) != string(record) {
		t.Fatalf("legacy PID record changed: %v", err)
	}
	legacy.stop()
	lock, err := AcquireInstanceLock(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestInstanceLockStaleRecordConcurrency(t *testing.T) {
	root := t.TempDir()
	stale := startInstanceTestProcess(t, "exit", root)
	stale.stop()
	writeInstanceTestRecord(t, root, fmt.Sprintf(`{"pid":%d,"startedAt":"2020-01-01T00:00:00Z"}`, stale.pid))
	const contenderCount = 8
	contenders := make([]instanceTestProcess, 0, contenderCount)
	for index := 0; index < contenderCount; index++ {
		contenders = append(contenders, startInstanceTestProcess(t, "acquire", root))
	}
	for _, contender := range contenders {
		if _, err := io.WriteString(contender.input, "start\n"); err != nil {
			t.Fatal(err)
		}
	}
	winners := 0
	for _, contender := range contenders {
		result, err := contender.output.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		switch strings.TrimSpace(result) {
		case "acquired":
			winners++
		case "rejected":
		default:
			t.Fatalf("unexpected helper response: %q", result)
		}
	}
	if winners != 1 {
		t.Fatalf("expected one stale-record winner, got %d", winners)
	}
}

func TestInstanceLockRejectsInvalidRecords(t *testing.T) {
	for _, record := range []string{"", "{", "null", "{}", `{"pid":0}`, `{"pid":-1}`, `{"pid":4294967297}`, `{"pid":1,"startedAt":"invalid"}`} {
		t.Run(record, func(t *testing.T) {
			root := t.TempDir()
			path := writeInstanceTestRecord(t, root, record)
			if lock, err := AcquireInstanceLock(root); err == nil {
				_ = lock.Close()
				t.Fatal("invalid PID record was accepted")
			}
			current, err := os.ReadFile(path)
			if err != nil || string(current) != record {
				t.Fatalf("invalid record was removed or replaced: %v", err)
			}
			if _, _, _, _, err := InstanceStatus(root); err == nil {
				t.Fatal("invalid PID status was accepted")
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			lock, err := AcquireInstanceLock(root)
			if err != nil {
				t.Fatalf("failed acquisition leaked OS lock: %v", err)
			}
			if err := lock.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInstanceLockRejectsUnsafeFiles(t *testing.T) {
	for _, filename := range []string{"bridge.pid", "bridge.instance.lock"} {
		t.Run(filename, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "logs", filename)
			if err := os.MkdirAll(path, 0o700); err != nil {
				t.Fatal(err)
			}
			if lock, err := AcquireInstanceLock(root); err == nil {
				_ = lock.Close()
				t.Fatal("directory was accepted as instance file")
			}
			if runtime.GOOS == "windows" {
				return
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(t.TempDir(), "target")
			if err := os.WriteFile(target, []byte("unchanged"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
			if lock, err := AcquireInstanceLock(root); err == nil {
				_ = lock.Close()
				t.Fatal("symlink was accepted as instance file")
			}
			current, err := os.ReadFile(target)
			if err != nil || string(current) != "unchanged" {
				t.Fatalf("symlink target changed: %v", err)
			}
		})
	}
}

func TestInstanceLockRejectsInaccessibleFiles(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires an unprivileged Unix user")
	}
	for _, filename := range []string{"bridge.pid", "bridge.instance.lock"} {
		t.Run(filename, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, "logs"), 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "logs", filename)
			if err := os.WriteFile(path, []byte("inaccessible"), 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
			if lock, err := AcquireInstanceLock(root); err == nil {
				_ = lock.Close()
				t.Fatal("inaccessible instance file was accepted")
			}
			if _, err := os.Lstat(path); err != nil {
				t.Fatalf("inaccessible instance file was removed: %v", err)
			}
		})
	}
}

func TestInstanceLockOSGuardWithoutPID(t *testing.T) {
	root := t.TempDir()
	logRoot := filepath.Join(root, "logs")
	if err := ensurePrivateDirectory(logRoot); err != nil {
		t.Fatal(err)
	}
	guard, err := acquireInstanceGuard(filepath.Join(logRoot, "bridge.instance.lock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = guard.Close() })
	second := startInstanceTestProcess(t, "acquire", root)
	if _, err := io.WriteString(second.input, "start\n"); err != nil {
		t.Fatal(err)
	}
	result, err := second.output.ReadString('\n')
	if err != nil || strings.TrimSpace(result) != "rejected" {
		t.Fatalf("OS lock did not independently reject second process: %q %v", result, err)
	}
}

func writeInstanceTestRecord(t *testing.T, root, record string) string {
	t.Helper()
	path := filepath.Join(root, "logs", "bridge.pid")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

type instanceTestProcess struct {
	pid    int
	input  io.WriteCloser
	output *bufio.Reader
	stop   func()
}

func startInstanceTestProcess(t *testing.T, mode, root string) instanceTestProcess {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestInstanceLockHelperProcess$", "--", mode, root)
	command.Env = append(os.Environ(), "FEISHU_INSTANCE_LOCK_TEST_HELPER=1")
	command.Stderr = os.Stderr
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = input.Close()
			if err := command.Wait(); err != nil {
				t.Errorf("instance test helper failed: %v", err)
			}
		})
	}
	t.Cleanup(stop)
	reader := bufio.NewReader(output)
	ready, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(ready))
	if err != nil {
		t.Fatalf("invalid helper PID: %q", ready)
	}
	return instanceTestProcess{pid: pid, input: input, output: reader, stop: stop}
}

func TestInstanceLockHelperProcess(t *testing.T) {
	if os.Getenv("FEISHU_INSTANCE_LOCK_TEST_HELPER") != "1" {
		return
	}
	mode, root := os.Args[len(os.Args)-2], os.Args[len(os.Args)-1]
	fmt.Println(os.Getpid())
	if mode == "exit" {
		return
	}
	reader := bufio.NewReader(os.Stdin)
	_, _ = reader.ReadString('\n')
	if mode == "legacy" {
		return
	}
	lock, err := AcquireInstanceLock(root)
	if err != nil {
		fmt.Println("rejected")
		return
	}
	fmt.Println("acquired")
	_, _ = reader.ReadString('\n')
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
}
