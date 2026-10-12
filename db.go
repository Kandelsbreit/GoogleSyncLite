package main

import (
	"database/sql"
	"fmt"
	pathPkg "path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type FileState struct {
	RelPath      string
	FileID       string
	MD5          string
	MTime        float64
	Size         int64
	IsDir        bool
	LastSyncedAt float64
}

type RecentFileItem struct {
	ID            int64  `json:"id"`
	RelPath       string `json:"rel_path"`
	Action        string `json:"action"` // "upload", "download", "delete", "conflict"
	Size          int64  `json:"size"`
	SizeFormatted string `json:"size_formatted"`
	Timestamp     string `json:"timestamp"`
	TimeAgo       string `json:"time_ago"`
	DriveLink     string `json:"drive_link"`
}

type SearchResultItem struct {
	RelPath       string `json:"rel_path"`
	FileName      string `json:"file_name"`
	Folder        string `json:"folder"`
	FileID        string `json:"file_id"`
	Size          int64  `json:"size"`
	SizeFormatted string `json:"size_formatted"`
	DriveLink     string `json:"drive_link"`
	MTime         string `json:"mtime"`
	SyncedAt      string `json:"synced_at"`
	IsDir         bool   `json:"is_dir"`
}

type StorageCategoryStat struct {
	Category      string  `json:"category"`
	Count         int     `json:"count"`
	TotalBytes    int64   `json:"total_bytes"`
	SizeFormatted string  `json:"size_formatted"`
	Percentage    float64 `json:"percentage"`
	Color         string  `json:"color"`
}

type StorageBreakdown struct {
	TotalFiles    int                   `json:"total_files"`
	TotalBytes    int64                 `json:"total_bytes"`
	SizeFormatted string                `json:"size_formatted"`
	Categories    []StorageCategoryStat `json:"categories"`
}

type Database struct {
	db   *sql.DB
	lock sync.Mutex
}

func OpenDatabase(path string) (*Database, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}

	// SQLite embedded single connection pool prevents WAL connection lock issues
	db.SetMaxOpenConns(1)

	_, err = db.Exec(`
		PRAGMA journal_mode = WAL;
		PRAGMA busy_timeout = 5000;
		PRAGMA synchronous = NORMAL;

		CREATE TABLE IF NOT EXISTS files (
			rel_path TEXT PRIMARY KEY,
			file_id TEXT,
			md5 TEXT,
			mtime REAL,
			size INTEGER,
			is_dir INTEGER DEFAULT 0,
			last_synced_at REAL
		);

		CREATE TABLE IF NOT EXISTS sync_meta (
			key TEXT PRIMARY KEY,
			value TEXT
		);

		CREATE TABLE IF NOT EXISTS recent_files (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			rel_path TEXT,
			action TEXT,
			size INTEGER,
			synced_at INTEGER
		);
		CREATE INDEX IF NOT EXISTS idx_recent_synced_at ON recent_files(synced_at DESC);
	`)
	if err != nil {
		db.Close()
		return nil, err
	}

	return &Database{db: db}, nil
}

func (d *Database) Close() error {
	return d.db.Close()
}

func (d *Database) GetMeta(key string) string {
	d.lock.Lock()
	defer d.lock.Unlock()

	var val string
	row := d.db.QueryRow("SELECT value FROM sync_meta WHERE key = ?", key)
	if err := row.Scan(&val); err != nil {
		return ""
	}
	return val
}

func (d *Database) SetMeta(key, value string) error {
	d.lock.Lock()
	defer d.lock.Unlock()

	_, err := d.db.Exec(`
		INSERT INTO sync_meta (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value;
	`, key, value)
	return err
}

