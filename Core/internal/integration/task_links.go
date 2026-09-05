package integration

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"ksfassistant/core/internal/privatestore"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	TaskLinkProtocol        = "codex-feishu-task-link-v1"
	TaskLinkSchema          = 2
	taskLinkHistoryTTL      = 7 * 24 * time.Hour
	taskLinkProtectionLimit = 30 * 24 * time.Hour
	taskLinkTerminalHistory = 500
)

type TaskLinkStore struct{ path string }

type TaskLinkCleanupReport struct {
	Removed            int `json:"removed"`
	Protected          int `json:"protected"`
	AbandonedProtected int `json:"abandonedProtected"`
}

type TaskLinkFile struct {
	Protocol      string                     `json:"protocol"`
	SchemaVersion int                        `json:"schemaVersion"`
	UpdatedAt     time.Time                  `json:"updatedAt"`
	Links         []TaskLink                 `json:"links"`
	Extra         map[string]json.RawMessage `json:"-"`
}

func (file *TaskLinkFile) UnmarshalJSON(data []byte) error {
	type plain TaskLinkFile
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, key := range []string{"protocol", "schemaVersion", "updatedAt", "links"} {
		delete(fields, key)
	}
	*file = TaskLinkFile(value)
	file.Extra = fields
	return nil
}

func (file TaskLinkFile) MarshalJSON() ([]byte, error) {
	type plain TaskLinkFile
	data, err := json.Marshal(plain(file))
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	for key, value := range file.Extra {
		if _, exists := fields[key]; !exists {
			fields[key] = value
		}
	}
	return json.Marshal(fields)
}

func (store TaskLinkStore) UpdateActiveByID(id string, patch func(*TaskLink)) (TaskLink, error) {
	var result TaskLink
	err := privatestore.WithFileLock(store.path+".lock", func() error {
		file, err := store.Load()
		if err != nil {
			return err
		}
		for index := range file.Links {
			link := &file.Links[index]
			if link.ID != id {
				continue
			}
			if effectiveTaskLinkState(*link, time.Now()) != "active" {
				return ErrInactiveTaskLink
			}
			patch(link)
			link.UpdatedAt = time.Now().UTC()
			result = *link
			return store.saveUnlocked(file)
		}
		return errors.New("task link record not found")
	})
	return result, err
}

// TaskLink intentionally keeps the on-disk v2 field names. Node can therefore
// read links created by Go during the explicit rollback window without a data
// migration, while all callers only receive PublicTaskLink projections.
type TaskLink struct {
	ID                  string        `json:"id"`
	TaskKey             string        `json:"taskKey"`
	ThreadID            string        `json:"threadId"`
	Title               string        `json:"title"`
	ProjectName         string        `json:"projectName,omitempty"`
	TargetAlias         string        `json:"targetAlias"`
	Target              MessageTarget `json:"target"`
	RootMessageID       string        `json:"rootMessageId,omitempty"`
	MessageIDs          []string      `json:"messageIds,omitempty"`
	ActiveTurnID        string        `json:"activeTurnId,omitempty"`
	PendingPlanTurnID   string        `json:"pendingPlanTurnId,omitempty"`
	PendingPlanRevision string        `json:"pendingPlanRevision,omitempty"`
	NextTurnMode        string        `json:"nextTurnMode,omitempty"`
	LinkState           string        `json:"linkState"`
	TurnState           string        `json:"turnState"`
	TurnOwner           string        `json:"turnOwner"`
	ActionRequired      string        `json:"actionRequired"`
	CreatedAt           time.Time     `json:"createdAt"`
	UpdatedAt           time.Time     `json:"updatedAt"`
	ExpiresAt           time.Time     `json:"expiresAt"`
	Phase               string        `json:"phase,omitempty"`
	Detail              string        `json:"detail,omitempty"`
	// Extra is retained verbatim across Go writes so the rollback bridge sees
	// its v2 fields (cards, input capture, cleanup paths, plan state) intact.
	Extra map[string]json.RawMessage `json:"-"`
}

func (link *TaskLink) UnmarshalJSON(data []byte) error {
	type plain TaskLink
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, key := range []string{"id", "taskKey", "threadId", "title", "projectName", "targetAlias", "target", "rootMessageId", "messageIds", "activeTurnId", "pendingPlanTurnId", "pendingPlanRevision", "nextTurnMode", "linkState", "turnState", "turnOwner", "actionRequired", "createdAt", "updatedAt", "expiresAt", "phase", "detail"} {
		delete(fields, key)
	}
	*link = TaskLink(value)
	link.Extra = fields
	return nil
}

