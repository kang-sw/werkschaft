// Native OS speech-to-text bridge for the desktop shell.
//
// The main window's pinned top frame installs `window.werkschaftSpeech` via
// `speech-bridge.js` and sends commands (`status`, `start`, `stop`, `cancel`)
// prefixed with `werkschaft:speech:`. Rust enforces strict pinned-origin
// isolation, single-owner recognizer semantics (a second `start` while active
// returns `error` with `code: "busy"`), memory-only audio capture, and
// immediate teardown on close-to-tray hide, minimize, navigation, or suspend.

use std::sync::{
    atomic::{AtomicBool, Ordering},
    Arc, Mutex,
};

use serde::{Deserialize, Serialize};
use serde_json::json;
use tauri::{AppHandle, Manager, WebviewWindow};
use url::Url;

use crate::{
    chrome, close,
    navigation::{destination, Destination},
    shell::Connection,
};

const SCRIPT: &str = include_str!("speech-bridge.js");
pub const PREFIX: &str = "werkschaft:speech:";
pub const MAX_MESSAGE: usize = 512;
const LOCALE_MAX: usize = 35;

/// A validated command received from the pinned dashboard top frame.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Command {
    Status { locale: Option<String> },
    Start { locale: Option<String> },
    Stop,
    Cancel,
}

/// Event delivered from Rust to the pinned top frame as `ws-native-speech`.
#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
#[serde(tag = "type", rename_all = "snake_case")]
pub enum SpeechEvent {
    Ready {
        available: bool,
        on_device: bool,
        #[serde(skip_serializing_if = "Option::is_none")]
        locale: Option<String>,
        #[serde(skip_serializing_if = "Option::is_none")]
        code: Option<String>,
        #[serde(skip_serializing_if = "Option::is_none")]
        reason: Option<String>,
    },
    Partial {
        text: String,
    },
    Final {
        text: String,
    },
    Error {
        code: String,
        #[serde(skip_serializing_if = "Option::is_none")]
        reason: Option<String>,
    },
    Hidden {
        #[serde(skip_serializing_if = "Option::is_none")]
        reason: Option<String>,
    },
}

/// Pure origin and window-generation check for incoming bridge commands.
pub fn origin_allowed(
    window_label: &str,
    current_epoch: usize,
    message_epoch: usize,
    source: &Url,
    pin: Option<&Url>,
) -> bool {
    let Some(pin) = pin else {
        return false;
    };
    close::checks_draft(window_label)
        && current_epoch == message_epoch
        && destination(source, Some(pin)) == Destination::Dashboard
}

/// Validates and parses a raw bridge message string.
pub fn parse_command(message: &str) -> Option<Command> {
    #[derive(Deserialize)]
    struct Payload {
        op: String,
        #[serde(default)]
        locale: Option<String>,
    }
    if message.len() > MAX_MESSAGE {
        return None;
    }
    let body = message.strip_prefix(PREFIX)?;
    let payload: Payload = serde_json::from_str(body).ok()?;
    let locale = sanitize_locale(payload.locale.as_deref())?;
    match payload.op.as_str() {
        "status" => Some(Command::Status { locale }),
        "start" => Some(Command::Start { locale }),
        "stop" => Some(Command::Stop),
        "cancel" => Some(Command::Cancel),
        _ => None,
    }
}

/// Sanitizes an optional BCP-47 locale tag. Returns `Some(None)` when omitted
/// or empty, `Some(Some(tag))` when valid, and `None` when malformed.
fn sanitize_locale(raw: Option<&str>) -> Option<Option<String>> {
    let Some(raw) = raw else {
        return Some(None);
    };
    let trimmed = raw.trim();
    if trimmed.is_empty() {
        return Some(None);
    }
    if trimmed.len() > LOCALE_MAX {
        return None;
    }
    if !trimmed
        .bytes()
        .all(|b| b.is_ascii_alphanumeric() || b == b'-' || b == b'_')
    {
        return None;
    }
    Some(Some(trimmed.replace('_', "-")))
}

/// Pure single-owner recognition session state machine.
///
/// Tracks an active monotonic session id so stale callbacks from a cancelled
/// or previously finished OS task are ignored and a concurrent `start` is
/// refused with `busy`.
#[derive(Debug, Default)]
pub struct SessionState {
    next_id: u64,
    phase: SessionPhase,
}