func (d *Database) GetAllFiles() (map[string]FileState, error) {
	d.lock.Lock()
	defer d.lock.Unlock()

	rows, err := d.db.Query("SELECT rel_path, file_id, md5, mtime, size, is_dir, last_synced_at FROM files")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]FileState)
	for rows.Next() {
		var s FileState
		var isDirInt int
		if err := rows.Scan(&s.RelPath, &s.FileID, &s.MD5, &s.MTime, &s.Size, &isDirInt, &s.LastSyncedAt); err != nil {
			return nil, err
		}
		s.IsDir = isDirInt == 1
		result[s.RelPath] = s
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (d *Database) GetFile(relPath string) (*FileState, error) {
	d.lock.Lock()
	defer d.lock.Unlock()

	var s FileState
	var isDirInt int
	row := d.db.QueryRow("SELECT rel_path, file_id, md5, mtime, size, is_dir, last_synced_at FROM files WHERE rel_path = ?", relPath)
	if err := row.Scan(&s.RelPath, &s.FileID, &s.MD5, &s.MTime, &s.Size, &isDirInt, &s.LastSyncedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	s.IsDir = isDirInt == 1
	return &s, nil
}

func (d *Database) UpsertFile(s FileState) error {
	d.lock.Lock()
	defer d.lock.Unlock()

	isDirInt := 0
	if s.IsDir {
		isDirInt = 1
	}

	_, err := d.db.Exec(`
		INSERT INTO files (rel_path, file_id, md5, mtime, size, is_dir, last_synced_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(rel_path) DO UPDATE SET
			file_id=excluded.file_id,
			md5=excluded.md5,
			mtime=excluded.mtime,
			size=excluded.size,
			is_dir=excluded.is_dir,
			last_synced_at=excluded.last_synced_at;
	`, s.RelPath, s.FileID, s.MD5, s.MTime, s.Size, isDirInt, float64(time.Now().Unix()))
	return err
}

func (d *Database) UpsertFilesBatch(list []FileState) error {
	if len(list) == 0 {
		return nil
	}
	d.lock.Lock()
	defer d.lock.Unlock()

	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO files (rel_path, file_id, md5, mtime, size, is_dir, last_synced_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(rel_path) DO UPDATE SET
			file_id=excluded.file_id,
			md5=excluded.md5,
			mtime=excluded.mtime,
			size=excluded.size,
			is_dir=excluded.is_dir,
			last_synced_at=excluded.last_synced_at;
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := float64(time.Now().Unix())
	for _, s := range list {
		isDirInt := 0
		if s.IsDir {
			isDirInt = 1
		}
		if _, err := stmt.Exec(s.RelPath, s.FileID, s.MD5, s.MTime, s.Size, isDirInt, now); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (d *Database) RemoveFile(relPath string) error {
	d.lock.Lock()
	defer d.lock.Unlock()

	_, err := d.db.Exec("DELETE FROM files WHERE rel_path = ?", relPath)
	return err
}

func (d *Database) RemoveFilesBatch(relPaths []string) error {
	if len(relPaths) == 0 {
		return nil
	}
	d.lock.Lock()
	defer d.lock.Unlock()

	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare("DELETE FROM files WHERE rel_path = ?")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, p := range relPaths {
		if _, err := stmt.Exec(p); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *Database) AddRecentFile(relPath, action string, size int64) error {
	if d == nil || d.db == nil {
		return nil
	}
	d.lock.Lock()
	defer d.lock.Unlock()

	now := time.Now().Unix()
	_, err := d.db.Exec(`
		INSERT INTO recent_files (rel_path, action, size, synced_at)
		VALUES (?, ?, ?, ?);
	`, relPath, action, size, now)
	if err != nil {
		return err
	}

	// Keep only the latest 100 entries so the table remains lean and fast
	_, _ = d.db.Exec(`
		DELETE FROM recent_files WHERE id NOT IN (
			SELECT id FROM recent_files ORDER BY synced_at DESC, id DESC LIMIT 100
		);
	`)
	return nil
}

func (d *Database) GetRecentFiles(limit int) ([]RecentFileItem, error) {
	if d == nil || d.db == nil {
		return []RecentFileItem{}, nil
	}
	d.lock.Lock()
	defer d.lock.Unlock()

	if limit <= 0 || limit > 50 {
		limit = 20
	}

	rows, err := d.db.Query(`
		SELECT r.id, r.rel_path, r.action, r.size, r.synced_at, COALESCE(f.file_id, '')
		FROM recent_files r
		LEFT JOIN files f ON r.rel_path = f.rel_path
		ORDER BY r.synced_at DESC, r.id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return []RecentFileItem{}, err
	}
	defer rows.Close()

	var items []RecentFileItem
	for rows.Next() {
		var it RecentFileItem
		var syncedAt int64
		var fileID string
		if err := rows.Scan(&it.ID, &it.RelPath, &it.Action, &it.Size, &syncedAt, &fileID); err != nil {
			return items, err
		}
		if fileID != "" && it.Action != "delete" {
			it.DriveLink = fmt.Sprintf("https://drive.google.com/file/d/%s/view", fileID)
		}
		it.SizeFormatted = FormatBytes(it.Size)
		t := time.Unix(syncedAt, 0)
		it.Timestamp = t.Format("15:04:05")
		it.TimeAgo = FormatTimeAgo(t)
		items = append(items, it)
	}
	if items == nil {
		items = []RecentFileItem{}
	}
	return items, nil
}

func FormatTimeAgo(t time.Time) string {
	diff := time.Since(t)
	if diff < time.Minute {
		return "только что"
	} else if diff < time.Hour {
		return fmt.Sprintf("%d мин. назад", int(diff.Minutes()))
	} else if diff < 24*time.Hour {
		return fmt.Sprintf("%d ч. назад", int(diff.Hours()))
	}
	return t.Format("02.01 15:04")
}

func (d *Database) SearchFiles(query string, limit int) ([]SearchResultItem, error) {
	if d == nil || d.db == nil {
		return []SearchResultItem{}, nil
	}
	d.lock.Lock()
	defer d.lock.Unlock()

	if limit <= 0 || limit > 100 {
		limit = 50
	}

	query = strings.TrimSpace(query)
	var rows *sql.Rows
	var err error

	if query == "" {
		rows, err = d.db.Query(`
			SELECT rel_path, COALESCE(file_id, ''), size, mtime, last_synced_at, is_dir
			FROM files
			WHERE is_dir = 0
			ORDER BY last_synced_at DESC, rel_path ASC
			LIMIT ?
		`, limit)
	} else {
		terms := strings.Fields(query)
		var whereClauses []string
		var args []interface{}
		for _, t := range terms {
			whereClauses = append(whereClauses, "rel_path LIKE ?")
			args = append(args, "%"+t+"%")
		}
		args = append(args, limit)
		sqlStr := fmt.Sprintf(`
			SELECT rel_path, COALESCE(file_id, ''), size, mtime, last_synced_at, is_dir
			FROM files
			WHERE %s
			ORDER BY rel_path ASC
			LIMIT ?
		`, strings.Join(whereClauses, " AND "))
		rows, err = d.db.Query(sqlStr, args...)
	}

	if err != nil {
		return []SearchResultItem{}, err
	}
	defer rows.Close()

	var results []SearchResultItem
	for rows.Next() {
		var it SearchResultItem
		var mtime float64
		var syncedAt float64
		var isDirInt int
		if err := rows.Scan(&it.RelPath, &it.FileID, &it.Size, &mtime, &syncedAt, &isDirInt); err != nil {
			return results, err
		}
		it.IsDir = isDirInt == 1
		it.FileName = pathPkg.Base(it.RelPath)
		dir := pathPkg.Dir(it.RelPath)
		if dir == "." {
			dir = ""
		}
		it.Folder = dir
		it.SizeFormatted = FormatBytes(it.Size)

		if it.FileID != "" {
			it.DriveLink = fmt.Sprintf("https://drive.google.com/file/d/%s/view", it.FileID)
		}

		if mtime > 0 {
			it.MTime = time.Unix(int64(mtime), 0).Format("02.01.2006 15:04")
		}
		if syncedAt > 0 {
			it.SyncedAt = time.Unix(int64(syncedAt), 0).Format("02.01.2006 15:04")
		}

		results = append(results, it)
	}
	if results == nil {
		results = []SearchResultItem{}
	}
	return results, nil
}

func (d *Database) GetStorageBreakdown() (*StorageBreakdown, error) {
	if d == nil || d.db == nil {
		return &StorageBreakdown{}, nil
	}
	d.lock.Lock()
	defer d.lock.Unlock()

	rows, err := d.db.Query(`
		SELECT rel_path, size
		FROM files
		WHERE is_dir = 0
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	categoriesMap := map[string]*StorageCategoryStat{
		"Документы":     {Category: "Документы", Color: "#3b82f6"},
		"Изображения":   {Category: "Изображения", Color: "#10b981"},
		"Видео и Аудио": {Category: "Видео и Аудио", Color: "#8b5cf6"},
		"Архивы":        {Category: "Архивы", Color: "#f59e0b"},
		"Код и Данные":  {Category: "Код и Данные", Color: "#06b6d4"},
		"Другое":        {Category: "Другое", Color: "#64748b"},
	}

	var totalFiles int
	var totalBytes int64

	for rows.Next() {
		var relPath string
		var size int64
		if err := rows.Scan(&relPath, &size); err != nil {
			continue
		}
		totalFiles++
		totalBytes += size

		ext := strings.ToLower(filepath.Ext(relPath))
		ext = strings.TrimPrefix(ext, ".")

		catKey := "Другое"
		switch ext {
		case "pdf", "doc", "docx", "xls", "xlsx", "ppt", "pptx", "txt", "rtf", "odt", "csv", "md":
			catKey = "Документы"
		case "jpg", "jpeg", "png", "gif", "bmp", "webp", "svg", "tiff", "psd", "ai", "ico", "raw":
			catKey = "Изображения"
		case "mp4", "mkv", "avi", "mov", "wmv", "flv", "mp3", "wav", "flac", "aac", "ogg", "m4a":
			catKey = "Видео и Аудио"
		case "zip", "rar", "7z", "tar", "gz", "bz2", "iso", "xz":
			catKey = "Архивы"
		case "go", "js", "ts", "py", "html", "css", "json", "xml", "sql", "yaml", "yml", "c", "cpp", "h", "rs", "java", "php", "sh", "bat", "ps1":
			catKey = "Код и Данные"
		}

		c := categoriesMap[catKey]
		c.Count++
		c.TotalBytes += size
	}

	order := []string{"Документы", "Изображения", "Видео и Аудио", "Архивы", "Код и Данные", "Другое"}
	var list []StorageCategoryStat
	for _, key := range order {
		c := categoriesMap[key]
		if totalBytes > 0 {
			c.Percentage = float64(c.TotalBytes) / float64(totalBytes) * 100.0
		}
		c.SizeFormatted = FormatBytes(c.TotalBytes)
		list = append(list, *c)
	}

	return &StorageBreakdown{
		TotalFiles:    totalFiles,
		TotalBytes:    totalBytes,
		SizeFormatted: FormatBytes(totalBytes),
		Categories:    list,
	}, nil
}
