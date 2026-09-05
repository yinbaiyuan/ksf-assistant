//go:build !windows

package privatestore

import "os"

func securePrivatePath(_ string, _ bool) error { return nil }

func replacePrivateFile(source, destination string) error {
	return os.Rename(source, destination)
}
