package taskruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type Source struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type Category struct {
	Source
	CategoryID       string `json:"category_id"`
	Name             string `json:"name"`
	ValidationStatus string `json:"validation_status"`
	ContextPolicy    string `json:"context_policy"`
}

type Job struct {
	Source
	JobID            string `json:"job_id"`
	Name             string `json:"name"`
	Role             string `json:"role"`
	ValidationStatus string `json:"validation_status"`
	ContextPolicy    string `json:"context_policy"`
}

type Ability struct {
	Source
	AbilityID        string `json:"ability_id"`
	Name             string `json:"name"`
	JobID            string `json:"job_id"`
	ResponsibilityID string `json:"responsibility_id"`
	ValidationStatus string `json:"validation_status"`
	ContextPolicy    string `json:"context_policy"`
}

type Skill struct {
	SkillID                      string `json:"skill_id"`
	SkillStage                   string `json:"skill_stage"`
	AbilityID                    string `json:"ability_id"`
	ResponsibilityID             string `json:"responsibility_id"`
	ResponsibilityRelationSHA256 string `json:"responsibility_relation_sha256"`
	AbilityValidationStatus      string `json:"ability_validation_status"`
	RelationStage                string `json:"relation_stage"`
	SupportScope                 string `json:"support_scope"`
	AssociationState             string `json:"association_state"`
	ProfessionalBodyPolicy       string `json:"professional_body_policy"`
	AbilityB1Eligible            bool   `json:"ability_b1_eligible"`
	SkillContractSHA256          string `json:"skill_contract_sha256"`
	RelationSHA256               string `json:"relation_sha256"`
}

type Receipt struct {
	Version             int    `json:"version"`
	Protocol            string `json:"protocol"`
	Status              string `json:"status"`
	TaskIntent          string `json:"task_intent"`
	TaskRef             string `json:"task_ref"`
	SourceBindingDigest string `json:"source_binding_digest"`
	CatalogSHA256       string `json:"catalog_sha256"`
	ExpectedCount       int    `json:"expected_count"`
	SourceBytes         int64  `json:"source_bytes"`
	AggregateSHA256     string `json:"aggregate_sha256"`
	Projection          struct {
		Category     Category  `json:"category"`
		Jobs         []Job     `json:"jobs"`
		Abilities    []Ability `json:"abilities"`
		ContextFiles []Source  `json:"context_files"`
	} `json:"projection"`
	DispatchableSkills []Skill `json:"dispatchable_skills"`
	ReceiptSHA256      string  `json:"receipt_sha256"`
}

func ParseReceipt(data json.RawMessage) (Receipt, error) {
	var receipt Receipt
	if len(data) > MaxRequestBytes {
		return receipt, fail("limit_exceeded", "receipt exceeds size limit")
	}
	if err := strictDecode(data, &receipt); err != nil {
		return receipt, err
	}
	if receipt.Version != 6 || receipt.Protocol != "verified-route-projection-v6" || receipt.Status != "verified" {
		return receipt, fail("route_invalid", "a verified v6 route receipt is required")
	}
	if !validText(receipt.TaskIntent, 4096) || strings.TrimSpace(receipt.TaskIntent) == "" || len(receipt.Projection.Jobs) < 1 || len(receipt.Projection.Jobs) > 3 || len(receipt.Projection.Abilities) < 1 || len(receipt.Projection.Abilities) > 3 || len(receipt.Projection.ContextFiles) > 128 || len(receipt.DispatchableSkills) > 128 {
		return receipt, fail("route_invalid", "invalid route cardinality or task intent")
	}
	if _, err := receipt.arguments(); err != nil {
		return receipt, err
	}
	for _, source := range receipt.sources() {
		if !relativePath(source.Path) || source.Bytes < 0 || source.Bytes > 8<<20 || !hexDigest(source.SHA256) {
			return receipt, fail("route_invalid", "invalid or unbounded receipt source")
		}
	}
	for _, source := range receipt.Projection.ContextFiles {
		parts := strings.Split(source.Path, "/")
		if parts[0] == "10项目" && (len(parts) != 3 || parts[2] != "项目记忆卡.md") {
			return receipt, fail("route_invalid", "project context must be a project root memory card")
		}
	}
	return receipt, nil
}

func hexDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}

func (receipt Receipt) sources() []Source {
	result := []Source{receipt.Projection.Category.Source}
	for _, job := range receipt.Projection.Jobs {
		result = append(result, job.Source)
	}
	for _, ability := range receipt.Projection.Abilities {
		result = append(result, ability.Source)
	}
	return append(result, receipt.Projection.ContextFiles...)
}

func (receipt Receipt) ProjectRoots() []string {
	projects := map[string]bool{}
	result := []string{}
	for _, source := range receipt.Projection.ContextFiles {
		parts := strings.Split(source.Path, "/")
		if len(parts) == 3 && parts[0] == "10项目" && parts[2] == "项目记忆卡.md" {
			if !projects[source.Path] {
				result = append(result, source.Path)
				projects[source.Path] = true
			}
		}
	}
	return result
}

