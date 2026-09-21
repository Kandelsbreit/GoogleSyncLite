import os
import sys
import json
import threading
import tkinter as tk
from tkinter import ttk, filedialog, messagebox
from typing import Callable, Optional

from autostart import is_autostart_enabled, set_autostart
from auth import TOKEN_FILE, CREDENTIALS_FILE, get_credentials

class SyncGUI:
    """Lightweight and modern Tkinter interface for Google Drive Sync."""
    def __init__(self, config_path: str, on_save_config: Callable, on_run_sync: Callable, on_run_verify: Callable, on_quit: Callable):
        self.config_path = config_path
        self.on_save_config = on_save_config
        self.on_run_sync = on_run_sync
        self.on_run_verify = on_run_verify
        self.on_quit = on_quit

        self.root = tk.Tk()
        self.root.title("Google Drive Sync Lite")
        self.root.geometry("620x540")
        self.root.minsize(580, 480)

        # Intercept window close (X) to hide to tray instead of killing
        self.root.protocol("WM_DELETE_WINDOW", self.hide_window)

        self.config = self._load_config()
        self._setup_style()
        self._create_widgets()
        self._refresh_auth_status()

    def _load_config(self) -> dict:
        if os.path.exists(self.config_path):
            try:
                with open(self.config_path, "r", encoding="utf-8") as f:
                    return json.load(f)
            except Exception:
                pass
        return {
            "local_folder": os.path.abspath("./sync_folder"),
            "remote_folder_id": "root",
            "sync_interval_seconds": 60,
            "dry_run": False
        }

    def _setup_style(self):
        style = ttk.Style()
        style.theme_use('clam')
        # Modern color palette
        self.root.configure(bg="#f8f9fa")
        style.configure("TFrame", background="#f8f9fa")
        style.configure("TLabelframe", background="#f8f9fa", font=("Segoe UI", 9, "bold"))
        style.configure("TLabelframe.Label", background="#f8f9fa", foreground="#1a73e8", font=("Segoe UI", 10, "bold"))
        style.configure("TLabel", background="#f8f9fa", font=("Segoe UI", 9))
        style.configure("TButton", font=("Segoe UI", 9), padding=5)
        style.configure("Accent.TButton", font=("Segoe UI", 9, "bold"), foreground="#ffffff", background="#1a73e8")
        style.configure("TCheckbutton", background="#f8f9fa", font=("Segoe UI", 9))

    def _create_widgets(self):
        # Top banner
        header_frame = ttk.Frame(self.root, padding=12)
        header_frame.pack(fill="x")
        title_label = tk.Label(
            header_frame, 
            text="Google Drive Sync Lite", 
            font=("Segoe UI", 14, "bold"), 
            fg="#202124", 
            bg="#f8f9fa"
        )
        title_label.pack(side="left")

        self.status_badge = tk.Label(
            header_frame,
            text="● Проверка статуса...",
            font=("Segoe UI", 9, "bold"),
            fg="#ea4335",
            bg="#f8f9fa"
        )
        self.status_badge.pack(side="right")

        # Main notebook (tabs)
        notebook = ttk.Notebook(self.root)
        notebook.pack(fill="both", expand=True, padx=12, pady=5)

        tab_settings = ttk.Frame(notebook, padding=12)
        tab_log = ttk.Frame(notebook, padding=12)
        notebook.add(tab_settings, text="Настройки и Папки")
        notebook.add(tab_log, text="Журнал синхронизации")

        # --- Tab 1: Settings ---
        # 1. Folder settings
        folder_group = ttk.LabelFrame(tab_settings, text="Папки синхронизации", padding=10)
        folder_group.pack(fill="x", pady=6)

        ttk.Label(folder_group, text="Локальная папка:").grid(row=0, column=0, sticky="w", pady=4)
        self.entry_local_folder = ttk.Entry(folder_group, width=42)
        self.entry_local_folder.insert(0, os.path.abspath(self.config.get("local_folder", "./sync_folder")))
        self.entry_local_folder.grid(row=0, column=1, sticky="ew", padx=6, pady=4)

        btn_browse = ttk.Button(folder_group, text="Обзор...", command=self._browse_folder)
        btn_browse.grid(row=0, column=2, pady=4)

        ttk.Label(folder_group, text="Google Drive Папка ID:").grid(row=1, column=0, sticky="w", pady=4)
        self.entry_remote_id = ttk.Entry(folder_group, width=42)
        self.entry_remote_id.insert(0, self.config.get("remote_folder_id", "root"))
        self.entry_remote_id.grid(row=1, column=1, sticky="ew", padx=6, pady=4)
        ttk.Label(folder_group, text="(или 'root')", foreground="gray").grid(row=1, column=2, sticky="w")

        folder_group.columnconfigure(1, weight=1)

        # 2. Automation & Autostart
        auto_group = ttk.LabelFrame(tab_settings, text="Автоматизация и Windows", padding=10)
        auto_group.pack(fill="x", pady=6)

        self.var_autostart = tk.BooleanVar(value=is_autostart_enabled())
        chk_autostart = ttk.Checkbutton(
            auto_group, 
            text="Запускать автоматически при старте Windows (в системный трей)",
            variable=self.var_autostart,
            command=self._toggle_autostart
        )
        chk_autostart.pack(anchor="w", pady=3)

        interval_frame = ttk.Frame(auto_group)
        interval_frame.pack(fill="x", pady=4)
        ttk.Label(interval_frame, text="Периодичность синхронизации (минут):").pack(side="left")
        self.spin_interval = ttk.Spinbox(interval_frame, from_=1, to=1440, width=6)
        interval_min = max(1, self.config.get("sync_interval_seconds", 60) // 60)
        self.spin_interval.set(interval_min)
        self.spin_interval.pack(side="left", padx=8)

        # 3. Actions & Account
        action_group = ttk.LabelFrame(tab_settings, text="Управление", padding=10)
        action_group.pack(fill="x", pady=6)

        btn_frame = ttk.Frame(action_group)
        btn_frame.pack(fill="x", pady=4)

        self.btn_auth = ttk.Button(btn_frame, text="🔑 Авторизация в Google", command=self._start_auth)
        self.btn_auth.pack(side="left", padx=4)

        self.btn_sync = ttk.Button(btn_frame, text="🔄 Синхронизировать сейчас", style="Accent.TButton", command=self._on_click_sync)
        self.btn_sync.pack(side="left", padx=4)

        self.btn_verify = ttk.Button(btn_frame, text="🛡 Самопроверка (MD5)", command=self._on_click_verify)
        self.btn_verify.pack(side="left", padx=4)

        btn_save = ttk.Button(btn_frame, text="💾 Сохранить настройки", command=self._save_settings)
        btn_save.pack(side="right", padx=4)

        # --- Tab 2: Logs ---
        self.txt_log = tk.Text(tab_log, wrap="word", bg="#ffffff", font=("Consolas", 9), relief="solid", bd=1)
        self.txt_log.pack(fill="both", expand=True, pady=4)
        scroll = ttk.Scrollbar(self.txt_log, command=self.txt_log.yview)
        self.txt_log.configure(yscrollcommand=scroll.set)
        scroll.pack(side="right", fill="y")

        # Bottom control bar
        bottom_frame = ttk.Frame(self.root, padding=8)
        bottom_frame.pack(fill="x", side="bottom")
        btn_hide = ttk.Button(bottom_frame, text="Свернуть в трей", command=self.hide_window)
        btn_hide.pack(side="right", padx=4)
        btn_exit = ttk.Button(bottom_frame, text="Полный выход", command=self._full_exit)
        btn_exit.pack(side="left", padx=4)

    def log(self, message: str):
        """Thread-safe logging to GUI console."""
        def append():
            self.txt_log.insert(tk.END, message + "\n")
            self.txt_log.see(tk.END)
        self.root.after(0, append)

    def _browse_folder(self):
        folder = filedialog.askdirectory(initialdir=self.entry_local_folder.get())
        if folder:
            self.entry_local_folder.delete(0, tk.END)
            self.entry_local_folder.insert(0, os.path.abspath(folder))

    def _refresh_auth_status(self):
        if os.path.exists(TOKEN_FILE):
            self.status_badge.config(text="● Авторизован в Google Drive", fg="#1e8e3e")
            self.btn_auth.config(text="✓ Аккаунт подключен")
        else:
            self.status_badge.config(text="● Требуется авторизация", fg="#ea4335")
            self.btn_auth.config(text="🔑 Войти через браузер")

    def _toggle_autostart(self):
        enabled = self.var_autostart.get()
        success = set_autostart(enabled)
        if success:
            state_str = "включен" if enabled else "отключен"
            self.log(f"[*] Автозапуск Windows {state_str}.")
        else:
            messagebox.showerror("Ошибка", "Не удалось изменить настройки автозапуска Windows.")

    def _save_settings(self):
        try:
            interval_sec = int(self.spin_interval.get()) * 60
        except ValueError:
            interval_sec = 60

        new_config = {
            "local_folder": self.entry_local_folder.get().strip(),
            "remote_folder_id": self.entry_remote_id.get().strip() or "root",
            "sync_interval_seconds": interval_sec,
            "dry_run": False
        }
        with open(self.config_path, "w", encoding="utf-8") as f:
            json.dump(new_config, f, indent=4)
        self.config = new_config
        self.on_save_config(new_config)
        self.log("[+] Настройки успешно сохранены!")
        messagebox.showinfo("Сохранено", "Параметры синхронизации сохранены.")

    def _start_auth(self):
        def worker():
            self.log("[*] Запуск авторизации через браузер...")
            try:
                get_credentials()
                self.log("[+] Авторизация успешно выполнена!")
                self.root.after(0, self._refresh_auth_status)
            except Exception as e:
                self.log(f"[!] Ошибка авторизации: {e}")
                self.root.after(0, lambda: messagebox.showerror("Ошибка входа", str(e)))
        threading.Thread(target=worker, daemon=True).start()

    def _on_click_sync(self):
        threading.Thread(target=self.on_run_sync, daemon=True).start()

    def _on_click_verify(self):
        threading.Thread(target=self.on_run_verify, daemon=True).start()

    def show_window(self):
        self.root.deiconify()
        self.root.lift()
        self.root.focus_force()

    def hide_window(self):
        self.root.withdraw()

    def _full_exit(self):
        self.root.destroy()
        self.on_quit()

    def run(self):
        self.root.mainloop()
