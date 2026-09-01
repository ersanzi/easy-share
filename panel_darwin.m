//go:build darwin

// macOS 快捷面板：无边框 NSPanel 内嵌 WKWebView，加载剪切板插件的 ?panel=1 页面。
// 全局热键用 Carbon RegisterEventHotKey（⌘⇧V）——这是 mac 剪贴板工具的通行默认
// （系统没有占位冲突，但会顶替个别应用的「粘贴并匹配样式」，与业界行为一致）。
//
// 线程模型：Wails 已在主线程跑 NSApplication 事件循环，本文件不另起 RunLoop，
// 所有窗口操作经 dispatch_async(dispatch_get_main_queue()) 挂上去（与
// tray_native_darwin.m 同一模式）；不触碰 NSApplicationDelegate，避免破坏 Wails 生命周期。
#import "panel_darwin.h"

#import <Cocoa/Cocoa.h>
#import <Carbon/Carbon.h>
#import <WebKit/WebKit.h>
#import <stdlib.h>

// 让无边框 NSPanel 可成为 key window——键盘输入（搜索框）的前提。
@interface ESPanelWindow : NSPanel
@end
@implementation ESPanelWindow
- (BOOL)canBecomeKey { return YES; }
@end

@interface ESPanelController : NSObject <WKScriptMessageHandler>
@property (strong) NSPanel *panel;
@property (strong) WKWebView *webView;
@property (copy) NSString *url;
@property (strong) NSRunningApplication *prevApp;  // 热键唤起时的前台应用（回贴目标）
@property (assign) BOOL visible;
- (void)bootstrapWithURL:(NSString *)url;
- (void)toggle;
- (void)hide;
- (void)pasteAndHide;
- (void)evaluate:(NSString *)js;
- (void)panelShown;
@end

static ESPanelController *panelController = nil;
static EventHotKeyRef panelHotkeyRef = NULL;

// ── C 接口（Go 侧调用；全部转主队列执行）──

void easyshare_panel_start(const char* url) {
  NSString *u = [NSString stringWithUTF8String:url];
  dispatch_async(dispatch_get_main_queue(), ^{
    if (panelController) return;
    panelController = [[ESPanelController alloc] init];
    [panelController bootstrapWithURL:u];
  });
}

void easyshare_panel_stop(void) {
  dispatch_async(dispatch_get_main_queue(), ^{
    if (!panelController) return;
    [panelController hide];
    if (panelHotkeyRef) {
      UnregisterEventHotKey(panelHotkeyRef);
      panelHotkeyRef = NULL;
    }
    [panelController.webView stopLoading];
    [panelController.panel orderOut:nil];
    panelController = nil;
  });
}

void easyshare_panel_eval(const char* js) {
  if (!js) return;
  NSString *script = [NSString stringWithUTF8String:js];
  dispatch_async(dispatch_get_main_queue(), ^{
    [panelController evaluate:script];
  });
}

void easyshare_panel_schedule_hide(void) {
  dispatch_async(dispatch_get_main_queue(), ^{
    [panelController hide];
  });
}

void easyshare_panel_paste_after_hide(void) {
  dispatch_async(dispatch_get_main_queue(), ^{
    [panelController pasteAndHide];
  });
}

// ── Carbon 热键回调 ──

static OSStatus ESPanelHotKeyHandler(EventHandlerCallRef next, EventRef event, void *ctx) {
  dispatch_async(dispatch_get_main_queue(), ^{
    [(__bridge ESPanelController *)ctx toggle];
  });
  return noErr;
}

@implementation ESPanelController

- (void)bootstrapWithURL:(NSString *)url {
  self.url = url;

  NSRect frame = NSMakeRect(0, 0, 384, 520);
  self.panel = [[ESPanelWindow alloc]
      initWithContentRect:frame
                styleMask:NSWindowStyleMaskBorderless
                  backing:NSBackingStoreBuffered
                    defer:NO];
  self.panel.level = NSFloatingWindowLevel;
  self.panel.opaque = NO;
  self.panel.backgroundColor = [NSColor clearColor];
  self.panel.hidesOnDeactivate = NO;  // 失焦收起由 resignKey 通知自己管
  self.panel.releasedWhenClosed = NO;
  self.panel.collectionBehavior =
      NSWindowCollectionBehaviorCanJoinAllSpaces | NSWindowCollectionBehaviorFullScreenAuxiliary;

  // 圆角：内容层统一裁 12pt，与插件页 UI 的观感衔接。
  self.panel.contentView.wantsLayer = YES;
  self.panel.contentView.layer.cornerRadius = 12;
  self.panel.contentView.layer.masksToBounds = YES;

  WKWebViewConfiguration *config = [[WKWebViewConfiguration alloc] init];
  [config.userContentController addScriptMessageHandler:self name:@"espanel"];
  self.webView = [[WKWebView alloc] initWithFrame:frame configuration:config];
  self.webView.wantsLayer = YES;
  self.webView.layer.cornerRadius = 12;
  self.webView.layer.masksToBounds = YES;
  self.webView.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
  [self.panel.contentView addSubview:self.webView];

  [[NSNotificationCenter defaultCenter]
      addObserver:self
         selector:@selector(resignKey)
             name:NSWindowDidResignKeyNotification
           object:self.panel];

  NSURLRequest *request = [NSURLRequest requestWithURL:[NSURL URLWithString:self.url]];
  [self.webView loadRequest:request];

  // 全局热键 ⌘⇧V。失败不阻断面板（仍可从主程序使用插件页），只留日志口。
  EventTypeSpec type = { kEventClassKeyboard, kEventHotKeyPressed };
  OSStatus st = InstallApplicationEventHandler(ESPanelHotKeyHandler, 1, &type,
                                               (__bridge void *)self, NULL);
  if (st == noErr) {
    EventHotKeyID hid = { 'eshp', 1 };
    st = RegisterEventHotKey(kVK_ANSI_V, cmdKey | shiftKey, hid,
                             GetApplicationEventTarget(), 0, &panelHotkeyRef);
  }
  if (st != noErr) {
    NSLog(@"[EasyShare] panel: ⌘⇧V 热键注册失败（OSStatus=%d）", st);
  }
}

