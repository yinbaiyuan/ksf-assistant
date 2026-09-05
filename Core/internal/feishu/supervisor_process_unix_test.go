//go:build !windows

package feishu

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestSupervisorOrphanHelperProcess(t *testing.T) {
	mode := os.Getenv("KSF_SUPERVISOR_ORPHAN_MODE")
	if mode == "" {
		return
	}
	if mode == "leaf" {
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	if mode == "stubborn" {
		signal.Ignore(os.Interrupt)
		if err := os.WriteFile(os.Getenv("KSF_SUPERVISOR_PID_FIXTURE"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
			os.Exit(12)
		}
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	executable, _ := os.Executable()
	child := exec.Command(executable, "-test.run=^TestSupervisorOrphanHelperProcess$")
	child.Env = append(os.Environ(), "KSF_SUPERVISOR_ORPHAN_MODE=leaf")
	if err := child.Start(); err != nil {
		os.Exit(11)
	}
	if err := os.WriteFile(os.Getenv("KSF_SUPERVISOR_PID_FIXTURE"), []byte(strconv.Itoa(child.Process.Pid)), 0600); err != nil {
		os.Exit(12)
	}
	os.Exit(7)
}

func TestSupervisorReapsOrphanProcessGroup(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	pidPath := filepath.Join(root, "child-pid")
	supervisor := NewSupervisor(SupervisorOptions{
		Executable: executable, DataRoot: root,
		Arguments:   []string{"-test.run=^TestSupervisorOrphanHelperProcess$"},
		Environment: []string{"KSF_SUPERVISOR_ORPHAN_MODE=parent", "KSF_SUPERVISOR_PID_FIXTURE=" + pidPath},
	})
	supervisor.restartDelays = nil
	if err := supervisor.Start(); err != nil {
		t.Fatal(err)
	}
	defer supervisor.Stop(context.Background())
	waitSupervisor(t, supervisor, func(status SupervisorStatus) bool { return status.State == StateDegraded && status.PID == 0 })
	data, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Kill(pid, syscall.SIGKILL)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("orphan descendant %d survived parent exit", pid)
}

func TestSupervisorStopDeadlineForcesReap(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	readyPath := filepath.Join(root, "ready")
	supervisor := NewSupervisor(SupervisorOptions{
		Executable: executable, DataRoot: root,
		Arguments:   []string{"-test.run=^TestSupervisorOrphanHelperProcess$"},
		Environment: []string{"KSF_SUPERVISOR_ORPHAN_MODE=stubborn", "KSF_SUPERVISOR_PID_FIXTURE=" + readyPath},
	})
	if err := supervisor.Start(); err != nil {
		t.Fatal(err)
	}
	defer supervisor.Stop(context.Background())
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(readyPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stubborn child did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if err := supervisor.Stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected caller deadline, got %v", err)
	}
	waitSupervisor(t, supervisor, func(status SupervisorStatus) bool { return status.State == StateStopped && status.PID == 0 })
	if supervisor.IsCurrentGeneration(supervisor.Generation()) {
		t.Fatal("force-stopped generation remains active")
	}
}
