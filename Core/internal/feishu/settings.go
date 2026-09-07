package feishu

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
)

const (
	SettingsFilename = "feishu-settings-v1.json"
	SetupFilename    = "feishu-setup-v1.json"

	SetupNotStarted           = "not_started"
	SetupAppPending           = "app_pending"
	SetupAppConfigured        = "app_configured"
	SetupAuthorizationPending = "authorization_pending"
	SetupPlatformPending      = "platform_pending"
	SetupVerifying            = "verifying"
	SetupReady                = "ready"
	SetupFailed               = "failed"

	SetupModeNew      = "new"
	SetupModeExisting = "existing"
)

const maximumPrivateJSONBytes = 1 << 20

var ErrSettingsConflict = errors.New("feishu_settings_changed: 设置已变化，请重新检查后确认")

type Switch struct {
	Enabled bool `json:"enabled"`
}

type DryRunSwitch struct {
	Enabled bool `json:"enabled"`
	DryRun  bool `json:"dryRun"`
}

type CodexSettings struct {
	DefaultThreadTitle string `json:"defaultThreadTitle"`
}

type Settings struct {
	Version        int           `json:"version"`
	Profile        string        `json:"profile"`
	Group          Switch        `json:"group"`
	MailEvents     Switch        `json:"mailEvents"`
	Outbound       DryRunSwitch  `json:"outbound"`
	Directory      Switch        `json:"directory"`
	GroupDirectory Switch        `json:"groupDirectory"`
	Actionbox      DryRunSwitch  `json:"actionbox"`
	Codex          CodexSettings `json:"codex"`
}

func DefaultSettings() Settings {
	return Settings{
		Version:   2,
		Outbound:  DryRunSwitch{Enabled: true},
		Actionbox: DryRunSwitch{Enabled: true},
		Codex:     CodexSettings{DefaultThreadTitle: "飞书默认对话"},
	}
}

func (settings Settings) Validate() error {
	if settings.Version != 1 && settings.Version != 2 {
		return fmt.Errorf("unsupported Feishu settings version %d", settings.Version)
	}
	if invalidOptionalText(settings.Profile, 80) {
		return errors.New("invalid legacy Feishu event profile")
	}
	if invalidText(settings.Codex.DefaultThreadTitle, 200) {
		return errors.New("invalid Codex default thread title")
	}
	return nil
}

type SetupState struct {
	Version         int    `json:"version"`
	Stage           string `json:"stage"`
	Mode            string `json:"mode,omitempty"`
	VerificationURL string `json:"verificationURL,omitempty"`
	UserCode        string `json:"userCode,omitempty"`
	LastError       string `json:"lastError,omitempty"`
	ReadyToActivate bool   `json:"readyToActivate,omitempty"`
}

func DefaultSetupState() SetupState {
	return SetupState{Version: 1, Stage: SetupNotStarted}
}

func (state SetupState) Validate() error {
	if state.Version != 1 {
		return fmt.Errorf("unsupported Feishu setup version %d", state.Version)
	}
	validStages := map[string]bool{
		SetupNotStarted: true, SetupAppPending: true, SetupAppConfigured: true,
		SetupAuthorizationPending: true, SetupPlatformPending: true,
		SetupVerifying: true, SetupReady: true, SetupFailed: true,
	}
	if !validStages[state.Stage] {
		return errors.New("unsupported Feishu setup stage")
	}
	if state.Mode != "" && state.Mode != SetupModeNew && state.Mode != SetupModeExisting {
		return errors.New("unsupported Feishu setup mode")
	}
	if state.VerificationURL != "" {
		parsed, err := url.Parse(state.VerificationURL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || len(state.VerificationURL) > 4096 {
			return errors.New("invalid Feishu verification URL")
		}
	}
	if invalidOptionalText(state.UserCode, 128) || invalidOptionalText(state.LastError, 1000) {
		return errors.New("invalid Feishu setup text")
	}
	return nil
}

type SettingsStore struct{ path string }
type SetupStore struct{ path string }

func NewSettingsStore(dataRoot string) SettingsStore {
	return SettingsStore{path: filepath.Join(dataRoot, SettingsFilename)}
}

func NewSetupStore(dataRoot string) SetupStore {
	return SetupStore{path: filepath.Join(dataRoot, SetupFilename)}
}

func (store SettingsStore) Load() (Settings, error) {
	settings := DefaultSettings()
	_, err := readKnownStoreJSON(store.path, &settings)
	if err != nil {
		return Settings{}, err
	}
	if err := settings.Validate(); err != nil {
		return Settings{}, err
	}
	if settings.Version == 1 {
		if err := store.migrateLegacy(); err != nil {
			return Settings{}, err
		}
		return store.Load()
	}
	settings.Outbound = DryRunSwitch{Enabled: true}
	settings.Actionbox = DryRunSwitch{Enabled: true}
	settings.Directory = Switch{}
	settings.GroupDirectory = Switch{}
	return settings, nil
}

func (store SettingsStore) Save(settings Settings) error {
	return store.save(nil, settings)
}

func (store SettingsStore) CompareAndSwap(expected, next Settings) error {
	return store.save(&expected, next)
}

func (store SettingsStore) save(expected *Settings, settings Settings) error {
	if _, err := store.Load(); err != nil {
		return err
	}
	return withProcessFileLock(store.path+".lock", func() error {
		previous := DefaultSettings()
		stored, err := readKnownStoreJSON(store.path, &previous)
		if err != nil {
			return err
		}
		if err := previous.Validate(); err != nil {
			return err
		}
		if expected != nil && previous != *expected {
			return ErrSettingsConflict
		}
		for _, key := range []string{"docbox", "outbound", "actionbox", "directory", "groupDirectory"} {
			delete(stored, key)
		}
		settings.Version = 2
		settings.Profile = previous.Profile
		if err := settings.Validate(); err != nil {
			return err
		}
		return writeKnownStoreJSON(store.path, stored, settings)
	})
}

func (store SetupStore) Load() (SetupState, error) {
	state := DefaultSetupState()
	_, err := readKnownStoreJSON(store.path, &state)
	if err != nil {
		return SetupState{}, err
	}
	if err := state.Validate(); err != nil {
		return SetupState{}, err
	}
	return state, nil
}

func (store SetupStore) Save(state SetupState) error {
	if err := state.Validate(); err != nil {
		return err
	}
	return withProcessFileLock(store.path+".lock", func() error {
		previous := DefaultSetupState()
		stored, err := readKnownStoreJSON(store.path, &previous)
		if err != nil {
			return err
		}
		if err := previous.Validate(); err != nil {
			return err
		}
		return writeKnownStoreJSON(store.path, stored, state)
	})
}

func readKnownStoreJSON(path string, target any) (map[string]json.RawMessage, error) {
	var data json.RawMessage
	missing, err := readPrivateJSON(path, &data)
	if err != nil {
		return nil, err
	}
	if missing {
		return map[string]json.RawMessage{}, nil
	}
	var stored map[string]json.RawMessage
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, err
	}
	if stored == nil {
		return nil, errors.New("Feishu private settings must be an object")
	}
	if settings, ok := target.(*Settings); ok {
		type diskSettings Settings
		value := diskSettings(*settings)
		if err := json.Unmarshal(data, &value); err != nil {
			return nil, err
		}
		*settings = Settings(value)
	} else if err := json.Unmarshal(data, target); err != nil {
		return nil, err
	}
	return stored, nil
}