func (link TaskLink) MarshalJSON() ([]byte, error) {
	type plain TaskLink
	base, err := json.Marshal(plain(link))
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(base, &fields); err != nil {
		return nil, err
	}
	for key, value := range link.Extra {
		if _, exists := fields[key]; !exists {
			fields[key] = value
		}
	}
	return json.Marshal(fields)
}

type PublicTaskLink struct {
	TaskKey           string `json:"taskKey"`
	Title             string `json:"title"`
	ProjectName       string `json:"projectName"`
	TargetAlias       string `json:"targetAlias"`
	LinkState         string `json:"linkState"`
	TurnState         string `json:"turnState"`
	TurnOwner         string `json:"turnOwner"`
	ActionRequired    string `json:"actionRequired"`
	Controls          any    `json:"controls"`
	CreatedAt         string `json:"createdAt"`
	UpdatedAt         string `json:"updatedAt"`
	ExpiresAt         string `json:"expiresAt"`
	RemainingSeconds  int    `json:"remainingSeconds"`
	HasPendingMessage bool   `json:"hasPendingMessage"`
	DetailAvailable   bool   `json:"detailAvailable"`
	Phase             string `json:"phase"`
	DetailSummary     string `json:"detailSummary"`
}

func (link TaskLink) ExtraString(name string) string {
	var value string
	if raw := link.Extra[name]; len(raw) > 0 {
		_ = json.Unmarshal(raw, &value)
	}
	return value
}
func (link TaskLink) ExtraRaw(name string) json.RawMessage {
	if raw := link.Extra[name]; len(raw) > 0 {
		return append(json.RawMessage(nil), raw...)
	}
	return nil
}
func (link *TaskLink) SetExtraRaw(name string, value json.RawMessage) {
	if link.Extra == nil {
		link.Extra = map[string]json.RawMessage{}
	}
	if len(value) == 0 || string(value) == "null" {
		delete(link.Extra, name)
		return
	}
	link.Extra[name] = append(json.RawMessage(nil), value...)
}
func (link *TaskLink) SetExtraString(name, value string) {
	if link.Extra == nil {
		link.Extra = map[string]json.RawMessage{}
	}
	if value == "" {
		delete(link.Extra, name)
		return
	}
	link.Extra[name], _ = json.Marshal(value)
}
func (link TaskLink) ExtraValue(name string, target any) bool {
	raw := link.Extra[name]
	return len(raw) > 0 && json.Unmarshal(raw, target) == nil
}
func (link *TaskLink) SetExtraValue(name string, value any) {
	if link.Extra == nil {
		link.Extra = map[string]json.RawMessage{}
	}
	if value == nil {
		delete(link.Extra, name)
		return
	}
	link.Extra[name], _ = json.Marshal(value)
}

func NewTaskLinkStore(dataRoot string) TaskLinkStore {
	return TaskLinkStore{path: filepath.Join(dataRoot, "task-links-v1.json")}
}

func (store TaskLinkStore) Load() (TaskLinkFile, error) {
	value := TaskLinkFile{Protocol: TaskLinkProtocol, SchemaVersion: TaskLinkSchema, UpdatedAt: time.Unix(0, 0).UTC(), Links: []TaskLink{}}
	missing, err := privatestore.ReadJSON(store.path, &value)
	if missing {
		return value, nil
	}
	if err != nil {
		return TaskLinkFile{}, err
	}
	if value.Protocol != TaskLinkProtocol || value.SchemaVersion != TaskLinkSchema {
		return TaskLinkFile{}, errors.New("unsupported task link store")
	}
	return value, nil
}

func (store TaskLinkStore) Save(value TaskLinkFile) error {
	return privatestore.WithFileLock(store.path+".lock", func() error { return store.saveUnlocked(value) })
}

