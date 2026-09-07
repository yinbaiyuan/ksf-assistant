package toolchain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

const Version = "1.0.93"

var safeName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,79}$`)
var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Config struct {
	ResourcesDir string
	HomeDir      string
	StateDir     string
	Profile      string
	ConfigDir    string
	DataRoot     string
}

type SkillStatus struct {
	Name    string   `json:"name"`
	State   string   `json:"state"`
	Details []string `json:"details,omitempty"`
}

type Status struct {
	InstallationState     string        `json:"installationState"`
	InstallationTitle     string        `json:"installationTitle"`
	InstallationAction    string        `json:"installationAction"`
	InstallationDetails   []string      `json:"installationDetails,omitempty"`
	Version               string        `json:"version"`
	ExecutionManifest     string        `json:"executionManifest"`
	SkillsAdapterRevision string        `json:"skillsAdapterRevision,omitempty"`
	SkillsAdapterDigest   string        `json:"skillsAdapterDigest,omitempty"`
	Installed             bool          `json:"installed"`
	Healthy               bool          `json:"healthy"`
	Skills                []SkillStatus `json:"skills"`
	LauncherPath          string        `json:"launcherPath"`
	TaskLauncherPath      string        `json:"taskLauncherPath"`
	Brand                 string        `json:"brand"`
	Profile               string        `json:"profile"`
	ConfigDir             string        `json:"configDir"`
	Problems              []string      `json:"problems"`
}

type Skill struct {
	Name  string            `json:"name"`
	Files map[string]string `json:"files"`
}

type Manifest struct {
	SchemaVersion int         `json:"schemaVersion"`
	Version       string      `json:"version"`
	License       string      `json:"license"`
	LicenseSHA256 string      `json:"licenseSha256"`
	Skills        []Skill     `json:"skills"`
	Adaptation    *Adaptation `json:"adaptation,omitempty"`
}

type Adaptation struct {
	SchemaVersion          int    `json:"schemaVersion"`
	Revision               string `json:"revision"`
	Digest                 string `json:"digest"`
	UpstreamVersion        string `json:"upstreamVersion"`
	UpstreamManifestSHA256 string `json:"upstreamManifestSha256"`
}

func (adaptation *Adaptation) valid(version string) bool {
	return adaptation == nil || (adaptation.SchemaVersion == 1 && safeName.MatchString(adaptation.Revision) && digestPattern.MatchString(adaptation.Digest) && adaptation.UpstreamVersion == version && digestPattern.MatchString(adaptation.UpstreamManifestSHA256))
}

type Manager struct {
	config       Config
	beforeCommit func(int) error
}

func New(config Config) (*Manager, error) {
	if config.HomeDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, errors.New("home_unavailable")
		}
		config.HomeDir = home
	}
	if config.ResourcesDir == "" {
		executable, err := os.Executable()
		if err != nil {
			return nil, errors.New("resources_unavailable")
		}
		config.ResourcesDir = filepath.Clean(filepath.Join(filepath.Dir(executable), "..", "..", ".."))
		if filepath.Base(filepath.Dir(executable)) == "MacOS" {
			config.ResourcesDir = filepath.Join(filepath.Dir(filepath.Dir(executable)), "Resources")
		}
	}
	if config.StateDir == "" {
		config.StateDir = filepath.Join(config.HomeDir, ".local", "share", "ksfassistant", "toolchain")
		if runtime.GOOS == "windows" {
			config.StateDir = filepath.Join(config.HomeDir, "AppData", "Local", "KSFAssistant", "toolchain")
		}
	}
	if config.ConfigDir == "" {
		config.ConfigDir = filepath.Join(config.HomeDir, ".lark-cli")
	}
	if config.DataRoot == "" {
		config.DataRoot = filepath.Join(config.HomeDir, ".config", "feishu-bridge")
	}
	if config.Profile == "" {
		config.Profile = "default"
	}
	if config.Profile != "default" {
		return nil, errors.New("invalid_profile")
	}
	for _, directory := range []string{config.HomeDir, config.ResourcesDir, config.StateDir, config.ConfigDir, config.DataRoot} {
		if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory || strings.ContainsAny(directory, "\x00\r\n") {
			return nil, errors.New("invalid_path")
		}
		if err := noSymlinks(directory); err != nil {
			return nil, err
		}
	}
	if !within(config.HomeDir, config.StateDir) || config.StateDir == config.HomeDir || within(config.StateDir, config.ConfigDir) || within(config.StateDir, config.ResourcesDir) || within(filepath.Join(config.HomeDir, ".agents"), config.StateDir) {
		return nil, errors.New("invalid_state_path")
	}
	return &Manager{config: config}, nil
}

func within(root, name string) bool {
	relative, err := filepath.Rel(root, name)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func noSymlinks(name string) error {
	for {
		info, err := os.Lstat(name)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return errors.New("path_unavailable")
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return errors.New("symlink_refused")
		}
		parent := filepath.Dir(name)
		if parent == name {
			return nil
		}
		name = parent
	}
}

func validRelative(name string) bool {
	if name == "" || strings.ContainsAny(name, "\\:\x00\r\n") || filepath.IsAbs(name) || filepath.ToSlash(filepath.Clean(name)) != name {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." || part == "." || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return false
		}
	}
	return true
}

func fileHash(name string) (string, error) {
	if err := noSymlinks(name); err != nil {
		return "", err
	}
	info, err := os.Stat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 256<<20 {
		return "", errors.New("invalid_file")
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return "", errors.New("file_unavailable")
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func readJSON(name string, result any) error {
	if err := noSymlinks(name); err != nil {
		return err
	}
	info, err := os.Stat(name)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 8<<20 {
		return errors.New("invalid_manifest")
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return errors.New("file_unavailable")
	}
	if json.Unmarshal(data, result) != nil {
		return errors.New("invalid_manifest")
	}
	return nil
}

func platform() string {
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	}
	return runtime.GOOS + "-" + arch
}

func suffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func (manager *Manager) manifest() (Manifest, error) {
	var manifest Manifest
	root := filepath.Join(manager.config.ResourcesDir, "runtime", "lark-skills")
	if err := readJSON(filepath.Join(root, "manifest.json"), &manifest); err != nil {
		return manifest, errors.New("skills_manifest_unavailable")
	}
	if manifest.SchemaVersion != 1 || manifest.Version != Version || manifest.License != "MIT" || len(manifest.Skills) == 0 {
		return manifest, errors.New("version_mismatch")
	}
	if !manifest.Adaptation.valid(manifest.Version) {
		return manifest, errors.New("invalid_adaptation_manifest")
	}
	if manifest.Adaptation != nil {
		digest, err := fileHash(filepath.Join(root, "adaptation-report.json"))
		if err != nil || digest != manifest.Adaptation.Digest {
			return manifest, errors.New("adaptation_checksum_mismatch")
		}
	}
	licenseHash, err := fileHash(filepath.Join(root, "LICENSE"))
	if err != nil || licenseHash != manifest.LicenseSHA256 {
		return manifest, errors.New("license_checksum_mismatch")
	}
	seen := map[string]bool{}
	for _, skill := range manifest.Skills {
		if !safeName.MatchString(skill.Name) || seen[skill.Name] || len(skill.Files) == 0 || skill.Files["SKILL.md"] == "" {
			return manifest, errors.New("invalid_skill_manifest")
		}
		seen[skill.Name] = true
		if err := verifyTree(filepath.Join(root, "skills", skill.Name), skill.Files); err != nil {
			return manifest, errors.New("skill_checksum_mismatch")
		}
	}
	return manifest, nil
}

func verifyTree(root string, files map[string]string) error {
	if err := noSymlinks(root); err != nil {
		return err
	}
	seen := map[string]bool{}
	directories := map[string]bool{".": true}
	for name, expected := range files {
		if !validRelative(name) || !digestPattern.MatchString(expected) {
			return errors.New("invalid_owned_path")
		}
		folded := strings.ToLower(name)
		if seen[folded] {
			return errors.New("case_collision")
		}
		seen[folded] = true
		for parent := filepath.Dir(filepath.FromSlash(name)); parent != "."; parent = filepath.Dir(parent) {
			directories[parent] = true
		}
		actual, err := fileHash(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil || actual != expected {
			return errors.New("owned_file_edited")
		}
	}
	return filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return errors.New("owned_file_missing")
		}
		relative, _ := filepath.Rel(root, name)
		if entry.IsDir() {
			if !directories[relative] {
				return errors.New("unowned_directory")
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || files[filepath.ToSlash(relative)] == "" {
			return errors.New("unowned_file")
		}
		return nil
	})
}

func (manager *Manager) binary() (string, error) {
	var manifest struct {
		Version   string `json:"version"`
		Artifacts map[string]struct {
			Executable string `json:"executable"`
			SHA256     string `json:"executableSha256"`
		} `json:"artifacts"`
	}
	if err := readJSON(filepath.Join(manager.config.ResourcesDir, "runtime", "lark-cli-runtime.json"), &manifest); err != nil {
		return "", errors.New("cli_manifest_unavailable")
	}
	artifact, found := manifest.Artifacts[platform()]
	if !found || manifest.Version != Version || artifact.Executable != "lark-cli"+suffix() || !digestPattern.MatchString(artifact.SHA256) {
		return "", errors.New("version_mismatch")
	}
	binary := filepath.Join(manager.config.ResourcesDir, "runtime", "lark-cli", platform(), artifact.Executable)
	digest, err := fileHash(binary)
	if err != nil || digest != artifact.SHA256 {
		return "", errors.New("cli_checksum_mismatch")
	}
	return binary, nil
}

func (manager *Manager) taskBinary() (string, error) {
	var manifest struct {
		Artifacts map[string]struct {
			SHA256 string `json:"taskExecutableSha256"`
		} `json:"artifacts"`
	}
	if readJSON(filepath.Join(manager.config.ResourcesDir, "runtime", "lark-cli-runtime.json"), &manifest) != nil {
		return "", errors.New("task_manifest_unavailable")
	}
	binary := filepath.Join(manager.config.ResourcesDir, "runtime", "task", platform(), "ksf-assistant-task"+suffix())
	actual, err := fileHash(binary)
	if err != nil || !digestPattern.MatchString(manifest.Artifacts[platform()].SHA256) || actual != manifest.Artifacts[platform()].SHA256 {
		return "", errors.New("task_checksum_mismatch")
	}
	return binary, nil
}
