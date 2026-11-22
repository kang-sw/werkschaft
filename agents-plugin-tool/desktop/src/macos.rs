use std::{
    cell::{Cell, RefCell},
    ffi::{c_char, c_int, CStr, CString},
    sync::{Arc, OnceLock},
    time::{Duration, Instant},
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

// Superclass of the guarded window class, where an allowed call continues.
static WINDOW_SUPER: OnceLock<&'static AnyClass> = OnceLock::new();
static ESCAPE_GUARD: OnceLock<()> = OnceLock::new();

extern "C" {
    // AppKit's NSApp: nil until NSApplication exists (always, in the app).
    static NSApp: *mut AnyObject;
}

// NSEventTypeKeyDown / NSEventTypeKeyUp.
const EVENT_KEY_DOWN: usize = 10;
const EVENT_KEY_UP: usize = 11;
// Stack frames logged per full-screen exit attempt.
const STACK_FRAMES: usize = 40;
// How long after an Esc keyDown a full-screen exit counts as caused by it.
const ESCAPE_RECENT: Duration = Duration::from_millis(500);

thread_local! {
    // When the key monitor last saw an Esc keyDown.
    static LAST_ESCAPE: Cell<Option<Instant>> = const { Cell::new(None) };
    // Set while close-to-tray takes the window out of full screen.
    static ALLOW_EXIT: Cell<bool> = const { Cell::new(false) };
    // Windows that left full screen on Esc without passing the method guard.
    static REENTER: RefCell<Vec<usize>> = const { RefCell::new(Vec::new()) };
}

/// Whether a full-screen exit counts as caused by Esc: the event being
/// handled is an Esc key, or an Esc keyDown was seen just before.
fn escape_triggered(current_key: Option<u16>, since_escape: Option<Duration>) -> bool {
    current_key == Some(KEY_ESCAPE) || since_escape.is_some_and(|elapsed| elapsed < ESCAPE_RECENT)
}

/// Whether a full-screen exit is refused: Esc caused it and close-to-tray
/// did not ask for it.
fn refuses_exit(escape: bool, allowed: bool) -> bool {
    escape && !allowed
}

/// `[NSApp currentEvent]` as its type and, for key events, its key code.
fn current_event() -> Option<(usize, Option<u16>)> {
    let app = unsafe { NSApp.as_ref() }?;
    let event: Option<Retained<AnyObject>> = unsafe { msg_send![app, currentEvent] };
    let event = event?;
    let kind: usize = unsafe { msg_send![&*event, type] };
    let key = matches!(kind, EVENT_KEY_DOWN | EVENT_KEY_UP)
        .then(|| -> u16 { unsafe { msg_send![&*event, keyCode] } });
    Some((kind, key))
}

/// Whether the full-screen exit under way counts as caused by Esc, and a
/// description of why for the log.
fn exit_trigger() -> (bool, String) {
    let current = current_event();
    let since = LAST_ESCAPE.with(Cell::get).map(|at| at.elapsed());
    let escape = escape_triggered(current.and_then(|(_, key)| key), since);
    let event = match current {
        Some((kind, Some(key))) => format!("type {kind} keyCode {key}"),
        Some((kind, None)) => format!("type {kind}"),
        None => "none".to_owned(),
    };
    let since = since.map_or("never".to_owned(), |elapsed| {
        format!("{} ms ago", elapsed.as_millis())
    });
    let bypass = ALLOW_EXIT.with(Cell::get);
    let description =
        format!("current event {event}; last Esc keyDown {since}; close-to-tray {bypass}; Esc-caused {escape}");
    (escape, description)
}

/// The current call stack, one log line per frame.
fn log_stack(context: &str) {
    let symbols: Retained<AnyObject> = unsafe { msg_send![class!(NSThread), callStackSymbols] };
    let count: usize = unsafe { msg_send![&*symbols, count] };
    for index in 0..count.min(STACK_FRAMES) {
        let frame: Retained<NSString> = unsafe { msg_send![&*symbols, objectAtIndex: index] };
        log(&format!("Werkschaft: {context} stack {frame}"));
    }
}

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

/// `-[NSWindow toggleFullScreen:]` leaves full screen by sending
/// `exitFullScreenMode:`, which starts the exit transition; guarding it here
/// refuses an Esc-caused exit from either entry.
unsafe extern "C-unwind" fn exit_full_screen_mode(
    this: &AnyObject,
    _: Sel,
    sender: Option<&AnyObject>,
) {
    let (escape, description) = exit_trigger();
    if refuses_exit(escape, ALLOW_EXIT.with(Cell::get)) {
        log(&format!(
            "Werkschaft: Esc full-screen exit refused at exitFullScreenMode: ({description})"
        ));
        log_stack("refused exit");
        return;
    }
    log(&format!(
        "Werkschaft: full-screen exit allowed at exitFullScreenMode: ({description})"
    ));
    log_stack("allowed exit");
    if let Some(superclass) = WINDOW_SUPER.get() {
        let _: () = unsafe { msg_send![super(this, *superclass), exitFullScreenMode: sender] };
    }
}

type WindowMethod = unsafe extern "C-unwind" fn(&AnyObject, Sel, Option<&AnyObject>);

#[derive(Debug, PartialEq, Eq)]
enum Guard {
    Installed,
    /// The matched class defines the method itself.
    AlreadyDefined,
    /// Guards already live on a class with another superclass.
    AlreadyInstalled,
    /// No class in the object's hierarchy has the expected name.
    NotFound,
}

/// Add a `void (id sender)` window method to the class named `name` in
/// `object`'s hierarchy, forwarding to that class's superclass. The method
/// is added to the class, never by swapping an object's isa: an observed
/// object (KVO) has a runtime subclass as its real isa, and replacing that
/// isa drops the observation. Walking up from the real isa finds the named
/// class beneath any such subclass.
fn add_window_method(
    object: &AnyObject,
    name: &CStr,
    selector: Sel,
    method: WindowMethod,
) -> Guard {
    let mut class = Some(object.class());
    while let Some(current) = class {
        if current.name() == name {
            let Some(superclass) = current.superclass() else {
                return Guard::NotFound;
            };
            if WINDOW_SUPER
                .get()
                .is_some_and(|guarded| !std::ptr::eq(*guarded, superclass))
            {
                return Guard::AlreadyInstalled;
            }
            // Returns NO, adding nothing, if the class already defines the
            // method itself; inherited definitions are overridden.
            let added = unsafe {
                ffi::class_addMethod(
                    (current as *const AnyClass).cast_mut(),
                    selector,
                    std::mem::transmute::<WindowMethod, objc2::runtime::Imp>(method),
                    c"v@:@".as_ptr(),
                )
            };
            if !added.as_bool() {
                return Guard::AlreadyDefined;
            }
            // Main thread only, so nothing calls the method before this is set.
            let _ = WINDOW_SUPER.set(superclass);
            return Guard::Installed;
        }
        class = current.superclass();
    }
    Guard::NotFound
}

// kVK_Escape in Carbon's HIToolbox/Events.h.
const KEY_ESCAPE: u16 = 53;
// NSEventMaskKeyDown | NSEventMaskKeyUp: 1 << type.
const EVENT_MASK_KEYS: u64 = (1 << EVENT_KEY_DOWN) | (1 << EVENT_KEY_UP);
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
/// Only Esc is looked at, and only Esc is logged: other key codes stay out
/// of the system log.
fn drop_key_down(event: &AnyObject) -> bool {
    let key_code: u16 = unsafe { msg_send![event, keyCode] };
    if key_code != KEY_ESCAPE {
        return false;
    }
    let kind: usize = unsafe { msg_send![event, type] };
    let window: Option<Retained<AnyObject>> = unsafe { msg_send![event, window] };
    let mask: usize = window.map_or(0, |window| unsafe { msg_send![&*window, styleMask] });
    let full_screen = mask & STYLE_MASK_FULL_SCREEN != 0;
    if kind != EVENT_KEY_DOWN {
        log(&format!(
            "Werkschaft: Esc keyUp seen by the key monitor (full screen {full_screen})"
        ));
        return false;
    }
    LAST_ESCAPE.with(|last| last.set(Some(Instant::now())));
    let resent = ESCAPES.with(|escapes| escapes.borrow_mut().resent(EventRef(event.retain())));
    let drops = drops_key(key_code, resent, mask);
    let pass = if resent { "re-send" } else { "first pass" };
    let action = if drops { "dropped" } else { "passed" };
    log(&format!(
        "Werkschaft: Esc keyDown {pass} {action} by the key monitor (full screen {full_screen})"
    ));
    drops
}

/// Drop an unhandled Esc before AppKit can act on it in full screen.
///
/// WKWebView passes every key to the page first. When the page leaves it
/// unhandled, WebKit hands the same NSEvent back to `-[NSApplication
/// sendEvent:]`. A local monitor runs at the top of `sendEvent:` for both
/// passes; it lets the first through and drops only the re-send while in
/// full screen, so page Esc handlers still run.
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
            addLocalMonitorForEventsMatchingMask: EVENT_MASK_KEYS,
            handler: &*block
        ]
    };
    // AppKit keeps the monitor; it stays for the life of the app.
    monitor.map(std::mem::forget).is_some()
}