// CleanupAt applies the bounded history policy without changing the v2 disk
// schema. Active records are never removed. Terminal records with unfinished
// remote or attachment work remain protected for at most 30 days.
func (store TaskLinkStore) CleanupAt(now time.Time) (TaskLinkCleanupReport, error) {
	report := TaskLinkCleanupReport{}
	err := privatestore.WithFileLock(store.path+".lock", func() error {
		file, err := store.Load()
		if err != nil {
			return err
		}
		kept := make([]TaskLink, 0, len(file.Links))
		terminal := make([]int, 0, len(file.Links))
		for _, link := range file.Links {
			state := effectiveTaskLinkState(link, now)
			if state == "active" {
				kept = append(kept, link)
				continue
			}
			age := now.Sub(link.UpdatedAt)
			protected := taskLinkCleanupProtected(link)
			if protected && age <= taskLinkProtectionLimit {
				report.Protected++
				kept = append(kept, link)
				continue
			}
			if protected {
				report.AbandonedProtected++
			}
			if age > taskLinkHistoryTTL {
				report.Removed++
				continue
			}
			terminal = append(terminal, len(kept))
			kept = append(kept, link)
		}

		if len(terminal) > taskLinkTerminalHistory {
			removeCount := len(terminal) - taskLinkTerminalHistory
			sort.SliceStable(terminal, func(i, j int) bool {
				return kept[terminal[i]].UpdatedAt.Before(kept[terminal[j]].UpdatedAt)
			})
			remove := map[int]bool{}
			for _, index := range terminal[:removeCount] {
				remove[index] = true
			}
			compacted := make([]TaskLink, 0, len(kept)-removeCount)
			for index, link := range kept {
				if remove[index] {
					report.Removed++
					continue
				}
				compacted = append(compacted, link)
			}
			kept = compacted
		}
		if report.Removed == 0 {
			return nil
		}
		file.Links = kept
		return store.saveUnlocked(file)
	})
	if err != nil {
		return TaskLinkCleanupReport{}, err
	}

	return report, nil
}

func taskLinkCleanupProtected(link TaskLink) bool {
	var pending, recovering bool
	_ = link.ExtraValue("cardSyncPending", &pending)
	_ = link.ExtraValue("recovering", &recovering)
	recoveryState := strings.ToLower(strings.TrimSpace(link.ExtraString("recoveryState")))
	return pending || recovering || strings.TrimSpace(link.ExtraString("pendingCleanupDir")) != "" || (recoveryState != "" && recoveryState != "completed" && recoveryState != "failed")
}
func (store TaskLinkStore) saveUnlocked(value TaskLinkFile) error {
	if value.Protocol != TaskLinkProtocol || value.SchemaVersion != TaskLinkSchema {
		return errors.New("invalid task link store")
	}
	value.UpdatedAt = time.Now().UTC()
	return privatestore.WriteJSON(store.path, value)
}

func taskKey(threadID string) string {
	sum := sha256.Sum256([]byte(threadID))
	return hex.EncodeToString(sum[:10])
}

func randomLinkID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "LINK-" + strings.ToUpper(hex.EncodeToString(b)), nil
}

// TaskLinkCardIdempotencyKey identifies one connection lifecycle, rather than
// the task itself. Reusing TaskKey here would make Feishu deduplicate a new
// card after the previous connection was released and the task reconnected.
func TaskLinkCardIdempotencyKey(link TaskLink) (string, error) {
	id := strings.TrimSpace(link.ID)
	if id == "" {
		return "", errors.New("task link id is required for card delivery")
	}
	sum := sha256.Sum256([]byte(TaskLinkProtocol + "\x00" + id))
	return "task-link-" + hex.EncodeToString(sum[:16]), nil
}

