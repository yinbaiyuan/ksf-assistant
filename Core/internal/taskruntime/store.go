package taskruntime

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const recordDirectory = ".agents/runtime-data/ksfassistant/tasks-v1"

type Options struct {
	KeyDirectory string
	Now          func() time.Time
}

type Store struct {
	root         string
	keyDirectory string
	keyAnchor    string
	now          func() time.Time
}

func Open(root string, options Options) (*Store, error) {
	if !validText(root, 4096) || strings.TrimSpace(root) == "" {
		return nil, fail("invalid_root", "an explicit KSF workspace root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fail("invalid_root", "cannot resolve KSF workspace root")
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, fail("invalid_root", "KSF workspace root does not exist")
	}
	for _, name := range []string{"AGENTS.md", ".agents"} {
		path, err := safePath(resolved, name, false)
		if err != nil {
			return nil, fail("invalid_root", "KSF workspace entry is unsafe")
		}
		info, err := os.Lstat(path)
		if err != nil || (name == "AGENTS.md" && !info.Mode().IsRegular()) || (name == ".agents" && !info.IsDir()) {
			return nil, fail("invalid_root", "KSF workspace requires AGENTS.md and .agents")
		}
	}
	keyDirectory := options.KeyDirectory
	if keyDirectory == "" {
		config, err := os.UserConfigDir()
		if err != nil {
			return nil, fail("key_unavailable", "cannot locate per-user application support")
		}
		keyDirectory = filepath.Join(config, "KSFAssistant", "task-runtime-v1")
	}
	keyDirectory, err = filepath.Abs(keyDirectory)
	if err != nil {
		return nil, fail("key_unavailable", "invalid private key directory")
	}
	anchor := keyDirectory
	var suffix []string
	for {
		if _, err := os.Lstat(anchor); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fail("key_unavailable", "cannot inspect private key directory")
		}
		suffix = append([]string{filepath.Base(anchor)}, suffix...)
		parent := filepath.Dir(anchor)
		if parent == anchor {
			return nil, fail("key_unavailable", "cannot resolve private key directory")
		}
		anchor = parent
	}
	anchor, err = filepath.EvalSymlinks(anchor)
	if err != nil {
		return nil, fail("key_unavailable", "cannot resolve private key directory")
	}
	keyDirectory = filepath.Join(append([]string{anchor}, suffix...)...)
	relative, err := filepath.Rel(resolved, keyDirectory)
	if err != nil || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
		return nil, fail("key_unavailable", "identity key must be outside the workspace")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Store{root: resolved, keyDirectory: keyDirectory, keyAnchor: anchor, now: now}, nil
}

func (store *Store) dataPath(name string, create bool) (string, error) {
	path, err := safePath(store.root, recordDirectory+"/"+name, create)
	if err != nil {
		return "", err
	}
	if create {
		if err := privateDirectory(filepath.Dir(path)); err != nil {
			return "", err
		}
	}
	return path, nil
}

func (store *Store) keyPath(create bool) (string, error) {
	relative, err := filepath.Rel(store.keyAnchor, filepath.Join(store.keyDirectory, "identity.key"))
	if err != nil {
		return "", fail("key_unavailable", "invalid private key directory")
	}
	path, err := safePath(store.keyAnchor, filepath.ToSlash(relative), create)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", os.ErrNotExist
		}
		return "", fail("key_unavailable", "identity key directory is missing or unsafe")
	}
	if create {
		if err := privateDirectory(filepath.Dir(path)); err != nil {
			return "", err
		}
	}
	return path, nil
}

func (store *Store) identityKey(ctx context.Context, create bool) ([]byte, error) {
	path, err := store.keyPath(create)
	if err != nil {
		return nil, fail("key_unavailable", "identity key directory is missing or unsafe")
	}
	read := func() ([]byte, error) {
		data, err := readFile(path, 32, true)
		if err != nil || len(data) != 32 {
			return nil, fail("key_unavailable", "identity key is missing, malformed or unsafe; no automatic rotation")
		}
		return data, nil
	}
	if !create {
		return read()
	}
	var key []byte
	err = withLock(ctx, filepath.Join(filepath.Dir(path), "identity.lock"), func() error {
		if _, err := os.Lstat(path); err == nil {
			key, err = read()
			return err
		} else if !errors.Is(err, os.ErrNotExist) {
			return fail("key_unavailable", "identity key cannot be inspected")
		}
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return fail("key_unavailable", "cannot generate a private identity key")
		}
		return atomicWrite(path, key)
	})
	return key, err
}

func validIdentity(host, identity string) bool {
	return host == "codex" && len(identity) >= 8 && validText(identity, 256) && strings.TrimSpace(identity) == identity
}

