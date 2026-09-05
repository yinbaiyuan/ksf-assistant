package feishu

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"codexusagebar/core/internal/privateipc"
)

const (
	StateStarting         = "starting"
	StateIdleUnconfigured = "idle_unconfigured"
	StateRunning          = "running"
	StateDegraded         = "degraded"
	StateStopping         = "stopping"
	StateStopped          = "stopped"
)

type SupervisorOptions struct {
	Executable  string
	Arguments   []string
	Environment []string
	Directory   string
	DataRoot    string
	Handler     privateipc.Handler
}

type SupervisorStatus struct {
	State                 string `json:"state"`
	Configured            bool   `json:"configured"`
	PID                   int    `json:"pid"`
	RestartCount          int    `json:"restartCount"`
	LastError             string `json:"lastError,omitempty"`
	LastDiagnosticAt      string `json:"lastDiagnosticAt,omitempty"`
	LastDiagnosticCode    string `json:"lastDiagnosticCode,omitempty"`
	LastDiagnosticSummary string `json:"lastDiagnosticSummary,omitempty"`
}

type Supervisor struct {
	mu             sync.Mutex
	options        SupervisorOptions
	cmd            *exec.Cmd
	stdin          io.WriteCloser
	stdout         io.ReadCloser
	peer           *privateipc.Peer
	tree           processTree
	state          string
	configured     bool
	stopping       bool
	restartCount   int
	lastError      string
	lastDiagnostic SupervisorDiagnostic
	startedAt      time.Time
	restartTimer   *time.Timer
	restartDelays  []time.Duration
	restartWindow  time.Duration
	healthyReset   time.Duration
	firstFailureAt time.Time
}

func NewSupervisor(options SupervisorOptions) *Supervisor {
	return &Supervisor{
		options:       options,
		state:         StateStopped,
		restartDelays: []time.Duration{time.Second, 2 * time.Second, 5 * time.Second},
		restartWindow: 5 * time.Minute,
		healthyReset:  5 * time.Minute,
	}
}

func (supervisor *Supervisor) SetHandler(handler privateipc.Handler) error {
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	if supervisor.cmd != nil {
		return errors.New("cannot replace CodexAssistant Feishu private IPC handler while running")
	}
	supervisor.options.Handler = handler
	return nil
}

func (supervisor *Supervisor) SetConfigured(configured bool) {
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	supervisor.configured = configured
	if supervisor.cmd != nil {
		if configured {
			supervisor.state = StateRunning
		} else {
			supervisor.state = StateIdleUnconfigured
		}
	}
}

func (supervisor *Supervisor) Start() error {
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	if supervisor.cmd != nil {
		return nil
	}
	if supervisor.restartTimer != nil {
		supervisor.restartTimer.Stop()
		supervisor.restartTimer = nil
	}
	supervisor.stopping = false
	return supervisor.startLocked()
}

func (supervisor *Supervisor) startLocked() error {
	executable, err := validateExecutable(supervisor.options.Executable)
	if err != nil {
		supervisor.state = StateDegraded
		supervisor.lastError = err.Error()
		return err
	}
	command := exec.Command(executable, supervisor.options.Arguments...)
	command.Dir = supervisor.options.Directory
	command.Env = append(os.Environ(), supervisor.options.Environment...)
	stdin, err := command.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return err
	}
	prepareProcessTree(command)
	supervisor.state = StateStarting
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		supervisor.state = StateDegraded
		supervisor.lastError = fmt.Sprintf("unable to start CodexAssistant Feishu: %v", err)
		return errors.New(supervisor.lastError)
	}
	tree, err := attachProcessTree(command)
	if err != nil {
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		supervisor.state = StateDegraded
		supervisor.lastError = fmt.Sprintf("unable to own CodexAssistant Feishu process tree: %v", err)
		return errors.New(supervisor.lastError)
	}
	supervisor.cmd = command
	supervisor.stdin = stdin
	supervisor.stdout = stdout
	supervisor.peer = privateipc.NewPeer(stdout, stdin, supervisor.options.Handler)
	supervisor.tree = tree
	supervisor.startedAt = time.Now()
	if supervisor.configured {
		supervisor.state = StateRunning
	} else {
		supervisor.state = StateIdleUnconfigured
	}
	peer := supervisor.peer
	go supervisor.drainStderr(command, stderr)
	go supervisor.servePeer(command, peer)
	go supervisor.wait(command, tree)
	return nil
}

