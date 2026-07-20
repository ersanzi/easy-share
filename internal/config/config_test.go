package config

import (
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func TestLoadMissingFileCreatesSecureDefaults(t *testing.T) {
	home := filepath.Join(t.TempDir(), "user")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	path := filepath.Join(t.TempDir(), "EasyShare", "config.json")
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got.APIHost != "127.0.0.1" {
		t.Errorf("APIHost = %q, want 127.0.0.1", got.APIHost)
	}
	if got.WebDAVPort != 19080 {
		t.Errorf("WebDAVPort = %d, want 19080", got.WebDAVPort)
	}
	if got.DiscoveryPort != 9527 {
		t.Errorf("DiscoveryPort = %d, want 9527", got.DiscoveryPort)
	}
	if got.DeviceID == "" {
		t.Error("DeviceID is empty")
	}
	if got.APIToken == "" {
		t.Error("APIToken is empty")
	}
	if got.WebDAVPassword == "" {
		t.Error("WebDAVPassword is empty")
	}
	if got.ReceiveDir != filepath.Join(home, "Downloads", "EasyShare") {
		t.Errorf("ReceiveDir = %q, want Windows Downloads EasyShare directory", got.ReceiveDir)
	}
	if got.WebDAVRoot != filepath.Join(home, "EasyShare") {
		t.Errorf("WebDAVRoot = %q, want user EasyShare directory", got.WebDAVRoot)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("second Load() error = %v", err)
	}
	if !reflect.DeepEqual(loaded, got) {
		t.Errorf("second Load() = %#v, want %#v", loaded, got)
	}
}

func TestLoadGeneratesDifferentSecretsForDifferentConfigs(t *testing.T) {
	first, err := Load(filepath.Join(t.TempDir(), "first", "config.json"))
	if err != nil {
		t.Fatalf("first Load() error = %v", err)
	}
	second, err := Load(filepath.Join(t.TempDir(), "second", "config.json"))
	if err != nil {
		t.Fatalf("second Load() error = %v", err)
	}

	if first.DeviceID == second.DeviceID {
		t.Error("DeviceID was reused")
	}
	if first.APIToken == second.APIToken {
		t.Error("APIToken was reused")
	}
	if first.WebDAVPassword == second.WebDAVPassword {
		t.Error("WebDAVPassword was reused")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	want := Config{
		DeviceID:       "device-id",
		DeviceName:     "workstation",
		APIHost:        "127.0.0.1",
		APIPort:        19079,
		APIToken:       "api-token",
		DiscoveryPort:  9527,
		TransferPort:   9528,
		ReceiveDir:     `C:\Users\tester\Downloads\EasyShare`,
		WebDAVRoot:     `C:\Users\tester\EasyShare`,
		WebDAVPort:     19080,
		WebDAVUsername: "EasyShare",
		WebDAVPassword: "webdav-password",
	}

	if err := Save(path, want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Load() = %#v, want %#v", got, want)
	}

	replacement := want
	replacement.DeviceName = "replacement"
	if err := Save(path, replacement); err != nil {
		t.Fatalf("replacement Save() error = %v", err)
	}
	got, err = Load(path)
	if err != nil {
		t.Fatalf("replacement Load() error = %v", err)
	}
	if !reflect.DeepEqual(got, replacement) {
		t.Errorf("replacement Load() = %#v, want %#v", got, replacement)
	}
}

func TestLoadRejectsInvalidPersistedConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load() accepted an insecure empty configuration")
	}
}

func TestConcurrentLoadReturnsOnePersistedIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	results := make(chan Config, 8)
	var waitGroup sync.WaitGroup
	for range 8 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			value, err := Load(path)
			if err != nil {
				t.Errorf("Load() error = %v", err)
				return
			}
			results <- value
		}()
	}
	waitGroup.Wait()
	close(results)
	var first Config
	for value := range results {
		if first.DeviceID == "" {
			first = value
		}
		if value.DeviceID != first.DeviceID || value.APIToken != first.APIToken {
			t.Fatal("concurrent Load returned different identities")
		}
	}
}
