use std::{
    cell::RefCell,
    ffi::{c_char, c_int, CStr, CString},
    sync::{Arc, OnceLock},
};

use block2::{Block, RcBlock};
use objc2::{
    class, define_class, ffi, msg_send,
    rc::{Retained, Weak},
    runtime::{AnyClass, AnyObject, NSObject, ProtocolObject, Sel},
    sel, DefinedClass, MainThreadOnly, Message,
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

// Runtime name of tao's NSWindow subclass (`#[name = "TaoWindow"]` in tao's
// macOS window.rs), which every Tauri window here is an instance of.
const WINDOW_CLASS: &CStr = c"TaoWindow";

/// Whether a `cancelOperation:` reaching the window must be dropped instead
/// of running NSWindow's own implementation, which leaves full screen.
fn swallows_cancel(style_mask: usize) -> bool {
    style_mask & STYLE_MASK_FULL_SCREEN != 0
}

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

// Superclass of the guarded window class, where an allowed cancel continues.
static WINDOW_SUPER: OnceLock<&'static AnyClass> = OnceLock::new();
static ESCAPE_GUARD: OnceLock<()> = OnceLock::new();

unsafe extern "C-unwind" fn cancel_operation(this: &AnyObject, _: Sel, sender: Option<&AnyObject>) {
    let mask: usize = unsafe { msg_send![this, styleMask] };
    if swallows_cancel(mask) {
        log("Werkschaft: Esc cancelOperation: dropped to keep full screen");
        return;
    }
    if let Some(superclass) = WINDOW_SUPER.get() {
        let _: () = unsafe { msg_send![super(this, *superclass), cancelOperation: sender] };
    }
}

#[derive(Debug, PartialEq, Eq)]
enum Guard {
    Installed,
    /// The matched class defines `cancelOperation:` itself.
    AlreadyDefined,
    /// A guard is already installed on some class.
    AlreadyInstalled,
    /// No class in the object's hierarchy has the expected name.
    NotFound,
}

/// Add the full-screen `cancelOperation:` guard to the class named `name` in
/// `object`'s hierarchy. The method is added to the class once, never by
/// swapping an object's isa: an observed object (KVO) has a runtime subclass
/// as its real isa, and replacing that isa drops the observation. Walking up
/// from the real isa finds the named class beneath any such subclass.
fn add_cancel_guard(object: &AnyObject, name: &CStr) -> Guard {
    if WINDOW_SUPER.get().is_some() {
        return Guard::AlreadyInstalled;
    }
    let mut class = Some(object.class());
    while let Some(current) = class {
        if current.name() == name {
            let Some(superclass) = current.superclass() else {
                return Guard::NotFound;
            };
            // Returns NO, adding nothing, if the class already defines
            // cancelOperation: itself; inherited definitions are overridden.
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
                return Guard::AlreadyDefined;
            }
            // Main thread only, so nothing calls the guard before this is set.
            let _ = WINDOW_SUPER.set(superclass);
            return Guard::Installed;
        }
        class = current.superclass();
    }
    Guard::NotFound
}

// kVK_Escape in Carbon's HIToolbox/Events.h.
const KEY_ESCAPE: u16 = 53;
// NSEventMaskKeyDown: 1 << NSEventTypeKeyDown (10).
const EVENT_MASK_KEY_DOWN: u64 = 1 << 10;
// Esc keyDowns remembered while WebKit decides whether the page handled them.
const PENDING_ESCAPES: usize = 8;

/// Whether the key monitor drops a keyDown: only WebKit's re-send of an Esc
/// the page left unhandled, and only in native full screen.
fn drops_key(key_code: u16, resent: bool, style_mask: usize) -> bool {
    key_code == KEY_ESCAPE && resent && style_mask & STYLE_MASK_FULL_SCREEN != 0
}

/// Esc keyDowns seen once. A second sighting of the same event is WebKit's
/// re-send; it is forgotten then. The oldest entry goes once full, since the
/// page handled it and WebKit never re-sent it.
struct Pending<T>(Vec<T>);

impl<T: PartialEq> Pending<T> {
    const fn new() -> Self {
        Self(Vec::new())
    }

    fn resent(&mut self, event: T) -> bool {
        if let Some(index) = self.0.iter().position(|seen| *seen == event) {
            self.0.remove(index);
            return true;
        }
        if self.0.len() == PENDING_ESCAPES {
            self.0.remove(0);
        }
        self.0.push(event);
        false
    }
}

/// An NSEvent compared by identity: WebKit re-sends the very object.
struct EventRef(Retained<AnyObject>);

impl PartialEq for EventRef {
    fn eq(&self, other: &Self) -> bool {
        std::ptr::eq(&*self.0, &*other.0)
    }
}

thread_local! {
    static ESCAPES: RefCell<Pending<EventRef>> = const { RefCell::new(Pending::new()) };
}

