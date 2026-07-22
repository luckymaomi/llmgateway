package releasearchive

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteCreatesCanonicalDeterministicArchive(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeArchiveFixture(t, root, "README.md", "release\n")
	writeArchiveFixture(t, root, "deploy/install-linux.sh", "#!/usr/bin/env bash\n")
	writeArchiveFixture(t, root, "llmgateway", "binary")
	entries := []string{"llmgateway", "README.md", "deploy/install-linux.sh"}
	modifiedAt := time.Date(2026, time.July, 22, 12, 34, 56, 0, time.UTC)

	firstPath := filepath.Join(t.TempDir(), "first.zip")
	secondPath := filepath.Join(t.TempDir(), "second.zip")
	for _, outputPath := range []string{firstPath, secondPath} {
		if err := Write(Options{SourceDirectory: root, OutputPath: outputPath, ModifiedAt: modifiedAt, Entries: entries}); err != nil {
			t.Fatalf("Write(%s): %v", outputPath, err)
		}
	}
	firstBytes, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("release archive changed between identical writes")
	}

	reader, err := zip.OpenReader(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	wantNames := []string{"README.md", "deploy/install-linux.sh", "llmgateway"}
	wantModes := []os.FileMode{0o644, 0o755, 0o755}
	if len(reader.File) != len(wantNames) {
		t.Fatalf("archive contains %d entries, want %d", len(reader.File), len(wantNames))
	}
	for index, file := range reader.File {
		if file.Name != wantNames[index] {
			t.Fatalf("entry %d name = %q, want %q", index, file.Name, wantNames[index])
		}
		if file.CreatorVersion>>8 != 3 {
			t.Fatalf("entry %s creator platform = %d, want Unix", file.Name, file.CreatorVersion>>8)
		}
		if file.Mode().Perm() != wantModes[index] || !file.Mode().IsRegular() {
			t.Fatalf("entry %s mode = %v, want regular %v", file.Name, file.Mode(), wantModes[index])
		}
		if !file.Modified.Equal(modifiedAt) {
			t.Fatalf("entry %s modified = %s, want %s", file.Name, file.Modified, modifiedAt)
		}
	}
}

func TestWriteRejectsTraversal(t *testing.T) {
	t.Parallel()

	outputPath := filepath.Join(t.TempDir(), "invalid.zip")
	err := Write(Options{
		SourceDirectory: t.TempDir(),
		OutputPath:      outputPath,
		ModifiedAt:      time.Date(2026, time.July, 22, 12, 34, 56, 0, time.UTC),
		Entries:         []string{"../outside"},
	})
	if err == nil {
		t.Fatal("Write accepted a traversal entry")
	}
	if _, statErr := os.Stat(outputPath); !os.IsNotExist(statErr) {
		t.Fatalf("invalid archive output remains: %v", statErr)
	}
}

func writeArchiveFixture(t *testing.T, root, relativePath, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
