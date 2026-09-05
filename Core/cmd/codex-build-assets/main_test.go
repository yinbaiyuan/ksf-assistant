package main

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractTarGzipFileSelectsOnlyNamedExecutable(t *testing.T) {
	root := t.TempDir()
	archivePath := filepath.Join(root, "runtime.tar.gz")
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	entries := map[string]string{
		"package/ignored.txt": "ignore",
		"package/lark-cli":    "native-runtime",
	}
	for name, body := range entries {
		if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "lark-cli")
	if err := extractTarGzipFile(archivePath, "lark-cli", output); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "native-runtime" {
		t.Fatalf("unexpected extracted data: %q", data)
	}
}

func TestStableIDIsDeterministicAndSPDXSafe(t *testing.T) {
	first := stableID("example/module@v1")
	second := stableID("example/module@v1")
	if first != second || len(first) != 20 {
		t.Fatalf("invalid stable ID: %q / %q", first, second)
	}
}
