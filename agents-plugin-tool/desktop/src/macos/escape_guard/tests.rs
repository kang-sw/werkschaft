//! Tests for the Esc guard.

use std::{
    sync::atomic::{AtomicUsize, Ordering},
    time::{Duration, Instant},
};

use objc2::{
    define_class, msg_send,
    rc::Retained,
    runtime::{AnyClass, AnyObject, NSObject},
    sel, ClassType,
};

use super::{
    add_window_method, cancel_operation, drop_key_down, drops_key, escape_triggered,
    exit_full_screen_mode, refuses_exit, swallows_cancel, Guard, Pending, WindowMethod, ALLOW_EXIT,
    ESCAPE_RECENT, KEY_ESCAPE, LAST_ESCAPE, PENDING_ESCAPES,
};
use crate::macos::STYLE_MASK_FULL_SCREEN;

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
    let add =
        |name, selector, method: WindowMethod| add_window_method(object, name, selector, method);
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
