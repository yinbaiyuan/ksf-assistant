package taskruntime

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorProvesMissingAndRefusesKeyRecreationWithRecords(t *testing.T) {
	value := newFixture(t)
	doctor := value.store.Doctor(context.Background())
	if doctor.Store != "missing" || doctor.Key != "missing" || doctor.Records != 0 {
		t.Fatal("fresh store not explicitly missing", doctor)
	}
	view, _, _, err := value.store.Report(context.Background(), reportRequest("private-thread-one", "create", 0, "unresolved", nil))
	if err != nil {
		t.Fatal(err)
	}
	path, _ := value.store.dataPath(view.TaskID+".json", false)
	before, _ := os.ReadFile(path)
	keyPath := filepath.Join(value.key, "identity.key")
	if err := os.Remove(keyPath); err != nil {
		t.Fatal(err)
	}
	doctor = value.store.Doctor(context.Background())
	if doctor.Store != "available" || doctor.Key != "missing" || doctor.Records != 1 {
		t.Fatal("orphaned records treated as empty", doctor)
	}
	_, _, _, err = value.store.Report(context.Background(), reportRequest("private-thread-new", "create", 0, "unresolved", nil))
	requireCode(t, err, "key_unavailable")
	if _, err := os.Stat(keyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing key was silently replaced")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("orphaned records changed")
	}
	writeTestFile(t, keyPath, []byte("malformed-key"), 0o600)
	doctor = value.store.Doctor(context.Background())
	if doctor.Key != "unavailable" {
		t.Fatal("malformed key reported missing", doctor)
	}
	_, _, _, err = value.store.Report(context.Background(), reportRequest("private-thread-new", "create", 0, "unresolved", nil))
	requireCode(t, err, "key_unavailable")
}

func TestGetFirstOnlyConvertsProvenFreshInstallationToNotFound(t *testing.T) {
	value := newFixture(t)
	t.Setenv("CODEX_THREAD_ID", "private-thread-one")
	get := func() string {
		var output bytes.Buffer
		Run(context.Background(), []string{"get", "--root", value.root}, strings.NewReader(`{"protocol":"ksfassistant-task-runtime-v1","version":1}`), &output, Options{KeyDirectory: value.key})
		return output.String()
	}
	if response := get(); !strings.Contains(response, `"code":"not_found"`) {
		t.Fatal("new-user get must allow safe revision-zero initialization", response)
	}
	if _, err := os.Stat(value.key); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("get-first initialized a key")
	}
	_, _, _, err := value.store.Report(context.Background(), reportRequest("private-thread-one", "create", 0, "unresolved", nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(value.key, "identity.key")); err != nil {
		t.Fatal(err)
	}
	if response := get(); !strings.Contains(response, `"code":"key_unavailable"`) {
		t.Fatal("orphaned records incorrectly treated as fresh installation", response)
	}
	writeTestFile(t, filepath.Join(value.key, "identity.key"), []byte("bad key"), 0o600)
	if response := get(); !strings.Contains(response, `"code":"key_unavailable"`) {
		t.Fatal("damaged key incorrectly treated as fresh installation", response)
	}
}
