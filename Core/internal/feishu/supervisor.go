package feishu

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ksfassistant/core/internal/privateipc"
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
	OnConnect   func(context.Context, uint64) error
}

type SupervisorStatus struct {
	State                 string `json:"state"`
	Generation            uint64 `json:"generation"`
	Configured            bool   `json:"configured"`
	PID                   int    `json:"pid"`
	RestartCount          int    `json:"restartCount"`
	LastError             string `json:"lastError,omitempty"`
	LastDiagnosticAt      string `json:"lastDiagnosticAt,omitempty"`
	LastDiagnosticCode    string `json:"lastDiagnosticCode,omitempty"`
	LastDiagnosticSummary string `json:"lastDiagnosticSummary,omitempty"`
}

type Supervisor struct {
	mu                        sync.Mutex
	options                   SupervisorOptions
	cmd                       *exec.Cmd
	stdin                     io.WriteCloser
	stdout                    io.ReadCloser
	peer                      *privateipc.Peer
	generation                uint64
	generationCtx             context.Context
	generationCancel          context.CancelFunc
	done                      chan struct{}
	restartToken              uint64
	tree                      processTree
	state                     string
	configured                bool
	stopping                  bool
	restartCount              int
	lastError                 string
	lastDiagnostic            SupervisorDiagnostic
	startedAt                 time.Time
	restartTimer              *time.Timer
	restartDelays             []time.Duration
	restartWindow             time.Duration
	healthyReset              time.Duration
	firstFailureAt            time.Time
	instanceConflictStartedAt time.Time
	instanceConflictDelay     time.Duration
	instanceConflictWindow    time.Duration
}

func NewSupervisor(options SupervisorOptions) *Supervisor {
	return &Supervisor{
		options:                options,
		state:                  StateStopped,
		restartDelays:          []time.Duration{time.Second, 2 * time.Second, 5 * time.Second},
		restartWindow:          5 * time.Minute,
		healthyReset:           5 * time.Minute,
		instanceConflictDelay:  2 * time.Second,
		instanceConflictWindow: 5 * time.Minute,
	}
}

func (supervisor *Supervisor) SetHandler(handler privateipc.Handler) error {
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	if supervisor.cmd != nil || supervisor.restartTimer != nil {
		return errors.New("cannot replace KSFAssistant Feishu private IPC handler while running")
	}
	supervisor.options.Handler = handler
	return nil
}

func (supervisor *Supervisor) SetConfigured(configured bool) {
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	supervisor.configured = configured
	if supervisor.cmd != nil && !supervisor.stopping && supervisor.state != StateDegraded && supervisor.state != StateStarting {
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
		if supervisor.stopping {
			return errors.New("KSFAssistant Feishu is stopping")
		}
		return nil
	}
	supervisor.restartToken++
	if supervisor.restartTimer != nil {
		supervisor.restartTimer.Stop()
		supervisor.restartTimer = nil
	}
	supervisor.stopping = false
	return supervisor.startLocked()
}

func (supervisor *Supervisor) startLocked() error {
	if present, alive, _, _, err := InstanceStatus(supervisor.options.DataRoot); err == nil && present && alive {
		now := time.Now()
		if supervisor.instanceConflictStartedAt.IsZero() {
			supervisor.instanceConflictStartedAt = now
		}
		if now.Sub(supervisor.instanceConflictStartedAt) < supervisor.instanceConflictWindow {
			supervisor.state = StateStarting
			supervisor.lastError = "waiting for previous KSFAssistant Feishu instance to exit"
			supervisor.scheduleInstanceConflictRetryLocked()
			return nil
		}
		supervisor.state = StateDegraded
		supervisor.lastError = "previous KSFAssistant Feishu instance did not exit"
		return errors.New(supervisor.lastError)
	}
	supervisor.instanceConflictStartedAt = time.Time{}
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
		supervisor.lastError = fmt.Sprintf("unable to start KSFAssistant Feishu: %v", err)
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
		supervisor.lastError = fmt.Sprintf("unable to own KSFAssistant Feishu process tree: %v", err)
		return errors.New(supervisor.lastError)
	}
	supervisor.cmd = command
	supervisor.stdin = stdin
	supervisor.stdout = stdout
	supervisor.generation++
	generation := supervisor.generation
	ctx, cancel := context.WithCancel(WithEpoch(context.Background(), generation))
	supervisor.generationCtx = ctx
	supervisor.generationCancel = cancel
	supervisor.done = make(chan struct{})
	supervisor.peer = privateipc.NewPeer(stdout, stdin, supervisor.bindHandler(generation, supervisor.options.Handler))
	supervisor.tree = tree
	supervisor.startedAt = time.Now()
	if supervisor.configured {
		supervisor.state = StateRunning
	} else {
		supervisor.state = StateIdleUnconfigured
	}
	peer := supervisor.peer
	go supervisor.drainStderr(command, stderr)
	go supervisor.servePeer(ctx, command, peer)
	go supervisor.wait(command, tree)
	if callback := supervisor.options.OnConnect; callback != nil {
		supervisor.state = StateStarting
		go supervisor.connected(ctx, generation, command, callback)
	}
	return nil
}

