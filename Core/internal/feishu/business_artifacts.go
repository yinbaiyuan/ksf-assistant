package feishu

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
)

type businessArtifactConsumerKey struct{}
type businessArtifactConsumer func(string, map[string]any) error

func consumeBusinessArtifacts(ctx context.Context, directory string, result map[string]any) error {
	if consume, ok := ctx.Value(businessArtifactConsumerKey{}).(businessArtifactConsumer); ok {
		physical, err := filepath.EvalSymlinks(directory)
		if err != nil {
			return errors.New("business_artifact_unavailable")
		}
		return consume(physical, result)
	}
	return nil
}

func copyBusinessArtifact(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("unsafe_business_artifact")
	}
	for directory := filepath.Dir(source); ; directory = filepath.Dir(directory) {
		info, err := os.Lstat(directory)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("unsafe_business_artifact")
		}
		if filepath.Dir(directory) == directory {
			break
		}
	}
	input, err := os.Open(source)
	if err != nil {
		return errors.New("business_artifact_unavailable")
	}
	defer input.Close()
	opened, err := input.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return errors.New("business_artifact_changed")
	}
	if err := ensurePrivateDirectory(filepath.Dir(destination)); err != nil {
		return err
	}
	output, err := os.CreateTemp(filepath.Dir(destination), ".artifact-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(output.Name())
	defer output.Close()
	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	after, err := input.Stat()
	if err != nil || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return errors.New("business_artifact_changed")
	}
	if err := output.Sync(); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	return replacePrivateFile(output.Name(), destination)
}
