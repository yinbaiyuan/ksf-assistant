package integration

import "testing"

func TestConnectionRevisionIgnoresProgressButTracksRelease(t *testing.T) {
	s := NewTaskLinkStore(t.TempDir())
	l, _ := s.Upsert("one", "one", "", "me")
	_, _ = s.UpdateByID(l.ID, func(v *TaskLink) { v.RootMessageID = "delivered" })
	initial := s.ConnectionRevision()
	_, _ = s.UpdateByID(l.ID, func(v *TaskLink) { v.Detail = "new streaming content"; v.TurnState = "completed" })
	if s.ConnectionRevision() != initial {
		t.Fatal("progress caused connection refresh")
	}
	_, _ = s.ReleaseByID(l.ID)
	if s.ConnectionRevision() == initial {
		t.Fatal("release did not notify hosts")
	}
}
