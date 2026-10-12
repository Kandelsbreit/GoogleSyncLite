package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"google.golang.org/api/drive/v3"
)

type FolderItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type App struct {
	ctx              context.Context
	db               *Database
	engine           *SyncEngineGo
	tray             *TrayManager
	watcher          *FileWatcher
	syncMutex        sync.Mutex
	authMutex        sync.Mutex
	pauseMutex       sync.Mutex
	isAuthenticating bool
	isSyncing        bool
	isPaused         bool
	pausedUntil      time.Time
	pauseTimer       *time.Timer
	syncCancel       context.CancelFunc
	lastSyncTime     string
	lastSyncMsg      string
}

func NewApp(db *Database) *App {
	return &App{
		db: db,
	}
}

func (a *App) ensureWatcher() {
	cfg := GetConfig()
	if !cfg.RealtimeWatch || cfg.LocalFolder == "" {
		if a.watcher != nil {
			a.watcher.Stop()
			a.watcher = nil
		}
		return
	}

	cleanFolder := filepath.Clean(cfg.LocalFolder)
	if a.watcher != nil {
		if a.watcher.localRoot == cleanFolder {
			return
		}
		a.watcher.Stop()
		a.watcher = nil
	}

	w, err := NewFileWatcher(cleanFolder, cfg.CustomIgnoreRules, func() {
		if a.IsPaused() {
			return
		}
		if IsAuthenticated() {
			_ = a.TriggerSync()
		}
	}, func(msg string) {
		a.broadcast(msg)
	})
	if err == nil {
		a.watcher = w
	} else {
		a.broadcast(fmt.Sprintf("[!] Не удалось запустить мониторинг файлов: %v", err))
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.ensureWatcher()

	// Start background ticker for periodic sync
	go a.backgroundTicker()
}

func (a *App) beforeClose(ctx context.Context) bool {
	// Instead of terminating the app, minimize/hide to tray so sync stays active
	runtime.WindowHide(ctx)
	return true // prevent window closing
}

func (a *App) shutdown(ctx context.Context) {
	a.syncMutex.Lock()
	if a.isSyncing && a.syncCancel != nil {
		a.syncCancel()
	}
	a.syncMutex.Unlock()

	// Do not close the SQLite handle while a cancelled sync goroutine can still
	// be using it. Network calls honour the context; give them a short grace
	// period before Wails tears the process down.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		a.syncMutex.Lock()
		running := a.isSyncing
		a.syncMutex.Unlock()
		if !running {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}

	if a.watcher != nil {
		a.watcher.Stop()
		a.watcher = nil
	}

	if a.tray != nil {
		a.tray.Stop()
	}
}

func (a *App) SetTray(tm *TrayManager) {
	a.tray = tm
}

func (a *App) broadcast(msg string) {
	fmt.Println(msg)
	if a.tray != nil {
		a.tray.UpdateTooltip("Google Sync Lite: " + msg)
	}
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "log", msg)
	}
}

func (a *App) ensureEngine(ctx context.Context) (*SyncEngineGo, error) {
	a.syncMutex.Lock()
	defer a.syncMutex.Unlock()

	if a.engine != nil {
		return a.engine, nil
	}

	srv, err := GetDriveService(ctx)
	if err != nil {
		return nil, err
	}

	cfg := GetConfig()
	engine := NewSyncEngineGo(srv, a.db, cfg.LocalFolder, cfg.RemoteFolderID, a.broadcast)
	engine.SetConcurrency(cfg.ConcurrentUploads)
	engine.SetBandwidthLimit(cfg.BandwidthLimitKBPS)
	engine.SetIgnoreMatcher(NewIgnoreMatcher(cfg.LocalFolder, cfg.CustomIgnoreRules))
	engine.SetProgressCallback(func(p SyncProgress) {
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, "sync-progress", p)
		}
	})
	a.engine = engine
	return a.engine, nil
}

func (a *App) GetAccountStorageInfo() (*StorageQuotaInfo, error) {
	ctx := context.Background()
	return GetAccountStorageInfo(ctx)
}

func (a *App) GetRecentFiles() ([]RecentFileItem, error) {
	if a.db == nil {
		return []RecentFileItem{}, nil
	}
	return a.db.GetRecentFiles(20)
}

func (a *App) updateSyncResult(timeStr, msg string) {
	a.syncMutex.Lock()
	if timeStr != "" {
		a.lastSyncTime = timeStr
		if a.db != nil {
			_ = a.db.SetMeta("last_sync_display", timeStr)
		}
	}
	if msg != "" {
		a.lastSyncMsg = msg
		if a.db != nil {
			_ = a.db.SetMeta("last_sync_msg", msg)
		}
	}
	a.syncMutex.Unlock()
}

