package drive

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// This acceptance test is opt-in because it creates a real Windows network
// drive mapping for the duration of the test. Example:
// EASYSHARE_TEST_DRIVE=Y: go test ./internal/drive -run TestWindowsWebClientDigestIntegration -v
func TestWindowsWebClientDigestIntegration(t *testing.T) {
	letter := os.Getenv("EASYSHARE_TEST_DRIVE")
	if letter == "" {
		t.Skip("set EASYSHARE_TEST_DRIVE to an unused drive letter")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "probe.txt"), []byte("digest-webdav-ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := NewService(root, "EasyShare", "integration-secret")
	if err := service.Start(0); err != nil {
		t.Fatal(err)
	}
	defer service.Stop(context.Background())
	url := "http://" + service.Addr()
	mapper := NewMapper()
	if err := mapper.Map(context.Background(), letter, url, "EasyShare", "integration-secret"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := mapper.Unmap(context.Background(), letter, url); err != nil {
			t.Errorf("cleanup mapping: %v", err)
		}
	}()
	data, err := os.ReadFile(filepath.Join(letter+`\`, "probe.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "digest-webdav-ok" {
		t.Fatalf("mapped content = %q", data)
	}
}
