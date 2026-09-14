package service

import (
	"encoding/json"
	"ksfassistant/core/internal/domain"
	"ksfassistant/core/internal/integration"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFeishuSnapshotCountsOnlyEffectiveConnectedTasks(t *testing.T) {
	s := gatewayBridgeFixture(t, func(s *Service) {
		var err error
		s.integrationRuntime, err = integration.NewRuntime(s.feishuDataRoot, &gatewayMessagePort{}, gatewayCorePort{})
		if err != nil {
			t.Fatal(err)
		}
	})
	store := s.integrationRuntime.Store()
	a, _ := store.Upsert("a", "done", "", "me")
	_, _ = store.UpdateByID(a.ID, func(l *integration.TaskLink) { l.TurnState = "completed"; l.RootMessageID = "delivered" })
	b, _ := store.Upsert("b", "released", "", "me")
	_, _ = store.UpdateByID(b.ID, func(l *integration.TaskLink) { l.RootMessageID = "released-card" })
	_, _ = store.ReleaseByID(b.ID)
	c, _ := store.Upsert("c", "expired", "", "me")
	_, _ = store.UpdateByID(c.ID, func(l *integration.TaskLink) {
		l.ExpiresAt = time.Now().Add(-time.Hour)
		l.RootMessageID = "expired-card"
	})
	_, _ = store.Upsert("pending", "no delivered card", "", "me")
	raw, _ := json.Marshal(s.composeIntegrationSnapshot(domain.FeishuSnapshot{}))
	var fields map[string]any
	_ = json.Unmarshal(raw, &fields)
	if fields["connectedTaskCount"] != float64(1) {
		t.Fatalf("connectedTaskCount=%v", fields["connectedTaskCount"])
	}
	_, _ = store.ReleaseByID(a.ID)
	zero := s.composeIntegrationSnapshot(domain.FeishuSnapshot{}).ConnectedTaskCount
	if zero == nil || *zero != 0 {
		t.Fatal("known empty count is not zero")
	}
	if err := os.WriteFile(filepath.Join(s.feishuDataRoot, "task-links-v1.json"), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if s.composeIntegrationSnapshot(domain.FeishuSnapshot{}).ConnectedTaskCount != nil {
		t.Fatal("unreadable store reported a count")
	}
}
