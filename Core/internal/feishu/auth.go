package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type userAuthState struct {
	SchemaVersion   int       `json:"schemaVersion"`
	Flow            string    `json:"flow"`
	StartedAt       time.Time `json:"startedAt"`
	VerificationURL string    `json:"verificationUrl"`
	UserCode        string    `json:"userCode"`
	DeviceCode      string    `json:"deviceCode"`
	QRPath          string    `json:"qrPath"`
}

func (runner CapabilityExecutor) RunAuthJSON(ctx context.Context, args []string, stdin []byte, timeout time.Duration) (map[string]any, error) {
	if runner.Binary == "" {
		return nil, errors.New("lark-cli binary is required")
	}
	if len(args) < 2 || !contains([]string{"auth", "contact", "config"}, args[0]) {
		return nil, errors.New("unsupported lark auth command")
	}
	full := []string{}
	if runner.Profile != "" && args[0] != "config" {
		full = append(full, "--profile", runner.Profile)
	}
	full = append(full, args...)
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(commandCtx, runner.Binary, full...)
	command.Dir = runner.WorkingDirectory
	command.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("lark-cli auth failed: %s", safeCommandError(stderr.String(), stdout.String()))
	}
	data := stdout.Bytes()
	if len(bytes.TrimSpace(data)) == 0 {
		data = stderr.Bytes()
	}
	var result map[string]any
	if json.Unmarshal(data, &result) != nil {
		if args[0] == "config" {
			return map[string]any{}, nil
		}
		return nil, errors.New("lark-cli auth returned non-json")
	}
	return result, nil
}

func ConfigureExistingApp(ctx context.Context, runner CapabilityExecutor, appID, appSecret, brand, profile string) (map[string]any, error) {
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{3,128}$`).MatchString(appID) || strings.TrimSpace(appSecret) == "" {
		return nil, errors.New("invalid existing app credentials")
	}
	if brand != "lark" {
		brand = "feishu"
	}
	if profile == "" {
		profile = "default"
	}
	_, err := runner.RunAuthJSON(ctx, []string{"config", "init", "--name", profile, "--app-id", appID, "--app-secret-stdin", "--brand", brand, "--lang", "zh_cn", "--json"}, []byte(appSecret+"\n"), time.Minute)
	if err != nil {
		return nil, err
	}
	return map[string]any{"status": "configured", "flow": "existing-app", "profile": profile, "brand": brand, "larkCliProfile": "configured", "windowsSdkCredential": "not_required", "next": "run_auth_start_user_for_qr_oauth"}, nil
}

func StartAppConfiguration(ctx context.Context, runner CapabilityExecutor, dataRoot, profile string, createNew bool) (map[string]any, error) {
	if profile == "" {
		profile = "default"
	}
	if !createNew {
		if _, err := runner.RunAuthJSON(ctx, []string{"auth", "status", "--json"}, nil, 15*time.Second); err != nil {
			return nil, errors.New("existing_app_credentials_required")
		}
		return map[string]any{"status": "configured", "flow": "existing-config", "profile": profile, "next": "run_auth_start_user_for_qr_oauth"}, nil
	}
	authRoot := filepath.Join(dataRoot, "auth")
	if err := ensurePrivateDirectory(authRoot); err != nil {
		return nil, err
	}
	logPath := filepath.Join(authRoot, "config-init.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	command := exec.Command(runner.Binary, "config", "init", "--new", "--name", profile, "--lang", "zh_cn")
	command.Dir = runner.WorkingDirectory
	command.Stdin = nil
	command.Stdout = logFile
	command.Stderr = logFile
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		return nil, err
	}
	_ = command.Process.Release()
	_ = logFile.Close()
	deadline := time.Now().Add(15 * time.Second)
	verification := ""
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(logPath)
		verification = regexp.MustCompile(`https?://[^\s"'<>]+`).FindString(string(data))
		if verification != "" {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	if verification == "" {
		return nil, fmt.Errorf("lark-cli config init did not return a verification URL; see private log: %s", logPath)
	}
	qrPath := filepath.Join(authRoot, "config-init.png")
	if err := runQRCode(ctx, runner, verification, qrPath); err != nil {
		return nil, err
	}
	statePath := filepath.Join(authRoot, "config-init.json")
	if err := writePrivateJSON(statePath, map[string]any{"schemaVersion": 1, "flow": "config-init", "profile": profile, "verificationUrl": verification, "qrPath": qrPath, "logPath": logPath, "startedAt": time.Now().UTC()}); err != nil {
		return nil, err
	}
	return map[string]any{"status": "pending", "flow": "config-init", "profile": profile, "verificationUrl": verification, "qrPath": qrPath, "statePath": statePath, "next": "scan_qr_then_run_auth_start_user"}, nil
}

func StartUserAuth(ctx context.Context, runner CapabilityExecutor, dataRoot, scope string) (map[string]any, error) {
	args := []string{"auth", "login", "--no-wait", "--json"}
	if scope == "" || scope == "required" {
		contract, err := RequiredPermissionScopes()
		if err != nil {
			return nil, err
		}
		args = append(args, "--scope", strings.Join(contract.User, ","))
	} else if scope != "recommend" {
		args = append(args, "--scope", scope)
	} else {
		args = append(args, "--recommend")
	}
	result, err := runner.RunAuthJSON(ctx, args, nil, 20*time.Second)
	if err != nil {
		return nil, err
	}
	verification := firstHTTPURL(result)
	device := recursiveText(result, "device_code", "deviceCode")
	userCode := recursiveText(result, "user_code", "userCode")
	if verification == "" || device == "" {
		return nil, errors.New("lark-cli auth login did not return verification data")
	}
	authRoot := filepath.Join(dataRoot, "auth")
	if err := ensurePrivateDirectory(authRoot); err != nil {
		return nil, err
	}
	qrPath := filepath.Join(authRoot, "user-oauth.png")
	if err := runQRCode(ctx, runner, verification, qrPath); err != nil {
		return nil, err
	}
	state := userAuthState{SchemaVersion: 1, Flow: "user-oauth", StartedAt: time.Now().UTC(), VerificationURL: verification, UserCode: userCode, DeviceCode: device, QRPath: qrPath}
	statePath := filepath.Join(authRoot, "user-oauth.json")
	if err := writePrivateJSON(statePath, state); err != nil {
		return nil, err
	}
	return map[string]any{"status": "pending", "flow": "user-oauth", "verificationUrl": verification, "userCode": userCode, "qrPath": qrPath, "statePath": statePath, "next": "scan_qr_then_run_auth_finish_user"}, nil
}

func FinishUserAuthFlow(ctx context.Context, runner CapabilityExecutor, dataRoot, deviceCode string) (map[string]any, error) {
	if deviceCode == "" {
		var state userAuthState
		missing, err := readPrivateJSON(filepath.Join(dataRoot, "auth", "user-oauth.json"), &state)
		if err != nil || missing {
			return nil, errors.New("device code is unavailable; run auth start-user first")
		}
		deviceCode = state.DeviceCode
	}
	result, err := runner.RunAuthJSON(ctx, []string{"auth", "login", "--device-code", deviceCode, "--json"}, nil, 2*time.Minute)
	if err != nil {
		return nil, err
	}
	return map[string]any{"status": "completed", "flow": "user-oauth", "result": result}, nil
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
	effectiveGranted := oauthGranted
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
	command := exec.CommandContext(ctx, runner.Binary, "auth", "qrcode", url, "--output", filepath.Base(path), "--size", "360")
	command.Dir = filepath.Dir(path)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		return fmt.Errorf("QR code generation failed: %s", safeCommandError(output.String()))
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