func (a *App) IsPaused() bool {
	a.pauseMutex.Lock()
	defer a.pauseMutex.Unlock()
	if !a.isPaused {
		return false
	}
	if !a.pausedUntil.IsZero() && time.Now().After(a.pausedUntil) {
		a.isPaused = false
		a.pausedUntil = time.Time{}
		return false
	}
	return true
}

func (a *App) PauseSync(durationMinutes int) error {
	a.pauseMutex.Lock()
	a.isPaused = true
	if a.pauseTimer != nil {
		a.pauseTimer.Stop()
		a.pauseTimer = nil
	}

	var msg string
	if durationMinutes > 0 {
		a.pausedUntil = time.Now().Add(time.Duration(durationMinutes) * time.Minute)
		a.pauseTimer = time.AfterFunc(time.Duration(durationMinutes)*time.Minute, func() {
			_ = a.ResumeSync()
		})
		msg = fmt.Sprintf("[*] Синхронизация приостановлена на %d мин. (до %s)", durationMinutes, a.pausedUntil.Format("15:04:05"))
	} else {
		a.pausedUntil = time.Time{}
		msg = "[*] Синхронизация приостановлена вручную до возобновления."
	}
	a.pauseMutex.Unlock()

	_ = a.TriggerStop()

	a.broadcast(msg)
	if a.tray != nil {
		a.tray.UpdateTooltip("Google Sync Lite: " + msg)
	}
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "status-updated")
	}
	return nil
}

func (a *App) ResumeSync() error {
	a.pauseMutex.Lock()
	a.isPaused = false
	a.pausedUntil = time.Time{}
	if a.pauseTimer != nil {
		a.pauseTimer.Stop()
		a.pauseTimer = nil
	}
	a.pauseMutex.Unlock()

	a.broadcast("[+] Синхронизация возобновлена.")
	if a.tray != nil {
		a.tray.UpdateTooltip("Google Sync Lite: Работает")
	}
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "status-updated")
	}
	return nil
}

func (a *App) OpenSyncIgnoreFile() error {
	cfg := GetConfig()
	folder := cfg.LocalFolder
	if folder == "" {
		return fmt.Errorf("локальная папка синхронизации не задана")
	}
	ignoreFile := filepath.Join(folder, ".syncignore")
	if _, err := os.Stat(ignoreFile); os.IsNotExist(err) {
		content := `# Google Sync Lite - .syncignore
# Правила исключения файлов и папок (подобно .gitignore).
# Поддерживаются маски (*, ?) и имена папок со слэшем (node_modules/).
# Строки, начинающиеся с #, игнорируются.

node_modules/
.git/
.vscode/
*.tmp
*.log
~$*
temp/
`
		_ = os.WriteFile(ignoreFile, []byte(content), 0644)
	}
	return exec.Command("notepad.exe", ignoreFile).Start()
}

func (a *App) GetStatus() map[string]interface{} {
	a.syncMutex.Lock()
	lastTime := a.lastSyncTime
	lastMsg := a.lastSyncMsg
	if lastTime == "" && a.db != nil {
		lastTime = a.db.GetMeta("last_sync_display")
	}
	if lastMsg == "" && a.db != nil {
		lastMsg = a.db.GetMeta("last_sync_msg")
	}
	isSyncing := a.isSyncing
	a.syncMutex.Unlock()

	isPaused := a.IsPaused()
	var pauseRemainingSec int64 = 0
	pausedUntilStr := ""
	if isPaused {
		a.pauseMutex.Lock()
		if !a.pausedUntil.IsZero() {
			rem := time.Until(a.pausedUntil)
			if rem > 0 {
				pauseRemainingSec = int64(rem.Seconds())
				pausedUntilStr = a.pausedUntil.Format("15:04")
			}
		}
		a.pauseMutex.Unlock()
	}

	return map[string]interface{}{
		"authenticated":           IsAuthenticated(),
		"config":                  GetConfig(),
		"is_syncing":              isSyncing,
		"is_paused":               isPaused,
		"paused_until":            pausedUntilStr,
		"pause_remaining_seconds": pauseRemainingSec,
		"last_sync_time":          lastTime,
		"last_sync_msg":           lastMsg,
	}
}

