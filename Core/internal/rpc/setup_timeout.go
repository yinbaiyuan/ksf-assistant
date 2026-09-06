package rpc

import "time"

func desktopRequestTimeout(method string) time.Duration {
	switch method {
	case "feishu/setup/activate", "feishu/setup/verify", "feishu/setup/continue":
		return 120 * time.Second
	default:
		return 45 * time.Second
	}
}
