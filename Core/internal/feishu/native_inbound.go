package feishu

import (
	"context"
	"errors"
	"sync"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

// NativeOfficialInbound owns the single official WebSocket connection used by
// KSFAssistant. Only task messages and card callbacks are registered.
type NativeOfficialInbound struct {
	client   *larkws.Client
	observer ConnectionObserver
	mu       sync.Mutex
	cancel   context.CancelFunc
	done     chan struct{}
}

func NewNativeOfficialInbound(credentials OfficialCredentials, sink EventSink, observer ConnectionObserver) (*NativeOfficialInbound, error) {
	if credentials.AppID == "" || credentials.AppSecret == "" || sink == nil {
		return nil, errors.New("official Feishu credentials and event sink are required")
	}
	if observer == nil {
		observer = func(string) {}
	}
	handler := dispatcher.NewEventDispatcher("", "")
	handler.OnCustomizedEvent("im.message.receive_v1", func(ctx context.Context, event *larkevent.EventReq) error {
		return sink(ctx, "im.message.receive_v1", append([]byte(nil), event.Body...))
	})
	handler.OnP2CardActionTrigger(func(ctx context.Context, event *callback.CardActionTriggerEvent) (*callback.CardActionTriggerResponse, error) {
		if event == nil || event.EventReq == nil {
			return nil, errors.New("invalid card callback")
		}
		return nil, sink(ctx, "card.action.trigger", append([]byte(nil), event.Body...))
	})
	options := []larkws.ClientOption{
		larkws.WithEventHandler(handler),
		larkws.WithLogLevel(larkcore.LogLevelError),
		larkws.WithOnReady(func() { observer("connected") }),
		larkws.WithOnReconnecting(func() { observer("reconnecting") }),
		larkws.WithOnReconnected(func() { observer("connected") }),
		larkws.WithOnDisconnected(func() { observer("disconnected") }),
		larkws.WithOnError(func(error) { observer("reconnecting") }),
	}
	if brandOrDefault(credentials.Brand) == "lark" {
		options = append(options, larkws.WithDomain(lark.LarkBaseUrl))
	} else {
		options = append(options, larkws.WithDomain(lark.FeishuBaseUrl))
	}
	return &NativeOfficialInbound{
		client: larkws.NewClient(credentials.AppID, credentials.AppSecret, options...), observer: observer,
	}, nil
}

func (inbound *NativeOfficialInbound) Start(ctx context.Context) error {
	inbound.mu.Lock()
	if inbound.cancel != nil {
		inbound.mu.Unlock()
		return errors.New("official Feishu event consumer is already running")
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	inbound.cancel, inbound.done = cancel, done
	inbound.mu.Unlock()
	inbound.observer("starting")
	err := inbound.client.Start(runCtx)
	inbound.mu.Lock()
	inbound.cancel = nil
	close(done)
	inbound.mu.Unlock()
	if runCtx.Err() != nil {
		inbound.observer("disconnected")
		return runCtx.Err()
	}
	inbound.observer("failed")
	return err
}

func (inbound *NativeOfficialInbound) Close() {
	inbound.mu.Lock()
	cancel, done := inbound.cancel, inbound.done
	inbound.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}