fn notification_window(notification: *mut AnyObject) -> Option<Retained<AnyObject>> {
    let notification = unsafe { notification.as_ref() }?;
    unsafe { msg_send![notification, object] }
}

fn observe(name: &NSString, block: &Block<dyn Fn(*mut AnyObject)>) {
    let token: Option<Retained<AnyObject>> = unsafe {
        msg_send![
            &*notification_center(),
            addObserverForName: name,
            object: None::<&AnyObject>,
            queue: None::<&AnyObject>,
            usingBlock: block
        ]
    };
    // Observers stay for the life of the app.
    if let Some(token) = token {
        std::mem::forget(token);
    }
}

/// Log every full-screen exit with its cause and stack. An Esc-caused exit
/// that got past the method guard is undone by re-entering full screen once
/// it ends: a visible flicker, the last resort if some path skips
/// `exitFullScreenMode:`.
fn observe_exits() {
    let will = RcBlock::new(|notification: *mut AnyObject| {
        let (escape, description) = exit_trigger();
        log(&format!(
            "Werkschaft: window will exit full screen ({description})"
        ));
        log_stack("will exit");
        if refuses_exit(escape, ALLOW_EXIT.with(Cell::get)) {
            if let Some(window) = notification_window(notification) {
                let address = Retained::as_ptr(&window) as usize;
                REENTER.with(|reenter| reenter.borrow_mut().push(address));
                log("Werkschaft: Esc exit got past exitFullScreenMode:; full screen is re-entered once it ends");
            }
        }
    });
    let finished = |outcome: &'static str| {
        RcBlock::new(move |notification: *mut AnyObject| {
            // Close-to-tray's exit is over.
            ALLOW_EXIT.with(|allow| allow.set(false));
            let Some(window) = notification_window(notification) else {
                return;
            };
            let address = Retained::as_ptr(&window) as usize;
            let reenter = REENTER.with(|reenter| {
                let mut reenter = reenter.borrow_mut();
                let found = reenter.iter().position(|seen| *seen == address);
                found.map(|index| reenter.remove(index)).is_some()
            });
            log(&format!(
                "Werkschaft: window {outcome} full screen (re-enter {reenter})"
            ));
            if reenter && outcome == "did exit" {
                let _: () = unsafe { msg_send![&*window, toggleFullScreen: None::<&AnyObject>] };
            }
        })
    };
    unsafe {
        observe(NSWindowWillExitFullScreenNotification, &will);
        observe(NSWindowDidExitFullScreenNotification, &finished("did exit"));
    }
}

