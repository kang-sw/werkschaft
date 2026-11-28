//! Post-transition repaint after native full screen, Space and occlusion
//! changes.
//!
//! Entering or leaving native full screen moves the window to another Space.
//! Now and then WKWebView comes back from that transition blank: the page is
//! left with a stale `document.hidden === true` (paused, its region updates
//! held) that no later `visibilitychange` clears, and nothing invalidates the
//! view until devtools or a resize does. Once the window is verified visible
//! and the transition has settled, this invalidates the view and asks the page
//! to recheck its own visibility through `window.__wsNativeVisibilityNudge`;
//! when the window stops being visible it tells the page through
//! `window.__wsNativeVisibilityHidden`, so a real hide still pauses it.
//!
//! The nudge only requests a redraw and runs a guarded script. It never shows,
//! orders front, unhides, resizes or toggles full screen on a window, so the
//! close-to-tray hide (`tray_hide`) and the Esc full-screen guard
//! (`escape_guard`) are untouched; a window that tray_hide ordered out is
//! occluded, and a nudge rechecks visibility before it does anything.

use std::{cell::RefCell, rc::Rc};

use block2::RcBlock;
use objc2::{
    class, msg_send,
    rc::{Retained, Weak},
    runtime::AnyObject,
};
use objc2_foundation::NSString;
use objc2_web_kit::WKWebView;

use super::{
    escape_guard::NSWindowDidExitFullScreenNotification, log, tray_hide::notification_center,
};

/// How long after the last transition the nudge runs. AppKit posts the
/// full-screen notifications as the Space animation ends and WebKit updates
/// its own visibility on the same occlusion change; a short settle lets both
/// land first and coalesces a burst (exit, occlusion, re-enter) into one nudge.
const SETTLE_SECONDS: f64 = 0.15;

// NSWindowOcclusionStateVisible.
const OCCLUSION_VISIBLE: usize = 1 << 1;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum Transition {
    EnterFullScreen,
    ExitFullScreen,
    Occlusion,
}

impl Transition {
    /// The reason passed to the page; diagnostic only.
    fn reason(self) -> &'static str {
        match self {
            Transition::EnterFullScreen => "fullscreen-enter",
            Transition::ExitFullScreen => "fullscreen-exit",
            Transition::Occlusion => "occlusion",
        }
    }
}

#[derive(Debug, PartialEq, Eq)]
enum Step {
    /// (Re-)arm the settle timer, replacing a pending one.
    Arm,
    /// Drop a pending nudge; `tell_page` once after a visible report.
    Hidden { tell_page: bool },
}

/// Whether a window is on screen for the user: some part visible, ordered in
/// and not in the Dock. Miniaturized and ordered-out windows report occluded
/// too; the extra checks keep a stale occlusion bit from counting.
fn window_visible(occlusion: usize, ordered_in: bool, miniaturized: bool) -> bool {
    occlusion & OCCLUSION_VISIBLE != 0 && ordered_in && !miniaturized
}

/// One window's nudge state. Main thread only.
#[derive(Debug, Default)]
struct Nudge {
    /// The page was last told the window is visible.
    told_visible: bool,
    /// The latest transition waiting for the settle timer.
    pending: Option<Transition>,
}

impl Nudge {
    fn transition(&mut self, transition: Transition, visible: bool) -> Step {
        match transition {
            // Posted as the Space animation ends, when the window may still
            // read as occluded for a moment: visibility is checked when the
            // timer fires instead.
            Transition::EnterFullScreen | Transition::ExitFullScreen => {
                self.pending = Some(transition);
                Step::Arm
            }
            Transition::Occlusion if visible => {
                self.pending = Some(transition);
                Step::Arm
            }
            Transition::Occlusion => {
                self.pending = None;
                Step::Hidden {
                    tell_page: std::mem::replace(&mut self.told_visible, false),
                }
            }
        }
    }

    /// The settle timer fired: the transition to nudge for, if one is pending
    /// and the window is visible now.
    fn fire(&mut self, visible: bool) -> Option<Transition> {
        let pending = self.pending.take()?;
        if !visible {
            return None;
        }
        self.told_visible = true;
        Some(pending)
    }
}

