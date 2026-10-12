//go:build darwin

// 会议听记的 macOS 麦克风授权：Wails v2.14 的 WailsContext 是 WKWebView 的 WKUIDelegate，
// 未实现媒体权限代理方法（requestMediaCapturePermission），前端 getUserMedia 会被默认拒绝。
// 本文件以 category 补上该方法：麦克风放行（首次触发系统 TCC 弹窗，需 Info.plist 的
// NSMicrophoneUsageDescription，已加），摄像头/屏幕共享拒绝——听记只要麦克风。
// Windows 无此问题（Wails v2.14 SetGlobalPermission(Allow) 默认放行）。
// 真机行为验证延后（本仓无 Mac 常态），编译由 GitHub Actions macos-latest 把关。

#import <Foundation/Foundation.h>
#import <WebKit/WebKit.h>

// 最小接口声明：只为让本编译单元认识类名；真实接口与实现在 Wails 源码内
// （internal/frontend/desktop/darwin/WailsContext.h/.m），跨编译单元的接口声明合法。
@interface WailsContext : NSObject
@end

@implementation WailsContext (MeetingMediaCapture)

- (void)webView:(WKWebView *)webView
    requestMediaCapturePermissionForOrigin:(WKSecurityOrigin *)origin
                        initiatedByFrame:(WKFrameInfo *)frame
                                    type:(WKMediaCaptureType)type
                         decisionHandler:(void (^)(WKPermissionDecision decision))decisionHandler {
  if (type == WKMediaCaptureTypeMicrophone) {
    decisionHandler(WKPermissionDecisionGrant);
  } else {
    decisionHandler(WKPermissionDecisionDeny);
  }
}

@end
