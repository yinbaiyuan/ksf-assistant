package feishu

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	HostContextFilename = "codexassistant-host-context-v1.json"
	HostContextProtocol = "codexassistant-host-context-v1"

	KSFNotConfigured = "not_configured"
	KSFReady         = "ready"
	KSFInvalid       = "invalid"
)

type HostContext struct {
	Protocol      string         `json:"protocol"`
	SchemaVersion int            `json:"schemaVersion"`
	UpdatedAt     time.Time      `json:"updatedAt"`
	KSF           KSFHostContext `json:"ksf"`
}

type KSFHostContext struct {
	State string `json:"state"`
	Root  string `json:"root,omitempty"`
}

type HostContextStore struct{ path string }

func NewHostContextStore(dataRoot string) HostContextStore {
	return HostContextStore{path: filepath.Join(dataRoot, HostContextFilename)}
}

func (store HostContextStore) Path() string { return store.path }

func (store HostContextStore) Load() (HostContext, error) {
	var value HostContext
	missing, err := readPrivateJSON(store.path, &value)
	if missing {
		return HostContext{}, errors.New("CodexAssistant host context is unavailable")
	}
	if err != nil {
		return HostContext{}, err
	}
	if err := value.Validate(); err != nil {
		return HostContext{}, err
	}
	if value.KSF.State == KSFReady {
		canonical, err := filepath.EvalSymlinks(value.KSF.Root)
		if err != nil {
			return HostContext{}, errors.New("KSF host context is invalid")
		}
		info, err := os.Stat(canonical)
		if err != nil || !info.IsDir() {
			return HostContext{}, errors.New("KSF host context is invalid")
		}
		value.KSF.Root = filepath.Clean(canonical)
	}
	return value, nil
}

func (store HostContextStore) SaveKSFRoot(root string) (HostContext, error) {
	context := HostContext{
		Protocol:      HostContextProtocol,
		SchemaVersion: 1,
		UpdatedAt:     time.Now().UTC(),
		KSF:           KSFHostContext{State: KSFNotConfigured},
	}
	root = strings.TrimSpace(root)
	if root != "" {
		resolved, err := filepath.Abs(root)
		if err != nil {
			context.KSF.State = KSFInvalid
		} else if canonical, err := filepath.EvalSymlinks(resolved); err != nil {
			context.KSF.State = KSFInvalid
		} else if info, err := os.Stat(canonical); err != nil || !info.IsDir() {
			context.KSF.State = KSFInvalid
		} else {
			context.KSF.State = KSFReady
			context.KSF.Root = filepath.Clean(canonical)
		}
	}
	if err := context.Validate(); err != nil {
		return HostContext{}, err
	}
	if err := writePrivateJSON(store.path, context); err != nil {
		return HostContext{}, err
	}
	return context, nil
}

func (context HostContext) Validate() error {
	if context.Protocol != HostContextProtocol || context.SchemaVersion != 1 || context.UpdatedAt.IsZero() {
		return errors.New("invalid CodexAssistant host context")
	}
	switch context.KSF.State {
	case KSFNotConfigured, KSFInvalid:
		if context.KSF.Root != "" {
			return errors.New("inactive KSF host context contains a root")
		}
	case KSFReady:
		if !filepath.IsAbs(context.KSF.Root) || strings.TrimSpace(context.KSF.Root) == "" {
			return errors.New("ready KSF host context requires an absolute root")
		}
	default:
		return errors.New("invalid KSF host context state")
	}
	return nil
}
