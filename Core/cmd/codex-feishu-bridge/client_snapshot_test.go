package main

import (
	"testing"

	"codexusagebar/core/internal/feishu"
)

func TestClientAggregateSnapshotIncludesProcessTargetsLinksAndQueues(t *testing.T) {
	root := t.TempDir()
	settings := feishu.DefaultSettings()
	value, err := clientAggregateSnapshot(root, settings)
	if err != nil {
		t.Fatal(err)
	}
	if value.RuntimeKind != "go" || value.TaskLinkProtocolVersion != 2 {
		t.Fatalf("snapshot = %#v", value)
	}
	if value.TargetAliases == nil || value.Links == nil || value.Queues == nil || value.Capabilities == nil {
		t.Fatalf("aggregate fields must be present: %#v", value)
	}
}
