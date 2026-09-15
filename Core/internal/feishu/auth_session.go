package feishu

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"ksfassistant/core/internal/feishuprotocol"
	"ksfassistant/core/internal/userapproval"
)

type userAuthSession struct {
	configurationID        string
	configurationStartedAt time.Time
	mu                     sync.Mutex
	status                 feishuprotocol.AuthStatus
	cancel                 context.CancelFunc
	ready                  chan struct{}
	done                   chan struct{}
	err                    error
	expectedScopes         []string
	authorizationRequestID string
	temporaryOperatorBind  bool
	authorizedOpenID       string
}

const maximumAuthSessionDuration = 10 * time.Minute

var userAuthSessions = struct {
	sync.Mutex
	items map[string]*userAuthSession
}{items: map[string]*userAuthSession{}}

func currentUserAuthSession(dataRoot string) *userAuthSession {
	userAuthSessions.Lock()
	defer userAuthSessions.Unlock()
	return userAuthSessions.items[dataRoot]
}

func (session *userAuthSession) snapshot() feishuprotocol.AuthStatus {
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.status
}

func CancelUserAuthFlow(dataRoot string) {
	userAuthSessions.Lock()
	session := userAuthSessions.items[dataRoot]
	delete(userAuthSessions.items, dataRoot)
	userAuthSessions.Unlock()
	if session != nil {
		session.cancel()
		<-session.done
	}
}

func startUserAuthSession(ctx context.Context, runner CapabilityExecutor, dataRoot, scope string) (feishuprotocol.AuthStatus, error) {
	if scope != "" && scope != "required" {
		return emptyAuthStatus("failed"), errors.New("agent_business_authorization_removed")
	}
	temporaryOperatorBind := true
	if runner.Binary == "" || runner.Profile != "" && runner.Profile != "default" || dataRoot == "" {
		return emptyAuthStatus("failed"), errors.New("授权配置无效")
	}
	application, scopeErr := runner.RunAuthJSON(ctx, []string{"auth", "scopes", "--json"}, nil, 15*time.Second)
	if scopeErr != nil {
		return emptyAuthStatus("failed"), errors.New("application_permissions_unverified")
	}
	profile, err := runner.RunAuthJSON(ctx, []string{"auth", "status", "--json"}, nil, 10*time.Second)
	appID, _ := profile["appId"].(string)
	if err != nil || profile["brand"] != "feishu" || strings.TrimSpace(appID) == "" {
		return emptyAuthStatus("failed"), errors.New("请先接入 default 配置的飞书应用，再发起用户授权")
	}
	if application["appId"] != appID || application["brand"] != "feishu" || application["tokenType"] != "user" {
		return emptyAuthStatus("failed"), errors.New("application_permissions_unverified")
	}
	if !validPermissionScopeValue(application["userScopes"]) {
		return emptyAuthStatus("failed"), errors.New("application_permissions_unverified")
	}
	requested, err := loginPermissionScopes(stringList(application["userScopes"]))
	if err != nil {
		return emptyAuthStatus("failed"), err
	}
	userAuthSessions.Lock()
	if existing := userAuthSessions.items[dataRoot]; existing != nil {
		select {
		case <-existing.done:
			delete(userAuthSessions.items, dataRoot)
		default:
			userAuthSessions.Unlock()
			return awaitUserAuthStart(ctx, existing)
		}
	}
	if len(userAuthSessions.items) >= 4 {
		userAuthSessions.Unlock()
		return emptyAuthStatus("failed"), errors.New("授权会话已达上限，请取消后重试")
	}
	releaseAuthorization, err := userapproval.TryExecutionLease(dataRoot)
	if err != nil {
		userAuthSessions.Unlock()
		return emptyAuthStatus("failed"), err
	}
	processCtx, cancel := context.WithTimeout(context.Background(), maximumAuthSessionDuration)
	session := &userAuthSession{status: emptyAuthStatus("pending"), cancel: cancel, ready: make(chan struct{}), done: make(chan struct{}), configurationStartedAt: time.Now(), expectedScopes: append([]string{}, requested...), temporaryOperatorBind: temporaryOperatorBind}
	session.status.Flow = "user-oauth"
	session.status.ProfileValid = true
	command := exec.Command(runner.Binary, "--profile", "default", "auth", "login", "--scope", strings.Join(requested, " "), "--json")
	command.Env = authEnvironment(runner.DataRoot)
	command.Dir = runner.WorkingDirectory
	command.Stderr = io.Discard
	command.WaitDelay = 2 * time.Second
	prepareProcessTree(command)
	stdout, err := command.StdoutPipe()
	if err == nil {
		err = command.Start()
	}
	if err != nil {
		if stdout != nil {
			_ = stdout.Close()
		}
		cancel()
		releaseAuthorization()
		userAuthSessions.Unlock()
		return emptyAuthStatus("failed"), errors.New("无法启动官方授权，请检查工具链后重试")
	}
	tree, err := attachProcessTree(command)
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		cancel()
		releaseAuthorization()
		userAuthSessions.Unlock()
		return emptyAuthStatus("failed"), errors.New("无法安全管理授权进程")
	}
	userAuthSessions.items[dataRoot] = session
	userAuthSessions.Unlock()
	processExited := make(chan struct{})
	watcherStopped := make(chan struct{})
	go func() {
		defer close(watcherStopped)
		select {
		case <-processCtx.Done():
			killProcessTree(tree, command)
			_ = stdout.Close()
		case <-processExited:
		}
	}()
	go func() {
		defer releaseAuthorization()
		completed, streamErr := session.readEvents(stdout)
		if streamErr != nil {
			cancel()
		}
		waitErr := command.Wait()
		close(processExited)
		<-watcherStopped
		closeProcessTree(tree)
		// The OAuth completion event owns finalization. An early status query
		// must not be the only opportunity to activate the product session.
		if streamErr == nil && waitErr == nil && completed && processCtx.Err() == nil {
			verified, verifyErr := readCLIAuthStatusForScopes(processCtx, runner, requested)
			if verifyErr != nil || processCtx.Err() != nil || !verified.IdentityValid || !verified.ProfileValid || len(verified.MissingCapabilities) > 0 {
				streamErr = errors.New("authorization_not_verified")
			}
		}
		session.mu.Lock()
		if streamErr != nil || waitErr != nil || !completed || processCtx.Err() != nil {
			session.err = errors.New("授权未完成或已过期，请重新发起授权")
			session.status.Status = "failed"
		} else {
			session.status.Status = "completed"
		}
		session.status.VerificationURL = ""
		session.status.UserCode = ""
		session.status.QRDataURL = ""
		session.mu.Unlock()
		releaseAuthorization()
		close(session.done)
		cancel()
	}()
	status, err := awaitUserAuthStart(ctx, session)
	if err == nil && status.VerificationURL != "" {
		if qr, qrErr := userAuthQR(ctx, runner, status.VerificationURL); qrErr == nil {
			session.mu.Lock()
			if session.status.Status == "pending" {
				session.status.QRDataURL = qr
			}
			status = session.status
			session.mu.Unlock()
		}
	}
	return status, err
}

