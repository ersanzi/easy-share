//go:build darwin

// macOS 剪贴板原生操作。见 clipboard_darwin.h 的接口契约。
//
// 设计要点：
// - 优先级与 Windows 实现一致：文件 > 图片 > 文本（同一份内容多类型并存时取最强语义）。
// - 图片统一转 PNG（系统复制多为 TIFF），与宿主的 /clipboard-files/{hash}.png 存储对齐。
// - NSPasteboard 自身线程安全，轮询线程直接调用即可，不依赖主线程。
#import <AppKit/AppKit.h>
#import <stdlib.h>
#import <string.h>

long easyshare_clip_change_count(void) {
  return (long)[NSPasteboard generalPasteboard].changeCount;
}

int easyshare_clip_classify(void) {
  NSPasteboard *pb = [NSPasteboard generalPasteboard];
  NSArray<NSPasteboardType> *types = pb.types;
  if (!types.count) return 0;
  if ([types containsObject:NSFilenamesPboardType]) return 3;
  if ([types containsObject:NSPasteboardTypePNG] || [types containsObject:NSTIFFPboardType]) return 2;
  NSString *text = [pb stringForType:NSPasteboardTypeString];
  if (text.length > 0) return 1;
  return 0;
}

char* easyshare_clip_read_text(void) {
  NSString *text = [[NSPasteboard generalPasteboard] stringForType:NSPasteboardTypeString];
  if (text.length == 0) return NULL;
  return strdup(text.UTF8String);
}

unsigned char* easyshare_clip_read_png(int* out_len) {
  NSPasteboard *pb = [NSPasteboard generalPasteboard];
  NSData *png = nil;

  // 剪贴板里本来就是 PNG：直接用。
  NSData *raw = [pb dataForType:NSPasteboardTypePNG];
  if (raw) png = raw;

  // 常见情形是 TIFF（Preview / 截图 / 浏览器复制图片）：经 NSBitmapImageRep 转 PNG。
  if (!png) {
    NSData *tiff = [pb dataForType:NSTIFFPboardType];
    if (tiff) {
      NSBitmapImageRep *rep = [[NSBitmapImageRep alloc] initWithData:tiff];
      if (rep) {
        png = [rep representationUsingType:NSBitmapImageFileTypePNG properties:@{}];
      }
    }
  }
  if (!png || png.length == 0 || png.length > 64u << 20) return NULL;

  unsigned char *out = (unsigned char *)malloc(png.length);
  if (!out) return NULL;
  memcpy(out, png.bytes, png.length);
  if (out_len) *out_len = (int)png.length;
  return out;
}

char* easyshare_clip_read_files_json(void) {
  NSArray *paths = [[NSPasteboard generalPasteboard]
      propertyListForType:NSFilenamesPboardType];
  if (![paths isKindOfClass:[NSArray class]] || paths.count == 0) return NULL;
  NSData *json = [NSJSONSerialization dataWithJSONObject:paths options:0 error:NULL];
  if (!json) return NULL;
  NSString *text = [[NSString alloc] initWithData:json encoding:NSUTF8StringEncoding];
  return text ? strdup(text.UTF8String) : NULL;
}

char* easyshare_clip_frontmost_app(void) {
  NSRunningApplication *app = [NSWorkspace sharedWorkspace].frontmostApplication;
  NSString *name = app.localizedName;
  if (name.length == 0) return NULL;
  return strdup(name.UTF8String);
}

int easyshare_clip_write_text(const char* text) {
  NSString *s = [NSString stringWithUTF8String:text];
  if (!s.length) return 0;
  NSPasteboard *pb = [NSPasteboard generalPasteboard];
  [pb clearContents];
  return [pb setString:s forType:NSPasteboardTypeString] ? 1 : 0;
}

int easyshare_clip_write_png(const unsigned char* data, int len) {
  if (!data || len <= 0) return 0;
  NSData *png = [NSData dataWithBytes:data length:(NSUInteger)len];
  NSImage *image = [[NSImage alloc] initWithData:png];
  if (!image) return 0;

  NSPasteboard *pb = [NSPasteboard generalPasteboard];
  [pb clearContents];
  // 同时声明 PNG 与 TIFF：文本编辑器等只认 TIFF，现代应用认 PNG。
  NSArray *types = @[ NSPasteboardTypePNG, NSTIFFPboardType ];
  [pb declareTypes:types owner:nil];
  BOOL ok = [pb setData:png forType:NSPasteboardTypePNG];
  NSData *tiff = [image TIFFRepresentation];
  if (tiff) ok = [pb setData:tiff forType:NSTIFFPboardType] || ok;
  return ok ? 1 : 0;
}

int easyshare_clip_write_files(const char* const* paths, int count) {
  if (!paths || count <= 0) return 0;
  NSMutableArray<NSURL *> *urls = [NSMutableArray arrayWithCapacity:(NSUInteger)count];
  for (int i = 0; i < count; i++) {
    NSString *p = [NSString stringWithUTF8String:paths[i]];
    if (!p.length) continue;
    NSURL *url = [NSURL fileURLWithPath:p];
    if (url) [urls addObject:url];
  }
  if (urls.count == 0) return 0;
  NSPasteboard *pb = [NSPasteboard generalPasteboard];
  [pb clearContents];
  return [pb writeObjects:urls] ? 1 : 0;
}
