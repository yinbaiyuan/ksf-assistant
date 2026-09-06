package service

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ksfassistant/core/internal/toolchain"
)

type fakeDesktopToolchain struct {
	status   toolchain.Status
	err      error
	reads    int
	installs int
}

func (manager *fakeDesktopToolchain) Status() (toolchain.Status, error) {
	manager.reads++
	return manager.status, manager.err
}

func (manager *fakeDesktopToolchain) Install() (toolchain.Status, error) {
	manager.installs++
	return manager.status, manager.err
}

func TestDesktopToolchainStatusDoesNotInstallOrExposePaths(t *testing.T) {
	manager := &fakeDesktopToolchain{status: toolchain.Status{
		Version: "1.0.93", Installed: true, Healthy: true,
		LauncherPath: "/private/launcher", ConfigDir: "/private/config", Profile: "private-profile",
	}}
	result, err := readDesktopToolchain(manager, false)
	if err != nil || manager.reads != 1 || manager.installs != 0 {
		t.Fatalf("unexpected read: %+v, %v", manager, err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"private", "launcherPath", "configDir", "profile"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("desktop projection leaked %s", forbidden)
		}
	}
	if !strings.Contains(string(encoded), `"skills":[]`) || result.SchemaVersion != 1 || !result.Healthy {
		t.Fatalf("unexpected safe status: %s", encoded)
	}
}

func TestDesktopToolchainInstallRequiresConfirmationBeforeManagerCreation(t *testing.T) {
	_, err := (&Service{}).InstallToolchain(ToolchainInstallRequest{})
	if err == nil || !strings.Contains(err.Error(), "确认") {
		t.Fatalf("missing confirmation was admitted: %v", err)
	}
}

func TestDesktopToolchainInstallAndErrorProjectionUseFakeOnly(t *testing.T) {
	manager := &fakeDesktopToolchain{err: errors.New("private credentials /private/path")}
	_, err := readDesktopToolchain(manager, true)
	if manager.installs != 1 || manager.reads != 0 || err == nil || strings.Contains(err.Error(), "private") {
		t.Fatalf("unsafe install failure: %+v, %v", manager, err)
	}
}