fn nudge_script(reason: &str) -> String {
    format!("window.__wsNativeVisibilityNudge&&window.__wsNativeVisibilityNudge(\"{reason}\")")
}

fn hidden_script(reason: &str) -> String {
    format!("window.__wsNativeVisibilityHidden&&window.__wsNativeVisibilityHidden(\"{reason}\")")
}

/// The observed window, its web view and nudge state.
struct Observed {
    window: Weak<AnyObject>,
    // Weak: Wry owns the view; a Retry re-install replaces it.
    webview: RefCell<Weak<AnyObject>>,
    nudge: RefCell<Nudge>,
    // The armed settle timer; invalidated when replaced or dropped.
    timer: RefCell<Option<Retained<AnyObject>>>,
}

impl Observed {
    fn cancel_timer(&self) {
        if let Some(timer) = self.timer.borrow_mut().take() {
            let _: () = unsafe { msg_send![&*timer, invalidate] };
        }
    }
}

impl Drop for Observed {
    fn drop(&mut self) {
        self.cancel_timer();
    }
}

struct Entry {
    observed: Rc<Observed>,
    // Notification center tokens; each block holds `observed`.
    tokens: Vec<Retained<AnyObject>>,
}

impl Entry {
    fn remove_observers(&self) {
        let center = notification_center();
        for token in &self.tokens {
            let _: () = unsafe { msg_send![&*center, removeObserver: &**token] };
        }
    }
}

thread_local! {
    static OBSERVED: RefCell<Vec<Entry>> = const { RefCell::new(Vec::new()) };
}

fn read_visible(window: &AnyObject) -> bool {
    let occlusion: usize = unsafe { msg_send![window, occlusionState] };
    let ordered_in: bool = unsafe { msg_send![window, isVisible] };
    let miniaturized: bool = unsafe { msg_send![window, isMiniaturized] };
    window_visible(occlusion, ordered_in, miniaturized)
}

fn evaluate(webview: &AnyObject, script: &str) {
    let script = NSString::from_str(script);
    // No completion handler: an older page without the hook evaluates to
    // undefined, and an error has nobody to report to.
    let _: () = unsafe {
        msg_send![
            webview,
            evaluateJavaScript: &*script,
            completionHandler: std::ptr::null::<block2::Block<dyn Fn(*mut AnyObject, *mut AnyObject)>>()
        ]
    };
}

/// Mark the web view and the window's content view as needing layout and
/// display, so the next display cycle commits a fresh layer tree to the
/// window server. No geometry changes: WebKit sees no resize, the page no
/// scroll jump, and an unchanged layout redraws nothing visible. A resize
/// jiggle (a visible flicker and a page resize) and private WebKit calls
/// were rejected.
fn repaint(window: &AnyObject, webview: &AnyObject) {
    let _: () = unsafe { msg_send![webview, setNeedsLayout: true] };
    let _: () = unsafe { msg_send![webview, setNeedsDisplay: true] };
    let content: Option<Retained<AnyObject>> = unsafe { msg_send![window, contentView] };
    if let Some(content) = content {
        let _: () = unsafe { msg_send![&*content, setNeedsDisplay: true] };
    }
}

fn fire(observed: &Observed) {
    observed.timer.borrow_mut().take();
    let Some(window) = observed.window.load() else {
        return;
    };
    let visible = read_visible(&window);
    let Some(transition) = observed.nudge.borrow_mut().fire(visible) else {
        return;
    };
    let Some(webview) = observed.webview.borrow().load() else {
        return;
    };
    repaint(&window, &webview);
    evaluate(&webview, &nudge_script(transition.reason()));
}

