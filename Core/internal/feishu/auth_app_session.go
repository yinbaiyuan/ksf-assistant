package feishu

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"ksfassistant/core/internal/privatestore"
	"ksfassistant/core/internal/userapproval"
)

type appConfigurationSession struct {
	*userAuthSession
	configPath                    string
	configHash                    [32]byte
	operatorBound                 bool
	operatorAuthorizationRequired bool
}

type registrationUser struct {
	OpenID      string `json:"openId"`
	TenantBrand string `json:"tenantBrand"`
}

type registrationDetails struct {
	AppID            string
	RegistrationUser *registrationUser
}

type appConfigurationNotStartedError struct{ message string }

func (err *appConfigurationNotStartedError) Error() string { return err.message }

func appConfigurationNotStarted(message string) error {
	return &appConfigurationNotStartedError{message: message}
}

// IsAppConfigurationNotStarted distinguishes a locally rejected request from
// a registration process whose remote result may be uncertain.
func IsAppConfigurationNotStarted(err error) bool {
	var target *appConfigurationNotStartedError
	return errors.As(err, &target)
}

type registrationOperatorRecovery struct {
	SchemaVersion int    `json:"schemaVersion"`
	AppID         string `json:"appId"`
	OpenID        string `json:"openId"`
}

var appConfigurationSessions = struct {
	sync.Mutex
	items map[string]*appConfigurationSession
}{items: map[string]*appConfigurationSession{}}

func CancelAppConfiguration(dataRoot string) {
	appConfigurationSessions.Lock()
	defer appConfigurationSessions.Unlock()
	if session := appConfigurationSessions.items[dataRoot]; session != nil {
		session.mu.Lock()
		session.cancel()
		session.mu.Unlock()
		<-session.done
		delete(appConfigurationSessions.items, dataRoot)
	}
}

