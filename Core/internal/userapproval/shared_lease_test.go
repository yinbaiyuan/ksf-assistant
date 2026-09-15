package userapproval

import "testing"

func TestSharedExecutionLeaseExcludesIdentityMutation(t *testing.T) {
	root := t.TempDir()
	a, err := TrySharedExecutionLease(root)
	if err != nil {
		t.Fatal(err)
	}
	defer a()
	b, err := TrySharedExecutionLease(root)
	if err != nil {
		t.Fatal(err)
	}
	if release, err := TryExecutionLease(root); err == nil {
		release()
		t.Fatal("exclusive mutation overlapped readers")
	}
	b()
	a()
	writer, err := TryExecutionLease(root)
	if err != nil {
		t.Fatal(err)
	}
	defer writer()
	if release, err := TrySharedExecutionLease(root); err == nil {
		release()
		t.Fatal("reader overlapped mutation")
	}
}
