use std::sync::{Arc, OnceLock};

use block2::Block;
use objc2::{
    define_class, ffi, msg_send,
    rc::Weak,
    runtime::{AnyClass, AnyObject, NSObject, ProtocolObject, Sel},
    sel, DefinedClass, MainThreadOnly,
};
use objc2_foundation::{ns_string, MainThreadMarker, NSError, NSObjectProtocol, NSString};
use objc2_web_kit::{
    WKNavigation, WKNavigationAction, WKNavigationActionPolicy, WKNavigationDelegate,
    WKNavigationResponse, WKNavigationResponsePolicy, WKScriptMessage, WKScriptMessageHandler,
    WKUserContentController, WKWebView,
};
use tauri::{AppHandle, Manager, WebviewWindow};
use url::Url;

use crate::{
    close,
    navigation::{self, Destination, Popup},
    notify,
    shell::Connection,
    updates, webview_setup_failed,
};

struct NavigationState {
    original: Weak<ProtocolObject<dyn WKNavigationDelegate>>,
    connection: std::sync::Weak<Connection>,
    dashboard: Option<(AppHandle, usize)>,
}

#[derive(Clone, Copy)]
enum Bridge {
    Update,
    Notify,
}

struct BridgeState {
    app: AppHandle,
    epoch: usize,
    bridge: Bridge,
}

define_class!(
    #[unsafe(super(NSObject))]
    #[thread_kind = MainThreadOnly]
    #[ivars = BridgeState]
    struct BridgeMessages;

    unsafe impl NSObjectProtocol for BridgeMessages {}

    unsafe impl WKScriptMessageHandler for BridgeMessages {
        #[unsafe(method(userContentController:didReceiveScriptMessage:))]
        fn receive(&self, _: &WKUserContentController, message: &WKScriptMessage) {
            let frame = unsafe { message.frameInfo() };
            if !unsafe { frame.isMainFrame() } {
                return;
            }
            let raw = unsafe { frame.request().URL().and_then(|url| url.absoluteString()) };
            let Some(source) = raw.and_then(|url| Url::parse(&url.to_string()).ok()) else {
                return;
            };
            let Ok(text) = (unsafe { message.body() }).downcast::<NSString>() else {
                return;
            };
            let state = self.ivars();
            if let Some(window) = state.app.get_webview_window("main") {
                let text = text.to_string();
                match state.bridge {
                    Bridge::Update => updates::receive(window, state.epoch, true, source, &text),
                    Bridge::Notify => notify::receive(window, state.epoch, source, &text),
                }
            }
        }
    }
);

define_class!(
    #[unsafe(super(NSObject))]
    #[thread_kind = MainThreadOnly]
    #[ivars = NavigationState]
    struct NavigationDelegate;

    unsafe impl NSObjectProtocol for NavigationDelegate {}

    unsafe impl WKNavigationDelegate for NavigationDelegate {
        #[unsafe(method(webView:decidePolicyForNavigationAction:decisionHandler:))]
        fn action(
            &self,
            webview: &WKWebView,
            action: &WKNavigationAction,
            reply: &Block<dyn Fn(WKNavigationActionPolicy)>,
        ) {
            let raw = unsafe { action.request().URL().and_then(|url| url.absoluteString()) };
            let allowed = raw
                .and_then(|url| Url::parse(&url.to_string()).ok())
                .is_some_and(|url| {
                    let target = unsafe { action.targetFrame().map(|frame| frame.isMainFrame()) };
                    self.allows(url, target)
                });
            if allowed {
                if let Some(original) = self.ivars().original.load() {
                    unsafe {
                        original.webView_decidePolicyForNavigationAction_decisionHandler(
                            webview, action, reply,
                        );
                    }
                    return;
                }
            }
            reply.call((WKNavigationActionPolicy::Cancel,));
        }

        #[unsafe(method(webView:decidePolicyForNavigationResponse:decisionHandler:))]
        fn response(
            &self,
            webview: &WKWebView,
            response: &WKNavigationResponse,
            reply: &Block<dyn Fn(WKNavigationResponsePolicy)>,
        ) {
            let raw = unsafe {
                response
                    .response()
                    .URL()
                    .and_then(|url| url.absoluteString())
            };
            let allowed = raw
                .and_then(|url| Url::parse(&url.to_string()).ok())
                .is_some_and(|url| self.allows(url, Some(unsafe { response.isForMainFrame() })));
            if allowed {
                if let Some(original) = self.ivars().original.load() {
                    unsafe {
                        original.webView_decidePolicyForNavigationResponse_decisionHandler(
                            webview, response, reply,
                        );
                    }
                    return;
                }
            }
            reply.call((WKNavigationResponsePolicy::Cancel,));
        }

        #[unsafe(method(webView:didCommitNavigation:))]
        fn committed(&self, webview: &WKWebView, navigation: Option<&WKNavigation>) {
            if let Some(original) = self.ivars().original.load() {
                unsafe {
                    original.webView_didCommitNavigation(webview, navigation);
                }
            }
        }

        #[unsafe(method(webView:didFinishNavigation:))]
        fn finished(&self, webview: &WKWebView, navigation: Option<&WKNavigation>) {
            if let Some(original) = self.ivars().original.load() {
                unsafe {
                    original.webView_didFinishNavigation(webview, navigation);
                }
            }
        }

        #[unsafe(method(webView:didFailProvisionalNavigation:withError:))]
        fn provisional_failed(&self, _: &WKWebView, _: Option<&WKNavigation>, error: &NSError) {
            self.load_failed(error);
        }

        #[unsafe(method(webView:didFailNavigation:withError:))]
        fn failed(&self, _: &WKWebView, _: Option<&WKNavigation>, error: &NSError) {
            self.load_failed(error);
        }

        #[unsafe(method(webViewWebContentProcessDidTerminate:))]
        fn renderer_failed(&self, _: &WKWebView) {
            self.recover("Dashboard renderer stopped");
        }
    }
);

