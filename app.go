package main

import (
	"context"
	"fmt"
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
	syncMutex        sync.Mutex
	authMutex        sync.Mutex
	isAuthenticating bool
	isSyncing        bool
	syncCancel       context.CancelFunc
	lastSyncTime     string
	lastSyncMsg      string
}

func NewApp(db *Database) *App {
	return &App{
		db: db,
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

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

func (a *App) ensureEngine(ctx context.Context) error {
	a.syncMutex.Lock()
	defer a.syncMutex.Unlock()

	if a.engine != nil {
		return nil
	}

	srv, err := GetDriveService(ctx)
	if err != nil {
		return err
	}

	cfg := GetConfig()
	a.engine = NewSyncEngineGo(srv, a.db, cfg.LocalFolder, cfg.RemoteFolderID, a.broadcast)
	return nil
}

func (a *App) GetStatus() map[string]interface{} {
	a.syncMutex.Lock()
	defer a.syncMutex.Unlock()

	return map[string]interface{}{
		"authenticated":  IsAuthenticated(),
		"config":         GetConfig(),
		"is_syncing":     a.isSyncing,
		"last_sync_time": a.lastSyncTime,
		"last_sync_msg":  a.lastSyncMsg,
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
			_ = a.ensureEngine(context.Background())
			if a.ctx != nil {
				runtime.EventsEmit(a.ctx, "status-updated")
			}
		}
	}()

	return nil
}

func (a *App) TriggerSync() error {
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

		if err := a.ensureEngine(ctx); err != nil {
			msg := fmt.Sprintf("[!] Ошибка подключения к Google Drive: %v", err)
			a.broadcast(msg)
			a.syncMutex.Lock()
			a.lastSyncMsg = msg
			a.syncMutex.Unlock()
			return
		}

		cfg := GetConfig()
		a.broadcast("[*] Запуск процедуры синхронизации файлов...")
		summary, err := a.engine.Sync(ctx, cfg.DryRun, cfg.SyncMode)

		a.syncMutex.Lock()
		a.lastSyncTime = time.Now().Format("15:04:05")
		if err != nil {
			if ctx.Err() != nil {
				msg := "[!] Синхронизация остановлена пользователем."
				a.lastSyncMsg = msg
				a.syncMutex.Unlock()
				a.broadcast(msg)
			} else {
				msg := fmt.Sprintf("[!] Ошибка синхронизации: %v", err)
				a.lastSyncMsg = msg
				a.syncMutex.Unlock()
				a.broadcast(msg)
			}
		} else {
			msg := fmt.Sprintf("[+] Готово! Проверено: %d, Загружено: %d, Скачано: %d, Удалено: %d",
				summary.VerifiedCount, summary.UploadedCount, summary.DownloadCount, summary.DeletedCount)
			a.lastSyncMsg = msg
			a.syncMutex.Unlock()
			a.broadcast(msg)
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
	go func() {
		ctx := context.Background()
		if err := a.ensureEngine(ctx); err != nil {
			a.broadcast(fmt.Sprintf("[!] Ошибка подключения: %v", err))
			return
		}
		a.broadcast("[*] Запуск проверки контрольных сумм (MD5 audit)...")
		matched, mismatched, errs, err := a.engine.VerifyIntegrity(ctx)
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

	res, err := srv.Files.List().
		Q("mimeType = 'application/vnd.google-apps.folder' and trashed = false").
		Fields("files(id, name, parents)").
		PageSize(100).
		Context(ctx).
		Do()

	if err != nil {
		return nil, fmt.Errorf("ошибка получения папок: %v", err)
	}

	folders := []FolderItem{
		{ID: "root", Name: "Мой Диск (Корень)"},
	}

	for _, f := range res.Files {
		folders = append(folders, FolderItem{
			ID:   f.Id,
			Name: f.Name,
		})
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

			if err := a.ensureEngine(ctx); err == nil {
				a.broadcast("[*] Плановая фоновая синхронизация...")
				summary, err := a.engine.Sync(ctx, cfg.DryRun, cfg.SyncMode)
				a.syncMutex.Lock()
				a.lastSyncTime = time.Now().Format("15:04:05")
				if err == nil && summary != nil {
					msg := fmt.Sprintf("[+] Готово! Проверено: %d, Загружено: %d, Скачано: %d, Удалено: %d",
						summary.VerifiedCount, summary.UploadedCount, summary.DownloadCount, summary.DeletedCount)
					a.lastSyncMsg = msg
					a.broadcast(msg)
				}
				a.syncMutex.Unlock()
			}

			a.syncMutex.Lock()
			a.isSyncing = false
			a.syncCancel = nil
			a.syncMutex.Unlock()

			if a.ctx != nil {
				runtime.EventsEmit(a.ctx, "status-updated")
			}
		}
	}
}
