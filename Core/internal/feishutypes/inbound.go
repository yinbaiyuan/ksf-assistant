package feishutypes

type InboundMessage struct {
	EventID, MessageID, RootID, ParentID, ChatID, ChatType, MessageType, Text, SenderOpenID string
	Raw                                                                                     map[string]any
}

type InboundCardAction struct {
	EventID, OperatorOpenID, ChatID, MessageID, Action, TaskKey, LinkID, QuestionRevision, Token string
	Value, FormValue                                                                             map[string]any
	Raw                                                                                          map[string]any
}

type InboundCard = InboundCardAction
type InboundResource struct {
	FileKey, ResourceType, DisplayName string
}

type InboundAsset struct {
	MessageType, ResourceType, DisplayName, LocalPath string
	SizeBytes                                         int64
}

type StagedInboundMessage struct {
	MessageType string
	Text        string
	Assets      []InboundAsset
	CleanupDir  string
}

type StageAssets = StagedInboundMessage
