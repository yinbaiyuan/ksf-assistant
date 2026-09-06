package toolchain

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"ksfassistant/core/internal/capabilitypolicy"
	"ksfassistant/core/internal/usercommand"
)

func TestLauncherPolicyAppliesWithoutDesktop(t *testing.T) {
	manager, status, marker, _ := approvalFixture(t)
	root := manager.config.DataRoot
	t.Setenv("FEISHU_BRIDGE_DATA_DIR", filepath.Join(t.TempDir(), "bypass-attempt"))
	policy := capabilitypolicy.Default()
	policy.CapabilityOverrides["calendar.shortcut.agenda"] = capabilitypolicy.Disabled
	policy.CapabilityOverrides["im.shortcut.messages.send"] = capabilitypolicy.Disabled
	if _, err := capabilitypolicy.NewStore(root).Save(policy, 1); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"calendar", "+agenda", "--as", "user"},
		{"im", "+messages-send", "--chat-id", "oc_fixture", "--text", "must not send", "--as", "bot"},
	} {
		var output bytes.Buffer
		_, err := Launch(context.Background(), status.LauncherPath, args, nil, &output, &output)
		if err == nil || err.Error() != "approval_policy_denied" {
			t.Fatalf("policy bypassed: %v", err)
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("disabled business executed")
	}
}

func bindApprovalRoot(t *testing.T, manager *Manager, root string) {
	t.Helper()
	manager.config.DataRoot = root
	if _, err := manager.Install(); err != nil {
		t.Fatal(err)
	}
}

func TestManagedCapabilitiesDoNotReadAuthorization(t *testing.T) {
	manager := fixture(t)
	status, err := manager.Install()
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	code, err := Launch(context.Background(), status.LauncherPath, []string{"managed", "capabilities", "--json"}, nil, &output, &output)
	if err != nil || code != 0 {
		t.Fatalf("%d %v", code, err)
	}
	expected, err := usercommand.CapabilitiesJSON()
	if err != nil || !bytes.Equal(bytes.TrimSpace(output.Bytes()), bytes.TrimSpace(expected)) {
		t.Fatal("not the installed execution manifest")
	}
	if _, err := os.Stat(manager.config.ConfigDir); !os.IsNotExist(err) {
		t.Fatal("diagnostics created authorization state")
	}
}

func TestSameCLIVersionRequiresCurrentManagedExecutor(t *testing.T) {
	manager := fixture(t)
	if _, err := manager.Install(); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(manager.config.ResourcesDir, "runtime", "toolchain", platform(), "ksf-assistant-toolchain"+suffix())
	if err := os.WriteFile(binary, []byte("updated gate same official CLI version"), 0700); err != nil {
		t.Fatal(err)
	}
	status, err := manager.Status()
	if err != nil || status.Healthy {
		t.Fatal("stale gate marked healthy")
	}
	if status.ExecutionManifest != usercommand.ManifestDigest() {
		t.Fatal("missing execution contract identity")
	}
	status, err = manager.Install()
	if err != nil || !status.Healthy {
		t.Fatalf("unchanged owned launcher did not upgrade: %+v %v", status, err)
	}
}

func TestBusinessResourceIdentifiersAreNotAuthenticationOverrides(t *testing.T) {
	for _, args := range [][]string{
		{"wiki", "spaces", "get_node", "--token", "wik_fixture", "--as", "user"},
		{"apps", "+file-get", "--app-id", "app_fixture", "--path", "fixture.txt", "--as", "user"},
		{"im", "+messages-send", "--chat-id", "oc_fixture", "--text", "--profile", "--as", "user"},
		{"im", "+messages-send", "--chat-id", "oc_fixture", "--text", "--access-token", "--as", "user"},
	} {
		if _, err := launchArguments(args, "default"); err != nil {
			t.Fatalf("business resource rejected: %v", err)
		}
	}
	for _, name := range []string{"--app-id", "--app-secret", "--access-token", "--profile", "--token"} {
		args := []string{"im", "+messages-send", "--chat-id", "oc_fixture", "--text", "fixture", "--as", "user", name, "override"}
		if _, err := launchArguments(args, "default"); err == nil {
			t.Fatal("unrecognized authentication override accepted", name)
		}
	}
}
