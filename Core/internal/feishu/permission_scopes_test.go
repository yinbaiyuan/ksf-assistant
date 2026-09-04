package feishu

import "testing"

func TestRequiredPermissionScopeContract(t *testing.T) {
	contract, err := RequiredPermissionScopes()
	if err != nil {
		t.Fatal(err)
	}
	if len(contract.Bot) != 17 || len(contract.User) != 119 {
		t.Fatalf("unexpected scope counts: bot=%d user=%d", len(contract.Bot), len(contract.User))
	}
	comparison := comparePermissionScopes(contract.User, append([]string{}, contract.User...))
	if !comparison.Complete || len(comparison.Missing) != 0 {
		t.Fatalf("expected complete scope comparison: %#v", comparison)
	}
	comparison = comparePermissionScopes(contract.User, contract.User[:118])
	if comparison.Complete || len(comparison.Missing) != 1 {
		t.Fatalf("expected one missing scope: %#v", comparison)
	}
}
