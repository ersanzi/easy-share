//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"easyshare/internal/cfapi"
	"easyshare/internal/shellundo"
	"golang.org/x/sys/windows"
)

// menuDelete只为明确选择的目标做临时回收准备，不下载云盘其它文件，不注册全局热键。
func (c *nativeDeleteCoordinator) menuDelete(ctx context.Context, paths []string) error {
	c.menuMu.Lock()
	defer c.menuMu.Unlock()
	if err := c.guard(ctx); err != nil {
		return err
	}
	selected, err := shellundo.ValidatePaths(c.cfg.RootPath, paths)
	if err != nil {
		return err
	}
	engine := c.engine()
	if engine == nil {
		return fmt.Errorf("原生删除写回引擎尚未就绪")
	}
	var protected []string
	for _, path := range selected {
		rel, ok := cfapi.RelativeOperationPath(c.cfg.RootPath, path)
		if !ok {
			return fmt.Errorf("禁止删除当前云盘根或根外项目")
		}
		protected = append(protected, rel)
	}
	c.mu.Lock()
	c.menuRoots = append([]string(nil), protected...)
	c.mu.Unlock()
	defer func() { c.mu.Lock(); c.menuRoots = nil; c.mu.Unlock() }()
	// 和当前上传串行：保持未提交快照，回收准备期间不继续发起该目标上传。
	if err := engine.ProtectDeletion(append(c.store.ActivePaths(), protected...)); err != nil {
		return err
	}
	defer func() { _ = engine.ProtectDeletion(c.store.ActivePaths()) }()
	for _, path := range selected {
		if err := filepath.WalkDir(path, func(full string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := c.guard(ctx); err != nil {
				return err
			}
			rel, ok := cfapi.RelativeOperationPath(c.cfg.RootPath, full)
			if !ok {
				return fmt.Errorf("回收准备路径越界")
			}
			status, err := c.queryStatus(c.mount, rel)
			if err != nil {
				return err
			}
			if status.Directory {
				return nil
			}
			if status.Placeholder && !status.FullContent {
				if err := hydrateUndoSelection(ctx, full, status.LocalIdentity); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	for _, full := range selected {
		rel, ok := cfapi.RelativeOperationPath(c.cfg.RootPath, full)
		if !ok {
			return fmt.Errorf("回收目标已越过当前云盘根")
		}
		status, err := c.queryStatus(c.mount, rel)
		if err != nil {
			return err
		}
		if status.Placeholder {
			// Shell 回收准备可先发 IS_UNDELETE，不能依赖该通知推断删除意图。
			// 菜单已明确选择目标，先用相同身份/版本/操作账完成服务器回收。
			request := cfapi.OperationRequest{Kind: cfapi.CallbackTypeNotifyDelete, Path: full, CloudIdentity: status.CloudIdentity, FileSize: status.Size}
			if status.Directory {
				request.Flags = cfapi.OperationIsDirectory
			}
			if err := c.Before(ctx, request); err != nil {
				return err
			}
		}
	}
	if err := c.guard(ctx); err != nil {
		return err
	}
	// NativeRecycle使用真正的IFileOperation回收与撤销记录，不放一个假的撤销操作。
	// CFAPI Before仍负责服务器确认，系统取消/失败之后本机保留，不重复发起删除。
	if err := shellundo.NativeRecycle(selected); err != nil {
		c.mu.Lock()
		for _, path := range protected {
			if record, found := c.store.Get(path); found && record.Status != "restored" {
				record.LocalFailure = true
				if saveErr := c.store.Put(record); saveErr != nil {
					c.issues[path] = saveErr
				}
			}
		}
		c.mu.Unlock()
		return errors.Join(err, c.BeforeReconcile(ctx, c.mount))
	}
	return c.BeforeReconcile(ctx, c.mount)
}

func hydrateUndoSelection(ctx context.Context, path string, expected cfapi.DirectoryNTFSIdentity) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES|windows.WRITE_DAC,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_OPEN_NO_RECALL, 0)
	if err != nil {
		return fmt.Errorf("所选文件正被占用，未删除:%w", err)
	}
	defer windows.CloseHandle(h)
	var actual cfapi.DirectoryNTFSIdentity
	if err := windows.GetFileInformationByHandleEx(h, windows.FileIdInfo, (*byte)(unsafe.Pointer(&actual)), uint32(unsafe.Sizeof(actual))); err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("回收准备目标身份已变化，未删除")
	}
	dll := windows.NewLazySystemDLL("cldapi.dll")
	hydrate := dll.NewProc("CfHydratePlaceholder")
	if err := hydrate.Find(); err != nil {
		return err
	}
	hr, _, _ := hydrate.Call(uintptr(h), 0, ^uintptr(0), 0, 0)
	if int32(uint32(hr)) < 0 {
		return fmt.Errorf("所选云端正文回收准备失败:0x%08X", uint32(hr))
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func (a *App) startNativeUndoMenu(root string, mount *cfapi.Mount, deletion *nativeDeleteCoordinator, lifetime *nativeMountLifetime) (io.Closer, error) {
	return shellundo.Start(root, func(paths []string) error {
		if lifetime.ctx.Err() != nil {
			return fmt.Errorf("网盘已退出登录，未删除")
		}
		// 不持cfapi.mu跨Shell操作：原生回调须读取当前会话，避免互相等待。
		a.cfapi.mu.Lock()
		current := a.cfapi.personal == mount && a.cfapi.deleteCoordinator == deletion && a.cfapi.lifetime == lifetime
		a.cfapi.mu.Unlock()
		if !current {
			return fmt.Errorf("当前网盘身份已变化，未删除")
		}
		ctx, cancel := context.WithTimeout(lifetime.ctx, 10*time.Minute)
		defer cancel()
		err := deletion.menuDelete(ctx, paths)
		if err != nil {
			a.reportError("Windows可撤销删除", err)
		}
		a.cloudMutationChanged()
		return err
	})
}

func nativeUndoRelative(root, path string) (string, bool) {
	rel, ok := cfapi.RelativeOperationPath(root, path)
	return rel, ok && rel != "." && !strings.HasPrefix(rel, "../")
}
