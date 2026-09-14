package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"ksfassistant/core/internal/domain"
	"sort"
	"time"
)

// ActivityRevision is a memory-only signal for both hosts. No account, token,
// history, filesystem or network read is allowed on this fast path.
func (service *Service) ActivityRevision() map[string]string {
	snapshot := service.desktop.Snapshot(time.Now())
	connections := ""
	if service.integrationRuntime != nil {
		connections = service.integrationRuntime.Store().ConnectionRevision()
	}
	return map[string]string{"revision": activityRevision(snapshot) + ":" + connections}
}

func activityRevision(snapshot domain.TaskActivitySnapshot) string {
	observations := append([]domain.TaskObservation(nil), snapshot.Observations...)
	sort.Slice(observations, func(i, j int) bool {
		if observations[i].HostID != observations[j].HostID {
			return observations[i].HostID < observations[j].HostID
		}
		return observations[i].ID < observations[j].ID
	})
	payload, _ := json.Marshal(struct {
		Availability string
		Observations []domain.TaskObservation
	}{snapshot.Availability, observations})
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}
