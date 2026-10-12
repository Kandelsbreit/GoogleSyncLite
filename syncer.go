package main

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	pathPkg "path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
)

type DriveFileMeta struct {
	ID           string
	Name         string
	Size         int64
	MD5          string
	ModifiedTime string
	ParentID     string
	IsDir        bool
	Trashed      bool
}

type SyncSummary struct {
	VerifiedCount int
	UploadedCount int
	DownloadCount int
	DeletedCount  int
	ConflictCount int
}

type SyncProgress struct {
	Phase          string  `json:"phase"`           // "idle", "scanning", "transferring", "done", "error"
	TotalFiles     int     `json:"total_files"`     // total count of files to upload/download
	ProcessedFiles int     `json:"processed_files"` // files processed so far
	CurrentFile    string  `json:"current_file"`    // name/path of active file being synced
	TotalBytes     int64   `json:"total_bytes"`     // total bytes to upload
	ProcessedBytes int64   `json:"processed_bytes"` // bytes transferred so far
	SpeedBps       float64 `json:"speed_bps"`       // current speed in bytes per second
	SpeedFormatted string  `json:"speed_formatted"` // formatted speed string e.g. "12.4 МБ/с"
	ETASeconds     int     `json:"eta_seconds"`     // estimated seconds left
	ETAFormatted   string  `json:"eta_formatted"`   // formatted ETA e.g. "01:24"
	Percent        float64 `json:"percent"`         // progress percentage (0.0 - 100.0)
	ActiveWorkers  int     `json:"active_workers"`  // count of concurrent threads
}

func FormatETA(seconds int) string {
	if seconds <= 0 {
		return "00:00"
	}
	if seconds >= 3600 {
		h := seconds / 3600
		m := (seconds % 3600) / 60
		s := seconds % 60
		return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
	}
	m := seconds / 60
	s := seconds % 60
	return fmt.Sprintf("%02d:%02d", m, s)
}

type rootedLocalFS struct {
	root *os.Root
}

func (r rootedLocalFS) Open(name string) (fs.File, error) {
	return r.root.Open(name)
}

type SyncEngineGo struct {
	srv              *drive.Service
	db                 *Database
	localRoot          string
	remoteRoot         string
	foldersMap         map[string]string // relPath -> remote folder ID
	md5Cache           map[string]string // relPath -> cached MD5
	logCallback        func(string)
	foldersMu          sync.Mutex
	md5Mu              sync.RWMutex
	concurrency        int
	progressCallback   func(SyncProgress)
	ignoreMatcher      *IgnoreMatcher
	bandwidthLimitKBPS int
	conflictStrategy   string
}

// ThrottledReader limits the read rate of data to a maximum KB/s.
type ThrottledReader struct {
	r                 io.Reader
	maxBytesPerSecond int64
	bytesReadInWindow int64
	windowStart       time.Time
}

func NewThrottledReader(r io.Reader, maxKBPS int) io.Reader {
	if maxKBPS <= 0 {
		return r
	}
	return &ThrottledReader{
		r:                 r,
		maxBytesPerSecond: int64(maxKBPS) * 1024,
		windowStart:       time.Now(),
	}
}

func (tr *ThrottledReader) Read(p []byte) (int, error) {
	if tr.maxBytesPerSecond <= 0 {
		return tr.r.Read(p)
	}

	if int64(len(p)) > 32*1024 {
		p = p[:32*1024]
	}

	n, err := tr.r.Read(p)
	if n > 0 {
		tr.bytesReadInWindow += int64(n)
		elapsed := time.Since(tr.windowStart)
		expectedDuration := time.Duration(float64(tr.bytesReadInWindow) / float64(tr.maxBytesPerSecond) * float64(time.Second))

		if elapsed < expectedDuration {
			sleepTime := expectedDuration - elapsed
			if sleepTime > 0 {
				time.Sleep(sleepTime)
			}
		}

		if time.Since(tr.windowStart) >= time.Second {
			tr.windowStart = time.Now()
			tr.bytesReadInWindow = 0
		}
	}
	return n, err
}

func copyLocalFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func NewSyncEngineGo(srv *drive.Service, db *Database, localRoot, remoteRoot string, logCb func(string)) *SyncEngineGo {
	cfg := GetConfig()
	return &SyncEngineGo{
		srv:                srv,
		db:                 db,
		localRoot:          localRoot,
		remoteRoot:         remoteRoot,
		foldersMap:         make(map[string]string),
		md5Cache:           make(map[string]string),
		logCallback:        logCb,
		concurrency:        4,
		bandwidthLimitKBPS: cfg.BandwidthLimitKBPS,
		conflictStrategy:   cfg.ConflictStrategy,
		ignoreMatcher:      NewIgnoreMatcher(localRoot, cfg.CombinedIgnoreRules()),
	}
}

func (s *SyncEngineGo) SetBandwidthLimit(kbps int) {
	if kbps < 0 {
		kbps = 0
	}
	s.bandwidthLimitKBPS = kbps
}

func (s *SyncEngineGo) SetConflictStrategy(strategy string) {
	if strategy != "local_wins" && strategy != "remote_wins" {
		strategy = "keep_both"
	}
	s.conflictStrategy = strategy
}

func (s *SyncEngineGo) SetConcurrency(c int) {
	if c <= 0 {
		c = 4
	} else if c > 16 {
		c = 16
	}
	s.concurrency = c
}

func (s *SyncEngineGo) SetProgressCallback(cb func(SyncProgress)) {
	s.progressCallback = cb
}

func (s *SyncEngineGo) SetIgnoreMatcher(m *IgnoreMatcher) {
	s.ignoreMatcher = m
}

func (s *SyncEngineGo) log(msg string) {
	if s.logCallback != nil {
		s.logCallback(msg)
	}
}

func (s *SyncEngineGo) relativeLocalPath(localPath string) (string, error) {
	rootPath, err := filepath.Abs(s.localRoot)
	if err != nil {
		return "", err
	}
	fullPath, err := filepath.Abs(localPath)
	if err != nil {
		return "", err
	}
	relPath, err := filepath.Rel(rootPath, fullPath)
	if err != nil {
		return "", err
	}
	relPath = filepath.Clean(relPath)
	if relPath == "." || relPath == ".." || strings.HasPrefix(relPath, ".."+string(os.PathSeparator)) || filepath.IsAbs(relPath) {
		return "", fmt.Errorf("путь выходит за пределы папки синхронизации: %s", localPath)
	}
	return relPath, nil
}

func (s *SyncEngineGo) openLocalRoot() (*os.Root, error) {
	rootPath := filepath.Clean(s.localRoot)
	rootInfo, err := os.Lstat(rootPath)
	if err != nil {
		return nil, err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return nil, fmt.Errorf("корень синхронизации не может быть ссылкой или не является папкой: %s", rootPath)
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, err
	}
	openedInfo, err := root.Stat(".")
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	if !os.SameFile(rootInfo, openedInfo) {
		_ = root.Close()
		return nil, fmt.Errorf("корень синхронизации изменился при открытии: %s", rootPath)
	}
	return root, nil
}

func (s *SyncEngineGo) openLocalPath(localPath string) (*os.File, error) {
	relPath, err := s.relativeLocalPath(localPath)
	if err != nil {
		return nil, err
	}
	root, err := s.openLocalRoot()
	if err != nil {
		return nil, err
	}
	file, openErr := root.Open(relPath)
	_ = root.Close()
	return file, openErr
}

func (s *SyncEngineGo) statLocalPath(localPath string) (os.FileInfo, error) {
	relPath, err := s.relativeLocalPath(localPath)
	if err != nil {
		return nil, err
	}
	root, err := s.openLocalRoot()
	if err != nil {
		return nil, err
	}
	info, statErr := root.Stat(relPath)
	_ = root.Close()
	return info, statErr
}

func (s *SyncEngineGo) removeLocalPath(localPath string) error {
	relPath, err := s.relativeLocalPath(localPath)
	if err != nil {
		return err
	}
	root, err := s.openLocalRoot()
	if err != nil {
		return err
	}
	removeErr := root.Remove(relPath)
	_ = root.Close()
	return removeErr
}

