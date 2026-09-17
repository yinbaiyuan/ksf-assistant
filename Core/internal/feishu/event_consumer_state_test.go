package feishu

import "testing"

func TestEventConsumerStateIsNodeCompatible(t *testing.T) {
	store := NewEventConsumerStateStore(t.TempDir())
	if err := store.UpdateConnection("connected"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkReceived("im.message.receive_v1"); err != nil {
		t.Fatal(err)
	}
	value, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	if value["schemaVersion"] != float64(3) || value["transport"] != "official-sdk" || value["status"] != "connected" {
		t.Fatalf("unexpected state: %#v", value)
	}
	if value["profile"] != "managed" || value["configurable"] != false || value["desiredConnection"] != true {
		t.Fatalf("event lifecycle regained role control: %#v", value)
	}
	events := value["events"].(map[string]any)
	if len(events) != len(FixedEventKeys) || events["im.message.receive_v1"].(map[string]any)["lastReceivedAt"] == nil {
		t.Fatalf("unexpected event state: %#v", events)
	}
}
