package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"

	"ksfassistant/core/internal/privateipc"
)

type epochContextKey struct{}

var ErrStaleGeneration = privateipc.NewError(-32030, "stale Feishu connection generation")

func (supervisor *Supervisor) SetOnConnect(handler func(context.Context, uint64) error) error {
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	if supervisor.cmd != nil || supervisor.restartTimer != nil {
		return errors.New("cannot replace Feishu OnConnect while running")
	}
	supervisor.options.OnConnect = handler
	return nil
}

func (supervisor *Supervisor) connected(ctx context.Context, generation uint64, command *exec.Cmd, callback func(context.Context, uint64) error) {
	supervisor.mu.Lock()
	supervisor.mu.Unlock()
	err := callback(ctx, generation)
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	if supervisor.cmd != command || !supervisor.currentGenerationLocked(generation) || ctx.Err() != nil {
		return
	}
	if err != nil {
		supervisor.lastError = "KSFAssistant Feishu initialization failed"
		supervisor.state = StateDegraded
		supervisor.generationCancel()
		_ = supervisor.peer.Close()
		killProcessTree(supervisor.tree, command)
		return
	}
	supervisor.lastError = ""
	if supervisor.configured {
		supervisor.state = StateRunning
	} else {
		supervisor.state = StateIdleUnconfigured
	}
}

func WithEpoch(ctx context.Context, epoch uint64) context.Context {
	return context.WithValue(ctx, epochContextKey{}, epoch)
}

func EpochFromContext(ctx context.Context) uint64 {
	epoch, _ := ctx.Value(epochContextKey{}).(uint64)
	return epoch
}

func (supervisor *Supervisor) Generation() uint64 {
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	return supervisor.generation
}

func (supervisor *Supervisor) IsCurrentGeneration(generation uint64) bool {
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	return supervisor.currentGenerationLocked(generation)
}

func (supervisor *Supervisor) currentGenerationLocked(generation uint64) bool {
	if generation == 0 || supervisor.generation != generation || supervisor.peer == nil || supervisor.stopping || supervisor.generationCtx.Err() != nil {
		return false
	}
	select {
	case <-supervisor.peer.Done():
		return false
	default:
		return true
	}
}

func (supervisor *Supervisor) bindHandler(generation uint64, handler privateipc.Handler) privateipc.Handler {
	return privateipc.HandlerFunc(func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		if !supervisor.IsCurrentGeneration(generation) {
			return nil, ErrStaleGeneration
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if handler == nil {
			return nil, privateipc.ErrMethodNotFound
		}
		result, err := handler.HandlePrivateRPC(WithEpoch(ctx, generation), method, params)
		if !supervisor.IsCurrentGeneration(generation) {
			return nil, ErrStaleGeneration
		}
		return result, err
	})
}