impl NavigationDelegate {
    fn allows(&self, url: Url, target: Option<bool>) -> bool {
        let pin = self
            .ivars()
            .connection
            .upgrade()
            .and_then(|state| state.pinned_url());
        if target == Some(false) {
            return navigation::frame_allowed(&url, pin.as_ref());
        }
        if target.is_none() {
            // WKUIDelegate handles these through the shared popup policy.
            return navigation::popup(&url, pin.as_ref()) != Popup::Block;
        }
        match navigation::destination(&url, pin.as_ref()) {
            Destination::Dashboard | Destination::Local => true,
            Destination::Browser => {
                navigation::open_browser(url);
                false
            }
            Destination::Block => false,
        }
    }

    fn load_failed(&self, error: &NSError) {
        // NSURLErrorCancelled and WebKit's policy-change interruption occur
        // when an otherwise healthy navigation was deliberately canceled.
        let code = error.code();
        if !matches!(code, -999 | 102) {
            self.recover("Dashboard navigation failed");
        }
    }

    fn recover(&self, reason: &'static str) {
        let (Some(connection), Some((app, epoch))) = (
            self.ivars().connection.upgrade(),
            self.ivars().dashboard.clone(),
        ) else {
            return;
        };
        // Wait until WebKit returns from its delegate callback before loading.
        std::thread::spawn(move || {
            let target = app.clone();
            let _ = app.run_on_main_thread(move || {
                if connection.window_epoch() == epoch {
                    if let Some(window) = target.get_webview_window(close::MAIN_WINDOW) {
                        connection.recover(window, reason);
                    }
                }
            });
        });
    }
}

// NSWindowStyleMaskFullScreen. The bit is set only while the window is in
// native (green-button / Ctrl+Cmd+F) full screen.
const STYLE_MASK_FULL_SCREEN: usize = 1 << 14;

/// Whether an unhandled Esc (AppKit `cancelOperation:`) must stop at the web
/// view instead of travelling on to NSWindow, which leaves full screen.
fn swallows_cancel(style_mask: usize) -> bool {
    style_mask & STYLE_MASK_FULL_SCREEN != 0
}

// Superclass of Wry's web view, where the override's `super` call starts.
static WRY_SUPER: OnceLock<&'static AnyClass> = OnceLock::new();
static ESCAPE_GUARD: OnceLock<()> = OnceLock::new();

unsafe extern "C-unwind" fn cancel_operation(this: &AnyObject, _: Sel, sender: Option<&AnyObject>) {
    let window: Option<objc2::rc::Retained<AnyObject>> = unsafe { msg_send![this, window] };
    let mask: usize = window.map_or(0, |window| unsafe { msg_send![&*window, styleMask] });
    if swallows_cancel(mask) {
        return;
    }
    if let Some(superclass) = WRY_SUPER.get() {
        let _: () = unsafe { msg_send![super(this, *superclass), cancelOperation: sender] };
    }
}

