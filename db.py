import sqlite3
import os
from typing import Optional, Dict, Any

class StateDB:
    """Manages the local sync state database using SQLite."""
    def __init__(self, db_path: str = "sync_state.db"):
        self.db_path = db_path
        self._init_db()

    def _get_conn(self) -> sqlite3.Connection:
        conn = sqlite3.connect(self.db_path)
        conn.row_factory = sqlite3.Row
        return conn

    def _init_db(self):
        conn = self._get_conn()
        try:
            with conn:
                conn.execute("""
                    CREATE TABLE IF NOT EXISTS files (
                        rel_path TEXT PRIMARY KEY,
                        file_id TEXT,
                        md5 TEXT,
                        mtime REAL,
                        size INTEGER,
                        is_dir INTEGER DEFAULT 0,
                        last_synced_at REAL
                    )
                """)
                conn.execute("""
                    CREATE TABLE IF NOT EXISTS config_meta (
                        key TEXT PRIMARY KEY,
                        value TEXT
                    )
                """)
        finally:
            conn.close()

    def get_file(self, rel_path: str) -> Optional[Dict[str, Any]]:
        conn = self._get_conn()
        try:
            cursor = conn.execute("SELECT * FROM files WHERE rel_path = ?", (rel_path,))
            row = cursor.fetchone()
            return dict(row) if row else None
        finally:
            conn.close()

    def get_all_files(self) -> Dict[str, Dict[str, Any]]:
        conn = self._get_conn()
        try:
            cursor = conn.execute("SELECT * FROM files")
            return {row["rel_path"]: dict(row) for row in cursor.fetchall()}
        finally:
            conn.close()

    def upsert_file(self, rel_path: str, file_id: str, md5: str, mtime: float, size: int, is_dir: bool, synced_at: float):
        conn = self._get_conn()
        try:
            with conn:
                conn.execute("""
                    INSERT INTO files (rel_path, file_id, md5, mtime, size, is_dir, last_synced_at)
                    VALUES (?, ?, ?, ?, ?, ?, ?)
                    ON CONFLICT(rel_path) DO UPDATE SET
                        file_id=excluded.file_id,
                        md5=excluded.md5,
                        mtime=excluded.mtime,
                        size=excluded.size,
                        is_dir=excluded.is_dir,
                        last_synced_at=excluded.last_synced_at
                """, (rel_path, file_id, md5, mtime, size, 1 if is_dir else 0, synced_at))
        finally:
            conn.close()

    def remove_file(self, rel_path: str):
        conn = self._get_conn()
        try:
            with conn:
                conn.execute("DELETE FROM files WHERE rel_path = ?", (rel_path,))
        finally:
            conn.close()
