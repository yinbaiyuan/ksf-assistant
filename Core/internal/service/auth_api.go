package service

import (
	"context"
	"encoding/json"
	"errors"

	managedfeishu "ksfassistant/core/internal/feishu"
	"ksfassistant/core/internal/feishuprotocol"
	"ksfassistant/core/internal/integration"
	"ksfassistant/core/internal/privateipc"
)

var ErrFeishuSettingsConflict = errors.New("飞书设置已变化，请重新检查后确认")

var ErrFeishuOperatorContextConflict = errors.New("确认时的应用或用户身份已变化，本次未绑定操作人")

func (store remoteSettingsStore) CompareAndSwap(ctx context.Context, before, after managedfeishu.Settings) error {
	var result managedfeishu.Settings
	err := (integrationFeishuPort{store.service}).call(ctx, feishuprotocol.MethodSettingsCompareAndSwap, map[string]any{"expected": before, "settings": after}, &result)
	var rpcErr *privateipc.RPCError
	if errors.As(err, &rpcErr) && rpcErr.Code == -32064 {
		return ErrFeishuSettingsConflict
	}
	return err
}

func confirmedFeishuUserAuth(auth *feishuprotocol.AuthStatus) bool {
	return auth != nil && auth.SchemaVersion == 1 && auth.Status == "authorized" && auth.Identity == "user" && auth.Profile == "default" && auth.IdentityValid && auth.ProfileValid
}

func publicFeishuAuthResult(auth feishuprotocol.AuthStatus) map[string]any {
	data, _ := json.Marshal(auth)
	var result map[string]any
	_ = json.Unmarshal(data, &result)
	return result
}

func (service *Service) BindFeishuOperator(ctx context.Context, confirm bool) error {
	return errors.New("绑定操作人必须携带已确认的应用和身份上下文，请重新确认")
}

func (service *Service) BindFeishuOperatorWithExpected(ctx context.Context, confirm bool, expected feishuprotocol.ConfigurationEvidence) error {
	if !confirm {
		return errors.New("绑定飞书操作人需要明确确认")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if expected.ApplicationState != "present" || !confirmedFeishuUserAuth(expected.Auth) || expected.IdentityRevision == "" || expected.ContextRevision == "" || expected.ApplicationID == "" {
		return errors.New("缺少已确认的应用和身份上下文，请重新确认")
	}
	evidence, err := service.readConfigurationEvidence(ctx)
	if err != nil {
		return err
	}
	if evidence.ApplicationState != "present" || !confirmedFeishuUserAuth(evidence.Auth) || evidence.IdentityRevision != expected.IdentityRevision || evidence.ContextRevision != expected.ContextRevision || evidence.ApplicationID != expected.ApplicationID {
		return ErrFeishuOperatorContextConflict
	}
	var result map[string]any
	err = (integrationFeishuPort{service}).call(ctx, feishuprotocol.MethodAuthEnsureUser, map[string]string{
		"identityRevision": expected.IdentityRevision, "contextRevision": expected.ContextRevision, "applicationId": expected.ApplicationID,
	}, &result)
	var rpcErr *privateipc.RPCError
	if errors.As(err, &rpcErr) && rpcErr.Code == -32065 {
		return ErrFeishuOperatorContextConflict
	}
	return err
}

func (service *Service) FeishuAuthStatus(ctx context.Context) (feishuprotocol.AuthStatus, error) {
	return service.desktopFeishuAuth(ctx, feishuprotocol.MethodAuthStatus, map[string]any{})
}

func (service *Service) StartDesktopFeishuAuth(ctx context.Context, request feishuprotocol.AuthStartRequest) (feishuprotocol.AuthStatus, error) {
	if request.Scope != "" && request.Scope != "required" {
		return feishuprotocol.AuthStatus{}, errors.New("只支持申请当前功能所需的精确权限")
	}
	return service.desktopFeishuAuth(ctx, feishuprotocol.MethodAuthStart, map[string]any{"kind": "user", "scope": "required"})
}

func (service *Service) FinishDesktopFeishuAuth(ctx context.Context) (feishuprotocol.AuthStatus, error) {
	return service.desktopFeishuAuth(ctx, feishuprotocol.MethodAuthFinish, map[string]any{})
}

func (service *Service) LogoutFeishuAuth(ctx context.Context) (feishuprotocol.AuthStatus, error) {
	var result feishuprotocol.AuthStatus
	if service.integrationRuntime == nil {
		return result, integration.ErrLogoutCardsPending
	}
	err := service.integrationRuntime.DisconnectForLogout(ctx, func() error {
		var err error
		result, err = service.desktopFeishuAuth(ctx, feishuprotocol.MethodAuthLogout, map[string]any{})
		return err
	})
	service.clearFeishuCache()
	return result, err
}

func (service *Service) desktopFeishuAuth(ctx context.Context, method string, params any) (feishuprotocol.AuthStatus, error) {
	if service.managedFeishuSupervisor == nil {
		return feishuprotocol.AuthStatus{}, errors.New("飞书授权服务不可用，请检查服务后重试")
	}
	var result feishuprotocol.AuthStatus
	if err := service.managedFeishuSupervisor.Call(ctx, method, params, &result); err != nil {
		return feishuprotocol.AuthStatus{}, errors.New("飞书授权操作未完成，请检查授权状态后重试")
	}
	if result.SchemaVersion != 1 {
		return feishuprotocol.AuthStatus{}, errors.New("飞书授权状态不兼容，请更新应用")
	}
	if result.MissingCapabilities == nil {
		result.MissingCapabilities = []string{}
	}
	service.clearFeishuCache()
	return result, nil
}