func (a *App) TriggerAuth() error {
	a.authMutex.Lock()
	if a.isAuthenticating {
		a.authMutex.Unlock()
		a.broadcast("[*] Авторизация уже запущена в браузере. Завершите вход.")
		return nil
	}
	a.isAuthenticating = true
	a.authMutex.Unlock()

	go func() {
		defer func() {
			a.authMutex.Lock()
			a.isAuthenticating = false
			a.authMutex.Unlock()
		}()

		a.broadcast("[*] Запуск OAuth авторизации...")
		_, err := AuthenticateViaBrowser()
		if err != nil {
			a.broadcast(fmt.Sprintf("[!] Ошибка авторизации: %v", err))
		} else {
			a.broadcast("[+] Авторизация успешна! Подключение к Google Drive установлено.")
			_, _ = a.ensureEngine(context.Background())
			if a.ctx != nil {
				runtime.EventsEmit(a.ctx, "status-updated")
			}
		}
	}()

	return nil
}

func (a *App) TriggerSync() error {
	if a.IsPaused() {
		a.broadcast("[*] Синхронизация сейчас приостановлена (на паузе).")
		return nil
	}

	a.syncMutex.Lock()
	if a.isSyncing {
		a.syncMutex.Unlock()
		a.broadcast("[*] Синхронизация уже выполняется. Подождите окончания...")
		return nil
	}
	a.isSyncing = true
	ctx, cancel := context.WithCancel(context.Background())
	a.syncCancel = cancel
	a.syncMutex.Unlock()

	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "status-updated")
	}

	go func() {
		defer func() {
			a.syncMutex.Lock()
			a.isSyncing = false
			a.syncCancel = nil
			a.syncMutex.Unlock()
			if a.ctx != nil {
				runtime.EventsEmit(a.ctx, "status-updated")
			}
		}()

		engine, err := a.ensureEngine(ctx)
		if err != nil {
			msg := fmt.Sprintf("[!] Ошибка подключения к Google Drive: %v", err)
			a.broadcast(msg)
			a.updateSyncResult("", msg)
			if a.tray != nil {
				a.tray.ShowNotification("Google Sync Lite", "Ошибка подключения к Google Drive", "error")
			}
			return
		}

		cfg := GetConfig()
		a.broadcast("[*] Запуск процедуры синхронизации файлов...")
		summary, err := engine.Sync(ctx, cfg.DryRun, cfg.SyncMode)

		if err != nil {
			if ctx.Err() != nil {
				msg := "[!] Синхронизация остановлена пользователем."
				a.updateSyncResult(time.Now().Format("15:04:05"), msg)
				a.broadcast(msg)
			} else {
				msg := fmt.Sprintf("[!] Ошибка синхронизации: %v", err)
				a.updateSyncResult("", msg)
				a.broadcast(msg)
				if cfg.NotificationsEnabled && a.tray != nil {
					a.tray.ShowNotification("Google Sync Lite", fmt.Sprintf("Ошибка: %v", err), "error")
				}
			}
		} else {
			msg := fmt.Sprintf("[+] Готово! Проверено: %d, Загружено: %d, Скачано: %d, Удалено: %d",
				summary.VerifiedCount, summary.UploadedCount, summary.DownloadCount, summary.DeletedCount)
			a.updateSyncResult(time.Now().Format("15:04:05"), msg)
			a.broadcast(msg)
			if cfg.NotificationsEnabled && a.tray != nil {
				if summary.UploadedCount > 0 || summary.DownloadCount > 0 || summary.DeletedCount > 0 {
					a.tray.ShowNotification(
						"Google Sync Lite",
						fmt.Sprintf("Синхронизация завершена: +%d / -%d / ~%d", summary.UploadedCount, summary.DeletedCount, summary.DownloadCount),
						"info",
					)
				}
			}
		}
	}()

	return nil
}

func (a *App) TriggerStop() error {
	a.syncMutex.Lock()
	if a.isSyncing && a.syncCancel != nil {
		a.broadcast("[*] Отправлен сигнал немедленной остановки...")
		a.syncCancel()
	}
	a.syncMutex.Unlock()
	return nil
}