/// Keep Esc from leaving native full screen.
///
/// The method guard on `exitFullScreenMode:` refuses an Esc-caused exit.
/// The key monitor drops WebKit's re-send of an unhandled Esc, the
/// `cancelOperation:` guard drops that command in full screen, and the exit
/// observers log every exit and undo one that got past the guard.
fn guard_escape(webview: &WKWebView) {
    ESCAPE_GUARD.get_or_init(|| {
        if monitor_escape() {
            log("Werkschaft: Esc full-screen key monitor installed");
        } else {
            log("Werkschaft: Esc full-screen key monitor not installed");
        }
        observe_exits();
        log("Werkschaft: full-screen exit observers installed");
        let window: Option<Retained<AnyObject>> = unsafe { msg_send![webview, window] };
        let Some(window) = window else {
            log("Werkschaft: Esc window guards not installed: web view has no window");
            return;
        };
        for (selector, method, label) in [
            (
                sel!(exitFullScreenMode:),
                exit_full_screen_mode as WindowMethod,
                "exitFullScreenMode:",
            ),
            (
                sel!(cancelOperation:),
                cancel_operation as WindowMethod,
                "cancelOperation:",
            ),
        ] {
            let outcome = add_window_method(&window, WINDOW_CLASS, selector, method);
            log(&format!(
                "Werkschaft: Esc window guard {label} on TaoWindow: {outcome:?}"
            ));
        }
    });
}