func (s *SyncEngineGo) computeMD5LocalPath(localPath string) (string, error) {
	file, err := s.openLocalPath(localPath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	return computeMD5Reader(file)
}

// IsIgnoredRelPath checks if a relative path or filename should be skipped by synchronization
func IsIgnoredRelPath(relPath string) bool {
	clean := filepath.ToSlash(relPath)
	parts := strings.Split(clean, "/")
	for _, part := range parts {
		if strings.HasPrefix(part, ".") ||
			strings.HasSuffix(part, ".tmp") ||
			strings.HasPrefix(part, "~$") ||
			part == "__pycache__" ||
			part == "$RECYCLE.BIN" ||
			part == "System Volume Information" {
			return true
		}
	}
	return false
}

// LocalChanges tracks only what changed locally compared to database
type LocalChanges struct {
	NewOrChanged map[string]FileState
	Deleted      []string
	TotalScanned int
}

// ScanLocalDelta quickly scans the local directory, comparing size + mtime with DB on-the-fly.
// Unchanged files are NOT added to the processing queue.
func (s *SyncEngineGo) ScanLocalDelta(ctx context.Context, stored map[string]FileState) (*LocalChanges, error) {
	// Pre-flight safety check: ensure the local directory / drive exists and is accessible
	root, err := s.openLocalRoot()
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("локальная папка не найдена или диск отключен: '%s'", s.localRoot)
	}
	if err != nil {
		return nil, fmt.Errorf("ошибка доступа к локальному диску/папке '%s': %w", s.localRoot, err)
	}
	defer root.Close()

	// Verify the folder can actually be read
	rootDir, err := root.Open(".")
	if err != nil {
		return nil, fmt.Errorf("не удалось открыть содержимое папки '%s': %w", s.localRoot, err)
	}
	entries, readErr := rootDir.ReadDir(-1)
	closeErr := rootDir.Close()
	if readErr == nil {
		readErr = closeErr
	}
	if readErr != nil {
		return nil, fmt.Errorf("не удалось прочитать содержимое папки '%s' (диск отключен или заблокирован): %w", s.localRoot, readErr)
	}

	// If database already contains many files, but the root folder is completely empty, trigger safety abort!
	if len(stored) > 10 && len(entries) == 0 {
		return nil, fmt.Errorf("КРИТИЧЕСКАЯ ЗАЩИТА: Папка '%s' пуста, хотя в базе числится %d файлов! Диск может быть отключен. Синхронизация заблокирована во избежание удаления данных из облака.", s.localRoot, len(stored))
	}
	// Canary / Anchor file check: verify drive and folder identity
	anchorName := ".google_sync_anchor"
	const expectedAnchor = "google-sync-lite-anchor-verified\n"
	anchorInfo, anchorErr := root.Lstat(anchorName)
	if anchorErr == nil && anchorInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("файл привязки диска не может быть символической ссылкой: %s", filepath.Join(s.localRoot, anchorName))
	}
	if os.IsNotExist(anchorErr) {
		if len(stored) > 10 {
			return nil, fmt.Errorf("КРИТИЧЕСКАЯ ЗАЩИТА: Файл привязки диска (.google_sync_anchor) не найден в '%s'! Диск отключен или сменилась буква. Проверьте накопитель!", s.localRoot)
		}
		anchorFile, err := root.OpenFile(anchorName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			return nil, fmt.Errorf("не удалось безопасно создать файл привязки диска: %w", err)
		}
		_, writeErr := anchorFile.Write([]byte(expectedAnchor))
		closeErr := anchorFile.Close()
		if writeErr != nil {
			return nil, fmt.Errorf("не удалось записать файл привязки диска: %w", writeErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("не удалось закрыть файл привязки диска: %w", closeErr)
		}
	} else if anchorErr != nil {
		return nil, fmt.Errorf("не удалось проверить файл привязки диска: %w", anchorErr)
	} else {
		if _, err := root.Stat(anchorName); err != nil {
			return nil, fmt.Errorf("не удалось безопасно проверить файл привязки диска: %w", err)
		}
		anchorBytes, err := root.ReadFile(anchorName)
		if err != nil {
			return nil, fmt.Errorf("не удалось прочитать файл привязки диска: %w", err)
		}
		if strings.TrimSpace(string(anchorBytes)) != strings.TrimSpace(expectedAnchor) {
			return nil, fmt.Errorf("КРИТИЧЕСКАЯ ЗАЩИТА: Поврежден или подменен идентификатор папки привязки (.google_sync_anchor) в '%s'! Проверьте правильность выбранного диска.", s.localRoot)
		}
	}

	seenPaths := make(map[string]bool)
	changes := &LocalChanges{
		NewOrChanged: make(map[string]FileState),
	}

	count := 0
	lastReport := time.Now()

	err = fs.WalkDir(rootedLocalFS{root: root}, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("неполное сканирование: не удалось прочитать '%s': %w", path, err)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if path == "." {
			return nil
		}

		name := d.Name()
		cleanRel := filepath.ToSlash(path)

		if s.ignoreMatcher != nil && s.ignoreMatcher.ShouldIgnore(cleanRel, d.IsDir()) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			if strings.HasPrefix(name, ".") || name == "__pycache__" || name == "$RECYCLE.BIN" || name == "System Volume Information" || name == "node_modules" {
				return fs.SkipDir
			}
			return nil
		}

		if IsIgnoredRelPath(cleanRel) {
			return nil
		}

		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("синхронизация остановлена: обнаружена символическая ссылка или reparse point '%s'", path)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("синхронизация остановлена: неподдерживаемый тип файла '%s'", path)
		}

		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("не удалось безопасно проверить '%s': %w", path, err)
		}

		seenPaths[cleanRel] = true

		fileMTime := float64(info.ModTime().Unix())
		fileSize := info.Size()

		count++
		if count%25000 == 0 || (count%5000 == 0 && time.Since(lastReport) > 2*time.Second) {
			s.log(fmt.Sprintf("    Просканировано локально: %d файлов...", count))
			lastReport = time.Now()
		}

		// Check if file is completely unchanged in DB
		if stored != nil {
			if st, exists := stored[cleanRel]; exists {
				if st.Size == fileSize && st.MTime == fileMTime && st.MD5 != "" {
					// Perfectly identical to DB record -> SKIP!
					return nil
				}
			}
		}

		// File is either NEW or MODIFIED
		changes.NewOrChanged[cleanRel] = FileState{
			RelPath: cleanRel,
			Size:    fileSize,
			MTime:   fileMTime,
			IsDir:   false,
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	// Check for files deleted locally that exist in DB, skipping ignored/temporary patterns
	for p := range stored {
		if s.ignoreMatcher != nil && s.ignoreMatcher.ShouldIgnore(p, false) {
			continue
		}
		if IsIgnoredRelPath(p) {
			continue
		}
		if !seenPaths[p] {
			changes.Deleted = append(changes.Deleted, p)
		}
	}

	changes.TotalScanned = count
	return changes, nil
}

func (s *SyncEngineGo) GetOrComputeMD5(ctx context.Context, relPath string, loc FileState) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if loc.MD5 != "" {
		return loc.MD5, nil
	}
	s.md5Mu.RLock()
	cached, ok := s.md5Cache[relPath]
	s.md5Mu.RUnlock()
	if ok && cached != "" {
		return cached, nil
	}
	localFullPath := filepath.Join(s.localRoot, filepath.FromSlash(relPath))
	hash, err := s.computeMD5LocalPath(localFullPath)
	if err != nil {
		return "", err
	}
	s.md5Mu.Lock()
	s.md5Cache[relPath] = hash
	s.md5Mu.Unlock()
	return hash, nil
}

func (s *SyncEngineGo) localPathForRemote(relPath string) string {
	return filepath.Clean(SanitizeWindowsPath(filepath.Join(s.localRoot, filepath.FromSlash(relPath))))
}

func firstParent(parents []string) string {
	if len(parents) == 0 {
		return ""
	}
	return parents[0]
}

func IsWindowsSafeRelativePath(relPath string) bool {
	probe := filepath.Join("C:\\", filepath.FromSlash(relPath))
	return filepath.ToSlash(filepath.Clean(probe)) == filepath.ToSlash(filepath.Clean(SanitizeWindowsPath(probe)))
}

