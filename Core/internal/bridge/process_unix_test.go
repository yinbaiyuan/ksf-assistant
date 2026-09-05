//go:build !windows

package bridge

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestRunKillsGrandchildrenWhenContextExpires(t *testing.T) {
	mode := os.Getenv("KSF_ASSISTANT_TRANSIENT_HELPER")
	if mode == "child" {
		for {
			time.Sleep(time.Hour)
		}
	}
	if mode == "parent" {
		command := exec.Command(os.Args[0], "-test.run=TestRunKillsGrandchildrenWhenContextExpires")
		command.Env = replaceEnvironmentValue(os.Environ(), "KSF_ASSISTANT_TRANSIENT_HELPER", "child")
		if err := command.Start(); err != nil {
			os.Exit(2)
		}
		if err := os.WriteFile(os.Getenv("KSF_ASSISTANT_TRANSIENT_PID_FILE"), []byte(strconv.Itoa(command.Process.Pid)), 0o600); err != nil {
			os.Exit(3)
		}
		for {
			time.Sleep(time.Hour)
		}
	}

	pidFile := t.TempDir() + "/child.pid"
	t.Setenv("KSF_ASSISTANT_TRANSIENT_HELPER", "parent")
	t.Setenv("KSF_ASSISTANT_TRANSIENT_PID_FILE", pidFile)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := run(ctx, ".", os.Args[0], []string{"-test.run=TestRunKillsGrandchildrenWhenContextExpires"}, nil)
	if !errors.Is(err, context.DeadlineExceeded) && (err == nil || err.Error() != context.DeadlineExceeded.Error()) {
		t.Fatalf("expected deadline error, got %v", err)
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for syscall.Kill(pid, 0) == nil && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if syscall.Kill(pid, 0) == nil {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Fatalf("grandchild process %d survived command cancellation", pid)
	}
}
