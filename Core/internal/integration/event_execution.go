package integration

import (
	"context"
	"encoding/json"
)

type eventExecutionKey struct{}

func beginEventEffect(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if begin, ok := ctx.Value(eventExecutionKey{}).(func() error); ok {
		return begin()
	}
	return nil
}

type eventCorePort struct{ CorePort }

func (port eventCorePort) StartThread(ctx context.Context, cwd, title string) (StartedThread, error) {
	if err := beginEventEffect(ctx); err != nil {
		return StartedThread{}, err
	}
	return port.CorePort.StartThread(ctx, cwd, title)
}

func (port eventCorePort) StartTurn(ctx context.Context, key, owner, thread, cwd, text string, mode map[string]any) (string, error) {
	if err := beginEventEffect(ctx); err != nil {
		return "", err
	}
	return port.CorePort.StartTurn(ctx, key, owner, thread, cwd, text, mode)
}

func (port eventCorePort) SteerTurn(ctx context.Context, key, owner, thread, turn, cwd, text string) (string, error) {
	if err := beginEventEffect(ctx); err != nil {
		return "", err
	}
	return port.CorePort.SteerTurn(ctx, key, owner, thread, turn, cwd, text)
}

func (port eventCorePort) InterruptTurn(ctx context.Context, key, owner, thread, turn string) error {
	if err := beginEventEffect(ctx); err != nil {
		return err
	}
	return port.CorePort.InterruptTurn(ctx, key, owner, thread, turn)
}

func (port eventCorePort) CancelApproval(ctx context.Context, key, owner, thread, turn string, request json.RawMessage, method string) error {
	if err := beginEventEffect(ctx); err != nil {
		return err
	}
	return port.CorePort.CancelApproval(ctx, key, owner, thread, turn, request, method)
}

func (port eventCorePort) AnswerInput(ctx context.Context, key, owner, thread, turn string, request json.RawMessage, question, revision, answer string) (json.RawMessage, error) {
	if err := beginEventEffect(ctx); err != nil {
		return nil, err
	}
	return port.CorePort.AnswerInput(ctx, key, owner, thread, turn, request, question, revision, answer)
}

type eventFeishuPort struct{ FeishuPort }

func (port eventFeishuPort) Send(ctx context.Context, target MessageTarget, format, content, key string) (string, error) {
	if err := beginEventEffect(ctx); err != nil {
		return "", err
	}
	return port.FeishuPort.Send(ctx, target, format, content, key)
}

func (port eventFeishuPort) Reply(ctx context.Context, messageID, format, content, key string) (string, error) {
	if err := beginEventEffect(ctx); err != nil {
		return "", err
	}
	return port.FeishuPort.Reply(ctx, messageID, format, content, key)
}

func (port eventFeishuPort) PatchCard(ctx context.Context, messageID, content string) error {
	if err := beginEventEffect(ctx); err != nil {
		return err
	}
	return port.FeishuPort.PatchCard(ctx, messageID, content)
}

func (port eventFeishuPort) StageInbound(ctx context.Context, message InboundMessage, limit int64) (StagedInboundMessage, error) {
	if err := beginEventEffect(ctx); err != nil {
		return StagedInboundMessage{}, err
	}
	return port.FeishuPort.StageInbound(ctx, message, limit)
}

func (port eventFeishuPort) Record(event string, fields map[string]any) error {
	if audit, ok := port.FeishuPort.(AuditPort); ok {
		return audit.Record(event, fields)
	}
	return nil
}

func (p eventCorePort) ObserveThread(ctx context.Context, owner, thread, turn string) (map[string]any, string, error) {
	if observer, ok := p.CorePort.(ObservationPort); ok {
		return observer.ObserveThread(ctx, owner, thread, turn)
	}
	s, e := p.ReadThread(ctx, owner, thread, turn)
	return s, "", e
}
