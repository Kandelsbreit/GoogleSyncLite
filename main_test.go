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

func TestSanitizeWindowsPath(t *testing.T) {
	input := `E:\SyncFolder\some:invalid*name?file"test|demo.txt`
	expected := `E:\SyncFolder\some_invalid_name_file_test_demo.txt`
	actual := SanitizeWindowsPath(input)
	if actual != expected {
		t.Fatalf("expected '%s', got '%s'", expected, actual)
	}

	// Reserved DOS name check
	resInput := `E:\SyncFolder\sub\aux.txt`
	resExpected := `E:\SyncFolder\sub\_aux.txt`
	resActual := SanitizeWindowsPath(resInput)
	if resActual != resExpected {
		t.Fatalf("expected '%s', got '%s'", resExpected, resActual)
	}

	// Trailing dots and spaces check
	trailInput := `E:\SyncFolder\trail. .\file.txt. `
	trailExpected := `E:\SyncFolder\trail\file.txt`
	trailActual := SanitizeWindowsPath(trailInput)
	if trailActual != trailExpected {
		t.Fatalf("expected '%s', got '%s'", trailExpected, trailActual)
	}

	// Path Traversal dotdot sanitization
	traversalInput := `E:\SyncFolder\..\..\evil.txt`
	traversalExpected := `E:\SyncFolder\_\_\evil.txt`
	traversalActual := SanitizeWindowsPath(traversalInput)
	if traversalActual != traversalExpected {
		t.Fatalf("expected '%s', got '%s'", traversalExpected, traversalActual)
	}
}

func TestPathsAnchoring(t *testing.T) {
	appDir := AppDir()
	if appDir == "" {
		t.Fatal("expected non-empty AppDir")
	}

	cfgPath := ConfigPath()
	if !filepath.IsAbs(cfgPath) {
		t.Fatalf("expected absolute ConfigPath, got %s", cfgPath)
	}

	dbPath := DatabasePath()
	if !filepath.IsAbs(dbPath) {
		t.Fatalf("expected absolute DatabasePath, got %s", dbPath)
	}
}

func TestNormalizeConfig(t *testing.T) {
	cfg := NormalizeConfig(Config{SyncMode: "unexpected", SyncIntervalSeconds: 1, MaxDeleteThreshold: 0})
	if cfg.SyncMode != "local_master" {
		t.Fatalf("unexpected sync mode: %s", cfg.SyncMode)
	}
	if cfg.SyncIntervalSeconds != 60 || cfg.MaxDeleteThreshold != 20 {
		t.Fatalf("config defaults were not normalized: %+v", cfg)
	}
	if !cfg.SafetyShield {
		t.Fatal("safety shield must remain enabled")
	}
}

func TestWindowsSafeRelativePath(t *testing.T) {
	if !IsWindowsSafeRelativePath("folder/file.txt") {
		t.Fatal("ordinary relative path must be accepted")
	}
	if IsWindowsSafeRelativePath("folder/a:b.txt") {
		t.Fatal("path with Windows-illegal characters must be rejected")
	}
}
