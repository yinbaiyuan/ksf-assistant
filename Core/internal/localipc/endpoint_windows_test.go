//go:build windows

package localipc

import (
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsPipeACLCurrentUserOnly(t *testing.T) {
	root, err := canonicalRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path, descriptor, err := pipeIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if descriptor != "D:P(A;;GA;;;"+user.User.Sid.String()+")" || !strings.HasPrefix(path, `\\.\pipe\ksfassistant-`) {
		t.Fatalf("unexpected pipe identity: %s %s", path, descriptor)
	}
	server, err := Listen(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	security, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	actual := security.String()
	if actual != descriptor && actual != strings.ReplaceAll(descriptor, ";;GA;", ";;FA;") {
		t.Fatalf("actual pipe ACL differs: %s", security.String())
	}
}