func (a *App) TriggerVerify() error {
	a.syncMutex.Lock()
	if a.isSyncing {
		a.syncMutex.Unlock()
		a.broadcast("[*] Операция уже выполняется. Дождитесь окончания текущего процесса.")
		return nil
	}
	a.isSyncing = true
	ctx, cancel := context.WithCancel(context.Background())
	a.syncCancel = cancel
	a.syncMutex.Unlock()

	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "status-updated")
	}

	go func() {
		defer func() {
			a.syncMutex.Lock()
			a.isSyncing = false
			a.syncCancel = nil
			a.syncMutex.Unlock()
			if a.ctx != nil {
				runtime.EventsEmit(a.ctx, "status-updated")
			}
		}()

		engine, err := a.ensureEngine(ctx)
		if err != nil {
			a.broadcast(fmt.Sprintf("[!] Ошибка подключения: %v", err))
			return
		}
		a.broadcast("[*] Запуск проверки контрольных сумм (MD5 audit)...")
		matched, mismatched, errs, err := engine.VerifyIntegrity(ctx)
		if err != nil {
			a.broadcast(fmt.Sprintf("[!] Ошибка аудита: %v", err))
			return
		}
		a.broadcast(fmt.Sprintf("[✓] Совпадают с облаком байт-в-байт: %d", matched))
		if mismatched > 0 {
			a.broadcast(fmt.Sprintf("[✗] Расхождений обнаружено: %d", mismatched))
			for _, e := range errs {
				a.broadcast("    - " + e)
			}
		} else {
			a.broadcast("[✓] Все локальные файлы 100% идентичны Google Drive.")
		}
	}()
	return nil
}

func (a *App) TriggerRestore() error {
	a.syncMutex.Lock()
	if a.isSyncing {
		a.syncMutex.Unlock()
		a.broadcast("[*] Процесс синхронизации уже выполняется. Дождитесь окончания.")
		return nil
	}
	a.isSyncing = true
	ctx, cancel := context.WithCancel(context.Background())
	a.syncCancel = cancel
	a.syncMutex.Unlock()

	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "status-updated")
	}

	go func() {
		defer func() {
			a.syncMutex.Lock()
			a.isSyncing = false
			a.syncCancel = nil
			a.syncMutex.Unlock()
			if a.ctx != nil {
				runtime.EventsEmit(a.ctx, "status-updated")
			}
		}()

		a.broadcast("[*] Запуск восстановления удаленных файлов из Корзины Google Диска...")
		srv, err := GetDriveService(ctx)
		if err != nil {
			a.broadcast(fmt.Sprintf("[!] Ошибка подключения к Google Drive: %v", err))
			return
		}
		restored, errs, err := RestoreAllTrashedFiles(ctx, srv, a.broadcast)
		if err != nil {
			a.broadcast(fmt.Sprintf("[!] Ошибка восстановления: %v", err))
			return
		}
		a.broadcast(fmt.Sprintf("[✓] Восстановление из Корзины завершено! Восстановлено: %d, Ошибок: %d", restored, errs))
	}()

	return nil
}

func (a *App) ChooseLocalFolder() (string, error) {
	// Native Windows folder dialog via Wails runtime
	selected, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Выберите папку для синхронизации",
	})
	if err != nil || selected == "" {
		// Fallback to Shell.Application COM BrowseForFolder
		psScript := `$app = New-Object -ComObject Shell.Application; $folder = $app.BrowseForFolder(0, 'Выберите папку для синхронизации', 0, 0); if ($folder) { Write-Output $folder.Self.Path }`
		out, psErr := exec.Command("powershell", "-NoProfile", "-Sta", "-Command", psScript).Output()
		if psErr == nil && strings.TrimSpace(string(out)) != "" {
			return strings.TrimSpace(string(out)), nil
		}
		return "", err
	}
	return selected, nil
}

func (a *App) OpenLocalFolder() error {
	cfg := GetConfig()
	target := cfg.LocalFolder
	if target == "" {
		target = "."
	}
	absTarget, _ := filepath.Abs(target)
	return exec.Command("explorer", absTarget).Start()
}

func (a *App) GetDriveFolders() ([]FolderItem, error) {
	ctx := context.Background()
	srv, err := GetDriveService(ctx)
	if err != nil {
		return nil, fmt.Errorf("требуется авторизация в Google Drive")
	}

	folders := []FolderItem{
		{ID: "root", Name: "Мой Диск (Корень)"},
	}

	pageToken := ""
	for {
		call := srv.Files.List().
			Q("mimeType = 'application/vnd.google-apps.folder' and trashed = false").
			Fields("nextPageToken, files(id, name, parents)").
			PageSize(100).
			Context(ctx)

		if pageToken != "" {
			call = call.PageToken(pageToken)
		}

		res, err := call.Do()
		if err != nil {
			return nil, fmt.Errorf("ошибка получения папок: %v", err)
		}

		for _, f := range res.Files {
			folders = append(folders, FolderItem{
				ID:   f.Id,
				Name: f.Name,
			})
		}

		pageToken = res.NextPageToken
		if pageToken == "" {
			break
		}
	}

	return folders, nil
}

