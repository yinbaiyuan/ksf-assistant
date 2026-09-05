//go:build !windows

package localipc

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestUnixOfflineCallDoesNotCreateEndpoint(t *testing.T) {
	root, err := canonicalRoot(filepath.Join(t.TempDir(), "offline"))
	if err != nil {
		t.Fatal(err)
	}
	_ = Call(context.Background(), root, "cli/execute", nil, nil)
	if _, err := os.Lstat(endpointDirectory(root)); !os.IsNotExist(err) {
		t.Fatalf("offline endpoint exists: %v", err)
	}
}

func TestUnixEndpointPermissionsAndSymlinkRejection(t *testing.T) {
	root, err := canonicalRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server, err := Listen(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	path, err := endpointPath(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := privatePath(path, false); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := dialEndpoint(context.Background(), root); err == nil {
		t.Fatal("insecure directory accepted")
	}
	if err := os.Chmod(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0666); err != nil {
		t.Fatal(err)
	}
	if _, err := dialEndpoint(context.Background(), root); err == nil {
		t.Fatal("insecure socket accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "target"), path); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	if listener, err := Listen(root, nil); err == nil {
		listener.Close()
		t.Fatal("socket symlink accepted")
	}
}

func TestUnixServerReclaimsStaleSocketAndRootAliases(t *testing.T) {
	root, err := canonicalRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path, err := endpointPath(root, true)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	stale.SetUnlinkOnClose(false)
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	_ = stale.Close()
	server, err := Listen(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	if duplicate, err := Listen(alias, nil); err == nil {
		duplicate.Close()
		t.Fatal("root alias bypassed unique listener")
	}
}