func awaitUserAuthStart(ctx context.Context, session *userAuthSession) (feishuprotocol.AuthStatus, error) {
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	select {
	case <-session.ready:
		return session.snapshot(), nil
	case <-session.done:
		session.mu.Lock()
		defer session.mu.Unlock()
		return session.status, session.err
	case <-ctx.Done():
		session.cancel()
		return emptyAuthStatus("failed"), errors.New("授权请求已取消，请重新发起")
	case <-timer.C:
		session.cancel()
		return emptyAuthStatus("failed"), errors.New("授权页面暂不可用，请重试")
	}
}

func (session *userAuthSession) readEvents(reader io.Reader) (bool, error) {
	decoder := json.NewDecoder(io.LimitReader(reader, maximumAuthOutputBytes+1))
	ready, completed := false, false
	for count := 0; count < 16; count++ {
		var event map[string]any
		err := decoder.Decode(&event)
		if err == io.EOF {
			return completed, nil
		}
		if err != nil || event == nil || authResultHasSecret(event) || decoder.InputOffset() > maximumAuthOutputBytes {
			return false, errors.New("授权事件格式无效")
		}
		switch event["event"] {
		case "device_authorization":
			verification, _ := event["verification_uri_complete"].(string)
			code, _ := event["user_code"].(string)
			if ready || !validAuthVerificationURL(verification) || !regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`).MatchString(code) {
				return false, errors.New("授权页面无效")
			}
			session.mu.Lock()
			session.status.VerificationURL = verification
			session.status.UserCode = code
			session.mu.Unlock()
			ready = true
			close(session.ready)
		case "authorization_complete":
			if !ready || completed {
				return false, errors.New("授权事件顺序无效")
			}
			if openID, _ := event["user_open_id"].(string); regexp.MustCompile(`^ou_[A-Za-z0-9_-]{1,252}$`).MatchString(openID) {
				session.mu.Lock()
				session.authorizedOpenID = openID
				session.mu.Unlock()
			}
			completed = true
		case "authorization_failed":
			return false, errors.New("授权未完成")
		default:
			return false, errors.New("授权事件不兼容")
		}
	}
	return false, errors.New("授权事件超出安全限制")
}

func validAuthVerificationURL(value string) bool {
	if len(value) > 4096 {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" {
		return false
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil || parsed.Fragment != "" {
		return false
	}
	for key := range query {
		normalized := strings.ToLower(strings.ReplaceAll(key, "_", ""))
		if strings.Contains(normalized, "token") || strings.Contains(normalized, "secret") || normalized == "devicecode" {
			return false
		}
	}
	host := strings.ToLower(parsed.Hostname())
	for _, suffix := range []string{"feishu.cn", "larksuite.com", "larkoffice.com"} {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}

func userAuthQR(ctx context.Context, runner CapabilityExecutor, verification string) (string, error) {
	root, err := os.MkdirTemp("", "ksfas-auth-qr-")
	if err != nil {
		return "", errors.New("二维码不可用")
	}
	defer os.RemoveAll(root)
	path := filepath.Join(root, "authorization.png")
	qrCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := runQRCode(qrCtx, runner, verification, path); err != nil {
		return "", err
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("二维码不可用")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maximumAuthOutputBytes+1))
	if err != nil || len(data) > maximumAuthOutputBytes || len(data) < 8 || string(data[:8]) != "\x89PNG\r\n\x1a\n" {
		return "", errors.New("二维码格式无效")
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(data), nil
}

func finishUserAuthSession(ctx context.Context, runner CapabilityExecutor, dataRoot string) (feishuprotocol.AuthStatus, error) {
	session := currentUserAuthSession(dataRoot)
	if session == nil {
		return emptyAuthStatus("failed"), errors.New("授权会话已失效，请重新发起授权")
	}
	select {
	case <-session.done:
		session.mu.Lock()
		err := session.err
		session.mu.Unlock()
		if err != nil {
			return emptyAuthStatus("failed"), err
		}
		release, err := userapproval.TryExecutionLease(dataRoot)
		if err != nil {
			return emptyAuthStatus("unknown"), err
		}
		defer release()
		return readCLIAuthStatusForScopes(ctx, runner, session.expectedScopes)
	default:
		return session.snapshot(), nil
	}
}
