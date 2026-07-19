package logging

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenCreatesAppendOnlyLog(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	file, path, err := Open("desktop.log")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("first\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	file, _, err = Open("desktop.log")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("second\n"); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "first\nsecond\n" {
		t.Fatalf("log = %q", data)
	}
	wantDirectory := filepath.Join(os.Getenv("LOCALAPPDATA"), "EasyShare", "logs")
	if filepath.Dir(path) != wantDirectory {
		t.Fatalf("directory = %q, want %q", filepath.Dir(path), wantDirectory)
	}
}
