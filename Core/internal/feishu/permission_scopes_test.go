package feishu

import "testing"

func TestRequiredPermissionScopeContract(t *testing.T) {
	contract, err := RequiredPermissionScopes()
	if err != nil {
		t.Fatal(err)
	}
	if len(contract.Bot) <= 17 || len(contract.User) <= 119 {
		t.Fatalf("unexpected scope counts: bot=%d user=%d", len(contract.Bot), len(contract.User))
	}
	for _, scope := range []string{"mail:user_mailbox.message:readonly", "approval:approval:read", "approval:instance:read", "approval:instance:write", "approval:task:read", "approval:task:write"} {
		if !contains(contract.User, scope) {
			t.Fatalf("expanded user scopes are missing %s", scope)
		}
	}
	if !contains(contract.Bot, "mail:event") {
		t.Fatalf("expanded scopes are incomplete: bot=%v user=%v", contract.Bot, contract.User)
	}
	comparison := comparePermissionScopes(contract.User, append([]string{}, contract.User...))
	if !comparison.Complete || len(comparison.Missing) != 0 {
		t.Fatalf("expected complete scope comparison: %#v", comparison)
	}
	comparison = comparePermissionScopes(contract.User, contract.User[:len(contract.User)-1])
	if comparison.Complete || len(comparison.Missing) != 1 {
		t.Fatalf("expected one missing scope: %#v", comparison)
	}
}
