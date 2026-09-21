import os
import sys
import json
import threading
import customtkinter as ctk
from tkinter import filedialog, messagebox

from auth import get_credentials, TOKEN_FILE
from drive_api import DriveClient
from db import StateDB
from syncer import SyncEngine
from autostart import is_autostart_enabled, set_autostart
from tray import TrayApp

CONFIG_FILE = "config.json"

DEFAULT_CONFIG = {
    "local_folder": os.path.abspath("./sync_folder"),
    "remote_folder_id": "root",
    "sync_interval_seconds": 60,
    "autostart": False,
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

def save_config(cfg: dict):
    with open(CONFIG_FILE, "w", encoding="utf-8") as f:
        json.dump(cfg, f, indent=4)

class ModernGraphiteApp(ctk.CTk):
    def __init__(self, start_minimized: bool = False):
        super().__init__()

        # Appearance configuration: Graphite dark theme matching user screenshot
        ctk.set_appearance_mode("dark")
        ctk.set_default_color_theme("dark-blue")

        self.title("Google Sync Lite")
        self.geometry("860x580")
        self.minsize(760, 480)
        self.configure(fg_color="#121316")  # Deep graphite background

        self.config = load_config()
        self.is_syncing = False
        self.engine = None
        self.tray = None

        # Intercept close button: minimize to tray
        self.protocol("WM_DELETE_WINDOW", self.hide_to_tray)

        self._setup_ui()
        self._init_tray()
        self._refresh_status()

        # Background sync loop
        threading.Thread(target=self._background_sync_worker, daemon=True).start()

        if start_minimized:
            self.withdraw()

    def _setup_ui(self):
        # Grid configuration: Left sidebar (Navigation), Right main panel
        self.grid_columnconfigure(1, weight=1)
        self.grid_rowconfigure(0, weight=1)

        # === 1. SIDEBAR (Graphite: #17181c) ===
        self.sidebar_frame = ctk.CTkFrame(self, width=200, corner_radius=0, fg_color="#17181c", border_width=1, border_color="#2b2e37")
        self.sidebar_frame.grid(row=0, column=0, sticky="nsew")
        self.sidebar_frame.grid_rowconfigure(5, weight=1)

        # Brand header
        self.logo_label = ctk.CTkLabel(
            self.sidebar_frame, 
            text=" Google Sync", 
            font=ctk.CTkFont(size=16, weight="bold"),
            text_color="#e6e8ec"
        )
        self.logo_label.grid(row=0, column=0, padx=20, pady=(20, 20), sticky="w")

        # Nav Buttons
        self.btn_nav_dash = ctk.CTkButton(
            self.sidebar_frame, text="  Обзор", anchor="w",
            fg_color="#24262c", hover_color="#2b2e37", text_color="#e6e8ec",
            height=36, corner_radius=6, command=lambda: self._select_frame("dash")
        )
        self.btn_nav_dash.grid(row=1, column=0, padx=12, pady=4, sticky="ew")

        self.btn_nav_settings = ctk.CTkButton(
            self.sidebar_frame, text="  Настройки", anchor="w",
            fg_color="transparent", hover_color="#24262c", text_color="#9da3af",
            height=36, corner_radius=6, command=lambda: self._select_frame("settings")
        )
        self.btn_nav_settings.grid(row=2, column=0, padx=12, pady=4, sticky="ew")

        self.btn_nav_logs = ctk.CTkButton(
            self.sidebar_frame, text="  Журнал", anchor="w",
            fg_color="transparent", hover_color="#24262c", text_color="#9da3af",
            height=36, corner_radius=6, command=lambda: self._select_frame("logs")
        )
        self.btn_nav_logs.grid(row=3, column=0, padx=12, pady=4, sticky="ew")

        # Sidebar footer status
        self.status_label = ctk.CTkLabel(
            self.sidebar_frame, 
            text="● Не авторизован", 
            font=ctk.CTkFont(size=11, weight="bold"),
            text_color="#f87171"
        )
        self.status_label.grid(row=6, column=0, padx=16, pady=(10, 4), sticky="w")

        self.btn_tray_hide = ctk.CTkButton(
            self.sidebar_frame, text="Свернуть в трей", height=28,
            fg_color="#1d1f24", hover_color="#2b2e37", text_color="#9da3af",
            border_width=1, border_color="#2b2e37", command=self.hide_to_tray
        )
        self.btn_tray_hide.grid(row=7, column=0, padx=12, pady=(4, 16), sticky="ew")

        # === 2. MAIN CONTENT AREA ===
        self.main_container = ctk.CTkFrame(self, fg_color="#121316", corner_radius=0)
        self.main_container.grid(row=0, column=1, sticky="nsew", padx=24, pady=20)
        self.main_container.grid_columnconfigure(0, weight=1)
        self.main_container.grid_rowconfigure(1, weight=1)

        # Frames for tabs
        self.frame_dash = ctk.CTkFrame(self.main_container, fg_color="transparent")
        self.frame_settings = ctk.CTkFrame(self.main_container, fg_color="transparent")
        self.frame_logs = ctk.CTkFrame(self.main_container, fg_color="transparent")

        self._build_dash_view()
        self._build_settings_view()
        self._build_logs_view()

        self._select_frame("dash")

    def _build_dash_view(self):
        title = ctk.CTkLabel(self.frame_dash, text="Состояние синхронизации", font=ctk.CTkFont(size=20, weight="bold"), text_color="#ffffff")
        title.pack(anchor="w", pady=(0, 16))

        # Action cards
        card_actions = ctk.CTkFrame(self.frame_dash, fg_color="#1d1f24", border_width=1, border_color="#2b2e37", corner_radius=8)
        card_actions.pack(fill="x", pady=(0, 16), padx=2)

        lbl_actions = ctk.CTkLabel(card_actions, text="Быстрые действия", font=ctk.CTkFont(size=13, weight="bold"), text_color="#e6e8ec")
        lbl_actions.pack(anchor="w", padx=16, pady=(12, 10))

        btn_box = ctk.CTkFrame(card_actions, fg_color="transparent")
        btn_box.pack(fill="x", padx=16, pady=(0, 14))

        self.btn_sync = ctk.CTkButton(
            btn_box, text="🔄  Синхронизировать", height=38,
            fg_color="#2b323f", hover_color="#374052", border_width=1, border_color="#4e5769",
            command=self._async_sync
        )
        self.btn_sync.pack(side="left", padx=(0, 10))

        self.btn_verify = ctk.CTkButton(
            btn_box, text="🛡  Самопроверка (MD5)", height=38,
            fg_color="#1d1f24", hover_color="#2b2e37", border_width=1, border_color="#2b2e37",
            command=self._async_verify
        )
        self.btn_verify.pack(side="left", padx=(0, 10))

        self.btn_auth = ctk.CTkButton(
            btn_box, text="🔑  Войти через Google", height=38,
            fg_color="#1d1f24", hover_color="#2b2e37", border_width=1, border_color="#2b2e37",
            command=self._async_auth
        )
        self.btn_auth.pack(side="left")

        # Paths overview card
        card_paths = ctk.CTkFrame(self.frame_dash, fg_color="#1d1f24", border_width=1, border_color="#2b2e37", corner_radius=8)
        card_paths.pack(fill="x", pady=(0, 16), padx=2)

        lbl_paths = ctk.CTkLabel(card_paths, text="Текущие папки", font=ctk.CTkFont(size=13, weight="bold"), text_color="#e6e8ec")
        lbl_paths.pack(anchor="w", padx=16, pady=(12, 8))

        self.lbl_local_display = ctk.CTkLabel(card_paths, text=f"Локальная папка: {self.config.get('local_folder')}", text_color="#9da3af", font=ctk.CTkFont(size=12))
        self.lbl_local_display.pack(anchor="w", padx=16, pady=2)

        self.lbl_remote_display = ctk.CTkLabel(card_paths, text=f"Google Drive ID: {self.config.get('remote_folder_id')}", text_color="#9da3af", font=ctk.CTkFont(size=12))
        self.lbl_remote_display.pack(anchor="w", padx=16, pady=(2, 14))

    def _build_settings_view(self):
        title = ctk.CTkLabel(self.frame_settings, text="Настройки программы", font=ctk.CTkFont(size=20, weight="bold"), text_color="#ffffff")
        title.pack(anchor="w", pady=(0, 16))

        card_folder = ctk.CTkFrame(self.frame_settings, fg_color="#1d1f24", border_width=1, border_color="#2b2e37", corner_radius=8)
        card_folder.pack(fill="x", pady=(0, 16), padx=2)

        ctk.CTkLabel(card_folder, text="Локальная папка на диске", font=ctk.CTkFont(size=12, weight="bold"), text_color="#e6e8ec").pack(anchor="w", padx=16, pady=(12, 4))
        
        row_local = ctk.CTkFrame(card_folder, fg_color="transparent")
        row_local.pack(fill="x", padx=16, pady=(0, 10))

        self.entry_local = ctk.CTkEntry(row_local, fg_color="#15161a", border_color="#2b2e37", text_color="#e6e8ec", height=36)
        self.entry_local.insert(0, self.config.get("local_folder", ""))
        self.entry_local.pack(side="left", fill="x", expand=True, padx=(0, 10))

        btn_browse = ctk.CTkButton(row_local, text="Обзор...", width=90, height=36, fg_color="#24262c", hover_color="#2b2e37", command=self._browse_folder)
        btn_browse.pack(side="right")

        ctk.CTkLabel(card_folder, text="Google Drive Folder ID (или 'root' для корня)", font=ctk.CTkFont(size=12, weight="bold"), text_color="#e6e8ec").pack(anchor="w", padx=16, pady=(4, 4))
        self.entry_remote = ctk.CTkEntry(card_folder, fg_color="#15161a", border_color="#2b2e37", text_color="#e6e8ec", height=36)
        self.entry_remote.insert(0, self.config.get("remote_folder_id", "root"))
        self.entry_remote.pack(fill="x", padx=16, pady=(0, 14))

        # Automation card
        card_auto = ctk.CTkFrame(self.frame_settings, fg_color="#1d1f24", border_width=1, border_color="#2b2e37", corner_radius=8)
        card_auto.pack(fill="x", pady=(0, 16), padx=2)

        self.switch_autostart = ctk.CTkSwitch(
            card_auto, text="Запускать при старте Windows (тихо в трей)",
            font=ctk.CTkFont(size=13), text_color="#e6e8ec", progress_color="#4e5769",
            command=self._on_toggle_autostart
        )
        if is_autostart_enabled():
            self.switch_autostart.select()
        else:
            self.switch_autostart.deselect()
        self.switch_autostart.pack(anchor="w", padx=16, pady=(16, 12))

        row_interval = ctk.CTkFrame(card_auto, fg_color="transparent")
        row_interval.pack(fill="x", padx=16, pady=(0, 14))
        ctk.CTkLabel(row_interval, text="Периодичность синхронизации (минут):", text_color="#9da3af").pack(side="left", padx=(0, 10))
        self.entry_interval = ctk.CTkEntry(row_interval, width=70, fg_color="#15161a", border_color="#2b2e37", text_color="#e6e8ec")
        interval_min = max(1, self.config.get("sync_interval_seconds", 60) // 60)
        self.entry_interval.insert(0, str(interval_min))
        self.entry_interval.pack(side="left")

        btn_save = ctk.CTkButton(
            self.frame_settings, text="💾 Сохранить параметры", height=38,
            fg_color="#2b323f", hover_color="#374052", border_width=1, border_color="#4e5769",
            command=self._save_settings
        )
        btn_save.pack(anchor="w", pady=(0, 10))

    def _build_logs_view(self):
        title = ctk.CTkLabel(self.frame_logs, text="Журнал событий", font=ctk.CTkFont(size=20, weight="bold"), text_color="#ffffff")
        title.pack(anchor="w", pady=(0, 12))

        self.log_textbox = ctk.CTkTextbox(
            self.frame_logs, fg_color="#15161a", border_width=1, border_color="#2b2e37",
            text_color="#9da3af", font=ctk.CTkFont(family="Consolas", size=11), corner_radius=8
        )
        self.log_textbox.pack(fill="both", expand=True)

    def _select_frame(self, name: str):
        self.btn_nav_dash.configure(fg_color="#24262c" if name == "dash" else "transparent", text_color="#ffffff" if name == "dash" else "#9da3af")
        self.btn_nav_settings.configure(fg_color="#24262c" if name == "settings" else "transparent", text_color="#ffffff" if name == "settings" else "#9da3af")
        self.btn_nav_logs.configure(fg_color="#24262c" if name == "logs" else "transparent", text_color="#ffffff" if name == "logs" else "#9da3af")

        self.frame_dash.pack_forget()
        self.frame_settings.pack_forget()
        self.frame_logs.pack_forget()

        if name == "dash":
            self.frame_dash.pack(fill="both", expand=True)
        elif name == "settings":
            self.frame_settings.pack(fill="both", expand=True)
        elif name == "logs":
            self.frame_logs.pack(fill="both", expand=True)

    def log(self, message: str):
        print(message)
        self.after(0, lambda: self._append_log(message))

    def _append_log(self, msg: str):
        self.log_textbox.insert("end", msg + "\n")
        self.log_textbox.see("end")

    def _browse_folder(self):
        folder = filedialog.askdirectory(initialdir=self.entry_local.get())
        if folder:
            self.entry_local.delete(0, "end")
            self.entry_local.insert(0, os.path.abspath(folder))

    def _on_toggle_autostart(self):
        enable = bool(self.switch_autostart.get())
        set_autostart(enable)
        self.config["autostart"] = enable
        save_config(self.config)
        self.log(f"[*] Автозапуск Windows: {'ВКЛЮЧЕН' if enable else 'ОТКЛЮЧЕН'}")

    def _save_settings(self):
        try:
            val = int(self.entry_interval.get())
            sec = max(30, val * 60)
        except ValueError:
            sec = 60

        self.config["local_folder"] = self.entry_local.get().strip()
        self.config["remote_folder_id"] = self.entry_remote.get().strip() or "root"
        self.config["sync_interval_seconds"] = sec
        save_config(self.config)

        self.lbl_local_display.configure(text=f"Локальная папка: {self.config['local_folder']}")
        self.lbl_remote_display.configure(text=f"Google Drive ID: {self.config['remote_folder_id']}")

        self.log("[+] Настройки успешно сохранены!")
        messagebox.showinfo("Сохранено", "Параметры сохранены!")

    def _refresh_status(self):
        if os.path.exists(TOKEN_FILE):
            self.status_label.configure(text="● Подключено к Drive", text_color="#4ade80")
            self.btn_auth.configure(text="✓ Аккаунт подключен")
        else:
            self.status_label.configure(text="● Не авторизован", text_color="#f87171")
            self.btn_auth.configure(text="🔑 Войти через Google")

    def _async_auth(self):
        def worker():
            self.log("[*] Запуск OAuth авторизации...")
            try:
                get_credentials()
                self.log("[+] Авторизация успешно выполнена!")
                self.after(0, self._refresh_status)
            except Exception as e:
                self.log(f"[!] Ошибка авторизации: {e}")
                self.after(0, lambda: messagebox.showerror("Ошибка", str(e)))
        threading.Thread(target=worker, daemon=True).start()

    def _init_engine(self):
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
            self.log(f"[!] Ошибка инициализации: {e}")
            return None

    def _async_sync(self):
        if self.is_syncing:
            self.log("[*] Синхронизация уже идет...")
            return
        def worker():
            self.is_syncing = True
            if self.tray:
                self.tray.update_icon_state(syncing=True)
            try:
                engine = self._init_engine()
                if not engine:
                    self.log("[!] Требуется авторизация перед синхронизацией.")
                    return
                self.log("[*] Начало синхронизации файлов...")
                engine.sync(dry_run=self.config.get("dry_run", False))
                self.log("[+] Синхронизация успешно завершена.")
                if self.tray:
                    self.tray.notify("Google Sync Lite", "Синхронизация завершена успешно.")
            except Exception as e:
                self.log(f"[!] Ошибка в процессе синхронизации: {e}")
            finally:
                self.is_syncing = False
                if self.tray:
                    self.tray.update_icon_state(syncing=False)
        threading.Thread(target=worker, daemon=True).start()

    def _async_verify(self):
        def worker():
            try:
                engine = self._init_engine()
                if not engine:
                    self.log("[!] Требуется авторизация для проверки.")
                    return
                matched, mismatched, errors = engine.verify_integrity()
                self.log("\n--- Результаты самопроверки целостности (MD5) ---")
                self.log(f"[✓] Файлов 100% совпадающих с облаком: {matched}")
                if mismatched > 0:
                    self.log(f"[✗] Расхождений обнаружено: {mismatched}")
                    for err in errors:
                        self.log(f"    - {err}")
                else:
                    self.log("[✓] Все локальные файлы полностью идентичны облачным.")
            except Exception as e:
                self.log(f"[!] Ошибка самопроверки: {e}")
        threading.Thread(target=worker, daemon=True).start()

    def _background_sync_worker(self):
        import time
        while True:
            interval = max(30, self.config.get("sync_interval_seconds", 60))
            time.sleep(interval)
            if os.path.exists(TOKEN_FILE) and not self.is_syncing:
                self._async_sync()

    def _init_tray(self):
        self.tray = TrayApp(
            on_open_gui=self.show_from_tray,
            on_sync_now=self._async_sync,
            on_exit=self.full_quit
        )
        threading.Thread(target=self.tray.run, daemon=True).start()

    def show_from_tray(self):
        self.after(0, lambda: (self.deiconify(), self.lift(), self.focus_force()))

    def hide_to_tray(self):
        self.withdraw()

    def full_quit(self):
        if self.tray:
            self.tray.stop()
        self.destroy()
        sys.exit(0)

if __name__ == "__main__":
    minimized = "--minimized" in sys.argv
    app = ModernGraphiteApp(start_minimized=minimized)
    app.mainloop()
