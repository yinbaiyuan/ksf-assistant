package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type runtimeManifest struct {
	Version   string                     `json:"version"`
	BaseURL   string                     `json:"baseUrl"`
	Artifacts map[string]runtimeArtifact `json:"artifacts"`
}

type runtimeArtifact struct {
	Archive    string `json:"archive"`
	SHA256     string `json:"sha256"`
	Executable string `json:"executable"`
}

type goModule struct {
	Path    string `json:"Path"`
	Version string `json:"Version"`
}

type lockFile struct {
	Packages map[string]lockPackage `json:"packages"`
}

type lockPackage struct {
	Version  string `json:"version"`
	Resolved string `json:"resolved"`
	License  string `json:"license"`
}

type spdxPackage struct {
	SPDXID           string `json:"SPDXID"`
	Name             string `json:"name"`
	VersionInfo      string `json:"versionInfo"`
	DownloadLocation string `json:"downloadLocation"`
	FilesAnalyzed    bool   `json:"filesAnalyzed"`
	LicenseConcluded string `json:"licenseConcluded"`
	LicenseDeclared  string `json:"licenseDeclared"`
}

func main() {
	repoRoot := flag.String("repo-root", "", "absolute repository root")
	targets := flag.String("stage-lark", "", "comma-separated lark-cli runtime targets")
	generateSBOM := flag.Bool("sbom", false, "generate the cross-platform SPDX document")
	generateCapabilities := flag.String("generate-feishu-capabilities", "", "absolute path to pinned lark-cli used to generate the reviewed capability additions")
	flag.Parse()
	if !filepath.IsAbs(*repoRoot) {
		fatal(errors.New("--repo-root must be absolute"))
	}
	if *targets != "" {
		for _, target := range strings.Split(*targets, ",") {
			if err := stageLarkCLI(*repoRoot, strings.TrimSpace(target)); err != nil {
				fatal(err)
			}
		}
	}
	if *generateSBOM {
		if err := writeSBOM(*repoRoot); err != nil {
			fatal(err)
		}
	}
	if *generateCapabilities != "" {
		if err := generateFeishuCapabilities(*repoRoot, *generateCapabilities); err != nil {
			fatal(err)
		}
	}
}

func stageLarkCLI(repoRoot, target string) error {
	var manifest runtimeManifest
	if err := readJSON(filepath.Join(repoRoot, "runtime", "lark-cli-runtime.json"), &manifest); err != nil {
		return err
	}
	artifact, ok := manifest.Artifacts[target]
	if !ok || artifact.Archive == "" || artifact.Executable == "" || len(artifact.SHA256) != 64 {
		return fmt.Errorf("invalid lark-cli runtime target: %s", target)
	}
	cacheDir := filepath.Join(repoRoot, "dist", "cache", "lark-cli")
	archivePath := filepath.Join(cacheDir, filepath.Base(artifact.Archive))
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return err
	}
	if digest, err := fileSHA256(archivePath); err != nil || digest != artifact.SHA256 {
		_ = os.Remove(archivePath)
		if err := download(manifest.BaseURL+"/"+artifact.Archive, archivePath); err != nil {
			return err
		}
	}
	if digest, err := fileSHA256(archivePath); err != nil || digest != artifact.SHA256 {
		return fmt.Errorf("lark-cli checksum mismatch: %s", artifact.Archive)
	}
	outputDir := filepath.Join(repoRoot, "dist", "runtime", "lark-cli", target)
	if err := os.RemoveAll(outputDir); err != nil {
		return err
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return err
	}
	output := filepath.Join(outputDir, artifact.Executable)
	if err := extractTarGzipFile(archivePath, artifact.Executable, output); err != nil {
		return err
	}
	return os.Chmod(output, 0o755)
}

func download(rawURL, destination string) error {
	client := &http.Client{Timeout: 2 * time.Minute}
	response, err := client.Get(rawURL)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("runtime download failed with HTTP %d", response.StatusCode)
	}
	temporary := destination + ".download"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(file, io.LimitReader(response.Body, 256*1024*1024+1))
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(temporary)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(temporary)
		return closeErr
	}
	return os.Rename(temporary, destination)
}

func extractTarGzipFile(archivePath, executable, output string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzipReader.Close()
	tReader := tar.NewReader(gzipReader)
	for {
		header, err := tReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if header.Typeflag != tar.TypeReg || filepath.Base(header.Name) != executable {
			continue
		}
		target, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
		if err != nil {
			return err
		}
		written, copyErr := io.Copy(target, io.LimitReader(tReader, 128*1024*1024+1))
		closeErr := target.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if written > 128*1024*1024 {
			return errors.New("lark-cli executable exceeds size limit")
		}
		return nil
	}
	return fmt.Errorf("lark-cli executable missing after extraction: %s", executable)
}

