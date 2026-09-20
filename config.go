package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type Config struct {
	LocalFolder         string `json:"local_folder"`
	RemoteFolderID      string `json:"remote_folder_id"`
	SyncIntervalSeconds int    `json:"sync_interval_seconds"`
	Autostart           bool   `json:"autostart"`
	DryRun              bool   `json:"dry_run"`
	SyncMode            string `json:"sync_mode"` // "local_master" or "two_way"
}

var (
	configLock sync.RWMutex
	appConfig  Config
)

const ConfigFileName = "config.json"

func LoadConfig() Config {
	configLock.Lock()
	defer configLock.Unlock()

	defaultLocal := filepath.Join(".", "sync_folder")
	absLocal, _ := filepath.Abs(defaultLocal)

	appConfig = Config{
		LocalFolder:         absLocal,
		RemoteFolderID:      "root",
		SyncIntervalSeconds: 60,
		Autostart:           false,
		DryRun:              false,
		SyncMode:            "local_master",
	}

	data, err := os.ReadFile(ConfigFileName)
	if err == nil {
		_ = json.Unmarshal(data, &appConfig)
		if appConfig.SyncMode == "" {
			appConfig.SyncMode = "local_master"
		}
	} else {
		SaveConfigUnsafe(appConfig)
	}
	return appConfig
}

func GetConfig() Config {
	configLock.RLock()
	defer configLock.RUnlock()
	return appConfig
}

func SaveConfig(cfg Config) error {
	configLock.Lock()
	defer configLock.Unlock()
	appConfig = cfg
	return SaveConfigUnsafe(cfg)
}

func SaveConfigUnsafe(cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(ConfigFileName, data, 0644)
}