extern "C" {
    // AppKit; posted with the window as object around a full-screen exit.
    static NSWindowWillExitFullScreenNotification: &'static NSString;
    static NSWindowDidExitFullScreenNotification: &'static NSString;
}

#[derive(Debug, PartialEq, Eq)]
enum CloseStep {
    Hide,
    /// Leave full screen first; hiding a full-screen window leaves its Space
    /// black instead of returning to the desktop.
    ExitFullScreen,
}

/// What hiding to the tray does now. A second close while full screen is
/// still being left hides at once rather than toggling back into it.
fn close_step(style_mask: usize, pending: bool) -> CloseStep {
    if style_mask & STYLE_MASK_FULL_SCREEN != 0 && !pending {
        CloseStep::ExitFullScreen
    } else {
        CloseStep::Hide
    }
}

thread_local! {
    // Observer token of a hide waiting for the window to leave full screen.
    static PENDING_HIDE: RefCell<Option<Retained<AnyObject>>> = const { RefCell::new(None) };
}

fn notification_center() -> Retained<AnyObject> {
    unsafe { msg_send![class!(NSNotificationCenter), defaultCenter] }
}

/// Forget a pending hide; true if one was waiting.
fn cancel_pending_hide() -> bool {
    ALLOW_EXIT.with(|allow| allow.set(false));
    let Some(token) = PENDING_HIDE.with(|pending| pending.borrow_mut().take()) else {
        return false;
    };
    let _: () = unsafe { msg_send![&*notification_center(), removeObserver: &*token] };
    true
}

/// Take `ns_window` out of native full screen and run `hide` once it is out.
fn exit_full_screen_then(ns_window: &AnyObject, hide: impl Fn() + 'static) {
    let block = RcBlock::new(move |_: *mut AnyObject| {
        if cancel_pending_hide() {
            hide();
        }
    });
    let token: Option<Retained<AnyObject>> = unsafe {
        msg_send![
            &*notification_center(),
            addObserverForName: NSWindowDidExitFullScreenNotification,
            object: ns_window,
            queue: None::<&AnyObject>,
            usingBlock: &*block
        ]
    };
    PENDING_HIDE.with(|pending| *pending.borrow_mut() = token);
    // The Esc exit guard lets this exit through; cleared when it ends.
    ALLOW_EXIT.with(|allow| allow.set(true));
    let _: () = unsafe { msg_send![ns_window, toggleFullScreen: None::<&AnyObject>] };
}

