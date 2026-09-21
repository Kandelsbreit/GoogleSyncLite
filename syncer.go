package main

import (
	"context"
	"fmt"
	"io"
	"os"
	pathPkg "path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"google.golang.org/api/drive/v3"
)

type DriveFileMeta struct {
	ID           string
	Name         string
	Size         int64
	MD5          string
	ModifiedTime string
	ParentID     string
	IsDir        bool
}

type SyncSummary struct {
	VerifiedCount int
	UploadedCount int
	DownloadCount int
	DeletedCount  int
	ConflictCount int
}

type SyncEngineGo struct {
	srv         *drive.Service
	db          *Database
	localRoot   string
	remoteRoot  string
	foldersMap  map[string]string // relPath -> remote folder ID
	logCallback func(string)
}

func NewSyncEngineGo(srv *drive.Service, db *Database, localRoot, remoteRoot string, logCb func(string)) *SyncEngineGo {
	return &SyncEngineGo{
		srv:         srv,
		db:          db,
		localRoot:   localRoot,
		remoteRoot:  remoteRoot,
		foldersMap:  make(map[string]string),
		logCallback: logCb,
	}
}

func (s *SyncEngineGo) log(msg string) {
	if s.logCallback != nil {
		s.logCallback(msg)
	}
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
	st, err := os.Stat(s.localRoot)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("локальная папка не найдена или диск отключен: '%s'", s.localRoot)
	}
	if err != nil {
		return nil, fmt.Errorf("ошибка доступа к локальному диску/папке '%s': %w", s.localRoot, err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("указанный путь '%s' не является папкой", s.localRoot)
	}

	// Verify the folder can actually be read
	entries, err := os.ReadDir(s.localRoot)
	if err != nil {
		return nil, fmt.Errorf("не удалось прочитать содержимое папки '%s' (диск отключен или заблокирован): %w", s.localRoot, err)
	}

	// If database already contains many files, but the root folder is completely empty, trigger safety abort!
	if len(stored) > 10 && len(entries) == 0 {
		return nil, fmt.Errorf("КРИТИЧЕСКАЯ ЗАЩИТА: Папка '%s' пуста, хотя в базе числится %d файлов! Диск может быть отключен. Синхронизация заблокирована во избежание удаления данных из облака.", s.localRoot, len(stored))
	}

	// Canary / Anchor file check: verify drive and folder identity
	anchorPath := filepath.Join(s.localRoot, ".google_sync_anchor")
	const expectedAnchor = "google-sync-lite-anchor-verified\n"
	anchorBytes, err := os.ReadFile(anchorPath)
	if os.IsNotExist(err) {
		if len(stored) > 10 {
			return nil, fmt.Errorf("КРИТИЧЕСКАЯ ЗАЩИТА: Файл привязки диска (.google_sync_anchor) не найден в '%s'! Диск отключен или сменилась буква. Проверьте накопитель!", s.localRoot)
		}
		_ = os.WriteFile(anchorPath, []byte(expectedAnchor), 0644)
	} else if err == nil {
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

	err = filepath.Walk(s.localRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if path == s.localRoot {
				return fmt.Errorf("ошибка доступа к корневой папке '%s': %w", s.localRoot, err)
			}
			s.log(fmt.Sprintf("[!] Пропуск недоступного файла/папки: %s (%v)", path, err))
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if info.IsDir() {
			name := info.Name()
			if strings.HasPrefix(name, ".") || name == "__pycache__" || name == "$RECYCLE.BIN" || name == "System Volume Information" {
				return filepath.SkipDir
			}
			return nil
		}

		rel, err := filepath.Rel(s.localRoot, path)
		if err != nil {
			return nil
		}
		cleanRel := filepath.ToSlash(rel)

		if IsIgnoredRelPath(cleanRel) {
			return nil
		}

		seenPaths[cleanRel] = true

		fileMTime := float64(info.ModTime().Unix())
		fileSize := info.Size()

		count++
		if count%10000 == 0 || time.Since(lastReport) > 2*time.Second {
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
	localFullPath := filepath.Join(s.localRoot, filepath.FromSlash(relPath))
	hash, err := ComputeMD5(localFullPath)
	if err != nil {
		return "", err
	}
	return hash, nil
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
	if rootID != "root" {
		queue = append(queue, queueItem{id: "root", path: ""})
	}

	visitedFolders := make(map[string]bool)

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		if visitedFolders[curr.id] && curr.id != rootID && curr.id != "root" {
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
func (s *SyncEngineGo) FetchRemoteDelta(ctx context.Context, lastSyncRFC3339 string) (map[string]DriveFileMeta, bool, error) {
	if lastSyncRFC3339 == "" {
		tree, err := s.FetchRemoteTree(ctx)
		return tree, false, err
	}

	// Query only files changed since last sync
	query := fmt.Sprintf("modifiedTime > '%s' and trashed = false", lastSyncRFC3339)
	s.log(fmt.Sprintf("[*] Запрос изменений в Google Drive (с %s)...", lastSyncRFC3339))

	filesMap := make(map[string]DriveFileMeta)
	pageToken := ""

	for {
		if ctx.Err() != nil {
			return nil, true, ctx.Err()
		}

		call := s.srv.Files.List().
			Q(query).
			Spaces("drive").
			Fields("nextPageToken, files(id, name, mimeType, md5Checksum, modifiedTime, size, parents)").
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

		// Invert foldersMap (folderID -> relPath) to resolve parent directory paths
		idToRelFolder := make(map[string]string)
		for fRel, fID := range s.foldersMap {
			idToRelFolder[fID] = fRel
		}

		for _, f := range r.Files {
			if f.MimeType != "application/vnd.google-apps.folder" {
				pID := ""
				if len(f.Parents) > 0 {
					pID = f.Parents[0]
				}

				// Resolve relative path for this file
				relPath := f.Name
				if pID != "" && pID != s.remoteRoot && pID != "root" {
					if parentRel, ok := idToRelFolder[pID]; ok && parentRel != "" {
						relPath = parentRel + "/" + f.Name
					} else {
						// Folder hierarchy for this file is not known; fallback to full FetchRemoteTree to ensure correctness
						s.log(fmt.Sprintf("[*] Обнаружен файл в новом подкаталоге Google Drive (%s), обновление полного дерева...", f.Name))
						tree, err := s.FetchRemoteTree(ctx)
						return tree, false, err
					}
				}

				filesMap[relPath] = DriveFileMeta{
					ID:           f.Id,
					Name:         f.Name,
					Size:         f.Size,
					MD5:          f.Md5Checksum,
					ModifiedTime: f.ModifiedTime,
					ParentID:     pID,
					IsDir:        false,
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
		if part == "" || part == "." || part == ".." {
			continue
		}

		// Replace illegal characters: < > : " | ? * and control chars (ASCII < 32)
		var sb strings.Builder
		for _, r := range part {
			if r < 32 || r == '<' || r == '>' || r == ':' || r == '"' || r == '|' || r == '?' || r == '*' {
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
	localDestPath = SanitizeWindowsPath(localDestPath)
	tmpDest := localDestPath + ".tmp"
	_ = os.MkdirAll(filepath.Dir(localDestPath), 0755)

	resp, err := s.srv.Files.Get(fileID).Context(ctx).Download()
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	out, err := os.Create(tmpDest)
	if err != nil {
		return err
	}

	_, copyErr := io.Copy(out, resp.Body)
	if copyErr != nil {
		out.Close()
		os.Remove(tmpDest)
		return copyErr
	}

	// Flush OS write buffers to physical disk to prevent zero-byte corrupt files on power loss
	if syncErr := out.Sync(); syncErr != nil {
		out.Close()
		os.Remove(tmpDest)
		return syncErr
	}
	out.Close()

	// Verify MD5 before replacing target file
	if expectedMD5 != "" {
		chkMD5, err := ComputeMD5(tmpDest)
		if err == nil && chkMD5 != expectedMD5 {
			os.Remove(tmpDest)
			return fmt.Errorf("ошибка целостности MD5 для %s (ожидался: %s, получен: %s)", localDestPath, expectedMD5, chkMD5)
		}
	}

	// Atomically replace target file
	return os.Rename(tmpDest, localDestPath)
}

func (s *SyncEngineGo) UploadFile(ctx context.Context, localPath, parentID, remoteName string) (*drive.File, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	f, err := os.Open(localPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	driveFile := &drive.File{
		Name:    remoteName,
		Parents: []string{parentID},
	}

	return s.srv.Files.Create(driveFile).Media(f).Fields("id, name, md5Checksum, modifiedTime, size").Context(ctx).Do()
}

func (s *SyncEngineGo) UpdateFile(ctx context.Context, fileID, localPath string) (*drive.File, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	f, err := os.Open(localPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return s.srv.Files.Update(fileID, &drive.File{}).Media(f).Fields("id, name, md5Checksum, modifiedTime, size").Context(ctx).Do()
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
			remoteFiles, isDelta, err = s.FetchRemoteDelta(ctx, lastSyncTimeRFC)
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

	// If no local changes and no remote changes -> instant completion!
	if len(localChanges.NewOrChanged) == 0 && len(localChanges.Deleted) == 0 && len(remoteFiles) == 0 {
		s.log("[✓] Изменений не обнаружено. Все файлы синхронизированы.")
		s.db.SetMeta("last_sync_rfc3339", startTime)
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

		for path := range allPaths {
			if ctx.Err() != nil {
				return summary, ctx.Err()
			}
			loc, hasLoc := localChanges.NewOrChanged[path]
			rem, hasRem := remoteFiles[path]
			localFullPath := filepath.Join(s.localRoot, filepath.FromSlash(path))

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
						s.log(fmt.Sprintf("[!] Ошибка создания папки для %s: %v", path, err))
					} else {
						fileName := pathPkg.Base(path)
						res, err := s.UploadFile(ctx, localFullPath, parentID, fileName)
						if err != nil {
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
				}
			} else if hasRem && !hasLoc {
				if syncMode == "two_way" {
					s.log(fmt.Sprintf("[↓] Скачивание из облака: %s", path))
					dlOk := false
					if !dryRun {
						if err := s.DownloadFile(ctx, rem.ID, localFullPath, rem.MD5); err != nil {
							s.log(fmt.Sprintf("[!] Ошибка скачивания %s: %v", path, err))
						} else {
							fi, _ := os.Stat(localFullPath)
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
				if _, err := os.Stat(localFolder); os.IsNotExist(err) {
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
				summary.DeletedCount++
			}
		}
	}

	// 2. Process locally new or changed files
	for path, loc := range localChanges.NewOrChanged {
		if ctx.Err() != nil {
			return summary, ctx.Err()
		}
		localFullPath := filepath.Join(s.localRoot, filepath.FromSlash(path))
		st, inDB := stored[path]

		if inDB && st.FileID != "" {
			// File exists in DB -> update it on Google Drive
			s.log(fmt.Sprintf("[↑] Загрузка обновлений: %s", path))
			uploadOk := false
			if !dryRun {
				res, err := s.UpdateFile(ctx, st.FileID, localFullPath)
				if err != nil {
					s.log(fmt.Sprintf("[!] Ошибка обновления файла %s в Google Drive: %v", path, err))
				} else {
					locMD5, _ := s.GetOrComputeMD5(ctx, path, loc)
					if res.Md5Checksum != "" && locMD5 != "" && res.Md5Checksum != locMD5 {
						s.log(fmt.Sprintf("[!] Ошибка целостности: MD5 файла %s не совпал с облачным!", path))
						continue
					}
					_ = s.db.UpsertFile(FileState{
						RelPath: path,
						FileID:  st.FileID,
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
		} else {
			// New file
			s.log(fmt.Sprintf("[↑] Загрузка нового файла: %s", path))
			uploadOk := false
			if !dryRun {
				parentID, err := s.EnsureRemoteFolder(ctx, pathPkg.Dir(path))
				if err != nil {
					s.log(fmt.Sprintf("[!] Ошибка создания папки для %s: %v", path, err))
				} else {
					fileName := pathPkg.Base(path)
					// Check if file already exists in cloud under parentID (to avoid duplicate files)
					escapedFile := strings.ReplaceAll(fileName, "\\", "\\\\")
					escapedFile = strings.ReplaceAll(escapedFile, "'", "\\'")
					qFile := fmt.Sprintf("'%s' in parents and name = '%s' and trashed = false", parentID, escapedFile)
					listRes, errList := s.srv.Files.List().Q(qFile).Spaces("drive").Fields("files(id, md5Checksum)").PageSize(1).Context(ctx).Do()
					if errList == nil && len(listRes.Files) > 0 {
						// Existing file in cloud -> update or register it!
						existID := listRes.Files[0].Id
						locMD5, _ := s.GetOrComputeMD5(ctx, path, loc)
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
							res, err := s.UpdateFile(ctx, existID, localFullPath)
							if err != nil {
								s.log(fmt.Sprintf("[!] Ошибка обновления существующего файла %s в Google Drive: %v", path, err))
							} else {
								if res.Md5Checksum != "" && locMD5 != "" && res.Md5Checksum != locMD5 {
									s.log(fmt.Sprintf("[!] Ошибка целостности: MD5 файла %s не совпал с облачным!", path))
									continue
								}
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
					} else {
						// New upload
						res, err := s.UploadFile(ctx, localFullPath, parentID, fileName)
						if err != nil {
							s.log(fmt.Sprintf("[!] Ошибка загрузки нового файла %s в Google Drive: %v", path, err))
						} else {
							locMD5, _ := s.GetOrComputeMD5(ctx, path, loc)
							if res.Md5Checksum != "" && locMD5 != "" && res.Md5Checksum != locMD5 {
								s.log(fmt.Sprintf("[!] Ошибка целостности: MD5 файла %s не совпал с локальным! Удаляем поврежденную копию...", path))
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
				}
			} else {
				uploadOk = true
			}
			if uploadOk {
				summary.UploadedCount++
			}
		}
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

			localFullPath := filepath.Join(s.localRoot, filepath.FromSlash(relName))

			// Check if local file exists and already has identical MD5
			if rem.MD5 != "" {
				if fi, err := os.Stat(localFullPath); err == nil && !fi.IsDir() {
					locMD5, _ := ComputeMD5(localFullPath)
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
					s.log(fmt.Sprintf("[!] Ошибка скачивания %s: %v", relName, err))
				} else {
					fi, _ := os.Stat(localFullPath)
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
			}
		}
	}

	s.db.SetMeta("last_sync_rfc3339", startTime)
	return summary, nil
}

func (s *SyncEngineGo) VerifyIntegrity(ctx context.Context) (int, int, []string, error) {
	stored, _ := s.db.GetAllFiles()
	localChanges, err := s.ScanLocalDelta(ctx, stored)
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
		localFullPath := filepath.Join(s.localRoot, filepath.FromSlash(path))
		fi, err := os.Stat(localFullPath)
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
			locMD5, _ = ComputeMD5(localFullPath)
		}

		if locMD5 == rem.MD5 {
			matched++
		} else {
			mismatched++
			errorsList = append(errorsList, fmt.Sprintf("Несовпадение хеша: %s (локальный: %s, облако: %s)", path, locMD5, rem.MD5))
		}
	}

	_ = localChanges
	return matched, mismatched, errorsList, nil
}