func validTaskID(identity string) bool {
	return strings.HasPrefix(identity, "task_") && hexDigest(strings.TrimPrefix(identity, "task_"))
}

func taskID(key []byte, host, identity string) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(Protocol + "\x00" + host + "\x00" + identity))
	return "task_" + hex.EncodeToString(mac.Sum(nil))
}

func (store *Store) TaskID(ctx context.Context, host, identity string) (string, error) {
	if !validIdentity(host, identity) {
		return "", fail("invalid_identity", "a supported host and private thread identity are required")
	}
	key, err := store.identityKey(ctx, false)
	if err != nil {
		return "", err
	}
	return taskID(key, host, identity), nil
}

func validateState(state ReportedState) (string, error) {
	switch state.Scope {
	case "ksf", "unresolved":
		if state.ProjectCard != "" {
			return "", fail("route_invalid", "only project scope accepts project_card")
		}
	case "project":
		if !relativePath(state.ProjectCard) {
			return "", fail("route_invalid", "project scope requires an explicit project_card")
		}
	default:
		return "", fail("invalid_request", "scope must be explicitly ksf, project or unresolved")
	}
	switch state.ReportedStatus {
	case "unknown", "running", "waiting", "blocked", "completed":
	default:
		return "", fail("invalid_request", "invalid reported_status")
	}
	if state.Progress != nil && (!validText(state.Progress.Summary, 512) || (state.Progress.Percent != nil && (*state.Progress.Percent < 0 || *state.Progress.Percent > 100))) {
		return "", fail("invalid_request", "progress summary or percent exceeds its bound")
	}
	if len(state.Receipt) == 0 {
		if state.Scope != "unresolved" {
			return "", fail("route_invalid", "only unresolved tasks may omit a receipt")
		}
		return "", nil
	}
	receipt, err := ParseReceipt(state.Receipt)
	if err != nil {
		return "", err
	}
	if state.Scope == "project" {
		for _, project := range receipt.ProjectRoots() {
			if project == state.ProjectCard {
				return project, nil
			}
		}
		return "", fail("route_invalid", "explicit project_card is not a verified context root")
	}
	return "", nil
}

func validEventID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' || char == '.' || char == ':') {
			return false
		}
	}
	return true
}

func payloadDigest(host, event string, revision uint64, state ReportedState) string {
	return digest(canonicalJSON(struct {
		Host             string        `json:"host"`
		EventID          string        `json:"event_id"`
		ExpectedRevision uint64        `json:"expected_revision"`
		State            ReportedState `json:"state"`
	}{host, event, revision, state}))
}

