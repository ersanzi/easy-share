//go:build windows

// Package explorerview 只初始化当前个人云根中的Explorer视图，不更改全局模板。
package explorerview

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
)

type Options struct {
	Root, StateFile string
	Logf            func(string, ...any)
}

type Watcher struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func InScope(root, path string) bool {
	if !filepath.IsAbs(root) || !filepath.IsAbs(path) {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func PortraitSize(dpi, workWidth, workHeight int32) (int32, int32) {
	if dpi <= 0 {
		dpi = 96
	}
	width, height := int32(760)*dpi/96, int32(900)*dpi/96
	width, height = min(width, workWidth-32), min(height, workHeight-32)
	if width >= height {
		width = height * 4 / 5
	}
	return max(width, 1), max(height, 1)
}

// Start 在独立STA线程观察导航；启动时已有窗口不会被重定尺寸。
func Start(ctx context.Context, options Options) *Watcher {
	ctx, cancel := context.WithCancel(ctx)
	w := &Watcher{cancel: cancel, done: make(chan struct{})}
	go w.run(ctx, options)
	return w
}

func (w *Watcher) Close() {
	if w != nil {
		w.cancel()
		<-w.done
	}
}

func (w *Watcher) run(ctx context.Context, options Options) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(w.done)
	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		if options.Logf != nil {
			options.Logf("Explorer 默认视图COM初始化失败: %v", err)
		}
		return
	}
	defer ole.CoUninitialize()
	object, err := oleutil.CreateObject("Shell.Application")
	if err != nil {
		return
	}
	defer object.Release()
	shell, err := object.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return
	}
	defer shell.Release()
	initialized := map[string]bool{}
	if data, err := os.ReadFile(options.StateFile); err == nil {
		_ = json.Unmarshal(data, &initialized)
	}
	if initialized == nil {
		initialized = map[string]bool{}
	}
	seen := map[int64]string{}
	layouts := map[string]Layout{}
	layoutFile := options.StateFile + ".layout.json"
	if data, err := os.ReadFile(layoutFile); err == nil {
		_ = json.Unmarshal(data, &layouts)
	}
	if layouts == nil {
		layouts = map[string]Layout{}
	}
	for key, layout := range layouts {
		if next, migrated := migrateDefaultStatusColumn(layout); migrated {
			layouts[key] = next
			initialized[key] = false
		}
	}
	first := true
	ticker := time.NewTicker(700 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		windows, err := oleutil.CallMethod(shell, "Windows")
		if err == nil {
			list := windows.ToIDispatch()
			if list != nil {
				count, countErr := oleutil.GetProperty(list, "Count")
				if countErr == nil {
					for i := 0; i < int(count.Val); i++ {
						entry, err := oleutil.CallMethod(list, "Item", i)
						if err != nil {
							continue
						}
						window := entry.ToIDispatch()
						if window != nil {
							hwnd, path, err := folderOf(window)
							if err == nil {
								old, known := seen[hwnd]
								seen[hwnd] = path
								key := strings.ToLower(filepath.Clean(path))
								if InScope(options.Root, path) && !initialized[key] {
									if err := applyDetails(window); err != nil {
										if options.Logf != nil {
											options.Logf("Explorer 云盘详细视图未应用: %v", err)
										}
									} else {
										initialized[key] = true
										if options.StateFile != "" {
											data, _ := json.Marshal(initialized)
											if err := os.WriteFile(options.StateFile, data, 0600); err != nil && options.Logf != nil {
												options.Logf("保存云盘视图默认记录失败: %v", err)
											}
										}
									}
								}
								if InScope(options.Root, path) {
									if layout, exists := layouts[key]; exists && (!known || old != path) {
										if err := restoreLayout(window, layout); err != nil && options.Logf != nil {
											options.Logf("恢复云盘用户布局失败: %v", err)
										}
									}
									if layout, err := readLayout(window); err == nil && len(layout.Keys) > 0 {
										layouts[key] = layout
										if options.StateFile != "" {
											data, _ := json.Marshal(layouts)
											_ = os.WriteFile(layoutFile, data, 0600)
										}
									}
								}
								if !first && !known && InScope(options.Root, path) {
									resizePortrait(uintptr(hwnd))
								}
							}
						}
						entry.Clear()
					}
					count.Clear()
				}
			}
			windows.Clear()
		}
		first = false
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func folderOf(window *ole.IDispatch) (int64, string, error) {
	handle, err := oleutil.GetProperty(window, "HWND")
	if err != nil {
		return 0, "", err
	}
	defer handle.Clear()
	document, err := oleutil.GetProperty(window, "Document")
	if err != nil {
		return 0, "", err
	}
	defer document.Clear()
	if document.ToIDispatch() == nil {
		return 0, "", fmt.Errorf("无文件夹视图")
	}
	folder, err := oleutil.GetProperty(document.ToIDispatch(), "Folder")
	if err != nil {
		return 0, "", err
	}
	defer folder.Clear()
	self, err := oleutil.GetProperty(folder.ToIDispatch(), "Self")
	if err != nil {
		return 0, "", err
	}
	defer self.Clear()
	path, err := oleutil.GetProperty(self.ToIDispatch(), "Path")
	if err != nil {
		return 0, "", err
	}
	defer path.Clear()
	return handle.Val, path.ToString(), nil
}
