package feishuprotocol

const MethodConfigurationEvidence = "bridge/configuration/evidence"
const MethodConfigurationCancel = "bridge/configuration/cancel"
const MethodConfigurationFlow = "bridge/configuration/flow"

type ConfigurationFlow struct {
	ID              string `json:"id"`
	Kind            string `json:"kind"`
	State           string `json:"state"`
	ExpiresAt       string `json:"expiresAt,omitempty"`
	VerificationURL string `json:"verificationURL,omitempty"`
	UserCode        string `json:"userCode,omitempty"`
	QRDataURL       string `json:"qrDataURL,omitempty"`
}

type ConfigurationEvidence struct {
	BotName                  string                `json:"botName,omitempty"`
	ServiceVersion           string                `json:"serviceVersion,omitempty"`
	CLIVersion               string                `json:"cliVersion,omitempty"`
	CLIState                 string                `json:"cliState,omitempty"`
	PermissionRevision       string                `json:"permissionRevision,omitempty"`
	MissingUserScopes        []string              `json:"missingUserScopes,omitempty"`
	MissingApplicationScopes []string              `json:"missingApplicationScopes,omitempty"`
	AuthorizationRequest     *AuthorizationRequest `json:"authorizationRequest,omitempty"`
	CleanupPending           bool                  `json:"cleanupPending"`

	SchemaVersion          int                `json:"schemaVersion"`
	ContextRevision        string             `json:"contextRevision"`
	IdentityRevision       string             `json:"identityRevision,omitempty"`
	UserName               string             `json:"userName,omitempty"`
	ApplicationID          string             `json:"applicationId,omitempty"`
	ApplicationState       string             `json:"applicationState"`
	CreationBlocked        bool               `json:"creationBlocked"`
	Auth                   *AuthStatus        `json:"auth,omitempty"`
	BotState               string             `json:"botState"`
	ApplicationPermissions string             `json:"applicationPermissions"`
	UserPermissions        string             `json:"userPermissions"`
	BotPermissions         string             `json:"botPermissions"`
	OperatorState          string             `json:"operatorState"`
	OperatorAlias          string             `json:"operatorAlias,omitempty"`
	CheckedAt              string             `json:"checkedAt"`
	Flow                   *ConfigurationFlow `json:"flow,omitempty"`
	Problems               []string           `json:"problems"`
}

type AuthorizationRequest struct {
	ID      string   `json:"id"`
	Purpose string   `json:"purpose"`
	Scopes  []string `json:"scopes"`
}

type ConfigurationCancelRequest struct {
	FlowID string `json:"flowId"`
	Kind   string `json:"kind"`
}