func FinishAppConfiguration(ctx context.Context, runner CapabilityExecutor, dataRoot string) (map[string]any, error) {
	appConfigurationSessions.Lock()
	session := appConfigurationSessions.items[dataRoot]
	appConfigurationSessions.Unlock()
	if session == nil {
		return nil, errors.New("创建会话已失效，请先在飞书后台确认结果；不会自动重新创建应用")
	}
	if err := ctx.Err(); err != nil {
		return nil, errors.New("创建结果检查已取消")
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.status.Status == "completed" {
		data, err := appConfigurationBytes(session.configPath)
		if err != nil || sha256.Sum256(data) != session.configHash {
			return nil, errors.New("创建后的应用配置已变化，请重新检查当前应用，不会重新创建")
		}
	}
	result := authStatusMap(session.status)
	result["operatorBound"] = session.operatorBound
	result["operatorAuthorizationRequired"] = session.operatorAuthorizationRequired
	return result, session.err
}

func startAppConfiguration(ctx context.Context, runner CapabilityExecutor, dataRoot, profile string) (map[string]any, error) {
	if ctx.Err() != nil {
		return nil, appConfigurationNotStarted("创建请求已取消")
	}
	if runner.Binary == "" || runner.Profile != "" && runner.Profile != "default" || profile != "" && profile != "default" || !filepath.IsAbs(dataRoot) || dataRoot != runner.DataRoot {
		return nil, appConfigurationNotStarted("创建应用配置无效，只支持受管 default 配置")
	}
	if err := appCreationBusinessGuard(dataRoot); err != nil {
		return nil, appConfigurationNotStarted(err.Error())
	}
	appConfigurationSessions.Lock()
	if existing := appConfigurationSessions.items[dataRoot]; existing != nil {
		appConfigurationSessions.Unlock()
		_, err := awaitUserAuthStart(ctx, existing.userAuthSession)
		if err != nil {
			return nil, err
		}
		return FinishAppConfiguration(ctx, runner, dataRoot)
	}
	if len(appConfigurationSessions.items) >= 4 {
		appConfigurationSessions.Unlock()
		return nil, appConfigurationNotStarted("创建会话已达上限，请取消后重试")
	}
	configPath, err := newAppConfigurationPath(dataRoot)
	if err != nil {
		appConfigurationSessions.Unlock()
		return nil, appConfigurationNotStarted(err.Error())
	}
	if probe := ProbeLarkCLI(ctx, runner.Binary); probe.State != "ready" {
		appConfigurationSessions.Unlock()
		return nil, appConfigurationNotStarted("固定版本官方 CLI 未通过检查，不能创建应用")
	}
	release, err := userapproval.TryExecutionLease(dataRoot)
	if err != nil {
		appConfigurationSessions.Unlock()
		return nil, appConfigurationNotStarted(err.Error())
	}
	stage, err := os.MkdirTemp(filepath.Dir(configPath), ".ksfas-registration-")
	if err == nil {
		err = ensurePrivateDirectory(stage)
	}
	if err != nil {
		release()
		appConfigurationSessions.Unlock()
		return nil, appConfigurationNotStarted("无法安全准备创建应用会话")
	}
	processCtx, cancel := context.WithTimeout(context.Background(), maximumAuthSessionDuration)
	session := &appConfigurationSession{userAuthSession: &userAuthSession{status: emptyAuthStatus("pending"), cancel: cancel, ready: make(chan struct{}), done: make(chan struct{}), configurationStartedAt: time.Now()}, configPath: configPath}
	session.status.Flow = "app-create"
	session.status.Identity = "bot"
	// config init always prints its machine-readable result to stdout. Unlike
	// auth commands, the upstream command has no --json flag.
	command := exec.Command(runner.Binary, "--profile", "default", "config", "init", "--new", "--name", "default", "--brand", "feishu", "--lang", "zh_cn")
	command.Env = appConfigurationEnvironment(dataRoot, stage)
	command.Dir = stage
	command.WaitDelay = 2 * time.Second
	stdout := &boundedCommandBuffer{limit: maximumAuthOutputBytes}
	stderr := &appRegistrationOutput{session: session.userAuthSession}
	command.Stdout, command.Stderr = stdout, stderr
	prepareProcessTree(command)
	if ctx.Err() != nil {
		cancel()
		release()
		_ = os.RemoveAll(stage)
		appConfigurationSessions.Unlock()
		return nil, appConfigurationNotStarted("创建请求已取消")
	}
	if err := command.Start(); err != nil {
		cancel()
		release()
		_ = os.RemoveAll(stage)
		appConfigurationSessions.Unlock()
		return nil, appConfigurationNotStarted("无法启动官方应用创建流程")
	}
	tree, err := attachProcessTree(command)
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		cancel()
		release()
		appConfigurationSessions.Unlock()
		return nil, errors.New("无法安全管理创建进程；请先在飞书后台确认结果")
	}
	appConfigurationSessions.items[dataRoot] = session
	appConfigurationSessions.Unlock()
	processExited := make(chan struct{})
	watcherStopped := make(chan struct{})
	go func() {
		defer close(watcherStopped)
		select {
		case <-processCtx.Done():
			killProcessTree(tree, command)
		case <-processExited:
		}
	}()
	go func() {
		waitErr := command.Wait()
		close(processExited)
		<-watcherStopped
		closeProcessTree(tree)
		session.mu.Lock()
		registration, valid := registrationResultDetails(stdout.Bytes())
		if !stderr.ready {
			// The upstream protocol cannot create or select an application until
			// the verification page has been delivered to the user. A begin
			// failure before that point is therefore definitively retryable even
			// though the local CLI process itself was started.
			session.err = appConfigurationNotStarted("飞书扫码入口未建立，应用创建或选择尚未开始")
		} else if waitErr != nil || stdout.overflow || !valid || processCtx.Err() != nil {
			session.err = errors.New("创建结果未确认或已取消、过期；请先在飞书后台确认，不会自动重试")
		} else if err := appCreationBusinessGuard(dataRoot); err != nil {
			session.err = errors.New("创建期间业务配置已变化；新应用未接入，请先确认结果，不要重复创建")
		} else {
			if registration.RegistrationUser != nil {
				session.err = writeRegistrationOperatorRecovery(dataRoot, registration.AppID, registration.RegistrationUser.OpenID)
			}
			if session.err == nil {
				session.err = publishAppConfiguration(stage, configPath, registration.AppID)
			}
			if session.err == nil && registration.RegistrationUser != nil {
				session.err = bindRegistrationOperator(dataRoot, registration.AppID, registration.RegistrationUser.OpenID)
				if session.err == nil {
					session.operatorBound = true
					_ = os.Remove(registrationOperatorRecoveryPath(dataRoot))
				}
			} else if session.err == nil {
				session.operatorAuthorizationRequired = true
			}
		}
		if session.err == nil {
			data, _ := appConfigurationBytes(filepath.Join(stage, "config.json"))
			session.configHash = sha256.Sum256(data)
			session.status.Status = "completed"
			session.status.ProfileValid = true
			_ = os.RemoveAll(stage)
		} else {
			session.status.Status = "failed"
			if _, err := os.Lstat(filepath.Join(stage, "config.json")); errors.Is(err, os.ErrNotExist) {
				_ = os.RemoveAll(stage)
			}
		}
		session.status.VerificationURL, session.status.UserCode, session.status.QRDataURL = "", "", ""
		session.mu.Unlock()
		release()
		close(session.done)
		cancel()
	}()
	status, err := awaitUserAuthStart(ctx, session.userAuthSession)
	if err != nil {
		CancelAppConfiguration(dataRoot)
		if !stderr.ready {
			return nil, appConfigurationNotStarted(err.Error())
		}
		return nil, err
	}
	if status.VerificationURL != "" {
		if qr, qrErr := userAuthQR(ctx, runner, status.VerificationURL); qrErr == nil {
			session.mu.Lock()
			if session.status.Status == "pending" {
				session.status.QRDataURL = qr
			}
			session.mu.Unlock()
		}
	}
	return FinishAppConfiguration(ctx, runner, dataRoot)
}

