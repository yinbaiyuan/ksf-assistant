package main

import (
	"errors"
	"testing"

	"codexusagebar/core/internal/feishu"
)

func TestCapabilityClientAdvicePreservesManualReviewOutcome(t *testing.T) {
	if got := operationPendingNextAction(feishu.OperationView{Status: feishu.OperationOutcomeUnknown, NextAction: "manual_review"}); got != "manual_review" {
		t.Fatalf("next action = %q", got)
	}
	if got := operationPendingNextAction(feishu.OperationView{Status: feishu.OperationRunning, NextAction: "execute"}); got != "query_same_operation" {
		t.Fatalf("running next action = %q", got)
	}
	var output map[string]any
	err := writeCapabilityRejection(func(value any) error {
		output, _ = value.(map[string]any)
		return nil
	}, errors.New("outbound_disabled"))
	if err != nil || output["errorCode"] != "outbound_disabled" || output["nextAction"] != "enable_outbound" {
		t.Fatalf("advice=%#v err=%v", output, err)
	}
}
