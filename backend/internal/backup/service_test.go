package backup

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestValidID(t *testing.T) {
	valid := []string{
		"netassets-20260724T010203Z-a1b2c3d4",
		"netassets-0",
	}
	for _, id := range valid {
		if !validID(id) {
			t.Errorf("validID(%q) = false, want true", id)
		}
	}
	invalid := []string{"", "backup-20260724T010203Z-a1b2c3d4", "netassets-../../etc", "netassets-a_b", "netassets-中文"}
	for _, id := range invalid {
		if validID(id) {
			t.Errorf("validID(%q) = true, want false", id)
		}
	}
}

func TestWriteDataArchiveSkipsBackups(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "uploads", "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "backups", "old-backup"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "uploads", "nested", "evidence.txt"), []byte("evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "backups", "old-backup", "database.dump"), []byte("not included"), 0o600); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(t.TempDir(), "app-data.tar.gz")
	if err := writeDataArchive(dataDir, archivePath); err != nil {
		t.Fatalf("writeDataArchive: %v", err)
	}
	file, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	entries := map[string]bool{}
	for {
		header, err := reader.Next()
		if err != nil {
			break
		}
		entries[header.Name] = true
	}
	if !entries["uploads/nested/evidence.txt"] {
		t.Fatalf("attachment file was not archived: %#v", entries)
	}
	for name := range entries {
		if len(name) >= len("backups") && name[:len("backups")] == "backups" {
			t.Fatalf("backup directory leaked into archive: %q", name)
		}
	}
}

func TestWriteDataArchiveRejectsSymbolicLink(t *testing.T) {
	dataDir := t.TempDir()
	target := filepath.Join(t.TempDir(), "target.txt")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dataDir, "outside-link")); err != nil {
		t.Skipf("symbolic links not supported in test environment: %v", err)
	}
	if err := writeDataArchive(dataDir, filepath.Join(t.TempDir(), "app-data.tar.gz")); err == nil {
		t.Fatal("writeDataArchive succeeded for a symbolic link, want error")
	}
}
