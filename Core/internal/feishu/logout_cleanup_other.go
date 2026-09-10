//go:build !darwin && !windows

package feishu

func purgeManagedPlatformCredentialStorage() error { return nil }
