package integration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ksfassistant/core/internal/privatestore"
)

const recentEventReceipts = 64

func terminalInboxEvent(record inboxEvent) bool {
	return record.State == "completed" || record.State == "failed" || record.State == "outcome_unknown"
}

func eventNeedsReview(record inboxEvent) bool {
	return record.State == "outcome_unknown" || record.LastError == "pre_execution_retries_exhausted"
}

func (inbox *eventInbox) receiptPath(id string) string {
	digest := sha256.Sum256([]byte(id))
	name := hex.EncodeToString(digest[:])
	return filepath.Join(inbox.receiptRoot, name[:2], name+".json")
}

func (inbox *eventInbox) receipt(id string) (inboxEvent, bool, error) {
	var record inboxEvent
	missing, err := privatestore.ReadJSON(inbox.receiptPath(id), &record)
	if err != nil || missing {
		return record, false, err
	}
	if record.Event.ID != id || !terminalInboxEvent(record) {
		return record, false, errors.New("invalid integration receipt")
	}
	return record, true, nil
}

func (inbox *eventInbox) archive(record inboxEvent) error {
	prior, exists, err := inbox.receipt(record.Event.ID)
	if err != nil {
		return err
	}
	if exists {
		if prior.Digest != record.Digest || prior.State != record.State {
			return errors.New("integration receipt conflict")
		}
	} else if err := privatestore.WriteJSON(inbox.receiptPath(record.Event.ID), record); err != nil {
		return err
	}
	if eventNeedsReview(record) {
		inbox.unknownReceipts[record.Event.ID] = true
	}
	return nil
}

func (inbox *eventInbox) compact(file eventInboxFile) (eventInboxFile, error) {
	file.Events = append([]inboxEvent(nil), file.Events...)
	terminal := 0
	for _, record := range file.Events {
		if terminalInboxEvent(record) {
			terminal++
		}
	}
	retained := make([]inboxEvent, 0, len(file.Events))
	for _, record := range file.Events {
		if terminal > recentEventReceipts && terminalInboxEvent(record) {
			if err := inbox.archive(record); err != nil {
				return file, err
			}
			terminal--
			continue
		}
		retained = append(retained, record)
	}
	file.Events = retained
	for {
		data, err := json.MarshalIndent(file, "", "  ")
		if err != nil {
			return file, err
		}
		if terminal <= recentEventReceipts && len(data) <= eventInboxMaxBytes {
			return file, nil
		}
		index := -1
		for candidate, record := range file.Events {
			if terminalInboxEvent(record) {
				index = candidate
				break
			}
		}
		if index < 0 {
			return file, errors.New("integration inbox capacity exceeded")
		}
		if err := inbox.archive(file.Events[index]); err != nil {
			return file, err
		}
		file.Events = append(file.Events[:index], file.Events[index+1:]...)
		terminal--
	}
}

func (inbox *eventInbox) scanReceipts(now time.Time) error {
	return filepath.WalkDir(inbox.receiptRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if errors.Is(walkErr, os.ErrNotExist) {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("unsafe integration receipt path")
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			return nil
		}
		var record inboxEvent
		if missing, err := privatestore.ReadJSON(path, &record); err != nil || missing {
			return err
		}
		if path != inbox.receiptPath(record.Event.ID) || !terminalInboxEvent(record) {
			return errors.New("invalid integration receipt")
		}
		if expiredInboxReceipt(record, now) {
			if err := os.Remove(path); err != nil {
				return err
			}
			delete(inbox.unknownReceipts, record.Event.ID)
		} else if eventNeedsReview(record) {
			inbox.unknownReceipts[record.Event.ID] = true
		}
		return nil
	})
}
