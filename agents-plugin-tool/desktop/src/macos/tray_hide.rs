//! Close-to-tray: leaves native full screen before hiding the main window.

use std::cell::RefCell;

use block2::RcBlock;
use objc2::{class, msg_send, rc::Retained, runtime::AnyObject};
use tauri::WebviewWindow;

use super::{
    escape_guard::{NSWindowDidExitFullScreenNotification, ALLOW_EXIT},
    log, STYLE_MASK_FULL_SCREEN,
};

#[derive(Debug, PartialEq, Eq)]
enum CloseStep {
    Hide,
    /// Leave full screen first; hiding a full-screen window leaves its Space
    /// black instead of returning to the desktop.
    ExitFullScreen,
    /// A hide already waits on the exit; hiding now, mid-transition, leaves
    /// the Space black, and toggling again would re-enter full screen.
    Wait,
}

/// What hiding to the tray does now. A repeated close while full screen is
/// still being left changes nothing: the pending hide runs once the exit
/// ends.
fn close_step(style_mask: usize, pending: bool) -> CloseStep {
    if pending {
        CloseStep::Wait
    } else if style_mask & STYLE_MASK_FULL_SCREEN != 0 {
        CloseStep::ExitFullScreen
    } else {
        CloseStep::Hide
    }
}

thread_local! {
    // Observer token of a hide waiting for the window to leave full screen.
    static PENDING_HIDE: RefCell<Option<Retained<AnyObject>>> = const { RefCell::new(None) };
}

pub(super) fn notification_center() -> Retained<AnyObject> {
    unsafe { msg_send![class!(NSNotificationCenter), defaultCenter] }
}

/// Forget a pending hide; true if one was waiting.
pub(super) fn cancel_pending_hide() -> bool {
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
    let pending = PENDING_HIDE.with(|pending| pending.borrow().is_some());
    let Ok(raw) = window.ns_window() else {
        cancel_pending_hide();
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
        CloseStep::Wait => {
            log("Werkschaft: close while leaving full screen; the pending hide stays");
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

#[cfg(test)]
mod tests {
    use std::{
        cell::Cell,
        rc::Rc,
        sync::atomic::{AtomicUsize, Ordering},
    };

    use objc2::{
        define_class, msg_send,
        rc::Retained,
        runtime::{AnyObject, NSObject},
        sel, ClassType,
    };

    use super::{
        cancel_pending_hide, close_step, exit_full_screen_then, notification_center, CloseStep,
        PENDING_HIDE,
    };
    use crate::macos::{
        escape_guard::{
            add_exit_failure_method, window_did_fail_to_exit_full_screen, Guard,
            NSWindowDidExitFullScreenNotification, ALLOW_EXIT,
        },
        STYLE_MASK_FULL_SCREEN,
    };

    const FULL: usize = 1 | 2 | 4 | 8 | STYLE_MASK_FULL_SCREEN;

    #[test]
    fn full_screen_close_exits_first_and_a_repeated_close_waits() {
        let windowed = 1 | 2 | 4 | 8;
        assert_eq!(close_step(windowed, false), CloseStep::Hide);
        assert_eq!(close_step(FULL, false), CloseStep::ExitFullScreen);
        assert_eq!(close_step(FULL, true), CloseStep::Wait);
        assert_eq!(close_step(windowed, true), CloseStep::Wait);
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

    define_class!(
        // Stands in for a full-screen NSWindow whose exit never finishes.
        #[unsafe(super(NSObject))]
        #[name = "WerkschaftTestStuckWindow"]
        struct StuckWindow;

        impl StuckWindow {
            #[unsafe(method(toggleFullScreen:))]
            fn toggle(&self, _: Option<&AnyObject>) {}
        }
    );

    #[test]
    fn a_failed_exit_drops_the_pending_hide_and_a_later_close_retries() {
        let window: Retained<StuckWindow> = unsafe { msg_send![StuckWindow::class(), new] };
        let hides = Rc::new(Cell::new(0));
        let counter = hides.clone();
        exit_full_screen_then(&window, move || counter.set(counter.get() + 1));
        assert!(ALLOW_EXIT.with(Cell::get), "the Esc bypass is armed");
        assert!(PENDING_HIDE.with(|pending| pending.borrow().is_some()));

        // AppKit's delegate call; no did-exit follows.
        unsafe { window_did_fail_to_exit_full_screen(&window, sel!(new), None) };
        assert!(!ALLOW_EXIT.with(Cell::get), "the Esc bypass is disarmed");
        assert!(PENDING_HIDE.with(|pending| pending.borrow().is_none()));
        assert_eq!(hides.get(), 0, "nothing was hidden");

        // A later close is not Wait again.
        assert_eq!(close_step(FULL, false), CloseStep::ExitFullScreen);
        let counter = hides.clone();
        let retry: Retained<FullScreenWindow> =
            unsafe { msg_send![FullScreenWindow::class(), new] };
        exit_full_screen_then(&retry, move || counter.set(counter.get() + 1));
        assert_eq!(hides.get(), 1, "the retried exit hides once it ends");
    }

    static FAILURES: AtomicUsize = AtomicUsize::new(0);

    define_class!(
        // Stands in for a window with a delegate: reports a stand-in delegate.
        #[unsafe(super(NSObject))]
        #[name = "WerkschaftTestDelegate"]
        struct Delegate;
    );

    #[test]
    fn the_failure_hook_is_added_to_the_delegate_class_once() {
        let delegate: Retained<Delegate> = unsafe { msg_send![Delegate::class(), new] };
        let failed = || {
            let _: () =
                unsafe { msg_send![&*delegate, windowDidFailToExitFullScreen: None::<&AnyObject>] };
        };
        assert_eq!(
            add_exit_failure_method(&delegate, c"NoSuchDelegate"),
            Guard::NotFound
        );
        assert_eq!(
            add_exit_failure_method(&delegate, c"WerkschaftTestDelegate"),
            Guard::Installed
        );
        // The installed method is the real one: it drops a pending hide.
        let window: Retained<StuckWindow> = unsafe { msg_send![StuckWindow::class(), new] };
        exit_full_screen_then(&window, || {
            FAILURES.fetch_add(1, Ordering::SeqCst);
        });
        failed();
        assert!(!cancel_pending_hide(), "the failure call dropped it");
        assert_eq!(FAILURES.load(Ordering::SeqCst), 0);
        assert_eq!(
            add_exit_failure_method(&delegate, c"WerkschaftTestDelegate"),
            Guard::AlreadyDefined
        );
    }
}
