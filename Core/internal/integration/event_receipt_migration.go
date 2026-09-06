package integration

import (
	"encoding/json"
	"errors"
	"path/filepath"

	"ksfassistant/core/internal/privatestore"
)

type eventReceiptMigration struct {
	Version int `json:"version"`
}

func (inbox *eventInbox) prepareReceiptMigration() (bool, error) {
	var marker eventReceiptMigration
	missing, err := privatestore.ReadJSON(inbox.receiptRoot+"-migration.json", &marker)
	if err != nil {
		return false, err
	}
	if !missing {
		if marker.Version != 1 {
			return false, errors.New("unsupported event receipt migration")
		}
		return false, nil
	}
	backup := filepath.Join(filepath.Dir(inbox.path), "integration-event-backups", "inbox-v1.json")
	var original json.RawMessage
	missing, err = privatestore.ReadJSON(backup, &original)
	if err != nil {
		return false, err
	}
	if missing {
		if _, err := privatestore.ReadJSON(inbox.path, &original); err != nil {
			return false, err
		}
		if err := privatestore.WriteJSON(backup, original); err != nil {
			return false, err
		}
	}
	return true, nil
}
