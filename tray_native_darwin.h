#ifndef EASYSHARE_TRAY_NATIVE_DARWIN_H
#define EASYSHARE_TRAY_NATIVE_DARWIN_H

#include <stddef.h>

// 供 Go 调用的最小 AppKit bridge。实现只管理 NSStatusItem，不接管应用 delegate。
void easyshare_tray_start(const void *icon_bytes, size_t icon_length);
void easyshare_tray_set_status(const char *status_utf8);

// 由 cgo 导出的 Go 回调，供 Objective-C 菜单动作调用。
extern void easyshareTrayReady(void);
extern void easyshareTrayOpen(void);
extern void easyshareTrayQuit(void);

#endif