func (store *Store) Report(ctx context.Context, request Request) (View, []Change, bool, error) {
	if request.Protocol != Protocol || request.Version != 1 {
		return View{}, nil, false, fail("unsupported_protocol", "expected task runtime protocol v1")
	}
	if !validIdentity(request.Host, request.ThreadID) {
		return View{}, nil, false, fail("invalid_identity", "a supported host and private thread identity are required")
	}
	if !validEventID(request.EventID) || request.ExpectedRevision == nil || request.State == nil {
		return View{}, nil, false, fail("invalid_request", "report requires event_id, expected_revision and state")
	}
	project, err := validateState(*request.State)
	if err != nil {
		return View{}, nil, false, err
	}
	if containsIdentity(request.State, request.ThreadID) || strings.Contains(request.EventID, request.ThreadID) {
		return View{}, nil, false, fail("invalid_identity", "raw identity may not appear in persisted report content")
	}
	if len(canonicalJSON(request)) > MaxRequestBytes {
		return View{}, nil, false, fail("limit_exceeded", "report exceeds request size limit")
	}
	key, err := store.identityKey(ctx, false)
	if err != nil {
		ids, scanErr := store.recordIDs()
		if scanErr != nil {
			return View{}, nil, false, scanErr
		}
		if len(ids) != 0 {
			return View{}, nil, false, fail("key_unavailable", "existing task records require the original private identity key; refusing initialization")
		}
		key, err = store.identityKey(ctx, true)
	}
	if err != nil {
		return View{}, nil, false, err
	}
	identity := taskID(key, request.Host, request.ThreadID)
	path, err := store.dataPath(identity+".json", true)
	if err != nil {
		return View{}, nil, false, err
	}
	payload := payloadDigest(request.Host, request.EventID, *request.ExpectedRevision, *request.State)
	var snapshot Snapshot
	var history []Change
	replayed := false
	err = withLock(ctx, strings.TrimSuffix(path, ".json")+".lock", func() error {
		record, err := store.load(identity)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		for index, change := range record.History {
			if change.EventID == request.EventID {
				if change.PayloadSHA256 != payload {
					return fail("event_conflict", "event ID already has different content or revision")
				}
				snapshot, history, replayed = change.Snapshot, record.History[:index+1], true
				return nil
			}
		}
		if record.Snapshot.Revision != *request.ExpectedRevision {
			return fail("revision_conflict", "expected revision does not match current task revision")
		}
		if len(record.History) >= maxHistory {
			return fail("limit_exceeded", "task history is full; no history was discarded")
		}
		commit := func() error {
			if record.Snapshot.Revision == 0 {
				ids, err := store.recordIDs()
				if err != nil {
					return err
				}
				if len(ids) >= maxRecords {
					return fail("limit_exceeded", "workspace task record limit reached")
				}
			}
			if len(request.State.Receipt) != 0 {
				if _, err := store.verify(ctx, request.State.Receipt); err != nil {
					return err
				}
			}
			now := store.now().UTC()
			created := record.Snapshot.CreatedAt
			if created.IsZero() {
				created = now
			}
			if now.Before(record.Snapshot.ReportedAt) {
				return fail("invalid_request", "clock moved backwards; report not committed")
			}
			state := *request.State
			if len(state.Receipt) != 0 {
				state.Receipt = canonicalJSON(state.Receipt)
			}
			snapshot = Snapshot{TaskID: identity, Host: request.Host, Revision: record.Snapshot.Revision + 1, WorkspaceDigest: workspaceDigest(store.root), CreatedAt: created, ReportedAt: now, State: state, ProjectCard: project}
			record = Record{Protocol: Protocol, Version: 1, Snapshot: snapshot, History: append(record.History, Change{EventID: request.EventID, PayloadSHA256: payload, Snapshot: snapshot})}
			data, err := json.Marshal(record)
			if err != nil || len(data)+1 > maxRecordBytes {
				return fail("limit_exceeded", "task record exceeds size limit; no history was discarded")
			}
			if ctx.Err() != nil {
				return fail("busy", "task operation cancelled before commit")
			}
			if err := atomicWrite(path, append(data, '\n')); err != nil {
				return err
			}
			history = record.History
			return nil
		}
		if record.Snapshot.Revision == 0 {
			return withLock(ctx, filepath.Join(filepath.Dir(path), ".index.lock"), commit)
		}
		return commit()
	})
	if err != nil {
		return View{}, nil, false, err
	}
	view := store.view(ctx, snapshot, !replayed)
	return view, history, replayed, nil
}

func (store *Store) load(identity string) (Record, error) {
	var record Record
	if !validTaskID(identity) {
		return record, fail("invalid_identity", "invalid public task ID")
	}
	path, err := store.dataPath(identity+".json", false)
	if err != nil {
		return record, err
	}
	data, err := readFile(path, maxRecordBytes, true)
	if err != nil {
		return record, err
	}
	if err := strictDecode(data, &record); err != nil {
		return Record{}, fail("corrupt_record", "task record has unknown fields or invalid JSON; not overwritten")
	}
	if record.Protocol != Protocol || record.Version != 1 {
		return Record{}, fail("unsupported_protocol", "task record uses an unsupported protocol or version; not overwritten")
	}
	if len(record.History) == 0 || len(record.History) > maxHistory || int(record.Snapshot.Revision) != len(record.History) {
		return Record{}, fail("corrupt_record", "task revision and history are inconsistent")
	}
	seen := map[string]bool{}
	var previous time.Time
	for index, change := range record.History {
		snapshot := change.Snapshot
		project, stateErr := validateState(snapshot.State)
		if stateErr != nil || project != snapshot.ProjectCard || snapshot.Host != "codex" || snapshot.TaskID != identity || snapshot.WorkspaceDigest != workspaceDigest(store.root) || snapshot.Revision != uint64(index+1) || snapshot.CreatedAt.IsZero() || !snapshot.CreatedAt.Equal(record.Snapshot.CreatedAt) || snapshot.ReportedAt.Before(snapshot.CreatedAt) || snapshot.ReportedAt.Before(previous) || !validEventID(change.EventID) || seen[change.EventID] || change.PayloadSHA256 != payloadDigest(snapshot.Host, change.EventID, uint64(index), snapshot.State) {
			return Record{}, fail("corrupt_record", "task history, workspace binding or payload digest is invalid")
		}
		seen[change.EventID] = true
		previous = snapshot.ReportedAt
	}
	if !bytes.Equal(canonicalJSON(record.Snapshot), canonicalJSON(record.History[len(record.History)-1].Snapshot)) {
		return Record{}, fail("corrupt_record", "task snapshot does not match committed history")
	}
	return record, nil
}