func (supervisor *Supervisor) drainStderr(command *exec.Cmd, stderr io.ReadCloser) {
	defer stderr.Close()
	reader := bufio.NewReaderSize(stderr, 64*1024)
	for {
		line, prefix, err := reader.ReadLine()
		if len(line) > 0 {
			diagnostic := ParseSupervisorDiagnostic(line)
			if prefix {
				diagnostic.Code = "child_stderr_chunk"
				diagnostic.SafeSummary = fmt.Sprintf("child_stderr_chunk · length=%d · %s", len(line), diagnostic.Fingerprint)
			}
			_ = NewDiagnosticLog(supervisor.options.DataRoot).Record(diagnostic)
			supervisor.mu.Lock()
			if supervisor.cmd == command {
				supervisor.lastDiagnostic = diagnostic
			}
			supervisor.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (supervisor *Supervisor) servePeer(command *exec.Cmd, peer *privateipc.Peer) {
	if err := peer.Serve(context.Background()); err != nil && !errors.Is(err, privateipc.ErrPeerClosed) {
		supervisor.mu.Lock()
		if supervisor.cmd == command && !supervisor.stopping {
			supervisor.lastError = "CodexAssistant Feishu private IPC stopped: " + err.Error()
			supervisor.state = StateDegraded
		}
		supervisor.mu.Unlock()
	}
}

func (supervisor *Supervisor) wait(command *exec.Cmd, tree processTree) {
	err := command.Wait()
	closeProcessTree(tree)
	now := time.Now()

	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	if supervisor.cmd != command {
		return
	}
	supervisor.cmd = nil
	supervisor.stdin = nil
	supervisor.stdout = nil
	supervisor.peer = nil
	supervisor.tree = processTree{}
	if supervisor.stopping {
		supervisor.state = StateStopped
		return
	}
	if now.Sub(supervisor.startedAt) >= supervisor.healthyReset {
		supervisor.restartCount = 0
		supervisor.firstFailureAt = time.Time{}
	}
	if supervisor.firstFailureAt.IsZero() || now.Sub(supervisor.firstFailureAt) > supervisor.restartWindow {
		supervisor.restartCount = 0
		supervisor.firstFailureAt = now
	}
	if err == nil {
		supervisor.lastError = "CodexAssistant Feishu exited unexpectedly"
	} else {
		supervisor.lastError = err.Error()
	}
	if supervisor.restartCount >= len(supervisor.restartDelays) {
		supervisor.state = StateDegraded
		return
	}
	delay := supervisor.restartDelays[supervisor.restartCount]
	supervisor.state = StateStarting
	supervisor.restartTimer = time.AfterFunc(delay, supervisor.restart)
}

// Call invokes one whitelisted operation on the managed child over its
// anonymous stdin/stdout pipes. No standalone client process is launched.
func (supervisor *Supervisor) Call(ctx context.Context, method string, params any, target any) error {
	supervisor.mu.Lock()
	peer := supervisor.peer
	supervisor.mu.Unlock()
	if peer == nil {
		return errors.New("CodexAssistant Feishu private IPC is unavailable")
	}
	return peer.Call(ctx, method, params, target)
}

func (supervisor *Supervisor) restart() {
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	supervisor.restartTimer = nil
	if supervisor.stopping || supervisor.cmd != nil {
		return
	}
	supervisor.restartCount++
	if err := supervisor.startLocked(); err != nil && supervisor.restartCount >= len(supervisor.restartDelays) {
		supervisor.state = StateDegraded
	}
}

func (supervisor *Supervisor) Restart(ctx context.Context) error {
	if err := supervisor.Stop(ctx); err != nil {
		return err
	}
	supervisor.mu.Lock()
	supervisor.restartCount = 0
	supervisor.firstFailureAt = time.Time{}
	supervisor.lastError = ""
	supervisor.mu.Unlock()
	return supervisor.Start()
}

func (supervisor *Supervisor) Stop(ctx context.Context) error {
	supervisor.mu.Lock()
	supervisor.stopping = true
	if supervisor.restartTimer != nil {
		supervisor.restartTimer.Stop()
		supervisor.restartTimer = nil
	}
	command := supervisor.cmd
	stdin := supervisor.stdin
	tree := supervisor.tree
	if command == nil {
		supervisor.state = StateStopped
		supervisor.mu.Unlock()
		return nil
	}
	supervisor.state = StateStopping
	supervisor.mu.Unlock()

	if stdin != nil {
		_ = stdin.Close()
	}
	interruptProcessTree(tree, command)

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		supervisor.mu.Lock()
		stopped := supervisor.cmd == nil
		supervisor.mu.Unlock()
		if stopped {
			return nil
		}
		select {
		case <-ctx.Done():
			killProcessTree(tree, command)
			return ctx.Err()
		case <-deadline.C:
			killProcessTree(tree, command)
			return nil
		case <-ticker.C:
		}
	}
}

func (supervisor *Supervisor) Status() SupervisorStatus {
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	status := SupervisorStatus{
		State:        supervisor.state,
		Configured:   supervisor.configured,
		RestartCount: supervisor.restartCount,
		LastError:    supervisor.lastError,
	}
	if !supervisor.lastDiagnostic.At.IsZero() {
		status.LastDiagnosticAt = supervisor.lastDiagnostic.At.UTC().Format(time.RFC3339Nano)
		status.LastDiagnosticCode = supervisor.lastDiagnostic.Code
		status.LastDiagnosticSummary = supervisor.lastDiagnostic.SafeSummary
	}
	if supervisor.cmd != nil && supervisor.cmd.Process != nil {
		status.PID = supervisor.cmd.Process.Pid
	}
	return status
}

func validateExecutable(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("CodexAssistant Feishu executable is not configured")
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", errors.New("CodexAssistant Feishu executable path is invalid")
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("CodexAssistant Feishu executable is unavailable")
	}
	return absolute, nil
}