#[derive(Debug, Default, PartialEq, Eq)]
enum SessionPhase {
    #[default]
    Idle,
    Listening {
        id: u64,
        last_partial: String,
    },
    Stopping {
        id: u64,
        last_partial: String,
    },
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum StartError {
    Busy,
}

impl SessionState {
    pub fn start(&mut self) -> Result<u64, StartError> {
        if !matches!(self.phase, SessionPhase::Idle) {
            return Err(StartError::Busy);
        }
        self.next_id += 1;
        let id = self.next_id;
        self.phase = SessionPhase::Listening {
            id,
            last_partial: String::new(),
        };
        Ok(id)
    }

    pub fn is_active(&self, id: u64) -> bool {
        matches!(
            self.phase,
            SessionPhase::Listening { id: cur, .. } | SessionPhase::Stopping { id: cur, .. }
                if cur == id
        )
    }

    pub fn is_stopping(&self, id: u64) -> bool {
        matches!(self.phase, SessionPhase::Stopping { id: cur, .. } if cur == id)
    }

    pub fn on_partial(&mut self, id: u64, text: String) -> Option<SpeechEvent> {
        match &mut self.phase {
            SessionPhase::Listening {
                id: cur,
                last_partial,
            }
            | SessionPhase::Stopping {
                id: cur,
                last_partial,
            } if *cur == id => {
                *last_partial = text.clone();
                Some(SpeechEvent::Partial { text })
            }
            _ => None,
        }
    }

    pub fn on_final(&mut self, id: u64, text: String) -> Option<SpeechEvent> {
        if !self.is_active(id) {
            return None;
        }
        self.phase = SessionPhase::Idle;
        Some(SpeechEvent::Final { text })
    }

    /// Transitions `Listening -> Stopping` and returns the active session id.
    pub fn begin_stop(&mut self) -> Option<u64> {
        let SessionPhase::Listening { id, last_partial } = std::mem::take(&mut self.phase) else {
            return None;
        };
        self.phase = SessionPhase::Stopping { id, last_partial };
        Some(id)
    }

    /// If session `id` is still in `Stopping`, finishes it with the last seen
    /// partial text so the frontend never stalls waiting for a final event.
    pub fn finish_stopping_with_fallback(&mut self, id: u64) -> Option<SpeechEvent> {
        if !self.is_stopping(id) {
            return None;
        }
        let SessionPhase::Stopping { last_partial, .. } = std::mem::take(&mut self.phase) else {
            return None;
        };
        Some(SpeechEvent::Final { text: last_partial })
    }

    /// Records a fatal error for session `id`, returning to `Idle`.
    pub fn on_error(
        &mut self,
        id: u64,
        code: impl Into<String>,
        reason: Option<String>,
    ) -> Option<SpeechEvent> {
        if !self.is_active(id) {
            return None;
        }
        self.phase = SessionPhase::Idle;
        Some(SpeechEvent::Error {
            code: code.into(),
            reason,
        })
    }

    /// Cancels an active session (`Listening` or `Stopping`), returning its id.
    pub fn cancel(&mut self) -> Option<u64> {
        match std::mem::take(&mut self.phase) {
            SessionPhase::Idle => None,
            SessionPhase::Listening { id, .. } | SessionPhase::Stopping { id, .. } => Some(id),
        }
    }

    /// Cancels any active session due to hide/minimize/tray/navigation/suspend.
    /// Returns `(cancelled_id, hidden_event)`.
    pub fn teardown(&mut self, reason: &str) -> (Option<u64>, SpeechEvent) {
        let cancelled = self.cancel();
        (
            cancelled,
            SpeechEvent::Hidden {
                reason: Some(reason.to_owned()),
            },
        )
    }
}

/// Builds the pinned-origin JS snippet that dispatches `ws-native-speech`.
pub fn event_script(origin: &str, event: &SpeechEvent) -> String {
    let origin = json!(origin);
    let detail = serde_json::to_string(event).unwrap_or_else(|_| "{}".to_owned());
    format!(
        "if (window === window.top && location.origin === {origin}) window.__werkschaftSpeechEvent?.({detail});"
    )
}

pub struct Controller {
    connection: Arc<Connection>,
    state: Mutex<SessionState>,
    minimized: AtomicBool,
    #[cfg(target_os = "macos")]
    backend: crate::macos::speech::Backend,
    #[cfg(windows)]
    backend: crate::windows_speech::Backend,
}

impl Controller {
    fn new(connection: Arc<Connection>) -> Self {
        Self {
            connection,
            state: Mutex::new(SessionState::default()),
            minimized: AtomicBool::new(false),
            #[cfg(target_os = "macos")]
            backend: crate::macos::speech::Backend::default(),
            #[cfg(windows)]
            backend: crate::windows_speech::Backend::default(),
        }
    }