func appCreationBusinessGuard(dataRoot string) error {
	settings, err := NewSettingsStore(dataRoot).Load()
	if err != nil || settings.Group.Enabled || settings.MailEvents.Enabled {
		return errors.New("已有业务通道启用或配置不可读，扫码新建不能用于切换应用；请恢复并沿用当前应用")
	}
	failure := errors.New("仍有活动飞书连接，扫码连接不能覆盖；请先注销并完成本地清理")
	if _, err := os.Lstat(logoutCleanupPath(dataRoot)); !errors.Is(err, os.ErrNotExist) {
		return failure
	}
	if config, loadErr := NewClientConfigStore(dataRoot).Load(); loadErr != nil {
		return failure
	} else if config.Operator != nil || len(config.MessageTargets) > 0 || len(config.DirectAllowedAliases) > 0 {
		return failure
	}
	var links struct {
		Links []struct {
			LinkState string `json:"linkState"`
		} `json:"links"`
	}
	if data, readErr := os.ReadFile(filepath.Join(dataRoot, "task-links-v1.json")); readErr == nil {
		if len(data) > maximumPrivateJSONBytes || json.Unmarshal(data, &links) != nil {
			return failure
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return failure
	}
	for _, link := range links.Links {
		if link.LinkState == "active" {
			return failure
		}
	}
	return nil
}

func appConfigurationEnvironment(dataRoot, stage string) []string {
	result := []string{}
	for _, entry := range authEnvironment(dataRoot) {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(name, "LARKSUITE_CLI_CONFIG_DIR") {
			result = append(result, entry)
		}
	}
	return append(result, "LARKSUITE_CLI_CONFIG_DIR="+stage)
}

func newAppConfigurationPath(dataRoot string) (string, error) {
	root, err := ManagedLarkCLIConfigDir(dataRoot)
	if err != nil {
		return "", errors.New("官方 CLI 配置目录不可用")
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return "", errors.New("官方 CLI 配置目录无效")
	}
	if _, err := os.Lstat(filepath.Join(root, "config.json")); !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("官方 CLI 已有配置或配置不可读，请沿用当前应用；扫码新建不会覆盖已有配置")
	}
	if err := ensurePrivateDirectory(root); err != nil {
		return "", errors.New("官方 CLI 配置目录不安全")
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", errors.New("官方 CLI 配置目录不可用")
	}
	return filepath.Join(canonical, "config.json"), nil
}

