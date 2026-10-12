package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Config struct {
	LocalFolder          string   `json:"local_folder"`
	RemoteFolderID       string   `json:"remote_folder_id"`
	SyncIntervalSeconds  int      `json:"sync_interval_seconds"`
	Autostart            bool     `json:"autostart"`
	DryRun               bool     `json:"dry_run"`
	SyncMode             string   `json:"sync_mode"` // "local_master" or "two_way"
	SafetyShield         bool     `json:"safety_shield"`
	AllowRemoteDeletion  bool     `json:"allow_remote_deletion"`
	MaxDeleteThreshold   int      `json:"max_delete_threshold"`
	RealtimeWatch        bool     `json:"realtime_watch"`
	ConcurrentUploads    int      `json:"concurrent_uploads"`
	CustomIgnoreRules    []string `json:"custom_ignore_rules"`
	NotificationsEnabled bool     `json:"notifications_enabled"`
	BandwidthLimitKBPS   int      `json:"bandwidth_limit_kbps"`
	ConflictStrategy     string   `json:"conflict_strategy"` // "keep_both", "local_wins", "remote_wins"
	QuietHoursEnabled    bool     `json:"quiet_hours_enabled"`
	QuietHoursStart      string   `json:"quiet_hours_start"` // e.g. "23:00"
	QuietHoursEnd        string   `json:"quiet_hours_end"`   // e.g. "07:00"
	ExcludedFolders      []string `json:"excluded_folders"`  // Selective Sync: folders to skip
}

func DefaultIgnoreRules() []string {
	return []string{
		".git",
		"node_modules",
		"*.tmp",
		"*.log",
		"__pycache__",
	}
}

func (cfg Config) CombinedIgnoreRules() []string {
	var combined []string
	combined = append(combined, cfg.CustomIgnoreRules...)
	for _, folder := range cfg.ExcludedFolders {
		folder = strings.TrimSpace(folder)
		if folder != "" {
			combined = append(combined, folder)
		}
	}
	return combined
}

