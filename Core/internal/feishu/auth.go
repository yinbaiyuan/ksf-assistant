package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"ksfassistant/core/internal/userapproval"
)

func (runner CapabilityExecutor) RunAuthJSON(ctx context.Context, args []string, stdin []byte, timeout time.Duration) (map[string]any, error) {
	if err := validateAuthCommand(runner, args, stdin); err != nil {
		return nil, err
	}
	if args[0] == "config" && args[1] == "init" || args[0] == "auth" && args[1] == "logout" {
		release, err := userapproval.TryExecutionLease(runner.DataRoot)
		if err != nil {
			return nil, err
		}
		defer release()
	}
	if timeout <= 0 || timeout > time.Minute {
		timeout = time.Minute
	}
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(commandCtx, runner.Binary, append([]string{"--profile", "default"}, args...)...)
	command.Dir = runner.WorkingDirectory
	command.Env = authEnvironment()
	command.WaitDelay = 2 * time.Second
	command.Stdin = bytes.NewReader(stdin)
	stdout := boundedCommandBuffer{limit: maximumAuthOutputBytes}
	stderr := boundedCommandBuffer{limit: 32 * 1024}
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return nil, errors.New("官方 CLI 授权操作失败，请检查授权配置后重试")
	}
	if stdout.overflow || stderr.overflow {
		return nil, errors.New("官方 CLI 授权输出超出安全限制")
	}
	var result map[string]any
	if json.Unmarshal(stdout.Bytes(), &result) != nil || result == nil || !validAuthResult(args, result) {
		return nil, errors.New("官方 CLI 授权响应格式无效")
	}
	return result, nil
}