func (s *SyncEngineGo) FetchRemoteTree(ctx context.Context) (map[string]DriveFileMeta, error) {
	filesMap := make(map[string]DriveFileMeta)
	s.foldersMap = make(map[string]string)
	s.foldersMap[""] = s.remoteRoot

	rootID := s.remoteRoot
	if rootID == "root" {
		rootFile, err := s.srv.Files.Get("root").Fields("id").Context(ctx).Do()
		if err == nil && rootFile != nil {
			rootID = rootFile.Id
		}
	}

	type rawDriveItem struct {
		id           string
		name         string
		mimeType     string
		parentID     string
		md5          string
		size         int64
		modifiedTime string
		isFolder     bool
	}

	children := make(map[string][]rawDriveItem)
	pageToken := ""
	scannedCount := 0
	lastLog := time.Now()

	s.log("[*] Загрузка метаданных файлов из Google Drive...")

	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		call := s.srv.Files.List().
			Q("trashed = false").
			Spaces("drive").
			Fields("nextPageToken, files(id, name, mimeType, parents, md5Checksum, size, modifiedTime)").
			PageSize(1000)

		if pageToken != "" {
			call = call.PageToken(pageToken)
		}

		r, err := call.Context(ctx).Do()
		if err != nil {
			return nil, fmt.Errorf("ошибка чтения Google Drive: %w", err)
		}

		for _, f := range r.Files {
			pID := ""
			if len(f.Parents) > 0 {
				pID = f.Parents[0]
			}
			isFolder := (f.MimeType == "application/vnd.google-apps.folder")
			item := rawDriveItem{
				id:           f.Id,
				name:         f.Name,
				mimeType:     f.MimeType,
				parentID:     pID,
				md5:          f.Md5Checksum,
				size:         f.Size,
				modifiedTime: f.ModifiedTime,
				isFolder:     isFolder,
			}
			children[pID] = append(children[pID], item)
		}

		scannedCount += len(r.Files)
		if time.Since(lastLog) > 2*time.Second {
			s.log(fmt.Sprintf("    Индексация Google Drive: получено %d элементов...", scannedCount))
			lastLog = time.Now()
		}

		pageToken = r.NextPageToken
		if pageToken == "" {
			break
		}
	}

	s.log(fmt.Sprintf("[*] Построение дерева каталогов (всего элементов: %d)...", scannedCount))

	type queueItem struct {
		id   string
		path string
	}
	var queue []queueItem
	queue = append(queue, queueItem{id: rootID, path: ""})

	visitedFolders := make(map[string]bool)

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		if visitedFolders[curr.id] {
			continue
		}
		visitedFolders[curr.id] = true

		for _, child := range children[curr.id] {
			childRelPath := child.name
			if curr.path != "" {
				childRelPath = curr.path + "/" + child.name
			}

			if child.isFolder {
				s.foldersMap[childRelPath] = child.id
				queue = append(queue, queueItem{id: child.id, path: childRelPath})
			} else {
				if !IsWindowsSafeRelativePath(childRelPath) {
					s.log(fmt.Sprintf("[!] Пропуск файла с несовместимым для Windows именем: %s", childRelPath))
					continue
				}
				filesMap[childRelPath] = DriveFileMeta{
					ID:           child.id,
					Name:         child.name,
					Size:         child.size,
					MD5:          child.md5,
					ModifiedTime: child.modifiedTime,
					ParentID:     curr.id,
					IsDir:        false,
				}
			}
		}
	}

	s.log(fmt.Sprintf("[✓] Дерево Google Drive сформировано: %d папок, %d файлов.", len(s.foldersMap), len(filesMap)))
	return filesMap, nil
}

// FetchRemoteDelta fetches ONLY files modified in Google Drive after lastSyncRFC3339.
// If lastSyncRFC3339 is empty, falls back to full FetchRemoteTree.
func (s *SyncEngineGo) FetchRemoteDelta(ctx context.Context, lastSyncRFC3339 string, stored map[string]FileState) (map[string]DriveFileMeta, bool, error) {
	if lastSyncRFC3339 == "" {
		tree, err := s.FetchRemoteTree(ctx)
		return tree, false, err
	}

	// Query only files changed since last sync
	query := fmt.Sprintf("modifiedTime > '%s'", lastSyncRFC3339)
	s.log(fmt.Sprintf("[*] Запрос изменений в Google Drive (с %s)...", lastSyncRFC3339))

	filesMap := make(map[string]DriveFileMeta)
	pageToken := ""

	// Build helper maps once — outside the pagination loop (O(n), not O(n × pages))
	storedByID := make(map[string]string, len(stored))
	for relPath, state := range stored {
		if state.FileID != "" {
			storedByID[state.FileID] = relPath
		}
	}

	// Invert foldersMap (folderID -> relPath) to resolve parent directory paths
	idToRelFolder := make(map[string]string, len(s.foldersMap))
	for fRel, fID := range s.foldersMap {
		idToRelFolder[fID] = fRel
	}

	for {
		if ctx.Err() != nil {
			return nil, true, ctx.Err()
		}

		call := s.srv.Files.List().
			Q(query).
			Spaces("drive").
			Fields("nextPageToken, files(id, name, mimeType, md5Checksum, modifiedTime, size, parents, trashed)").
			PageSize(100)

		if pageToken != "" {
			call = call.PageToken(pageToken)
		}

		r, err := call.Context(ctx).Do()
		if err != nil {
			s.log(fmt.Sprintf("[!] Ошибка дельта-запроса (%v), переключение на полное дерево...", err))
			tree, err := s.FetchRemoteTree(ctx)
			return tree, false, err
		}

		for _, f := range r.Files {
			if f.MimeType != "application/vnd.google-apps.folder" {
				if f.Trashed {
					knownPath, ok := storedByID[f.Id]
					if !ok {
						continue
					}
					filesMap[knownPath] = DriveFileMeta{
						ID: f.Id, Name: f.Name, Size: f.Size, MD5: f.Md5Checksum,
						ModifiedTime: f.ModifiedTime, ParentID: firstParent(f.Parents), Trashed: f.Trashed,
					}
					continue
				}
				pID := ""
				if len(f.Parents) > 0 {
					pID = f.Parents[0]
				}

				// Resolve relative path for this file
				relPath := f.Name
				isRemoteRoot := pID == s.remoteRoot || (s.remoteRoot == "root" && pID == "root")
				if pID != "" && !isRemoteRoot {
					if parentRel, ok := idToRelFolder[pID]; ok && parentRel != "" {
						relPath = parentRel + "/" + f.Name
					} else {
						// Folder hierarchy for this file is not known; fallback to full FetchRemoteTree to ensure correctness
						s.log(fmt.Sprintf("[*] Обнаружен файл в новом подкаталоге Google Drive (%s), обновление полного дерева...", f.Name))
						tree, err := s.FetchRemoteTree(ctx)
						return tree, false, err
					}
				}

				if !IsWindowsSafeRelativePath(relPath) {
					s.log(fmt.Sprintf("[!] Пропуск удалённого файла с несовместимым для Windows именем: %s", relPath))
					continue
				}
				filesMap[relPath] = DriveFileMeta{
					ID:           f.Id,
					Name:         f.Name,
					Size:         f.Size,
					MD5:          f.Md5Checksum,
					ModifiedTime: f.ModifiedTime,
					ParentID:     pID,
					IsDir:        false,
					Trashed:      f.Trashed,
				}
			}
		}

		pageToken = r.NextPageToken
		if pageToken == "" {
			break
		}
	}

	return filesMap, true, nil
}

func (s *SyncEngineGo) EnsureRemoteFolder(ctx context.Context, relDir string) (string, error) {
	relDir = filepath.ToSlash(relDir)
	relDir = strings.Trim(strings.ReplaceAll(relDir, "\\", "/"), "/")
	if relDir == "" || relDir == "." {
		return s.remoteRoot, nil
	}

	parts := strings.Split(relDir, "/")
	accum := ""
	parentID := s.remoteRoot

	s.foldersMu.Lock()
	defer s.foldersMu.Unlock()

	for _, part := range parts {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		part = strings.TrimSpace(part)
		if part == "" || part == "." {
			continue
		}
		if accum == "" {
			accum = part
		} else {
			accum = accum + "/" + part
		}

		if id, exists := s.foldersMap[accum]; exists {
			parentID = id
		} else {
			// Check if folder already exists in Google Drive under parentID
			escapedPart := strings.ReplaceAll(part, "\\", "\\\\")
			escapedPart = strings.ReplaceAll(escapedPart, "'", "\\'")
			q := fmt.Sprintf("'%s' in parents and name = '%s' and mimeType = 'application/vnd.google-apps.folder' and trashed = false", parentID, escapedPart)
			listCall := s.srv.Files.List().Q(q).Spaces("drive").Fields("files(id)").PageSize(1)
			r, err := listCall.Context(ctx).Do()
			if err == nil && len(r.Files) > 0 {
				s.foldersMap[accum] = r.Files[0].Id
				parentID = r.Files[0].Id
			} else {
				s.log(fmt.Sprintf("[*] Создание удаленной папки: %s", accum))
				fMeta := &drive.File{
					Name:     part,
					MimeType: "application/vnd.google-apps.folder",
					Parents:  []string{parentID},
				}
				folder, err := s.srv.Files.Create(fMeta).Fields("id").Context(ctx).Do()
				if err != nil {
					return "", fmt.Errorf("ошибка создания папки %s: %w", accum, err)
				}
				s.foldersMap[accum] = folder.Id
				parentID = folder.Id
			}
		}
	}
	return parentID, nil
}

