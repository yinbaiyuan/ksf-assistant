package feishu

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestHostContextStoreStatesAndPermissions(t *testing.T) {
	dataRoot := filepath.Join(t.TempDir(), "private")
	store := NewHostContextStore(dataRoot)

	context, err := store.SaveKSFRoot("")
	if err != nil {
		t.Fatal(err)
	}
	if context.KSF.State != KSFNotConfigured || context.KSF.Root != "" {
		t.Fatalf("unexpected unconfigured context: %#v", context.KSF)
	}

	ksfRoot := filepath.Join(t.TempDir(), "KSF")
	if err := os.Mkdir(ksfRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	context, err = store.SaveKSFRoot(ksfRoot)
	if err != nil {
		t.Fatal(err)
	}
	wantRoot, _ := filepath.EvalSymlinks(ksfRoot)
	if context.KSF.State != KSFReady || context.KSF.Root != wantRoot {
		t.Fatalf("unexpected ready context: %#v", context.KSF)
	}

	context, err = store.SaveKSFRoot(filepath.Join(ksfRoot, "missing"))
	if err != nil {
		t.Fatal(err)
	}
	if context.KSF.State != KSFInvalid || context.KSF.Root != "" {
		t.Fatalf("unexpected invalid context: %#v", context.KSF)
	}

	if runtime.GOOS != "windows" {
		rootInfo, err := os.Stat(dataRoot)
		if err != nil {
			t.Fatal(err)
		}
		fileInfo, err := os.Stat(store.Path())
		if err != nil {
			t.Fatal(err)
		}
		if rootInfo.Mode().Perm() != 0o700 || fileInfo.Mode().Perm() != 0o600 {
			t.Fatalf("unexpected permissions root=%o file=%o", rootInfo.Mode().Perm(), fileInfo.Mode().Perm())
		}
	}
}
