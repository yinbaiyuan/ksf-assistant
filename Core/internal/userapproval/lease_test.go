package userapproval

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLeaseContentionHelper(t *testing.T) {
	root := os.Getenv("KSF_APPROVAL_LEASE_TEST_ROOT")
	if root == "" {
		return
	}
	release, err := TryExecutionLease(root)
	if err == nil {
		release()
		os.Exit(2)
	}
	if !strings.Contains(err.Error(), "busy") {
		os.Exit(3)
	}
}

func TestExecutionLeaseCrossProcessAndRelease(t *testing.T) {
	root := t.TempDir()
	release, err := TryExecutionLease(root)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if second, err := TryExecutionLease(root); err == nil {
		second()
		t.Fatal("second lease admitted")
	}
	child := exec.Command(os.Args[0], "-test.run=^TestLeaseContentionHelper$")
	child.Env = append(os.Environ(), "KSF_APPROVAL_LEASE_TEST_ROOT="+root)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("cross process lease %v %s", err, output)
	}
	release()
	release()
	next, err := TryExecutionLease(root)
	if err != nil {
		t.Fatal(err)
	}
	next()
}

func TestExecutionLeaseRejectsUnsafePaths(t *testing.T) {
	if release, err := TryExecutionLease("relative"); err == nil {
		release()
		t.Fatal("relative path accepted")
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "user-authorization-execution-v1.lock"), 0700); err != nil {
		t.Fatal(err)
	}
	if release, err := TryExecutionLease(root); err == nil {
		release()
		t.Fatal("directory accepted")
	}
}