func (supervisor *Supervisor) scheduleInstanceConflictRetryLocked() {
	supervisor.restartToken++
	token := supervisor.restartToken
	delay := supervisor.instanceConflictDelay
	if delay <= 0 {
		delay = 2 * time.Second
	}
	supervisor.restartTimer = time.AfterFunc(delay, func() {
		supervisor.mu.Lock()
		defer supervisor.mu.Unlock()
		if token != supervisor.restartToken || supervisor.stopping || supervisor.cmd != nil {
			return
		}
		supervisor.restartTimer = nil
		_ = supervisor.startLocked()
	})
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

func (supervisor *Supervisor) servePeer(ctx context.Context, command *exec.Cmd, peer *privateipc.Peer) {
	err := peer.Serve(ctx)
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	if supervisor.cmd == command && !supervisor.stopping {
		supervisor.generationCancel()
		supervisor.lastError = "KSFAssistant Feishu private IPC closed"
		if err != nil {
			supervisor.lastError += ": " + err.Error()
		}
		supervisor.state = StateDegraded
		killProcessTree(supervisor.tree, command)
	}
}

func (supervisor *Supervisor) wait(command *exec.Cmd, tree processTree) {
	err := command.Wait()
	now := time.Now()

	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	closeProcessTree(tree)
	if supervisor.cmd != command {
		return
	}
	supervisor.generationCancel()
	_ = supervisor.peer.Close()
	close(supervisor.done)
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
		supervisor.lastError = "KSFAssistant Feishu exited unexpectedly"
	} else {
		supervisor.lastError = err.Error()
	}
	if supervisor.restartCount >= len(supervisor.restartDelays) {
		supervisor.state = StateDegraded
		return
	}
	supervisor.scheduleRestartLocked()
}

// Call invokes one whitelisted operation on the managed child over its
// anonymous stdin/stdout pipes. No standalone client process is launched.
func (supervisor *Supervisor) Call(ctx context.Context, method string, params any, target any) error {
	supervisor.mu.Lock()
	peer := supervisor.peer
	generation := supervisor.generation
	valid := supervisor.currentGenerationLocked(generation)
	supervisor.mu.Unlock()
	if epoch := EpochFromContext(ctx); epoch != 0 && (epoch != generation || !valid) {
		return ErrStaleGeneration
	}
	if !valid {
		return errors.New("KSFAssistant Feishu private IPC is unavailable")
	}
	var result json.RawMessage
	if err := peer.Call(ctx, method, params, &result); err != nil {
		return err
	}
	if !supervisor.IsCurrentGeneration(generation) {
		return ErrStaleGeneration
	}
	if target != nil && len(result) != 0 {
		return privateipc.DecodeStrict(result, target, true)
	}
	return nil
}

func (supervisor *Supervisor) scheduleRestartLocked() {
	if supervisor.restartCount >= len(supervisor.restartDelays) {
		supervisor.state = StateDegraded
		return
	}
	supervisor.restartToken++
	token := supervisor.restartToken
	supervisor.state = StateStarting
	supervisor.restartTimer = time.AfterFunc(supervisor.restartDelays[supervisor.restartCount], func() { supervisor.restart(token) })
}

func (supervisor *Supervisor) restart(token uint64) {
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	if token != supervisor.restartToken || supervisor.stopping || supervisor.cmd != nil {
		return
	}
	supervisor.restartTimer = nil
	supervisor.restartCount++
	if err := supervisor.startLocked(); err != nil {
		supervisor.scheduleRestartLocked()
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
	supervisor.restartToken++
	if supervisor.restartTimer != nil {
		supervisor.restartTimer.Stop()
		supervisor.restartTimer = nil
	}
	command := supervisor.cmd
	tree := supervisor.tree
	if command == nil {
		supervisor.state = StateStopped
		supervisor.mu.Unlock()
		return nil
	}
	supervisor.state = StateStopping
	supervisor.generationCancel()
	_ = supervisor.peer.Close()
	done := supervisor.done
	interruptProcessTree(tree, command)
	supervisor.mu.Unlock()

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	kill := func() {
		supervisor.mu.Lock()
		defer supervisor.mu.Unlock()
		if supervisor.cmd == command {
			killProcessTree(tree, command)
		}
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		kill()
		return ctx.Err()
	case <-deadline.C:
		kill()
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (supervisor *Supervisor) Status() SupervisorStatus {
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	status := SupervisorStatus{
		State:        supervisor.state,
		Generation:   supervisor.generation,
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
		return "", errors.New("KSFAssistant Feishu executable is not configured")
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", errors.New("KSFAssistant Feishu executable path is invalid")
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("KSFAssistant Feishu executable is unavailable")
	}
	return absolute, nil
}