/// Local monitor body; true drops the event before AppKit dispatches it.
fn drop_key_down(event: &AnyObject) -> bool {
    let key_code: u16 = unsafe { msg_send![event, keyCode] };
    if key_code != KEY_ESCAPE {
        return false;
    }
    let resent = ESCAPES.with(|escapes| escapes.borrow_mut().resent(EventRef(event.retain())));
    let window: Option<Retained<AnyObject>> = unsafe { msg_send![event, window] };
    let mask: usize = window.map_or(0, |window| unsafe { msg_send![&*window, styleMask] });
    let drops = drops_key(key_code, resent, mask);
    if drops {
        log("Werkschaft: Esc re-send dropped to keep full screen (page saw the key)");
    }
    drops
}

/// Drop an unhandled Esc before AppKit can act on it in full screen.
///
/// WKWebView passes every key to the page first. When the page leaves it
/// unhandled, WebKit hands the same NSEvent back to `-[NSApplication
/// sendEvent:]`, and whichever AppKit path then acts on Esc (key equivalent,
/// `keyDown:`, `cancelOperation:`) can leave full screen. A local monitor runs
/// at the top of `sendEvent:` for both passes; it lets the first through and
/// drops only the re-send while in full screen, so page Esc handlers still run.
fn monitor_escape() -> bool {
    let block = RcBlock::new(|event: *mut AnyObject| -> *mut AnyObject {
        match unsafe { event.as_ref() } {
            Some(key) if drop_key_down(key) => std::ptr::null_mut(),
            _ => event,
        }
    });
    let monitor: Option<Retained<AnyObject>> = unsafe {
        msg_send![
            class!(NSEvent),
            addLocalMonitorForEventsMatchingMask: EVENT_MASK_KEY_DOWN,
            handler: &*block
        ]
    };
    // AppKit keeps the monitor; it stays for the life of the app.
    monitor.map(std::mem::forget).is_some()
}

