package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"google.golang.org/api/drive/v3"
)

//go:embed web/*
var webFS embed.FS

type Server struct {
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
	subscribers      map[chan string]bool
	subMutex         sync.Mutex
}

func (s *Server) broadcast(msg string) {
	fmt.Println(msg)
	if s.tray != nil {
		s.tray.UpdateTooltip("Google Sync Lite: " + msg)
	}
	s.subMutex.Lock()
	defer s.subMutex.Unlock()
	for ch := range s.subscribers {
		select {
		case ch <- msg:
		default:
		}
	}
}

func (s *Server) ensureEngine(ctx context.Context) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	if s.engine != nil {
		return nil
	}

	srv, err := GetDriveService(ctx)
	if err != nil {
		return err
	}

	cfg := GetConfig()
	s.engine = NewSyncEngineGo(srv, s.db, cfg.LocalFolder, cfg.RemoteFolderID, s.broadcast)
	return nil
}

func main() {
	minimized := flag.Bool("minimized", false, "Start minimized in tray")
	flag.Parse()

	cfg := LoadConfig()
	cfg.Autostart = IsAutostartEnabled()
	_ = SaveConfig(cfg)

	db, err := OpenDatabase("sync_state_go.db")
	if err != nil {
		fmt.Printf("[!] DB Error: %v\n", err)
		return
	}
	defer db.Close()

	srvApp := &Server{
		db:          db,
		subscribers: make(map[chan string]bool),
	}

	// Setup embedded static web handler
	subFS, _ := fs.Sub(webFS, "web")
	http.Handle("/", http.FileServer(http.FS(subFS)))

	// SSE stream for real-time live logs
	http.HandleFunc("/api/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		ch := make(chan string, 10)
		srvApp.subMutex.Lock()
		srvApp.subscribers[ch] = true
		srvApp.subMutex.Unlock()

		defer func() {
			srvApp.subMutex.Lock()
			delete(srvApp.subscribers, ch)
			srvApp.subMutex.Unlock()
		}()

		notify := r.Context().Done()
		for {
			select {
			case <-notify:
				return
			case msg := <-ch:
				fmt.Fprintf(w, "data: %s\n\n", msg)
				w.(http.Flusher).Flush()
			}
		}
	})

	// Status API
	http.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		srvApp.syncMutex.Lock()
		syncing := srvApp.isSyncing
		lastTime := srvApp.lastSyncTime
		lastMsg := srvApp.lastSyncMsg
		srvApp.syncMutex.Unlock()

		json.NewEncoder(w).Encode(map[string]interface{}{
			"authenticated":  IsAuthenticated(),
			"config":         GetConfig(),
			"is_syncing":     syncing,
			"last_sync_time": lastTime,
			"last_sync_msg":  lastMsg,
		})
	})

	// Auth API
	http.HandleFunc("/api/auth", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			return
		}
		srvApp.authMutex.Lock()
		if srvApp.isAuthenticating {
			srvApp.authMutex.Unlock()
			srvApp.broadcast("[*] Авторизация уже запущена в браузере. Завершите вход.")
			w.WriteHeader(http.StatusOK)
			return
		}
		srvApp.isAuthenticating = true
		srvApp.authMutex.Unlock()

		go func() {
			defer func() {
				srvApp.authMutex.Lock()
				srvApp.isAuthenticating = false
				srvApp.authMutex.Unlock()
			}()

			srvApp.broadcast("[*] Запуск OAuth авторизации...")
			_, err := AuthenticateViaBrowser()
			if err != nil {
				srvApp.broadcast(fmt.Sprintf("[!] Ошибка авторизации: %v", err))
			} else {
				srvApp.broadcast("[+] Авторизация успешна! Подключение к Google Drive установлено.")
				_ = srvApp.ensureEngine(context.Background())
			}
		}()
		w.WriteHeader(http.StatusOK)
	})

	// Trigger Sync
	http.HandleFunc("/api/sync", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			return
		}
		srvApp.syncMutex.Lock()
		if srvApp.isSyncing {
			srvApp.syncMutex.Unlock()
			srvApp.broadcast("[*] Синхронизация уже выполняется. Подождите окончания...")
			w.WriteHeader(http.StatusOK)
			return
		}
		srvApp.isSyncing = true
		ctx, cancel := context.WithCancel(context.Background())
		srvApp.syncCancel = cancel
		srvApp.syncMutex.Unlock()

		go func() {
			defer func() {
				srvApp.syncMutex.Lock()
				srvApp.isSyncing = false
				srvApp.syncCancel = nil
				srvApp.syncMutex.Unlock()
			}()

			if err := srvApp.ensureEngine(ctx); err != nil {
				msg := fmt.Sprintf("[!] Ошибка подключения к Google Drive: %v", err)
				srvApp.broadcast(msg)
				srvApp.syncMutex.Lock()
				srvApp.lastSyncMsg = msg
				srvApp.syncMutex.Unlock()
				return
			}
			cfg := GetConfig()
			srvApp.broadcast("[*] Запуск процедуры синхронизации файлов...")
			summary, err := srvApp.engine.Sync(ctx, cfg.DryRun, cfg.SyncMode)
			srvApp.syncMutex.Lock()
			srvApp.lastSyncTime = time.Now().Format("15:04:05")
			if err != nil {
				if ctx.Err() != nil {
					msg := "[!] Синхронизация остановлена пользователем."
					srvApp.lastSyncMsg = msg
					srvApp.syncMutex.Unlock()
					srvApp.broadcast(msg)
				} else {
					msg := fmt.Sprintf("[!] Ошибка синхронизации: %v", err)
					srvApp.lastSyncMsg = msg
					srvApp.syncMutex.Unlock()
					srvApp.broadcast(msg)
				}
			} else {
				msg := fmt.Sprintf("[+] Готово! Проверено: %d, Загружено: %d, Скачано: %d, Удалено: %d",
					summary.VerifiedCount, summary.UploadedCount, summary.DownloadCount, summary.DeletedCount)
				srvApp.lastSyncMsg = msg
				srvApp.syncMutex.Unlock()
				srvApp.broadcast(msg)
			}
		}()
		w.WriteHeader(http.StatusOK)
	})

	// Stop Sync
	http.HandleFunc("/api/sync/stop", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			return
		}
		srvApp.syncMutex.Lock()
		if srvApp.isSyncing && srvApp.syncCancel != nil {
			srvApp.broadcast("[*] Отправлен сигнал немедленной остановки...")
			srvApp.syncCancel()
		}
		srvApp.syncMutex.Unlock()
		w.WriteHeader(http.StatusOK)
	})

	// Trigger Verify
	http.HandleFunc("/api/verify", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			return
		}
		go func() {
			ctx := context.Background()
			if err := srvApp.ensureEngine(ctx); err != nil {
				srvApp.broadcast(fmt.Sprintf("[!] Ошибка подключения: %v", err))
				return
			}
			srvApp.broadcast("[*] Запуск проверки контрольных сумм (MD5 audit)...")
			matched, mismatched, errs, err := srvApp.engine.VerifyIntegrity(ctx)
			if err != nil {
				srvApp.broadcast(fmt.Sprintf("[!] Ошибка аудита: %v", err))
				return
			}
			srvApp.broadcast(fmt.Sprintf("[✓] Совпадают с облаком байт-в-байт: %d", matched))
			if mismatched > 0 {
				srvApp.broadcast(fmt.Sprintf("[✗] Расхождений обнаружено: %d", mismatched))
				for _, e := range errs {
					srvApp.broadcast("    - " + e)
				}
			} else {
				srvApp.broadcast("[✓] Все локальные файлы 100% идентичны Google Drive.")
			}
		}()
		w.WriteHeader(http.StatusOK)
	})

	// Autostart toggle API
	http.HandleFunc("/api/autostart", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Autostart bool `json:"autostart"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		_ = SetAutostart(req.Autostart)
		cfg := GetConfig()
		cfg.Autostart = req.Autostart
		_ = SaveConfig(cfg)
		w.WriteHeader(http.StatusOK)
	})

	// Choose local folder dialog (Native Windows Shell BrowseForFolder)
	http.HandleFunc("/api/choose-local-folder", func(w http.ResponseWriter, r *http.Request) {
		psScript := `$app = New-Object -ComObject Shell.Application; $folder = $app.BrowseForFolder(0, 'Выберите папку для синхронизации', 0, 0); if ($folder) { Write-Output $folder.Self.Path }`
		out, err := exec.Command("powershell", "-NoProfile", "-Sta", "-Command", psScript).Output()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		selected := strings.TrimSpace(string(out))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"folder": selected,
		})
	})

	// Open local folder in Windows Explorer
	http.HandleFunc("/api/open-local-folder", func(w http.ResponseWriter, r *http.Request) {
		cfg := GetConfig()
		target := cfg.LocalFolder
		if target == "" {
			target = "."
		}
		absTarget, _ := filepath.Abs(target)
		_ = exec.Command("explorer", absTarget).Start()
		w.WriteHeader(http.StatusOK)
	})

	// List folders in Google Drive for folder picker modal
	http.HandleFunc("/api/drive-folders", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		ctx := context.Background()
		srv, err := GetDriveService(ctx)
		if err != nil {
			http.Error(w, "Требуется авторизация", http.StatusUnauthorized)
			return
		}

		// Query all non-trashed folders
		res, err := srv.Files.List().
			Q("mimeType = 'application/vnd.google-apps.folder' and trashed = false").
			Fields("files(id, name, parents)").
			PageSize(100).
			Context(ctx).
			Do()

		if err != nil {
			http.Error(w, fmt.Sprintf("Ошибка получения папок: %v", err), 500)
			return
		}

		type FolderItem struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}

		folders := []FolderItem{
			{ID: "root", Name: "📁 Мой Диск (Корень)"},
		}

		for _, f := range res.Files {
			folders = append(folders, FolderItem{
				ID:   f.Id,
				Name: f.Name,
			})
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"folders": folders,
		})
	})

	// Create new folder in Google Drive
	http.HandleFunc("/api/drive-create-folder", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			return
		}
		var req struct {
			Name     string `json:"name"`
			ParentID string `json:"parent_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
			http.Error(w, "Некорректное имя папки", 400)
			return
		}

		ctx := context.Background()
		srv, err := GetDriveService(ctx)
		if err != nil {
			http.Error(w, "Не авторизован", http.StatusUnauthorized)
			return
		}

		parent := req.ParentID
		if parent == "" {
			parent = "root"
		}

		fMeta := &drive.File{
			Name:     strings.TrimSpace(req.Name),
			MimeType: "application/vnd.google-apps.folder",
			Parents:  []string{parent},
		}

		folder, err := srv.Files.Create(fMeta).Fields("id, name").Context(ctx).Do()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"id":   folder.Id,
			"name": folder.Name,
		})
	})

	// Save Config API
	http.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		var newCfg Config
		if err := json.NewDecoder(r.Body).Decode(&newCfg); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		_ = SaveConfig(newCfg)
		// Reset engine to reload paths
		srvApp.syncMutex.Lock()
		srvApp.engine = nil
		srvApp.syncMutex.Unlock()
		w.WriteHeader(http.StatusOK)
	})

	// Background ticker
	go func() {
		for {
			cfg := GetConfig()
			interval := time.Duration(cfg.SyncIntervalSeconds) * time.Second
			if interval < 30*time.Second {
				interval = 60 * time.Second
			}
			time.Sleep(interval)

			if IsAuthenticated() {
				srvApp.syncMutex.Lock()
				if srvApp.isSyncing {
					srvApp.syncMutex.Unlock()
					continue
				}
				srvApp.isSyncing = true
				ctx, cancel := context.WithCancel(context.Background())
				srvApp.syncCancel = cancel
				srvApp.syncMutex.Unlock()

				if err := srvApp.ensureEngine(ctx); err == nil {
					srvApp.broadcast("[*] Плановая фоновая синхронизация...")
					summary, err := srvApp.engine.Sync(ctx, cfg.DryRun, cfg.SyncMode)
					srvApp.syncMutex.Lock()
					srvApp.lastSyncTime = time.Now().Format("15:04:05")
					if err == nil && summary != nil {
						msg := fmt.Sprintf("[+] Готово! Проверено: %d, Загружено: %d, Скачано: %d, Удалено: %d",
							summary.VerifiedCount, summary.UploadedCount, summary.DownloadCount, summary.DeletedCount)
						srvApp.lastSyncMsg = msg
						srvApp.broadcast(msg)
					}
					srvApp.syncMutex.Unlock()
				}

				srvApp.syncMutex.Lock()
				srvApp.isSyncing = false
				srvApp.syncCancel = nil
				srvApp.syncMutex.Unlock()
			}
		}
	}()

	port := 8765
	url := fmt.Sprintf("http://localhost:%d", port)

	openWindow := func() {
		chromePaths := []string{
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
		}
		var appCmd *exec.Cmd
		for _, p := range chromePaths {
			if _, err := os.Stat(p); err == nil {
				appCmd = exec.Command(p, fmt.Sprintf("--app=%s", url), "--window-size=920,680")
				break
			}
		}

		if appCmd != nil {
			if err := appCmd.Start(); err != nil {
				_ = OpenBrowser(url)
			}
		} else {
			_ = OpenBrowser(url)
		}
	}

	triggerManualSync := func() {
		go func() {
			srvApp.syncMutex.Lock()
			if srvApp.isSyncing {
				srvApp.syncMutex.Unlock()
				srvApp.broadcast("[*] Синхронизация уже выполняется...")
				return
			}
			srvApp.isSyncing = true
			ctx, cancel := context.WithCancel(context.Background())
			srvApp.syncCancel = cancel
			srvApp.syncMutex.Unlock()

			if err := srvApp.ensureEngine(ctx); err == nil {
				srvApp.broadcast("[*] Запуск синхронизации из трея...")
				cfg := GetConfig()
				summary, err := srvApp.engine.Sync(ctx, cfg.DryRun, cfg.SyncMode)
				srvApp.syncMutex.Lock()
				srvApp.lastSyncTime = time.Now().Format("15:04:05")
				if err == nil && summary != nil {
					msg := fmt.Sprintf("[+] Готово! Проверено: %d, Загружено: %d, Скачано: %d, Удалено: %d",
						summary.VerifiedCount, summary.UploadedCount, summary.DownloadCount, summary.DeletedCount)
					srvApp.lastSyncMsg = msg
					srvApp.broadcast(msg)
				}
				srvApp.syncMutex.Unlock()
			}
			srvApp.syncMutex.Lock()
			srvApp.isSyncing = false
			srvApp.syncCancel = nil
			srvApp.syncMutex.Unlock()
		}()
	}

	stopSync := func() {
		srvApp.syncMutex.Lock()
		if srvApp.isSyncing && srvApp.syncCancel != nil {
			srvApp.broadcast("[*] Отправлен сигнал остановки из системного трея...")
			srvApp.syncCancel()
		}
		srvApp.syncMutex.Unlock()
	}

	quitApp := func() {
		srvApp.syncMutex.Lock()
		if srvApp.isSyncing && srvApp.syncCancel != nil {
			srvApp.syncCancel()
		}
		srvApp.syncMutex.Unlock()
		if srvApp.tray != nil {
			srvApp.tray.Stop()
		}
		os.Exit(0)
	}

	// Initialize native system tray
	tray, err := StartTray(openWindow, triggerManualSync, stopSync, quitApp)
	if err == nil {
		srvApp.tray = tray
		defer tray.Stop()
	}

	// Launch window using Chrome/Edge app-mode or default browser
	if !*minimized {
		go func() {
			time.Sleep(300 * time.Millisecond)
			openWindow()
		}()
	}

	fmt.Printf("[+] Google Sync Lite (Go) запущен на %s\n", url)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", port), nil); err != nil {
		fmt.Printf("[!] Server error: %v\n", err)
	}
}