func (store TaskLinkStore) Upsert(threadID, title, projectName, targetAlias string) (TaskLink, error) {
	var result TaskLink
	err := privatestore.WithFileLock(store.path+".lock", func() error {
		var err error
		result, err = store.upsertUnlocked(threadID, title, projectName, targetAlias)
		return err
	})
	return result, err
}
func (store TaskLinkStore) upsertUnlocked(threadID, title, projectName, targetAlias string) (TaskLink, error) {
	if strings.TrimSpace(threadID) == "" || strings.TrimSpace(title) == "" || strings.TrimSpace(targetAlias) == "" {
		return TaskLink{}, errors.New("task link requires thread id, title, and target alias")
	}
	file, err := store.Load()
	if err != nil {
		return TaskLink{}, err
	}
	now := time.Now().UTC()
	key := taskKey(threadID)
	for i := range file.Links {
		if file.Links[i].ThreadID == threadID && effectiveTaskLinkState(file.Links[i], now) == "active" {
			file.Links[i].Title = title
			file.Links[i].ProjectName = projectName
			file.Links[i].TargetAlias = targetAlias
			file.Links[i].UpdatedAt = now
			file.Links[i].ExpiresAt = now.Add(24 * time.Hour)
			if err := store.saveUnlocked(file); err != nil {
				return TaskLink{}, err
			}
			return file.Links[i], nil
		}
	}
	id, err := randomLinkID()
	if err != nil {
		return TaskLink{}, err
	}
	link := TaskLink{ID: id, TaskKey: key, ThreadID: threadID, Title: title, ProjectName: projectName, TargetAlias: targetAlias, LinkState: "active", TurnState: "idle", TurnOwner: "none", ActionRequired: "none", CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(24 * time.Hour), Phase: "已连接", Detail: "等待飞书指令"}
	file.Links = append(file.Links, link)
	if err := store.saveUnlocked(file); err != nil {
		return TaskLink{}, err
	}
	return link, nil
}

func (store TaskLinkStore) Update(taskKey string, patch func(*TaskLink)) (TaskLink, error) {
	var result TaskLink
	err := privatestore.WithFileLock(store.path+".lock", func() error { var err error; result, err = store.updateUnlocked(taskKey, patch); return err })
	return result, err
}

// Release applies the single task-link release transition used by both the
// desktop client and Feishu card callbacks. A linked card is marked pending
// before the local state is committed so a transient Feishu failure can be
// reconciled by the long-lived bridge.
func (store TaskLinkStore) Release(taskKey string) (TaskLink, error) {
	return store.Update(taskKey, func(link *TaskLink) {
		link.LinkState = "released"
		link.TurnState = "idle"
		link.TurnOwner = "none"
		link.ActionRequired = "none"
		link.Phase = "已断开"
		link.Detail = "任务连接已释放。"
		if TaskLinkCardMessageID(*link) != "" {
			link.SetExtraValue("cardSyncPending", true)
		}
	})
}

func (store TaskLinkStore) ReleaseByID(id string) (TaskLink, error) {
	return store.UpdateByID(id, func(link *TaskLink) {
		link.LinkState = "released"
		link.TurnState = "idle"
		link.TurnOwner = "none"
		link.ActionRequired = "none"
		link.Phase = "已断开"
		link.Detail = "任务连接已释放。"
		if TaskLinkCardMessageID(*link) != "" {
			link.SetExtraValue("cardSyncPending", true)
		}
	})
}

// UpdateByID updates an exact historical record, including released links.
// This is intentionally narrower than Update, whose mutation contract only
// permits the effective active link for a task key.
func (store TaskLinkStore) UpdateByID(id string, patch func(*TaskLink)) (TaskLink, error) {
	var result TaskLink
	err := privatestore.WithFileLock(store.path+".lock", func() error {
		file, err := store.Load()
		if err != nil {
			return err
		}
		for index := range file.Links {
			if file.Links[index].ID != id {
				continue
			}
			patch(&file.Links[index])
			file.Links[index].UpdatedAt = time.Now().UTC()
			result = file.Links[index]
			return store.saveUnlocked(file)
		}
		return errors.New("task link record not found")
	})
	return result, err
}
func (store TaskLinkStore) updateUnlocked(taskKey string, patch func(*TaskLink)) (TaskLink, error) {
	file, err := store.Load()
	if err != nil {
		return TaskLink{}, err
	}
	now := time.Now().UTC()
	best := -1
	for i := range file.Links {
		if file.Links[i].TaskKey == taskKey && effectiveTaskLinkState(file.Links[i], now) == "active" {
			best = i
		}
	}
	if best < 0 {
		return TaskLink{}, fmt.Errorf("task link not found")
	}
	patch(&file.Links[best])
	file.Links[best].UpdatedAt = now
	if err := store.saveUnlocked(file); err != nil {
		return TaskLink{}, err
	}
	return file.Links[best], nil
}

func (store TaskLinkStore) FindByMessage(messageID string) (TaskLink, bool, error) {
	file, err := store.Load()
	if err != nil {
		return TaskLink{}, false, err
	}
	now := time.Now().UTC()
	for index := len(file.Links) - 1; index >= 0; index-- {
		link := file.Links[index]
		if effectiveTaskLinkState(link, now) != "active" {
			continue
		}
		if link.RootMessageID == messageID || containsString(link.MessageIDs, messageID) {
			return link, true, nil
		}
	}
	return TaskLink{}, false, nil
}

