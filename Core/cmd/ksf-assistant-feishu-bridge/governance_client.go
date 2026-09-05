package main

import "ksfassistant/core/internal/feishu"

func operationTerminal(status feishu.OperationStatus) bool {
	switch status {
	case feishu.OperationSucceeded, feishu.OperationFailed, feishu.OperationExpired, feishu.OperationCancelled, feishu.OperationOutcomeUnknown:
		return true
	default:
		return false
	}
}
