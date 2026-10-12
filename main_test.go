package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
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

func TestScanLocalDeltaRejectsSymlinkWithoutProducingDeletions(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "normal.txt"), []byte("ok"), 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked.txt")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}

	engine := &SyncEngineGo{localRoot: root}
	changes, err := engine.ScanLocalDelta(context.Background(), map[string]FileState{
		"linked.txt": {RelPath: "linked.txt", MD5: "previous"},
	})
	if err == nil {
		t.Fatal("expected scan to fail when it encounters a symlink")
	}
	if changes != nil {
		t.Fatalf("incomplete scan must not return changes that could imply deletion: %+v", changes)
	}
}

func TestRootedLocalAccessRejectsSymlinkEscapeAndAllowsRegularFile(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "redirect")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	engine := &SyncEngineGo{localRoot: root}

	if _, err := engine.openLocalPath(filepath.Join(root, "redirect", "secret.txt")); err == nil {
		t.Fatal("expected rooted open to reject a symlink escape")
	}
	if err := engine.removeLocalPath(filepath.Join(root, "redirect", "secret.txt")); err == nil {
		t.Fatal("expected rooted remove to reject a symlink escape")
	}
	if _, err := os.Stat(filepath.Join(outside, "secret.txt")); !os.IsNotExist(err) {
		t.Fatalf("outside file should remain absent, stat error: %v", err)
	}

	regular := filepath.Join(root, "regular.txt")
	if err := os.WriteFile(regular, []byte("regular"), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := engine.openLocalPath(regular)
	if err != nil {
		t.Fatalf("regular in-root file should open: %v", err)
	}
	_ = file.Close()
	if _, err := engine.statLocalPath(regular); err != nil {
		t.Fatalf("regular in-root file should stat: %v", err)
	}
}

func TestSyncRootRejectsSymlinkToExternalDirectory(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "sync-root")
	if err := os.Symlink(outside, root); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}

	for _, linkedRoot := range []string{root, root + string(os.PathSeparator)} {
		t.Run(filepath.Base(filepath.Clean(linkedRoot)), func(t *testing.T) {
			engine := &SyncEngineGo{localRoot: linkedRoot}
			if _, err := engine.openLocalPath(filepath.Join(linkedRoot, "secret.txt")); err == nil {
				t.Fatal("rooted file access must reject a linked sync root")
			}
			changes, err := engine.ScanLocalDelta(context.Background(), nil)
			if err == nil {
				t.Fatal("scan must reject a linked sync root")
			}
			if changes != nil {
				t.Fatalf("linked sync root must not produce a snapshot: %+v", changes)
			}
		})
	}
}

func TestDownloadFileRejectsSymlinkParentBeforeNetworkRequest(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "redirect")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}

	engine := &SyncEngineGo{localRoot: root}
	err := engine.DownloadFile(context.Background(), "file-id", filepath.Join(root, "redirect", "new", "file.txt"), "")
	if err == nil {
		t.Fatal("expected download destination through symlink to be rejected")
	}
	if _, err := os.Stat(filepath.Join(outside, "new")); !os.IsNotExist(err) {
		t.Fatalf("download must not create directories outside the root, stat error: %v", err)
	}
}

func TestAnchorFileIsIgnored(t *testing.T) {
	// .google_sync_anchor starts with '.' and must be filtered by IsIgnoredRelPath
	// to prevent it from being uploaded to Google Drive.
	if !IsIgnoredRelPath(".google_sync_anchor") {
		t.Fatal(".google_sync_anchor must be ignored by IsIgnoredRelPath (starts with '.')")
	}
	// Ensure regular hidden files are also ignored
	if !IsIgnoredRelPath(".gitignore") {
		t.Fatal(".gitignore must be ignored by IsIgnoredRelPath")
	}
	// Ensure normal files are not ignored
	if IsIgnoredRelPath("documents/report.pdf") {
		t.Fatal("regular file must not be ignored by IsIgnoredRelPath")
	}
}

