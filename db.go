package main

import (
	"database/sql"
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
	return result, nil
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
