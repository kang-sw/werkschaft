use std::{
    sync::{atomic::Ordering, mpsc, Arc},
    thread,
    time::{Duration, Instant},
};

use tauri::{AppHandle, Manager, WebviewWindow};
use tauri_plugin_dialog::{
    DialogExt, MessageDialogButtons, MessageDialogKind, MessageDialogResult,
};

use crate::{
    endpoint::FullStop,
    navigation::{destination, Destination},
    shell::Connection,
};

pub const MAIN_WINDOW: &str = "main";

pub fn checks_draft(label: &str) -> bool {
    label == MAIN_WINDOW
}

// The app has two lifetimes a user sees: the app itself, which the window's
// close button only hides to the tray, and the background (the daemon and
// every session it runs). Close ends the app and keeps the background; a
// full quit stops the background first and ends the app only once it has
// fully stopped. Hiding never asks about drafts: the page keeps running.

/// A real exit, chosen from the tray (or the system's quit for Close).
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Exit {
    Close,
    FullQuit,
}

/// What the app does with a request to exit.
#[derive(Debug, PartialEq, Eq)]
pub enum ExitRequest {
    /// An exit this app asked for itself (`code` set: an exit flow's end,
    /// a restart) proceeds.
    Proceed,
    /// The Windows webview rebuild keeps the app alive meanwhile.
    Prevent,
    /// A quit from the user (the app menu, the Dock, the system) is the
    /// tray's Close: prevented, then the drafts are checked first.
    Close,
}

pub fn exit_request(code: Option<i32>, rebuilding: bool) -> ExitRequest {
    match (rebuilding, code) {
        (true, _) => ExitRequest::Prevent,
        (false, None) => ExitRequest::Close,
        (false, Some(_)) => ExitRequest::Proceed,
    }
}

/// Starts an exit unless one is already running. The draft check, the
/// confirmation, and the full stop run on a worker; every outcome other
/// than the app's exit restores the window and releases the document.
pub fn exit(app: AppHandle, connection: Arc<Connection>, kind: Exit) {
    if connection.closing.swap(true, Ordering::AcqRel) {
        return;
    }
    thread::spawn(move || {
        let main = app.get_webview_window(MAIN_WINDOW);
        let drafts_ready = main
            .as_ref()
            .is_none_or(|window| drafts_saved(window, &connection));
        let done = match kind {
            Exit::Close => close(&app, main.as_ref(), drafts_ready),
            Exit::FullQuit => full_quit(&app, main.as_ref(), &connection, drafts_ready),
        };
        if done {
            app.exit(0);
        } else {
            if let Some(window) = &main {
                release_document(window);
                let _ = window.set_title("Werkschaft");
            }
            connection.closing.store(false, Ordering::Release);
        }
    });
}

// Whether the main window's drafts are saved: true for a bundled local page,
// and for the pinned dashboard whose renderer reports its store ready.
fn drafts_saved(window: &WebviewWindow, connection: &Connection) -> bool {
    let pin = connection.pinned_url();
    let Some(current) = window.url().ok() else {
        return false;
    };
    match destination(&current, pin.as_ref()) {
        Destination::Local => true,
        Destination::Dashboard => {
            pin.is_some_and(|pin| draft_ready(window, &pin.origin().ascii_serialization()))
        }
        _ => false,
    }
}

fn close(app: &AppHandle, main: Option<&WebviewWindow>, drafts_ready: bool) -> bool {
    drafts_ready
        || confirm(
            app,
            main,
            "Close Werkschaft?",
            &close_message(),
            "Stay",
            "Close",
        )
}

fn close_message() -> String {
    "Some changes may not be saved. Close Werkschaft anyway? The background keeps running."
        .to_owned()
}

fn full_quit(
    app: &AppHandle,
    main: Option<&WebviewWindow>,
    connection: &Connection,
    drafts_ready: bool,
) -> bool {
    let Some(daemon) = connection.pinned_daemon() else {
        inform(
            app,
            main,
            MessageDialogKind::Warning,
            "Background not stopped",
            "Werkschaft is not connected to the background, so nothing was stopped. Connect with Connection → Retry, then quit again.",
        );
        return false;
    };
    let sessions = daemon.running_sessions().ok();
    if let Some(message) = full_quit_question(sessions, drafts_ready) {
        if !confirm(
            app,
            main,
            "Quit Werkschaft and stop the background?",
            &message,
            "Cancel",
            "Stop and Quit",
        ) {
            return false;
        }
    }
    if let Some(window) = main {
        let _ = window.set_title("Werkschaft — Stopping the background…");
    }
    let outcome = daemon.full_stop();
    let stopped = matches!(outcome, Ok(FullStop::Stopped));
    let report = match outcome {
        Ok(FullStop::Stopped) if !daemon.wait_stopped(Duration::from_secs(20)) => {
            Report::warning(STOPPED_BUT_RUNNING)
        }
        Ok(FullStop::Stopped) => {
            // A client still running outside the stopped sessions may start
            // a new, empty background through its normal recovery.
            thread::sleep(Duration::from_millis(1500));
            match daemon.successor() {
                Some(successor) => Report::exit(successor_message(&successor.version)),
                None => return true,
            }
        }
        other => full_stop_report(other),
    };
    inform(app, main, report.kind, "Full quit", &report.message);
    stopped && report.exits
}

