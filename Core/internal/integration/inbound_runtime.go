package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"ksfassistant/core/internal/corebridge"
)

var desktopIntegerRequestID = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)$`)

type Runtime struct {
	healthMu     sync.Mutex
	healthIssues map[string]bool
	dataRoot     string
	core         CorePort
	messages     FeishuPort
	links        TaskLinkStore
	inbox        *eventInbox
	actionMu     sync.Mutex
	actionLocks  map[string]*actionLock
	watchWG      sync.WaitGroup
	closed       bool
	watchMu      sync.Mutex
	watchers     map[string]context.CancelFunc
	watchCtx     context.Context
	stopWatch    context.CancelFunc
}

func NewRuntime(dataRoot string, messages FeishuPort, core CorePort) (*Runtime, error) {
	if core == nil || messages == nil {
		return nil, errors.New("integration ports are unavailable")
	}
	if strings.TrimSpace(dataRoot) == "" {
		return nil, errors.New("integration data root is required")
	}
	watchCtx, stopWatch := context.WithCancel(context.Background())
	inbox, err := newEventInbox(dataRoot)
	if err != nil {
		stopWatch()
		return nil, err
	}
	return &Runtime{dataRoot: dataRoot, core: eventCorePort{core}, messages: eventFeishuPort{messages}, links: NewTaskLinkStore(dataRoot), inbox: inbox, watchers: map[string]context.CancelFunc{}, watchCtx: watchCtx, stopWatch: stopWatch}, nil
}

func (runtime *Runtime) Close() {
	runtime.watchMu.Lock()
	runtime.closed = true
	runtime.stopWatch()
	runtime.watchMu.Unlock()
	runtime.watchWG.Wait()
}
func (runtime *Runtime) ResumeActive() error {
	if runtime.watchCtx.Err() != nil {
		return ErrRuntimeClosed
	}
	runtime.launchWatcher("maintenance", runtime.runMaintenance)
	recoveryErr := runtime.resumeActiveLinks(runtime.watchCtx)
	return errors.Join(recoveryErr, runtime.ResumeEvents())
}

func (runtime *Runtime) resumeActiveLinks(ctx context.Context) error {

	if runtime.watchCtx.Err() != nil {
		return ErrRuntimeClosed
	}
	file, err := runtime.links.Load()
	runtime.setHealth("task observation recovery failed", err)
	if err != nil {
		return err
	}
	var recoveryErrors error
	for _, link := range file.Links {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if effectiveTaskLinkState(link, time.Now()) == "active" && link.ExtraString("cardActionSchemaVersion") != "3" {
			needsCardRefresh := link.ExtraString("inputSubmissionError") != "" || link.TurnState == "waiting_input"
			updated, updateErr := runtime.links.UpdateActiveByID(link.ID, func(value *TaskLink) {
				value.SetExtraString("cardActionSchemaVersion", "3")
				value.SetExtraString("inputSubmissionError", "")
				if needsCardRefresh {
					value.SetExtraValue("cardSyncPending", true)
				}
			})
			recoveryErrors = errors.Join(recoveryErrors, updateErr)
			if updateErr == nil && needsCardRefresh {
				link = updated
				recoveryErrors = errors.Join(recoveryErrors, SyncTaskLinkCard(ctx, runtime.links, runtime.messages, link, ""))
			}
		}
		if effectiveTaskLinkState(link, time.Now()) != "active" || link.ActiveTurnID == "" || (link.TurnState != "running" && link.TurnState != "queued" && link.TurnState != "waiting_input") {
			if effectiveTaskLinkState(link, time.Now()) == "active" && runtime.desktopOwned(link) {
				runtime.observeDesktopTask(link.TaskKey)
			}
			continue
		}
		if runtime.desktopOwned(link) {
			runtime.observeDesktopTask(link.TaskKey)
			continue
		}
		cardMessageID := link.RootMessageID
		runtime.observeTurn(link.TaskKey, link.ThreadID, link.ActiveTurnID, cardMessageID, cardMessageID, link.ExtraString("pendingCleanupDir"))
	}
	runtime.setHealth("task observation recovery failed", recoveryErrors)
	return recoveryErrors
}
func (runtime *Runtime) HandleMessage(ctx context.Context, message InboundMessage) error {
	unlock := runtime.lockActions(runtime.messageActionKeys(message)...)
	defer unlock()
	if runtime.watchCtx.Err() != nil {
		return ErrRuntimeClosed
	}
	prompt := strings.TrimSpace(message.Text)
	var link TaskLink
	var err error
	found := false
	foundByOwnMessage := false
	for _, candidate := range []string{message.MessageID, message.RootID, message.ParentID} {
		if candidate == "" {
			continue
		}
		link, found, err = runtime.links.FindAnyByMessage(candidate)
		if err != nil {
			return err
		}
		if found {
			if effectiveTaskLinkState(link, time.Now()) != "active" {
				return ErrInactiveTaskLink
			}
			foundByOwnMessage = candidate == message.MessageID
			break
		}
	}
	cleanupDir := ""
	if message.MessageType != "text" {
		if message.ChatType != "p2p" && message.ChatType != "direct" && message.ChatType != "" {
			_, err := runtime.messages.Reply(ctx, message.MessageID, "text", "群聊中的附件暂不交给 Codex 处理；请在授权单聊中发送。", replyIdempotencyKey(message.MessageID, "unsupported-group-attachment", 0))
			return err
		}
		staged, err := runtime.messages.StageInbound(ctx, message, 25*1024*1024)
		if err != nil {
			return err
		}
		cleanupDir = staged.CleanupDir
		prompt = InboundAssetsPrompt(staged.Text, staged)
	}
	if strings.TrimSpace(prompt) == "" {
		_, err := runtime.messages.Reply(ctx, message.MessageID, "text", "消息中没有可读取的文本或附件资源。", replyIdempotencyKey(message.MessageID, "empty", 0))
		return err
	}
	if foundByOwnMessage && link.ActiveTurnID != "" {
		if link.RootMessageID == message.MessageID {
			cardJSON, cardErr := TaskLinkCardJSON(link)
			if cardErr != nil {
				return cardErr
			}
			cardMessageID, cardErr := runtime.messages.Send(ctx, link.Target, "card", cardJSON, replyIdempotencyKey(message.MessageID, "processing-card", 0))
			if cardErr != nil {
				return cardErr
			}
			link, _ = runtime.links.UpdateActiveByID(link.ID, func(value *TaskLink) {
				value.RootMessageID = cardMessageID
				value.MessageIDs = appendUnique(value.MessageIDs, cardMessageID)
			})
			runtime.observeTurn(link.TaskKey, link.ThreadID, link.ActiveTurnID, message.MessageID, cardMessageID, link.ExtraString("pendingCleanupDir"))
		}
		return nil
	}
	if foundByOwnMessage && (link.TurnState == "completed" || link.TurnState == "failed" || link.TurnState == "interrupted") {
		return nil
	}
	if !found {
		workspace, err := runtime.workspace()
		if err != nil {
			return err
		}
		title := firstMessageTitle(prompt)
		threadID, err := runtime.core.StartThread(ctx, workspace, title)
		if err != nil {
			return err
		}
		alias := runtime.aliasForOpenID(message.SenderOpenID)
		link, err = runtime.links.Upsert(threadID, title, "", alias)
		if err != nil {
			return err
		}
		link, err = runtime.links.UpdateActiveByID(link.ID, func(value *TaskLink) {
			value.Target = MessageTarget{Type: "open_id", ID: message.SenderOpenID}
			value.RootMessageID = message.MessageID
			value.MessageIDs = append(value.MessageIDs, message.MessageID)
			value.SetExtraString("runtimeOwner", "bridge")
			value.SetExtraString("workingDirectory", workspace)
		})
		if err != nil {
			return err
		}
	}
	workspace := link.ExtraString("workingDirectory")
	if err := beginEventEffect(ctx); err != nil {
		return err
	}
	turnID, err := runtime.startTurn(ctx, link, workspace, prompt, nil)
	if err != nil {
		_ = runtime.messages.CleanupInbound(ctx, cleanupDir)
		return err
	}
	link, err = runtime.links.UpdateActiveByID(link.ID, func(value *TaskLink) {
		value.TurnState = "running"
		value.TurnOwner = "bridge"
		value.ActionRequired = "none"
		value.ActiveTurnID = turnID
		value.Phase = "运行中"
		value.Detail = "Codex 已开始处理。"
		value.SetExtraString("latestInput", prompt)
		value.SetExtraString("activeTurnMode", "default")
		value.SetExtraString("pendingCleanupDir", cleanupDir)
		value.MessageIDs = appendUnique(value.MessageIDs, message.MessageID)
	})
	if err != nil {
		return err
	}
	cardJSON, _ := TaskLinkCardJSON(link)
	cardMessageID, cardErr := runtime.messages.Send(ctx, link.Target, "card", cardJSON, replyIdempotencyKey(message.MessageID, "processing-card", 0))
	if cardErr == nil {
		link, _ = runtime.links.UpdateActiveByID(link.ID, func(value *TaskLink) {
			if value.RootMessageID == message.MessageID {
				value.RootMessageID = cardMessageID
			}
			value.MessageIDs = appendUnique(value.MessageIDs, cardMessageID)
		})
	} else {
		fallbackID, fallbackErr := runtime.messages.Send(ctx, link.Target, "text", "Codex 已收到消息，正在处理。", replyIdempotencyKey(message.MessageID, "processing-text-fallback", 0))
		_ = runtime.recordAudit("inbound_feedback_fallback", map[string]any{
			"phase": "processing", "replyError": cardErr.Error(), "fallbackError": errorText(fallbackErr), "fallbackSent": fallbackID != "",
		})
	}
	runtime.observeTurn(link.TaskKey, link.ThreadID, turnID, message.MessageID, cardMessageID, cleanupDir)
	return nil
}
func (runtime *Runtime) HandleCard(ctx context.Context, action InboundCardAction) error {
	var handled bool
	var decodeErr error
	action, handled, decodeErr = decodeTaskCard(action)
	if decodeErr != nil {
		return decodeErr
	}
	if !handled {
		return nil
	}
	unlock := runtime.lockActions("task:"+action.TaskKey, conversationKey(action.ChatID, action.OperatorOpenID))
	defer unlock()
	if runtime.watchCtx.Err() != nil {
		return ErrRuntimeClosed
	}
	if action.TaskKey == "" || action.LinkID == "" {
		return errors.New("invalid_card_link")
	}
	link, found, err := runtime.links.FindByID(action.LinkID)
	if err != nil {
		return err
	}
	if !found || link.TaskKey != action.TaskKey {
		return errors.New("task link not found")
	}
	if effectiveTaskLinkState(link, time.Now()) != "active" {
		return ErrInactiveTaskLink
	}
	target := link.Target
	if target.ID == "" && link.TargetAlias != "" {
		target, _ = runtime.messages.ResolveMessageTarget(link.TargetAlias)
	}
	if target.Type != "open_id" || target.ID != action.OperatorOpenID {
		return errors.New("card operator does not match task link")
	}
	if action.MessageID == "" || (action.MessageID != link.RootMessageID && !containsString(link.MessageIDs, action.MessageID)) {
		return errors.New("card message does not match task link")
	}
	if err := validateTaskCard(action); err != nil {
		return err
	}
	if err := beginEventEffect(ctx); err != nil {
		return err
	}
	switch action.Action {
	case "task_link_interrupt":
		if link.ActiveTurnID != "" {
			if err := runtime.interruptTurn(ctx, link); err != nil {
				return err
			}
		}
		link, err := runtime.links.UpdateActiveByID(link.ID, func(value *TaskLink) {
			value.TurnState = "interrupted"
			value.TurnOwner = "none"
			value.ActionRequired = "none"
			value.Phase = "已停止"
		})
		if err != nil {
			return err
		}
		return runtime.patchTaskLinkCard(ctx, action.MessageID, link)
	case "task_link_release":
		link, err := runtime.links.ReleaseByID(link.ID)
		if err != nil {
			return err
		}
		return SyncTaskLinkCard(ctx, runtime.links, runtime.messages, link, action.MessageID)
	case "task_link_followup", "task_link_answer":
		followup := strings.TrimSpace(fmt.Sprint(action.FormValue["followup"]))
		if action.Action == "task_link_answer" {
			questionID, answer := fmt.Sprint(action.Value["questionId"]), fmt.Sprint(action.Value["answer"])
			requestID := link.ExtraRaw("pendingQuestionRequestRef")
			if len(requestID) == 0 {
				requestID, _ = json.Marshal(link.ExtraString("pendingQuestionRequestID"))
			}
			if serverRequestID(requestID) == "" || action.QuestionRevision != link.ExtraString("pendingQuestionRevision") {
				return errors.New("invalid_card_question_revision")
			}
			if !taskLinkHasPendingQuestion(link, questionID) {
				return errors.New("invalid_card_question_state")
			}
			if link.ExtraString("pendingInputSubmissionRevision") == action.QuestionRevision {
				if submittedAt, parseErr := time.Parse(time.RFC3339Nano, link.ExtraString("pendingInputSubmissionAt")); parseErr == nil && time.Since(submittedAt) < 30*time.Second {
					return nil
				}
			}
			link, err = runtime.links.UpdateActiveByID(link.ID, func(value *TaskLink) {
				value.SetExtraString("pendingInputSubmissionRevision", action.QuestionRevision)
				value.SetExtraString("pendingInputSubmissionAt", time.Now().UTC().Format(time.RFC3339Nano))
			})
			if err != nil {
				return err
			}
			requestID, err = runtime.answerInput(ctx, link, requestID, questionID, action.QuestionRevision, answer)
			if err != nil {
				_, _ = runtime.links.UpdateActiveByID(link.ID, func(value *TaskLink) {
					value.SetExtraString("pendingInputSubmissionRevision", "")
					value.SetExtraString("pendingInputSubmissionAt", "")
					if strings.Contains(err.Error(), "desktop_user_input_outcome_unknown") {
						value.Detail = "提交未生效，可重试。"
						value.SetExtraString("pendingInputLastErrorRevision", action.QuestionRevision)
						value.SetExtraValue("cardSyncPending", true)
					}
				})
				if strings.Contains(err.Error(), "desktop_user_input_outcome_unknown") {
					updated, _, _ := runtime.links.FindByTaskKey(link.TaskKey)
					_ = runtime.patchTaskLinkCard(context.Background(), action.MessageID, updated)
					return nil
				}
				return err
			}
			link, err = runtime.links.UpdateActiveByID(link.ID, func(value *TaskLink) {
				value.TurnState = "running"
				value.ActionRequired = "none"
				value.Phase = "执行"
				value.Detail = "已收到你的选择。"
				value.SetExtraValue("pendingQuestions", nil)
				value.SetExtraString("pendingQuestionRequestID", "")
				value.SetExtraRaw("pendingQuestionRequestRef", nil)
				value.SetExtraString("pendingQuestionRevision", "")
				value.SetExtraString("pendingInputAnswerRequestID", serverRequestID(requestID))
				value.SetExtraRaw("pendingInputAnswerRequestRef", requestID)
				value.SetExtraString("pendingInputAnswerTurnID", value.ActiveTurnID)
				value.SetExtraString("pendingInputAnsweredAt", time.Now().UTC().Format(time.RFC3339Nano))
				value.SetExtraString("pendingInputSubmissionRevision", "")
				value.SetExtraString("pendingInputSubmissionAt", "")
				value.SetExtraString("pendingInputLastErrorRevision", "")
				value.SetExtraValue("cardSyncPending", true)
			})
			if err != nil {
				return err
			}
			return runtime.patchTaskLinkCard(ctx, action.MessageID, link)
		}
		if followup == "" {
			return errors.New("empty task link followup")
		}
		workspace := link.ExtraString("workingDirectory")
		turnID := link.ActiveTurnID
		selectedMode := link.ExtraString("activeTurnMode")
		if selectedMode != "plan" {
			selectedMode = "default"
		}
		if (link.TurnState == "running" || link.TurnState == "waiting_input") && turnID != "" {
			turnID, err = runtime.steerTurn(ctx, link, workspace, followup)
		} else {
			mode := strings.TrimSpace(fmt.Sprint(action.FormValue["turnMode"]))
			var collaboration map[string]any
			if mode == "plan" {
				selectedMode = "plan"
				snapshot, readErr := runtime.readThread(ctx, link)
				if readErr != nil {
					return readErr
				}
				collaboration, err = planCollaborationMode(snapshot)
				if err != nil {
					return err
				}
			}
			turnID, err = runtime.startTurn(ctx, link, workspace, followup, collaboration)
		}
		if err != nil {
			return err
		}
		link, err = runtime.links.UpdateActiveByID(link.ID, func(value *TaskLink) {
			value.TurnState = "running"
			value.TurnOwner = "bridge"
			value.ActionRequired = "none"
			value.ActiveTurnID = turnID
			value.PendingPlanRevision = ""
			value.PendingPlanTurnID = ""
			value.Phase = "运行中"
			value.Detail = "Codex 已收到补充内容。"
			value.SetExtraString("latestInput", followup)
			value.SetExtraString("activeTurnMode", selectedMode)
			value.NextTurnMode = "default"
		})
		if err != nil {
			return err
		}
		_ = runtime.patchTaskLinkCard(ctx, action.MessageID, link)
		runtime.observeTurn(link.TaskKey, link.ThreadID, turnID, action.MessageID, action.MessageID, "")
		return nil
	case "task_link_implement_plan":
		snapshot, err := runtime.readThread(ctx, link)
		if err != nil {
			return err
		}
		pending := pendingPlanImplementation(snapshot)
		revision := planRevision(pending.TurnID, pending.Content)
		if link.TurnState != "plan_ready" || pending.Content == "" || revision != fmt.Sprint(action.Value["planRevision"]) || revision != link.PendingPlanRevision {
			return errors.New("plan card is stale")
		}
		workspace := link.ExtraString("workingDirectory")
		turnID, err := runtime.startTurn(ctx, link, workspace, "PLEASE IMPLEMENT THIS PLAN:\n"+pending.Content, nil)
		if err != nil {
			return err
		}
		link, err = runtime.links.UpdateActiveByID(link.ID, func(value *TaskLink) {
			value.TurnState = "running"
			value.TurnOwner = "bridge"
			value.ActionRequired = "none"
			value.ActiveTurnID = turnID
			value.PendingPlanRevision = ""
			value.PendingPlanTurnID = ""
			value.Phase = "执行"
			value.Detail = "计划已开始执行。"
			value.SetExtraString("activeTurnMode", "default")
			value.NextTurnMode = "default"
		})
		if err != nil {
			return err
		}
		_ = runtime.patchTaskLinkCard(ctx, action.MessageID, link)
		runtime.observeTurn(link.TaskKey, link.ThreadID, turnID, action.MessageID, action.MessageID, "")
		return nil
	default:
		return errors.New("unsupported_card_action")
	}
}

func (runtime *Runtime) ApplyInputOutcome(ctx context.Context, outcome corebridge.InputOutcome) error {
	unlock := runtime.lockActions("task:" + outcome.TaskKey)
	defer unlock()
	if runtime.watchCtx.Err() != nil {
		return ErrRuntimeClosed
	}
	link, found, err := runtime.links.FindByTaskKey(outcome.TaskKey)
	if err != nil || !found {
		return err
	}
	if link.ThreadID != outcome.ThreadID || link.ActiveTurnID != outcome.TurnID || link.ExtraString("pendingQuestionRevision") != outcome.QuestionRevision {
		return nil
	}
	updated, err := runtime.links.UpdateActiveByID(link.ID, func(value *TaskLink) {
		value.SetExtraString("pendingInputSubmissionRevision", "")
		value.SetExtraString("pendingInputSubmissionAt", "")
		if outcome.State == "succeeded" {
			value.TurnState = "running"
			value.ActionRequired = "none"
			value.Phase = "执行"
			value.Detail = "已收到你的选择。"
			value.SetExtraValue("pendingQuestions", nil)
			value.SetExtraString("pendingQuestionRequestID", "")
			value.SetExtraRaw("pendingQuestionRequestRef", nil)
			value.SetExtraString("pendingQuestionRevision", "")
			value.SetExtraString("pendingInputLastErrorRevision", "")
		} else {
			value.Detail = "提交未生效，可重试。"
			value.SetExtraString("pendingInputLastErrorRevision", outcome.QuestionRevision)
		}
		value.SetExtraValue("cardSyncPending", true)
	})
	if err != nil {
		return err
	}
	return SyncTaskLinkCard(ctx, runtime.links, runtime.messages, updated, "")
}
func (runtime *Runtime) workspace() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return runtime.core.Workspace(ctx)
}
func (runtime *Runtime) aliasForOpenID(openID string) string {
	return runtime.messages.AliasForOpenID(openID)
}

func (runtime *Runtime) waitForTurn(parent context.Context, taskKey, threadID, turnID, replyTo, cardMessageID, cleanupDir string) {
	ctx, cancel := context.WithTimeout(parent, 30*time.Minute)
	defer cancel()
	defer func() {
		if parent.Err() == nil {
			_ = runtime.messages.CleanupInbound(ctx, cleanupDir)
		}
	}()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		link, found, _ := runtime.links.FindByTaskKey(taskKey)
		if !found || effectiveTaskLinkState(link, time.Now()) != "active" || link.ThreadID != threadID || link.ActiveTurnID != turnID || link.TurnState == "interrupted" || ctx.Err() != nil {
			return
		}
		snapshot, err := runtime.readThread(ctx, link)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			if request, waiting := runtime.pendingInput(link, snapshot); waiting {
				requestID := serverRequestID(request.ID)
				requestTurnID := strings.TrimSpace(request.TurnID)
				if requestTurnID == "" {
					requestTurnID = turnID
				}
				owner := runtime.core.ProjectionOwner(link.ThreadID, link.ExtraString("runtimeOwner"))
				revision := PendingQuestionRevisionScoped(link.TaskKey, requestTurnID, owner, request.ID, request.Questions)
				needsUpdate := link.TurnState != "waiting_input" || link.ActionRequired != "feishu" || link.ActiveTurnID != turnID || string(link.ExtraRaw("pendingQuestionRequestRef")) != string(request.ID) || link.ExtraString("pendingQuestionRevision") != revision
				if needsUpdate {
					link, _ = runtime.links.UpdateActiveByID(link.ID, func(value *TaskLink) {
						value.TurnState = "waiting_input"
						value.ActionRequired = "feishu"
						value.ActiveTurnID = turnID
						value.Phase = "等待输入"
						value.Detail = "Codex 等待你的选择。"
						value.SetExtraValue("pendingQuestions", request.Questions)
						value.SetExtraString("pendingQuestionRequestID", requestID)
						value.SetExtraRaw("pendingQuestionRequestRef", request.ID)
						value.SetExtraString("pendingQuestionRevision", revision)
					})
					if cardMessageID != "" {
						_ = runtime.patchTaskLinkCard(ctx, cardMessageID, link)
					}
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
				continue
			}
			pending := pendingPlanImplementation(snapshot)
			if pending.Content != "" && pending.TurnID == turnID {
				revision := planRevision(pending.TurnID, pending.Content)
				link, _ := runtime.links.UpdateActiveByID(link.ID, func(value *TaskLink) {
					value.TurnState = "plan_ready"
					value.TurnOwner = "none"
					value.ActionRequired = "feishu"
					value.ActiveTurnID = ""
					value.PendingPlanTurnID = pending.TurnID
					value.PendingPlanRevision = revision
					value.Phase = "计划已生成"
					value.Detail = pending.Content
					value.SetExtraString("pendingCleanupDir", "")
					value.SetExtraValue("pendingQuestions", nil)
					value.SetExtraString("pendingQuestionRequestID", "")
					value.SetExtraRaw("pendingQuestionRequestRef", nil)
					value.SetExtraString("pendingQuestionRevision", "")
				})
				if cardMessageID != "" {
					_ = runtime.patchTaskLinkCard(ctx, cardMessageID, link)
				}
				return
			}
			state, final := bridgeTurnProjection(snapshot, turnID)
			if state == "running" && link.TurnState == "waiting_input" {
				link, _ = runtime.links.UpdateActiveByID(link.ID, func(value *TaskLink) {
					value.TurnState = "running"
					value.ActionRequired = "none"
					value.Phase = "执行"
					value.Detail = "Codex 正在处理。"
					value.SetExtraValue("pendingQuestions", nil)
					value.SetExtraString("pendingQuestionRequestID", "")
					value.SetExtraRaw("pendingQuestionRequestRef", nil)
					value.SetExtraString("pendingQuestionRevision", "")
				})
				if cardMessageID != "" {
					_ = runtime.patchTaskLinkCard(ctx, cardMessageID, link)
				}
			}
			if state != "running" {
				_, _ = runtime.links.UpdateActiveByID(link.ID, func(value *TaskLink) {
					value.TurnState = state
					value.TurnOwner = "none"
					value.ActionRequired = "none"
					value.ActiveTurnID = ""
					value.Phase = map[string]string{"completed": "已完成", "failed": "失败", "interrupted": "已停止"}[state]
					value.Detail = final
					value.SetExtraString("pendingCleanupDir", "")
					value.SetExtraValue("pendingQuestions", nil)
					value.SetExtraString("pendingQuestionRequestID", "")
					value.SetExtraRaw("pendingQuestionRequestRef", nil)
					value.SetExtraString("pendingQuestionRevision", "")
				})
				if final == "" {
					final = map[string]string{"completed": "任务已完成。", "failed": "任务执行失败，请在 Codex Desktop 查看。", "interrupted": "任务已停止。"}[state]
				}
				link, _, _ := runtime.links.FindByTaskKey(taskKey)
				patched := false
				if cardMessageID != "" {
					patched = runtime.patchTaskLinkCard(ctx, cardMessageID, link) == nil
				}
				if !patched {
					finalCard, cardErr := TaskLinkCardJSON(link)
					fallbackID := ""
					if cardErr == nil {
						fallbackID, cardErr = runtime.messages.Send(ctx, link.Target, "card", finalCard, replyIdempotencyKey(replyTo, "final-card-fallback", 0))
					}
					if cardErr != nil {
						textID, textErr := runtime.messages.Send(ctx, link.Target, "text", final, replyIdempotencyKey(replyTo, "final-text-fallback", 0))
						_ = runtime.recordAudit("inbound_feedback_fallback", map[string]any{
							"phase": "final", "cardError": cardErr.Error(), "fallbackError": errorText(textErr), "fallbackSent": textID != "",
						})
					} else if fallbackID != "" {
						_ = runtime.recordAudit("inbound_card_recreated", map[string]any{"phase": "final", "sent": true})
					}
				}
				return
			}
		}
		select {
		case <-ctx.Done():
			if runtime.watchCtx.Err() != nil {
				return
			}
			_, _ = runtime.links.UpdateActiveByID(link.ID, func(value *TaskLink) {
				value.TurnState = "failed"
				value.TurnOwner = "none"
				value.ActiveTurnID = ""
				value.Phase = "超时"
				value.Detail = "Codex 任务等待超时。"
				value.SetExtraString("pendingCleanupDir", "")
			})
			return
		case <-ticker.C:
		}
	}
}

func replyIdempotencyKey(messageID, phase string, part int) string {
	digest := sha256.Sum256([]byte(messageID + "\x00" + phase + "\x00" + strconv.Itoa(part)))
	return "codex-" + hex.EncodeToString(digest[:])[:32]
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (runtime *Runtime) desktopOwned(link TaskLink) bool {
	return link.ExtraString("runtimeOwner") != "bridge"
}
func (runtime *Runtime) readThread(ctx context.Context, link TaskLink) (map[string]any, error) {
	owner := link.ExtraString("runtimeOwner")
	if owner == "" {
		owner = "desktop"
	}
	value, err := runtime.core.ReadThread(ctx, owner, link.ThreadID, link.ActiveTurnID)
	if err != nil {
		return nil, err
	}
	if owner != "bridge" {
		return normalizeDesktopState(value), nil
	}
	return value, nil
}

func normalizeDesktopState(state map[string]any) map[string]any {
	result := map[string]any{}
	for key, value := range state {
		result[key] = value
	}
	turns := []any{}
	seen := map[string]bool{}
	add := func(raw any) {
		turn, ok := raw.(map[string]any)
		if !ok {
			return
		}
		id := strings.TrimSpace(fmt.Sprint(turn["id"]))
		if id == "" || id == "<nil>" {
			id = strings.TrimSpace(fmt.Sprint(turn["turnId"]))
		}
		if id == "" || id == "<nil>" || seen[id] {
			return
		}
		copy := map[string]any{}
		for key, value := range turn {
			copy[key] = value
		}
		copy["id"] = id
		if copy["status"] == nil {
			copy["status"] = map[string]any{"type": "unknown"}
		}
		if copy["items"] == nil {
			copy["items"] = []any{}
		}
		seen[id] = true
		turns = append(turns, copy)
	}
	if direct, ok := state["turns"].([]any); ok {
		for _, turn := range direct {
			add(turn)
		}
	}
	if history, ok := state["turnHistory"].(map[string]any); ok {
		if inner, ok := history["history"].(map[string]any); ok {
			if entities, ok := inner["entitiesByKey"].(map[string]any); ok {
				for _, turn := range entities {
					add(turn)
				}
			}
		}
	}
	sort.SliceStable(turns, func(i, j int) bool { return desktopTurnTimestamp(turns[i]) < desktopTurnTimestamp(turns[j]) })
	result["turns"] = turns
	return result
}

func desktopTurnTimestamp(raw any) int64 {
	turn, _ := raw.(map[string]any)
	if turn == nil {
		return 0
	}
	for _, key := range []string{"turnStartedAtMs", "startedAtMs"} {
		switch value := turn[key].(type) {
		case float64:
			if value > 0 {
				return int64(value)
			}
		case string:
			if number, err := strconv.ParseInt(value, 10, 64); err == nil && number > 0 {
				return number
			}
		}
	}
	for _, key := range []string{"startedAt", "createdAt"} {
		if value, ok := turn[key].(string); ok {
			if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
				return parsed.UnixMilli()
			}
		}
	}
	return 0
}
func (runtime *Runtime) startTurn(ctx context.Context, link TaskLink, cwd, text string, mode map[string]any) (string, error) {
	owner := link.ExtraString("runtimeOwner")
	if owner == "" {
		owner = "desktop"
	}
	return runtime.core.StartTurn(ctx, link.TaskKey, owner, link.ThreadID, cwd, text, mode)
}
func (runtime *Runtime) steerTurn(ctx context.Context, link TaskLink, cwd, text string) (string, error) {
	owner := link.ExtraString("runtimeOwner")
	if owner == "" {
		owner = "desktop"
	}
	return runtime.core.SteerTurn(ctx, link.TaskKey, owner, link.ThreadID, link.ActiveTurnID, cwd, text)
}
func (runtime *Runtime) interruptTurn(ctx context.Context, link TaskLink) error {
	owner := link.ExtraString("runtimeOwner")
	if owner == "" {
		owner = "desktop"
	}
	return runtime.core.InterruptTurn(ctx, link.TaskKey, owner, link.ThreadID, link.ActiveTurnID)
}
func (runtime *Runtime) pendingInput(link TaskLink, snapshot map[string]any) (corebridge.PendingUserInput, bool) {
	if !runtime.desktopOwned(link) {
		return runtime.core.PendingInput(link.ThreadID)
	}
	requestID, pendingTurnID, questions := desktopPendingInputRaw(snapshot, link.ActiveTurnID)
	if len(requestID) == 0 {
		return corebridge.PendingUserInput{}, false
	}
	return corebridge.PendingUserInput{ID: requestID, Method: "item/tool/requestUserInput", ThreadID: link.ThreadID, TurnID: pendingTurnID, Questions: questions}, true
}
func (runtime *Runtime) answerInput(ctx context.Context, link TaskLink, expectedRequestID json.RawMessage, questionID, questionRevision, answer string) (json.RawMessage, error) {
	owner := link.ExtraString("runtimeOwner")
	if owner == "" {
		owner = "desktop"
	}
	return runtime.core.AnswerInput(ctx, link.TaskKey, owner, link.ThreadID, link.ActiveTurnID, expectedRequestID, questionID, questionRevision, answer)
}

func serverRequestID(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return ""
	}
	switch item := value.(type) {
	case string:
		return strings.TrimSpace(item)
	case json.Number:
		if desktopIntegerRequestID.MatchString(item.String()) {
			return item.String()
		}
		return ""
	default:
		return ""
	}
}

func taskLinkHasPendingQuestion(link TaskLink, questionID string) bool {
	var questions []map[string]any
	if !link.ExtraValue("pendingQuestions", &questions) {
		return false
	}
	for _, question := range questions {
		if strings.TrimSpace(fmt.Sprint(question["id"])) == questionID {
			return true
		}
	}
	return false
}

func desktopRequestPending(value any, requestID string) bool {
	candidates := []desktopInputCandidate{}
	collectDesktopPendingInputs(value, &candidates)
	for _, candidate := range candidates {
		if candidate.id == requestID {
			return true
		}
	}
	return false
}

func desktopPendingInput(value any, expectedTurnID string) (string, string, []map[string]any) {
	return matchingDesktopPendingInput(value, expectedTurnID, nil)
}

func desktopPendingInputRaw(value any, expectedTurnID string) (json.RawMessage, string, []map[string]any) {
	candidates := []desktopInputCandidate{}
	collectDesktopPendingInputs(value, &candidates)
	matches := []desktopInputCandidate{}
	for _, candidate := range candidates {
		if expectedTurnID != "" && candidate.turnID != "" && candidate.turnID != expectedTurnID {
			continue
		}
		matches = append(matches, candidate)
	}
	if len(matches) != 1 || len(matches[0].rawID) == 0 {
		return nil, "", nil
	}
	return append(json.RawMessage(nil), matches[0].rawID...), matches[0].turnID, matches[0].questions
}

type desktopInputCandidate struct {
	id, turnID string
	rawID      json.RawMessage
	questions  []map[string]any
}

func matchingDesktopPendingInput(value any, expectedTurnID string, expectedQuestionIDs []string) (string, string, []map[string]any) {
	return matchingDesktopPendingInputByID(value, expectedTurnID, expectedQuestionIDs, "")
}

func matchingDesktopPendingInputByID(value any, expectedTurnID string, expectedQuestionIDs []string, expectedRequestID string) (string, string, []map[string]any) {
	candidates := []desktopInputCandidate{}
	collectDesktopPendingInputs(value, &candidates)
	matches := []desktopInputCandidate{}
	for _, candidate := range candidates {
		if expectedRequestID != "" && candidate.id != expectedRequestID {
			continue
		}
		// Desktop's requestUserInput payloads are not uniform: some versions
		// omit turnId. The question set remains the binding identity, while a
		// present turn id must still match the active linked turn.
		if expectedTurnID != "" && candidate.turnID != "" && candidate.turnID != expectedTurnID {
			continue
		}
		if len(expectedQuestionIDs) > 0 && !equalStringSets(questionIDs(candidate.questions), expectedQuestionIDs) {
			continue
		}
		matches = append(matches, candidate)
	}
	if len(matches) != 1 || matches[0].id == "" {
		return "", "", nil
	}
	return matches[0].id, matches[0].turnID, matches[0].questions
}

func collectDesktopPendingInputs(value any, candidates *[]desktopInputCandidate) {
	switch item := value.(type) {
	case map[string]any:
		method := fmt.Sprint(item["method"])
		params, _ := item["params"].(map[string]any)
		turnID := strings.TrimSpace(fmt.Sprint(params["turnId"]))
		if turnID == "<nil>" {
			turnID = ""
		}
		if strings.Contains(method, "requestUserInput") {
			questions := []map[string]any{}
			if values, ok := params["questions"].([]any); ok {
				for _, raw := range values {
					if question, ok := raw.(map[string]any); ok {
						if secret, _ := question["isSecret"].(bool); !secret {
							questions = append(questions, question)
						}
					}
				}
			}
			rawID := desktopRequestIDRaw(item["id"])
			id := serverRequestID(rawID)
			if id != "" {
				*candidates = append(*candidates, desktopInputCandidate{id: id, rawID: rawID, turnID: turnID, questions: questions})
			}
		}
		for _, child := range item {
			collectDesktopPendingInputs(child, candidates)
		}
	case []any:
		for _, child := range item {
			collectDesktopPendingInputs(child, candidates)
		}
	}
}

func desktopRequestIDRaw(value any) json.RawMessage {
	switch item := value.(type) {
	case string:
		raw, _ := json.Marshal(item)
		return raw
	case json.Number:
		if desktopIntegerRequestID.MatchString(item.String()) {
			return json.RawMessage(item.String())
		}
	case json.RawMessage:
		if serverRequestID(item) != "" {
			return append(json.RawMessage(nil), item...)
		}
	}
	return nil
}

func questionIDs(questions []map[string]any) []string {
	result := []string{}
	for _, question := range questions {
		id := strings.TrimSpace(fmt.Sprint(question["id"]))
		if id != "" && id != "<nil>" {
			result = append(result, id)
		}
	}
	sort.Strings(result)
	return result
}
func equalStringSets(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	a, b := append([]string{}, left...), append([]string{}, right...)
	sort.Strings(a)
	sort.Strings(b)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (runtime *Runtime) patchTaskLinkCard(ctx context.Context, messageID string, link TaskLink) error {
	return SyncTaskLinkCard(ctx, runtime.links, runtime.messages, link, messageID)
}

func (runtime *Runtime) ReconcileTaskLinkCards(ctx context.Context) {
	err := runtime.reconcileTaskLinkCards(ctx)
	runtime.setHealth("task card reconciliation failed", err)
}

func (runtime *Runtime) launchWatcher(key string, run func(context.Context)) {
	runtime.watchMu.Lock()
	if runtime.closed {
		runtime.watchMu.Unlock()
		return
	}
	if _, exists := runtime.watchers[key]; exists {
		runtime.watchMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(runtime.watchCtx)
	runtime.watchers[key] = cancel
	runtime.watchWG.Add(1)
	runtime.watchMu.Unlock()
	go func() {
		defer runtime.watchWG.Done()
		defer cancel()
		defer func() { runtime.watchMu.Lock(); delete(runtime.watchers, key); runtime.watchMu.Unlock() }()
		run(ctx)
	}()
}

func (runtime *Runtime) observeDesktopTask(taskKey string) {
	runtime.launchWatcher("desktop:"+taskKey, func(ctx context.Context) { runtime.runDesktopTaskObserver(ctx, taskKey) })
}

func (runtime *Runtime) observeTurn(taskKey, threadID, turnID, replyTo, cardMessageID, cleanupDir string) {
	runtime.launchWatcher("turn:"+taskKey+":"+turnID, func(ctx context.Context) {
		runtime.waitForTurn(ctx, taskKey, threadID, turnID, replyTo, cardMessageID, cleanupDir)
	})
}

func (runtime *Runtime) runDesktopTaskObserver(ctx context.Context, taskKey string) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	reportedFailure := false
	for {
		if ctx.Err() != nil {
			return
		}
		link, found, err := runtime.links.FindByTaskKey(taskKey)
		if err != nil || !found || link.LinkState != "active" || (!link.ExpiresAt.IsZero() && !link.ExpiresAt.After(time.Now())) || !runtime.desktopOwned(link) {
			return
		}
		readCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
		err = runtime.reconcileDesktopTaskLink(readCtx, link)
		cancel()
		if err != nil && !reportedFailure {
			reportedFailure = true
			_ = runtime.recordAudit("task_link_status_read_failed", map[string]any{"taskKey": link.TaskKey, "error": errorText(err)})
		} else if err == nil {
			reportedFailure = false
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type desktopTaskProjection struct {
	TurnID, TurnState, TurnOwner, ActionRequired string
	Phase, Detail                                string
	PendingPlan                                  pendingPlan
	PendingQuestions                             []map[string]any
	PendingRequestID                             string
	PendingRequestRef                            json.RawMessage
	PendingQuestionRevision                      string
}

func (runtime *Runtime) reconcileDesktopTaskLink(ctx context.Context, link TaskLink) error {
	snapshot, err := runtime.readThread(ctx, link)
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	projection := projectDesktopTaskLink(snapshot)
	if len(projection.PendingQuestions) > 0 {
		owner := runtime.core.ProjectionOwner(link.ThreadID, link.ExtraString("runtimeOwner"))
		projection.PendingQuestionRevision = PendingQuestionRevisionScoped(link.TaskKey, projection.TurnID, owner, projection.PendingRequestRef, projection.PendingQuestions)
	}
	if projection.TurnState == "waiting_input" && desktopAnswerSubmissionPending(link, projection) {
		return nil
	}
	if projection.TurnID == "" || !desktopProjectionRequiresSync(link, projection) {
		return nil
	}
	updated, err := runtime.links.UpdateActiveByID(link.ID, func(value *TaskLink) {
		value.TurnState = projection.TurnState
		value.TurnOwner = projection.TurnOwner
		value.ActionRequired = projection.ActionRequired
		value.Phase = projection.Phase
		value.Detail = projection.Detail
		value.NextTurnMode = "default"
		if projection.TurnState == "running" || projection.TurnState == "waiting_input" || projection.TurnState == "desktop_action_required" {
			value.ActiveTurnID = projection.TurnID
		} else {
			value.ActiveTurnID = ""
		}
		if projection.PendingPlan.Content != "" {
			value.PendingPlanTurnID = projection.PendingPlan.TurnID
			value.PendingPlanRevision = planRevision(projection.PendingPlan.TurnID, projection.PendingPlan.Content)
		} else {
			value.PendingPlanTurnID = ""
			value.PendingPlanRevision = ""
		}
		if len(projection.PendingQuestions) > 0 {
			value.SetExtraValue("pendingQuestions", projection.PendingQuestions)
			value.SetExtraString("pendingQuestionRequestID", projection.PendingRequestID)
			value.SetExtraRaw("pendingQuestionRequestRef", projection.PendingRequestRef)
			value.SetExtraString("pendingQuestionRevision", projection.PendingQuestionRevision)
			if value.ExtraString("pendingInputLastErrorRevision") == projection.PendingQuestionRevision {
				value.Detail = "提交未生效，可重试。"
			} else {
				value.SetExtraString("pendingInputLastErrorRevision", "")
			}
		} else {
			value.SetExtraValue("pendingQuestions", nil)
			value.SetExtraString("pendingQuestionRequestID", "")
			value.SetExtraRaw("pendingQuestionRequestRef", nil)
			value.SetExtraString("pendingQuestionRevision", "")
			value.SetExtraString("pendingInputLastErrorRevision", "")
		}
		if projection.TurnState != "waiting_input" || projection.PendingRequestID != value.ExtraString("pendingInputAnswerRequestID") {
			value.SetExtraString("pendingInputAnswerRequestID", "")
			value.SetExtraRaw("pendingInputAnswerRequestRef", nil)
			value.SetExtraString("pendingInputAnswerTurnID", "")
			value.SetExtraString("pendingInputAnsweredAt", "")
		}
		value.SetExtraValue("cardSyncPending", true)
	})
	if err != nil {
		return err
	}
	if err := SyncTaskLinkCard(ctx, runtime.links, runtime.messages, updated, ""); err != nil {
		return err
	}
	if projection.TurnState != "running" && projection.TurnState != "waiting_input" && projection.TurnState != "desktop_action_required" && projection.TurnState != "plan_ready" {
		_, err = runtime.links.UpdateActiveByID(link.ID, func(value *TaskLink) {
			value.SetExtraString("lastDeliveredTurnId", projection.TurnID)
		})
	}
	return err
}

func projectDesktopTaskLink(snapshot map[string]any) desktopTaskProjection {
	normalized := normalizeDesktopState(snapshot)
	turns, _ := normalized["turns"].([]any)
	if len(turns) == 0 {
		return desktopTaskProjection{}
	}
	turn, _ := turns[len(turns)-1].(map[string]any)
	if turn == nil {
		return desktopTaskProjection{}
	}
	turnID := cleanString(turn["id"])
	result := desktopTaskProjection{TurnID: turnID, TurnState: "idle", TurnOwner: "none", ActionRequired: "none", Phase: "已连接", Detail: "任务已连接。回复本消息可继续任务。"}
	if pending := pendingPlanImplementation(normalized); pending.Content != "" {
		result.TurnState, result.ActionRequired = "plan_ready", "feishu"
		result.Phase, result.Detail, result.PendingPlan = "计划已生成", pending.Content, pending
		return result
	}
	if requestRef, _, questions := desktopPendingInputRaw(normalized, turnID); len(questions) > 0 {
		result.TurnState, result.TurnOwner, result.ActionRequired = "waiting_input", "desktop", "feishu"
		result.Phase, result.Detail, result.PendingQuestions = "等待输入", "Codex 等待你的选择。", questions
		result.PendingRequestRef = requestRef
		result.PendingRequestID = serverRequestID(requestRef)
		return result
	}
	status := strings.ToLower(statusType(turn["status"]))
	thread := snapshot
	if value, ok := snapshot["thread"].(map[string]any); ok {
		thread = value
	}
	threadStatus := strings.ToLower(statusType(thread["status"]))
	if isRunningStatus(status) || isRunningStatus(threadStatus) {
		result.TurnState, result.TurnOwner = "running", "desktop"
		result.Phase, result.Detail = "运行中", desktopTurnProgress(turn)
		if desktopNeedsLocalAction(thread) {
			result.TurnState, result.ActionRequired = "desktop_action_required", "desktop"
			result.Phase, result.Detail = "需要桌面操作", "当前轮等待 Codex Desktop 操作。"
		}
		return result
	}
	switch status {
	case "completed":
		result.TurnState, result.Phase, result.Detail = "completed", "已完成", finalAgentText(turn)
		if result.Detail == "" {
			result.Detail = "任务已完成。"
		}
	case "failed":
		result.TurnState, result.Phase, result.Detail = "failed", "失败", "任务执行失败，请在 Codex Desktop 查看。"
	case "interrupted", "cancelled", "canceled":
		result.TurnState, result.Phase, result.Detail = "interrupted", "已停止", "任务已停止。"
	}
	return result
}

func desktopAnswerSubmissionPending(link TaskLink, projection desktopTaskProjection) bool {
	requestID := link.ExtraRaw("pendingInputAnswerRequestRef")
	if len(requestID) == 0 {
		legacy := link.ExtraString("pendingInputAnswerRequestID")
		requestID, _ = json.Marshal(legacy)
	}
	projectionRef := projection.PendingRequestRef
	if len(projectionRef) == 0 && projection.PendingRequestID != "" {
		projectionRef, _ = json.Marshal(projection.PendingRequestID)
	}
	if len(requestID) == 0 || string(requestID) != string(projectionRef) {
		return false
	}
	if turnID := link.ExtraString("pendingInputAnswerTurnID"); turnID != "" && projection.TurnID != "" && turnID != projection.TurnID {
		return false
	}
	answeredAt, err := time.Parse(time.RFC3339Nano, link.ExtraString("pendingInputAnsweredAt"))
	return err == nil && time.Since(answeredAt) < 30*time.Second
}

func desktopProjectionRequiresSync(link TaskLink, next desktopTaskProjection) bool {
	if link.TurnState != next.TurnState || link.TurnOwner != next.TurnOwner || link.ActionRequired != next.ActionRequired || link.ActiveTurnID != activeProjectionTurnID(next) {
		return true
	}
	if link.Phase != next.Phase || strings.TrimSpace(link.Detail) != strings.TrimSpace(next.Detail) {
		return true
	}
	if link.PendingPlanTurnID != next.PendingPlan.TurnID || link.PendingPlanRevision != planRevisionIfPresent(next.PendingPlan) {
		return true
	}
	if link.ExtraString("pendingQuestionRequestID") != next.PendingRequestID || link.ExtraString("pendingQuestionRevision") != next.PendingQuestionRevision {
		return true
	}
	if isTerminalTaskState(next.TurnState) && next.TurnID != link.ExtraString("lastDeliveredTurnId") {
		return true
	}
	return false
}

func activeProjectionTurnID(value desktopTaskProjection) string {
	if value.TurnState == "running" || value.TurnState == "waiting_input" || value.TurnState == "desktop_action_required" {
		return value.TurnID
	}
	return ""
}

func planRevisionIfPresent(value pendingPlan) string {
	if value.Content == "" {
		return ""
	}
	return planRevision(value.TurnID, value.Content)
}

func isTerminalTaskState(value string) bool {
	return value == "completed" || value == "failed" || value == "interrupted" || value == "idle"
}

func isRunningStatus(value string) bool {
	return value == "active" || value == "running" || value == "inprogress" || value == "in_progress"
}

func desktopNeedsLocalAction(thread map[string]any) bool {
	status, _ := thread["status"].(map[string]any)
	flags, _ := status["activeFlags"].(map[string]any)
	if flags == nil {
		flags, _ = status["flags"].(map[string]any)
	}
	if flags == nil {
		flags = status
	}
	for _, key := range []string{"waitingOnApproval", "waiting_on_approval", "waitingOnUserInput", "waiting_on_user_input"} {
		if value, _ := flags[key].(bool); value {
			return true
		}
	}
	return false
}

func desktopTurnProgress(turn map[string]any) string {
	items, _ := turn["items"].([]any)
	for index := len(items) - 1; index >= 0; index-- {
		item, _ := items[index].(map[string]any)
		if item == nil || fmt.Sprint(item["type"]) != "agentMessage" || strings.ToLower(fmt.Sprint(item["phase"])) != "commentary" {
			continue
		}
		if value := boundedPublicText(fmt.Sprint(item["text"]), 900); value != "" {
			return value
		}
	}
	return "Codex 正在处理。"
}

func boundedPublicText(value string, maximum int) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\r\n", "\n"))
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	return strings.TrimSpace(string(runes[:maximum-1])) + "…"
}

func cleanString(value any) string {
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "<nil>" {
		return ""
	}
	return text
}

type pendingPlan struct{ TurnID, Content string }

func pendingPlanImplementation(snapshot map[string]any) pendingPlan {
	thread, _ := snapshot["thread"].(map[string]any)
	if thread == nil {
		thread = snapshot
	}
	if value, ok := thread["pendingPlanImplementation"].(map[string]any); ok {
		return pendingPlan{TurnID: strings.TrimSpace(fmt.Sprint(value["turnId"])), Content: strings.TrimSpace(fmt.Sprint(value["planContent"]))}
	}
	if requests, ok := thread["requests"].([]any); ok {
		for _, raw := range requests {
			request, _ := raw.(map[string]any)
			if fmt.Sprint(request["method"]) != "item/plan/requestImplementation" {
				continue
			}
			params, _ := request["params"].(map[string]any)
			value := pendingPlan{TurnID: strings.TrimSpace(fmt.Sprint(params["turnId"])), Content: strings.TrimSpace(fmt.Sprint(params["planContent"]))}
			if value.TurnID != "" && value.TurnID != "<nil>" && value.Content != "" && value.Content != "<nil>" {
				return value
			}
		}
	}
	turns, _ := thread["turns"].([]any)
	for turnIndex := len(turns) - 1; turnIndex >= 0; turnIndex-- {
		turn, _ := turns[turnIndex].(map[string]any)
		items, _ := turn["items"].([]any)
		for itemIndex := len(items) - 1; itemIndex >= 0; itemIndex-- {
			item, _ := items[itemIndex].(map[string]any)
			if fmt.Sprint(item["type"]) == "planImplementation" && item["isCompleted"] != true {
				return pendingPlan{TurnID: strings.TrimSpace(fmt.Sprint(item["turnId"])), Content: strings.TrimSpace(fmt.Sprint(item["planContent"]))}
			}
		}
	}
	return pendingPlan{}
}

func planRevision(turnID, content string) string {
	sum := sha256.Sum256([]byte(turnID + "\x00" + content))
	return hex.EncodeToString(sum[:10])
}

func planCollaborationMode(snapshot map[string]any) (map[string]any, error) {
	model := recursiveString(snapshot, "model")
	if model == "" {
		return nil, errors.New("cannot determine current Codex model for plan mode")
	}
	return map[string]any{"mode": "plan", "settings": map[string]any{"model": model, "reasoning_effort": nil, "developer_instructions": nil}}, nil
}

func recursiveString(value any, wanted string) string {
	switch item := value.(type) {
	case map[string]any:
		if text, ok := item[wanted].(string); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
		for _, child := range item {
			if text := recursiveString(child, wanted); text != "" {
				return text
			}
		}
	case []any:
		for _, child := range item {
			if text := recursiveString(child, wanted); text != "" {
				return text
			}
		}
	}
	return ""
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
func bridgeTurnProjection(snapshot map[string]any, turnID string) (string, string) {
	thread, _ := snapshot["thread"].(map[string]any)
	if thread == nil {
		thread = snapshot
	}
	turns, _ := thread["turns"].([]any)
	for index := len(turns) - 1; index >= 0; index-- {
		turn, _ := turns[index].(map[string]any)
		if turn == nil || fmt.Sprint(turn["id"]) != turnID {
			continue
		}
		status := strings.ToLower(statusType(turn["status"]))
		if status == "completed" {
			return "completed", finalAgentText(turn)
		}
		if status == "failed" {
			return "failed", ""
		}
		if status == "interrupted" || status == "cancelled" || status == "canceled" {
			return "interrupted", ""
		}
		return "running", ""
	}
	return "running", ""
}
func statusType(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	if object, ok := value.(map[string]any); ok {
		return fmt.Sprint(object["type"])
	}
	return ""
}
func finalAgentText(turn map[string]any) string {
	items, _ := turn["items"].([]any)
	for index := len(items) - 1; index >= 0; index-- {
		item, _ := items[index].(map[string]any)
		if item != nil && fmt.Sprint(item["type"]) == "agentMessage" {
			text := strings.TrimSpace(fmt.Sprint(item["text"]))
			if text != "" && text != "<nil>" {
				if len([]rune(text)) > 6000 {
					return string([]rune(text)[:6000]) + "…"
				}
				return text
			}
		}
	}
	return ""
}
func firstMessageTitle(text string) string {
	line := strings.TrimSpace(strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")[0])
	runes := []rune("飞书 · " + line)
	if len(runes) > 64 {
		runes = runes[:64]
	}
	return string(runes)
}
func appendUnique(values []string, value string) []string {
	for _, item := range values {
		if item == value {
			return values
		}
	}
	return append(values, value)
}

func (runtime *Runtime) recordAudit(event string, fields map[string]any) error {
	if audit, ok := runtime.messages.(AuditPort); ok {
		return audit.Record(event, fields)
	}
	return nil
}
