package usercommand

import (
	"encoding/json"
	"errors"
)

func ParseAuthStatus(data []byte, as string) (Identity, error) {
	var status struct {
		AppID      string `json:"appId"`
		Brand      string `json:"brand"`
		Identities struct {
			User struct {
				Available bool   `json:"available"`
				OpenID    string `json:"openId"`
				UserName  string `json:"userName"`
				Verified  *bool  `json:"verified"`
			} `json:"user"`
			Bot struct {
				Available bool   `json:"available"`
				Verified  *bool  `json:"verified"`
				AppName   string `json:"appName"`
			} `json:"bot"`
		} `json:"identities"`
	}
	if len(data) > 256*1024 || validateJSON(data) != nil || json.Unmarshal(data, &status) != nil || status.AppID == "" || status.Brand != "feishu" {
		return Identity{}, errors.New("user_command_identity_unavailable")
	}
	identity := Identity{AppID: status.AppID, Profile: "default", Brand: status.Brand}
	identity.ApplicationName = status.Identities.Bot.AppName
	switch as {
	case "user":
		user := status.Identities.User
		if !user.Available || user.OpenID == "" || user.Verified != nil && !*user.Verified {
			return Identity{}, errors.New("user_command_identity_unavailable")
		}
		identity.UserID, identity.UserName = user.OpenID, user.UserName
	case "bot":
		bot := status.Identities.Bot
		if !bot.Available || bot.Verified != nil && !*bot.Verified {
			return Identity{}, errors.New("user_command_identity_unavailable")
		}
	default:
		return Identity{}, errors.New("user_command_identity_required")
	}
	return identity, nil
}

func ValidateIdentity(expected, actual Identity) error {
	if expected != actual || actual.AppID == "" || actual.Profile != "default" || actual.Brand != "feishu" {
		return errors.New("user_command_identity_changed")
	}
	return nil
}
