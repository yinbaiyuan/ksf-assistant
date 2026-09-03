package bridge

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"
)

type FeishuSupervisor struct {
	root string
	node string
	mu   sync.Mutex
	cmd  *exec.Cmd
}

func NewFeishuSupervisor(root, node string) *FeishuSupervisor {
	return &FeishuSupervisor{root: root, node: node}
}

func (supervisor *FeishuSupervisor) Start() error {
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	if supervisor.cmd != nil && supervisor.cmd.ProcessState == nil {
		return nil
	}
	_, node, err := validateFeishuWithNode(supervisor.root, supervisor.node)
	if err != nil {
		return err
	}
	entry := filepath.Join(supervisor.root, "scripts", "start-bridge.js")
	if info, statErr := os.Lstat(entry); statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("飞书桥启动入口无效")
	}
	cmd := exec.Command(node, entry)
	cmd.Dir = supervisor.root
	cmd.Env = append(os.Environ(), "FEISHU_BRIDGE_PROJECT_ROOT="+supervisor.root)
	cmd.Stdin = nil
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("无法启动飞书桥: %w", err)
	}
	supervisor.cmd = cmd
	go func(current *exec.Cmd) {
		_ = current.Wait()
		supervisor.mu.Lock()
		if supervisor.cmd == current {
			supervisor.cmd = nil
		}
		supervisor.mu.Unlock()
	}(cmd)
	return nil
}

func (supervisor *FeishuSupervisor) Restart() error {
	if err := supervisor.Stop(context.Background()); err != nil {
		return err
	}
	return supervisor.Start()
}

func (supervisor *FeishuSupervisor) Stop(ctx context.Context) error {
	supervisor.mu.Lock()
	cmd := supervisor.cmd
	supervisor.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if runtime.GOOS == "windows" {
		killer := exec.CommandContext(ctx, "taskkill.exe", "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F")
		killer.Stdout, killer.Stderr = io.Discard, io.Discard
		_ = killer.Run()
	} else {
		_ = cmd.Process.Signal(os.Interrupt)
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		supervisor.mu.Lock()
		stopped := supervisor.cmd == nil
		supervisor.mu.Unlock()
		if stopped {
			return nil
		}
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			return ctx.Err()
		case <-deadline.C:
			_ = cmd.Process.Kill()
			return nil
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (supervisor *FeishuSupervisor) Status() map[string]any {
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	running := supervisor.cmd != nil && supervisor.cmd.ProcessState == nil
	result := map[string]any{"running": running, "managedBy": "codex-usage-core-v2"}
	if running {
		result["pid"] = supervisor.cmd.Process.Pid
	}
	return result
}