func (store TaskLinkStore) FindByID(id string) (TaskLink, bool, error) {
	file, err := store.Load()
	if err != nil {
		return TaskLink{}, false, err
	}
	for index := len(file.Links) - 1; index >= 0; index-- {
		if file.Links[index].ID == id {
			return file.Links[index], true, nil
		}
	}
	return TaskLink{}, false, nil
}

func (store TaskLinkStore) FindByTaskKey(taskKey string) (TaskLink, bool, error) {
	file, err := store.Load()
	if err != nil {
		return TaskLink{}, false, err
	}
	now := time.Now().UTC()
	for index := len(file.Links) - 1; index >= 0; index-- {
		if file.Links[index].TaskKey == taskKey && effectiveTaskLinkState(file.Links[index], now) == "active" {
			return file.Links[index], true, nil
		}
	}
	return TaskLink{}, false, nil
}

// FindAnyByTaskKey returns the same effective record projected by PublicLinks,
// including released or expired links. Mutations remain limited to active links.
func (store TaskLinkStore) FindAnyByTaskKey(taskKey string) (TaskLink, bool, error) {
	file, err := store.Load()
	if err != nil {
		return TaskLink{}, false, err
	}
	now := time.Now().UTC()
	best := -1
	for index := range file.Links {
		link := file.Links[index]
		if link.TaskKey != taskKey {
			continue
		}
		if best < 0 {
			best = index
			continue
		}
		current := file.Links[best]
		linkActive := effectiveTaskLinkState(link, now) == "active"
		currentActive := effectiveTaskLinkState(current, now) == "active"
		if (linkActive && !currentActive) || (linkActive == currentActive && link.UpdatedAt.After(current.UpdatedAt)) {
			best = index
		}
	}
	if best < 0 {
		return TaskLink{}, false, nil
	}
	return file.Links[best], true, nil
}

func effectiveTaskLinkState(link TaskLink, now time.Time) string {
	if link.LinkState != "active" {
		return link.LinkState
	}
	if !link.ExpiresAt.IsZero() && !link.ExpiresAt.After(now) {
		return "expired"
	}
	return "active"
}

func PublicLinks(links []TaskLink) []PublicTaskLink {
	now := time.Now().UTC()
	byTaskKey := map[string]TaskLink{}
	for _, link := range links {
		existing, found := byTaskKey[link.TaskKey]
		linkActive := effectiveTaskLinkState(link, now) == "active"
		existingActive := found && effectiveTaskLinkState(existing, now) == "active"
		if !found || (linkActive && !existingActive) || (linkActive == existingActive && link.UpdatedAt.After(existing.UpdatedAt)) {
			byTaskKey[link.TaskKey] = link
		}
	}
	result := make([]PublicTaskLink, 0, len(byTaskKey))
	for _, link := range byTaskKey {
		result = append(result, projectTaskLink(link, now))
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].UpdatedAt > result[j].UpdatedAt })
	return result
}

func projectTaskLink(link TaskLink, now time.Time) PublicTaskLink {
	state := effectiveTaskLinkState(link, now)
	remaining := 0
	if state == "active" {
		remaining = max(0, int(link.ExpiresAt.Sub(now).Seconds()))
	}
	controls := map[string]bool{"canSend": state == "active" && link.TurnState != "running", "canSteer": false, "canInterrupt": state == "active" && link.TurnState == "running", "canAnswer": state == "active" && link.TurnState == "waiting_input", "canRelease": state == "active", "acceptsAttachments": state == "active"}
	return PublicTaskLink{TaskKey: link.TaskKey, Title: link.Title, ProjectName: link.ProjectName, TargetAlias: link.TargetAlias, LinkState: state, TurnState: link.TurnState, TurnOwner: link.TurnOwner, ActionRequired: link.ActionRequired, Controls: controls, CreatedAt: link.CreatedAt.Format(time.RFC3339), UpdatedAt: link.UpdatedAt.Format(time.RFC3339), ExpiresAt: link.ExpiresAt.Format(time.RFC3339), RemainingSeconds: remaining, DetailAvailable: link.Detail != "", Phase: link.Phase, DetailSummary: link.Detail}
}
