package integration

import (
	"errors"
	"ksfassistant/core/internal/feishuprotocol"
)

// EventReceiptState never dispatches; a missing receipt alone is the only case
// in which the delivery layer may resume the original event identity.
func (r *Runtime) EventReceiptState(event feishuprotocol.Event) (string, error) {
	_, _, digest, err := decodeEvent(event)
	if err != nil {
		return "", err
	}
	r.inbox.mu.Lock()
	defer r.inbox.mu.Unlock()
	check := func(v inboxEvent) (string, error) {
		if v.Digest != digest {
			return "", errors.New("integration event ID conflict")
		}
		if v.State == "outcome_unknown" {
			return "outcome_unknown", nil
		}
		return "accepted", nil
	}
	for _, v := range r.inbox.file.Events {
		if v.Event.ID == event.ID {
			return check(v)
		}
	}
	v, found, err := r.inbox.receipt(event.ID)
	if err != nil {
		return "", err
	}
	if found {
		return check(v)
	}
	return "not_received", nil
}
