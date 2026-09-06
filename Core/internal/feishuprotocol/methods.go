package feishuprotocol

const (
	MethodBridgeInitialize   = "bridge/initialize"
	MethodBridgeSnapshotRead = "bridge/snapshot/read"
	MethodBridgeSnapshotPush = "bridge/snapshot/push"
	MethodMessageTest        = "bridge/message/test"
	MethodProfileSet         = "bridge/profile/set"
	MethodAuthConfigure      = "bridge/auth/configure"
	MethodAuthStatus         = "bridge/auth/status"
	MethodAuthStart          = "bridge/auth/start"
	MethodAuthFinish         = "bridge/auth/finish"
	MethodAuthLogout         = "bridge/auth/logout"
	MethodAuthCancel         = "bridge/auth/cancel"
	MethodAuthEnsureUser     = "bridge/auth/ensureCurrentUser"
	MethodPermissionsRead    = "bridge/permissions/read"
	MethodSettingsReload     = "bridge/settings/reload"
	MethodOperationPrepare   = "bridge/operation/prepare"
	MethodOperationConfirm   = "bridge/operation/confirm"
	MethodOperationCancel    = "bridge/operation/cancel"
	MethodOperationStatus    = "bridge/operation/status"
	MethodPolicyRead         = "bridge/policy/read"
	MethodPolicyUpdate       = "bridge/policy/update"
)
