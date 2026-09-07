package feishu

import "errors"

// MessageReceipt is strictly read-only. An absent or interrupted receipt never
// authorizes a second remote send.
func (transport *ServiceTransport) MessageReceipt(key string) (string, string, error) {
	var ledger transportExecution
	missing, err := readPrivateJSON(transport.executionPath(key), &ledger)
	if err != nil {
		return "", "unknown", err
	}
	if missing {
		return "", "not_submitted", nil
	}
	if ledger.Version != 1 {
		return "", "unknown", errors.New("transport_receipt_invalid")
	}
	operations := NewOperationService(transport.root, NewCapabilityPolicyStore(transport.root), nil)
	record, err := operations.load(ledger.OperationID)
	if err != nil {
		return "", "unknown", err
	}
	if record.Status == OperationSucceeded {
		messageID, _ := record.Result["messageID"].(string)
		if messageID != "" {
			return messageID, "completed", nil
		}
	}
	if record.Status == OperationFailed {
		return "", "failed", nil
	}
	switch record.Status {
	case OperationAwaitingConfirmation, OperationCancelled, OperationExpired:
		if record.AttemptCount == 0 {
			return "", "not_submitted", nil
		}
	}
	return "", "unknown", nil
}