// SanitizeWindowsPath cleans illegal Windows filename characters, reserved device names,
// control characters, and trailing dots/spaces while preserving drive root (e.g. E:\)
func SanitizeWindowsPath(fullPath string) string {
	volume := filepath.VolumeName(fullPath)
	rest := fullPath[len(volume):]

	// Normalize slashes for processing
	isSlash := strings.Contains(rest, "/")
	var sep string
	if isSlash {
		sep = "/"
	} else {
		sep = string(filepath.Separator)
	}

	parts := strings.Split(rest, sep)
	reservedNames := map[string]bool{
		"CON": true, "PRN": true, "AUX": true, "NUL": true,
		"COM1": true, "COM2": true, "COM3": true, "COM4": true,
		"COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
		"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true,
		"LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
	}

	for i, part := range parts {
		if part == "" {
			continue
		}
		if part == "." || part == ".." {
			parts[i] = "_"
			continue
		}

		// Replace illegal characters: < > : " | ? * and control chars (ASCII < 32)
		var sb strings.Builder
		for _, r := range part {
			if r < 32 || r == '<' || r == '>' || r == ':' || r == '"' || r == '|' || r == '?' || r == '*' || r == '\\' {
				sb.WriteRune('_')
			} else {
				sb.WriteRune(r)
			}
		}
		cleaned := sb.String()

		// Windows cannot end filenames or directory names with a space or dot
		trimmed := strings.TrimRight(cleaned, " .")
		if trimmed == "" {
			trimmed = "_"
		}

		// Check DOS reserved names (e.g. AUX, CON, NUL, COM1, or AUX.txt)
		baseUpper := strings.ToUpper(trimmed)
		extIdx := strings.Index(baseUpper, ".")
		stem := baseUpper
		if extIdx != -1 {
			stem = baseUpper[:extIdx]
		}
		if reservedNames[stem] {
			trimmed = "_" + trimmed
		}

		parts[i] = trimmed
	}

	return volume + strings.Join(parts, sep)
}

func (s *SyncEngineGo) DownloadFile(ctx context.Context, fileID, localDestPath, expectedMD5 string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	cleanDestPath := filepath.Clean(SanitizeWindowsPath(localDestPath))
	relDest, err := s.relativeLocalPath(cleanDestPath)
	if err != nil {
		return fmt.Errorf("попытка выхода за пределы папки синхронизации (Path Traversal заблокирован): %w", err)
	}
	root, err := s.openLocalRoot()
	if err != nil {
		return err
	}
	defer root.Close()
	tmpDest := relDest + ".tmp"
	if parent := filepath.Dir(relDest); parent != "." {
		if err := root.MkdirAll(parent, 0755); err != nil {
			return fmt.Errorf("не удалось безопасно создать локальную папку для %s: %w", cleanDestPath, err)
		}
	}

	resp, err := s.srv.Files.Get(fileID).Context(ctx).Download()
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if err := root.Remove(tmpDest); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("не удалось удалить временный файл предыдущей загрузки %s: %w", cleanDestPath, err)
	}
	out, err := root.OpenFile(tmpDest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}

	_, copyErr := io.Copy(out, resp.Body)
	if copyErr != nil {
		out.Close()
		_ = root.Remove(tmpDest)
		return copyErr
	}

	// Flush OS write buffers to physical disk to prevent zero-byte corrupt files on power loss
	if syncErr := out.Sync(); syncErr != nil {
		out.Close()
		_ = root.Remove(tmpDest)
		return syncErr
	}
	if closeErr := out.Close(); closeErr != nil {
		_ = root.Remove(tmpDest)
		return closeErr
	}

	// Verify MD5 before replacing target file
	if expectedMD5 != "" {
		tmpFile, err := root.Open(tmpDest)
		if err != nil {
			_ = root.Remove(tmpDest)
			return fmt.Errorf("не удалось проверить целостность скачанного файла %s: %w", cleanDestPath, err)
		}
		chkMD5, hashErr := computeMD5Reader(tmpFile)
		closeErr := tmpFile.Close()
		if hashErr != nil {
			_ = root.Remove(tmpDest)
			return fmt.Errorf("не удалось проверить целостность скачанного файла %s: %w", cleanDestPath, hashErr)
		}
		if closeErr != nil {
			_ = root.Remove(tmpDest)
			return fmt.Errorf("не удалось закрыть временный файл %s: %w", cleanDestPath, closeErr)
		}
		if chkMD5 != expectedMD5 {
			_ = root.Remove(tmpDest)
			return fmt.Errorf("ошибка целостности MD5 для %s (ожидался: %s, получен: %s)", cleanDestPath, expectedMD5, chkMD5)
		}
	}

	// Atomically replace target file
	if renameErr := root.Rename(tmpDest, relDest); renameErr != nil {
		_ = root.Remove(tmpDest) // Clean orphaned .tmp on rename error
		return renameErr
	}
	return nil
}

func isAuthOrTokenError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	if strings.Contains(errStr, "invalid_grant") ||
		strings.Contains(errStr, "token has been expired or revoked") ||
		strings.Contains(errStr, "oauth2: cannot fetch token") ||
		strings.Contains(errStr, "statuscode: 401") ||
		strings.Contains(errStr, "statuscode: 400") && strings.Contains(errStr, "invalid_grant") {
		return true
	}
	if gErr, ok := err.(*googleapi.Error); ok {
		if gErr.Code == 401 {
			return true
		}
	}
	return false
}

func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	if isAuthOrTokenError(err) {
		return false
	}
	if gErr, ok := err.(*googleapi.Error); ok {
		if gErr.Code == 429 || gErr.Code >= 500 {
			return true
		}
	}
	return false
}

func (s *SyncEngineGo) UploadFile(ctx context.Context, localPath, parentID, remoteName string) (*drive.File, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	var res *drive.File
	var lastErr error

	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<attempt) * 500 * time.Millisecond
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		f, err := s.openLocalPath(localPath)
		if err != nil {
			return nil, err
		}

		fi, _ := f.Stat()
		var mediaOpts []googleapi.MediaOption
		// For files >= 8 MB, use 8 MB chunk size for reliable resumable uploads
		if fi != nil && fi.Size() >= 8*1024*1024 {
			mediaOpts = append(mediaOpts, googleapi.ChunkSize(8*1024*1024))
		}

		var reader io.Reader = f
		if s.bandwidthLimitKBPS > 0 {
			reader = NewThrottledReader(f, s.bandwidthLimitKBPS)
		}

		driveFile := &drive.File{
			Name:    remoteName,
			Parents: []string{parentID},
		}

		res, lastErr = s.srv.Files.Create(driveFile).Media(reader, mediaOpts...).Fields("id, name, md5Checksum, modifiedTime, size").Context(ctx).Do()
		f.Close()

		if lastErr == nil {
			return res, nil
		}
		if !isRetryableError(lastErr) {
			break
		}
	}
	return nil, lastErr
}

func (s *SyncEngineGo) UpdateFile(ctx context.Context, fileID, localPath string) (*drive.File, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	var res *drive.File
	var lastErr error

	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<attempt) * 500 * time.Millisecond
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		f, err := s.openLocalPath(localPath)
		if err != nil {
			return nil, err
		}

		fi, _ := f.Stat()
		var mediaOpts []googleapi.MediaOption
		// For files >= 8 MB, use 8 MB chunk size for reliable resumable updates
		if fi != nil && fi.Size() >= 8*1024*1024 {
			mediaOpts = append(mediaOpts, googleapi.ChunkSize(8*1024*1024))
		}

		var reader io.Reader = f
		if s.bandwidthLimitKBPS > 0 {
			reader = NewThrottledReader(f, s.bandwidthLimitKBPS)
		}

		res, lastErr = s.srv.Files.Update(fileID, &drive.File{}).Media(reader, mediaOpts...).Fields("id, name, md5Checksum, modifiedTime, size").Context(ctx).Do()
		f.Close()

		if lastErr == nil {
			return res, nil
		}
		if !isRetryableError(lastErr) {
			break
		}
	}
	return nil, lastErr
}

