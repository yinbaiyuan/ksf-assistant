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
	command.Env = authEnvironment(runner.DataRoot)
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
	if err := ctx.Err(); err != nil {
		return nil, errors.New("应用接入请求已取消")
	}
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
	if profile != "default" {
		return nil, errors.New("官方 CLI 配置必须为 default")
	}
	if _, err := newAppConfigurationPath(runner.DataRoot); err != nil {
		return nil, err
	}
	CancelUserAuthFlow(runner.DataRoot)
	_, err := runner.RunAuthJSON(ctx, []string{"config", "init", "--name", profile, "--app-id", appID, "--app-secret-stdin", "--brand", brand, "--lang", "zh_cn"}, []byte(appSecret+"\n"), time.Minute)
	if err != nil {
		return nil, err
	}
	return map[string]any{"status": "configured", "flow": "existing-app", "profile": profile, "brand": brand, "larkCliProfile": "configured", "next": "run_auth_start_user_for_qr_oauth"}, nil
}

func StartAppConfiguration(ctx context.Context, runner CapabilityExecutor, dataRoot, profile string, createNew bool) (map[string]any, error) {
	if createNew {
		return startAppConfiguration(ctx, runner, dataRoot, profile)
	}
	if profile == "" {
		profile = "default"
	}
	if profile != "default" {
		return nil, errors.New("官方 CLI 配置必须为 default")
	}
	current, err := runner.RunAuthJSON(ctx, []string{"auth", "status", "--json"}, nil, 15*time.Second)
	appID, _ := current["appId"].(string)
	if err != nil || current["brand"] != "feishu" || !regexp.MustCompile(`^cli_[A-Za-z0-9_-]{1,124}$`).MatchString(appID) {
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
	session := currentUserAuthSession(dataRoot)
	status, err := finishUserAuthSession(ctx, runner, dataRoot)
	if err == nil && status.Status == "authorized" && status.IdentityValid && session != nil {
		session.mu.Lock()
		temporaryOperatorBind := session.temporaryOperatorBind
		authorizationRequestID := session.authorizationRequestID
		authorizedOpenID := session.authorizedOpenID
		session.mu.Unlock()
		if !temporaryOperatorBind {
			request, requestErr := readProgressiveAuthorizationRequest(dataRoot)
			if requestErr != nil || request == nil || request.ID != authorizationRequestID {
				err = errors.New("渐进授权回执与本地请求不匹配")
			} else if removeErr := removeProgressiveAuthorizationRequest(dataRoot); removeErr != nil {
				err = removeErr
			}
			return authStatusMap(status), err
		}
		raw, readErr := runner.RunAuthJSON(ctx, []string{"auth", "status", "--verify", "--json"}, nil, 15*time.Second)
		identities, _ := raw["identities"].(map[string]any)
		user, _ := identities["user"].(map[string]any)
		openID, _ := user["openId"].(string)
		if openID == "" {
			openID, _ = user["open_id"].(string)
		}
		if openID == "" {
			openID = authorizedOpenID
		}
		appID, _ := raw["appId"].(string)
		if readErr != nil || raw["brand"] != "feishu" {
			err = errors.New("补充本人授权结果无法核验")
		} else if bindErr := bindRegistrationOperator(dataRoot, appID, openID); bindErr != nil {
			err = bindErr
		} else if _, logoutErr := runner.RunAuthJSON(ctx, []string{"auth", "logout", "--json"}, nil, 30*time.Second); logoutErr != nil {
			err = errors.New("本人已绑定，但临时用户令牌尚未清除")
		}
	}
	return authStatusMap(status), err
}

func EnsureCurrentUser(ctx context.Context, runner CapabilityExecutor, store ClientConfigStore) (map[string]any, error) {
	return nil, errors.New("绑定操作人必须携带已确认的应用和身份上下文")
}

type OperatorBindingExpectation struct {
	IdentityRevision string `json:"identityRevision"`
	ContextRevision  string `json:"contextRevision"`
	ApplicationID    string `json:"applicationId"`
}

var ErrOperatorContextConflict = errors.New("确认时的应用和身份上下文已变化，未绑定操作人")

func (expected OperatorBindingExpectation) Valid() bool {
	digest := regexp.MustCompile(`^[a-f0-9]{64}$`)
	return digest.MatchString(expected.IdentityRevision) && digest.MatchString(expected.ContextRevision) && regexp.MustCompile(`^cli_[A-Za-z0-9_]{1,124}$`).MatchString(expected.ApplicationID)
}

func EnsureCurrentUserWithExpected(ctx context.Context, runner CapabilityExecutor, store ClientConfigStore, expected OperatorBindingExpectation) (map[string]any, error) {
	if !expected.Valid() {
		return nil, errors.New("缺少有效的已确认应用和身份上下文")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	release, err := userapproval.TryExecutionLease(runner.DataRoot)
	if err != nil {
		return nil, err
	}
	defer release()
	config, err := store.Load()
	if err != nil {
		return nil, err
	}
	if err := store.saveWithGuard(config, func(current ClientConfig, next *ClientConfig) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		presence, stamp, err := configurationFileEvidenceFor(runner.DataRoot)
		if err != nil || presence != "present" {
			return ErrOperatorContextConflict
		}
		raw, err := runner.RunAuthJSON(ctx, []string{"auth", "status", "--verify", "--json"}, nil, 15*time.Second)
		if err != nil {
			return err
		}
		identities, _ := raw["identities"].(map[string]any)
		user, _ := identities["user"].(map[string]any)
		openID, _ := user["openId"].(string)
		if openID == "" {
			openID, _ = user["open_id"].(string)
		}
		if raw["appId"] != expected.ApplicationID || raw["brand"] != "feishu" || configurationIdentityState(user) != "present" || !regexp.MustCompile(`^ou_[A-Za-z0-9_-]+$`).MatchString(openID) {
			return ErrOperatorContextConflict
		}
		identityBytes, _ := json.Marshal([]string{expected.ApplicationID, "feishu", "default", openID})
		contextBytes, _ := json.Marshal([]any{expected.ApplicationID, raw["brand"], "default", openID, user["available"], user["verified"], user["scope"], current.DirectAllowedAliases, current.MessageTargets})
		if configurationDigest(string(identityBytes)) != expected.IdentityRevision || configurationDigest(string(contextBytes)) != expected.ContextRevision {
			return ErrOperatorContextConflict
		}
		currentPresence, currentStamp, err := configurationFileEvidenceFor(runner.DataRoot)
		if err != nil || currentPresence != presence || currentStamp != stamp {
			return ErrOperatorContextConflict
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		next.MessageTargets["我"] = MessageTarget{Type: "open_id", ID: openID}
		next.Operator = &OperatorBinding{AppID: expected.ApplicationID, OpenID: openID}
		if !contains(next.DirectAllowedAliases, "我") {
			next.DirectAllowedAliases = append(next.DirectAllowedAliases, "我")
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return map[string]any{"status": "configured", "targetAlias": "我"}, nil
}

func AuthPermissions(ctx context.Context, runner CapabilityExecutor) (map[string]any, error) {
	status, err := runner.RunAuthJSON(ctx, []string{"auth", "status", "--verify", "--json"}, nil, 30*time.Second)
	if err != nil {
		return nil, err
	}
	scopes, scopeErr := runner.RunAuthJSON(ctx, []string{"auth", "scopes", "--json"}, nil, 30*time.Second)
	var verified any
	if value, ok := status["verified"].(bool); ok {
		verified = value
	}
	identities, _ := status["identities"].(map[string]any)
	bot, _ := identities["bot"].(map[string]any)
	user, _ := identities["user"].(map[string]any)
	baseBotScopes := BaseConnectionPermissionScopes()
	botGranted := stringList(scopes["botScopes"])
	botScopeReportAvailable := scopeErr == nil && validPermissionScopeValue(scopes["botScopes"])
	var botApplication any
	if botScopeReportAvailable {
		botApplication = comparePermissionScopes(baseBotScopes, botGranted)
	}
	botReady := authIdentityReady(bot)
	if ready, known := botReady.(bool); known && botScopeReportAvailable {
		verified = ready && comparePermissionScopes(baseBotScopes, botGranted).Complete
	} else {
		verified = nil
	}
	oauthGranted := stringList(user["scope"])
	appGranted := stringList(scopes["userScopes"])
	oauthReportAvailable := validPermissionScopeValue(user["scope"])
	appReportAvailable := scopeErr == nil && validPermissionScopeValue(scopes["userScopes"])
	userRequired := []string{}
	request, requestErr := readProgressiveAuthorizationRequest(runner.DataRoot)
	if requestErr != nil {
		return nil, requestErr
	}
	appID, _ := status["appId"].(string)
	if request != nil && request.ApplicationID == appID {
		userRequired = append(userRequired, request.Scopes...)
	}
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
	userComparison := comparePermissionScopes(userRequired, effectiveGranted)
	var application, oauth, complete, missing any
	if appReportAvailable {
		application = comparePermissionScopes(userRequired, appGranted)
	}
	if oauthReportAvailable {
		oauth = comparePermissionScopes(userRequired, oauthGranted)
	}
	if appReportAvailable && oauthReportAvailable {
		complete, missing = userComparison.Complete, userComparison.Missing
	}
	return map[string]any{"status": "ok", "permissions": map[string]any{
		"verified": verified,
		"identities": map[string]any{
			"bot":  map[string]any{"ready": botReady, "requiredCount": len(baseBotScopes), "scopeVerification": "verified_by_lark_cli_auth_scopes", "required": baseBotScopes, "application": botApplication},
			"user": map[string]any{"ready": authIdentityReady(user), "requiredCount": userComparison.RequiredCount, "grantedCount": userComparison.GrantedCount, "missing": missing, "excess": userComparison.Excess, "complete": complete, "application": application, "oauth": oauth},
		},
		"note": "Base readiness uses only the managed bot messaging/card scope set. User scopes are optional and authorized per operation.",
	}}, nil
}

func authIdentityReady(identity map[string]any) any {
	available, known := identity["available"].(bool)
	if !known {
		return nil
	}
	if !available {
		return false
	}
	verified, known := identity["verified"].(bool)
	if !known {
		return nil
	}
	return verified
}

func validPermissionScopeValue(value any) bool {
	switch scopes := value.(type) {
	case string, []string:
		return true
	case []any:
		for _, scope := range scopes {
			if _, valid := scope.(string); !valid {
				return false
			}
		}
		return true
	}
	return false
}

func runQRCode(ctx context.Context, runner CapabilityExecutor, url, path string) error {
	if !validAuthVerificationURL(url) {
		return errors.New("二维码链接无效")
	}
	command := exec.CommandContext(ctx, runner.Binary, "auth", "qrcode", url, "--output", filepath.Base(path), "--size", "360")
	command.Dir = filepath.Dir(path)
	command.Env = authEnvironment(runner.DataRoot)
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
