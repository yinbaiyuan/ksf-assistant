package feishu

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestProbeLarkCLIVerifiesPinnedExecutableAndCachesResult(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is POSIX-only")
	}
	resetLarkCLIProbeCacheForTest()
	root := t.TempDir()
	countPath := filepath.Join(root, "count")
	binary := filepath.Join(root, "lark-cli")
	script := fmt.Sprintf(`#!/bin/sh
printf x >> %q
case "$1" in
  --version) printf 'lark-cli version 1.0.92\n' ;;
  schema) printf '{"name":"approval approvals get","inputSchema":{"type":"object"}}\n' ;;
  *) exit 2 ;;
esac
`, countPath)
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	first := ProbeLarkCLI(context.Background(), binary)
	second := ProbeLarkCLI(context.Background(), binary)
	if first.State != "ready" || first.Version != PinnedLarkCLIVersion || second.State != "ready" {
		t.Fatalf("unexpected probe results: %#v %#v", first, second)
	}
	count, err := os.ReadFile(countPath)
	if err != nil || string(count) != "xx" {
		t.Fatalf("expected one two-command probe, count=%q err=%v", count, err)
	}
}

func TestProbeLarkCLIRejectsWrongVersionAndNonExecutableFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is POSIX-only")
	}
	resetLarkCLIProbeCacheForTest()
	root := t.TempDir()
	binary := filepath.Join(root, "lark-cli")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf 'lark-cli version 1.0.91\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if result := ProbeLarkCLI(context.Background(), binary); result.State != "unavailable" || result.Code != "version_mismatch" {
		t.Fatalf("wrong version was accepted: %#v", result)
	}
	if err := os.Chmod(binary, 0o600); err != nil {
		t.Fatal(err)
	}
	if result := ProbeLarkCLI(context.Background(), binary); result.Code != "not_executable" {
		t.Fatalf("non-executable file was accepted: %#v", result)
	}
}
