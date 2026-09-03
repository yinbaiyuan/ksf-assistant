package bridge

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestFeishuSupervisorOwnsChildLifecycle(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is unavailable")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"bridge-client.js": "process.stdout.write('{}')\n",
		"start-bridge.js":  "setInterval(() => {}, 1000)\n",
	} {
		if err := os.WriteFile(filepath.Join(root, "scripts", name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	supervisor := NewFeishuSupervisor(root, node)
	if err := supervisor.Start(); err != nil {
		t.Fatal(err)
	}
	if running, _ := supervisor.Status()["running"].(bool); !running {
		t.Fatal("expected managed bridge to be running")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := supervisor.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if running, _ := supervisor.Status()["running"].(bool); running {
		t.Fatal("expected managed bridge to stop with the core")
	}
}
