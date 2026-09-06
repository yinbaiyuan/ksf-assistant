package usercommand

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPinnedOfficialCommandFlags(t *testing.T) {
	binary := os.Getenv("KSF_USERCOMMAND_PINNED_CLI")
	if binary == "" {
		t.Skip("set KSF_USERCOMMAND_PINNED_CLI to the verified offline 1.0.93 binary for source-contract validation")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	env := []string{"HOME=" + home, "USERPROFILE=" + home, "PATH=" + os.Getenv("PATH"), "LARKSUITE_CLI_CONFIG_DIR=" + filepath.Join(home, "config"), "LARKSUITE_CLI_REMOTE_META=off", "LARKSUITE_CLI_NO_UPDATE_NOTIFIER=1", "LARKSUITE_CLI_NO_SKILLS_NOTIFIER=1"}
	version := exec.Command(binary, "--version")
	version.Env = env
	output, err := version.Output()
	if err != nil || !strings.Contains(string(output), Version) {
		t.Fatal("wrong fixed CLI")
	}
	for _, spec := range legacySpecifications {
		t.Run(spec.path, func(t *testing.T) {
			args := append(strings.Fields(spec.path), "--help")
			command := exec.Command(binary, args...)
			command.Env = env
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("unavailable fixed command: %s", spec.path)
			}
			for _, flag := range strings.Fields(spec.values + " " + spec.switches) {
				if !strings.Contains(string(output), "--"+flag) {
					t.Errorf("unsupported flag %s in fixed CLI", flag)
				}
			}
		})
	}
}
