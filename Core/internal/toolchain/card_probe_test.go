package toolchain

import "testing"

func TestCardKitRawRoutesRemainInternal(t *testing.T) {
	_, err := launchArguments([]string{"api", "POST", "/open-apis/cardkit/v1/cards", "--as", "bot", "--data", "{}"}, "default")
	if err == nil || err.Error() != "cardkit_internal_transport_required" {
		t.Fatal(err)
	}
}
