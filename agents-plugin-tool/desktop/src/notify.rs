// Native notifications for the dashboard's attention events: a question to
// the user and a session's full-idle completion. The main window's dashboard
// page selects the events and posts each through the notify bridge
// (notify-bridge.js); the shell shows it once natively and answers whether it
// did, so the page uses its browser notification only when the native path
// failed. Secondary windows carry a marker and notify nothing: the main
// window, hidden in the tray or not, is the one notifier.

use std::{
    collections::HashSet,
    sync::{Arc, Mutex},
    thread,
};

use serde::Deserialize;
use serde_json::json;
use tauri::{AppHandle, Manager, WebviewWindow};
use url::Url;

use crate::{
    close,
    navigation::{destination, Destination},
    shell::Connection,
};

const SCRIPT: &str = include_str!("notify-bridge.js");
pub const PREFIX: &str = "werkschaft:notify:";
/// The longest message the bridge accepts, in bytes: its bounded fields,
/// JSON-escaped, in UTF-8.
pub const MAX_MESSAGE: usize = 8192;
const TITLE_MAX: usize = 200;
const BODY_MAX: usize = 1000;
const TAG_MAX: usize = 300;

pub struct Notifier {
    connection: Arc<Connection>,
    identifier: String,
    showing: Mutex<Showing>,
}

pub fn manage(app: &AppHandle, connection: Arc<Connection>) {
    app.manage(Arc::new(Notifier {
        connection,
        identifier: app.config().identifier.clone(),
        showing: Mutex::new(Showing::default()),
    }));
}

/// One notification the page asks for.
#[derive(Debug, PartialEq, Eq)]
pub struct Notice {
    pub id: u64,
    pub title: String,
    pub body: String,
    pub tag: String,
}

pub fn parse(message: &str) -> Option<Notice> {
    #[derive(Deserialize)]
    struct Payload {
        id: u64,
        title: String,
        #[serde(default)]
        body: String,
        tag: String,
    }
    if message.len() > MAX_MESSAGE {
        return None;
    }
    let payload: Payload = serde_json::from_str(message.strip_prefix(PREFIX)?).ok()?;
    let clip = |text: &str, limit: usize| -> String {
        text.trim()
            .chars()
            .filter(|c| !c.is_control() || *c == '\n')
            .take(limit)
            .collect()
    };
    let notice = Notice {
        id: payload.id,
        title: clip(&payload.title, TITLE_MAX),
        body: clip(&payload.body, BODY_MAX),
        tag: clip(&payload.tag, TAG_MAX),
    };
    (!notice.title.is_empty() && !notice.tag.is_empty()).then_some(notice)
}

/// The tags being shown now, so one event posted twice at once shows once.
/// A tag leaves when its show returns, shown or not: the page selects each
/// event once, and a later event may repeat a tag (the same approval asked
/// again), so nothing is remembered past the show, and a failed show never
/// keeps a later one from showing or falling back.
#[derive(Default)]
pub struct Showing {
    tags: HashSet<String>,
}

impl Showing {
    /// Takes tag for one show; false while another show holds it.
    pub fn begin(&mut self, tag: &str) -> bool {
        self.tags.insert(tag.to_owned())
    }

    pub fn end(&mut self, tag: &str) {
        self.tags.remove(tag);
    }
}

/// Handles a bridge message from the main window's top frame at `source`.
pub fn receive(window: WebviewWindow, epoch: usize, source: Url, message: &str) {
    let Some(notifier) = window.try_state::<Arc<Notifier>>() else {
        return;
    };
    let notifier = notifier.inner().clone();
    let Some(pin) = notifier.connection.pinned_url() else {
        return;
    };
    if !close::checks_draft(window.label())
        || notifier.connection.window_epoch() != epoch
        || destination(&source, Some(&pin)) != Destination::Dashboard
    {
        return;
    }
    let Some(notice) = parse(message) else {
        return;
    };
    let origin = pin.origin().ascii_serialization();
    if !notifier.showing.lock().unwrap().begin(&notice.tag) {
        // The same event is being shown: the page must not fall back for this
        // copy; the show in progress answers its own copy, and falls back for
        // it if it fails.
        let _ = window.eval(answer_script(&origin, notice.id, true));
        return;
    }
    thread::spawn(move || {
        let shown = show(&notifier.identifier, &notice.title, &notice.body);
        notifier.showing.lock().unwrap().end(&notice.tag);
        if !shown {
            eprintln!("Werkschaft: Could not show a native notification; the page falls back");
        }
        let _ = window.eval(answer_script(&origin, notice.id, shown));
    });
}

fn answer_script(origin: &str, id: u64, shown: bool) -> String {
    let origin = json!(origin);
    format!(
        "if (window === window.top && location.origin === {origin}) window.__werkschaftNotifyResult?.({id}, {shown});"
    )
}

