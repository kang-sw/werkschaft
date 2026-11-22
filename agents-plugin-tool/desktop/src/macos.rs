mod escape_guard;
mod tray_hide;
mod webview_delegate;

pub use tray_hide::{hide_to_tray, keep_shown};

use std::{
    ffi::{c_char, c_int, CString},
    sync::Arc,
};

use objc2::{ffi, msg_send, rc::Weak, runtime::ProtocolObject};
use objc2_foundation::{ns_string, MainThreadMarker};
use objc2_web_kit::{WKNavigationDelegate, WKWebView};
use tauri::{Manager, WebviewWindow};

use crate::{close, notify, shell::Connection, updates, webview_setup_failed};
use escape_guard::guard_escape;
use webview_delegate::{Bridge, BridgeMessages, BridgeState, NavigationDelegate, NavigationState};

// NSWindowStyleMaskFullScreen. The bit is set only while the window is in
// native (green-button / Ctrl+Cmd+F) full screen.
const STYLE_MASK_FULL_SCREEN: usize = 1 << 14;

extern "C" {
    // libc; variadic.
    fn syslog(priority: c_int, format: *const c_char, ...);
}

// LOG_NOTICE in <syslog.h>.
const LOG_NOTICE: c_int = 5;

/// One line in the unified log; stderr is lost for a Finder-launched app.
/// syslog's `%s` argument is logged in the clear, where NSLog's arguments
/// (and objc2's static NSStrings) show as `<private>`.
fn log(message: &str) {
    let Ok(message) = CString::new(message) else {
        return;
    };
    unsafe { syslog(LOG_NOTICE, c"%s".as_ptr(), message.as_ptr()) };
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
