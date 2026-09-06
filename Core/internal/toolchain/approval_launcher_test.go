package toolchain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"ksfassistant/core/internal/localipc"
	"ksfassistant/core/internal/privateipc"
	"ksfassistant/core/internal/userapproval"
	"ksfassistant/core/internal/usercommand"
)

func approvalFixture(t *testing.T) (*Manager, Status, string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell fake is POSIX; cross-platform command/gate logic is separately tested")
	}
	manager := fixture(t)
	root := filepath.Dir(manager.config.HomeDir)
	marker, statusFile := filepath.Join(root, "executed"), filepath.Join(root, "auth-status.json")
	writeStatus := `{"appId":"cli_fixture","brand":"feishu","identities":{"user":{"available":true,"openId":"ou_fixture","userName":"Fixture user"},"bot":{"available":true,"appName":"Fixture app"}}}`
	if err := os.WriteFile(statusFile, []byte(writeStatus), 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(manager.config.ResourcesDir, "runtime", "lark-cli", platform(), "lark-cli"+suffix())
	script := "#!/bin/sh\nif [ \"$3\" = auth ]; then cat " + strconv.Quote(statusFile) + "; exit 0; fi\nprintf '%s\\n' \"$@\" >> " + strconv.Quote(marker) + "\ncat >> " + strconv.Quote(marker) + "\nfor argument in \"$@\"; do case \"$argument\" in file=frozen-*) cat \"${argument#file=}\" >> " + strconv.Quote(marker) + ";; esac; done\nprintf '{\"ok\":true}\\n'\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	hash, _ := fileHash(binary)
	manifest := filepath.Join(manager.config.ResourcesDir, "runtime", "lark-cli-runtime.json")
	var value map[string]any
	if err := readJSON(manifest, &value); err != nil {
		t.Fatal(err)
	}
	value["artifacts"].(map[string]any)[platform()].(map[string]any)["executableSha256"] = hash
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(manager.config.ConfigDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(manager.config.ConfigDir, "config.json"), map[string]any{"apps": []any{map[string]any{"name": "default", "brand": "feishu"}}}); err != nil {
		t.Fatal(err)
	}
	status, err := manager.Install()
	if err != nil {
		t.Fatal(err)
	}
	return manager, status, marker, statusFile
}