/// Keep Esc from leaving native full screen.
///
/// The key monitor drops WebKit's re-send of an unhandled Esc. The window
/// guard also drops any `cancelOperation:` that still reaches the window in
/// full screen: NSWindow implements it, and nothing between the web view and
/// the window (Wry's views, NSView, NSResponder) does.
fn guard_escape(webview: &WKWebView) {
    ESCAPE_GUARD.get_or_init(|| {
        if monitor_escape() {
            log("Werkschaft: Esc full-screen key monitor installed");
        } else {
            log("Werkschaft: Esc full-screen key monitor not installed");
        }
        let window: Option<Retained<AnyObject>> = unsafe { msg_send![webview, window] };
        let outcome = window.map_or(Guard::NotFound, |window| {
            add_cancel_guard(&window, WINDOW_CLASS)
        });
        log(match outcome {
            Guard::Installed => "Werkschaft: Esc full-screen window guard installed on TaoWindow",
            Guard::AlreadyDefined => {
                "Werkschaft: Esc window guard not installed: TaoWindow defines cancelOperation:"
            }
            Guard::AlreadyInstalled => {
                "Werkschaft: Esc window guard not installed: already installed"
            }
            Guard::NotFound => "Werkschaft: Esc window guard not installed: no TaoWindow",
        });
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
    use std::sync::atomic::{AtomicUsize, Ordering};

    use objc2::{
        define_class, msg_send,
        rc::Retained,
        runtime::{AnyClass, AnyObject, NSObject},
        sel, ClassType,
    };

    use super::{
        add_cancel_guard, drop_key_down, drops_key, swallows_cancel, Guard, Pending, KEY_ESCAPE,
        PENDING_ESCAPES, STYLE_MASK_FULL_SCREEN,
    };

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

    fn defines(class: &AnyClass, name: &str) -> bool {
        class
            .instance_methods()
            .iter()
            .any(|method| method.name().name().to_str() == Ok(name))
    }

    #[test]
    fn appkit_cancels_only_at_the_window() {
        // The guard rests on these: NSWindow owns cancelOperation:, and no
        // view class between the web view and the window responds to it.
        let window = AnyClass::get(c"NSWindow").expect("AppKit is linked");
        assert!(defines(window, "cancelOperation:"));
        for name in [c"NSView", c"NSResponder", c"WKWebView"] {
            let class = AnyClass::get(name).expect("framework is linked");
            assert!(class.instance_method(sel!(cancelOperation:)).is_none());
        }
    }

    static MASK: AtomicUsize = AtomicUsize::new(0);
    static CANCELS: AtomicUsize = AtomicUsize::new(0);

    define_class!(
        // Stands in for NSWindow: owns cancelOperation: and reports a mask.
        #[unsafe(super(NSObject))]
        #[name = "WerkschaftTestWindowBase"]
        struct WindowBase;

        impl WindowBase {
            #[unsafe(method(styleMask))]
            fn style_mask(&self) -> usize {
                MASK.load(Ordering::SeqCst)
            }

            #[unsafe(method(cancelOperation:))]
            fn cancel(&self, _: Option<&AnyObject>) {
                CANCELS.fetch_add(1, Ordering::SeqCst);
            }
        }
    );

    define_class!(
        // Stands in for TaoWindow: inherits cancelOperation:.
        #[unsafe(super(WindowBase, NSObject))]
        #[name = "WerkschaftTestWindow"]
        struct Window;
    );

    #[test]
    fn guard_drops_cancel_only_in_full_screen() {
        // Runs as one test: the guard's superclass is process-wide.
        let window: Retained<Window> = unsafe { msg_send![Window::class(), new] };
        let object: &AnyObject = &window;
        let cancel = || {
            let _: () = unsafe { msg_send![object, cancelOperation: None::<&AnyObject>] };
            CANCELS.load(Ordering::SeqCst)
        };

        assert_eq!(add_cancel_guard(object, c"NoSuchWindow"), Guard::NotFound);
        assert_eq!(
            add_cancel_guard(object, c"WerkschaftTestWindowBase"),
            Guard::AlreadyDefined
        );
        assert_eq!(cancel(), 1, "nothing installed yet");

        assert_eq!(
            add_cancel_guard(object, c"WerkschaftTestWindow"),
            Guard::Installed
        );
        MASK.store(1 | 2 | 4 | 8, Ordering::SeqCst);
        assert_eq!(cancel(), 2, "windowed: the cancel reaches the superclass");
        MASK.store(1 | 2 | 4 | 8 | STYLE_MASK_FULL_SCREEN, Ordering::SeqCst);
        assert_eq!(cancel(), 2, "full screen: the cancel is dropped");
        MASK.store(1, Ordering::SeqCst);
        assert_eq!(cancel(), 3, "back to windowed");

        assert_eq!(
            add_cancel_guard(object, c"WerkschaftTestWindow"),
            Guard::AlreadyInstalled
        );
    }

    const FULL: usize = 1 | 2 | 4 | 8 | STYLE_MASK_FULL_SCREEN;

    #[test]
    fn only_a_resent_escape_in_full_screen_is_dropped() {
        assert!(drops_key(KEY_ESCAPE, true, FULL));
        assert!(
            !drops_key(KEY_ESCAPE, false, FULL),
            "first pass reaches the page"
        );
        assert!(!drops_key(KEY_ESCAPE, true, FULL & !STYLE_MASK_FULL_SCREEN));
        assert!(!drops_key(36, true, FULL), "Return is never touched");
    }

    #[test]
    fn pending_escapes_recognize_resends_in_any_order() {
        let mut pending = Pending::new();
        assert!(!pending.resent(1));
        assert!(!pending.resent(2), "a second Esc before WebKit answers");
        assert!(pending.resent(1), "re-send of the first");
        assert!(pending.resent(2), "re-send of the second");
        assert!(!pending.resent(1), "forgotten once re-sent");
    }

    #[test]
    fn pending_escapes_are_bounded() {
        let mut pending = Pending::new();
        for event in 0..=PENDING_ESCAPES {
            assert!(!pending.resent(event));
        }
        assert!(!pending.resent(0), "the oldest was evicted");
        assert!(pending.resent(PENDING_ESCAPES));
    }

    static KEY_CODE: AtomicUsize = AtomicUsize::new(0);
    static KEY_WINDOW_MASK: AtomicUsize = AtomicUsize::new(0);

    define_class!(
        // Stands in for the key window: reports its style mask.
        #[unsafe(super(NSObject))]
        #[name = "WerkschaftTestKeyWindow"]
        struct KeyWindow;

        impl KeyWindow {
            #[unsafe(method(styleMask))]
            fn style_mask(&self) -> usize {
                KEY_WINDOW_MASK.load(Ordering::SeqCst)
            }
        }
    );

    define_class!(
        // Stands in for an NSEvent: a key code and a window.
        #[unsafe(super(NSObject))]
        #[name = "WerkschaftTestKeyEvent"]
        struct KeyEvent;

        impl KeyEvent {
            #[unsafe(method(keyCode))]
            fn key_code(&self) -> u16 {
                KEY_CODE.load(Ordering::SeqCst) as u16
            }

            #[unsafe(method_id(window))]
            fn window(&self) -> Option<Retained<KeyWindow>> {
                Some(unsafe { msg_send![KeyWindow::class(), new] })
            }
        }
    );

    #[test]
    fn monitor_drops_only_the_resend_of_an_escape() {
        let new_event = || -> Retained<KeyEvent> { unsafe { msg_send![KeyEvent::class(), new] } };
        KEY_WINDOW_MASK.store(FULL, Ordering::SeqCst);
        KEY_CODE.store(KEY_ESCAPE.into(), Ordering::SeqCst);

        let first = new_event();
        let second = new_event();
        assert!(!drop_key_down(&first), "the page gets the Esc");
        assert!(!drop_key_down(&second));
        assert!(drop_key_down(&first), "unhandled re-send is dropped");
        assert!(drop_key_down(&second));

        KEY_WINDOW_MASK.store(FULL & !STYLE_MASK_FULL_SCREEN, Ordering::SeqCst);
        let windowed = new_event();
        assert!(!drop_key_down(&windowed));
        assert!(
            !drop_key_down(&windowed),
            "outside full screen AppKit keeps it"
        );

        KEY_WINDOW_MASK.store(FULL, Ordering::SeqCst);
        KEY_CODE.store(36, Ordering::SeqCst);
        let other = new_event();
        assert!(!drop_key_down(&other));
        assert!(!drop_key_down(&other), "other keys pass both times");
    }
}
