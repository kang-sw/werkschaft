use std::{
    cell::RefCell,
    sync::atomic::{AtomicBool, AtomicUsize, Ordering},
};

use objc2::{
    define_class, msg_send,
    rc::{autoreleasepool, Retained},
    runtime::{AnyObject, NSObject},
    ClassType,
};
use objc2_foundation::NSString;

use super::{
    fire, hidden_script, nudge_script, observe_window, window_visible,
    NSWindowDidChangeOcclusionStateNotification, NSWindowDidEnterFullScreenNotification, Nudge,
    Step, Transition, OBSERVED, OCCLUSION_VISIBLE,
};
use crate::macos::{
    escape_guard::NSWindowDidExitFullScreenNotification, tray_hide::notification_center,
};

#[test]
fn only_an_ordered_in_unminiaturized_window_with_a_visible_part_counts() {
    assert!(window_visible(OCCLUSION_VISIBLE, true, false));
    assert!(
        !window_visible(0, true, false),
        "fully occluded or on another Space"
    );
    assert!(
        !window_visible(OCCLUSION_VISIBLE, false, false),
        "ordered out (tray hide)"
    );
    assert!(
        !window_visible(OCCLUSION_VISIBLE, true, true),
        "in the Dock"
    );
}

#[test]
fn full_screen_transitions_arm_even_while_still_occluded_and_fire_checks_again() {
    let mut nudge = Nudge::default();
    assert_eq!(
        nudge.transition(Transition::ExitFullScreen, false),
        Step::Arm
    );
    assert_eq!(
        nudge.fire(false),
        None,
        "still not visible when the timer fires: nothing"
    );
    assert_eq!(nudge.fire(true), None, "the pending transition was used up");
    assert_eq!(
        nudge.transition(Transition::EnterFullScreen, false),
        Step::Arm
    );
    assert_eq!(nudge.fire(true), Some(Transition::EnterFullScreen));
    assert!(nudge.told_visible);
}

#[test]
fn a_burst_coalesces_into_one_nudge_for_the_latest_transition() {
    let mut nudge = Nudge::default();
    assert_eq!(
        nudge.transition(Transition::ExitFullScreen, true),
        Step::Arm
    );
    assert_eq!(nudge.transition(Transition::Occlusion, true), Step::Arm);
    assert_eq!(
        nudge.transition(Transition::EnterFullScreen, true),
        Step::Arm
    );
    assert_eq!(nudge.fire(true), Some(Transition::EnterFullScreen));
    assert_eq!(nudge.fire(true), None, "one nudge per burst");
}

#[test]
fn hiding_drops_a_pending_nudge_and_tells_the_page_once() {
    let mut nudge = Nudge::default();
    assert_eq!(
        nudge.transition(Transition::Occlusion, false),
        Step::Hidden { tell_page: false },
        "never reported visible: nothing to take back"
    );
    nudge.transition(Transition::Occlusion, true);
    nudge.fire(true);
    nudge.transition(Transition::ExitFullScreen, true);
    assert_eq!(
        nudge.transition(Transition::Occlusion, false),
        Step::Hidden { tell_page: true }
    );
    assert_eq!(nudge.fire(true), None, "the pending nudge was dropped");
    assert_eq!(
        nudge.transition(Transition::Occlusion, false),
        Step::Hidden { tell_page: false },
        "a repeated hidden report is not sent again"
    );
}

#[test]
fn scripts_are_guarded_for_pages_without_the_hooks() {
    assert_eq!(
        nudge_script("fullscreen-exit"),
        "window.__wsNativeVisibilityNudge&&window.__wsNativeVisibilityNudge(\"fullscreen-exit\")"
    );
    assert_eq!(
        hidden_script("occlusion"),
        "window.__wsNativeVisibilityHidden&&window.__wsNativeVisibilityHidden(\"occlusion\")"
    );
    for transition in [
        Transition::EnterFullScreen,
        Transition::ExitFullScreen,
        Transition::Occlusion,
    ] {
        let reason = transition.reason();
        assert!(reason.bytes().all(|b| b.is_ascii_lowercase() || b == b'-'));
    }
}

static OCCLUSION: AtomicUsize = AtomicUsize::new(0);
static MINIATURIZED: AtomicBool = AtomicBool::new(false);

thread_local! {
    static CALLS: RefCell<Vec<String>> = const { RefCell::new(Vec::new()) };
}

fn calls() -> Vec<String> {
    CALLS.with(|calls| std::mem::take(&mut *calls.borrow_mut()))
}

