package feishu

import (
	"encoding/json"
	"net/url"
	"testing"
)

func TestNativeRegistrationURLAcceptsOfficialLauncherContract(t *testing.T) {
	value, err := nativeRegistrationURL("https://open.feishu.cn/page/launcher?user_code=ABC-123")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if parsed.Path != "/page/launcher" || query.Get("user_code") != "ABC-123" || query.Get("from") != "sdk" || query.Get("tp") != "sdk" {
		t.Fatalf("launcher registration contract changed: %s", value)
	}
	if _, forced := query["createOnly"]; forced {
		t.Fatalf("launcher must offer existing applications: %s", value)
	}
	if query.Get("addons") == "" {
		t.Fatal("launcher registration addons are missing")
	}
}

func TestNativeRegistrationURLRejectsUntrustedLauncherVariants(t *testing.T) {
	for _, value := range []string{
		"https://open.feishu.cn.evil.test/page/launcher?user_code=ABC-123",
		"https://open.feishu.cn/page/other?user_code=ABC-123",
		"https://open.feishu.cn/page/launcher?user_code=ABC-123&device_code=secret",
		"https://open.feishu.cn/page/launcher?user_code=ABC-123&extra=value",
		"https://open.feishu.cn/page/launcher?user_code=bad%20code",
	} {
		if _, err := nativeRegistrationURL(value); err == nil {
			t.Fatalf("accepted untrusted registration URL: %s", value)
		}
	}
}

func TestNativeRegistrationResponseUsesOfficialExpiryField(t *testing.T) {
	var response nativeRegistrationResponse
	if err := json.Unmarshal([]byte(`{"device_code":"device","user_code":"ABC-123","verification_uri_complete":"https://open.feishu.cn/page/launcher?user_code=ABC-123","expires_in":600,"interval":5}`), &response); err != nil {
		t.Fatal(err)
	}
	if response.ExpiresIn != 600 || response.Interval != 5 {
		t.Fatalf("official registration timing was not decoded: %+v", response)
	}
}
