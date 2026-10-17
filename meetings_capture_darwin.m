//go:build darwin

// 会议听记的 macOS 麦克风授权：Wails v2.14 的 WailsContext 是 WKWebView 的 WKUIDelegate，
// 未实现媒体权限代理方法（requestMediaCapturePermission），前端 getUserMedia 会被默认拒绝。
// 本文件在运行时给 WailsContext 补上该方法：麦克风放行（首次触发系统 TCC 弹窗，需 Info.plist 的
// NSMicrophoneUsageDescription，已加），摄像头/屏幕共享拒绝——听记只要麦克风。
// Windows 无此问题（Wails v2.14 SetGlobalPermission(Allow) 默认放行）。
//
// 为什么用 objc_getClass + class_addMethod 而不是 @implementation category：
// category 会在编译期生成对 _OBJC_CLASS_$_WailsContext 的符号引用，而 WailsContext 由
// Wails 内部包（internal/frontend/desktop/darwin）编译，那个符号不进最终链接产物，
// 于是链接期报 `Undefined symbols: _OBJC_CLASS_$_WailsContext`（2026-10-17 macOS CI 首次坐实）。
// objc_getClass 是纯运行时查找，只传字符串，不产生任何链接期依赖；类在本文件之前加载
// （Wails 的 darwin 包被 main 引用，必然先于主包的 cgo 单元初始化），查找必然命中。
// 同一手法也避免了「重复实现覆盖」——class_addMethod 在已存在时不生效，此时保留 Wails 原实现。

#import <Foundation/Foundation.h>
#import <WebKit/WebKit.h>
#import <objc/runtime.h>

// 只处理麦克风；摄像头与屏幕共享一律拒绝（听记用不到）。
static void EasyShareGrantMicrophoneOnly(id self, SEL _cmd, WKWebView *webView,
                                         WKSecurityOrigin *origin, WKFrameInfo *frame,
                                         WKMediaCaptureType type,
                                         void (^decisionHandler)(WKPermissionDecision)) {
  (void)self;
  (void)_cmd;
  (void)webView;
  (void)origin;
  (void)frame;
  if (type == WKMediaCaptureTypeMicrophone) {
    decisionHandler(WKPermissionDecisionGrant);
  } else {
    decisionHandler(WKPermissionDecisionDeny);
  }
}

__attribute__((constructor)) static void EasyShareInstallMediaCapturePermission(void) {
  @autoreleasepool {
    // 该方法自 macOS 12 起提供；更早系统不实现，交给默认行为（拒绝）即可。
    if (@available(macOS 12.0, *)) {
      Class contextClass = objc_getClass("WailsContext");
      if (contextClass == Nil) {
        NSLog(@"[EasyShare] WailsContext 未找到，麦克风授权补丁未安装");
        return;
      }
      SEL selector = @selector(webView:requestMediaCapturePermissionForOrigin:initiatedByFrame:type:decisionHandler:);
      IMP implementation = (IMP)EasyShareGrantMicrophoneOnly;
      // addMethod 只在该类尚未实现该方法时生效：Wails 将来自己补上就不会被我们覆盖。
      if (class_addMethod(contextClass, selector, implementation, "v@:@@@^v")) {
        NSLog(@"[EasyShare] 麦克风授权代理已安装");
      } else {
        NSLog(@"[EasyShare] WailsContext 已实现媒体权限代理，保留原实现");
      }
    }
  }
}
