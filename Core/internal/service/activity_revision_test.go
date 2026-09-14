package service

import (
	"ksfassistant/core/internal/domain"
	"testing"
	"time"
)

func TestActivityRevisionIgnoresClockAndMapOrderButTracksStatus(t *testing.T) {
	a := domain.TaskObservation{ID: "a", HostID: "local", RuntimeStatus: "active"}
	b := domain.TaskObservation{ID: "b", HostID: "local", RuntimeStatus: "idle"}
	first := domain.TaskActivitySnapshot{Availability: "available", ObservedAt: time.Now(), Observations: []domain.TaskObservation{a, b}}
	revision := activityRevision(first)
	first.ObservedAt = first.ObservedAt.Add(time.Minute)
	first.Observations = []domain.TaskObservation{b, a}
	if activityRevision(first) != revision {
		t.Fatal("clock/order triggered refresh")
	}
	first.Observations[1].RuntimeStatus = "idle"
	if activityRevision(first) == revision {
		t.Fatal("completion failed to trigger refresh")
	}
}