func writeKnownStoreJSON(path string, stored map[string]json.RawMessage, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var updated map[string]json.RawMessage
	if err := json.Unmarshal(data, &updated); err != nil {
		return err
	}
	if err := mergeKnownStoreFields(stored, updated, reflect.TypeOf(value)); err != nil {
		return err
	}
	return writePrivateJSON(path, stored)
}

func mergeKnownStoreFields(stored, updated map[string]json.RawMessage, model reflect.Type) error {
	for index := 0; index < model.NumField(); index++ {
		field := model.Field(index)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if field.PkgPath != "" || name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		previous := stored[name]
		for key, raw := range stored {
			if strings.EqualFold(key, name) {
				if previous == nil {
					previous = raw
				}
				delete(stored, key)
			}
		}
		replacement, exists := updated[name]
		if !exists {
			continue
		}
		if field.Type.Kind() == reflect.Struct {
			retained := map[string]json.RawMessage{}
			if len(previous) != 0 {
				if err := json.Unmarshal(previous, &retained); err != nil {
					return err
				}
			}
			if retained == nil {
				retained = map[string]json.RawMessage{}
			}
			var nested map[string]json.RawMessage
			if err := json.Unmarshal(replacement, &nested); err != nil {
				return err
			}
			if err := mergeKnownStoreFields(retained, nested, field.Type); err != nil {
				return err
			}
			var err error
			replacement, err = json.Marshal(retained)
			if err != nil {
				return err
			}
		}
		stored[name] = replacement
	}
	return nil
}

func readPrivateJSON(path string, target any) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maximumPrivateJSONBytes {
		return false, errors.New("unsafe Feishu private settings file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return false, errors.New("insecure Feishu private settings permissions")
	}
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maximumPrivateJSONBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return false, err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return false, errors.New("Feishu private settings contain trailing data")
	}
	return false, nil
}

func writePrivateJSON(path string, value any) error {
	root := filepath.Dir(path)
	if err := ensurePrivateDirectory(root); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temporary := filepath.Join(root, fmt.Sprintf(".%s.%d.tmp", filepath.Base(path), os.Getpid()))
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	clean := true
	defer func() {
		if clean {
			_ = os.Remove(temporary)
		}
	}()
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := replacePrivateFile(temporary, path); err != nil {
		return err
	}
	clean = false
	if runtime.GOOS != "windows" {
		return os.Chmod(path, 0o600)
	}
	return securePrivatePath(path, false)
}

func ensurePrivateDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("unsafe Feishu private settings directory")
	}
	if runtime.GOOS != "windows" {
		return os.Chmod(path, 0o700)
	}
	return securePrivatePath(path, true)
}

func invalidText(value string, maximum int) bool {
	return value == "" || len(value) > maximum || strings.IndexFunc(value, func(r rune) bool {
		return r < 0x20 || r == 0x7f
	}) >= 0
}

func invalidOptionalText(value string, maximum int) bool {
	return value != "" && invalidText(value, maximum)
}

func containsFold(haystack, needle string) bool {
	return bytes.Contains(bytes.ToLower([]byte(haystack)), bytes.ToLower([]byte(needle)))
}