func (s *SyncEngineGo) Sync(ctx context.Context, dryRun bool, syncMode string) (*SyncSummary, error) {
	if syncMode == "" {
		syncMode = "local_master"
	}

	stored, err := s.db.GetAllFiles()
	if err != nil {
		return nil, err
	}

	lastSyncTimeRFC := s.db.GetMeta("last_sync_rfc3339")
	startTime := time.Now().UTC().Format(time.RFC3339)
	cfg := GetConfig()

	s.log(fmt.Sprintf("[*] Быстрое дельта-сканирование (Режим: %s)...", func() string {
		if syncMode == "local_master" {
			return "Локальный диск как основа (Master / Mirror)"
		}
		return "Двусторонняя синхронизация (Two-way)"
	}()))

	if cfg.SafetyShield {
		s.log(fmt.Sprintf("    [🛡 Щит Безопасности: АКТИВЕН | Удаление в облаке: %s]", func() string {
			if cfg.AllowRemoteDeletion {
				return fmt.Sprintf("РАЗРЕШЕНО (порог: %d файлов)", cfg.MaxDeleteThreshold)
			}
			return "ЗАПРЕЩЕНО (файлы в облаке 100% защищены)"
		}()))
	}

	// Step 1: Scan local directory for DELTA only
	localChanges, err := s.ScanLocalDelta(ctx, stored)
	if err != nil {
		return nil, err
	}

	s.log(fmt.Sprintf("    Локально просканировано: %d файлов. Изменений/новых: %d, удаленных: %d.",
		localChanges.TotalScanned, len(localChanges.NewOrChanged), len(localChanges.Deleted)))

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	// Step 2: Query remote changes
	var remoteFiles map[string]DriveFileMeta
	var isDelta bool

	if syncMode == "local_master" {
		// In local_master mode, local disk is the master source of truth.
		// If no local files changed and we have stored state:
		if len(stored) > 0 && len(localChanges.NewOrChanged) == 0 && len(localChanges.Deleted) == 0 {
			s.log("[✓] Все файлы на диске идентичны предыдущему состоянию. Изменений нет.")
			s.db.SetMeta("last_sync_rfc3339", startTime)
			return &SyncSummary{VerifiedCount: localChanges.TotalScanned}, nil
		}
		// If DB is empty (first run), fetch remote tree to match existing cloud files and avoid duplicate re-upload!
		if len(stored) == 0 {
			s.log("[*] Первичный запуск: сканирование существующей структуры Google Drive...")
			remoteFiles, err = s.FetchRemoteTree(ctx)
			if err != nil {
				return nil, err
			}
			isDelta = false
		} else {
			remoteFiles = make(map[string]DriveFileMeta)
			isDelta = true
		}
	} else {
		// two_way mode
		if len(stored) > 0 && lastSyncTimeRFC != "" {
			remoteFiles, isDelta, err = s.FetchRemoteDelta(ctx, lastSyncTimeRFC, stored)
			if err != nil {
				return nil, err
			}
		} else {
			s.log("[*] Построение дерева Google Drive...")
			remoteFiles, err = s.FetchRemoteTree(ctx)
			if err != nil {
				return nil, err
			}
			s.log(fmt.Sprintf("    Всего файлов в облаке: %d", len(remoteFiles)))
		}
	}

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	summary := &SyncSummary{}
	operationFailed := false
	if syncMode == "two_way" {
		strategy := s.conflictStrategy
		if strategy == "" {
			strategy = "keep_both"
		}
		for path, rem := range remoteFiles {
			loc, locallyChanged := localChanges.NewOrChanged[path]
			if !locallyChanged {
				continue
			}
			summary.ConflictCount++
			if rem.Trashed {
				if strategy == "remote_wins" {
					delete(localChanges.NewOrChanged, path)
					_ = os.Remove(s.localPathForRemote(path))
					_ = s.db.RemoveFile(path)
					summary.DeletedCount++
					s.log(fmt.Sprintf("[⚡] Конфликт '%s': файл удалён в облаке, удаляем локально (remote_wins)", path))
				} else {
					delete(remoteFiles, path)
					s.log(fmt.Sprintf("[⚡] Конфликт '%s': файл изменён локально, но удалён в Google Drive. Локальная версия сохранена.", path))
					_ = s.db.AddRecentFile(path, "conflict_local", loc.Size)
				}
				continue
			}

			// File changed both locally and in Google Drive
			switch strategy {
			case "local_wins":
				delete(remoteFiles, path)
				s.log(fmt.Sprintf("[⚡] Конфликт '%s': выбрана локальная версия (local_wins), изменения отправляются в Google Drive", path))
				_ = s.db.AddRecentFile(path, "conflict_local", loc.Size)

			case "remote_wins":
				locPath := s.localPathForRemote(path)
				_ = copyLocalFile(locPath, locPath+".conflict.bak")
				delete(localChanges.NewOrChanged, path)
				s.log(fmt.Sprintf("[⚡] Конфликт '%s': выбрана облачная версия (remote_wins), локальная копия сохранена в .conflict.bak", path))
				_ = s.db.AddRecentFile(path, "conflict_remote", loc.Size)

			default: // "keep_both"
				ext := pathPkg.Ext(path)
				base := strings.TrimSuffix(path, ext)
				timeStr := time.Now().Format("2006-01-02_150405")
				conflictRelPath := fmt.Sprintf("%s (Конфликт %s)%s", base, timeStr, ext)

				locPath := s.localPathForRemote(path)
				conflictLocPath := s.localPathForRemote(conflictRelPath)

				if err := copyLocalFile(locPath, conflictLocPath); err == nil {
					s.log(fmt.Sprintf("[⚡] Конфликт '%s': создана резервная копия '%s'", path, conflictRelPath))
					delete(localChanges.NewOrChanged, path)
					localChanges.NewOrChanged[conflictRelPath] = FileState{
						RelPath: conflictRelPath,
						Size:    loc.Size,
						MTime:   float64(time.Now().Unix()),
					}
					_ = s.db.AddRecentFile(conflictRelPath, "conflict", loc.Size)
				} else {
					s.log(fmt.Sprintf("[!] Ошибка создания копии конфликта для %s: %v. Применяется local_wins.", path, err))
					delete(remoteFiles, path)
				}
			}
		}
	}

	// If no local changes and no remote changes -> instant completion!
	if len(localChanges.NewOrChanged) == 0 && len(localChanges.Deleted) == 0 && len(remoteFiles) == 0 {
		s.log("[✓] Изменений не обнаружено. Все файлы синхронизированы.")
		if operationFailed {
			return summary, fmt.Errorf("синхронизация завершена с ошибками; контрольная точка не обновлена, проблемные операции будут повторены")
		}
		if err := s.db.SetMeta("last_sync_rfc3339", startTime); err != nil {
			return summary, err
		}
		return summary, nil
	}

	// If full tree was fetched (first run), handle initial comparison
	if !isDelta && len(stored) == 0 {
		s.log("[*] Первичное сопоставление локальных файлов с Google Drive...")
		allPaths := make(map[string]bool)
		for p := range localChanges.NewOrChanged {
			allPaths[p] = true
		}
		for p := range remoteFiles {
			allPaths[p] = true
		}

		// Pre-flight check: in local_master with deletion enabled, calculate total pending deletions
		if syncMode == "local_master" && cfg.AllowRemoteDeletion {
			missingRemoteFilesCount := 0
			for p := range remoteFiles {
				if _, hasLoc := localChanges.NewOrChanged[p]; !hasLoc {
					missingRemoteFilesCount++
				}
			}

			// Blast Radius Threshold: never mass-delete cloud files on first run without safety threshold check!
			if missingRemoteFilesCount > cfg.MaxDeleteThreshold {
				return summary, fmt.Errorf("[🛡 Щит Безопасности] Первичный запуск: обнаружено %d файлов в Google Drive, отсутствующих локально (порог безопасности: %d). Удаление отменено для защиты ваших данных! Отключите удаление или увеличьте порог в Настройках, если это намеренное действие.", missingRemoteFilesCount, cfg.MaxDeleteThreshold)
			}
		}

		// Pre-flight check: in two_way mode, limit local deletions on first run
		if syncMode == "two_way" {
			missingLocalCount := 0
			for p := range remoteFiles {
				if _, hasLoc := localChanges.NewOrChanged[p]; !hasLoc {
					if rf := remoteFiles[p]; rf.Trashed {
						missingLocalCount++
					}
				}
			}
			if missingLocalCount > cfg.MaxDeleteThreshold {
				return summary, fmt.Errorf("[🛡 Щит Безопасности] Первичный запуск (two_way): обнаружено %d файлов для удаления локально (порог безопасности: %d). Удаление отменено! Увеличьте порог в Настройках, если это намеренное действие.", missingLocalCount, cfg.MaxDeleteThreshold)
			}
		}

		for path := range allPaths {
			if ctx.Err() != nil {
				return summary, ctx.Err()
			}
			loc, hasLoc := localChanges.NewOrChanged[path]
			rem, hasRem := remoteFiles[path]
			localFullPath := s.localPathForRemote(path)

			if hasLoc && hasRem {
				locMD5, _ := s.GetOrComputeMD5(ctx, path, loc)
				if locMD5 != "" && rem.MD5 != "" && locMD5 == rem.MD5 {
					_ = s.db.UpsertFile(FileState{
						RelPath: path,
						FileID:  rem.ID,
						MD5:     locMD5,
						MTime:   loc.MTime,
						Size:    loc.Size,
					})
					summary.VerifiedCount++
				} else {
					// Upload or update to cloud
					s.log(fmt.Sprintf("[↑] Загрузка на Google Drive: %s", path))
					uploadOk := false
					if !dryRun {
						res, err := s.UpdateFile(ctx, rem.ID, localFullPath)
						if err != nil {
							operationFailed = true
							s.log(fmt.Sprintf("[!] Ошибка обновления файла %s в Google Drive: %v", path, err))
						} else {
							locMD5, _ := s.GetOrComputeMD5(ctx, path, loc)
							if res.Md5Checksum != "" && locMD5 != "" && res.Md5Checksum != locMD5 {
								s.log(fmt.Sprintf("[!] Ошибка целостности: MD5 загруженного файла %s не совпал с локальным!", path))
								continue
							}
							_ = s.db.UpsertFile(FileState{
								RelPath: path,
								FileID:  rem.ID,
								MD5:     res.Md5Checksum,
								MTime:   loc.MTime,
								Size:    loc.Size,
							})
							uploadOk = true
						}
					} else {
						uploadOk = true
					}
					if uploadOk {
						summary.UploadedCount++
					}
				}
			} else if hasLoc && !hasRem {
				s.log(fmt.Sprintf("[↑] Загрузка нового файла: %s", path))
				uploadOk := false
				if !dryRun {
					parentID, err := s.EnsureRemoteFolder(ctx, pathPkg.Dir(path))
					if err != nil {
						operationFailed = true
						s.log(fmt.Sprintf("[!] Ошибка создания папки для %s: %v", path, err))
					} else {
						fileName := pathPkg.Base(path)
						res, err := s.UploadFile(ctx, localFullPath, parentID, fileName)
						if err != nil {
							operationFailed = true
							s.log(fmt.Sprintf("[!] Ошибка загрузки файла %s в Google Drive: %v", path, err))
						} else {
							locMD5, _ := s.GetOrComputeMD5(ctx, path, loc)
							if res.Md5Checksum != "" && locMD5 != "" && res.Md5Checksum != locMD5 {
								s.log(fmt.Sprintf("[!] Ошибка целостности: MD5 нового файла %s не совпал с локальным! Удаляем поврежденную копию...", path))
								_, _ = s.srv.Files.Update(res.Id, &drive.File{Trashed: true}).Context(ctx).Do()
								continue
							}
							_ = s.db.UpsertFile(FileState{
								RelPath: path,
								FileID:  res.Id,
								MD5:     locMD5,
								MTime:   loc.MTime,
								Size:    loc.Size,
							})
							uploadOk = true
						}
					}
				} else {
					uploadOk = true
				}
				if uploadOk {
					summary.UploadedCount++
					_ = s.db.AddRecentFile(path, "upload", loc.Size)
				}
			} else if hasRem && !hasLoc {
				if syncMode == "two_way" {
					if rem.Trashed {
						localPath := s.localPathForRemote(path)
						if !dryRun {
							if err := s.removeLocalPath(localPath); err != nil && !os.IsNotExist(err) {
								return summary, fmt.Errorf("не удалось применить удаление из Google Drive для %s: %w", path, err)
							}
						}
						if err := s.db.RemoveFile(path); err != nil {
							return summary, err
						}
						_ = s.db.AddRecentFile(path, "delete", 0)
						s.log(fmt.Sprintf("[-] Удалено локально вслед за Google Drive: %s", path))
						summary.DeletedCount++
						continue
					}
					s.log(fmt.Sprintf("[↓] Скачивание из облака: %s", path))
					dlOk := false
					if !dryRun {
						if err := s.DownloadFile(ctx, rem.ID, localFullPath, rem.MD5); err != nil {
							operationFailed = true
							s.log(fmt.Sprintf("[!] Ошибка скачивания %s: %v", path, err))
						} else {
							fi, err := s.statLocalPath(localFullPath)
							if err != nil {
								operationFailed = true
								s.log(fmt.Sprintf("[!] Не удалось проверить скачанный файл %s: %v", path, err))
								continue
							}
							_ = s.db.UpsertFile(FileState{
								RelPath: path,
								FileID:  rem.ID,
								MD5:     rem.MD5,
								MTime:   float64(fi.ModTime().Unix()),
								Size:    fi.Size(),
							})
							dlOk = true
						}
					} else {
						dlOk = true
					}
					if dlOk {
						summary.DownloadCount++
						_ = s.db.AddRecentFile(path, "download", rem.Size)
					}
				} else if syncMode == "local_master" {
					if !cfg.AllowRemoteDeletion {
						s.log(fmt.Sprintf("[🛡 Щит Безопасности] Файл отсутствует локально, но защищен от удаления: %s", path))
						continue
					}
					// Safety Tripwire: Never delete from Google Drive if localRoot is unreadable or missing
					if fi, err := os.Stat(s.localRoot); err != nil || !fi.IsDir() {
						return summary, fmt.Errorf("удаление заблокировано: локальный диск недоступен (%s)", s.localRoot)
					}
					s.log(fmt.Sprintf("[-] Удаление из Google Drive (отсутствует локально): %s", path))
					if !dryRun {
						_, _ = s.srv.Files.Update(rem.ID, &drive.File{Trashed: true}).Context(ctx).Do()
					}
					_ = s.db.RemoveFile(path)
					_ = s.db.AddRecentFile(path, "delete", 0)
					summary.DeletedCount++
				}
			}
		}

		// In local_master mode, clean up remote folders only if AllowRemoteDeletion is enabled
		if syncMode == "local_master" && cfg.AllowRemoteDeletion {
			// Safety Tripwire: Never delete folders if localRoot is unreadable or missing
			if fi, err := os.Stat(s.localRoot); err != nil || !fi.IsDir() {
				return summary, fmt.Errorf("удаление папок заблокировано: локальный диск недоступен (%s)", s.localRoot)
			}
			var folderPaths []string
			for fPath := range s.foldersMap {
				if fPath != "" {
					folderPaths = append(folderPaths, fPath)
				}
			}
			sort.Slice(folderPaths, func(i, j int) bool {
				return len(folderPaths[i]) > len(folderPaths[j])
			})

			for _, fPath := range folderPaths {
				if ctx.Err() != nil {
					return summary, ctx.Err()
				}
				localFolder := filepath.Join(s.localRoot, filepath.FromSlash(fPath))
				if _, err := s.statLocalPath(localFolder); os.IsNotExist(err) {
					folderID := s.foldersMap[fPath]
					if folderID != "" && folderID != s.remoteRoot {
						s.log(fmt.Sprintf("[-] Удаление лишней папки из Google Drive: %s", fPath))
						if !dryRun {
							_, _ = s.srv.Files.Update(folderID, &drive.File{Trashed: true}).Context(ctx).Do()
						}
					}
					delete(s.foldersMap, fPath)
				}
			}
		}

		s.db.SetMeta("last_sync_rfc3339", startTime)
		return summary, nil
	}

	// ==========================================
	// FAST DELTA PROCESSING (Повторные синхронизации)
	// ==========================================

	// 1. Process locally deleted files
	if len(localChanges.Deleted) > 0 {
		if !cfg.AllowRemoteDeletion {
			s.log(fmt.Sprintf("[🛡 Щит Безопасности] Локально не найдено %d файлов. Удаление в Google Диске ЗАПРЕЩЕНО — все файлы в облаке сохранены.", len(localChanges.Deleted)))
			for _, delPath := range localChanges.Deleted {
				s.log(fmt.Sprintf("    [сохранен в облаке] %s", delPath))
			}
		} else {
			// Safety Tripwire: Never delete if localRoot is unreadable or missing
			if fi, err := os.Stat(s.localRoot); err != nil || !fi.IsDir() {
				return summary, fmt.Errorf("удаление заблокировано: локальный диск недоступен (%s)", s.localRoot)
			}
			// Blast Radius Threshold: Never delete more than MaxDeleteThreshold files without explicit user interaction
			if len(localChanges.Deleted) > cfg.MaxDeleteThreshold {
				return summary, fmt.Errorf("[🛡 Щит Безопасности] Превышен порог безопасного удаления (%d файлов > лимит %d). Операция прервана для защиты ваших данных! Измените лимит в Настройках, если удаление намеренное.", len(localChanges.Deleted), cfg.MaxDeleteThreshold)
			}
			for _, delPath := range localChanges.Deleted {
				if ctx.Err() != nil {
					return summary, ctx.Err()
				}
				st := stored[delPath]
				s.log(fmt.Sprintf("[-] Удален локально: %s", delPath))
				if !dryRun && st.FileID != "" {
					_, _ = s.srv.Files.Update(st.FileID, &drive.File{Trashed: true}).Context(ctx).Do()
				}
				_ = s.db.RemoveFile(delPath)
				_ = s.db.AddRecentFile(delPath, "delete", 0)
				summary.DeletedCount++
			}
		}
	}

	// 2. Process locally new or changed files with Concurrent Worker Pool
	var totalUploadBytes int64
	for _, loc := range localChanges.NewOrChanged {
		totalUploadBytes += loc.Size
	}

	if len(localChanges.NewOrChanged) > 0 {
		workers := s.concurrency
		if workers <= 0 {
			workers = 4
		}
		if workers > len(localChanges.NewOrChanged) {
			workers = len(localChanges.NewOrChanged)
		}

		type uploadTask struct {
			path string
			loc  FileState
		}

		taskChan := make(chan uploadTask, len(localChanges.NewOrChanged))
		for p, l := range localChanges.NewOrChanged {
			taskChan <- uploadTask{path: p, loc: l}
		}
		close(taskChan)

		var (
			uploadWg                sync.WaitGroup
			processedCount          int64
			processedBytes          int64
			uploadedOkCount         int64
			tokenAuthErr            error
			tokenErrOnce            sync.Once
			workerCtx, cancelWorker = context.WithCancel(ctx)
			syncStartTime           = time.Now()
			progressMu              sync.Mutex
			lastProgressReport      time.Time
			anyOpFailed             int32
		)
		defer cancelWorker()

		reportProgress := func(currentFile string, force bool) {
			if s.progressCallback == nil {
				return
			}
			progressMu.Lock()
			defer progressMu.Unlock()

			now := time.Now()
			if !force && now.Sub(lastProgressReport) < 200*time.Millisecond {
				return
			}
			lastProgressReport = now

			pCount := int(atomic.LoadInt64(&processedCount))
			pBytes := atomic.LoadInt64(&processedBytes)
			elapsed := now.Sub(syncStartTime).Seconds()

			speedBps := 0.0
			if elapsed > 0.5 && pBytes > 0 {
				speedBps = float64(pBytes) / elapsed
			}

			remBytes := totalUploadBytes - pBytes
			etaSec := 0
			if speedBps > 0 && remBytes > 0 {
				etaSec = int(float64(remBytes) / speedBps)
			}

			percent := 0.0
			if totalUploadBytes > 0 {
				percent = float64(pBytes) / float64(totalUploadBytes) * 100.0
			} else if len(localChanges.NewOrChanged) > 0 {
				percent = float64(pCount) / float64(len(localChanges.NewOrChanged)) * 100.0
			}
			if percent > 100.0 {
				percent = 100.0
			}

			s.progressCallback(SyncProgress{
				Phase:          "transferring",
				TotalFiles:     len(localChanges.NewOrChanged),
				ProcessedFiles: pCount,
				CurrentFile:    currentFile,
				TotalBytes:     totalUploadBytes,
				ProcessedBytes: pBytes,
				SpeedBps:       speedBps,
				SpeedFormatted: FormatSpeed(speedBps),
				ETASeconds:     etaSec,
				ETAFormatted:   FormatETA(etaSec),
				Percent:        percent,
				ActiveWorkers:  workers,
			})
		}

		reportProgress("", true)

		for w := 0; w < workers; w++ {
			uploadWg.Add(1)
			go func() {
				defer uploadWg.Done()
				for task := range taskChan {
					if workerCtx.Err() != nil {
						return
					}

					path := task.path
					loc := task.loc
					localFullPath := s.localPathForRemote(path)
					st, inDB := stored[path]
					uploadOk := false

					reportProgress(pathPkg.Base(path), false)

					if inDB && st.FileID != "" {
						s.log(fmt.Sprintf("[↑] Загрузка обновлений: %s", path))
						if !dryRun {
							res, err := s.UpdateFile(workerCtx, st.FileID, localFullPath)
							if err != nil {
								if isAuthOrTokenError(err) {
									tokenErrOnce.Do(func() {
										tokenAuthErr = err
										cancelWorker()
									})
									return
								}
								// Self-healing: if file in cloud was deleted/missing (404), clear stale FileID and upload fresh
								if gErr, ok := err.(*googleapi.Error); ok && gErr.Code == 404 {
									s.log(fmt.Sprintf("[*] Файл %s отсутствует в облаке (404 Not Found). Сброс ID и повторная загрузка...", path))
									_ = s.db.RemoveFile(path)
									parentID, pErr := s.EnsureRemoteFolder(workerCtx, pathPkg.Dir(path))
									if pErr == nil {
										fileName := pathPkg.Base(path)
										newRes, upErr := s.UploadFile(workerCtx, localFullPath, parentID, fileName)
										if upErr == nil {
											locMD5, _ := s.GetOrComputeMD5(workerCtx, path, loc)
											_ = s.db.UpsertFile(FileState{
												RelPath: path,
												FileID:  newRes.Id,
												MD5:     locMD5,
												MTime:   loc.MTime,
												Size:    loc.Size,
											})
											uploadOk = true
										} else {
											atomic.StoreInt32(&anyOpFailed, 1)
											s.log(fmt.Sprintf("[!] Ошибка повторной загрузки %s: %v", path, upErr))
										}
									} else {
										atomic.StoreInt32(&anyOpFailed, 1)
									}
								} else {
									atomic.StoreInt32(&anyOpFailed, 1)
									s.log(fmt.Sprintf("[!] Ошибка обновления файла %s в Google Drive: %v", path, err))
								}
							} else {
								locMD5, _ := s.GetOrComputeMD5(workerCtx, path, loc)
								if res.Md5Checksum != "" && locMD5 != "" && res.Md5Checksum != locMD5 {
									s.log(fmt.Sprintf("[!] Ошибка целостности: MD5 файла %s не совпал с облачным!", path))
								} else {
									_ = s.db.UpsertFile(FileState{
										RelPath: path,
										FileID:  st.FileID,
										MD5:     res.Md5Checksum,
										MTime:   loc.MTime,
										Size:    loc.Size,
									})
									uploadOk = true
								}
							}
						} else {
							uploadOk = true
						}
					} else {
						s.log(fmt.Sprintf("[↑] Загрузка нового файла: %s", path))
						if !dryRun {
							parentID, err := s.EnsureRemoteFolder(workerCtx, pathPkg.Dir(path))
							if err != nil {
								if isAuthOrTokenError(err) {
									tokenErrOnce.Do(func() {
										tokenAuthErr = err
										cancelWorker()
									})
									return
								}
								atomic.StoreInt32(&anyOpFailed, 1)
								s.log(fmt.Sprintf("[!] Ошибка создания папки для %s: %v", path, err))
							} else {
								fileName := pathPkg.Base(path)
								escapedFile := strings.ReplaceAll(fileName, "\\", "\\\\")
								escapedFile = strings.ReplaceAll(escapedFile, "'", "\\'")
								qFile := fmt.Sprintf("'%s' in parents and name = '%s' and trashed = false", parentID, escapedFile)
								listRes, errList := s.srv.Files.List().Q(qFile).Spaces("drive").Fields("files(id, md5Checksum)").PageSize(1).Context(workerCtx).Do()
								if errList != nil && isAuthOrTokenError(errList) {
									tokenErrOnce.Do(func() {
										tokenAuthErr = errList
										cancelWorker()
									})
									return
								}
								if errList == nil && len(listRes.Files) > 0 {
									existID := listRes.Files[0].Id
									locMD5, _ := s.GetOrComputeMD5(workerCtx, path, loc)
									if listRes.Files[0].Md5Checksum == locMD5 && locMD5 != "" {
										_ = s.db.UpsertFile(FileState{
											RelPath: path,
											FileID:  existID,
											MD5:     locMD5,
											MTime:   loc.MTime,
											Size:    loc.Size,
										})
										uploadOk = true
									} else {
										res, err := s.UpdateFile(workerCtx, existID, localFullPath)
										if err != nil {
											if isAuthOrTokenError(err) {
												tokenErrOnce.Do(func() {
													tokenAuthErr = err
													cancelWorker()
												})
												return
											}
											atomic.StoreInt32(&anyOpFailed, 1)
											s.log(fmt.Sprintf("[!] Ошибка обновления существующего файла %s в Google Drive: %v", path, err))
										} else {
											if res.Md5Checksum != "" && locMD5 != "" && res.Md5Checksum != locMD5 {
												s.log(fmt.Sprintf("[!] Ошибка целостности: MD5 файла %s не совпал с облачным!", path))
											} else {
												_ = s.db.UpsertFile(FileState{
													RelPath: path,
													FileID:  existID,
													MD5:     res.Md5Checksum,
													MTime:   loc.MTime,
													Size:    loc.Size,
												})
												uploadOk = true
											}
										}
									}
								} else {
									res, err := s.UploadFile(workerCtx, localFullPath, parentID, fileName)
									if err != nil {
										if isAuthOrTokenError(err) {
											tokenErrOnce.Do(func() {
												tokenAuthErr = err
												cancelWorker()
											})
											return
										}
										atomic.StoreInt32(&anyOpFailed, 1)
										s.log(fmt.Sprintf("[!] Ошибка загрузки нового файла %s в Google Drive: %v", path, err))
									} else {
										locMD5, _ := s.GetOrComputeMD5(workerCtx, path, loc)
										if res.Md5Checksum != "" && locMD5 != "" && res.Md5Checksum != locMD5 {
											s.log(fmt.Sprintf("[!] Ошибка целостности: MD5 файла %s не совпал с локальным! Удаляем поврежденную копию...", path))
											_, _ = s.srv.Files.Update(res.Id, &drive.File{Trashed: true}).Context(workerCtx).Do()
										} else {
											_ = s.db.UpsertFile(FileState{
												RelPath: path,
												FileID:  res.Id,
												MD5:     locMD5,
												MTime:   loc.MTime,
												Size:    loc.Size,
											})
											uploadOk = true
										}
									}
								}
							}
						} else {
							uploadOk = true
						}
					}

					atomic.AddInt64(&processedCount, 1)
					atomic.AddInt64(&processedBytes, loc.Size)
					if uploadOk {
						atomic.AddInt64(&uploadedOkCount, 1)
						_ = s.db.AddRecentFile(path, "upload", loc.Size)
					}
					reportProgress(pathPkg.Base(path), false)
				}
			}()
		}

		uploadWg.Wait()

		if tokenAuthErr != nil {
			return summary, fmt.Errorf("авторизация Google недействительна (токен отозван или истек). Нажмите «Вход Google Drive» в окне приложения")
		}
		if ctx.Err() != nil {
			return summary, ctx.Err()
		}
		if atomic.LoadInt32(&anyOpFailed) > 0 {
			operationFailed = true
		}
		summary.UploadedCount += int(atomic.LoadInt64(&uploadedOkCount))
		reportProgress("", true)
	}

	// 3. Process remote delta (files changed in cloud) if in two_way mode
	if syncMode == "two_way" && len(remoteFiles) > 0 {
		for relName, rem := range remoteFiles {
			if ctx.Err() != nil {
				return summary, ctx.Err()
			}

			// If file was just uploaded in this sync session, skip re-downloading it
			if _, wasLocallyUploaded := localChanges.NewOrChanged[relName]; wasLocallyUploaded {
				continue
			}

			localFullPath := s.localPathForRemote(relName)

			// Check if local file exists and already has identical MD5
			if rem.MD5 != "" {
				if fi, err := s.statLocalPath(localFullPath); err == nil && !fi.IsDir() {
					locMD5, _ := s.computeMD5LocalPath(localFullPath)
					if locMD5 == rem.MD5 {
						_ = s.db.UpsertFile(FileState{
							RelPath: relName,
							FileID:  rem.ID,
							MD5:     rem.MD5,
							MTime:   float64(fi.ModTime().Unix()),
							Size:    fi.Size(),
						})
						summary.VerifiedCount++
						continue
					}
				}
			}

			s.log(fmt.Sprintf("[↓] Скачивание обновлений из Google Диска: %s", relName))
			dlOk := false
			if !dryRun {
				if err := s.DownloadFile(ctx, rem.ID, localFullPath, rem.MD5); err != nil {
					operationFailed = true
					s.log(fmt.Sprintf("[!] Ошибка скачивания %s: %v", relName, err))
				} else {
					fi, err := s.statLocalPath(localFullPath)
					if err != nil {
						operationFailed = true
						s.log(fmt.Sprintf("[!] Не удалось проверить скачанный файл %s: %v", relName, err))
						continue
					}
					_ = s.db.UpsertFile(FileState{
						RelPath: relName,
						FileID:  rem.ID,
						MD5:     rem.MD5,
						MTime:   float64(fi.ModTime().Unix()),
						Size:    fi.Size(),
					})
					dlOk = true
				}
			} else {
				dlOk = true
			}
			if dlOk {
				summary.DownloadCount++
				_ = s.db.AddRecentFile(relName, "download", rem.Size)
			}
		}
	}

	if operationFailed {
		return summary, fmt.Errorf("синхронизация завершена с ошибками; контрольная точка не обновлена, проблемные операции будут повторены")
	}
	if err := s.db.SetMeta("last_sync_rfc3339", startTime); err != nil {
		return summary, err
	}
	if s.progressCallback != nil {
		s.progressCallback(SyncProgress{
			Phase:          "done",
			TotalFiles:     summary.UploadedCount + summary.DownloadCount,
			ProcessedFiles: summary.UploadedCount + summary.DownloadCount,
			Percent:        100.0,
			SpeedFormatted: "0 Б/с",
			ETAFormatted:   "00:00",
		})
	}
	return summary, nil
}

