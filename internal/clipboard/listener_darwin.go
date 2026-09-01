//go:build darwin

// Package clipboard 的 macOS 监听实现：NSPasteboard changeCount 轮询。
//
// 与 Windows 的消息推送不同，macOS 没有剪贴板变更回调机制，业界通行做法是
// 轮询 changeCount（只读一个整数，开销可忽略，800ms 足够跟手）。读取与回写
// 的具体动作在 clipboard_darwin.m（ObjC），本文件只做轮询编排与 Entry 组装。
package clipboard

/*
#cgo LDFLAGS: -framework AppKit -framework Foundation
#include <stdlib.h>
#include "clipboard_darwin.h"
*/
import "C"

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	_ "image/png"
	"os"
	"time"
	"unsafe"

	"C"
)

// startListener 启动轮询协程。可重入（Start 内部已做运行中判断）。
func (s *Service) startListener() error {
	go s.pollLoop()
	return nil
}

// stopListener 关闭 stopCh 让轮询协程退出。
func (s *Service) stopListener() {
	close(s.stopCh)
}

// pollLoop 轮询 changeCount：变化即读取并记录。基线取启动时的计数值，
// 启动前已存在的剪贴板内容不算「新复制」。
func (s *Service) pollLoop() {
	last := int64(C.easyshare_clip_change_count())
	ticker := time.NewTicker(800 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
		}
		cur := int64(C.easyshare_clip_change_count())
		if cur == last {
			continue
		}
		last = cur
		s.handleUpdate()
	}
}

// goCFree 释放 ObjC 侧 malloc/strdup 的内存（同一进程分配器）。
func goCFree(p *C.char) { C.free(unsafe.Pointer(p)) }

// handleUpdate 读取当前剪贴板并组装 Entry（优先级与 Windows 一致：文件 > 图片 > 文本）。
func (s *Service) handleUpdate() {
	source := ""
	if p := C.easyshare_clip_frontmost_app(); p != nil {
		source = C.GoString(p)
		goCFree(p)
	}

	switch C.easyshare_clip_classify() {
	case 3: // 文件列表
		p := C.easyshare_clip_read_files_json()
		if p == nil {
			return
		}
		paths := decodeJSONStringArray(C.GoString(p))
		goCFree(p)
		if len(paths) == 0 || len(paths) > maxFilePaths {
			return
		}
		s.record(Entry{
			ID: NewID(), Kind: KindFiles, Files: paths,
			Size: int64(len(paths)), Source: source, CreatedAt: nowMillis(),
			Hash: hashOf(stringsJoin(paths, "\n")),
		})
	case 2: // 图片（PNG/TIFF → PNG 字节）
		var length C.int
		p := C.easyshare_clip_read_png(&length)
		if p == nil || length <= 0 {
			return
		}
		png := C.GoBytes(unsafe.Pointer(p), length)
		C.free(unsafe.Pointer(p))
		cfg, _, err := image.DecodeConfig(bytesReader(png))
		if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
			return
		}
		s.record(Entry{
			ID: NewID(), Kind: KindImage,
			Width: cfg.Width, Height: cfg.Height,
			Size: int64(len(png)), Source: source, CreatedAt: nowMillis(),
			Hash: hashOfBytes(png), imagePNG: png,
		})
	case 1: // 文本
		p := C.easyshare_clip_read_text()
		if p == nil {
			return
		}
		text := C.GoString(p)
		goCFree(p)
		if text == "" || len(text) > maxTextBytes {
			return
		}
		s.record(Entry{
			ID: NewID(), Kind: KindText, Text: text,
			Size: int64(len(text)), Source: source, CreatedAt: nowMillis(),
			Hash: hashOf(text),
		})
	}
}

// Write 回写剪贴板（macOS 实现）。语义与 Windows 一致：成功后打 selfWrite 标记，
// 轮询产生的回环事件由 record 内的去重窗口吸收。
func (s *Service) Write(req WriteRequest) error {
	var hash string
	var err error
	switch req.Kind {
	case KindText:
		if req.Text == "" {
			return fmt.Errorf("文本为空")
		}
		cText := C.CString(req.Text)
		ok := C.easyshare_clip_write_text(cText) != 0
		C.free(unsafe.Pointer(cText))
		if !ok {
			return fmt.Errorf("写剪贴板失败")
		}
		hash = hashOf(req.Text)
	case KindImage:
		data, readErr := readFileBytes(s.ImagePath(req.ImageFile))
		if readErr != nil {
			return fmt.Errorf("读图片: %w", readErr)
		}
		var length C.int = C.int(len(data))
		cData := C.CBytes(data)
		ok := C.easyshare_clip_write_png((*C.uchar)(cData), length) != 0
		C.free(cData)
		if !ok {
			return fmt.Errorf("写剪贴板失败")
		}
		hash = hashOfBytes(data)
	case KindFiles:
		if len(req.Files) == 0 {
			return fmt.Errorf("文件列表为空")
		}
		cPaths := make([]*C.char, len(req.Files))
		for i, p := range req.Files {
			cPaths[i] = C.CString(p)
		}
		ok := C.easyshare_clip_write_files(&cPaths[0], C.int(len(req.Files))) != 0
		for _, c := range cPaths {
			C.free(unsafe.Pointer(c))
		}
		if !ok {
			return fmt.Errorf("写剪贴板失败")
		}
		hash = hashOf(stringsJoin(req.Files, "\n"))
	default:
		return fmt.Errorf("未知回写类型 %q", req.Kind)
	}
	s.markSelfWrite(hash)
	return nil
}

// --- 小工具（darwin 侧专用，语义与 Windows 实现里的同名逻辑一致）---

func hashOfBytes(b []byte) string { return hashOf(string(b)) }

func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

func readFileBytes(path string) ([]byte, error) { return os.ReadFile(path) }

// decodeJSONStringArray 解析 ObjC 侧序列化的路径 JSON 数组，非法输入返回 nil。
func decodeJSONStringArray(raw string) []string {
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}
