package transfer

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// zipFolder 将目录打包为临时 zip 文件，返回临时文件路径和压缩后大小。
// zip 内部路径保留相对于 dir 的目录结构（如 "sub/file.txt"）。
// 调用方负责在使用完毕后删除临时文件。
func zipFolder(dir string) (tempPath string, size int64, err error) {
	temp, err := os.CreateTemp("", "easyshare-folder-*.zip")
	if err != nil {
		return "", 0, fmt.Errorf("创建临时文件: %w", err)
	}
	tempPath = temp.Name()
	writer := zip.NewWriter(temp)
	walkErr := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relative, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		if relative == "." {
			return nil
		}
		// 统一使用斜杠作为 zip 内部分隔符
		relative = filepath.ToSlash(relative)
		if info.IsDir() {
			_, err := writer.Create(relative + "/")
			return err
		}
		header, headerErr := zip.FileInfoHeader(info)
		if headerErr != nil {
			return headerErr
		}
		header.Name = relative
		header.Method = zip.Deflate
		entry, createErr := writer.CreateHeader(header)
		if createErr != nil {
			return createErr
		}
		file, openErr := os.Open(path)
		if openErr != nil {
			return openErr
		}
		defer file.Close()
		_, copyErr := io.Copy(entry, file)
		return copyErr
	})
	closeErr := writer.Close()
	temp.Close()
	if walkErr != nil {
		os.Remove(tempPath)
		return "", 0, fmt.Errorf("打包文件夹: %w", walkErr)
	}
	if closeErr != nil {
		os.Remove(tempPath)
		return "", 0, fmt.Errorf("关闭 zip: %w", closeErr)
	}
	info, statErr := os.Stat(tempPath)
	if statErr != nil {
		os.Remove(tempPath)
		return "", 0, statErr
	}
	return tempPath, info.Size(), nil
}

// unzipTo 将 zip 文件解压到目标目录，防止 zip slip 路径穿越。
func unzipTo(zipPath, targetDir string) error {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("打开 zip: %w", err)
	}
	defer reader.Close()
	for _, file := range reader.File {
		// 防止 zip slip：确保解压路径在 targetDir 内
		name := filepath.FromSlash(file.Name)
		destination := filepath.Join(targetDir, name)
		if !strings.HasPrefix(destination, filepath.Clean(targetDir)+string(os.PathSeparator)) {
			continue
		}
		if file.FileInfo().IsDir() {
			os.MkdirAll(destination, 0o755)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return err
		}
		source, err := file.Open()
		if err != nil {
			return err
		}
		target, err := os.Create(destination)
		if err != nil {
			source.Close()
			return err
		}
		_, copyErr := io.Copy(target, source)
		target.Close()
		source.Close()
		if copyErr != nil {
			return copyErr
		}
	}
	return nil
}
