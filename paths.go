package main

import (
	"os"
	"path/filepath"
	"sync"
)

var (
	appDirOnce   sync.Once
	cachedAppDir string
)

// AppDir returns the absolute directory path containing the running executable.
// Falls back to current working directory if os.Executable fails.
func AppDir() string {
	appDirOnce.Do(func() {
		exe, err := os.Executable()
		if err == nil {
			cachedAppDir = filepath.Dir(exe)
		} else {
			cwd, _ := os.Getwd()
			cachedAppDir = cwd
		}
	})
	return cachedAppDir
}

// ConfigPath returns the absolute path to config.json
func ConfigPath() string {
	return filepath.Join(AppDir(), "config.json")
}

// DatabasePath returns the absolute path to sync_state_go.db
func DatabasePath() string {
	return filepath.Join(AppDir(), "sync_state_go.db")
}

// CredentialsPath returns the absolute path to credentials.json
func CredentialsPath() string {
	return filepath.Join(AppDir(), "credentials.json")
}

// TokenPath returns the absolute path to token.json
func TokenPath() string {
	return filepath.Join(AppDir(), "token.json")
}

// DefaultSyncFolderPath returns the absolute path to the default local sync folder
func DefaultSyncFolderPath() string {
	return filepath.Join(AppDir(), "sync_folder")
}
