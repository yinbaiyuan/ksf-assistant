package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"ksfassistant/core/internal/feishuprotocol"
)

const maximumAuthOutputBytes = 256 * 1024

func validateAuthCommand(runner CapabilityExecutor, args []string, stdin []byte) error {
	if runner.Binary == "" || runner.Profile != "" && runner.Profile != "default" || len(args) < 3 || len(stdin) > 16*1024 {
		return errors.New("官方 CLI 授权配置无效")
	}
	allowed := map[string]bool{"auth/status": true, "auth/scopes": true, "auth/logout": true, "contact/+search-user": true, "config/init": true}
	if !allowed[args[0]+"/"+args[1]] || !contains(args, "--json") {
		return errors.New("不支持的官方 CLI 授权命令")
	}
	flags := map[string]bool{"--json": false}
	switch args[0] + "/" + args[1] {
	case "auth/status":
		flags["--verify"] = false
	case "contact/+search-user":
		flags["--user-ids"], flags["--as"] = true, true
	case "config/init":
		flags["--name"], flags["--app-id"], flags["--brand"], flags["--lang"] = true, true, true, true
		flags["--app-secret-stdin"] = false
	}
	seen := map[string]bool{}
	for index := 2; index < len(args); index++ {
		flag := args[index]
		valueRequired, allowed := flags[flag]
		if !allowed || seen[flag] {
			return errors.New("不支持的官方 CLI 授权参数")
		}
		seen[flag] = true
		if valueRequired {
			index++
			if index >= len(args) || args[index] == "" || strings.HasPrefix(args[index], "-") || strings.ContainsAny(args[index], "\x00\r\n") {
				return errors.New("官方 CLI 授权参数无效")
			}
			if flag == "--name" && args[index] != "default" || flag == "--as" && args[index] != "user" || flag == "--user-ids" && args[index] != "me" {
				return errors.New("官方 CLI 身份或配置不匹配")
			}
		}
	}
	if args[0] == "config" && (!seen["--app-secret-stdin"] || !seen["--app-id"] || !seen["--name"]) {
		return errors.New("官方 CLI 应用配置不完整")
	}
	if args[0] != "config" && len(stdin) != 0 {
		return errors.New("授权操作不接受凭据输入")
	}
	return nil
}

func authEnvironment() []string {
	result := []string{}
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		upper := strings.ToUpper(name)
		if upper == "LARKSUITE_CLI_CONFIG_DIR" || !strings.HasPrefix(upper, "LARK") && !strings.HasPrefix(upper, "FEISHU_") {
			result = append(result, entry)
		}
	}
	return append(result, "LARKSUITE_CLI_PROFILE=default", "LARKSUITE_CLI_NO_UPDATE_NOTIFIER=1", "LARKSUITE_CLI_NO_SKILLS_NOTIFIER=1", "LARKSUITE_CLI_REMOTE_META=off")
}

func authResultHasSecret(value any) bool {
	switch item := value.(type) {
	case map[string]any:
		for key, child := range item {
			normalized := strings.ToLower(strings.ReplaceAll(key, "_", ""))
			switch normalized {
			case "accesstoken", "refreshtoken", "devicecode", "appsecret", "clientsecret", "authorization", "token":
				if child != "****" {
					return true
				}
			}
			if authResultHasSecret(child) {
				return true
			}
		}
	case []any:
		for _, child := range item {
			if authResultHasSecret(child) {
				return true
			}
		}
	}
	return false
}

func validAuthResult(args []string, result map[string]any) bool {
	if authResultHasSecret(result) || result["error"] != nil || result["ok"] == false {
		return false
	}
	if code, exists := result["code"]; exists && code != float64(0) {
		return false
	}
	switch args[0] + "/" + args[1] {
	case "auth/status":
		identities, ok := result["identities"].(map[string]any)
		if !ok {
			return false
		}
		user, ok := identities["user"].(map[string]any)
		if !ok {
			return false
		}
		_, ok = user["available"].(bool)
		return ok
	case "auth/scopes":
		_, ok := result["userScopes"].([]any)
		return ok
	case "auth/logout":
		_, ok := result["loggedOut"].(bool)
		return result["ok"] == true && ok
	case "config/init":
		_, ok := result["appId"].(string)
		delete(result, "appSecret")
		return ok
	case "contact/+search-user":
		return result["data"] != nil
	}
	return false
}

