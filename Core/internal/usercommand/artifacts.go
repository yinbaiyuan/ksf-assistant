package usercommand

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type ArtifactTarget struct {
	Path      string `json:"path"`
	Directory bool   `json:"directory"`
	Required  bool   `json:"required,omitempty"`
}

type ArtifactPlan struct {
	Root      string           `json:"root"`
	Directory string           `json:"directory,omitempty"`
	Targets   []ArtifactTarget `json:"targets,omitempty"`
}

type Artifact struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

func artifactTargets(parsed parsed) []ArtifactTarget {
	var targets []ArtifactTarget
	if parsed.spec.descriptor == nil {
		return targets
	}
	for _, flag := range parsed.spec.descriptor.Flags {
		if flag.Role != "artifact" {
			continue
		}
		for _, value := range parsed.values[flag.Name] {
			target := ArtifactTarget{Path: value, Directory: strings.Contains(flag.Name, "dir") || value == "." || strings.HasSuffix(value, "/")}
			var companions []ArtifactTarget
			for _, companion := range parsed.spec.descriptor.ArtifactCompanions {
				if companion.Flag == flag.Name {
					target.Required = true
					companions = append(companions, ArtifactTarget{Path: strings.TrimSuffix(value, companion.TrimSuffix) + companion.Suffix, Required: true})
				}
			}
			targets = append(targets, target)
			targets = append(targets, companions...)
		}
	}
	return targets
}

func freezeArtifactPlan(parsed parsed, cwd string) (*ArtifactPlan, error) {
	targets := artifactTargets(parsed)
	if len(targets) == 0 && !parsed.spec.descriptor.Artifacts {
		return nil, nil
	}
	root, err := filepath.Abs(cwd)
	if err != nil {
		return nil, errors.New("user_command_output_root_invalid")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, errors.New("user_command_output_root_invalid")
	}
	plan := &ArtifactPlan{Root: root, Targets: targets}
	if len(targets) == 0 {
		var nonce [8]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return nil, err
		}
		plan.Directory = "ksfas-artifacts-" + hex.EncodeToString(nonce[:])
	}
	return plan, nil
}

func validateArtifactPlan(plan *ArtifactPlan, parsed parsed) error {
	targets := artifactTargets(parsed)
	needed := len(targets) > 0 || parsed.spec.descriptor.Artifacts
	if plan == nil {
		if needed {
			return errors.New("user_command_artifact_plan_required")
		}
		return nil
	}
	if !needed || !filepath.IsAbs(plan.Root) || filepath.Clean(plan.Root) != plan.Root || strings.ContainsRune(plan.Root, 0) || len(plan.Root) > 4096 {
		return errors.New("user_command_artifact_plan_invalid")
	}
	if len(targets) != len(plan.Targets) {
		return errors.New("user_command_artifact_plan_changed")
	}
	for index, target := range targets {
		if target != plan.Targets[index] || !safeArtifactPath(target.Path) {
			return errors.New("user_command_artifact_plan_changed")
		}
	}
	if len(targets) > 0 {
		if plan.Directory != "" {
			return errors.New("user_command_artifact_plan_changed")
		}
	} else {
		if !strings.HasPrefix(plan.Directory, "ksfas-artifacts-") || len(plan.Directory) != len("ksfas-artifacts-")+16 {
			return errors.New("user_command_artifact_plan_invalid")
		}
		if _, err := hex.DecodeString(strings.TrimPrefix(plan.Directory, "ksfas-artifacts-")); err != nil {
			return errors.New("user_command_artifact_plan_invalid")
		}
	}
	return nil
}

func (plan *ArtifactPlan) permits(relative string) bool {
	if !safeArtifactPath(relative) {
		return false
	}
	if plan.Directory != "" {
		return true
	}
	for _, target := range plan.Targets {
		clean := filepath.ToSlash(filepath.Clean(target.Path))
		if relative == clean || target.Directory && (clean == "." || strings.HasPrefix(relative, clean+"/")) || !target.Directory && strings.HasPrefix(relative, clean+".") {
			return true
		}
	}
	return false
}

func safeExistingDirectory(path string) error {
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("user_command_artifact_parent_unsafe")
		}
		if filepath.Dir(current) == current {
			return nil
		}
	}
}

func createArtifactParents(root, relative string) ([]string, error) {
	if err := safeExistingDirectory(root); err != nil {
		return nil, err
	}
	var created []string
	current := root
	for _, part := range strings.Split(filepath.ToSlash(filepath.Dir(relative)), "/") {
		if part == "." || part == "" {
			continue
		}
		current = filepath.Join(current, part)
		if err := os.Mkdir(current, 0700); err == nil {
			created = append(created, current)
		} else if !os.IsExist(err) {
			return created, err
		}
		if err := safeExistingDirectory(current); err != nil {
			return created, err
		}
	}
	return created, nil
}