type appRegistrationOutput struct {
	session *userAuthSession
	pending []byte
	total   int
	ready   bool
}

func (output *appRegistrationOutput) Write(data []byte) (int, error) {
	output.total += len(data)
	if output.total > maximumAuthOutputBytes {
		output.session.cancel()
		return 0, errors.New("创建输出超出安全限制")
	}
	output.pending = append(output.pending, data...)
	for {
		index := bytes.IndexByte(output.pending, '\n')
		if index < 0 {
			break
		}
		line := strings.TrimSpace(string(output.pending[:index]))
		output.pending = output.pending[index+1:]
		if !strings.HasPrefix(line, "https://") {
			continue
		}
		if !validAppRegistrationURL(line) || output.ready {
			output.session.cancel()
			return 0, errors.New("官方应用创建链接不兼容")
		}
		output.session.mu.Lock()
		output.session.status.VerificationURL = line
		output.session.mu.Unlock()
		output.ready = true
		close(output.session.ready)
	}
	return len(data), nil
}

func validAppRegistrationURL(value string) bool {
	if !validAuthVerificationURL(value) {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host != "open.feishu.cn" || parsed.Path != "/page/cli" || parsed.RawPath != "" {
		return false
	}
	query := parsed.Query()
	// The controlled patch intentionally reports the upstream protocol version
	// to Feishu while the binary itself retains its managed distribution version.
	if len(query) != 4 || !regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`).MatchString(query.Get("user_code")) || query.Get("lpv") != PinnedLarkCLIUpstreamVersion || query.Get("ocv") != PinnedLarkCLIUpstreamVersion || query.Get("from") != "cli" {
		return false
	}
	for _, values := range query {
		if len(values) != 1 {
			return false
		}
	}
	return true
}

func registrationResult(data []byte) (string, bool) {
	result, valid := registrationResultDetails(data)
	return result.AppID, valid
}

func registrationResultDetails(data []byte) (registrationDetails, bool) {
	var result struct {
		AppID            string            `json:"appId"`
		AppSecret        string            `json:"appSecret"`
		Brand            string            `json:"brand"`
		RegistrationUser *registrationUser `json:"registrationUser,omitempty"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return registrationDetails{}, false
	}
	valid := result.Brand == "feishu" && result.AppSecret == "****" && regexp.MustCompile(`^cli_[A-Za-z0-9_-]{1,124}$`).MatchString(result.AppID)
	if result.RegistrationUser != nil && (result.RegistrationUser.TenantBrand != "feishu" || !regexp.MustCompile(`^ou_[A-Za-z0-9_-]{1,252}$`).MatchString(result.RegistrationUser.OpenID)) {
		valid = false
	}
	return registrationDetails{AppID: result.AppID, RegistrationUser: result.RegistrationUser}, valid
}

func registrationOperatorRecoveryPath(dataRoot string) string {
	return filepath.Join(dataRoot, "private-cache", "app-operator-recovery-v1.json")
}

func writeRegistrationOperatorRecovery(dataRoot, appID, openID string) error {
	return privatestore.WriteJSON(registrationOperatorRecoveryPath(dataRoot), registrationOperatorRecovery{SchemaVersion: 1, AppID: appID, OpenID: openID})
}

