import os
import sys
import json
import time
import threading
import argparse
from typing import Optional

from auth import get_credentials, TOKEN_FILE, CREDENTIALS_FILE
from drive_api import DriveClient
from db import StateDB
from syncer import SyncEngine
from autostart import is_autostart_enabled, set_autostart

CONFIG_FILE = "config.json"

DEFAULT_CONFIG = {
    "local_folder": os.path.abspath("./sync_folder"),
    "remote_folder_id": "root",
    "sync_interval_seconds": 60,
    "dry_run": False
}

def load_config() -> dict:
    if not os.path.exists(CONFIG_FILE):
        with open(CONFIG_FILE, "w", encoding="utf-8") as f:
            json.dump(DEFAULT_CONFIG, f, indent=4)
        return DEFAULT_CONFIG
    try:
        with open(CONFIG_FILE, "r", encoding="utf-8") as f:
            return json.load(f)
    except Exception:
        return DEFAULT_CONFIG

class Application:
    """Main application orchestrator supporting GUI, background worker, and System Tray."""
    def __init__(self, start_minimized: bool = False):
        self.config = load_config()
        self.start_minimized = start_minimized
        self.engine: Optional[SyncEngine] = None
        self.is_syncing = False
        self.running = True
        self.tray = None
        self.gui = None

    def init_engine(self) -> Optional[SyncEngine]:
        try:
            creds = get_credentials()
            drive = DriveClient(creds)
            db = StateDB()
            self.engine = SyncEngine(
                local_root=self.config.get("local_folder", "./sync_folder"),
                remote_root_id=self.config.get("remote_folder_id", "root"),
                drive_client=drive,
                db=db
            )
            return self.engine
        except Exception as e:
            self.log(f"[!] Engine init notice: {e}")
            return None

    def log(self, text: str):
        print(text)
        if self.gui:
            self.gui.log(text)

    def run_sync(self):
        if self.is_syncing:
            self.log("[*] Синхронизация уже выполняется, пропуск вызова.")
            return

        self.is_syncing = True
        if self.tray:
            self.tray.update_icon_state(syncing=True)

        try:
            if not self.engine:
                self.init_engine()

            if not self.engine:
                self.log("[!] Не удалось начать: требуется вход через Google (нажмите кнопку Авторизация).")
                return

            self.log("[*] Начинаем процедуру синхронизации...")
            self.engine.sync(dry_run=self.config.get("dry_run", False))
            if self.tray:
                self.tray.notify("Google Sync Lite", "Синхронизация успешно завершена.")
        except Exception as err:
            self.log(f"[!] Ошибка синхронизации: {err}")
        finally:
            self.is_syncing = False
            if self.tray:
                self.tray.update_icon_state(syncing=False)

    def run_verify(self):
        try:
            if not self.engine:
                self.init_engine()
            if not self.engine:
                self.log("[!] Требуется авторизация для проверки.")
                return

            matched, mismatched, errors = self.engine.verify_integrity()
            self.log("\n--- Результаты самопроверки целостности (MD5) ---")
            self.log(f"[✓] Файлов 100% совпадающих с облаком: {matched}")
            if mismatched > 0:
                self.log(f"[✗] Обнаружено расхождений: {mismatched}")
                for err in errors:
                    self.log(f"    - {err}")
            else:
                self.log("[✓] Все локальные файлы полностью идентичны облачным.")
        except Exception as err:
            self.log(f"[!] Ошибка самопроверки: {err}")

    def on_config_updated(self, new_config: dict):
        self.config = new_config
        # Re-initialize engine with updated paths
        self.init_engine()

    def background_sync_worker(self):
        """Timer loop that triggers sync according to interval in settings."""
        last_sync = 0
        while self.running:
            interval = max(30, self.config.get("sync_interval_seconds", 60))
            now = time.time()
            if now - last_sync >= interval:
                if os.path.exists(TOKEN_FILE):
                    self.run_sync()
                    last_sync = time.time()
            time.sleep(5)

    def start_gui_mode(self):
        from gui import SyncGUI
        from tray import TrayApp

        self.gui = SyncGUI(
            config_path=CONFIG_FILE,
            on_save_config=self.on_config_updated,
            on_run_sync=self.run_sync,
            on_run_verify=self.run_verify,
            on_quit=self.shutdown
        )

        self.tray = TrayApp(
            on_open_gui=self.gui.show_window,
            on_sync_now=self.run_sync,
            on_exit=self.shutdown
        )

        # Launch background timer
        threading.Thread(target=self.background_sync_worker, daemon=True).start()

        # Launch tray icon in separate thread
        threading.Thread(target=self.tray.run, daemon=True).start()

        if self.start_minimized:
            self.gui.hide_window()
        else:
            self.gui.show_window()

        self.gui.run()

    def shutdown(self):
        self.running = False
        if self.tray:
            self.tray.stop()
        sys.exit(0)

def main():
    parser = argparse.ArgumentParser(description="Google Drive Sync Lite (GUI & Tray)")
    parser.add_argument("--minimized", action="store_true", help="Start directly in system tray")
    parser.add_argument("--cli", choices=["login", "sync", "verify"], help="Run in CLI headless mode")

    args = parser.parse_args()

    app = Application(start_minimized=args.minimized)

    if args.cli == "login":
        get_credentials()
    elif args.cli == "sync":
        app.run_sync()
    elif args.cli == "verify":
        app.run_verify()
    else:
        # Default: Modern GUI + System Tray
        app.start_gui_mode()

if __name__ == "__main__":
    main()
