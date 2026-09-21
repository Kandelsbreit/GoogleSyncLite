package main

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/api/drive/v3"
)

// RestoreAllTrashedFiles searches for all files and folders in Google Drive Trash
// and untrashes them (sets Trashed: false).
func RestoreAllTrashedFiles(ctx context.Context, srv *drive.Service, logger func(string)) (int, int, error) {
	if logger == nil {
		logger = func(s string) { fmt.Println(s) }
	}

	logger("[*] Поиск удаленных элементов в Корзине Google Диска...")

	var trashedItems []*drive.File
	pageToken := ""

	for {
		if ctx.Err() != nil {
			return 0, 0, ctx.Err()
		}

		call := srv.Files.List().
			Q("trashed = true").
			Spaces("drive").
			Fields("nextPageToken, files(id, name, mimeType)").
			PageSize(1000)

		if pageToken != "" {
			call = call.PageToken(pageToken)
		}

		res, err := call.Context(ctx).Do()
		if err != nil {
			return 0, 0, fmt.Errorf("ошибка получения списка корзины: %w", err)
		}

		trashedItems = append(trashedItems, res.Files...)
		logger(fmt.Sprintf("    Найдено в корзине: %d элементов...", len(trashedItems)))

		pageToken = res.NextPageToken
		if pageToken == "" {
			break
		}
	}

	total := len(trashedItems)
	logger(fmt.Sprintf("[*] Всего в Корзине обнаружено: %d элементов. Начинаем восстановление...", total))
	if total == 0 {
		return 0, 0, nil
	}

	// First restore folders so that parents exist, then files
	var folders []*drive.File
	var files []*drive.File
	for _, item := range trashedItems {
		if item.MimeType == "application/vnd.google-apps.folder" {
			folders = append(folders, item)
		} else {
			files = append(files, item)
		}
	}

	var restoredCount int64
	var errorCount int64

	restoreList := func(items []*drive.File, itemType string, concurrency int) {
		if len(items) == 0 {
			return
		}
		logger(fmt.Sprintf("[*] Восстановление %s (%d шт.)...", itemType, len(items)))

		sem := make(chan struct{}, concurrency)
		var wg sync.WaitGroup

		for _, item := range items {
			if ctx.Err() != nil {
				break
			}
			sem <- struct{}{}
			wg.Add(1)

			go func(it *drive.File) {
				defer wg.Done()
				defer func() { <-sem }()

				// Restore file by un-trashing (ForceSendFields is REQUIRED to send false)
				var err error
				for attempt := 0; attempt < 3; attempt++ {
					if ctx.Err() != nil {
						return
					}
					_, err = srv.Files.Update(it.Id, &drive.File{
						Trashed:         false,
						ForceSendFields: []string{"Trashed"},
					}).Context(ctx).Do()
					if err == nil {
						break
					}
					time.Sleep(time.Duration(attempt+1) * 200 * time.Millisecond)
				}

				if err != nil {
					errCount := atomic.AddInt64(&errorCount, 1)
					if errCount <= 5 {
						logger(fmt.Sprintf("    [!] Ошибка восстановления %s (%s): %v", it.Name, it.Id, err))
					}
				} else {
					n := atomic.AddInt64(&restoredCount, 1)
					if n%100 == 0 || n == int64(total) {
						logger(fmt.Sprintf("    Восстановлено: %d / %d элементов...", n, total))
					}
				}
			}(item)
		}
		wg.Wait()
	}

	// 1. Restore folders first (concurrency 10)
	restoreList(folders, "папок", 10)

	// 2. Restore files (concurrency 25)
	restoreList(files, "файлов", 25)

	logger(fmt.Sprintf("[✓] Восстановление завершено! Успешно восстановлено: %d, ошибок: %d.", restoredCount, errorCount))
	return int(restoredCount), int(errorCount), nil
}