func bindRegistrationOperator(dataRoot, appID, openID string) error {
	if !regexp.MustCompile(`^cli_[A-Za-z0-9_-]{1,124}$`).MatchString(appID) || !regexp.MustCompile(`^ou_[A-Za-z0-9_-]{1,252}$`).MatchString(openID) {
		return errors.New("注册用户身份无效")
	}
	store := NewClientConfigStore(dataRoot)
	config, err := store.Load()
	if err != nil {
		return err
	}
	config.Operator = &OperatorBinding{AppID: appID, OpenID: openID}
	config.MessageTargets["我"] = MessageTarget{Type: "open_id", ID: openID}
	if !contains(config.DirectAllowedAliases, "我") {
		config.DirectAllowedAliases = append(config.DirectAllowedAliases, "我")
	}
	return store.Save(config)
}

func RecoverRegistrationOperator(dataRoot string) error {
	var recovery registrationOperatorRecovery
	missing, err := privatestore.ReadJSON(registrationOperatorRecoveryPath(dataRoot), &recovery)
	if missing {
		return nil
	}
	if err != nil || recovery.SchemaVersion != 1 {
		return errors.New("本人绑定恢复记录不可读")
	}
	configDir, err := ManagedLarkCLIConfigDir(dataRoot)
	if err != nil {
		return err
	}
	data, err := appConfigurationBytes(filepath.Join(configDir, "config.json"))
	if err != nil || !bytes.Contains(data, []byte(`"appId":"`+recovery.AppID+`"`)) && !bytes.Contains(data, []byte(`"appId": "`+recovery.AppID+`"`)) {
		return errors.New("应用配置与本人绑定恢复记录不匹配")
	}
	if err := bindRegistrationOperator(dataRoot, recovery.AppID, recovery.OpenID); err != nil {
		return err
	}
	return os.Remove(registrationOperatorRecoveryPath(dataRoot))
}

func appConfigurationBytes(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maximumAuthOutputBytes {
		return nil, errors.New("应用配置文件无效")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maximumAuthOutputBytes+1))
	if err != nil || len(data) > maximumAuthOutputBytes {
		return nil, errors.New("应用配置文件超出限制")
	}
	return data, nil
}

func publishAppConfiguration(stage, destination, appID string) error {
	failure := errors.New("创建结果未接入：配置发生变化或落盘校验失败；已保留官方暂存配置，请先确认结果，不要重复创建")
	source := filepath.Join(stage, "config.json")
	var configuration struct {
		CurrentApp string `json:"currentApp,omitempty"`
		Apps       []struct {
			Name      string `json:"name"`
			AppID     string `json:"appId"`
			AppSecret struct {
				Source string `json:"source"`
				ID     string `json:"id"`
			} `json:"appSecret"`
			Brand string `json:"brand"`
			Lang  string `json:"lang,omitempty"`
			Users []any  `json:"users"`
		} `json:"apps"`
	}
	missing, err := privatestore.ReadJSON(source, &configuration)
	if err != nil || missing || len(configuration.Apps) != 1 {
		return failure
	}
	app := configuration.Apps[0]
	if app.Name != "default" || app.AppID != appID || app.Brand != "feishu" || app.AppSecret.Source != "keychain" || app.AppSecret.ID != "appsecret:"+appID || len(app.Users) != 0 || configuration.CurrentApp != "" && configuration.CurrentApp != "default" {
		return failure
	}
	file, err := os.OpenFile(source, os.O_RDWR, 0)
	if err != nil {
		return failure
	}
	err = file.Sync()
	_ = file.Close()
	if err != nil {
		return failure
	}
	if err := securePrivatePath(source, false); err != nil {
		return failure
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(destination))
	if err != nil || parent != filepath.Dir(destination) {
		return failure
	}
	if err := os.Link(source, destination); err != nil {
		return failure
	}
	if runtime.GOOS != "windows" {
		directory, err := os.Open(parent)
		if err != nil {
			return failure
		}
		err = directory.Sync()
		_ = directory.Close()
		if err != nil {
			return failure
		}
	}
	return nil
}