func (store *Store) view(ctx context.Context, snapshot Snapshot, verified bool) View {
	result := View{Snapshot: snapshot, ReportFreshness: "stale", RouteFreshness: "unverified"}
	age := store.now().Sub(snapshot.ReportedAt)
	if age >= 0 && age <= 15*time.Minute {
		result.ReportFreshness = "recent"
	}
	if len(snapshot.State.Receipt) != 0 {
		result.RouteFreshness = "current"
		if !verified {
			_, err := store.verify(ctx, snapshot.State.Receipt)
			result.RouteFreshness = routeFreshness(err)
		}
	}
	return result
}

func (store *Store) Get(ctx context.Context, identity string) (View, []Change, error) {
	record, err := store.load(identity)
	if errors.Is(err, os.ErrNotExist) {
		return View{}, nil, fail("not_found", "task record does not exist")
	}
	if err != nil {
		return View{}, nil, err
	}
	return store.view(ctx, record.Snapshot, false), record.History, nil
}

func (store *Store) recordIDs() ([]string, error) {
	path, err := store.dataPath("probe", false)
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	directory, err := os.Open(filepath.Dir(path))
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	entries, err := directory.ReadDir(maxEntries + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) > maxEntries {
		return nil, fail("limit_exceeded", "runtime directory scan limit exceeded")
	}
	ids := []string{}
	for _, entry := range entries {
		name := entry.Name()
		if name == ".index.lock" {
			continue
		}
		if strings.HasPrefix(name, ".task-") && strings.HasSuffix(name, ".tmp") {
			continue
		}
		if strings.HasSuffix(name, ".lock") && validTaskID(strings.TrimSuffix(name, ".lock")) {
			continue
		}
		if !strings.HasSuffix(name, ".json") || !validTaskID(strings.TrimSuffix(name, ".json")) {
			return nil, fail("corrupt_record", "runtime directory contains an unknown entry")
		}
		ids = append(ids, strings.TrimSuffix(name, ".json"))
	}
	if len(ids) > maxRecords {
		return nil, fail("limit_exceeded", "runtime record limit exceeded")
	}
	sort.Strings(ids)
	return ids, nil
}

func (store *Store) List(ctx context.Context, limit int, after string) ([]View, string, error) {
	if limit == 0 {
		limit = 25
	}
	if limit < 1 || limit > 100 || (after != "" && !validTaskID(after)) {
		return nil, "", fail("invalid_request", "invalid list limit or cursor")
	}
	ids, err := store.recordIDs()
	if err != nil {
		return nil, "", err
	}
	result := []View{}
	next := ""
	for _, identity := range ids {
		if ctx.Err() != nil {
			return nil, "", fail("busy", "list cancelled or timed out")
		}
		if identity <= after {
			continue
		}
		if len(result) == limit {
			next = result[len(result)-1].TaskID
			break
		}
		view, _, err := store.Get(ctx, identity)
		if err != nil {
			return nil, "", err
		}
		result = append(result, view)
	}
	return result, next, nil
}

func (store *Store) Doctor(ctx context.Context) Doctor {
	result := Doctor{Store: "available", Key: "available", Verifier: "available", Diagnostics: []Diagnostic{}}
	if _, err := store.identityKey(ctx, false); err != nil {
		result.Key = "unavailable"
		path, pathErr := store.keyPath(false)
		if errors.Is(pathErr, os.ErrNotExist) {
			result.Key = "missing"
		} else if pathErr == nil {
			if _, statErr := os.Lstat(path); errors.Is(statErr, os.ErrNotExist) {
				result.Key = "missing"
			}
		}
	}
	if _, err := store.verifierPath(); err != nil {
		result.Verifier = "unavailable"
	}
	ids, err := store.recordIDs()
	if err != nil {
		result.Store = "unavailable"
		result.Diagnostics = append(result.Diagnostics, Diagnostic{Code: errorCode(err)})
		return result
	}
	path, pathErr := store.dataPath("probe", false)
	if errors.Is(pathErr, os.ErrNotExist) {
		result.Store = "missing"
	} else if pathErr == nil {
		if _, statErr := os.Lstat(filepath.Dir(path)); errors.Is(statErr, os.ErrNotExist) {
			result.Store = "missing"
		}
	}
	for _, identity := range ids {
		if ctx.Err() != nil {
			result.Store = "unavailable"
			result.Diagnostics = append(result.Diagnostics, Diagnostic{Code: "busy"})
			return result
		}
		if _, err := store.load(identity); err != nil {
			result.Diagnostics = append(result.Diagnostics, Diagnostic{TaskID: identity, Code: errorCode(err)})
		}
	}
	result.Records = len(ids)
	if len(result.Diagnostics) > 0 {
		result.Store = "degraded"
	}
	return result
}

func errorCode(err error) string {
	var failure *Error
	if errors.As(err, &failure) {
		return failure.Code
	}
	return "io_error"
}