define_class!(
    // Stands in for an NSWindow: reports the occlusion state the test sets.
    #[unsafe(super(NSObject))]
    #[name = "WerkschaftTestNudgeWindow"]
    struct NudgeWindow;

    impl NudgeWindow {
        #[unsafe(method(occlusionState))]
        fn occlusion_state(&self) -> usize {
            OCCLUSION.load(Ordering::SeqCst)
        }

        #[unsafe(method(isVisible))]
        fn is_visible(&self) -> bool {
            true
        }

        #[unsafe(method(isMiniaturized))]
        fn is_miniaturized(&self) -> bool {
            MINIATURIZED.load(Ordering::SeqCst)
        }

        #[unsafe(method(contentView))]
        fn content_view(&self) -> *mut AnyObject {
            std::ptr::null_mut()
        }
    }
);

define_class!(
    // Stands in for the WKWebView: records redraw requests and scripts.
    #[unsafe(super(NSObject))]
    #[name = "WerkschaftTestNudgeWebView"]
    struct NudgeWebView;

    impl NudgeWebView {
        #[unsafe(method(setNeedsLayout:))]
        fn set_needs_layout(&self, _: bool) {
            CALLS.with(|calls| calls.borrow_mut().push("layout".to_owned()));
        }

        #[unsafe(method(setNeedsDisplay:))]
        fn set_needs_display(&self, _: bool) {
            CALLS.with(|calls| calls.borrow_mut().push("display".to_owned()));
        }

        #[unsafe(method(evaluateJavaScript:completionHandler:))]
        fn evaluate(
            &self,
            script: &NSString,
            _: *const block2::Block<dyn Fn(*mut AnyObject, *mut AnyObject)>,
        ) {
            CALLS.with(|calls| calls.borrow_mut().push(script.to_string()));
        }
    }
);

fn post(name: &NSString, window: &AnyObject) {
    // The pool releases the posted notification, which retains the window.
    autoreleasepool(|_| {
        let _: () = unsafe {
            msg_send![&*notification_center(), postNotificationName: name, object: window]
        };
    });
}

fn armed() -> (bool, Option<Transition>) {
    OBSERVED.with(|entries| {
        let entries = entries.borrow();
        let observed = &entries[0].observed;
        let armed = observed.timer.borrow().is_some();
        let pending = observed.nudge.borrow().pending;
        (armed, pending)
    })
}

#[test]
fn window_notifications_arm_cancel_and_nudge_through_the_observers() {
    let window: Retained<NudgeWindow> = unsafe { msg_send![NudgeWindow::class(), new] };
    let webview: Retained<NudgeWebView> = unsafe { msg_send![NudgeWebView::class(), new] };
    let other: Retained<NudgeWindow> = unsafe { msg_send![NudgeWindow::class(), new] };
    assert!(observe_window(&window, &webview), "installed");
    assert!(
        !observe_window(&window, &webview),
        "a re-install adds no second observer set"
    );

    OCCLUSION.store(0, Ordering::SeqCst);
    post(unsafe { NSWindowDidExitFullScreenNotification }, &window);
    assert_eq!(armed(), (true, Some(Transition::ExitFullScreen)));
    post(
        unsafe { NSWindowDidChangeOcclusionStateNotification },
        &window,
    );
    assert_eq!(
        armed(),
        (false, None),
        "still occluded: the pending nudge is dropped"
    );
    assert_eq!(
        calls(),
        Vec::<String>::new(),
        "the page was never told visible"
    );

    post(unsafe { NSWindowDidEnterFullScreenNotification }, &other);
    assert_eq!(
        armed(),
        (false, None),
        "another window's notification is not ours"
    );

    OCCLUSION.store(OCCLUSION_VISIBLE, Ordering::SeqCst);
    post(
        unsafe { NSWindowDidChangeOcclusionStateNotification },
        &window,
    );
    assert_eq!(armed(), (true, Some(Transition::Occlusion)));
    MINIATURIZED.store(true, Ordering::SeqCst);
    OBSERVED.with(|entries| fire(&entries.borrow()[0].observed));
    assert_eq!(
        calls(),
        Vec::<String>::new(),
        "minimized when the timer fired: nothing"
    );
    MINIATURIZED.store(false, Ordering::SeqCst);

    post(unsafe { NSWindowDidEnterFullScreenNotification }, &window);
    OBSERVED.with(|entries| fire(&entries.borrow()[0].observed));
    assert_eq!(
        calls(),
        vec![
            "layout".to_owned(),
            "display".to_owned(),
            nudge_script("fullscreen-enter"),
        ]
    );
    assert_eq!(armed(), (false, None));

    OCCLUSION.store(0, Ordering::SeqCst);
    post(
        unsafe { NSWindowDidChangeOcclusionStateNotification },
        &window,
    );
    assert_eq!(calls(), vec![hidden_script("occlusion")]);

    drop(window);
    let next: Retained<NudgeWindow> = unsafe { msg_send![NudgeWindow::class(), new] };
    assert!(observe_window(&next, &webview));
    assert_eq!(
        OBSERVED.with(|entries| entries.borrow().len()),
        1,
        "the deallocated window's entry and observers are gone"
    );
}
