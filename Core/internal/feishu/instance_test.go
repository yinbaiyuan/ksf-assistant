package feishu

import "testing"

func TestInstanceLockPreventsSecondConsumerAndCleansUp(t *testing.T) {
	root := t.TempDir()
	first, err := AcquireInstanceLock(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireInstanceLock(root); err == nil {
		t.Fatal("second consumer was accepted")
	}
	present, alive, _, _, err := InstanceStatus(root)
	if err != nil || !present || !alive {
		t.Fatalf("unexpected instance status: %v %v %v", present, alive, err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	present, _, _, _, err = InstanceStatus(root)
	if err != nil || present {
		t.Fatalf("instance lock was not removed: %v %v", present, err)
	}
}