func (receipt Receipt) arguments() ([]string, error) {
	validName := func(name string) bool {
		return validText(name, 256) && strings.TrimSpace(name) == name && name != "" && !strings.ContainsAny(name, "/\\")
	}
	if !validName(receipt.Projection.Category.Name) {
		return nil, fail("route_invalid", "invalid category name")
	}
	args := []string{"route", "--verify-route", "--category", receipt.Projection.Category.Name, "--task-intent", receipt.TaskIntent}
	jobs := map[string]string{}
	mainCount := 0
	for _, job := range receipt.Projection.Jobs {
		if !validName(job.Name) || job.JobID == "" || jobs[job.JobID] != "" {
			return nil, fail("route_invalid", "invalid route jobs")
		}
		jobs[job.JobID] = job.Name
		switch job.Role {
		case "main":
			mainCount++
			args = append(args, "--main-job", job.Name)
		case "collaborator":
			args = append(args, "--collaborator", job.Name)
		default:
			return nil, fail("route_invalid", "invalid job role")
		}
	}
	if mainCount != 1 {
		return nil, fail("route_invalid", "exactly one main job is required")
	}
	for _, ability := range receipt.Projection.Abilities {
		if !validName(ability.Name) || jobs[ability.JobID] == "" {
			return nil, fail("route_invalid", "invalid ability job relationship")
		}
		args = append(args, "--ability", jobs[ability.JobID]+"·"+ability.Name)
	}
	for _, source := range receipt.Projection.ContextFiles {
		args = append(args, "--context-file", source.Path)
	}
	return args, nil
}

func (store *Store) checkSources(receipt Receipt) error {
	seen := map[string]bool{}
	var total int64
	for _, source := range receipt.sources() {
		if seen[source.Path] {
			continue
		}
		seen[source.Path] = true
		total += source.Bytes
		if total > 32<<20 {
			return fail("limit_exceeded", "route sources exceed total byte limit")
		}
		path, err := safePath(store.root, source.Path, false)
		if err != nil {
			return fail("route_invalid", "route source path is missing or unsafe")
		}
		data, err := readFile(path, 8<<20, false)
		if err != nil || int64(len(data)) != source.Bytes || digest(data) != source.SHA256 {
			return fail("route_invalid", "receipt source no longer matches the current workspace")
		}
	}
	if len(seen) != receipt.ExpectedCount || total != receipt.SourceBytes {
		return fail("route_invalid", "receipt source totals do not match")
	}
	return nil
}

func (store *Store) verifierPath() (string, error) {
	name := "ksf-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path, err := safePath(store.root, ".agents/bin/"+name, false)
	if err != nil {
		return "", fail("verifier_unavailable", "current platform KSF verifier is unavailable or unsafe")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || !safeFileInfo(info) || (runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0) {
		return "", fail("verifier_unavailable", "current platform KSF verifier is unavailable or unsafe")
	}
	return path, nil
}

type boundedOutput struct {
	bytes.Buffer
	exceeded bool
}

func (output *boundedOutput) Write(data []byte) (int, error) {
	if output.Len()+len(data) > MaxRequestBytes+128 {
		output.exceeded = true
		return 0, io.ErrShortBuffer
	}
	return output.Buffer.Write(data)
}

func (store *Store) runVerifier(ctx context.Context, args []string) ([]byte, error) {
	path, err := store.verifierPath()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	arguments := append(append([]string{}, args...), "--root", store.root)
	command := exec.CommandContext(ctx, path, arguments...)
	command.Dir = store.root
	for _, value := range os.Environ() {
		name, _, _ := strings.Cut(value, "=")
		if !strings.EqualFold(name, "CODEX_THREAD_ID") && !strings.EqualFold(name, "KSF_ROOT") {
			command.Env = append(command.Env, value)
		}
	}
	command.WaitDelay = time.Second
	var output boundedOutput
	command.Stdout = &output
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		return nil, fail("verifier_unavailable", "current KSF verifier did not return a successful bounded receipt")
	}
	if output.exceeded {
		return nil, fail("verifier_unavailable", "current KSF verifier exceeded output limit")
	}
	line := strings.TrimSuffix(strings.TrimSuffix(output.String(), "\n"), "\r")
	const prefix = "KSF_ROUTE_CONTEXT_VERIFIED "
	if !strings.HasPrefix(line, prefix) || strings.ContainsAny(line, "\r\n") {
		return nil, fail("route_invalid", "current KSF verifier must return exactly one v6 receipt line")
	}
	return []byte(strings.TrimPrefix(line, prefix)), nil
}

func (store *Store) verify(ctx context.Context, raw json.RawMessage) (Receipt, error) {
	receipt, err := ParseReceipt(raw)
	if err != nil {
		return receipt, err
	}
	if err := store.checkSources(receipt); err != nil {
		return receipt, err
	}
	args, err := receipt.arguments()
	if err != nil {
		return receipt, err
	}
	current, err := store.runVerifier(ctx, args)
	if err != nil {
		return receipt, err
	}
	if _, err := ParseReceipt(current); err != nil {
		return receipt, err
	}
	if !bytes.Equal(canonicalJSON(raw), canonicalJSON(json.RawMessage(current))) {
		return receipt, fail("route_invalid", "receipt does not match the current workspace KSF verifier")
	}
	if err := store.checkSources(receipt); err != nil {
		return receipt, err
	}
	return receipt, nil
}

func routeFreshness(err error) string {
	if err == nil {
		return "current"
	}
	var failure *Error
	if errors.As(err, &failure) && failure.Code != "verifier_unavailable" {
		return "stale"
	}
	return "unavailable"
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func workspaceDigest(root string) string { return digest([]byte(filepath.Clean(root))) }
