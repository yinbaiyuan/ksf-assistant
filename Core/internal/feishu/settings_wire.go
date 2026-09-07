package feishu

import (
	"bytes"
	"encoding/json"
	"errors"
)

// Retired switches remain readable for v1 migration, but are absent from v2 writes.
func (settings Settings) MarshalJSON() ([]byte, error) {
	type legacy Settings
	if settings.Version == 1 {
		return json.Marshal(legacy(settings))
	}
	return json.Marshal(struct {
		Version    int           `json:"version"`
		Profile    string        `json:"profile"`
		Group      Switch        `json:"group"`
		MailEvents Switch        `json:"mailEvents"`
		Codex      CodexSettings `json:"codex"`
	}{settings.Version, settings.Profile, settings.Group, settings.MailEvents, settings.Codex})
}

func (settings *Settings) UnmarshalJSON(data []byte) error {
	type legacy Settings
	value := legacy(*settings)
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if value.Version == 2 {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}
		for _, key := range []string{"outbound", "actionbox", "directory", "groupDirectory"} {
			if _, ok := raw[key]; ok {
				return errors.New("configuration_action_retired")
			}
		}
	}
	*settings = Settings(value)
	if settings.Version == 2 {
		settings.Outbound = DryRunSwitch{Enabled: true}
		settings.Actionbox = DryRunSwitch{Enabled: true}
		settings.Directory, settings.GroupDirectory = Switch{}, Switch{}
	}
	return nil
}