func (a *App) CreateDriveFolder(name, parentID string) (FolderItem, error) {
	ctx := context.Background()
	srv, err := GetDriveService(ctx)
	if err != nil {
		return FolderItem{}, fmt.Errorf("требуется авторизация в Google Drive")
	}

	parent := strings.TrimSpace(parentID)
	if parent == "" {
		parent = "root"
	}

	fMeta := &drive.File{
		Name:     strings.TrimSpace(name),
		MimeType: "application/vnd.google-apps.folder",
		Parents:  []string{parent},
	}

	folder, err := srv.Files.Create(fMeta).Fields("id, name").Context(ctx).Do()
	if err != nil {
		return FolderItem{}, err
	}

	return FolderItem{
		ID:   folder.Id,
		Name: folder.Name,
	}, nil
}

func (a *App) SaveSettings(cfg Config) error {
	if err := SaveConfig(cfg); err != nil {
		return err
	}

	a.syncMutex.Lock()
	a.engine = nil // Reset engine so it picks up updated folders
	a.syncMutex.Unlock()
	a.ensureWatcher()

	a.broadcast("[+] Настройки синхронизации успешно сохранены.")
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "status-updated")
	}
	return nil
}

func (a *App) ToggleAutostart(enabled bool) error {
	if err := SetAutostart(enabled); err != nil {
		return err
	}
	cfg := GetConfig()
	cfg.Autostart = enabled
	_ = SaveConfig(cfg)
	a.broadcast(fmt.Sprintf("[*] Автозапуск Windows: %v", enabled))
	return nil
}

func (a *App) ShowWindow() {
	if a.ctx != nil {
		runtime.WindowShow(a.ctx)
		runtime.WindowUnminimise(a.ctx)
	}
}

func (a *App) MinimizeWindow() {
	if a.ctx != nil {
		runtime.WindowMinimise(a.ctx)
	}
}

func (a *App) HideToTray() {
	if a.ctx != nil {
		runtime.WindowHide(a.ctx)
	}
}

func (a *App) Quit() {
	if a.tray != nil {
		a.tray.Stop()
	}
	if a.ctx != nil {
		runtime.Quit(a.ctx)
	}
}

func (a *App) backgroundTicker() {
	for {
		cfg := GetConfig()
		interval := time.Duration(cfg.SyncIntervalSeconds) * time.Second
		if interval < 30*time.Second {
			interval = 60 * time.Second
		}
		time.Sleep(interval)

		if IsAuthenticated() {
			if a.IsPaused() {
				continue
			}

			a.syncMutex.Lock()
			if a.isSyncing {
				a.syncMutex.Unlock()
				continue
			}
			a.isSyncing = true
			ctx, cancel := context.WithCancel(context.Background())
			a.syncCancel = cancel
			a.syncMutex.Unlock()

			if a.ctx != nil {
				runtime.EventsEmit(a.ctx, "status-updated")
			}

			// Run sync in a goroutine so the defer always fires even on panic.
			done := make(chan struct{})
			go func() {
				defer close(done)
				defer func() {
					a.syncMutex.Lock()
					a.isSyncing = false
					a.syncCancel = nil
					a.syncMutex.Unlock()
					if a.ctx != nil {
						runtime.EventsEmit(a.ctx, "status-updated")
					}
				}()

				if engine, err := a.ensureEngine(ctx); err == nil {
					a.broadcast("[*] Плановая фоновая синхронизация...")
					summary, err := engine.Sync(ctx, cfg.DryRun, cfg.SyncMode)
					if err == nil && summary != nil {
						msg := fmt.Sprintf("[+] Готово! Проверено: %d, Загружено: %d, Скачано: %d, Удалено: %d",
							summary.VerifiedCount, summary.UploadedCount, summary.DownloadCount, summary.DeletedCount)
						a.updateSyncResult(time.Now().Format("15:04:05"), msg)
						a.broadcast(msg)
						if cfg.NotificationsEnabled && a.tray != nil {
							if summary.UploadedCount > 0 || summary.DownloadCount > 0 || summary.DeletedCount > 0 {
								a.tray.ShowNotification(
									"Google Sync Lite",
									fmt.Sprintf("Синхронизация завершена: +%d / -%d / ~%d", summary.UploadedCount, summary.DeletedCount, summary.DownloadCount),
									"info",
								)
							}
						}
					} else if err != nil {
						msg := fmt.Sprintf("[!] Ошибка фоновой синхронизации: %v", err)
						a.updateSyncResult("", msg)
						a.broadcast(msg)
						if cfg.NotificationsEnabled && a.tray != nil {
							a.tray.ShowNotification("Google Sync Lite", fmt.Sprintf("Ошибка фоновой синхронизации: %v", err), "error")
						}
					}
				}
			}()
			<-done
		}
	}
}
