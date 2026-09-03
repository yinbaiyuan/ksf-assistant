package bridge

import (
	"testing"

	"codexusagebar/core/internal/domain"
)

func TestTaskKeyMatchesBridgeContract(t *testing.T) {
	if value := domain.PublicTaskKey("thread-1"); value != "4b0a5fefc328e6b9257b" {
		t.Fatalf("unexpected task key: %s", value)
	}
}
