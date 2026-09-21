//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

const (
	RegistryRunKey = `Software\Microsoft\Windows\CurrentVersion\Run`
	AppName        = "GoogleSyncLiteGo"
)

func IsAutostartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, RegistryRunKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()

	_, _, err = k.GetStringValue(AppName)
	return err == nil
}

func SetAutostart(enable bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, RegistryRunKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()

	if enable {
		exePath, err := os.Executable()
		if err != nil {
			return err
		}
		absExe, _ := filepath.Abs(exePath)
		cmd := fmt.Sprintf(`"%s" --minimized`, absExe)
		return k.SetStringValue(AppName, cmd)
	} else {
		_ = k.DeleteValue(AppName)
		return nil
	}
}
