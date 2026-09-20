package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHasherGo(t *testing.T) {
	tmpDir := t.TempDir()
	sampleFile := filepath.Join(tmpDir, "test.txt")
	err := os.WriteFile(sampleFile, []byte("Hello Google Drive Sync!"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	hash, err := ComputeMD5(sampleFile)
	if err != nil {
		t.Fatal(err)
	}

	expected := "96844ad70852ebc9318620e79375a7fa"
	if hash != expected {
		t.Fatalf("expected hash %s, got %s", expected, hash)
	}
}

func TestDatabaseGo(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := OpenDatabase(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	err = db.UpsertFile(FileState{
		RelPath: "docs/test.txt",
		FileID:  "id123",
		MD5:     "hash123",
		MTime:   100.0,
		Size:    50,
	})
	if err != nil {
		t.Fatal(err)
	}

	all, err := db.GetAllFiles()
	if err != nil {
		t.Fatal(err)
	}

	item, ok := all["docs/test.txt"]
	if !ok || item.FileID != "id123" {
		t.Fatalf("expected file in db, got %v", item)
	}
}

func TestPathNormalization(t *testing.T) {
	relPath := "Cloud/Cloud/d/2N/file.c9r"
	dir := filepath.ToSlash(filepath.Dir(relPath))
	base := filepath.Base(relPath)

	if dir != "Cloud/Cloud/d/2N" {
		t.Fatalf("expected dir 'Cloud/Cloud/d/2N', got '%s'", dir)
	}
	if base != "file.c9r" {
		t.Fatalf("expected base 'file.c9r', got '%s'", base)
	}

	// Test Windows backslash path
	winPath := `Cloud\Cloud\d\2N\file.c9r`
	clean := filepath.ToSlash(winPath)
	if clean != "Cloud/Cloud/d/2N/file.c9r" {
		t.Fatalf("expected clean 'Cloud/Cloud/d/2N/file.c9r', got '%s'", clean)
	}
}
