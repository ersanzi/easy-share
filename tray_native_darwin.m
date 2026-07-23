//go:build darwin

#import <Cocoa/Cocoa.h>
#import <dispatch/dispatch.h>

#import "tray_native_darwin.h"

// 使用 EasyShare 专属类名，避免与 Wails 的 Objective-C 类发生全局符号冲突。
@interface EasyShareTrayController : NSObject {
    NSStatusItem *_statusItem;
    NSMenuItem *_statusMenuItem;
}

- (void)installWithIconData:(NSData *)iconData;
- (void)setStatusTitle:(NSString *)title;
- (void)openWindow:(id)sender;
- (void)quitApplication:(id)sender;

@end

static EasyShareTrayController *easyShareTrayController = nil;

@implementation EasyShareTrayController

- (void)installWithIconData:(NSData *)iconData {
    _statusItem = [[[NSStatusBar systemStatusBar]
        statusItemWithLength:NSSquareStatusItemLength] retain];

    NSImage *image = [[[NSImage alloc] initWithData:iconData] autorelease];
    if (image != nil) {
        [image setSize:NSMakeSize(18.0, 18.0)];
        _statusItem.button.image = image;
        _statusItem.button.imageScaling = NSImageScaleProportionallyDown;
    } else {
        // 图标资源异常时仍保留可操作入口，避免应用只能从活动监视器退出。
        _statusItem.button.title = @"ES";
    }
    _statusItem.button.toolTip = @"EasyShare - 局域网文件传输";

    NSMenu *menu = [[[NSMenu alloc] initWithTitle:@"EasyShare"] autorelease];
    [menu setAutoenablesItems:NO];

    NSMenuItem *openItem = [menu addItemWithTitle:@"打开主窗口"
                                           action:@selector(openWindow:)
                                    keyEquivalent:@""];
    [openItem setTarget:self];

    [menu addItem:[NSMenuItem separatorItem]];

    _statusMenuItem = [[menu addItemWithTitle:@"服务状态：启动中…"
                                       action:nil
                                keyEquivalent:@""] retain];
    [_statusMenuItem setEnabled:NO];

    [menu addItem:[NSMenuItem separatorItem]];

    NSMenuItem *quitItem = [menu addItemWithTitle:@"退出 EasyShare"
                                           action:@selector(quitApplication:)
                                    keyEquivalent:@""];
    [quitItem setTarget:self];

    [_statusItem setMenu:menu];
}

- (void)setStatusTitle:(NSString *)title {
    [_statusMenuItem setTitle:title];
}

- (void)openWindow:(id)sender {
    easyshareTrayOpen();
}

- (void)quitApplication:(id)sender {
    easyshareTrayQuit();
}

- (void)dealloc {
    if (_statusItem != nil) {
        [[NSStatusBar systemStatusBar] removeStatusItem:_statusItem];
        [_statusItem release];
    }
    [_statusMenuItem release];
    [super dealloc];
}

@end

static void easyshare_tray_start_on_main(void *context) {
    NSData *iconData = (NSData *)context;
    @autoreleasepool {
        if (easyShareTrayController == nil) {
            easyShareTrayController = [[EasyShareTrayController alloc] init];
            [easyShareTrayController installWithIconData:iconData];
        }
        easyshareTrayReady();
    }
    [iconData release];
}

static void easyshare_tray_set_status_on_main(void *context) {
    NSString *title = (NSString *)context;
    @autoreleasepool {
        [easyShareTrayController setStatusTitle:title];
    }
    [title release];
}

void easyshare_tray_start(const void *icon_bytes, size_t icon_length) {
    // NSData 在当前线程同步复制 Go 内存，dispatch 后不再持有 Go 指针。
    NSData *iconData = [[NSData alloc] initWithBytes:icon_bytes length:icon_length];
    dispatch_async_f(dispatch_get_main_queue(), iconData, easyshare_tray_start_on_main);
}

void easyshare_tray_set_status(const char *status_utf8) {
    // NSString 同步复制 C 字符串，Go 可在函数返回后立即释放 CString。
    NSString *title = [[NSString alloc] initWithUTF8String:status_utf8];
    if (title == nil) {
        title = [@"服务状态：未知" retain];
    }
    dispatch_async_f(dispatch_get_main_queue(), title, easyshare_tray_set_status_on_main);
}
