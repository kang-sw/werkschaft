//! The Esc guard that keeps native full screen from being left by Esc.

use std::{
    cell::{Cell, RefCell},
    ffi::CStr,
    sync::OnceLock,
    time::{Duration, Instant},
};

use block2::{Block, RcBlock};
use objc2::{
    class, ffi, msg_send,
    rc::Retained,
    runtime::{AnyClass, AnyObject, Sel},
    sel, Message,
};
use objc2_foundation::NSString;
use objc2_web_kit::WKWebView;

use super::{
    log,
    tray_hide::{cancel_pending_hide, notification_center},
    STYLE_MASK_FULL_SCREEN,
};

// Runtime name of tao's NSWindow subclass (`#[name = "TaoWindow"]` in tao's
// macOS window.rs), which every Tauri window here is an instance of.
const WINDOW_CLASS: &CStr = c"TaoWindow";

/// Whether a `cancelOperation:` reaching the window must be dropped instead
/// of running NSWindow's own implementation, which leaves full screen.
fn swallows_cancel(style_mask: usize) -> bool {
    style_mask & STYLE_MASK_FULL_SCREEN != 0
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
    pub(super) static LAST_ESCAPE: Cell<Option<Instant>> = const { Cell::new(None) };
    // Set while close-to-tray takes the window out of full screen.
    pub(super) static ALLOW_EXIT: Cell<bool> = const { Cell::new(false) };
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
pub(super) enum Guard {
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
pub(super) fn guard_escape(webview: &WKWebView) {
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
        let outcome = guard_exit_failure(&window);
        log(&format!(
            "Werkschaft: full-screen exit failure hook windowDidFailToExitFullScreen: on TaoWindowDelegate: {outcome:?}"
        ));
    });
}

/// AppKit reports a refused or failed programmatic full-screen exit only to
/// the window delegate (`windowDidFailToExitFullScreen:`), with no
/// notification, and no did-exit follows. Drop the pending hide and the Esc
/// bypass so a later close starts the exit again.
pub(super) unsafe extern "C-unwind" fn window_did_fail_to_exit_full_screen(
    _: &AnyObject,
    _: Sel,
    _: Option<&AnyObject>,
) {
    log("Werkschaft: full-screen exit failed; the pending close-to-tray hide is dropped");
    cancel_pending_hide();
}

// Runtime name of tao's window delegate (`#[name = "TaoWindowDelegate"]` in
// tao's macOS window_delegate.rs); it does not implement the failure method.
const DELEGATE_CLASS: &CStr = c"TaoWindowDelegate";

/// Add `windowDidFailToExitFullScreen:` to the delegate's class. Nothing is
/// forwarded: the delegate class does not define the method.
fn guard_exit_failure(window: &AnyObject) -> Guard {
    let delegate: Option<Retained<AnyObject>> = unsafe { msg_send![window, delegate] };
    match delegate {
        Some(delegate) => add_exit_failure_method(&delegate, DELEGATE_CLASS),
        None => Guard::NotFound,
    }
}

/// The class named `name` in `delegate`'s hierarchy gets the failure method.
pub(super) fn add_exit_failure_method(delegate: &AnyObject, name: &CStr) -> Guard {
    let mut class = Some(delegate.class());
    while let Some(current) = class {
        if current.name() == name {
            // NO, adding nothing, if the class already defines the method.
            let added = unsafe {
                ffi::class_addMethod(
                    (current as *const AnyClass).cast_mut(),
                    sel!(windowDidFailToExitFullScreen:),
                    std::mem::transmute::<WindowMethod, objc2::runtime::Imp>(
                        window_did_fail_to_exit_full_screen as WindowMethod,
                    ),
                    c"v@:@".as_ptr(),
                )
            };
            return if added.as_bool() {
                Guard::Installed
            } else {
                Guard::AlreadyDefined
            };
        }
        class = current.superclass();
    }
    Guard::NotFound
}

extern "C" {
    // AppKit; posted with the window as object around a full-screen exit.
    pub(super) static NSWindowWillExitFullScreenNotification: &'static NSString;
    pub(super) static NSWindowDidExitFullScreenNotification: &'static NSString;
}

#[cfg(test)]
mod tests;