func writeSBOM(repoRoot string) error {
	revision, err := commandOutput(repoRoot, "git", "rev-parse", "--verify", "HEAD")
	if err != nil {
		return err
	}
	epoch := strings.TrimSpace(os.Getenv("SOURCE_DATE_EPOCH"))
	if epoch == "" {
		epoch, err = commandOutput(repoRoot, "git", "show", "-s", "--format=%ct", "HEAD")
		if err != nil {
			return err
		}
	}
	seconds, err := time.ParseDuration(strings.TrimSpace(epoch) + "s")
	if err != nil {
		return fmt.Errorf("invalid source date epoch: %w", err)
	}
	modules, err := goModules(filepath.Join(repoRoot, "Core"))
	if err != nil {
		return err
	}
	var lock lockFile
	if err := readJSON(filepath.Join(repoRoot, "Windows", "package-lock.json"), &lock); err != nil {
		return err
	}
	packages := []spdxPackage{{SPDXID: "SPDXRef-Application", Name: "KSFAssistant", VersionInfo: "0.11.0-preview.3", DownloadLocation: "NOASSERTION", FilesAnalyzed: false, LicenseConcluded: "MIT", LicenseDeclared: "MIT"}}
	for _, module := range modules {
		if module.Path == "ksfassistant/core" {
			continue
		}
		location := "NOASSERTION"
		if module.Version != "" {
			location = "https://proxy.golang.org/" + module.Path + "/@v/" + module.Version + ".zip"
		}
		packages = append(packages, spdxPackage{SPDXID: "SPDXRef-Go-" + stableID(module.Path), Name: module.Path, VersionInfo: fallback(module.Version, "unknown"), DownloadLocation: location, FilesAnalyzed: false, LicenseConcluded: "NOASSERTION", LicenseDeclared: "NOASSERTION"})
	}
	for location, metadata := range lock.Packages {
		if !strings.HasPrefix(location, "node_modules/") || metadata.Version == "" {
			continue
		}
		name := strings.TrimPrefix(location, "node_modules/")
		license := fallback(metadata.License, "NOASSERTION")
		packages = append(packages, spdxPackage{SPDXID: "SPDXRef-Npm-" + stableID(name+"@"+metadata.Version), Name: name, VersionInfo: metadata.Version, DownloadLocation: fallback(metadata.Resolved, "NOASSERTION"), FilesAnalyzed: false, LicenseConcluded: license, LicenseDeclared: license})
	}
	sort.Slice(packages, func(i, j int) bool { return packages[i].SPDXID < packages[j].SPDXID })
	relationships := make([]map[string]string, 0, len(packages)-1)
	for _, item := range packages[1:] {
		relationships = append(relationships, map[string]string{"spdxElementId": "SPDXRef-Application", "relationshipType": "DEPENDS_ON", "relatedSpdxElement": item.SPDXID})
	}
	document := map[string]any{
		"spdxVersion": "SPDX-2.3", "dataLicense": "CC0-1.0", "SPDXID": "SPDXRef-DOCUMENT",
		"name":              "KSFAssistant-" + revision[:min(12, len(revision))],
		"documentNamespace": "https://ksfassistant.invalid/spdx/" + revision,
		"creationInfo":      map[string]any{"created": time.Unix(0, 0).UTC().Add(seconds).Format(time.RFC3339), "creators": []string{"Tool: ksf-assistant-build-assets"}},
		"packages":          packages, "relationships": relationships,
	}
	outputDir := filepath.Join(repoRoot, "dist", "sbom")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(outputDir, "KSFAssistant.spdx.json"), data, 0o644); err != nil {
		return err
	}
	fmt.Printf("Generated SPDX 2.3 SBOM with %d packages.\n", len(packages))
	return nil
}

func goModules(directory string) ([]goModule, error) {
	cmd := exec.Command("go", "list", "-m", "-json", "all")
	cmd.Dir = directory
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(output)))
	var modules []goModule
	for decoder.More() {
		var module goModule
		if err := decoder.Decode(&module); err != nil {
			return nil, err
		}
		modules = append(modules, module)
	}
	return modules, nil
}

func readJSON(path string, output any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, output)
}

func commandOutput(directory, name string, arguments ...string) (string, error) {
	cmd := exec.Command(name, arguments...)
	cmd.Dir = directory
	output, err := cmd.Output()
	return strings.TrimSpace(string(output)), err
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func stableID(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])[:20]
}

func fallback(value, defaultValue string) string {
	if value == "" {
		return defaultValue
	}
	return value
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
