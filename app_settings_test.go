package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"easyshare/internal/config"
)

func TestOpenReceiveFolderUsesLatestPersistedSetting(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "EasyShare", "config.json")
	value := loadTestConfig(t, configPath)
	cachedDirectory := filepath.Join(t.TempDir(), "cached-receive")
	value.ReceiveDir = cachedDirectory
	if err := config.Save(configPath, value); err != nil {
		t.Fatalf("Save(cached config) error = %v", err)
	}

	app := NewApp()
	app.configPath = configPath
	app.config = value

	currentDirectory := filepath.Join(t.TempDir(), "current-receive")
	current := value
	current.ReceiveDir = currentDirectory
	if err := config.Save(configPath, current); err != nil {
		t.Fatalf("Save(current config) error = %v", err)
	}

	var openedDirectory string
	err := app.openReceiveFolder(func(directory string) error {
		openedDirectory = directory
		return nil
	})
	if err != nil {
		t.Fatalf("openReceiveFolder() error = %v", err)
	}
	if openedDirectory != currentDirectory {
		t.Errorf("opened directory = %q, want current setting %q", openedDirectory, currentDirectory)
	}
	if openedDirectory == cachedDirectory {
		t.Errorf("opened stale cached directory %q", cachedDirectory)
	}
}

func TestOpenReceiveFolderRejectsInvalidCurrentConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	app := NewApp()
	app.configPath = configPath
	app.config.ReceiveDir = filepath.Join(t.TempDir(), "stale-receive")
	opened := false

	err := app.openReceiveFolder(func(string) error {
		opened = true
		return nil
	})
	if err == nil {
		t.Fatal("openReceiveFolder() accepted invalid current config")
	}
	if !strings.Contains(err.Error(), "读取接收目录配置失败") {
		t.Errorf("error = %q, want actionable receive-directory context", err)
	}
	if opened {
		t.Fatal("folder opener was called after current config failed to load")
	}
}

func loadTestConfig(t *testing.T, path string) config.Config {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	value, err := config.Load(path)
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	return value
}
