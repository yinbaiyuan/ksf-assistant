package service

import (
	"context"
	"errors"

	"ksfassistant/core/internal/feishuprotocol"
)

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
	return service.desktopFeishuAuth(ctx, feishuprotocol.MethodAuthLogout, map[string]any{})
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