func TestReadBotAndLocalLauncherDoNotRequireCore(t *testing.T) {
	manager, status, marker, _ := approvalFixture(t)
	bindApprovalRoot(t, manager, filepath.Join(filepath.Dir(manager.config.HomeDir), "not-running"))
	for _, args := range [][]string{{"--version"}, {"calendar", "+agenda", "--as", "user"}, {"im", "+messages-send", "--chat-id", "oc_fixture", "--text", "bot message", "--as", "bot"}} {
		var output bytes.Buffer
		code, err := Launch(context.Background(), status.LauncherPath, args, nil, &output, &output)
		if code != 0 || err != nil {
			t.Fatalf("%v: %d %v", args, code, err)
		}
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("read/bot did not execute")
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	_, err := Launch(context.Background(), status.LauncherPath, []string{"im", "+messages-send", "--chat-id", "oc_fixture", "--text", "user message", "--as", "user"}, nil, &output, &output)
	if err == nil || !strings.Contains(err.Error(), "desktop_unavailable") {
		t.Fatal("user write ran without Core")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("unapproved command started")
	}
}

func TestLauncherPendingCoreExitIsDesktopUnavailable(t *testing.T) {
	manager, status, marker, _ := approvalFixture(t)
	root := filepath.Join(filepath.Dir(manager.config.HomeDir), "ipc")
	bindApprovalRoot(t, manager, root)
	pending := make(chan struct{})
	var once sync.Once
	server, err := localipc.Listen(root, privateipc.HandlerFunc(func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		switch method {
		case "userApproval/request":
			return usercommand.RequestResult{ID: "pending-exit"}, nil
		case "userApproval/status":
			once.Do(func() { close(pending) })
			<-ctx.Done()
			return nil, ctx.Err()
		default:
			return nil, errors.New("unexpected_method")
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		var output bytes.Buffer
		_, err := Launch(ctx, status.LauncherPath, []string{"im", "+messages-send", "--chat-id", "oc_fixture", "--text", "pending exit", "--as", "user"}, nil, &output, &output)
		result <- err
	}()
	select {
	case <-pending:
	case <-ctx.Done():
		t.Fatal("launcher never reached pending approval")
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err == nil || err.Error() != "user_approval_desktop_unavailable" {
			t.Fatalf("pending Core exit: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("launcher did not stop after Core exit")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("pending command executed after Core exit")
	}
}

func TestReadAndBotRefuseAuthorizationChangesWithoutDesktop(t *testing.T) {
	manager, status, marker, _ := approvalFixture(t)
	root := filepath.Join(filepath.Dir(manager.config.HomeDir), "authorization-root")
	bindApprovalRoot(t, manager, root)
	release, err := userapproval.TryExecutionLease(root)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	for _, args := range [][]string{
		{"calendar", "+agenda", "--as", "user"},
		{"im", "+messages-send", "--chat-id", "oc_fixture", "--text", "bot message", "--as", "bot"},
	} {
		var output bytes.Buffer
		if code, err := Launch(context.Background(), status.LauncherPath, args, nil, &output, &output); code == 0 || err == nil || !strings.Contains(err.Error(), "authorization_busy") {
			t.Fatalf("authorization switch was not excluded: %d %v", code, err)
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("business command started during authorization transaction")
	}
}

func TestLauncherGateAndFrozenFile(t *testing.T) {
	for _, decision := range []string{"approved", "denied", "identity_changed", "authorization_busy"} {
		t.Run(decision, func(t *testing.T) {
			manager, status, marker, statusFile := approvalFixture(t)
			root := filepath.Join(filepath.Dir(manager.config.HomeDir), "ipc")
			bindApprovalRoot(t, manager, root)
			source := filepath.Join(filepath.Dir(manager.config.HomeDir), "payload.txt")
			if err := os.WriteFile(source, []byte("approved file content"), 0600); err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			consumes, cancels := 0, 0
			outcome := ""
			var releaseBusy func()
			defer func() {
				if releaseBusy != nil {
					releaseBusy()
				}
			}()
			server, err := localipc.Listen(root, privateipc.HandlerFunc(func(ctx context.Context, method string, params json.RawMessage) (any, error) {
				mu.Lock()
				defer mu.Unlock()
				switch method {
				case "userApproval/request":
					release, err := userapproval.TryExecutionLease(root)
					if err != nil {
						return nil, errors.New("lease held before user decision")
					}
					release()
					command, err := usercommand.Decode(params)
					if err != nil {
						return nil, err
					}
					review, err := usercommand.Evaluate(command)
					if err != nil || !review.NeedsApproval {
						return nil, errors.New("invalid request")
					}
					if err := manager.ValidateIdentity(ctx, command.Identity); err != nil {
						return nil, err
					}
					if _, err := os.Stat(marker); !os.IsNotExist(err) {
						return nil, errors.New("executed before approval")
					}
					if err := os.WriteFile(source, []byte("unapproved replacement"), 0600); err != nil {
						return nil, err
					}
					if decision == "identity_changed" {
						data, _ := os.ReadFile(statusFile)
						if err := os.WriteFile(statusFile, bytes.ReplaceAll(data, []byte("ou_fixture"), []byte("ou_other")), 0600); err != nil {
							return nil, err
						}
					}
					return usercommand.RequestResult{ID: "fixture-request"}, nil
				case "userApproval/status":
					state := decision
					if state == "identity_changed" {
						state = "approved"
					}
					if decision == "authorization_busy" {
						var err error
						releaseBusy, err = userapproval.TryExecutionLease(root)
						if err != nil {
							return nil, err
						}
						state = "approved"
					}
					return usercommand.StatusResult{State: state}, nil
				case "userApproval/consume":
					if release, err := userapproval.TryExecutionLease(root); err == nil {
						release()
						return nil, errors.New("consume is not lease protected")
					}
					consumes++
					return usercommand.ConsumeResult{Allowed: true}, nil
				case "userApproval/cancel":
					cancels++
					return map[string]any{"cancelled": true}, nil
				case "userApproval/result":
					if release, err := userapproval.TryExecutionLease(root); err == nil {
						release()
						return nil, errors.New("execution lease released before reporting")
					}
					var result usercommand.ResultRequest
					if json.Unmarshal(params, &result) != nil {
						return nil, errors.New("bad result")
					}
					outcome = result.Outcome
					return map[string]any{"ok": true}, nil
				default:
					return nil, privateipc.ErrMethodNotFound
				}
			}))
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			var output bytes.Buffer
			code, launchErr := Launch(context.Background(), status.LauncherPath, []string{"im", "files", "create", "--data", `{"file_type":"stream","file_name":"fixture.txt"}`, "--file", "file=" + source, "--as", "user"}, nil, &output, &output)
			mu.Lock()
			defer mu.Unlock()
			data, readErr := os.ReadFile(marker)
			if decision == "approved" {
				if code != 0 || launchErr != nil || consumes != 1 || cancels != 0 || outcome != "succeeded" {
					t.Fatalf("code=%d err=%v consumes=%d cancels=%d outcome=%s", code, launchErr, consumes, cancels, outcome)
				}
				if readErr != nil || !bytes.Contains(data, []byte("approved file content")) || bytes.Contains(data, []byte("unapproved replacement")) {
					t.Fatal("did not execute immutable snapshot")
				}
				release, err := userapproval.TryExecutionLease(root)
				if err != nil {
					t.Fatal("lease leaked")
				}
				release()
			} else {
				if launchErr == nil || consumes != 0 || cancels != 1 || !os.IsNotExist(readErr) {
					t.Fatalf("denied request started: %v consumes=%d cancels=%d", launchErr, consumes, cancels)
				}
			}
		})
	}
}

func TestIdentityRejectsTamperedBinary(t *testing.T) {
	manager, _, _, _ := approvalFixture(t)
	identity, err := manager.Identity(context.Background(), "user")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ValidateIdentity(context.Background(), identity); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(manager.config.ResourcesDir, "runtime", "lark-cli", platform(), "lark-cli"+suffix())
	if err := os.WriteFile(binary, []byte("changed executable"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := manager.ValidateIdentity(context.Background(), identity); err == nil {
		t.Fatal("tampered CLI trusted")
	}
}