func emptyAuthStatus(status string) feishuprotocol.AuthStatus {
	return feishuprotocol.AuthStatus{SchemaVersion: 1, Status: status, Identity: "user", Profile: "default", MissingCapabilities: []string{}}
}

func authStatusMap(status feishuprotocol.AuthStatus) map[string]any {
	encoded, _ := json.Marshal(status)
	var result map[string]any
	_ = json.Unmarshal(encoded, &result)
	return result
}

func ReadAuthStatus(ctx context.Context, runner CapabilityExecutor, dataRoot string) (feishuprotocol.AuthStatus, error) {
	if session := currentUserAuthSession(dataRoot); session != nil {
		select {
		case <-session.done:
		default:
			return session.snapshot(), nil
		}
	}
	return readCLIAuthStatus(ctx, runner)
}

func readCLIAuthStatus(ctx context.Context, runner CapabilityExecutor) (feishuprotocol.AuthStatus, error) {
	status := emptyAuthStatus("unknown")
	raw, err := runner.RunAuthJSON(ctx, []string{"auth", "status", "--verify", "--json"}, nil, 15*time.Second)
	if err != nil {
		return status, err
	}
	appID, _ := raw["appId"].(string)
	status.ProfileValid = raw["brand"] == "feishu" && strings.TrimSpace(appID) != ""
	identities := raw["identities"].(map[string]any)
	user := identities["user"].(map[string]any)
	observation := cliUserAuthState(user)
	status.IdentityValid = observation == "authorized"
	if status.ProfileValid || observation == "failed" {
		status.Status = observation
	}
	if !status.ProfileValid {
		status.MissingCapabilities = append(status.MissingCapabilities, "应用配置待验证")
	}
	granted := stringList(user["scope"])
	status.GrantedScopeCount = len(comparePermissionScopes(nil, granted).Excess)
	required := []string{"contact:user.base:readonly"}
	if len(comparePermissionScopes(required, granted).Missing) > 0 {
		status.MissingCapabilities = append(status.MissingCapabilities, "用户授权范围")
	}
	scopes, err := runner.RunAuthJSON(ctx, []string{"auth", "scopes", "--json"}, nil, 15*time.Second)
	if err != nil {
		status.MissingCapabilities = append(status.MissingCapabilities, "应用权限待验证")
	} else if len(comparePermissionScopes(required, stringList(scopes["userScopes"])).Missing) > 0 {
		status.MissingCapabilities = append(status.MissingCapabilities, "应用功能权限")
	}
	return status, nil
}

func cliUserAuthState(user map[string]any) string {
	if user["verified"] == false || user["status"] == "verify_failed" {
		return "failed"
	}
	available, known := user["available"].(bool)
	if !known {
		return "unknown"
	}
	if !available {
		if user["status"] == "missing" && user["verified"] != true {
			return "unauthorized"
		}
		return "unknown"
	}
	status, _ := user["status"].(string)
	if user["verified"] == true && (status == "" || status == "ready" || status == "needs_refresh") {
		return "authorized"
	}
	return "unknown"
}

func LogoutUserAuth(ctx context.Context, runner CapabilityExecutor, dataRoot string) (feishuprotocol.AuthStatus, error) {
	if err := ctx.Err(); err != nil {
		return emptyAuthStatus("unknown"), err
	}
	CancelUserAuthFlow(dataRoot)
	_, err := runner.RunAuthJSON(ctx, []string{"auth", "logout", "--json"}, nil, 30*time.Second)
	if err != nil {
		return emptyAuthStatus("unknown"), err
	}
	unknown := emptyAuthStatus("unknown")
	unknown.MissingCapabilities = []string{"退出结果尚未确认，请重新检查用户授权"}
	raw, err := runner.RunAuthJSON(ctx, []string{"auth", "status", "--verify", "--json"}, nil, 15*time.Second)
	if err != nil {
		return unknown, nil
	}
	identities, _ := raw["identities"].(map[string]any)
	user, _ := identities["user"].(map[string]any)
	appID, _ := raw["appId"].(string)
	profileValid := raw["brand"] == "feishu" && strings.TrimSpace(appID) != ""
	if !profileValid || cliUserAuthState(user) != "unauthorized" {
		return unknown, nil
	}
	status := emptyAuthStatus("unauthorized")
	status.ProfileValid = profileValid
	return status, nil
}
