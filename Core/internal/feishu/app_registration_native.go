package feishu

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

const nativeRegistrationEndpoint = "https://accounts.feishu.cn/oauth/v1/app/registration"

type nativeRegistrationStart struct {
	DeviceCode              string
	UserCode                string
	VerificationURIComplete string
	ExpiresIn               int
	Interval                int
}

type nativeRegistrationResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

func startNativeAppConfiguration(ctx context.Context, dataRoot, profile string) (map[string]any, error) {
	if ctx.Err() != nil || profile != "" && profile != "default" {
		return nil, appConfigurationNotStarted("创建应用配置无效，只支持内部 default 配置")
	}
	if err := appCreationBusinessGuard(dataRoot); err != nil {
		return nil, appConfigurationNotStarted(err.Error())
	}
	credentialPath := officialCredentialPath(dataRoot)
	if _, err := os.Lstat(credentialPath); !errors.Is(err, os.ErrNotExist) {
		return nil, appConfigurationNotStarted("飞书应用已有配置或配置不可读，请沿用当前应用；扫码新建不会覆盖已有配置")
	}
	appConfigurationSessions.Lock()
	if existing := appConfigurationSessions.items[dataRoot]; existing != nil {
		appConfigurationSessions.Unlock()
		if _, err := awaitUserAuthStart(ctx, existing.userAuthSession); err != nil {
			return nil, err
		}
		return FinishAppConfiguration(ctx, CapabilityExecutor{}, dataRoot)
	}
	if len(appConfigurationSessions.items) >= 4 {
		appConfigurationSessions.Unlock()
		return nil, appConfigurationNotStarted("创建会话已达上限，请取消后重试")
	}
	processCtx, cancel := context.WithTimeout(context.Background(), maximumAuthSessionDuration)
	started, err := requestNativeRegistration(processCtx)
	if err != nil {
		cancel()
		appConfigurationSessions.Unlock()
		return nil, appConfigurationNotStarted("飞书扫码入口暂不可用，请稍后重试")
	}
	verification, err := nativeRegistrationURL(started.VerificationURIComplete)
	if err != nil {
		cancel()
		appConfigurationSessions.Unlock()
		return nil, appConfigurationNotStarted("飞书扫码入口不兼容")
	}
	png, err := qrcode.Encode(verification, qrcode.Medium, 256)
	if err != nil {
		cancel()
		appConfigurationSessions.Unlock()
		return nil, appConfigurationNotStarted("二维码不可用")
	}
	status := emptyAuthStatus("pending")
	status.Flow, status.Identity, status.VerificationURL, status.UserCode, status.QRDataURL = "app-create", "bot", verification, started.UserCode, "data:image/png;base64,"+base64.StdEncoding.EncodeToString(png)
	session := &appConfigurationSession{userAuthSession: &userAuthSession{status: status, cancel: cancel, ready: make(chan struct{}), done: make(chan struct{}), configurationStartedAt: time.Now()}, configPath: credentialPath}
	close(session.ready)
	appConfigurationSessions.items[dataRoot] = session
	appConfigurationSessions.Unlock()
	go finishNativeAppRegistration(processCtx, dataRoot, started, session)
	return FinishAppConfiguration(ctx, CapabilityExecutor{}, dataRoot)
}

func requestNativeRegistration(ctx context.Context) (nativeRegistrationStart, error) {
	form := url.Values{"action": {"begin"}, "archetype": {"PersonalAgent"}, "auth_method": {"client_secret"}, "request_user_info": {"open_id"}}
	var result nativeRegistrationResponse
	if err := nativeRegistrationRequest(ctx, form, &result); err != nil || result.DeviceCode == "" || result.VerificationURIComplete == "" {
		return nativeRegistrationStart{}, errors.New("registration response incomplete")
	}
	if result.ExpiresIn <= 0 || result.ExpiresIn > 900 {
		result.ExpiresIn = 600
	}
	if result.Interval <= 0 || result.Interval > 60 {
		result.Interval = 5
	}
	return nativeRegistrationStart{DeviceCode: result.DeviceCode, UserCode: result.UserCode, VerificationURIComplete: result.VerificationURIComplete, ExpiresIn: result.ExpiresIn, Interval: result.Interval}, nil
}

func nativeRegistrationURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "open.feishu.cn" || parsed.RawPath != "" || !validAuthVerificationURL(raw) {
		return "", errors.New("invalid registration URL")
	}
	query := parsed.Query()
	switch parsed.Path {
	case "/page/cli":
		if !validAppRegistrationURL(raw) {
			return "", errors.New("invalid registration URL")
		}
	case "/page/launcher":
		if len(query) != 1 || len(query["user_code"]) != 1 || !regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`).MatchString(query.Get("user_code")) {
			return "", errors.New("invalid registration URL")
		}
	default:
		return "", errors.New("invalid registration URL")
	}
	addons := map[string]any{
		"scopes":    map[string]any{"tenant": BaseConnectionPermissionScopes()},
		"events":    map[string]any{"items": map[string]any{"tenant": []string{"im.message.receive_v1"}}},
		"callbacks": map[string]any{"items": []string{"card.action.trigger"}},
	}
	body, _ := json.Marshal(addons)
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	writer.Header.ModTime, writer.Header.OS = time.Unix(0, 0), 255
	if _, err := writer.Write(body); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	query.Set("from", "sdk")
	query.Set("tp", "sdk")
	query.Set("source", "go-sdk/ksfassistant")
	// The official registration page defaults to offering both creation and
	// selection of an existing app.  Never carry a createOnly value from a
	// legacy verification URL: true hides the existing-app path, while omission
	// preserves the cross-platform flow users already had on macOS.
	query.Del("createOnly")
	query.Set("addons", base64.RawURLEncoding.EncodeToString(compressed.Bytes()))
	parsed.RawQuery = query.Encode()
	if !validAuthVerificationURL(parsed.String()) {
		return "", errors.New("invalid registration URL")
	}
	return parsed.String(), nil
}

func finishNativeAppRegistration(ctx context.Context, dataRoot string, started nativeRegistrationStart, session *appConfigurationSession) {
	defer close(session.done)
	deadline, interval := time.Now().Add(time.Duration(started.ExpiresIn)*time.Second), started.Interval
	for time.Now().Before(deadline) && ctx.Err() == nil {
		result, err := pollNativeRegistration(ctx, started.DeviceCode)
		if err == nil {
			code := stringValue(result["error"])
			if code == "slow_down" {
				interval = min(interval+5, 60)
			}
			if code == "access_denied" || code == "expired_token" || code == "invalid_grant" {
				session.finishNativeFailure("创建未完成或已过期，请重新发起")
				return
			}
			if code == "" {
				appID, secret := stringValue(result["client_id"]), stringValue(result["client_secret"])
				userInfo, _ := result["user_info"].(map[string]any)
				openID, brand := stringValue(userInfo["open_id"]), brandOrDefault(stringValue(userInfo["tenant_brand"]))
				if appID != "" && secret != "" {
					if openID == "" {
						session.finishNativeFailure("飞书未返回扫码用户身份，请重新扫码接入")
						return
					}
					if brand != "feishu" {
						session.finishNativeFailure("当前仅支持飞书租户")
						return
					}
					if err := StoreOfficialCredentials(dataRoot, appID, secret, brand); err != nil {
						session.finishNativeFailure("创建成功但本机凭据保存失败，请重新接入")
						return
					}
					if err := bindRegistrationOperator(dataRoot, appID, openID); err != nil {
						session.finishNativeFailure("应用已创建，但本人绑定保存失败")
						return
					}
					data, readErr := appConfigurationBytes(session.configPath)
					session.mu.Lock()
					if readErr == nil {
						session.configHash = sha256.Sum256(data)
						session.status.Status, session.status.ProfileValid, session.operatorBound = "completed", true, true
					} else {
						session.err, session.status.Status = errors.New("创建凭据落盘校验失败"), "failed"
					}
					session.status.VerificationURL, session.status.UserCode, session.status.QRDataURL = "", "", ""
					session.mu.Unlock()
					return
				}
			}
		}
		timer := time.NewTimer(time.Duration(interval) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			session.finishNativeFailure("创建结果未确认或已取消、过期；请先在飞书后台确认")
			return
		case <-timer.C:
		}
	}
	session.finishNativeFailure("创建结果未确认或已过期，请重新发起")
}

func pollNativeRegistration(ctx context.Context, deviceCode string) (map[string]any, error) {
	result := map[string]any{}
	err := nativeRegistrationRequest(ctx, url.Values{"action": {"poll"}, "device_code": {deviceCode}}, &result)
	return result, err
}

func nativeRegistrationRequest(ctx context.Context, form url.Values, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, nativeRegistrationEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maximumAuthOutputBytes+1))
	if err != nil || len(data) > maximumAuthOutputBytes || resp.StatusCode >= 500 {
		return errors.New("registration response invalid")
	}
	if json.Unmarshal(data, result) != nil {
		return errors.New("registration response invalid")
	}
	return nil
}

func (session *appConfigurationSession) finishNativeFailure(message string) {
	session.mu.Lock()
	defer session.mu.Unlock()
	session.err, session.status.Status = errors.New(message), "failed"
	session.status.VerificationURL, session.status.UserCode, session.status.QRDataURL = "", "", ""
}
