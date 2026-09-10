package integration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"ksfassistant/core/internal/corebridge"
)

func TestBridgeApprovalCancelsInterruptsAndLocksWithoutProjectingPayload(t *testing.T) {
	for _, test := range []struct {
		method, category string
	}{
		{"item/commandExecution/requestApproval", "command"},
		{"item/fileChange/requestApproval", "file_change"},
		{"item/permissions/requestApproval", "permissions"},
	} {
		t.Run(test.category, func(t *testing.T) {
			requestID := json.RawMessage(`"approval-1"`)
			cancelled := make(chan struct{}, 1)
			core := &fakeCorePort{
				read: func(context.Context, string, string, string) (map[string]any, error) {
					return map[string]any{"turns": []any{map[string]any{"id": "turn-1", "status": "running"}}}, nil
				},
				pending: func(string) (corebridge.PendingUserInput, bool) {
					return corebridge.PendingUserInput{ID: requestID, Method: test.method, ThreadID: "thread-1", TurnID: "turn-1"}, true
				},
				cancel: func(id json.RawMessage, method string) error {
					if string(id) != string(requestID) || method != test.method {
						return errors.New("approval identity changed")
					}
					cancelled <- struct{}{}
					return nil
				},
			}
			messages := &fakeFeishuPort{}
			runtime := testRuntime(t, core, messages)
			link := seedCard(t, runtime)
			runtime.observeTurn(link.TaskKey, link.ThreadID, link.ActiveTurnID, "card-1", "card-1", "")
			select {
			case <-cancelled:
			case <-time.After(time.Second):
				t.Fatal("approval was not cancelled")
			}
			var stored TaskLink
			for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
				stored, _, _ = runtime.Store().FindByID(link.ID)
				if permissionBlocked(stored) {
					break
				}
			}
			if stored.TurnState != "desktop_action_required" || stored.ActiveTurnID != "" || stored.Detail != permissionBlockedDetail {
				t.Fatalf("link was not safely locked: %#v", stored)
			}
			if stored.ExtraString("permissionBlockedTurnId") != "turn-1" || stored.ExtraString("permissionBlockedKind") != test.category || stored.ExtraString("permissionBlockedAt") == "" {
				t.Fatalf("minimal lock metadata missing: %#v", stored.Extra)
			}
			encoded, _ := json.Marshal(stored)
			if strings.Contains(string(encoded), "command") && test.category != "command" || strings.Contains(string(encoded), "must-not-leak") {
				t.Fatalf("approval payload leaked: %s", encoded)
			}
			card, err := TaskLinkCardJSON(stored)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(card, "task_link_release") || strings.Contains(card, "task_link_interrupt") || strings.Contains(card, "task_link_followup") || strings.Contains(card, "task_link_answer") || strings.Contains(card, "task_link_implement_plan") {
				t.Fatalf("locked card exposed controls: %s", card)
			}
			controls := projectTaskLink(stored, time.Now()).Controls.(map[string]bool)
			if !controls["canRelease"] || controls["canSend"] || controls["canSteer"] || controls["canInterrupt"] || controls["canAnswer"] || controls["acceptsAttachments"] {
				t.Fatalf("locked public controls were not fail-closed: %#v", controls)
			}
		})
	}
}

