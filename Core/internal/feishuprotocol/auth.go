package feishuprotocol

type AuthStatus struct {
	SchemaVersion       int      `json:"schemaVersion"`
	Status              string   `json:"status"`
	Identity            string   `json:"identity"`
	Profile             string   `json:"profile"`
	IdentityValid       bool     `json:"identityValid"`
	ProfileValid        bool     `json:"profileValid"`
	GrantedScopeCount   int      `json:"grantedScopeCount"`
	MissingCapabilities []string `json:"missingCapabilities"`
	Flow                string   `json:"flow,omitempty"`
	VerificationURL     string   `json:"verificationUrl,omitempty"`
	UserCode            string   `json:"userCode,omitempty"`
	QRDataURL           string   `json:"qrDataURL,omitempty"`
}

type AuthStartRequest struct {
	Scope string `json:"scope"`
}