const STOPPED_BUT_RUNNING: &str =
    "Every session was stopped, but the background did not exit. Werkschaft stays open.";

// The confirmation a full quit asks, None when nothing would be lost: no
// running session or exec job (every holder the daemon lists) and every
// draft saved.
fn full_quit_question(sessions: Option<usize>, drafts_ready: bool) -> Option<String> {
    let mut lines = Vec::new();
    match sessions {
        Some(0) => (),
        Some(1) => lines.push(
            "1 running session or job (an agent session, subagent, terminal, or exec job) will be stopped with the background."
                .to_owned(),
        ),
        Some(count) => lines.push(format!(
            "{count} running sessions and jobs (agent sessions, subagents, terminals, and exec jobs) will be stopped with the background."
        )),
        None => lines.push(
            "The running sessions and jobs could not be counted. Everything still running will be stopped with the background."
                .to_owned(),
        ),
    }
    if !drafts_ready {
        lines.push("Some changes may not be saved.".to_owned());
    }
    (!lines.is_empty()).then(|| lines.join("\n\n"))
}

fn successor_message(version: &str) -> String {
    let version = if version.is_empty() {
        String::new()
    } else {
        format!(" (version {version})")
    };
    format!(
        "Every session and the background were stopped, but another client still running outside Werkschaft has since started a new, empty background{version}."
    )
}

struct Report {
    kind: MessageDialogKind,
    message: String,
    exits: bool,
}

impl Report {
    fn warning(message: &str) -> Self {
        Self {
            kind: MessageDialogKind::Warning,
            message: message.to_owned(),
            exits: false,
        }
    }

    fn exit(message: String) -> Self {
        Self {
            kind: MessageDialogKind::Info,
            message,
            exits: true,
        }
    }
}

// What the app reports for a full stop that did not complete. None of these
// ends the app: the background, or some of it, may still be running.
fn full_stop_report(outcome: Result<FullStop, &'static str>) -> Report {
    let message = match outcome {
        Err(reason) => format!("Nothing was stopped: {reason}."),
        Ok(FullStop::Stopped) => return Report::warning(STOPPED_BUT_RUNNING),
        Ok(FullStop::Incomplete { remaining }) => {
            let sessions = if remaining == 1 {
                "1 session or job".to_owned()
            } else {
                format!("{remaining} sessions or jobs")
            };
            format!("The full quit is incomplete: {sessions} could not be stopped, so the background keeps running. Werkschaft stays open.")
        }
        Ok(FullStop::Unsupported) => "The running background is older than this app and cannot stop its sessions. Nothing was stopped; update the plugin or restart the background, then quit again.".to_owned(),
        Ok(FullStop::Unauthorized) => "The background refused this app's credential. Nothing was stopped; retry the connection, then quit again.".to_owned(),
        Ok(FullStop::AlreadyStopping) => "The background was already stopping, so its sessions were not stopped and may still be running.".to_owned(),
        Ok(FullStop::Unknown) => "Werkschaft could not confirm the full quit. Some sessions or the background may still be running.".to_owned(),
    };
    Report {
        kind: MessageDialogKind::Warning,
        message,
        exits: false,
    }
}

// A dialog needs a visible parent: the hidden main window is shown first.
fn show(main: Option<&WebviewWindow>) {
    if let Some(window) = main {
        // The pending-hide state lives on the main thread; this may not.
        #[cfg(target_os = "macos")]
        let _ = window.run_on_main_thread(crate::macos::keep_shown);
        let _ = window.show();
        let _ = window.unminimize();
        let _ = window.set_focus();
    }
}

// Asks on a worker thread; the safe answer is the default button.
fn confirm(
    app: &AppHandle,
    main: Option<&WebviewWindow>,
    title: &str,
    message: &str,
    stay: &str,
    proceed: &str,
) -> bool {
    show(main);
    let mut dialog = app
        .dialog()
        .message(message)
        .title(title)
        .kind(MessageDialogKind::Warning)
        .buttons(MessageDialogButtons::OkCancelCustom(
            stay.into(),
            proceed.into(),
        ));
    if let Some(window) = main {
        dialog = dialog.parent(window);
    }
    dialog.blocking_show_with_result() == MessageDialogResult::Custom(proceed.into())
}

fn inform(
    app: &AppHandle,
    main: Option<&WebviewWindow>,
    kind: MessageDialogKind,
    title: &str,
    message: &str,
) {
    show(main);
    let mut dialog = app.dialog().message(message).title(title).kind(kind);
    if let Some(window) = main {
        dialog = dialog.parent(window);
    }
    dialog.blocking_show();
}

fn eval(window: &WebviewWindow, script: &str, deadline: Instant) -> Option<String> {
    let (sender, receiver) = mpsc::channel();
    window
        .eval_with_callback(script, move |value| {
            let _ = sender.send(value);
        })
        .ok()?;
    receiver
        .recv_timeout(deadline.saturating_duration_since(Instant::now()))
        .ok()
}