func TestPermissionLockedLinkRejectsRemoteInputButAllowsRelease(t *testing.T) {
	core, messages := &fakeCorePort{}, &fakeFeishuPort{}
	runtime := testRuntime(t, core, messages)
	link := seedCard(t, runtime)
	link, _ = runtime.Store().UpdateActiveByID(link.ID, func(value *TaskLink) {
		value.TurnState = "desktop_action_required"
		value.ActiveTurnID = ""
		value.Detail = permissionBlockedDetail
		value.SetExtraString("permissionBlockedTurnId", "turn-1")
		value.SetExtraString("permissionBlockedKind", "command")
	})
	if err := runtime.HandleMessage(context.Background(), InboundMessage{MessageID: "reply-locked", RootID: "card-1", ParentID: "card-1", SenderOpenID: "user-1", MessageType: "text", Text: "继续"}); err != nil {
		t.Fatal(err)
	}
	if core.calls.Load() != 0 || len(messages.replies) != 1 || messages.replies[0] != permissionBlockedDetail {
		t.Fatalf("locked input reached Codex or returned wrong notice: %d %#v", core.calls.Load(), messages.replies)
	}
	followup := InboundCardAction{TaskKey: link.TaskKey, LinkID: link.ID, MessageID: "card-1", OperatorOpenID: "user-1", Action: "task_link_followup", FormValue: map[string]any{"followup": "继续"}}
	if err := runtime.HandleCard(context.Background(), followup); err == nil || err.Error() != "task_link_permission_locked" {
		t.Fatalf("locked card action accepted: %v", err)
	}
	if _, err := runtime.Interrupt(context.Background(), link.TaskKey); err == nil || err.Error() != "task_link_permission_locked" {
		t.Fatalf("locked control API accepted interrupt: %v", err)
	}
	release := followup
	release.Action = "task_link_release"
	if err := runtime.HandleCard(context.Background(), release); err != nil {
		t.Fatal(err)
	}
	stored, _, _ := runtime.Store().FindByID(link.ID)
	if stored.LinkState != "released" || core.calls.Load() != 0 {
		t.Fatalf("disconnect did not remain independently usable: %#v", stored)
	}
}

func TestPermissionLockRecoversOnlyAfterDifferentDesktopTurn(t *testing.T) {
	currentTurn := "turn-blocked"
	snapshot := func() map[string]any {
		return map[string]any{
			"status": map[string]any{"type": "active"},
			"turns": []any{map[string]any{
				"id": currentTurn, "status": "running",
				"items": []any{map[string]any{
					"type":    "userMessage",
					"content": []any{map[string]any{"type": "text", "text": "本地继续"}},
				}},
			}},
		}
	}
	core := &fakeCorePort{
		desktop: func(context.Context, string) (map[string]any, string, string, bool, error) {
			return snapshot(), "desktop-owner", "revision-1", true, nil
		},
		read: func(context.Context, string, string, string) (map[string]any, error) { return snapshot(), nil },
	}
	runtime := testRuntime(t, core, &fakeFeishuPort{})
	link := seedCard(t, runtime)
	link, _ = runtime.Store().UpdateActiveByID(link.ID, func(value *TaskLink) {
		value.ActiveTurnID = ""
		value.TurnState = "desktop_action_required"
		value.SetExtraString("permissionBlockedTurnId", "turn-blocked")
		value.SetExtraString("permissionBlockedKind", "command")
		value.SetExtraString("permissionBlockedAt", time.Now().UTC().Format(time.RFC3339Nano))
	})
	recovered, err := runtime.tryRecoverPermissionBlockedTask(context.Background(), link)
	if err != nil || recovered {
		t.Fatalf("opening the same Desktop task unlocked it: %v %v", recovered, err)
	}
	unchanged, _, _ := runtime.Store().FindByID(link.ID)
	if unchanged.ExtraString("runtimeOwner") != "bridge" || !permissionBlocked(unchanged) {
		t.Fatal("ownership changed without a new turn")
	}
	currentTurn = "turn-local-new"
	recovered, err = runtime.tryRecoverPermissionBlockedTask(context.Background(), unchanged)
	if err != nil || !recovered {
		t.Fatalf("new Desktop turn did not recover link: %v %v", recovered, err)
	}
	stored, _, _ := runtime.Store().FindByID(link.ID)
	if stored.ExtraString("runtimeOwner") != "desktop" || permissionBlocked(stored) || stored.ActiveTurnID != "turn-local-new" || stored.TurnState != "running" {
		t.Fatalf("Desktop authority snapshot was not restored: %#v", stored)
	}
}
