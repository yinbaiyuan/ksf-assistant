package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"ksfassistant/core/internal/larkversion"
)

const PinnedLarkCLIVersion = larkversion.Version
const PinnedLarkCLIUpstreamVersion = larkversion.UpstreamVersion

const (
	larkCLIProbeTimeout = 3 * time.Second
	larkCLIProbeTTL     = 5 * time.Minute
	larkCLIProbeOutput  = 64 * 1024
)

type LarkCLIProbeResult struct {
	State     string    `json:"state"`
	Version   string    `json:"version,omitempty"`
	Code      string    `json:"code,omitempty"`
	Detail    string    `json:"detail,omitempty"`
	CheckedAt time.Time `json:"checkedAt"`
}

type larkCLIProbeEntry struct {
	signature string
	expiresAt time.Time
	result    LarkCLIProbeResult
}

var larkCLIProbeCache = struct {
	sync.Mutex
	entries map[string]larkCLIProbeEntry
}{entries: map[string]larkCLIProbeEntry{}}

// ProbeLarkCLI verifies the exact pinned runtime by executing both its version
// command and a local schema lookup. It never performs a remote Feishu API
// request, so Dashboard refreshes cannot create network traffic or require a
// user token. Results are cached against the binary's file identity.
func ProbeLarkCLI(parent context.Context, binary string) LarkCLIProbeResult {
	now := time.Now().UTC()
	binary = strings.TrimSpace(binary)
	if binary == "" {
		return LarkCLIProbeResult{State: "disabled", Code: "not_configured", Detail: "fixed lark-cli is not configured", CheckedAt: now}
	}
	absolute, err := filepath.Abs(binary)
	if err != nil {
		return unavailableLarkCLIProbe(now, "invalid_path")
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return unavailableLarkCLIProbe(now, "path_unavailable")
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return unavailableLarkCLIProbe(now, "unsafe_path")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return unavailableLarkCLIProbe(now, "not_executable")
	}
	signature := strings.Join([]string{absolute, info.ModTime().UTC().Format(time.RFC3339Nano), strconv.FormatUint(uint64(info.Mode()), 10), strconv.FormatInt(info.Size(), 10)}, "\x00")
	larkCLIProbeCache.Lock()
	entry, found := larkCLIProbeCache.entries[absolute]
	larkCLIProbeCache.Unlock()
	if found && entry.signature == signature && now.Before(entry.expiresAt) {
		return entry.result
	}

	result := executeLarkCLIProbe(parent, absolute, now)
	// A first launch can be delayed by platform executable verification. Do not
	// turn that transient timeout (or another execution failure) into a five
	// minute outage. Only cache results that describe the inspected binary
	// itself; transient failures are retried by the next read/action.
	if cacheableLarkCLIProbe(result) {
		larkCLIProbeCache.Lock()
		larkCLIProbeCache.entries[absolute] = larkCLIProbeEntry{signature: signature, expiresAt: now.Add(larkCLIProbeTTL), result: result}
		larkCLIProbeCache.Unlock()
	}
	return result
}

func cacheableLarkCLIProbe(result LarkCLIProbeResult) bool {
	if result.State == "ready" {
		return true
	}
	switch result.Code {
	case "invalid_path", "path_unavailable", "unsafe_path", "not_executable", "version_mismatch", "schema_probe_invalid":
		return true
	default:
		return false
	}
}

func executeLarkCLIProbe(parent context.Context, binary string, now time.Time) LarkCLIProbeResult {
	ctx, cancel := context.WithTimeout(parent, larkCLIProbeTimeout)
	defer cancel()
	versionOutput, err := runLocalLarkCLIProbe(ctx, binary, "--version")
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return unavailableLarkCLIProbe(now, "probe_timeout")
		}
		return unavailableLarkCLIProbe(now, "probe_failed")
	}
	expected := "lark-cli version " + PinnedLarkCLIVersion
	if strings.TrimSpace(string(versionOutput)) != expected {
		return unavailableLarkCLIProbe(now, "version_mismatch")
	}
	schemaOutput, err := runLocalLarkCLIProbe(ctx, binary, "schema", "approval.approvals.get")
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return unavailableLarkCLIProbe(now, "probe_timeout")
		}
		return unavailableLarkCLIProbe(now, "schema_probe_failed")
	}
	var schema struct {
		Name        string         `json:"name"`
		InputSchema map[string]any `json:"inputSchema"`
	}
	if json.Unmarshal(schemaOutput, &schema) != nil || strings.TrimSpace(schema.Name) == "" || schema.InputSchema == nil {
		return unavailableLarkCLIProbe(now, "schema_probe_invalid")
	}
	return LarkCLIProbeResult{State: "ready", Version: PinnedLarkCLIVersion, Code: "verified", Detail: "lark-cli " + PinnedLarkCLIVersion + " executable and contract verified", CheckedAt: now}
}

func runLocalLarkCLIProbe(ctx context.Context, binary string, arguments ...string) ([]byte, error) {
	stdout := &boundedCommandBuffer{limit: larkCLIProbeOutput}
	stderr := &boundedCommandBuffer{limit: 4096}
	command := exec.CommandContext(ctx, binary, arguments...)
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		return nil, err
	}
	if stdout.overflow || stderr.overflow {
		return nil, errors.New("lark-cli probe output exceeded limit")
	}
	return append([]byte(nil), stdout.Bytes()...), nil
}

func unavailableLarkCLIProbe(now time.Time, code string) LarkCLIProbeResult {
	return LarkCLIProbeResult{State: "unavailable", Code: code, Detail: "fixed lark-cli unavailable: " + code, CheckedAt: now}
}

func resetLarkCLIProbeCacheForTest() {
	larkCLIProbeCache.Lock()
	larkCLIProbeCache.entries = map[string]larkCLIProbeEntry{}
	larkCLIProbeCache.Unlock()
}