    pub(crate) fn emit(&self, app: &AppHandle, event: &SpeechEvent) {
        let Some(pin) = self.connection.pinned_url() else {
            return;
        };
        let Some(window) = chrome::get(app, close::MAIN_WINDOW) else {
            return;
        };
        let origin = pin.origin().ascii_serialization();
        let _ = window.eval(event_script(&origin, event));
    }

    pub(crate) fn is_active(&self, id: u64) -> bool {
        self.state.lock().unwrap().is_active(id)
    }

    pub(crate) fn is_stopping(&self, id: u64) -> bool {
        self.state.lock().unwrap().is_stopping(id)
    }

    pub(crate) fn deliver_partial(&self, app: &AppHandle, id: u64, text: String) {
        let event = self.state.lock().unwrap().on_partial(id, text);
        if let Some(event) = event {
            self.emit(app, &event);
        }
    }

    pub(crate) fn deliver_final(&self, app: &AppHandle, id: u64, text: String) {
        let event = self.state.lock().unwrap().on_final(id, text);
        if let Some(event) = event {
            self.emit(app, &event);
        }
    }

    pub(crate) fn deliver_stopped_fallback(&self, app: &AppHandle, id: u64) {
        let event = self
            .state
            .lock()
            .unwrap()
            .finish_stopping_with_fallback(id);
        if let Some(event) = event {
            self.emit(app, &event);
        }
    }

    pub(crate) fn deliver_error(
        &self,
        app: &AppHandle,
        id: u64,
        code: impl Into<String>,
        reason: Option<String>,
    ) {
        let event = self.state.lock().unwrap().on_error(id, code, reason);
        if let Some(event) = event {
            self.emit(app, &event);
        }
    }

    #[cfg(windows)]
    pub(crate) fn backend_active(
        &self,
    ) -> &Mutex<Option<crate::windows_speech::ActiveSession>> {
        self.backend.active_slot()
    }

    #[cfg(windows)]
    pub(crate) fn backend_cancel(&self, id: u64) {
        self.backend.cancel(None, id);
    }

    #[cfg(windows)]
    pub(crate) fn backend_close_if(&self, id: u64, cancel: bool) {
        self.backend.close_if(id, cancel);
    }

    pub(crate) fn handle_command(self: &Arc<Self>, app: &AppHandle, command: Command) {
        match command {
            Command::Status { locale } => {
                let controller = self.clone();
                let app = app.clone();
                std::thread::spawn(move || {
                    let event = controller.backend.probe_status(locale.as_deref());
                    controller.emit(&app, &event);
                });
            }
            Command::Start { locale } => {
                let id = {
                    let mut state = self.state.lock().unwrap();
                    match state.start() {
                        Ok(id) => id,
                        Err(StartError::Busy) => {
                            drop(state);
                            self.emit(
                                app,
                                &SpeechEvent::Error {
                                    code: "busy".to_owned(),
                                    reason: Some("Native speech recognition is already active.".to_owned()),
                                },
                            );
                            return;
                        }
                    }
                };
                self.backend.start(self.clone(), app.clone(), id, locale);
            }
            Command::Stop => {
                let id = self.state.lock().unwrap().begin_stop();
                if let Some(id) = id {
                    self.backend.stop(self.clone(), app.clone(), id);
                }
            }
            Command::Cancel => {
                let id = self.state.lock().unwrap().cancel();
                if let Some(id) = id {
                    self.backend.cancel(Some(app), id);
                }
            }
        }
    }

    pub fn teardown_and_notify(&self, app: &AppHandle, reason: &str) {
        let (cancelled, event) = self.state.lock().unwrap().teardown(reason);
        if let Some(id) = cancelled {
            self.backend.cancel(Some(app), id);
        }
        self.emit(app, &event);
    }