fn arm(observed: &Rc<Observed>) {
    observed.cancel_timer();
    // Weak: the run loop retains the timer and the timer its block until it
    // fires or is invalidated; an entry dropped meanwhile is not kept alive.
    let target = Rc::downgrade(observed);
    let block = RcBlock::new(move |_: *mut AnyObject| {
        if let Some(observed) = target.upgrade() {
            fire(&observed);
        }
    });
    let timer: Retained<AnyObject> = unsafe {
        msg_send![
            class!(NSTimer),
            timerWithTimeInterval: SETTLE_SECONDS,
            repeats: false,
            block: &*block
        ]
    };
    // Common modes: a nudge armed during a live resize or menu tracking still
    // runs. Notifications arrive on the main thread, whose run loop this is.
    let run_loop: Retained<AnyObject> = unsafe { msg_send![class!(NSRunLoop), currentRunLoop] };
    let _: () = unsafe { msg_send![&*run_loop, addTimer: &*timer, forMode: NSRunLoopCommonModes] };
    *observed.timer.borrow_mut() = Some(timer);
}

fn on_transition(observed: &Rc<Observed>, transition: Transition) {
    let Some(window) = observed.window.load() else {
        return;
    };
    let visible = read_visible(&window);
    let step = observed.nudge.borrow_mut().transition(transition, visible);
    match step {
        Step::Arm => arm(observed),
        Step::Hidden { tell_page } => {
            observed.cancel_timer();
            if !tell_page {
                return;
            }
            if let Some(webview) = observed.webview.borrow().load() {
                evaluate(&webview, &hidden_script(transition.reason()));
            }
        }
    }
}

fn observe(
    window: &AnyObject,
    name: &NSString,
    observed: &Rc<Observed>,
    transition: Transition,
) -> Option<Retained<AnyObject>> {
    let observed = observed.clone();
    let block = RcBlock::new(move |_: *mut AnyObject| on_transition(&observed, transition));
    unsafe {
        msg_send![
            &*notification_center(),
            addObserverForName: name,
            object: window,
            queue: None::<&AnyObject>,
            usingBlock: &*block
        ]
    }
}

/// Observe `window`'s full-screen and occlusion changes and nudge `webview`.
/// Idempotent per window: a re-install (menu Retry) only replaces the web
/// view. Entries of windows since deallocated are dropped with their
/// observers.
fn observe_window(window: &AnyObject, webview: &AnyObject) -> bool {
    OBSERVED.with(|entries| {
        let mut entries = entries.borrow_mut();
        entries.retain(|entry| {
            let live = entry.observed.window.load().is_some();
            if !live {
                entry.remove_observers();
            }
            live
        });
        let same = |entry: &&Entry| {
            entry
                .observed
                .window
                .load()
                .is_some_and(|seen| std::ptr::eq(&*seen, window))
        };
        if let Some(entry) = entries.iter().find(same) {
            *entry.observed.webview.borrow_mut() = Weak::new(webview);
            return false;
        }
        let observed = Rc::new(Observed {
            window: Weak::new(window),
            webview: RefCell::new(Weak::new(webview)),
            nudge: RefCell::new(Nudge::default()),
            timer: RefCell::new(None),
        });
        let tokens = unsafe {
            [
                (
                    NSWindowDidEnterFullScreenNotification,
                    Transition::EnterFullScreen,
                ),
                (
                    NSWindowDidExitFullScreenNotification,
                    Transition::ExitFullScreen,
                ),
                (
                    NSWindowDidChangeOcclusionStateNotification,
                    Transition::Occlusion,
                ),
            ]
        }
        .into_iter()
        .filter_map(|(name, transition)| observe(window, name, &observed, transition))
        .collect();
        entries.push(Entry { observed, tokens });
        true
    })
}

/// Install the post-transition nudge for the web view's window.
pub(super) fn observe_transitions(webview: &WKWebView) {
    let window: Option<Retained<AnyObject>> = unsafe { msg_send![webview, window] };
    let Some(window) = window else {
        log("Werkschaft: full-screen repaint observers not installed: web view has no window");
        return;
    };
    if observe_window(&window, webview) {
        log("Werkschaft: full-screen and occlusion repaint observers installed");
    }
}

extern "C" {
    // AppKit; posted with the window as object.
    static NSWindowDidEnterFullScreenNotification: &'static NSString;
    static NSWindowDidChangeOcclusionStateNotification: &'static NSString;
    // Foundation.
    static NSRunLoopCommonModes: &'static NSString;
}

#[cfg(test)]
mod tests;
