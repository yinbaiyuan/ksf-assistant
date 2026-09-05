//go:build !darwin && !windows

package localipc

func normalizeRoot(root string) (string, error) { return root, nil }
