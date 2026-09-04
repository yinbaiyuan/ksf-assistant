package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"codexusagebar/core/internal/codex"
	"codexusagebar/core/internal/desktop"
	"codexusagebar/core/internal/feishu"
)

type inboundRuntime struct {
	dataRoot string
	codex    *codex.Client
	desktop  *desktop.ActivityClient
	messages *feishu.OfficialMessageClient
	links    feishu.TaskLinkStore
	config   feishu.ClientConfig
	executor feishu.CapabilityExecutor
}

func newInboundRuntime(dataRoot string, messages *feishu.OfficialMessageClient) (*inboundRuntime, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	executable, err := codex.LocateExecutable(home)
	if err != nil {
		return nil, err
	}
	config, err := feishu.NewClientConfigStore(dataRoot).Load()
	if err != nil {
		return nil, err
	}
	desktopClient := desktop.New(desktop.DefaultEndpoint(home))
	if err := desktopClient.Start(context.Background()); err != nil {
		return nil, fmt.Errorf("connect Codex Desktop IPC: %w", err)
	}
	return &inboundRuntime{dataRoot: dataRoot, codex: &codex.Client{Executable: executable, Timeout: 20 * time.Second}, desktop: desktopClient, messages: messages, links: feishu.NewTaskLinkStore(dataRoot), config: config, executor: feishu.CapabilityExecutor{Binary: strings.TrimSpace(os.Getenv("LARK_CLI_BIN")), Profile: strings.TrimSpace(os.Getenv("LARK_CLI_PROFILE")), DataRoot: dataRoot, WorkingDirectory: dataRoot}}, nil
}
func (runtime *inboundRuntime) Close() { runtime.desktop.Close(); _ = runtime.codex.Close() }
func (runtime *inboundRuntime) ResumeActive() error {
	file, err := runtime.links.Load()
	if err != nil {
		return err
	}
	for _, link := range file.Links {
		if link.LinkState != "active" || link.ActiveTurnID == "" || (link.TurnState != "running" && link.TurnState != "queued" && link.TurnState != "waiting_input") {
			continue
		}
		cardMessageID := link.RootMessageID
		go runtime.waitForTurn(link.TaskKey, link.ThreadID, link.ActiveTurnID, cardMessageID, cardMessageID, link.ExtraString("pendingCleanupDir"))
	}
	return nil
}
func (runtime *inboundRuntime) HandleMessage(ctx context.Context, message feishu.InboundMessage) error {
	prompt := strings.TrimSpace(message.Text)
	cleanupDir := ""
	if message.MessageType != "text" {
		if message.ChatType != "p2p" && message.ChatType != "direct" && message.ChatType != "" {
			_, err := runtime.messages.Reply(ctx, message.MessageID, "text", "群聊中的附件暂不交给 Codex 处理；请在授权单聊中发送。", replyIdempotencyKey(message.MessageID, "unsupported-group-attachment", 0))
			return err
		}
		staged, err := feishu.StageInboundMessage(ctx, runtime.executor, message, 25*1024*1024)
		if err != nil {
			return err
		}
		cleanupDir = staged.CleanupDir
		prompt = feishu.InboundAssetsPrompt(staged.Text, staged)
	}
	if strings.TrimSpace(prompt) == "" {
		_, err := runtime.messages.Reply(ctx, message.MessageID, "text", "消息中没有可读取的文本或附件资源。", replyIdempotencyKey(message.MessageID, "empty", 0))
		return err
	}
	workspace, err := runtime.workspace()
	if err != nil {
		return err
	}
	var link feishu.TaskLink
	found := false
	foundByOwnMessage := false
	for _, candidate := range []string{message.MessageID, message.RootID, message.ParentID} {
		if candidate != "" {
			link, found, err = runtime.links.FindByMessage(candidate)
			if err != nil {
				return err
			}
			if found {
				foundByOwnMessage = candidate == message.MessageID
				break
			}
		}
	}
	if foundByOwnMessage && link.ActiveTurnID != "" {
		if link.RootMessageID == message.MessageID {
			cardJSON, cardErr := feishu.TaskLinkCardJSON(link)
			if cardErr != nil {
				return cardErr
			}
			cardMessageID, cardErr := runtime.messages.Send(ctx, link.Target, "card", cardJSON, replyIdempotencyKey(message.MessageID, "processing-card", 0))
			if cardErr != nil {
				return cardErr
			}
			link, _ = runtime.links.Update(link.TaskKey, func(value *feishu.TaskLink) {
				value.RootMessageID = cardMessageID
				value.MessageIDs = appendUnique(value.MessageIDs, cardMessageID)
			})
			go runtime.waitForTurn(link.TaskKey, link.ThreadID, link.ActiveTurnID, message.MessageID, cardMessageID, link.ExtraString("pendingCleanupDir"))
		}
		return nil
	}
	if foundByOwnMessage && (link.TurnState == "completed" || link.TurnState == "failed" || link.TurnState == "interrupted") {
		return nil
	}
	if !found {
		title := firstMessageTitle(prompt)
		threadID, err := runtime.codex.StartBridgeThread(ctx, workspace, title)
		if err != nil {
			return err
		}
		alias := runtime.aliasForOpenID(message.SenderOpenID)
		link, err = runtime.links.Upsert(threadID, title, "", alias)
		if err != nil {
			return err
		}
		link, err = runtime.links.Update(link.TaskKey, func(value *feishu.TaskLink) {
			value.Target = feishu.MessageTarget{Type: "open_id", ID: message.SenderOpenID}
			value.RootMessageID = message.MessageID
			value.MessageIDs = append(value.MessageIDs, message.MessageID)
			value.SetExtraString("runtimeOwner", "bridge")
		})
		if err != nil {
			return err
		}
	}
	turnID, err := runtime.startTurn(ctx, link, workspace, prompt, nil)
	if err != nil {
		_ = feishu.CleanupInboundAssets(runtime.dataRoot, cleanupDir)
		return err
	}
	link, err = runtime.links.Update(link.TaskKey, func(value *feishu.TaskLink) {
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
	cardJSON, _ := feishu.TaskLinkCardJSON(link)
	cardMessageID, cardErr := runtime.messages.Send(ctx, link.Target, "card", cardJSON, replyIdempotencyKey(message.MessageID, "processing-card", 0))
	if cardErr == nil {
		link, _ = runtime.links.Update(link.TaskKey, func(value *feishu.TaskLink) {
			if value.RootMessageID == message.MessageID {
				value.RootMessageID = cardMessageID
			}
			value.MessageIDs = appendUnique(value.MessageIDs, cardMessageID)
		})
	} else {
		fallbackID, fallbackErr := runtime.messages.Send(ctx, link.Target, "text", "Codex 已收到消息，正在处理。", replyIdempotencyKey(message.MessageID, "processing-text-fallback", 0))
		_ = feishu.NewAuditLog(runtime.dataRoot).Record("inbound_feedback_fallback", map[string]any{
			"phase": "processing", "replyError": cardErr.Error(), "fallbackError": errorText(fallbackErr), "fallbackSent": fallbackID != "",
		})
	}
	go runtime.waitForTurn(link.TaskKey, link.ThreadID, turnID, message.MessageID, cardMessageID, cleanupDir)
	return nil
}
func (runtime *inboundRuntime) HandleCard(ctx context.Context, action feishu.InboundCardAction) error {
	if action.TaskKey == "" {
		return errors.New("card action has no task key")
	}
	link, found, err := runtime.links.FindByTaskKey(action.TaskKey)
	if err != nil {
		return err
	}
	if !found && action.Action == "task_link_release" {
		candidate, candidateFound, candidateErr := runtime.links.FindAnyByTaskKey(action.TaskKey)
		if candidateErr != nil {
			return candidateErr
		}
		if candidateFound && candidate.LinkState == "released" {
			link, found = candidate, true
		}
	}
	if !found {
		return errors.New("task link not found")
	}
	target := link.Target
	if target.ID == "" && link.TargetAlias != "" {
		target, _ = runtime.config.ResolveMessageTarget(link.TargetAlias)
	}
	if target.Type != "open_id" || target.ID != action.OperatorOpenID {
		return errors.New("card operator does not match task link")
	}
	if action.MessageID == "" || (action.MessageID != link.RootMessageID && !containsString(link.MessageIDs, action.MessageID)) {
		return errors.New("card message does not match task link")
	}
	switch action.Action {
	case "task_link_interrupt":
		link, err := runtime.links.Update(action.TaskKey, func(value *feishu.TaskLink) {
			value.TurnState = "interrupted"
			value.TurnOwner = "none"
			value.ActionRequired = "none"
			value.Phase = "已停止"
		})
		if err != nil {
			return err
		}
		if link.ActiveTurnID != "" {
			if err := runtime.interruptTurn(ctx, link); err != nil {
				return err
			}
		}
		return runtime.patchTaskLinkCard(ctx, action.MessageID, link)
	case "task_link_release":
		if link.LinkState == "released" {
			return feishu.SyncTaskLinkCard(ctx, runtime.links, runtime.messages, link, action.MessageID)
		}
		link, err := runtime.links.Release(action.TaskKey)
		if err != nil {
			return err
		}
		return feishu.SyncTaskLinkCard(ctx, runtime.links, runtime.messages, link, action.MessageID)
	case "task_link_followup", "task_link_answer":
		followup := strings.TrimSpace(fmt.Sprint(action.FormValue["followup"]))
		if action.Action == "task_link_answer" {
			questionID, answer := fmt.Sprint(action.Value["questionId"]), fmt.Sprint(action.Value["answer"])
			if err := runtime.answerInput(ctx, link, questionID, answer); err != nil {
				return err
			}
			link, err = runtime.links.Update(action.TaskKey, func(value *feishu.TaskLink) {
				value.TurnState = "running"
				value.ActionRequired = "none"
				value.Phase = "执行"
				value.Detail = "已收到你的选择。"
				value.SetExtraValue("pendingQuestions", nil)
			})
			if err != nil {
				return err
			}
			return runtime.patchTaskLinkCard(ctx, action.MessageID, link)
		}
		if followup == "" {
			return errors.New("empty task link followup")
		}
		workspace, err := runtime.workspace()
		if err != nil {
			return err
		}
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
		link, err = runtime.links.Update(action.TaskKey, func(value *feishu.TaskLink) {
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
		go runtime.waitForTurn(link.TaskKey, link.ThreadID, turnID, action.MessageID, action.MessageID, "")
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
		workspace, err := runtime.workspace()
		if err != nil {
			return err
		}
		turnID, err := runtime.startTurn(ctx, link, workspace, "PLEASE IMPLEMENT THIS PLAN:\n"+pending.Content, nil)
		if err != nil {
			return err
		}
		link, err = runtime.links.Update(action.TaskKey, func(value *feishu.TaskLink) {
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
		go runtime.waitForTurn(link.TaskKey, link.ThreadID, turnID, action.MessageID, action.MessageID, "")
		return nil
	default:
		return errors.New("unsupported_card_action")
	}
}
func (runtime *inboundRuntime) workspace() (string, error) {
	context, err := feishu.NewHostContextStore(runtime.dataRoot).Load()
	if err != nil {
		return "", err
	}
	if context.KSF.State != feishu.KSFReady {
		return "", errors.New("KSF workspace is not configured")
	}
	return context.KSF.Root, nil
}
func (runtime *inboundRuntime) aliasForOpenID(openID string) string {
	for alias, target := range runtime.config.MessageTargets {
		if target.Type == "open_id" && target.ID == openID {
			return alias
		}
	}
	return ""
}
func (runtime *inboundRuntime) waitForTurn(taskKey, threadID, turnID, replyTo, cardMessageID, cleanupDir string) {
	defer func() { _ = feishu.CleanupInboundAssets(runtime.dataRoot, cleanupDir) }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		link, found, _ := runtime.links.FindByTaskKey(taskKey)
		if !found {
			return
		}
		snapshot, err := runtime.readThread(ctx, link)
		if err == nil {
			if request, waiting := runtime.pendingInput(link, snapshot); waiting {
				link, _ := runtime.links.Update(taskKey, func(value *feishu.TaskLink) {
					value.TurnState = "waiting_input"
					value.ActionRequired = "feishu"
					value.ActiveTurnID = turnID
					value.Phase = "等待输入"
					value.Detail = "Codex 等待你的选择。"
					value.SetExtraValue("pendingQuestions", request.Questions)
				})
				if cardMessageID != "" {
					_ = runtime.patchTaskLinkCard(context.Background(), cardMessageID, link)
				}
			}
			pending := pendingPlanImplementation(snapshot)
			if pending.Content != "" && pending.TurnID == turnID {
				revision := planRevision(pending.TurnID, pending.Content)
				link, _ := runtime.links.Update(taskKey, func(value *feishu.TaskLink) {
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
				})
				if cardMessageID != "" {
					_ = runtime.patchTaskLinkCard(context.Background(), cardMessageID, link)
				}
				return
			}
			state, final := bridgeTurnProjection(snapshot, turnID)
			if state != "running" {
				_, _ = runtime.links.Update(taskKey, func(value *feishu.TaskLink) {
					value.TurnState = state
					value.TurnOwner = "none"
					value.ActionRequired = "none"
					value.ActiveTurnID = ""
					value.Phase = map[string]string{"completed": "已完成", "failed": "失败", "interrupted": "已停止"}[state]
					value.Detail = final
					value.SetExtraString("pendingCleanupDir", "")
				})
				if final == "" {
					final = map[string]string{"completed": "任务已完成。", "failed": "任务执行失败，请在 Codex Desktop 查看。", "interrupted": "任务已停止。"}[state]
				}
				link, _, _ := runtime.links.FindByTaskKey(taskKey)
				patched := false
				if cardMessageID != "" {
					patched = runtime.patchTaskLinkCard(context.Background(), cardMessageID, link) == nil
				}
				if !patched {
					finalCard, cardErr := feishu.TaskLinkCardJSON(link)
					fallbackID := ""
					if cardErr == nil {
						fallbackID, cardErr = runtime.messages.Send(context.Background(), link.Target, "card", finalCard, replyIdempotencyKey(replyTo, "final-card-fallback", 0))
					}
					if cardErr != nil {
						textID, textErr := runtime.messages.Send(context.Background(), link.Target, "text", final, replyIdempotencyKey(replyTo, "final-text-fallback", 0))
						_ = feishu.NewAuditLog(runtime.dataRoot).Record("inbound_feedback_fallback", map[string]any{
							"phase": "final", "cardError": cardErr.Error(), "fallbackError": errorText(textErr), "fallbackSent": textID != "",
						})
					} else if fallbackID != "" {
						_ = feishu.NewAuditLog(runtime.dataRoot).Record("inbound_card_recreated", map[string]any{"phase": "final", "sent": true})
					}
				}
				return
			}
		}
		select {
		case <-ctx.Done():
			_, _ = runtime.links.Update(taskKey, func(value *feishu.TaskLink) {
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

func (runtime *inboundRuntime) desktopOwned(link feishu.TaskLink) bool {
	return link.ExtraString("runtimeOwner") != "bridge"
}
func (runtime *inboundRuntime) readThread(ctx context.Context, link feishu.TaskLink) (map[string]any, error) {
	if runtime.desktopOwned(link) {
		value, err := runtime.desktop.ReadConversationState(ctx, link.ThreadID)
		if err != nil {
			return nil, err
		}
		return normalizeDesktopState(value), nil
	}
	return runtime.codex.ReadBridgeThread(ctx, link.ThreadID)
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
func (runtime *inboundRuntime) startTurn(ctx context.Context, link feishu.TaskLink, cwd, text string, mode map[string]any) (string, error) {
	if runtime.desktopOwned(link) {
		return runtime.desktop.StartBridgeTurn(ctx, link.ThreadID, cwd, text, mode)
	}
	return runtime.codex.StartBridgeTurnWithMode(ctx, link.ThreadID, cwd, text, mode)
}
func (runtime *inboundRuntime) steerTurn(ctx context.Context, link feishu.TaskLink, cwd, text string) (string, error) {
	if runtime.desktopOwned(link) {
		return runtime.desktop.SteerBridgeTurn(ctx, link.ThreadID, cwd, link.ActiveTurnID, text)
	}
	return runtime.codex.SteerBridgeTurn(ctx, link.ThreadID, link.ActiveTurnID, text)
}
func (runtime *inboundRuntime) interruptTurn(ctx context.Context, link feishu.TaskLink) error {
	if runtime.desktopOwned(link) {
		return runtime.desktop.InterruptBridgeTurn(ctx, link.ThreadID, link.ActiveTurnID)
	}
	return runtime.codex.InterruptBridgeTurn(ctx, link.ThreadID, link.ActiveTurnID)
}
func (runtime *inboundRuntime) pendingInput(link feishu.TaskLink, snapshot map[string]any) (codex.ServerRequest, bool) {
	if !runtime.desktopOwned(link) {
		return runtime.codex.PendingBridgeUserInput(link.ThreadID)
	}
	requestID, pendingTurnID, questions := desktopPendingInput(snapshot, link.ActiveTurnID)
	if requestID == "" {
		return codex.ServerRequest{}, false
	}
	return codex.ServerRequest{ID: json.RawMessage(`null`), Method: "item/tool/requestUserInput", ThreadID: link.ThreadID, TurnID: pendingTurnID, Questions: questions}, true
}
func (runtime *inboundRuntime) answerInput(ctx context.Context, link feishu.TaskLink, questionID, answer string) error {
	answers := map[string]any{questionID: map[string]any{"answers": []string{answer}}}
	if !runtime.desktopOwned(link) {
		return runtime.codex.AnswerBridgeUserInput(link.ThreadID, answers)
	}
	snapshot, err := runtime.desktop.ReadConversationState(ctx, link.ThreadID)
	if err != nil {
		return err
	}
	var expected []map[string]any
	_ = link.ExtraValue("pendingQuestions", &expected)
	requestID, _, _ := matchingDesktopPendingInput(snapshot, link.ActiveTurnID, questionIDs(expected))
	if requestID == "" {
		return errors.New("Codex Desktop no longer has the expected user-input request")
	}
	return runtime.desktop.SubmitBridgeUserInput(ctx, link.ThreadID, requestID, map[string]any{"answers": answers})
}

func desktopPendingInput(value any, expectedTurnID string) (string, string, []map[string]any) {
	return matchingDesktopPendingInput(value, expectedTurnID, nil)
}

type desktopInputCandidate struct {
	id, turnID string
	questions  []map[string]any
}

func matchingDesktopPendingInput(value any, expectedTurnID string, expectedQuestionIDs []string) (string, string, []map[string]any) {
	candidates := []desktopInputCandidate{}
	collectDesktopPendingInputs(value, &candidates)
	matches := []desktopInputCandidate{}
	for _, candidate := range candidates {
		if expectedTurnID != "" && candidate.turnID != expectedTurnID {
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
			id := strings.TrimSpace(fmt.Sprint(item["id"]))
			if id != "" && id != "<nil>" {
				*candidates = append(*candidates, desktopInputCandidate{id: id, turnID: turnID, questions: questions})
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

func (runtime *inboundRuntime) patchTaskLinkCard(ctx context.Context, messageID string, link feishu.TaskLink) error {
	return feishu.SyncTaskLinkCard(ctx, runtime.links, runtime.messages, link, messageID)
}

func (runtime *inboundRuntime) reconcileTaskLinkCards(ctx context.Context) {
	file, err := runtime.links.Load()
	if err != nil {
		return
	}
	for _, link := range file.Links {
		if !feishu.TaskLinkCardSyncPending(link) {
			continue
		}
		_ = feishu.SyncTaskLinkCard(ctx, runtime.links, runtime.messages, link, "")
	}
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

func interruptLinkedTurn(dataRoot, taskKey string) error {
	file, err := feishu.NewTaskLinkStore(dataRoot).Load()
	if err != nil {
		return err
	}
	var selected *feishu.TaskLink
	for index := len(file.Links) - 1; index >= 0; index-- {
		if file.Links[index].TaskKey == taskKey && file.Links[index].LinkState == "active" {
			copy := file.Links[index]
			selected = &copy
			break
		}
	}
	if selected == nil {
		return errors.New("task link not found")
	}
	if selected.ActiveTurnID == "" {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if selected.ExtraString("runtimeOwner") != "bridge" {
		client := desktop.New(desktop.DefaultEndpoint(home))
		defer client.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := client.Start(ctx); err != nil {
			return err
		}
		return client.InterruptBridgeTurn(ctx, selected.ThreadID, selected.ActiveTurnID)
	}
	executable, err := codex.LocateExecutable(home)
	if err != nil {
		return err
	}
	client := &codex.Client{Executable: executable, Timeout: 10 * time.Second}
	defer client.Close()
	return client.InterruptBridgeTurn(context.Background(), selected.ThreadID, selected.ActiveTurnID)
}