/// Installs the bridge in the main window's pinned dashboard page.
pub fn inject(window: &WebviewWindow, source: &Url) {
    let Some(notifier) = window.try_state::<Arc<Notifier>>() else {
        return;
    };
    let pin = notifier.connection.pinned_url();
    if !close::checks_draft(window.label())
        || destination(source, pin.as_ref()) != Destination::Dashboard
    {
        return;
    }
    let origin = json!(source.origin().ascii_serialization());
    let _ = window.eval(format!(
        "(() => {{ {SCRIPT}\ninstallWerkschaftNotifyBridge({origin}); }})()"
    ));
}

/// Marks a secondary window's dashboard page, which then notifies nothing.
pub fn mark_secondary(window: &WebviewWindow) {
    let _ = window.eval(SECONDARY_SCRIPT);
}

const SECONDARY_SCRIPT: &str = "if (window === window.top && !('werkschaftSecondary' in window)) Object.defineProperty(window, 'werkschaftSecondary', { value: true });";

#[cfg(target_os = "macos")]
fn show(identifier: &str, title: &str, body: &str) -> bool {
    use std::sync::OnceLock;
    // The application is set once per process; an unsigned app may be
    // refused, which every later notification then reports.
    static APPLICATION: OnceLock<bool> = OnceLock::new();
    if !*APPLICATION.get_or_init(|| mac_notification_sys::set_application(identifier).is_ok()) {
        return false;
    }
    mac_notification_sys::Notification::new()
        .title(title)
        .message(body)
        .asynchronous(true)
        .send()
        .is_ok()
}

#[cfg(windows)]
fn show(identifier: &str, title: &str, body: &str) -> bool {
    // The app identifier is the toast's AppUserModelID; a portable copy that
    // no shortcut registers may be refused, which reports a failure.
    tauri_winrt_notification::Toast::new(identifier)
        .title(title)
        .text1(body)
        .show()
        .is_ok()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn message(payload: serde_json::Value) -> String {
        format!("{PREFIX}{payload}")
    }

    #[test]
    fn a_bridge_message_is_bounded_and_needs_a_title_and_tag() {
        assert_eq!(
            parse(&message(
                json!({"id": 3, "title": " ws/lead finished ", "body": "line one\nline two", "tag": "ws-idle:lead:1"})
            )),
            Some(Notice {
                id: 3,
                title: "ws/lead finished".into(),
                body: "line one\nline two".into(),
                tag: "ws-idle:lead:1".into(),
            })
        );
        let long = parse(&message(json!({"id": 1, "title": "t".repeat(500), "body": "b".repeat(1500), "tag": "g".repeat(400)}))).unwrap();
        assert_eq!(
            (
                long.title.chars().count(),
                long.body.chars().count(),
                long.tag.chars().count()
            ),
            (TITLE_MAX, BODY_MAX, TAG_MAX)
        );
        let control = parse(&message(
            json!({"id": 1, "title": "a\u{7}b\tc", "tag": "x"}),
        ))
        .unwrap();
        assert_eq!(control.title, "abc");
        for bad in [
            message(json!({"id": 1, "title": "", "tag": "x"})),
            message(json!({"id": 1, "title": "t", "tag": " "})),
            message(json!({"id": -1, "title": "t", "tag": "x"})),
            message(json!({"title": "t", "tag": "x"})),
            "werkschaft:update:check".to_owned(),
            format!("{PREFIX}not json"),
            format!(
                "{PREFIX}{{\"id\":1,\"title\":\"{}\",\"tag\":\"x\"}}",
                "t".repeat(MAX_MESSAGE)
            ),
        ] {
            assert_eq!(parse(&bad), None, "{bad:.80}");
        }
    }

    #[test]
    fn a_tag_is_held_only_while_it_is_shown() {
        let mut showing = Showing::default();
        assert!(showing.begin("ws-approval:a\nBash\ngo test"));
        // A copy posted while the first is still showing is that show.
        assert!(!showing.begin("ws-approval:a\nBash\ngo test"));
        assert!(showing.begin("ws-ask:a:1"));
        // Once the show returns (shown or failed), the same tag is a new
        // event again: an approval asked again, or one whose show failed.
        showing.end("ws-approval:a\nBash\ngo test");
        assert!(showing.begin("ws-approval:a\nBash\ngo test"));
        showing.end("ws-approval:a\nBash\ngo test");
        showing.end("ws-ask:a:1");
        assert!(showing.tags.is_empty());
    }

    #[test]
    fn the_answer_reaches_only_the_pinned_top_frame() {
        let script = answer_script("http://127.0.0.1:49015", 7, false);
        assert_eq!(
            script,
            "if (window === window.top && location.origin === \"http://127.0.0.1:49015\") window.__werkschaftNotifyResult?.(7, false);"
        );
        assert!(SECONDARY_SCRIPT.starts_with("if (window === window.top"));
    }
}
