package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestRetiredAliasesNeverLaunchCLI(t *testing.T) {
	for _, name := range []string{"ksfas-lark", "lark-cli", "ksfas-lark.exe", "lark-cli.exe"} {
		var output bytes.Buffer
		if run([]string{name, "auth", "login"}, &output) == 0 || !bytes.Contains(output.Bytes(), []byte("agent_feishu_middleware_removed_use_independent_cli")) {
			t.Fatalf("retired alias accepted: %s: %s", name, output.String())
		}
	}
}

func TestErrorsAreJSONAndNeverEchoArguments(t *testing.T) {
	for _, args := range [][]string{{"manager"}, {"manager", "install", "--secret=do-not-echo"}, {"manager", "status", "--resources", "secret-relative"}} {
		var output bytes.Buffer
		if run(args, &output) == 0 {
			t.Fatal("expected failure")
		}
		var envelope map[string]any
		if json.Unmarshal(output.Bytes(), &envelope) != nil || envelope["ok"] != false || envelope["schemaVersion"] != float64(1) {
			t.Fatal("invalid envelope")
		}
		if bytes.Contains(output.Bytes(), []byte("do-not-echo")) || bytes.Contains(output.Bytes(), []byte("secret-relative")) {
			t.Fatal("argument exposed")
		}
	}
}

func TestApprovalFailureCodesRemainDistinctWithoutDetails(t *testing.T) {
	for _, code := range []string{"user_command_unsupported", "user_approval_denied", "user_approval_desktop_unavailable", "approval_policy_changed", "user_command_identity_changed"} {
		var output bytes.Buffer
		failure(&output, code+": sensitive external response")
		var envelope struct {
			Error struct {
				Code string `json:"code"`
			}
		}
		if json.Unmarshal(output.Bytes(), &envelope) != nil || envelope.Error.Code != code || bytes.Contains(output.Bytes(), []byte("sensitive")) {
			t.Fatalf("unsafe projection %s", output.String())
		}
	}
	for _, code := range []string{"unknown_private_identifier", "user_approval_denied_secret", "user_command_unsupported\nsecret"} {
		if got := safeFailureCode(code); got != "toolchain_failed" {
			t.Fatal("unknown code leaked")
		}
	}
}