func (execution *Execution) PublishArtifacts() ([]Artifact, error) {
	if execution == nil || execution.Dir == "" {
		return nil, errors.New("user_command_execution_closed")
	}
	if execution.publicationAttempted {
		return nil, errors.New("user_command_artifacts_already_attempted")
	}
	execution.publicationAttempted = true
	if execution.artifactPlan == nil {
		return nil, nil
	}
	plan := execution.artifactPlan
	if err := safeExistingDirectory(plan.Root); err != nil {
		return nil, err
	}
	inputs := map[string]bool{}
	for _, input := range execution.inputs {
		inputs[filepath.Clean(input)] = true
	}
	var sources []string
	var total int64
	err := filepath.WalkDir(execution.Dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == execution.Dir {
			return nil
		}
		if inputs[path] {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("user_command_artifact_symlink")
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("user_command_artifact_not_regular")
		}
		relative, err := filepath.Rel(execution.Dir, path)
		if err != nil || !plan.permits(filepath.ToSlash(relative)) {
			return errors.New("user_command_artifact_unplanned")
		}
		total += info.Size()
		if total > 512*1024*1024 || len(sources) >= 1000 {
			return errors.New("user_command_artifact_limit_exceeded")
		}
		sources = append(sources, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(sources) == 0 && (len(plan.Targets) > 0 || execution.requireArtifact) {
		return nil, errors.New("user_command_artifact_missing")
	}
	for _, target := range plan.Targets {
		if !target.Required {
			continue
		}
		found := false
		for _, source := range sources {
			relative, _ := filepath.Rel(execution.Dir, source)
			found = found || filepath.Clean(relative) == filepath.Clean(target.Path)
		}
		if !found {
			return nil, errors.New("user_command_artifact_missing")
		}
	}
	if len(sources) > 0 && plan.Directory != "" {
		if _, err := os.Lstat(filepath.Join(plan.Root, plan.Directory)); !os.IsNotExist(err) {
			return nil, errors.New("user_command_artifact_conflict")
		}
	}
	sort.Strings(sources)
	var published []Artifact
	for _, source := range sources {
		relative, _ := filepath.Rel(execution.Dir, source)
		destination := filepath.Join(plan.Root, plan.Directory, relative)
		if _, err := os.Lstat(destination); !os.IsNotExist(err) {
			return nil, errors.New("user_command_artifact_conflict")
		}
	}
	for _, source := range sources {
		relative, _ := filepath.Rel(execution.Dir, source)
		relative = filepath.Join(plan.Directory, relative)
		_, err := createArtifactParents(plan.Root, relative)
		if err != nil {
			return published, fmt.Errorf("user_command_artifact_publication_incomplete: %w", err)
		}
		destination := filepath.Join(plan.Root, relative)
		artifact, err := publishArtifactFile(source, destination)
		if err != nil {
			return published, fmt.Errorf("user_command_artifact_publication_incomplete: %w", err)
		}
		published = append(published, artifact)
	}
	return published, nil
}

func publishArtifactFile(source, destination string) (Artifact, error) {
	var artifact Artifact
	before, err := os.Lstat(source)
	if err != nil || !before.Mode().IsRegular() {
		return artifact, errors.New("user_command_artifact_not_regular")
	}
	input, err := os.Open(source)
	if err != nil {
		return artifact, err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil || !os.SameFile(before, info) {
		return artifact, errors.New("user_command_artifact_changed")
	}
	output, err := os.CreateTemp(filepath.Dir(destination), ".ksfas-publish-")
	if err != nil {
		return artifact, err
	}
	defer os.Remove(output.Name())
	digest := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(output, digest), io.LimitReader(input, 512*1024*1024+1))
	if copyErr == nil {
		copyErr = output.Sync()
	}
	closeErr := output.Close()
	if copyErr != nil {
		return artifact, copyErr
	}
	if closeErr != nil {
		return artifact, closeErr
	}
	after, err := input.Stat()
	if err != nil || size != before.Size() || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return artifact, errors.New("user_command_artifact_changed")
	}
	if err := safeExistingDirectory(filepath.Dir(destination)); err != nil {
		return artifact, err
	}
	if err := os.Link(output.Name(), destination); err != nil {
		return artifact, fmt.Errorf("user_command_artifact_publish_failed: %w", err)
	}
	return Artifact{Path: destination, Bytes: size, SHA256: hex.EncodeToString(digest.Sum(nil))}, nil
}