/// Hide the main window to the tray, leaving native full screen first so the
/// user lands on the desktop. Restoring shows the window windowed.
pub fn hide_to_tray(window: &WebviewWindow) {
    let pending = cancel_pending_hide();
    let Ok(raw) = window.ns_window() else {
        let _ = window.hide();
        return;
    };
    // On the main thread Tauri returns the live NSWindow pointer directly.
    let ns_window = unsafe { &*raw.cast::<AnyObject>() };
    let mask: usize = unsafe { msg_send![ns_window, styleMask] };
    match close_step(mask, pending) {
        CloseStep::Hide => {
            let _ = window.hide();
        }
        CloseStep::ExitFullScreen => {
            log("Werkschaft: leaving full screen before hiding to the tray");
            let target = window.clone();
            exit_full_screen_then(ns_window, move || {
                let _ = target.hide();
            });
        }
    }
}

/// Showing the main window again cancels a hide still waiting on full screen.
/// Main thread only: the pending hide is main-thread state.
pub fn keep_shown() {
    cancel_pending_hide();
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

    use std::{cell::Cell, rc::Rc};

    use std::time::{Duration, Instant};

    use super::{
        add_window_method, cancel_operation, cancel_pending_hide, close_step, drop_key_down,
        drops_key, escape_triggered, exit_full_screen_mode, exit_full_screen_then,
        notification_center, refuses_exit, swallows_cancel, CloseStep, Guard,
        NSWindowDidExitFullScreenNotification, Pending, WindowMethod, ALLOW_EXIT, ESCAPE_RECENT,
        KEY_ESCAPE, LAST_ESCAPE, PENDING_ESCAPES, STYLE_MASK_FULL_SCREEN,
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
        // toggleFullScreen: leaves full screen through exitFullScreenMode:,
        // which the exit guard overrides; both are NSWindow's own.
        assert!(defines(window, "exitFullScreenMode:"));
        assert!(defines(window, "toggleFullScreen:"));
        for name in [c"NSView", c"NSResponder", c"WKWebView"] {
            let class = AnyClass::get(name).expect("framework is linked");
            assert!(class.instance_method(sel!(cancelOperation:)).is_none());
        }
    }

    static MASK: AtomicUsize = AtomicUsize::new(0);
    static CANCELS: AtomicUsize = AtomicUsize::new(0);
    static EXITS: AtomicUsize = AtomicUsize::new(0);

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

            #[unsafe(method(exitFullScreenMode:))]
            fn exit(&self, _: Option<&AnyObject>) {
                EXITS.fetch_add(1, Ordering::SeqCst);
            }
        }
    );

    define_class!(
        // Stands in for TaoWindow: inherits both methods.
        #[unsafe(super(WindowBase, NSObject))]
        #[name = "WerkschaftTestWindow"]
        struct Window;
    );

    #[test]
    fn exit_is_refused_only_when_esc_caused_it() {
        let soon = Some(Duration::from_millis(10));
        let late = Some(ESCAPE_RECENT + Duration::from_millis(1));
        assert!(
            escape_triggered(Some(KEY_ESCAPE), None),
            "Esc is the current event"
        );
        assert!(escape_triggered(None, soon), "an Esc keyDown just before");
        assert!(
            escape_triggered(Some(3), soon),
            "Ctrl+Cmd+F right after an Esc"
        );
        assert!(!escape_triggered(Some(3), late), "Ctrl+Cmd+F");
        assert!(!escape_triggered(None, None), "green button or menu click");
        assert!(refuses_exit(true, false));
        assert!(!refuses_exit(true, true), "close-to-tray asked for it");
        assert!(!refuses_exit(false, false));
    }

    #[test]
    fn window_guards_refuse_only_what_they_should() {
        // Runs as one test: the guards' superclass is process-wide.
        let window: Retained<Window> = unsafe { msg_send![Window::class(), new] };
        let object: &AnyObject = &window;
        let cancel = || {
            let _: () = unsafe { msg_send![object, cancelOperation: None::<&AnyObject>] };
            CANCELS.load(Ordering::SeqCst)
        };
        let exit = || {
            let _: () = unsafe { msg_send![object, exitFullScreenMode: None::<&AnyObject>] };
            EXITS.load(Ordering::SeqCst)
        };
        let add = |name, selector, method: WindowMethod| {
            add_window_method(object, name, selector, method)
        };
        let cancel_guard = cancel_operation as WindowMethod;
        let exit_guard = exit_full_screen_mode as WindowMethod;

        assert_eq!(
            add(c"NoSuchWindow", sel!(cancelOperation:), cancel_guard),
            Guard::NotFound
        );
        assert_eq!(
            add(
                c"WerkschaftTestWindowBase",
                sel!(cancelOperation:),
                cancel_guard
            ),
            Guard::AlreadyDefined
        );
        assert_eq!(cancel(), 1, "nothing installed yet");

        assert_eq!(
            add(
                c"WerkschaftTestWindow",
                sel!(cancelOperation:),
                cancel_guard
            ),
            Guard::Installed
        );
        MASK.store(1 | 2 | 4 | 8, Ordering::SeqCst);
        assert_eq!(cancel(), 2, "windowed: the cancel reaches the superclass");
        MASK.store(FULL, Ordering::SeqCst);
        assert_eq!(cancel(), 2, "full screen: the cancel is dropped");
        MASK.store(1, Ordering::SeqCst);
        assert_eq!(cancel(), 3, "back to windowed");
        assert_eq!(
            add(
                c"WerkschaftTestWindow",
                sel!(cancelOperation:),
                cancel_guard
            ),
            Guard::AlreadyDefined
        );

        // The same class takes the exit guard; no NSApp exists in tests, so
        // only the recent-Esc record marks an exit as Esc-caused.
        assert_eq!(
            add(
                c"WerkschaftTestWindow",
                sel!(exitFullScreenMode:),
                exit_guard
            ),
            Guard::Installed
        );
        LAST_ESCAPE.with(|last| last.set(None));
        assert_eq!(exit(), 1, "no Esc: the exit goes through");
        LAST_ESCAPE.with(|last| last.set(Some(Instant::now())));
        assert_eq!(exit(), 1, "right after Esc: refused");
        ALLOW_EXIT.with(|allow| allow.set(true));
        assert_eq!(exit(), 2, "close-to-tray: goes through");
        ALLOW_EXIT.with(|allow| allow.set(false));
        LAST_ESCAPE.with(|last| last.set(None));
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
            #[unsafe(method(type))]
            fn kind(&self) -> usize {
                10
            }

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

    #[test]
    fn full_screen_close_exits_first_and_a_second_close_hides() {
        let windowed = 1 | 2 | 4 | 8;
        assert_eq!(close_step(windowed, false), CloseStep::Hide);
        assert_eq!(close_step(FULL, false), CloseStep::ExitFullScreen);
        assert_eq!(close_step(FULL, true), CloseStep::Hide);
        assert_eq!(close_step(windowed, true), CloseStep::Hide);
    }

    define_class!(
        // Stands in for a full-screen NSWindow: toggling posts the exit.
        #[unsafe(super(NSObject))]
        #[name = "WerkschaftTestFullScreenWindow"]
        struct FullScreenWindow;

        impl FullScreenWindow {
            #[unsafe(method(toggleFullScreen:))]
            fn toggle(&self, _: Option<&AnyObject>) {
                let _: () = unsafe {
                    msg_send![
                        &*notification_center(),
                        postNotificationName: NSWindowDidExitFullScreenNotification,
                        object: self
                    ]
                };
            }
        }
    );

    #[test]
    fn hide_runs_once_after_leaving_full_screen() {
        let window: Retained<FullScreenWindow> =
            unsafe { msg_send![FullScreenWindow::class(), new] };
        let hides = Rc::new(Cell::new(0));
        let counter = hides.clone();
        exit_full_screen_then(&window, move || counter.set(counter.get() + 1));
        assert_eq!(hides.get(), 1, "hidden once the exit notification arrived");
        assert!(!cancel_pending_hide(), "the observer removed itself");

        let _: () = unsafe { msg_send![&*window, toggleFullScreen: None::<&AnyObject>] };
        assert_eq!(hides.get(), 1, "a later exit does not hide again");
    }
}