/// Keep Esc from leaving native full screen.
///
/// Wry makes its `WryWebViewParent` the window's content view (replacing
/// tao's view, whose own `cancelOperation:` would otherwise end the responder
/// chain), so an Esc the page leaves unhandled reaches AppKit as
/// `cancelOperation:` and the window exits full screen. Esc still reaches the
/// page first; only the leftover command is dropped while in full screen.
///
/// The method is added to Wry's own class once, never by swapping an object's
/// isa: an observed view (KVO) reports its original class while its real isa
/// is a runtime subclass, and replacing that isa drops the observation.
fn guard_escape(webview: &WKWebView) {
    ESCAPE_GUARD.get_or_init(|| {
        let object: &AnyObject = webview;
        let mut class = Some(object.class());
        while let Some(current) = class {
            if current.name() == c"WryWebView" {
                let Some(superclass) = current.superclass() else {
                    return;
                };
                let _ = WRY_SUPER.set(superclass);
                // Returns NO, adding nothing, if the class already defines
                // cancelOperation: itself.
                let added = unsafe {
                    ffi::class_addMethod(
                        (current as *const AnyClass).cast_mut(),
                        sel!(cancelOperation:),
                        std::mem::transmute::<
                            unsafe extern "C-unwind" fn(&AnyObject, Sel, Option<&AnyObject>),
                            objc2::runtime::Imp,
                        >(cancel_operation),
                        c"v@:@".as_ptr(),
                    )
                };
                if !added.as_bool() {
                    eprintln!("Werkschaft: Could not guard Esc in full screen");
                }
                return;
            }
            class = current.superclass();
        }
        eprintln!("Werkschaft: Web view class not recognized; Esc may leave full screen");
    });
}

pub fn install(
    window: &WebviewWindow,
    connection: Arc<Connection>,
    ready: impl FnOnce(WebviewWindow) + Send + 'static,
) -> tauri::Result<()> {
    static DELEGATE_KEY: u8 = 0;
    let target = window.clone();
    window.with_webview(move |platform| {
        let marker = MainThreadMarker::new().expect("with_webview runs on the main thread");
        // The pinned Tauri runtime supplies a WKWebView on the main thread.
        let webview = unsafe { &*platform.inner().cast::<WKWebView>() };
        let Some(original) = (unsafe { webview.navigationDelegate() }) else {
            webview_setup_failed(&target, &connection);
            return;
        };
        guard_escape(webview);
        let main = close::checks_draft(target.label());
        let epoch = connection.window_epoch();
        let delegate = marker
            .alloc::<NavigationDelegate>()
            .set_ivars(NavigationState {
                original: Weak::new(&original),
                connection: Arc::downgrade(&connection),
                dashboard: main.then(|| (target.app_handle().clone(), epoch)),
            });
        let delegate: objc2::rc::Retained<NavigationDelegate> =
            unsafe { msg_send![super(delegate), init] };
        let protocol = ProtocolObject::from_ref(&*delegate);
        unsafe {
            // WKWebView's delegate property is weak. Associate the replacement
            // with its view; the original remains owned by Wry.
            ffi::objc_setAssociatedObject(
                (webview as *const WKWebView).cast_mut().cast(),
                std::ptr::from_ref(&DELEGATE_KEY).cast(),
                (protocol as *const ProtocolObject<dyn WKNavigationDelegate>)
                    .cast_mut()
                    .cast(),
                ffi::OBJC_ASSOCIATION_RETAIN_NONATOMIC,
            );
            webview.setNavigationDelegate(Some(protocol));
            let bridges = [
                (
                    Bridge::Update,
                    ns_string!("werkschaftUpdate"),
                    target.try_state::<Arc<updates::Controller>>().is_some(),
                ),
                (
                    Bridge::Notify,
                    ns_string!("werkschaftNotify"),
                    target.try_state::<Arc<notify::Notifier>>().is_some(),
                ),
            ];
            for (bridge, name, managed) in bridges {
                if !main || !managed {
                    continue;
                }
                let messages = marker.alloc::<BridgeMessages>().set_ivars(BridgeState {
                    app: target.app_handle().clone(),
                    epoch,
                    bridge,
                });
                let messages: objc2::rc::Retained<BridgeMessages> =
                    msg_send![super(messages), init];
                let controller = webview.configuration().userContentController();
                controller.removeScriptMessageHandlerForName(name);
                controller.addScriptMessageHandler_name(ProtocolObject::from_ref(&*messages), name);
            }
        }
        ready(target);
    })
}

#[cfg(test)]
mod tests {
    use super::{swallows_cancel, STYLE_MASK_FULL_SCREEN};

    #[test]
    fn escape_is_swallowed_only_in_native_full_screen() {
        // titled | closable | miniaturizable | resizable: an ordinary window.
        let windowed = 1 | 2 | 4 | 8;
        assert!(!swallows_cancel(windowed));
        assert!(swallows_cancel(windowed | STYLE_MASK_FULL_SCREEN));
        assert!(!swallows_cancel(0));
    }

    #[test]
    fn full_screen_bit_matches_appkit() {
        // NSWindowStyleMaskFullScreen == 1 << 14 (16384) in AppKit's NSWindow.h.
        assert_eq!(STYLE_MASK_FULL_SCREEN, 16384);
    }
}
