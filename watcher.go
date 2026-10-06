package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

type FileWatcher struct {
	watcher       *fsnotify.Watcher
	localRoot     string
	onChange      func()
	logCb         func(string)
	stopCh        chan struct{}
	debounceTimer *time.Timer
	debounceMu    sync.Mutex
	watchedDirs   map[string]bool
	watchedMu     sync.RWMutex
}

func NewFileWatcher(localRoot string, onChange func(), logCb func(string)) (*FileWatcher, error) {
	if localRoot == "" {
		return nil, fmt.Errorf("локальная папка для мониторинга не указана")
	}

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("не удалось запустить файловый наблюдатель: %w", err)
	}

	fw := &FileWatcher{
		watcher:     w,
		localRoot:   filepath.Clean(localRoot),
		onChange:    onChange,
		logCb:       logCb,
		stopCh:      make(chan struct{}),
		watchedDirs: make(map[string]bool),
	}

	fw.initWatchDirs()
	go fw.loop()

	return fw, nil
}

func (fw *FileWatcher) initWatchDirs() {
	count := 0
	_ = filepath.WalkDir(fw.localRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if path != fw.localRoot && (strings.HasPrefix(name, ".") || name == "$RECYCLE.BIN" || name == "System Volume Information" || name == "__pycache__" || name == "node_modules") {
				return fs.SkipDir
			}
			fw.addDir(path)
			count++
		}
		return nil
	})
	if fw.logCb != nil {
		fw.logCb(fmt.Sprintf("[*] Монитор реального времени активен (наблюдение за %d папками)", count))
	}
}

func (fw *FileWatcher) addDir(path string) {
	fw.watchedMu.Lock()
	defer fw.watchedMu.Unlock()

	clean := filepath.Clean(path)
	if fw.watchedDirs[clean] {
		return
	}
	if err := fw.watcher.Add(clean); err == nil {
		fw.watchedDirs[clean] = true
	}
}

func (fw *FileWatcher) removeDir(path string) {
	fw.watchedMu.Lock()
	defer fw.watchedMu.Unlock()

	clean := filepath.Clean(path)
	if fw.watchedDirs[clean] {
		_ = fw.watcher.Remove(clean)
		delete(fw.watchedDirs, clean)
	}
}

func (fw *FileWatcher) isIgnoredPath(fullPath string) bool {
	clean := filepath.Clean(fullPath)
	base := filepath.Base(clean)

	if base == ".google_sync_anchor" ||
		strings.HasPrefix(base, ".~") ||
		strings.HasPrefix(base, "~$") ||
		strings.HasSuffix(base, ".tmp") ||
		strings.HasSuffix(base, ".crdownload") ||
		strings.EqualFold(base, "desktop.ini") ||
		strings.EqualFold(base, "thumbs.db") {
		return true
	}

	rel, err := filepath.Rel(fw.localRoot, clean)
	if err != nil {
		return true
	}

	relSlash := filepath.ToSlash(rel)
	if IsIgnoredRelPath(relSlash) {
		return true
	}

	// Check if any path segment starts with . or is system volume / recycle bin
	parts := strings.Split(relSlash, "/")
	for _, part := range parts {
		if strings.HasPrefix(part, ".") || part == "$RECYCLE.BIN" || part == "System Volume Information" || part == "__pycache__" || part == "node_modules" {
			return true
		}
	}

	return false
}

func (fw *FileWatcher) loop() {
	for {
		select {
		case <-fw.stopCh:
			return
		case err, ok := <-fw.watcher.Errors:
			if !ok {
				return
			}
			if fw.logCb != nil && err != nil {
				fw.logCb(fmt.Sprintf("[!] Ошибка монитора изменений: %v", err))
			}
		case event, ok := <-fw.watcher.Events:
			if !ok {
				return
			}

			// If a new directory was created, add it to watched folders
			if event.Op&fsnotify.Create != 0 {
				fi, err := os.Stat(event.Name)
				if err == nil && fi.IsDir() {
					name := fi.Name()
					if !strings.HasPrefix(name, ".") && name != "$RECYCLE.BIN" && name != "System Volume Information" && name != "__pycache__" && name != "node_modules" {
						_ = filepath.WalkDir(event.Name, func(p string, d fs.DirEntry, wErr error) error {
							if wErr == nil && d.IsDir() {
								subName := d.Name()
								if strings.HasPrefix(subName, ".") || subName == "$RECYCLE.BIN" || subName == "System Volume Information" {
									return fs.SkipDir
								}
								fw.addDir(p)
							}
							return nil
						})
					}
				}
			}

			// If directory removed, clean up from watched map
			if event.Op&fsnotify.Remove != 0 {
				fw.removeDir(event.Name)
			}

			if fw.isIgnoredPath(event.Name) {
				continue
			}

			// Schedule debounced sync trigger
			fw.scheduleSync()
		}
	}
}

func (fw *FileWatcher) scheduleSync() {
	fw.debounceMu.Lock()
	defer fw.debounceMu.Unlock()

	if fw.debounceTimer != nil {
		fw.debounceTimer.Stop()
	}

	fw.debounceTimer = time.AfterFunc(2500*time.Millisecond, func() {
		if fw.logCb != nil {
			fw.logCb("[*] Обнаружены изменения на локальном диске (Real-time). Запуск синхронизации...")
		}
		if fw.onChange != nil {
			fw.onChange()
		}
	})
}

func (fw *FileWatcher) Stop() {
	fw.debounceMu.Lock()
	if fw.debounceTimer != nil {
		fw.debounceTimer.Stop()
	}
	fw.debounceMu.Unlock()

	select {
	case <-fw.stopCh:
	default:
		close(fw.stopCh)
	}

	if fw.watcher != nil {
		_ = fw.watcher.Close()
	}
}