func TestStatusPersistence(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := OpenDatabase(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.SetMeta("last_sync_display", "15:42:00"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetMeta("last_sync_msg", "Готово! Все файлы защищены"); err != nil {
		t.Fatal(err)
	}

	if got := db.GetMeta("last_sync_display"); got != "15:42:00" {
		t.Fatalf("expected 15:42:00, got %s", got)
	}
	if got := db.GetMeta("last_sync_msg"); got != "Готово! Все файлы защищены" {
		t.Fatalf("expected 'Готово! Все файлы защищены', got %s", got)
	}
}

func TestFileWatcherFilters(t *testing.T) {
	tmpDir := t.TempDir()
	var triggered bool
	fw, err := NewFileWatcher(tmpDir, nil, func() {
		triggered = true
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer fw.Stop()

	// Verify ignored paths filter
	if !fw.isIgnoredPath(filepath.Join(tmpDir, ".google_sync_anchor")) {
		t.Fatal("expected anchor to be ignored")
	}
	if !fw.isIgnoredPath(filepath.Join(tmpDir, "test.tmp")) {
		t.Fatal("expected .tmp to be ignored")
	}
	if !fw.isIgnoredPath(filepath.Join(tmpDir, "~$document.docx")) {
		t.Fatal("expected ~$ to be ignored")
	}
	if !fw.isIgnoredPath(filepath.Join(tmpDir, "Thumbs.db")) {
		t.Fatal("expected Thumbs.db to be ignored")
	}
	if fw.isIgnoredPath(filepath.Join(tmpDir, "important_file.dat")) {
		t.Fatal("expected regular file to NOT be ignored")
	}
	_ = triggered
}

func TestScanLocalDeltaOptimized(t *testing.T) {
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "sub")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}

	f1 := filepath.Join(tmpDir, "f1.txt")
	f2 := filepath.Join(subDir, "f2.txt")
	_ = os.WriteFile(f1, []byte("data1"), 0644)
	_ = os.WriteFile(f2, []byte("data2"), 0644)

	dbDir := t.TempDir()
	db, err := OpenDatabase(filepath.Join(dbDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	engine := NewSyncEngineGo(nil, db, tmpDir, "root", nil)
	changes, err := engine.ScanLocalDelta(context.Background(), nil)
	if err != nil {
		t.Fatalf("ScanLocalDelta error: %v", err)
	}

	if len(changes.NewOrChanged) != 2 {
		t.Fatalf("expected 2 new files, got %d", len(changes.NewOrChanged))
	}
}

func TestFormatBytesAndSpeed(t *testing.T) {
	if s := FormatBytes(0); s != "0 Б" {
		t.Fatalf("expected '0 Б', got '%s'", s)
	}
	if s := FormatBytes(1024); s != "1.0 КБ" {
		t.Fatalf("expected '1.0 КБ', got '%s'", s)
	}
	if s := FormatBytes(1048576 * 15); s != "15.0 МБ" {
		t.Fatalf("expected '15.0 МБ', got '%s'", s)
	}
	if s := FormatBytes(1073741824 * 5); s != "5.0 ГБ" {
		t.Fatalf("expected '5.0 ГБ', got '%s'", s)
	}

	if s := FormatSpeed(0); s != "0 Б/с" {
		t.Fatalf("expected '0 Б/с', got '%s'", s)
	}
	if s := FormatSpeed(1048576 * 2.5); s != "2.5 МБ/с" {
		t.Fatalf("expected '2.5 МБ/с', got '%s'", s)
	}
}

func TestFormatETA(t *testing.T) {
	if s := FormatETA(0); s != "00:00" {
		t.Fatalf("expected '00:00', got '%s'", s)
	}
	if s := FormatETA(65); s != "01:05" {
		t.Fatalf("expected '01:05', got '%s'", s)
	}
	if s := FormatETA(3665); s != "01:01:05" {
		t.Fatalf("expected '01:01:05', got '%s'", s)
	}
}

func TestConfigConcurrencyNormalization(t *testing.T) {
	cfg := NormalizeConfig(Config{
		ConcurrentUploads: 0,
	})
	if cfg.ConcurrentUploads != 4 {
		t.Fatalf("expected default 4, got %d", cfg.ConcurrentUploads)
	}

	cfgMax := NormalizeConfig(Config{
		ConcurrentUploads: 99,
	})
	if cfgMax.ConcurrentUploads != 16 {
		t.Fatalf("expected capped 16, got %d", cfgMax.ConcurrentUploads)
	}

	cfgCustom := NormalizeConfig(Config{
		ConcurrentUploads: 8,
	})
	if cfgCustom.ConcurrentUploads != 8 {
		t.Fatalf("expected 8, got %d", cfgCustom.ConcurrentUploads)
	}
}

func TestGetOrComputeMD5ThreadSafety(t *testing.T) {
	tmpDir := t.TempDir()
	sample := filepath.Join(tmpDir, "parallel.txt")
	_ = os.WriteFile(sample, []byte("parallel md5 test content"), 0644)

	dbDir := t.TempDir()
	db, err := OpenDatabase(filepath.Join(dbDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	engine := NewSyncEngineGo(nil, db, tmpDir, "root", nil)
	engine.SetConcurrency(8)

	ctx := context.Background()
	loc := FileState{RelPath: "parallel.txt"}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h, err := engine.GetOrComputeMD5(ctx, "parallel.txt", loc)
			if err != nil || h == "" {
				t.Errorf("error computing md5: %v", err)
			}
		}()
	}
	wg.Wait()
}

func TestIgnoreMatcher(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a .syncignore file in tmpDir
	syncIgnoreContent := `
# Comment
node_modules/
*.secret
build/
`
	if err := os.WriteFile(filepath.Join(tmpDir, ".syncignore"), []byte(syncIgnoreContent), 0644); err != nil {
		t.Fatal(err)
	}

	customRules := []string{"*.log", "dist/"}
	matcher := NewIgnoreMatcher(tmpDir, customRules)

	// Builtin rules
	if !matcher.ShouldIgnore("desktop.ini", false) {
		t.Error("expected desktop.ini to be ignored")
	}
	if !matcher.ShouldIgnore("thumbs.db", false) {
		t.Error("expected thumbs.db to be ignored")
	}
	if !matcher.ShouldIgnore("temp.tmp", false) {
		t.Error("expected temp.tmp to be ignored")
	}
	if !matcher.ShouldIgnore("~$$doc.docx", false) {
		t.Error("expected ~$ to be ignored")
	}

	// .syncignore rules
	if !matcher.ShouldIgnore("node_modules/pkg/index.js", false) {
		t.Error("expected file inside node_modules to be ignored")
	}
	if !matcher.ShouldIgnore("node_modules", true) {
		t.Error("expected node_modules directory to be ignored")
	}
	if !matcher.ShouldIgnore("api.secret", false) {
		t.Error("expected *.secret to be ignored")
	}
	if !matcher.ShouldIgnore("build/bundle.js", false) {
		t.Error("expected build directory file to be ignored")
	}

	// Custom rules from config
	if !matcher.ShouldIgnore("app.log", false) {
		t.Error("expected *.log to be ignored")
	}
	if !matcher.ShouldIgnore("dist/main.js", false) {
		t.Error("expected dist/ directory to be ignored")
	}

	// Regular files should NOT be ignored
	if matcher.ShouldIgnore("src/main.go", false) {
		t.Error("expected src/main.go to NOT be ignored")
	}
	if matcher.ShouldIgnore("photo.png", false) {
		t.Error("expected photo.png to NOT be ignored")
	}
}

func TestAppPauseResume(t *testing.T) {
	app := &App{}

	if app.IsPaused() {
		t.Fatal("expected initially not paused")
	}

	// Pause manual
	_ = app.PauseSync(0)
	if !app.IsPaused() {
		t.Fatal("expected paused after PauseSync(0)")
	}

	status := app.GetStatus()
	if isPaused, ok := status["is_paused"].(bool); !ok || !isPaused {
		t.Fatal("expected status[is_paused] to be true")
	}

	// Resume
	_ = app.ResumeSync()
	if app.IsPaused() {
		t.Fatal("expected unpaused after ResumeSync()")
	}

	statusAfter := app.GetStatus()
	if isPaused, ok := statusAfter["is_paused"].(bool); !ok || isPaused {
		t.Fatal("expected status[is_paused] to be false")
	}

	// Pause with duration
	_ = app.PauseSync(60)
	if !app.IsPaused() {
		t.Fatal("expected paused after PauseSync(60)")
	}
	statusTimed := app.GetStatus()
	remSec, ok := statusTimed["pause_remaining_seconds"].(int64)
	if !ok || remSec <= 0 {
		t.Fatalf("expected positive pause_remaining_seconds, got %v", statusTimed["pause_remaining_seconds"])
	}

	_ = app.ResumeSync()
	if app.IsPaused() {
		t.Fatal("expected unpaused after final ResumeSync()")
	}
}

func TestThrottledReader(t *testing.T) {
	data := []byte("Hello rate limited world! This is a test chunk to verify that no bytes are lost during throttling.")
	r := bytes.NewReader(data)

	// Test 0 kbps = unlimited
	unlimited := NewThrottledReader(r, 0)
	buf, err := io.ReadAll(unlimited)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf, data) {
		t.Fatal("data mismatch in unlimited reader")
	}

	// Test with rate limiting (e.g. 50 KB/s)
	r2 := bytes.NewReader(data)
	throttled := NewThrottledReader(r2, 50)
	buf2, err := io.ReadAll(throttled)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf2, data) {
		t.Fatal("data mismatch in throttled reader")
	}
}

func TestRecentFilesDatabase(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "recent_test.db")
	db, err := OpenDatabase(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Initial query should be empty
	items, err := db.GetRecentFiles(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("expected 0 items initially, got %d", len(items))
	}

	// Add items
	_ = db.AddRecentFile("docs/report.pdf", "upload", 1048576)
	_ = db.AddRecentFile("images/photo.png", "download", 2097152)
	_ = db.AddRecentFile("temp/old.log", "delete", 0)

	items, err = db.GetRecentFiles(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}

	// Check most recent is first
	if items[0].RelPath != "temp/old.log" || items[0].Action != "delete" {
		t.Errorf("unexpected first item: %+v", items[0])
	}
	if items[1].RelPath != "images/photo.png" || items[1].Action != "download" || items[1].SizeFormatted != "2.0 МБ" {
		t.Errorf("unexpected second item: %+v", items[1])
	}
	if items[2].RelPath != "docs/report.pdf" || items[2].Action != "upload" || items[2].SizeFormatted != "1.0 МБ" {
		t.Errorf("unexpected third item: %+v", items[2])
	}

	// Test App wrapper
	app := NewApp(db)
	appItems, err := app.GetRecentFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(appItems) != 3 {
		t.Fatalf("expected 3 app items, got %d", len(appItems))
	}
}

func TestIsInQuietHours(t *testing.T) {
	// Daytime range 09:00 - 18:00
	t1 := time.Date(2026, 10, 12, 12, 30, 0, 0, time.UTC) // inside
	t2 := time.Date(2026, 10, 12, 8, 30, 0, 0, time.UTC)  // outside
	t3 := time.Date(2026, 10, 12, 19, 0, 0, 0, time.UTC)  // outside

	if !IsInQuietHours(t1, "09:00", "18:00") {
		t.Errorf("expected t1 (12:30) to be in quiet hours 09:00-18:00")
	}
	if IsInQuietHours(t2, "09:00", "18:00") {
		t.Errorf("expected t2 (08:30) NOT to be in quiet hours 09:00-18:00")
	}
	if IsInQuietHours(t3, "09:00", "18:00") {
		t.Errorf("expected t3 (19:00) NOT to be in quiet hours 09:00-18:00")
	}

	// Overnight range 23:00 - 07:00
	tNight1 := time.Date(2026, 10, 12, 23, 30, 0, 0, time.UTC) // inside
	tNight2 := time.Date(2026, 10, 12, 3, 15, 0, 0, time.UTC)  // inside
	tDay := time.Date(2026, 10, 12, 14, 0, 0, 0, time.UTC)     // outside

	if !IsInQuietHours(tNight1, "23:00", "07:00") {
		t.Errorf("expected tNight1 (23:30) to be in overnight quiet hours 23:00-07:00")
	}
	if !IsInQuietHours(tNight2, "23:00", "07:00") {
		t.Errorf("expected tNight2 (03:15) to be in overnight quiet hours 23:00-07:00")
	}
	if IsInQuietHours(tDay, "23:00", "07:00") {
		t.Errorf("expected tDay (14:00) NOT to be in overnight quiet hours 23:00-07:00")
	}

	// Invalid input handling
	if IsInQuietHours(tDay, "invalid", "07:00") {
		t.Errorf("expected invalid format to return false")
	}
}

func TestCopyLocalFile(t *testing.T) {
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "orig.txt")
	dst := filepath.Join(tmpDir, "sub", "copy.txt")

	content := []byte("Conflict Test Data 12345")
	if err := os.WriteFile(src, content, 0644); err != nil {
		t.Fatal(err)
	}

	if err := copyLocalFile(src, dst); err != nil {
		t.Fatalf("copyLocalFile failed: %v", err)
	}

	read, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("failed reading copied file: %v", err)
	}
	if string(read) != string(content) {
		t.Fatalf("expected copied content '%s', got '%s'", content, read)
	}
}

