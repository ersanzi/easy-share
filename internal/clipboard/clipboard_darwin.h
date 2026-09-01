#pragma once

// macOS 剪贴板原生操作（clipboard_darwin.m 实现，listener_darwin.go 经 cgo 调用）。
// 返回的字符串/字节缓冲一律用 malloc/strdup 分配，Go 侧负责 free（同一进程分配器）。

// changeCount 轮询用：剪贴板版本号，每次写入自增。
long easyshare_clip_change_count(void);

// 分类当前内容：0 空 / 1 文本 / 2 图片 / 3 文件列表。
int easyshare_clip_classify(void);

// 读取纯文本（UTF-8）；无内容返回 NULL。调用方 free。
char* easyshare_clip_read_text(void);

// 读取图片并转 PNG 字节（out_len 出参）；无图片或转换失败返回 NULL。调用方 free。
unsigned char* easyshare_clip_read_png(int* out_len);

// 读取文件路径列表（JSON 字符串数组的 UTF-8 文本）；非文件内容返回 NULL。调用方 free。
char* easyshare_clip_read_files_json(void);

// 前台应用名（作为记录来源 Source）；失败返回 NULL。调用方 free。
char* easyshare_clip_frontmost_app(void);

// 回写：成功返回非 0。
int easyshare_clip_write_text(const char* text);
int easyshare_clip_write_png(const unsigned char* data, int len);
int easyshare_clip_write_files(const char* const* paths, int count);
