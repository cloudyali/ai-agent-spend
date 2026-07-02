#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#import "bar.h"
#import "_cgo_export.h"

// AISBar owns the status item, the popover, and the web view, and is the web view's
// navigation delegate so it can intercept aispend://<action> links.
@interface AISBar : NSObject <WKNavigationDelegate>
@property (strong) NSStatusItem *item;
@property (strong) NSPopover *popover;
@property (strong) WKWebView *web;
@end

@implementation AISBar

- (void)toggle:(id)sender {
    if (self.popover.isShown) {
        [self.popover performClose:sender];
        return;
    }
    NSStatusBarButton *b = self.item.button;
    [self.popover showRelativeToRect:b.bounds ofView:b preferredEdge:NSRectEdgeMinY];
    [self.web.window makeKeyWindow];
}

- (void)webView:(WKWebView *)webView
    decidePolicyForNavigationAction:(WKNavigationAction *)action
                    decisionHandler:(void (^)(WKNavigationActionPolicy))decisionHandler {
    NSURL *u = action.request.URL;
    if ([u.scheme isEqualToString:@"aispend"]) {
        goAction((char *)u.host.UTF8String); // "refresh" | "quit"
        decisionHandler(WKNavigationActionPolicyCancel);
        return;
    }
    decisionHandler(WKNavigationActionPolicyAllow);
}

- (void)webView:(WKWebView *)webView didFinishNavigation:(WKNavigation *)navigation {
    // Shrink-wrap the popover to the rendered content so a short (idle-heavy) view never shows
    // an empty gap below it. Runs on every load, so the height re-fits on each refresh.
    NSPopover *popover = self.popover; // self is the delegate instance; captured by the block
    [webView evaluateJavaScript:@"document.body.scrollHeight" completionHandler:^(id result, NSError *error) {
        if (![result isKindOfClass:[NSNumber class]]) {
            return; // measurement failed — leave the current size be
        }
        CGFloat h = [result doubleValue];
        if (h < 1) {
            return;
        }
        // evaluateJavaScript delivers its completion handler on the main thread, so touching
        // AppKit here is safe. Width stays 320 to match the body.
        popover.contentSize = NSMakeSize(320, h);
    }];
}

@end

static AISBar *gBar;

void RunBar(void) {
    @autoreleasepool {
        NSApplication *app = [NSApplication sharedApplication];
        [app setActivationPolicy:NSApplicationActivationPolicyAccessory];

        gBar = [[AISBar alloc] init];
        gBar.item = [[NSStatusBar systemStatusBar] statusItemWithLength:NSVariableStatusItemLength];
        gBar.item.button.title = @"AiSpend";
        gBar.item.button.target = gBar;
        gBar.item.button.action = @selector(toggle:);

        WKWebViewConfiguration *cfg = [[WKWebViewConfiguration alloc] init];
        gBar.web = [[WKWebView alloc] initWithFrame:NSMakeRect(0, 0, 320, 420) configuration:cfg];
        gBar.web.navigationDelegate = gBar;

        NSViewController *vc = [[NSViewController alloc] init];
        vc.view = gBar.web;

        gBar.popover = [[NSPopover alloc] init];
        gBar.popover.contentViewController = vc;
        gBar.popover.contentSize = NSMakeSize(320, 420);
        gBar.popover.behavior = NSPopoverBehaviorTransient;

        [app run];
    }
}

// The NSString is built synchronously (before Go frees the C string); the block retains
// it under ARC, so it stays valid when the main queue runs the update later.
void SetTitle(const char *s) {
    NSString *t = [NSString stringWithUTF8String:s];
    dispatch_async(dispatch_get_main_queue(), ^{ gBar.item.button.title = t; });
}

void SetHTML(const char *s) {
    NSString *h = [NSString stringWithUTF8String:s];
    // A real (never-fetched) base URL gives the document a normal origin. With baseURL:nil the
    // origin is opaque, and WebKit silently drops clicks on our aispend:// links — so the
    // navigation delegate never fires and Refresh/Quit do nothing. No network happens: the HTML
    // has no external references, so WebKit never loads this URL.
    NSURL *base = [NSURL URLWithString:@"https://aispend.local/"];
    dispatch_async(dispatch_get_main_queue(), ^{ [gBar.web loadHTMLString:h baseURL:base]; });
}

void QuitBar(void) {
    dispatch_async(dispatch_get_main_queue(), ^{ [NSApp terminate:nil]; });
}