func ConfigureExistingApp(ctx context.Context, runner CapabilityExecutor, appID, appSecret, brand, profile string) (map[string]any, error) {
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{3,128}$`).MatchString(appID) || strings.TrimSpace(appSecret) == "" {
		return nil, errors.New("invalid existing app credentials")
	}
	if brand != "" && brand != "feishu" {
		return nil, errors.New("此版本仅支持飞书品牌 feishu")
	}
	brand = "feishu"
	if profile == "" {
		profile = "default"
	}
	CancelUserAuthFlow(runner.DataRoot)
	_, err := runner.RunAuthJSON(ctx, []string{"config", "init", "--name", profile, "--app-id", appID, "--app-secret-stdin", "--brand", brand, "--lang", "zh_cn", "--json"}, []byte(appSecret+"\n"), time.Minute)
	if err != nil {
		return nil, err
	}
	return map[string]any{"status": "configured", "flow": "existing-app", "profile": profile, "brand": brand, "larkCliProfile": "configured", "next": "run_auth_start_user_for_qr_oauth"}, nil
}

func StartAppConfiguration(ctx context.Context, runner CapabilityExecutor, dataRoot, profile string, createNew bool) (map[string]any, error) {
	if createNew {
		return nil, errors.New("此版本暂不支持自动创建专用飞书应用，请在飞书后台创建后接入已有应用")
	}
	if profile == "" {
		profile = "default"
	}
	if profile != "default" {
		return nil, errors.New("官方 CLI 配置必须为 default")
	}
	if _, err := runner.RunAuthJSON(ctx, []string{"auth", "status", "--json"}, nil, 15*time.Second); err != nil {
		return nil, errors.New("existing_app_credentials_required")
	}
	return map[string]any{"status": "configured", "flow": "existing-config", "profile": profile, "next": "run_auth_start_user_for_qr_oauth"}, nil
}

func StartUserAuth(ctx context.Context, runner CapabilityExecutor, dataRoot, scope string) (map[string]any, error) {
	status, err := startUserAuthSession(ctx, runner, dataRoot, scope)
	return authStatusMap(status), err
}

func FinishUserAuthFlow(ctx context.Context, runner CapabilityExecutor, dataRoot, deviceCode string) (map[string]any, error) {
	if deviceCode != "" {
		return nil, errors.New("旧版授权续传已停用，请重新发起授权")
	}
	status, err := finishUserAuthSession(ctx, runner, dataRoot)
	return authStatusMap(status), err
}

func EnsureCurrentUser(ctx context.Context, runner CapabilityExecutor, store ClientConfigStore) (map[string]any, error) {
	result, err := runner.RunAuthJSON(ctx, []string{"contact", "+search-user", "--user-ids", "me", "--as", "user", "--json"}, nil, time.Minute)
	if err != nil {
		return nil, err
	}
	openID := recursiveText(result, "open_id", "openId")
	if !regexp.MustCompile(`^ou_[A-Za-z0-9_-]+$`).MatchString(openID) {
		return nil, errors.New("cannot determine current Feishu user")
	}
	config, err := store.Load()
	if err != nil {
		return nil, err
	}
	config.MessageTargets["我"] = MessageTarget{Type: "open_id", ID: openID}
	if !contains(config.DirectAllowedAliases, "我") {
		config.DirectAllowedAliases = append(config.DirectAllowedAliases, "我")
	}
	if err := store.Save(config); err != nil {
		return nil, err
	}
	return map[string]any{"status": "configured", "targetAlias": "我"}, nil
}

func AuthPermissions(ctx context.Context, runner CapabilityExecutor) (map[string]any, error) {
	contract, err := RequiredPermissionScopes()
	if err != nil {
		return nil, err
	}
	status, err := runner.RunAuthJSON(ctx, []string{"auth", "status", "--verify", "--json"}, nil, 30*time.Second)
	if err != nil {
		return nil, err
	}
	scopes, err := runner.RunAuthJSON(ctx, []string{"auth", "scopes", "--json"}, nil, 30*time.Second)
	if err != nil {
		return nil, err
	}
	verified, _ := status["verified"].(bool)
	identities, _ := status["identities"].(map[string]any)
	bot, _ := identities["bot"].(map[string]any)
	user, _ := identities["user"].(map[string]any)
	botVerified, _ := bot["verified"].(bool)
	botAvailable, _ := bot["available"].(bool)
	userVerified, _ := user["verified"].(bool)
	userAvailable, _ := user["available"].(bool)
	oauthGranted := stringList(user["scope"])
	appGranted := stringList(scopes["userScopes"])
	appReportAvailable := scopes["userScopes"] != nil
	effectiveGranted := append([]string{}, oauthGranted...)
	if appReportAvailable {
		allowed := map[string]bool{}
		for _, value := range appGranted {
			allowed[value] = true
		}
		effectiveGranted = effectiveGranted[:0]
		for _, value := range oauthGranted {
			if allowed[value] {
				effectiveGranted = append(effectiveGranted, value)
			}
		}
	}
	userComparison := comparePermissionScopes(contract.User, effectiveGranted)
	var application any
	if appReportAvailable {
		application = comparePermissionScopes(contract.User, appGranted)
	}
	return map[string]any{"status": "ok", "permissions": map[string]any{
		"verified": verified,
		"identities": map[string]any{
			"bot":  map[string]any{"ready": botVerified && botAvailable, "requiredCount": len(contract.Bot), "scopeVerification": "not_exposed_by_lark_cli_auth_scopes", "required": contract.Bot},
			"user": map[string]any{"ready": userVerified && userAvailable, "requiredCount": userComparison.RequiredCount, "grantedCount": userComparison.GrantedCount, "missing": userComparison.Missing, "excess": userComparison.Excess, "complete": userComparison.Complete, "application": application, "oauth": comparePermissionScopes(contract.User, oauthGranted)},
		},
		"note": "User completeness requires both application permissions and user OAuth grants; extra scopes are not bridge capabilities.",
	}}, nil
}

func runQRCode(ctx context.Context, runner CapabilityExecutor, url, path string) error {
	if !validAuthVerificationURL(url) {
		return errors.New("二维码链接无效")
	}
	command := exec.CommandContext(ctx, runner.Binary, "auth", "qrcode", url, "--output", filepath.Base(path), "--size", "360")
	command.Dir = filepath.Dir(path)
	command.Env = authEnvironment()
	command.WaitDelay = 2 * time.Second
	output := boundedCommandBuffer{limit: maximumAuthOutputBytes}
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		return errors.New("二维码生成失败，请使用官方授权链接")
	}
	if output.overflow {
		return errors.New("二维码输出超出安全限制")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("QR code was not created safely")
	}
	return os.Chmod(path, 0o600)
}
func firstHTTPURL(value any) string {
	data, _ := json.Marshal(value)
	return regexp.MustCompile(`https?://[^\s"'<>]+`).FindString(string(data))
}
func recursiveText(value any, names ...string) string {
	switch item := value.(type) {
	case map[string]any:
		for _, name := range names {
			if text, ok := item[name].(string); ok && text != "" {
				return text
			}
		}
		for _, child := range item {
			if text := recursiveText(child, names...); text != "" {
				return text
			}
		}
	case []any:
		for _, child := range item {
			if text := recursiveText(child, names...); text != "" {
				return text
			}
		}
	}
	return ""
}
