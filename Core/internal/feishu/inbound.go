package feishu

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

// FixedEventKeys is the frozen lark-cli 1.0.92 event surface. Approval and
// arbitrary event keys are intentionally absent.
var FixedEventKeys = []string{
	"application.bot.menu_v6",
	"board.whiteboard.updated_v1",
	"card.action.trigger",
	"im.chat.disbanded_v1",
	"im.chat.member.bot.added_v1",
	"im.chat.member.bot.deleted_v1",
	"im.chat.member.user.added_v1",
	"im.chat.member.user.deleted_v1",
	"im.chat.member.user.withdrawn_v1",
	"im.chat.updated_v1",
	"im.message.message_read_v1",
	"im.message.reaction.created_v1",
	"im.message.reaction.deleted_v1",
	"im.message.receive_v1",
	"minutes.minute.generated_v1",
	"task.task.update_user_access_v2",
	"vc.meeting.participant_meeting_ended_v1",
	"vc.meeting.participant_meeting_joined_v1",
	"vc.meeting.participant_meeting_started_v1",
	"vc.note.generated_v1",
	"vc.recording.recording_ended_v1",
	"vc.recording.recording_started_v1",
	"vc.recording.recording_transcript_generated_v1",
}

type EventSink func(context.Context, string, []byte) error
type ConnectionObserver func(string)

// OfficialInbound owns exactly one official SDK client. HandlePayload is also
// used by deterministic replay tests, so production comparison never requires
// a second real Feishu consumer.
type OfficialInbound struct {
	dispatcher *dispatcher.EventDispatcher
	client     *larkws.Client
	observer   ConnectionObserver
	mu         sync.Mutex
	started    bool
}

func NewOfficialInbound(appID, appSecret string, sink EventSink, observer ConnectionObserver) (*OfficialInbound, error) {
	if strings.TrimSpace(appID) == "" || appSecret == "" {
		return nil, errors.New("official SDK credentials are required")
	}
	if sink == nil {
		return nil, errors.New("event sink is required")
	}
	if observer == nil {
		observer = func(string) {}
	}
	d := dispatcher.NewEventDispatcher("", "")
	for _, key := range FixedEventKeys {
		eventKey := key
		d.OnCustomizedEvent(eventKey, func(ctx context.Context, event *larkevent.EventReq) error {
			body := slices.Clone(event.Body)
			return sink(ctx, eventKey, body)
		})
	}
	client := larkws.NewClient(
		strings.TrimSpace(appID), appSecret,
		larkws.WithEventHandler(d),
		larkws.WithLogLevel(larkcore.LogLevelError),
		larkws.WithLogger(discardSDKLogger{}),
		larkws.WithSource("codex-usage-bar"),
		larkws.WithAutoReconnect(true),
		larkws.WithOnReady(func() { observer("connected") }),
		larkws.WithOnReconnecting(func() { observer("reconnecting") }),
		larkws.WithOnReconnected(func() { observer("connected") }),
		larkws.WithOnError(func(error) { observer("failed") }),
		larkws.WithOnDisconnected(func() { observer("disconnected") }),
	)
	return &OfficialInbound{dispatcher: d, client: client, observer: observer}, nil
}

func (inbound *OfficialInbound) Start(ctx context.Context) error {
	inbound.mu.Lock()
	if inbound.started {
		inbound.mu.Unlock()
		return errors.New("official SDK consumer is already running")
	}
	inbound.started = true
	inbound.mu.Unlock()
	inbound.observer("starting")
	err := inbound.client.Start(ctx)
	inbound.mu.Lock()
	inbound.started = false
	inbound.mu.Unlock()
	return err
}

func (inbound *OfficialInbound) Close() { inbound.client.Close() }

func (inbound *OfficialInbound) HandlePayload(ctx context.Context, payload []byte) error {
	_, err := inbound.dispatcher.Do(ctx, slices.Clone(payload))
	return err
}

type discardSDKLogger struct{}

func (discardSDKLogger) Debug(context.Context, ...interface{}) {}
func (discardSDKLogger) Info(context.Context, ...interface{})  {}
func (discardSDKLogger) Warn(context.Context, ...interface{})  {}
func (discardSDKLogger) Error(context.Context, ...interface{}) {}