- (void)toggle {
  if (self.visible) {
    [self hide];
    return;
  }
  [self show];
}

- (void)show {
  // 记录唤起时的前台应用：选中条目后的回贴落点。
  self.prevApp = [NSWorkspace sharedWorkspace].frontmostApplication;

  // 贴光标弹出，钳进光标所在显示器的可视区。
  NSPoint mouse = [NSEvent mouseLocation];
  NSScreen *target = nil;
  for (NSScreen *s in [NSScreen screens]) {
    if (NSPointInRect(mouse, s.frame)) { target = s; break; }
  }
  if (!target) target = NSScreen.mainScreen;

  NSRect vis = target.visibleFrame;
  NSRect f = self.panel.frame;
  f.origin.x = mouse.x + 12;
  f.origin.y = mouse.y - f.size.height - 12;
  if (NSMaxX(f) > NSMaxX(vis)) f.origin.x = NSMaxX(vis) - f.size.width;
  if (NSMinX(f) < NSMinX(vis)) f.origin.x = NSMinX(vis);
  if (NSMinY(f) < NSMinY(vis)) f.origin.y = NSMinY(vis);
  [self.panel setFrame:f display:YES];

  [NSApp activateIgnoringOtherApps:YES];
  [self.panel makeKeyAndOrderFront:nil];
  self.visible = YES;
  [self panelShown];
}

- (void)hide {
  if (!self.visible) return;
  [self.panel orderOut:nil];
  self.visible = NO;
}

- (void)resignKey {
  [self hide];
}

// 与 Windows panel_windows.go 的 pasteOnOwnThread 语义一致：
// 收起 → 切回之前的应用 → 延迟数十毫秒 → 确认后合成 ⌘V。
// 合成按键需要辅助功能授权；未授权时降级为「仅复制」不打扰用户。
- (void)pasteAndHide {
  [self hide];
  NSRunningApplication *prev = self.prevApp;
  self.prevApp = nil;
  if (!prev || prev == NSRunningApplication.currentApplication) return;

  NSDictionary *options = @{ (__bridge NSString *)kAXTrustedCheckOptionPrompt: @NO };
  if (!AXIsProcessTrustedWithOptions((__bridge CFDictionaryRef)options)) return;

  [prev activateWithOptions:NSApplicationActivateIgnoringOtherApps];
  pid_t pid = prev.processIdentifier;
  dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(0.08 * NSEC_PER_SEC)),
                 dispatch_get_main_queue(), ^{
    CGEventRef down = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)kVK_ANSI_V, YES);
    CGEventSetFlags(down, kCGEventFlagMaskCommand);
    CGEventPostToPid(pid, down);
    CFRelease(down);
    CGEventRef up = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)kVK_ANSI_V, NO);
    CGEventSetFlags(up, kCGEventFlagMaskCommand);
    CGEventPostToPid(pid, up);
    CFRelease(up);
  });
}

- (void)evaluate:(NSString *)js {
  if (!self.webView || js.length == 0) return;
  [self.webView evaluateJavaScript:js completionHandler:nil];
}

// 面板弹出通知：脚本由 Go 侧供给（SDK 协议的单一事实源）。
- (void)panelShown {
  const char *s = easysharePanelShownScript();
  if (!s) return;
  NSString *js = [NSString stringWithUTF8String:s];
  free((void *)s);
  [self evaluate:js];
}

// ── WKScriptMessageHandler：面板页 → Go ──

- (void)userContentController:(WKUserContentController *)userContentController
      didReceiveScriptMessage:(WKScriptMessage *)message {
  NSData *json = nil;
  if ([message.body isKindOfClass:[NSDictionary class]]) {
    json = [NSJSONSerialization dataWithJSONObject:message.body options:0 error:NULL];
  } else if ([message.body isKindOfClass:[NSString class]]) {
    json = [message.body dataUsingEncoding:NSUTF8StringEncoding];
  }
  if (!json) return;
  NSString *text = [[NSString alloc] initWithData:json encoding:NSUTF8StringEncoding];

  const char *reply = easysharePanelMessage(text.UTF8String);
  if (reply) {
    NSString *js = [NSString stringWithUTF8String:reply];
    free((void *)reply);
    [self evaluate:js];
  }
}

@end