func TestRecentFileDriveLinkAndConflictStrategy(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "conflict_test.db")
	db, err := OpenDatabase(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// 1. Add file to files table
	err = db.UpsertFile(FileState{
		RelPath: "project/design.psd",
		FileID:  "drive_file_id_999",
		Size:    5000,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 2. Add recent file entry
	_ = db.AddRecentFile("project/design.psd", "upload", 5000)

	// 3. Query recent files and verify DriveLink joined
	items, err := db.GetRecentFiles(5)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 recent file, got %d", len(items))
	}
	expectedLink := "https://drive.google.com/file/d/drive_file_id_999/view"
	if items[0].DriveLink != expectedLink {
		t.Fatalf("expected DriveLink %s, got %s", expectedLink, items[0].DriveLink)
	}

	// 4. Test SetConflictStrategy on engine
	engine := &SyncEngineGo{}
	engine.SetConflictStrategy("local_wins")
	if engine.conflictStrategy != "local_wins" {
		t.Errorf("expected local_wins, got %s", engine.conflictStrategy)
	}
	engine.SetConflictStrategy("remote_wins")
	if engine.conflictStrategy != "remote_wins" {
		t.Errorf("expected remote_wins, got %s", engine.conflictStrategy)
	}
	engine.SetConflictStrategy("unknown")
	if engine.conflictStrategy != "keep_both" {
		t.Errorf("expected fallback to keep_both, got %s", engine.conflictStrategy)
	}
}

func TestCombinedIgnoreRules(t *testing.T) {
	cfg := Config{
		CustomIgnoreRules: []string{"*.tmp", "node_modules"},
		ExcludedFolders:   []string{"BigVideos", "HeavyArchive"},
	}
	combined := cfg.CombinedIgnoreRules()
	if len(combined) != 4 {
		t.Fatalf("expected 4 combined rules, got %d", len(combined))
	}
	matcher := NewIgnoreMatcher("", combined)
	if !matcher.ShouldIgnore("BigVideos", true) {
		t.Errorf("expected BigVideos folder to be ignored")
	}
	if !matcher.ShouldIgnore("BigVideos/clip.mp4", false) {
		t.Errorf("expected BigVideos/clip.mp4 to be ignored")
	}
	if matcher.ShouldIgnore("Photos/image.png", false) {
		t.Errorf("expected Photos/image.png NOT to be ignored")
	}
}

func TestSearchFilesAndStorageBreakdown(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "search_test.db")
	db, err := OpenDatabase(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Insert test files
	_ = db.UpsertFile(FileState{RelPath: "docs/annual_report_2026.pdf", FileID: "id_pdf", Size: 2048})
	_ = db.UpsertFile(FileState{RelPath: "photos/family_vacation.jpg", FileID: "id_jpg", Size: 4096})
	_ = db.UpsertFile(FileState{RelPath: "code/main.go", FileID: "id_go", Size: 1024})
	_ = db.UpsertFile(FileState{RelPath: "archive/backup.zip", FileID: "id_zip", Size: 8192})

	// 1. Test SearchFiles with query
	results, err := db.SearchFiles("report", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].FileName != "annual_report_2026.pdf" {
		t.Fatalf("expected 1 result for 'report', got %v", results)
	}
	if results[0].DriveLink != "https://drive.google.com/file/d/id_pdf/view" {
		t.Errorf("unexpected drive link: %s", results[0].DriveLink)
	}

	// 2. Test Multi-word SearchFiles
	results, err = db.SearchFiles("family vacation", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].FileName != "family_vacation.jpg" {
		t.Fatalf("expected 1 result for 'family vacation', got %v", results)
	}

	// 3. Test Empty SearchFiles (all files)
	allResults, err := db.SearchFiles("", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(allResults) != 4 {
		t.Fatalf("expected 4 results, got %d", len(allResults))
	}

	// 4. Test Storage Breakdown
	breakdown, err := db.GetStorageBreakdown()
	if err != nil {
		t.Fatal(err)
	}
	if breakdown.TotalFiles != 4 {
		t.Fatalf("expected 4 total files, got %d", breakdown.TotalFiles)
	}
	expectedTotalBytes := int64(2048 + 4096 + 1024 + 8192)
	if breakdown.TotalBytes != expectedTotalBytes {
		t.Fatalf("expected %d total bytes, got %d", expectedTotalBytes, breakdown.TotalBytes)
	}
}

func TestDatabaseGetFileAndFormatTimeAgo(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_getfile.db")
	db, err := OpenDatabase(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// 1. GetFile on non-existent file
	f, err := db.GetFile("missing.txt")
	if err != nil {
		t.Fatal(err)
	}
	if f != nil {
		t.Fatalf("expected nil for missing file, got %+v", f)
	}

	// 2. Insert and GetFile
	st := FileState{
		RelPath: "docs/doc.pdf",
		FileID:  "drive_doc_123",
		MD5:     "abc123",
		MTime:   1700000000,
		Size:    4096,
		IsDir:   false,
	}
	if err := db.UpsertFile(st); err != nil {
		t.Fatal(err)
	}

	f, err = db.GetFile("docs/doc.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if f == nil || f.FileID != "drive_doc_123" || f.Size != 4096 {
		t.Fatalf("unexpected file state: %+v", f)
	}

	// 3. Test FormatTimeAgo
	now := time.Now()
	if FormatTimeAgo(now.Add(-10*time.Second)) != "только что" {
		t.Errorf("unexpected time ago: %s", FormatTimeAgo(now.Add(-10*time.Second)))
	}
	if FormatTimeAgo(now.Add(-15*time.Minute)) != "15 мин. назад" {
		t.Errorf("unexpected time ago: %s", FormatTimeAgo(now.Add(-15*time.Minute)))
	}
	if FormatTimeAgo(now.Add(-3*time.Hour)) != "3 ч. назад" {
		t.Errorf("unexpected time ago: %s", FormatTimeAgo(now.Add(-3*time.Hour)))
	}

	// 4. Test App helper validation
	app := NewApp(db)
	url, err := app.GetFileDriveURL("docs/doc.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://drive.google.com/file/d/drive_doc_123/view" {
		t.Fatalf("unexpected drive URL: %s", url)
	}

	// Missing file for sharing
	_, err = app.CreateShareLink("nonexistent.txt", true)
	if err == nil {
		t.Fatal("expected error for non-existent file share link")
	}

	// Missing file for revisions
	_, err = app.GetFileRevisions("nonexistent.txt")
	if err == nil {
		t.Fatal("expected error for non-existent file revisions")
	}
}