    pub fn shutdown(&self) {
        let cancelled = self.state.lock().unwrap().cancel();
        if let Some(id) = cancelled {
            self.backend.cancel(None, id);
        }
    }
}

pub fn manage(app: &AppHandle, connection: Arc<Connection>) {
    app.manage(Arc::new(Controller::new(connection)));
}

/// Injects `window.werkschaftSpeech` into the main window's pinned dashboard
/// top frame.
pub fn inject(window: &WebviewWindow, source: &Url) {
    let Some(controller) = window.try_state::<Arc<Controller>>() else {
        return;
    };
    let pin = controller.connection.pinned_url();
    if !close::checks_draft(window.label())
        || destination(source, pin.as_ref()) != Destination::Dashboard
    {
        return;
    }
    let origin = json!(source.origin().ascii_serialization());
    let _ = window.eval(format!(
        "(() => {{ {SCRIPT}\ninstallWerkschaftSpeechBridge({origin}); }})()"
    ));
}

/// Handles a raw bridge message from the main window's top frame at `source`.
pub fn receive(window: WebviewWindow, epoch: usize, source: Url, message: &str) {
    let Some(controller) = window.try_state::<Arc<Controller>>() else {
        return;
    };
    let controller = controller.inner().clone();
    let pin = controller.connection.pinned_url();
    if !origin_allowed(
        window.label(),
        controller.connection.window_epoch(),
        epoch,
        &source,
        pin.as_ref(),
    ) {
        return;
    }
    let Some(command) = parse_command(message) else {
        return;
    };
    controller.handle_command(window.app_handle(), command);
}

/// Immediately releases audio handles and notifies the pinned top frame.
pub fn teardown(app: &AppHandle, reason: &str) {
    if let Some(controller) = app.try_state::<Arc<Controller>>() {
        controller.teardown_and_notify(app, reason);
    }
}

/// Detects minimize transitions from Tauri's `WindowEvent::Resized` and tears
/// down native capture on the `false -> true` edge.
pub fn on_window_resized(app: &AppHandle, window: &WebviewWindow) {
    let Some(controller) = app.try_state::<Arc<Controller>>() else {
        return;
    };
    let minimized = window.is_minimized().unwrap_or(false);
    let was_minimized = controller.minimized.swap(minimized, Ordering::AcqRel);
    if minimized && !was_minimized {
        controller.teardown_and_notify(app, "minimized");
    }
}

/// Releases native capture handles on window destruction or app exit.
pub fn shutdown(app: &AppHandle) {
    if let Some(controller) = app.try_state::<Arc<Controller>>() {
        controller.shutdown();
    }
}

/// Matches a requested BCP-47 locale tag against a list of supported tags
/// (case-insensitive exact match, then primary language subtag fallback).
pub fn match_supported_locale<'a>(requested: &str, supported: &'a [String]) -> Option<&'a str> {
    let req = requested.trim().replace('_', "-").to_ascii_lowercase();
    if req.is_empty() {
        return None;
    }
    if let Some(exact) = supported
        .iter()
        .find(|tag| tag.replace('_', "-").to_ascii_lowercase() == req)
    {
        return Some(exact.as_str());
    }
    let primary = req.split('-').next().unwrap_or("");
    if primary.is_empty() {
        return None;
    }
    supported
        .iter()
        .find(|tag| {
            tag.replace('_', "-")
                .to_ascii_lowercase()
                .split('-')
                .next()
                == Some(primary)
        })
        .map(String::as_str)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn msg(payload: serde_json::Value) -> String {
        format!("{PREFIX}{payload}")
    }

    #[test]
    fn origin_gate_allows_only_main_window_current_epoch_and_pinned_dashboard() {
        let pin = Url::parse("http://127.0.0.1:49015/").unwrap();
        let same = Url::parse("http://127.0.0.1:49015/sessions/1").unwrap();
        let wrong_port = Url::parse("http://127.0.0.1:49016/").unwrap();
        let remote = Url::parse("https://example.com/").unwrap();
        let local = Url::parse("tauri://localhost/index.html").unwrap();

        assert!(origin_allowed("main", 3, 3, &same, Some(&pin)));
        assert!(!origin_allowed("secondary-1", 3, 3, &same, Some(&pin)));
        assert!(!origin_allowed("main", 4, 3, &same, Some(&pin)));
        assert!(!origin_allowed("main", 3, 3, &wrong_port, Some(&pin)));
        assert!(!origin_allowed("main", 3, 3, &remote, Some(&pin)));
        assert!(!origin_allowed("main", 3, 3, &local, Some(&pin)));
        assert!(!origin_allowed("main", 3, 3, &same, None));
    }

    #[test]
    fn parse_command_validates_prefix_size_op_and_locale() {
        assert_eq!(
            parse_command(&msg(json!({"op": "status"}))),
            Some(Command::Status { locale: None })
        );
        assert_eq!(
            parse_command(&msg(json!({"op": "status", "locale": " ko_KR "}))),
            Some(Command::Status {
                locale: Some("ko-KR".into())
            })
        );
        assert_eq!(
            parse_command(&msg(json!({"op": "start", "locale": "en-US"}))),
            Some(Command::Start {
                locale: Some("en-US".into())
            })
        );
        assert_eq!(
            parse_command(&msg(json!({"op": "start", "locale": "   "}))),
            Some(Command::Start { locale: None })
        );
        assert_eq!(parse_command(&msg(json!({"op": "stop"}))), Some(Command::Stop));
        assert_eq!(
            parse_command(&msg(json!({"op": "cancel"}))),
            Some(Command::Cancel)
        );

        for bad in [
            "werkschaft:notify:{\"op\":\"status\"}".to_owned(),
            msg(json!({"op": "unknown"})),
            msg(json!({"op": "start", "locale": "en-US; rm -rf"})),
            msg(json!({"op": "start", "locale": "a".repeat(LOCALE_MAX + 1)})),
            format!("{PREFIX}not-json"),
            format!("{PREFIX}{{\"op\":\"start\",\"pad\":\"{}\"}}", "x".repeat(MAX_MESSAGE)),
        ] {
            assert_eq!(parse_command(&bad), None, "{bad:.80}");
        }
    }

    #[test]
    fn session_state_enforces_single_owner_and_teardown_lifecycle() {
        let mut s = SessionState::default();
        let id1 = s.start().expect("first start succeeds");
        assert_eq!(s.start(), Err(StartError::Busy), "second start refused");

        assert_eq!(
            s.on_partial(id1, "hello".into()),
            Some(SpeechEvent::Partial {
                text: "hello".into()
            })
        );
        assert_eq!(
            s.on_partial(id1 + 99, "stale".into()),
            None,
            "stale session id ignored"
        );

        assert_eq!(s.begin_stop(), Some(id1));
        assert_eq!(s.start(), Err(StartError::Busy), "still busy while stopping");
        assert_eq!(
            s.on_final(id1, "hello world".into()),
            Some(SpeechEvent::Final {
                text: "hello world".into()
            })
        );
        assert_eq!(s.on_final(id1, "duplicate".into()), None);

        // Fallback final when stop finishes without OS final callback.
        let id2 = s.start().expect("idle again");
        let _ = s.on_partial(id2, "partial text".into());
        assert_eq!(s.begin_stop(), Some(id2));
        assert_eq!(
            s.finish_stopping_with_fallback(id2),
            Some(SpeechEvent::Final {
                text: "partial text".into()
            })
        );

        // Teardown cancels active session and produces hidden notice.
        let id3 = s.start().expect("idle after fallback");
        let (cancelled, hidden) = s.teardown("minimized");
        assert_eq!(cancelled, Some(id3));
        assert_eq!(
            hidden,
            SpeechEvent::Hidden {
                reason: Some("minimized".into())
            }
        );
        assert_eq!(s.on_partial(id3, "late".into()), None);

        // Teardown when idle still emits hidden notice with no cancelled id.
        let (cancelled_idle, hidden_idle) = s.teardown("tray");
        assert_eq!(cancelled_idle, None);
        assert_eq!(
            hidden_idle,
            SpeechEvent::Hidden {
                reason: Some("tray".into())
            }
        );

        // Cancel returns id and resets to idle; error returns to idle.
        let id4 = s.start().unwrap();
        assert_eq!(s.cancel(), Some(id4));
        assert_eq!(s.cancel(), None);

        let id5 = s.start().unwrap();
        assert_eq!(
            s.on_error(id5, "audio_error", Some("no mic".into())),
            Some(SpeechEvent::Error {
                code: "audio_error".into(),
                reason: Some("no mic".into())
            })
        );
        assert!(s.start().is_ok());
    }

    #[test]
    fn event_script_targets_only_pinned_top_frame_and_serializes_ready() {
        let ev = SpeechEvent::Ready {
            available: false,
            on_device: false,
            locale: Some("ko-KR".into()),
            code: Some("privacy_disabled".into()),
            reason: Some("Enable Online speech recognition".into()),
        };
        let script = event_script("http://127.0.0.1:49015", &ev);
        assert!(script.starts_with(
            "if (window === window.top && location.origin === \"http://127.0.0.1:49015\") window.__werkschaftSpeechEvent?.("
        ));
        assert!(script.contains("\"type\":\"ready\""));
        assert!(script.contains("\"available\":false"));
        assert!(script.contains("\"code\":\"privacy_disabled\""));
    }

    #[test]
    fn match_supported_locale_prefers_exact_then_primary_language() {
        let supported = vec!["en-US".into(), "ko-KR".into(), "ja-JP".into()];
        assert_eq!(match_supported_locale("ko-KR", &supported), Some("ko-KR"));
        assert_eq!(match_supported_locale("ko_kr", &supported), Some("ko-KR"));
        assert_eq!(match_supported_locale("ko", &supported), Some("ko-KR"));
        assert_eq!(match_supported_locale("en-GB", &supported), Some("en-US"));
        assert_eq!(match_supported_locale("fr-FR", &supported), None);
    }
}