pub fn draft_ready(window: &WebviewWindow, origin: &str) -> bool {
    // Evaluation runs in the main frame. The IPC surface stays closed to every
    // page, and the renderer only reports whether its existing store is ready.
    let origin = serde_json::to_string(origin).expect("string");
    let start = format!(
        "(() => {{
            if (window !== window.top || location.origin !== {origin}) return -1;
            if (document.documentElement.hasAttribute('data-login-gate')) return 1;
            if (typeof window.wsNativeCloseReady !== 'function') return -1;
            const attempt = {{ result: 0, wasInert: document.documentElement.inert }};
            window.__werkschaftClose = attempt;
            document.documentElement.inert = true;
            Promise.resolve().then(() => window.wsNativeCloseReady()).then(
                ready => {{ attempt.result = ready === true ? 1 : -1; }},
                () => {{ attempt.result = -1; }}
            );
            return 0;
        }})()"
    );
    let poll = format!(
        "window === window.top && location.origin === {origin} && window.__werkschaftClose ?
            window.__werkschaftClose.result : -1"
    );
    let deadline = Instant::now() + Duration::from_secs(2);
    let mut result = eval(window, &start, deadline);
    while result.as_deref() == Some("0") && Instant::now() < deadline {
        thread::sleep(Duration::from_millis(40));
        result = eval(window, &poll, deadline);
    }
    result.as_deref() == Some("1")
}

pub fn release_document(window: &WebviewWindow) {
    let _ = window.eval(
        "if (window.__werkschaftClose) {
            document.documentElement.inert = window.__werkschaftClose.wasInert;
            delete window.__werkschaftClose;
        }",
    );
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn only_main_close_intercepts_the_native_close_event() {
        assert!(super::checks_draft(super::MAIN_WINDOW));
        assert!(!super::checks_draft("secondary-1"));
        assert!(!super::checks_draft("secondary-2"));
    }

    #[test]
    fn a_user_quit_becomes_close_and_the_apps_own_exits_proceed() {
        assert_eq!(exit_request(None, false), ExitRequest::Close);
        assert_eq!(exit_request(Some(0), false), ExitRequest::Proceed);
        assert_eq!(exit_request(Some(1), false), ExitRequest::Proceed);
        assert_eq!(
            exit_request(Some(tauri::RESTART_EXIT_CODE), false),
            ExitRequest::Proceed
        );
        assert_eq!(exit_request(None, true), ExitRequest::Prevent);
        assert_eq!(exit_request(Some(0), true), ExitRequest::Prevent);
    }

    #[test]
    fn full_quit_asks_only_when_something_would_be_lost() {
        assert_eq!(full_quit_question(Some(0), true), None);
        let one = full_quit_question(Some(1), true).unwrap();
        assert!(
            one.starts_with("1 running session or job (")
                && one.contains("exec job")
                && !one.contains("changes")
        );
        let many = full_quit_question(Some(3), true).unwrap();
        assert!(many.starts_with(
            "3 running sessions and jobs (agent sessions, subagents, terminals, and exec jobs)"
        ));
        // A count that could not be read is never taken as zero.
        let unknown = full_quit_question(None, true).unwrap();
        assert!(unknown.contains("could not be counted"));
        let drafts = full_quit_question(Some(0), false).unwrap();
        assert_eq!(drafts, "Some changes may not be saved.");
        let both = full_quit_question(Some(2), false).unwrap();
        assert!(
            both.starts_with("2 running sessions and jobs")
                && both.ends_with("Some changes may not be saved.")
        );
    }

    #[test]
    fn an_incomplete_full_stop_never_exits_and_says_what_happened() {
        let cases = [
            (
                Ok(FullStop::Incomplete { remaining: 2 }),
                "2 sessions or jobs could not be stopped",
            ),
            (
                Ok(FullStop::Incomplete { remaining: 1 }),
                "1 session or job could not be stopped",
            ),
            (Ok(FullStop::Unsupported), "older than this app"),
            (Ok(FullStop::Unauthorized), "refused this app's credential"),
            (Ok(FullStop::AlreadyStopping), "already stopping"),
            (Ok(FullStop::Unknown), "could not confirm"),
            (Ok(FullStop::Stopped), "did not exit"),
            (
                Err("Dashboard changed before authenticated request; retry"),
                "Nothing was stopped: Dashboard changed",
            ),
        ];
        for (outcome, expected) in cases {
            let report = full_stop_report(outcome);
            assert!(!report.exits);
            assert!(report.message.contains(expected), "{}", report.message);
        }
        for outcome in [
            Ok(FullStop::Unsupported),
            Ok(FullStop::Unauthorized),
            Err("x"),
        ] {
            assert!(full_stop_report(outcome)
                .message
                .contains("Nothing was stopped"));
        }
    }

    #[test]
    fn a_successor_background_is_reported_not_hidden() {
        let message = successor_message("0.50.9");
        assert!(message.contains("new, empty background (version 0.50.9)"));
        assert!(successor_message("").ends_with("new, empty background."));
        assert!(Report::exit(message).exits);
    }
}
