package integration

import (
	"context"
	"errors"
	"strings"
	"time"
)

var ErrRuntimeClosed = errors.New("integration runtime is closed")
var ErrInactiveTaskLink = errors.New("inactive_task_link")

func (runtime *Runtime) Store() TaskLinkStore { return runtime.links }

func (runtime *Runtime) CreateTaskLink(ctx context.Context, request CreateTaskLinkRequest) (PublicTaskLink, error) {
	done, admissionErr := runtime.admitOperation(ctx)
	if admissionErr != nil {
		return PublicTaskLink{}, admissionErr
	}
	defer done()
	unlock := runtime.lockActions("task:" + taskKey(request.ThreadID))
	defer unlock()
	if runtime.watchCtx.Err() != nil {
		return PublicTaskLink{}, ErrRuntimeClosed
	}
	target, err := runtime.messages.ResolveMessageTarget(request.TargetAlias)
	if err != nil {
		return PublicTaskLink{}, err
	}
	if target.Type != "open_id" || strings.TrimSpace(target.ID) == "" {
		return PublicTaskLink{}, errors.New("task links require a direct message target")
	}
	link, err := runtime.links.Upsert(request.ThreadID, request.Title, request.ProjectName, request.TargetAlias)
	if err != nil {
		return PublicTaskLink{}, err
	}
	link, err = runtime.links.UpdateActiveByID(link.ID, func(value *TaskLink) {
		if strings.TrimSpace(request.CWD) != "" {
			value.SetExtraString("workingDirectory", request.CWD)
		}
		if value.RootMessageID == "" {
			value.Target = target
		}
	})
	if err != nil {
		return PublicTaskLink{}, err
	}
	if link.RootMessageID == "" {
		card, err := TaskLinkCardJSON(link)
		if err != nil {
			return PublicTaskLink{}, err
		}
		key, err := TaskLinkCardIdempotencyKey(link)
		if err != nil {
			return PublicTaskLink{}, err
		}
		messageID, err := runtime.messages.Send(ctx, target, "card", card, key)
		if err != nil {
			return PublicTaskLink{}, err
		}
		if strings.TrimSpace(messageID) == "" {
			return PublicTaskLink{}, errors.New("task card delivery was not acknowledged")
		}
		link, err = runtime.links.UpdateActiveByID(link.ID, func(value *TaskLink) {
			value.RootMessageID = messageID
			value.MessageIDs = appendUnique(value.MessageIDs, messageID)
		})
		if err != nil {
			return PublicTaskLink{}, err
		}
	}
	if runtime.desktopOwned(link) {
		runtime.observeDesktopTask(link.TaskKey)
	}
	return projectTaskLink(link, time.Now()), nil
}

func (runtime *Runtime) Release(ctx context.Context, taskKey string) (PublicTaskLink, error) {
	done, admissionErr := runtime.admitOperation(ctx)
	if admissionErr != nil {
		return PublicTaskLink{}, admissionErr
	}
	defer done()
	unlock := runtime.lockActions("task:" + taskKey)
	defer unlock()
	if runtime.watchCtx.Err() != nil {
		return PublicTaskLink{}, ErrRuntimeClosed
	}
	link, found, err := runtime.links.FindAnyByTaskKey(taskKey)
	if err != nil {
		return PublicTaskLink{}, err
	}
	if !found {
		return PublicTaskLink{}, errors.New("task link not found")
	}
	if link.LinkState == "released" {
		return projectTaskLink(link, time.Now()), nil
	}
	if effectiveTaskLinkState(link, time.Now()) != "active" {
		return PublicTaskLink{}, ErrInactiveTaskLink
	}
	link, err = runtime.links.ReleaseByID(link.ID)
	if err != nil {
		return PublicTaskLink{}, err
	}
	err = SyncTaskLinkCard(ctx, runtime.links, runtime.messages, link, "")
	return projectTaskLink(link, time.Now()), err
}

func (runtime *Runtime) Interrupt(ctx context.Context, taskKey string) (PublicTaskLink, error) {
	done, admissionErr := runtime.admitOperation(ctx)
	if admissionErr != nil {
		return PublicTaskLink{}, admissionErr
	}
	defer done()
	unlock := runtime.lockActions("task:" + taskKey)
	defer unlock()
	if runtime.watchCtx.Err() != nil {
		return PublicTaskLink{}, ErrRuntimeClosed
	}
	link, found, err := runtime.links.FindAnyByTaskKey(taskKey)
	if err != nil {
		return PublicTaskLink{}, err
	}
	if !found {
		return PublicTaskLink{}, errors.New("task link not found")
	}
	if effectiveTaskLinkState(link, time.Now()) != "active" {
		return PublicTaskLink{}, ErrInactiveTaskLink
	}
	if permissionBlocked(link) {
		return PublicTaskLink{}, errors.New("task_link_permission_locked")
	}
	if link.ActiveTurnID != "" {
		if err := runtime.interruptTurn(ctx, link); err != nil {
			return PublicTaskLink{}, err
		}
	}
	link, err = runtime.links.UpdateActiveByID(link.ID, func(value *TaskLink) {
		value.TurnState, value.TurnOwner, value.ActionRequired = "interrupted", "none", "none"
		value.Phase, value.Detail = "已停止", "当前任务已标记为中断。"
		value.SetExtraValue("cardSyncPending", true)
	})
	if err != nil {
		return PublicTaskLink{}, err
	}
	err = SyncTaskLinkCard(ctx, runtime.links, runtime.messages, link, "")
	return projectTaskLink(link, time.Now()), err
}
