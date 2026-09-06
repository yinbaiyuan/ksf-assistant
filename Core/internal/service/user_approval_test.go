package service

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ksfassistant/core/internal/feishu"
	"ksfassistant/core/internal/privateipc"
	"ksfassistant/core/internal/usercommand"
)

func approvalClient(t *testing.T, core *Service) *privateipc.Peer {
	t.Helper()
	local, remote := net.Pipe()
	client := privateipc.NewPeer(local, local, nil)
	server := privateipc.NewPeer(remote, remote, privateipc.HandlerFunc(core.handleLocalRPC))
	ctx, cancel := context.WithCancel(context.Background())
	go client.Serve(ctx)
	go server.Serve(ctx)
	t.Cleanup(func() { cancel(); client.Close(); server.Close(); local.Close(); remote.Close() })
	return client
}

func approvalCommand() usercommand.Command {
	return usercommand.Command{Version: usercommand.Version, Args: []string{"im", "+messages-send", "--as", "user", "--chat-id", "chat_fixture", "--text", "private body sentinel"}, Identity: usercommand.Identity{AppID: "app_fixture", UserID: "user_fixture", UserName: "fixture user", ApplicationName: "fixture app", Profile: "default", Brand: "feishu"}}
}

func approvalService(t *testing.T) *Service {
	core := &Service{feishuDataRoot: t.TempDir()}
	core.approvals().verifyIdentity = func(context.Context, usercommand.Identity) error { return nil }
	core.UserApprovalPoll(true)
	t.Cleanup(core.approvals().broker.Close)
	return core
}

func TestUserApprovalNativeOnlySingleConnection(t *testing.T) {
	core := approvalService(t)
	client := approvalClient(t, core)
	other := approvalClient(t, core)
	ctx := context.Background()
	var requested usercommand.RequestResult
	command := approvalCommand()
	if err := client.Call(ctx, "userApproval/request", command, &requested); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"userApproval/decide", "userApproval/poll"} {
		var result any
		if client.Call(ctx, method, map[string]any{"id": requested.ID, "approve": true}, &result) == nil {
			t.Fatal("CLI exposed host control")
		}
	}
	var status usercommand.StatusResult
	if other.Call(ctx, "userApproval/status", usercommand.StatusRequest{ID: requested.ID}, &status) == nil {
		t.Fatal("other connection reused approval")
	}
	if err := client.Call(ctx, "userApproval/status", usercommand.StatusRequest{ID: requested.ID}, &status); err != nil || status.State != "pending" {
		t.Fatalf("%+v %v", status, err)
	}
	core.UserApprovalPoll(true)
	if !core.approvals().broker.Decide(requested.ID, true) {
		t.Fatal("native decision failed")
	}
	review, err := usercommand.Evaluate(command)
	if err != nil {
		t.Fatal(err)
	}
	var consumed usercommand.ConsumeResult
	input := usercommand.ConsumeRequest{ID: requested.ID, Digest: review.Digest}
	if err := client.Call(ctx, "userApproval/consume", input, &consumed); err != nil || !consumed.Allowed {
		t.Fatalf("%+v %v", consumed, err)
	}
	if client.Call(ctx, "userApproval/consume", input, &consumed) == nil {
		t.Fatal("second consume allowed")
	}
	var result any
	if err := client.Call(ctx, "userApproval/result", usercommand.ResultRequest{ID: requested.ID, Outcome: "unknown"}, &result); err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(filepath.Join(core.feishuDataRoot, "user-approval-audit-v1", requested.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"private body sentinel", "chat_fixture", "app_fixture", "user_fixture", "fixture user"} {
		if strings.Contains(string(bytes), forbidden) {
			t.Fatal("audit leaked request data")
		}
	}
	if !strings.Contains(string(bytes), `"unknown"`) {
		t.Fatal("unknown result missing")
	}
}

func TestUserApprovalPolicyAndIdentityRechecked(t *testing.T) {
	for _, scenario := range []string{"identity", "policy", "body", "disabled"} {
		t.Run(scenario, func(t *testing.T) {
			core := approvalService(t)
			client := approvalClient(t, core)
			ctx := context.Background()
			command := approvalCommand()
			if scenario == "disabled" {
				policy := feishu.DefaultCapabilityPolicy()
				policy.CapabilityOverrides["im.shortcut.messages.send"] = feishu.CapabilityDisabled
				if _, err := feishu.NewCapabilityPolicyStore(core.feishuDataRoot).Save(policy, 1); err != nil {
					t.Fatal(err)
				}
			}
			var requested usercommand.RequestResult
			err := client.Call(ctx, "userApproval/request", command, &requested)
			if scenario == "disabled" {
				if err == nil {
					t.Fatal("disabled requested")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			core.UserApprovalPoll(true)
			core.approvals().broker.Decide(requested.ID, true)
			review, _ := usercommand.Evaluate(command)
			switch scenario {
			case "identity":
				core.approvals().verifyIdentity = func(context.Context, usercommand.Identity) error { return errors.New("identity changed") }
			case "policy":
				if _, err := feishu.NewCapabilityPolicyStore(core.feishuDataRoot).Save(feishu.DefaultCapabilityPolicy(), 1); err != nil {
					t.Fatal(err)
				}
			case "body":
				review.Digest = strings.Repeat("b", 64)
			}
			var consumed usercommand.ConsumeResult
			if client.Call(ctx, "userApproval/consume", usercommand.ConsumeRequest{ID: requested.ID, Digest: review.Digest}, &consumed) == nil {
				t.Fatal("changed request consumed")
			}
		})
	}
}

func TestUserApprovalRejectsClaimedReadAndApproval(t *testing.T) {
	core := approvalService(t)
	client := approvalClient(t, core)
	command := approvalCommand()
	encoded, _ := json.Marshal(command)
	var fields map[string]any
	_ = json.Unmarshal(encoded, &fields)
	fields["approved"] = true
	fields["risk"] = "read"
	var result any
	if client.Call(context.Background(), "userApproval/request", fields, &result) == nil {
		t.Fatal("caller assertions accepted")
	}
	core.UserApprovalPoll(false)
	if client.Call(context.Background(), "userApproval/request", command, &result) == nil {
		t.Fatal("unavailable desktop accepted")
	}
}
