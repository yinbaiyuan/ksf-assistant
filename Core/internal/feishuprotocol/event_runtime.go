package feishuprotocol

const ManagedEventProfile = "managed"

func ManagedEventConsumerStatus() map[string]any {
	return map[string]any{
		"profile": ManagedEventProfile, "profileValid": true,
		"desiredConnection": true, "managedBy": "feishu-service", "configurable": false,
	}
}
