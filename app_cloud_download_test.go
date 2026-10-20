package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"easyshare/internal/config"
)

func TestSafeCloudDownloadName(t *testing.T) {
	cases := []struct {
		name string
		key  string
		want string
	}{
		{name: "普通文件", key: "文档/季度报告.pdf", want: "季度报告.pdf"},
		{name: "去掉尾斜杠", key: "目录/", want: "目录"},
		{name: "路径穿越只取末段", key: "../预算.xlsx", want: "预算.xlsx"},
		{name: "Windows 非法字符", key: `a:b*c?.txt`, want: "a_b_c_.txt"},
		{name: "反斜杠", key: `dir\report.docx`, want: "dir_report.docx"},
		{name: "去掉结尾点", key: "报告.txt.", want: "报告.txt"},
		{name: "设备保留名", key: "CON.txt", want: "_CON.txt"},
		{name: "空名回落", key: "/", want: ""},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, err := safeCloudDownloadName(test.key)
			if test.want == "" {
				if err == nil {
					t.Fatalf("safeCloudDownloadName(%q) 期望报错，得到 %q", test.key, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("safeCloudDownloadName(%q) error = %v", test.key, err)
			}
			if got != test.want {
				t.Errorf("safeCloudDownloadName(%q) = %q, want %q", test.key, got, test.want)
			}
		})
	}
}

func TestUniqueDownloadPathAddsSequence(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "report.pdf")
	for index := 0; index < 3; index++ {
		path := target
		if index > 0 {
			path = filepath.Join(dir, fmt.Sprintf("report (%d).pdf", index))
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	got := uniqueDownloadPath(target)
	want := filepath.Join(dir, "report (3).pdf")
	if got != want {
		t.Errorf("uniqueDownloadPath() = %q, want %q", got, want)
	}
	if _, err := os.Stat(got); !os.IsNotExist(err) {
		t.Errorf("候选路径不应已存在：%v", err)
	}
}

func TestPlaceDownloadedFileOverwritesTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "report.pdf")
	temp := filepath.Join(dir, ".part")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(temp, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := placeDownloadedFile(temp, target); err != nil {
		t.Fatalf("placeDownloadedFile() error = %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" {
		t.Errorf("target content = %q, want new", data)
	}
	if _, err := os.Stat(temp); !os.IsNotExist(err) {
		t.Errorf("临时文件应已移走：%v", err)
	}
}

func TestDownloadDirectoryReadsLatestConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "EasyShare", "config.json")
	value := loadTestConfig(t, configPath)
	stale := filepath.Join(t.TempDir(), "stale")
	value.ReceiveDir = stale
	if err := config.Save(configPath, value); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	app.configPath = configPath
	app.config = value

	current := filepath.Join(t.TempDir(), "current")
	value.ReceiveDir = current
	if err := config.Save(configPath, value); err != nil {
		t.Fatal(err)
	}

	got, err := app.downloadDirectory()
	if err != nil {
		t.Fatalf("downloadDirectory() error = %v", err)
	}
	if got != current {
		t.Errorf("downloadDirectory() = %q, want %q", got, current)
	}
}
