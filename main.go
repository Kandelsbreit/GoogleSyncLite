package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/src
var assets embed.FS

func main() {
	restoreOnly := flag.Bool("restore", false, "Restore all files and folders from Google Drive trash")
	minimized := flag.Bool("minimized", false, "Start minimized in tray")
	flag.Parse()

	if *restoreOnly {
		ctx := context.Background()
		srv, err := GetDriveService(ctx)
		if err != nil {
			fmt.Printf("[!] Auth error: %v\n", err)
			return
		}
		restored, errs, err := RestoreAllTrashedFiles(ctx, srv, func(msg string) {
			fmt.Println(msg)
		})
		if err != nil {
			fmt.Printf("[!] Restore error: %v\n", err)
			return
		}
		fmt.Printf("[✓] Готово! Восстановлено: %d, ошибок: %d\n", restored, errs)
		return
	}

	// Single instance enforcement: allow only ONE copy of GoogleSyncLite to run
	singleLock, isOnlyInstance := AcquireSingleInstanceLock()
	if !isOnlyInstance {
		fmt.Println("[!] Google Sync Lite уже запущена. Активировано существующее окно.")
		return
	}
	defer ReleaseSingleInstanceLock(singleLock)

	cfg := LoadConfig()
	cfg.Autostart = IsAutostartEnabled()
	_ = SaveConfig(cfg)

	db, err := OpenDatabase("sync_state_go.db")
	if err != nil {
		fmt.Printf("[!] DB Error: %v\n", err)
		return
	}
	defer db.Close()

	app := NewApp(db)

	// Initialize native system tray
	tray, err := StartTray(
		func() { app.ShowWindow() },
		func() { _ = app.TriggerSync() },
		func() { _ = app.TriggerStop() },
		func() { app.Quit() },
	)
	if err == nil {
		app.SetTray(tray)
		defer tray.Stop()
	}

	// Create Wails application with modern frameless window
	err = wails.Run(&options.App{
		Title:       "Google Sync Lite",
		Width:       960,
		Height:      700,
		MinWidth:    820,
		MinHeight:   580,
		Frameless:   true,
		StartHidden: *minimized,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 13, G: 15, B: 23, A: 255},
		OnStartup:        app.startup,
		OnBeforeClose:    app.beforeClose,
		OnShutdown:       app.shutdown,
		Bind: []interface{}{
			app,
		},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			BackdropType:         windows.Mica,
			Theme:                windows.Dark,
		},
	})

	if err != nil {
		println("Error:", err.Error())
		os.Exit(1)
	}
}