func (s *SyncEngineGo) VerifyIntegrity(ctx context.Context) (int, int, []string, error) {
	stored, err := s.db.GetAllFiles()
	if err != nil {
		return 0, 0, nil, err
	}
	remoteFiles, err := s.FetchRemoteTree(ctx)
	if err != nil {
		return 0, 0, nil, err
	}

	matched := 0
	mismatched := 0
	var errorsList []string

	for path, rem := range remoteFiles {
		if ctx.Err() != nil {
			return matched, mismatched, errorsList, ctx.Err()
		}

		st, inDB := stored[path]
		localFullPath := s.localPathForRemote(path)
		fi, err := s.statLocalPath(localFullPath)
		if err != nil {
			errorsList = append(errorsList, fmt.Sprintf("Отсутствует локально: %s", path))
			mismatched++
			continue
		}

		if rem.MD5 == "" {
			continue // Native Google docs
		}

		locMD5 := ""
		if inDB && st.Size == fi.Size() && st.MTime == float64(fi.ModTime().Unix()) && st.MD5 != "" {
			locMD5 = st.MD5
		} else {
			locMD5, _ = s.computeMD5LocalPath(localFullPath)
		}

		if locMD5 == rem.MD5 {
			matched++
		} else {
			mismatched++
			errorsList = append(errorsList, fmt.Sprintf("Несовпадение хеша: %s (локальный: %s, облако: %s)", path, locMD5, rem.MD5))
		}
	}

	return matched, mismatched, errorsList, nil
}
