package capabilitypolicy

import (
	"errors"
	"os"
	"path/filepath"
)

// Logout persists independently of OAuth tokens, including the bot identity.
// An absent marker is compatible with installations predating explicit logout.
const SessionFilename = "feishu-session-signed-out-v1"

var ErrSignedOut = errors.New("feishu_login_required")

func CheckSession(root string) error {
	_, err := os.Lstat(filepath.Join(root, SessionFilename))
	if os.IsNotExist(err) {
		return nil
	}
	return ErrSignedOut
}
func SignOut(root string) error {
	if !filepath.IsAbs(root) {
		return errors.New("feishu_session_root_invalid")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(root, SessionFilename), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if os.IsExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// SignIn is called only after a new desktop OAuth flow is verified.
func SignIn(root string) error {
	err := os.Remove(filepath.Join(root, SessionFilename))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