func parseTimeToMinutes(s string) (int, error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid format")
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil || h < 0 || h > 23 {
		return 0, fmt.Errorf("invalid hour")
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 0 || m > 59 {
		return 0, fmt.Errorf("invalid minute")
	}
	return h*60 + m, nil
}

func IsInQuietHours(now time.Time, startStr, endStr string) bool {
	sMin, err1 := parseTimeToMinutes(startStr)
	eMin, err2 := parseTimeToMinutes(endStr)
	if err1 != nil || err2 != nil {
		return false
	}
	curMin := now.Hour()*60 + now.Minute()
	if sMin == eMin {
		return false
	}
	if sMin < eMin {
		return curMin >= sMin && curMin < eMin
	}
	// Overnight span e.g. 23:00 to 07:00
	return curMin >= sMin || curMin < eMin
}

func NormalizeConfig(cfg Config) Config {
	if cfg.LocalFolder == "" {
		cfg.LocalFolder = DefaultSyncFolderPath()
	}
	if cfg.RemoteFolderID == "" {
		cfg.RemoteFolderID = "root"
	}
	if cfg.SyncIntervalSeconds < 30 {
		cfg.SyncIntervalSeconds = 60
	}
	if cfg.SyncMode != "local_master" && cfg.SyncMode != "two_way" {
		cfg.SyncMode = "local_master"
	}
	if cfg.MaxDeleteThreshold <= 0 {
		cfg.MaxDeleteThreshold = 20
	}
	if cfg.ConcurrentUploads <= 0 {
		cfg.ConcurrentUploads = 4
	} else if cfg.ConcurrentUploads > 16 {
		cfg.ConcurrentUploads = 16
	}
	if len(cfg.CustomIgnoreRules) == 0 {
		cfg.CustomIgnoreRules = DefaultIgnoreRules()
	}
	if cfg.BandwidthLimitKBPS < 0 {
		cfg.BandwidthLimitKBPS = 0
	}
	if cfg.ConflictStrategy != "local_wins" && cfg.ConflictStrategy != "remote_wins" {
		cfg.ConflictStrategy = "keep_both"
	}
	if cfg.QuietHoursStart == "" {
		cfg.QuietHoursStart = "23:00"
	}
	if cfg.QuietHoursEnd == "" {
		cfg.QuietHoursEnd = "07:00"
	}
	if cfg.ExcludedFolders == nil {
		cfg.ExcludedFolders = []string{}
	}
	cfg.SafetyShield = true
	return cfg
}

var (
	configLock sync.RWMutex
	appConfig  Config
)

const ConfigFileName = "config.json"

func LoadConfig() Config {
	configLock.Lock()
	defer configLock.Unlock()

	cfgPath := ConfigPath()

	appConfig = Config{
		LocalFolder:          DefaultSyncFolderPath(),
		RemoteFolderID:       "root",
		SyncIntervalSeconds:  60,
		Autostart:            false,
		DryRun:               false,
		SyncMode:             "local_master",
		SafetyShield:         true,
		AllowRemoteDeletion:  false,
		MaxDeleteThreshold:   20,
		RealtimeWatch:        true,
		ConcurrentUploads:    4,
		CustomIgnoreRules:    DefaultIgnoreRules(),
		NotificationsEnabled: true,
		BandwidthLimitKBPS:   0,
		ConflictStrategy:     "keep_both",
		QuietHoursEnabled:    false,
		QuietHoursStart:      "23:00",
		QuietHoursEnd:        "07:00",
		ExcludedFolders:      []string{},
	}

	data, err := os.ReadFile(cfgPath)
	if err == nil {
		rawMap := make(map[string]json.RawMessage)
		_ = json.Unmarshal(data, &rawMap)
		if unmarshalErr := json.Unmarshal(data, &appConfig); unmarshalErr != nil {
			// Backup corrupted config so user settings aren't lost silently
			_ = os.WriteFile(cfgPath+".bak", data, 0644)
		}
		if _, hasWatch := rawMap["realtime_watch"]; !hasWatch {
			appConfig.RealtimeWatch = true
		}
		if _, hasConc := rawMap["concurrent_uploads"]; !hasConc {
			appConfig.ConcurrentUploads = 4
		}
		if _, hasNotif := rawMap["notifications_enabled"]; !hasNotif {
			appConfig.NotificationsEnabled = true
		}
		if _, hasRules := rawMap["custom_ignore_rules"]; !hasRules || len(appConfig.CustomIgnoreRules) == 0 {
			appConfig.CustomIgnoreRules = DefaultIgnoreRules()
		}
		if _, hasBandwidth := rawMap["bandwidth_limit_kbps"]; !hasBandwidth {
			appConfig.BandwidthLimitKBPS = 0
		}
		if _, hasConflict := rawMap["conflict_strategy"]; !hasConflict {
			appConfig.ConflictStrategy = "keep_both"
		}
		if _, hasQuiet := rawMap["quiet_hours_enabled"]; !hasQuiet {
			appConfig.QuietHoursEnabled = false
		}
		if _, hasQStart := rawMap["quiet_hours_start"]; !hasQStart {
			appConfig.QuietHoursStart = "23:00"
		}
		if _, hasQEnd := rawMap["quiet_hours_end"]; !hasQEnd {
			appConfig.QuietHoursEnd = "07:00"
		}
		if _, hasExcl := rawMap["excluded_folders"]; !hasExcl {
			appConfig.ExcludedFolders = []string{}
		}
		appConfig = NormalizeConfig(appConfig)
	} else {
		saveConfigUnsafe(appConfig)
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
	cfg = NormalizeConfig(cfg)
	appConfig = cfg
	return saveConfigUnsafe(cfg)
}

func saveConfigUnsafe(cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	cfgPath := ConfigPath()
	tmpFile := cfgPath + ".tmp"
	f, err := os.OpenFile(tmpFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		_ = os.Remove(tmpFile)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(tmpFile)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpFile)
		return err
	}
	return os.Rename(tmpFile, cfgPath)
}
