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
    match payload.op.as_str() {
        "status" => Some(Command::Status {
            locale: sanitize_locale(payload.locale.as_deref())?,
        }),
        "start" => Some(Command::Start {
            locale: sanitize_locale(payload.locale.as_deref())?,
        }),
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

/// Algorithmic Hangul Jamo (NFD) -> precomposed Hangul Syllable (NFC) normalizer.
fn normalize_hangul_nfc(s: &str) -> String {
    if s.is_ascii() {
        return s.to_owned();
    }
    let mut out: Vec<char> = Vec::with_capacity(s.chars().count());
    for c in s.chars() {
        let code = c as u32;
        if (0x1161..=0x1175).contains(&code) {
            if let Some(&prev) = out.last() {
                let p = prev as u32;
                if (0x1100..=0x1112).contains(&p) {
                    let l = p - 0x1100;
                    let v = code - 0x1161;
                    let lv = 0xAC00 + (l * 21 + v) * 28;
                    if let Some(ch) = char::from_u32(lv) {
                        *out.last_mut().unwrap() = ch;
                        continue;
                    }
                }
            }
        } else if (0x11A8..=0x11C2).contains(&code) {
            if let Some(&prev) = out.last() {
                let p = prev as u32;
                if (0xAC00..=0xD7A3).contains(&p) && (p - 0xAC00) % 28 == 0 {
                    let t = code - 0x11A8 + 1;
                    if let Some(ch) = char::from_u32(p + t) {
                        *out.last_mut().unwrap() = ch;
                        continue;
                    }
                }
            }
        }
        out.push(c);
    }
    out.into_iter().collect()
}

fn is_boundary_punct(c: char) -> bool {
    matches!(
        c,
        '.' | ','
            | '!'
            | '?'
            | ';'
            | ':'
            | '"'
            | '\''
            | '('
            | ')'
            | '['
            | ']'
            | '\u{3002}'
            | '\u{3001}'
            | '\u{FF0C}'
            | '\u{FF0E}'
            | '\u{FF01}'
            | '\u{FF1F}'
            | '\u{FF1A}'
            | '\u{FF1B}'
            | '\u{2026}'
            | '\u{300C}'
            | '\u{300D}'
            | '\u{300E}'
            | '\u{300F}'
            | '\u{201C}'
            | '\u{201D}'
            | '\u{2018}'
            | '\u{2019}'
    )
}

fn is_sep_punct_or_ws(c: char) -> bool {
    c.is_whitespace()
        || matches!(
            c,
            '.' | ','
                | '!'
                | '?'
                | ';'
                | ':'
                | '\u{3002}'
                | '\u{3001}'
                | '\u{FF0C}'
                | '\u{FF0E}'
                | '\u{FF01}'
                | '\u{FF1F}'
                | '\u{FF1A}'
                | '\u{FF1B}'
                | '\u{2026}'
        )
}

fn tokens_with_end(nfc: &str) -> Vec<(String, usize)> {
    let mut out = Vec::new();
    let mut start: Option<usize> = None;
    for (idx, ch) in nfc.char_indices() {
        if ch.is_whitespace() {
            if let Some(s) = start.take() {
                let norm = nfc[s..idx]
                    .trim_matches(is_boundary_punct)
                    .to_lowercase();
                if !norm.is_empty() {
                    out.push((norm, idx));
                }
            }
        } else if start.is_none() {
            start = Some(idx);
        }
    }
    if let Some(s) = start {
        let norm = nfc[s..].trim_matches(is_boundary_punct).to_lowercase();
        if !norm.is_empty() {
            out.push((norm, nfc.len()));
        }
    }
    out
}

fn advance_past_sep(s: &str, pos: usize) -> String {
    let rest = &s[pos..];
    rest.trim_start_matches(is_sep_punct_or_ws).to_owned()
}

// Trade-off: an identical single-word repetition right across a pause is treated
// as flicker re-emission rather than intentional duplicate dictation.
fn strip_committed_prefix(committed_joined: &str, raw_next: &str) -> Option<String> {
    let c_nfc = normalize_hangul_nfc(committed_joined);
    let c_trim = c_nfc.trim();
    if c_trim.is_empty() {
        return None;
    }
    let i_nfc = normalize_hangul_nfc(raw_next);
    let i_trim = i_nfc.trim_start();
    if i_trim.is_empty() {
        return None;
    }
    let c_tokens = tokens_with_end(c_trim);
    if c_tokens.is_empty() {
        return None;
    }
    let i_tokens = tokens_with_end(i_trim);
    if i_tokens.len() >= c_tokens.len()
        && i_tokens
            .iter()
            .zip(c_tokens.iter())
            .all(|(a, b)| a.0 == b.0)
    {
        return Some(advance_past_sep(i_trim, i_tokens[c_tokens.len() - 1].1));
    }
    let c_core = c_trim.trim_end_matches(is_sep_punct_or_ws);
    if !c_core.is_empty() && c_core != c_trim && i_trim.starts_with(c_trim) {
        return Some(advance_past_sep(i_trim, c_trim.len()));
    }
    if !c_core.is_empty() && i_trim.starts_with(c_core) {
        let after = &i_trim[c_core.len()..];
        if after.is_empty() || after.chars().next().is_some_and(is_sep_punct_or_ws) {
            return Some(advance_past_sep(i_trim, c_core.len()));
        }
    }
    None
}

fn strip_committed_segments_prefix(segments: &[String], raw_next: &str) -> Option<String> {
    for start in 0..segments.len() {
        let suffix = segments[start..].join(" ");
        if let Some(rest) = strip_committed_prefix(&suffix, raw_next) {
            return Some(rest);
        }
    }
    None
}

fn is_disjoint_jump(prev: &str, next: &str) -> bool {
    let prev_nfc = normalize_hangul_nfc(prev);
    let next_nfc = normalize_hangul_nfc(next);
    let prev_trim = prev_nfc.trim();
    let next_trim = next_nfc.trim();
    if prev_trim.is_empty() || next_trim.is_empty() {
        return false;
    }
    let prev_chars = prev_trim.chars().count();
    let next_chars = next_trim.chars().count();
    let prev_tokens = tokens_with_end(prev_trim);
    let next_tokens = tokens_with_end(next_trim);
    if prev_tokens.is_empty() || next_tokens.is_empty() {
        return false;
    }

    if prev_chars < 6 && prev_tokens.len() < 2 {
        return false;
    }
    if next_chars * 2 >= prev_chars {
        return false;
    }

    let prefix_len = prev_trim
        .chars()
        .zip(next_trim.chars())
        .take_while(|(a, b)| a.to_lowercase().eq(b.to_lowercase()))
        .count();
    if prefix_len >= 2 {
        return false;
    }

    let prev_lower = prev_trim.to_lowercase();
    let next_lower = next_trim.to_lowercase();
    if next_chars >= 3 && prev_lower.contains(&next_lower) {
        return false;
    }

    let mut prev_set: Vec<&str> = Vec::new();
    for (tok, _) in &prev_tokens {
        if !prev_set.contains(&tok.as_str()) {
            prev_set.push(tok.as_str());
        }
    }
    let mut next_set: Vec<&str> = Vec::new();
    for (tok, _) in &next_tokens {
        if !next_set.contains(&tok.as_str()) {
            next_set.push(tok.as_str());
        }
    }
    let shared = next_set.iter().filter(|t| prev_set.contains(*t)).count();
    if shared == 0 {
        return true;
    }
    if shared >= next_set.len() {
        return false;
    }
    let union = prev_set.len() + next_set.len() - shared;
    prefix_len == 0 && shared * 3 <= union
}

/// Accumulates pre-pause segments when an OS recognizer resets its partial
/// buffer across silence pauses while keeping normal in-place refinement intact.
#[derive(Debug, Default, Clone, PartialEq, Eq)]
struct SpeechAccumulator {
    committed_segments: Vec<String>,
    current_partial: String,
}

impl SpeechAccumulator {
    fn current_text(&self) -> String {
        let committed = self.committed_segments.join(" ");
        let cur = self.current_partial.trim();
        if committed.is_empty() {
            return self.current_partial.clone();
        }
        if cur.is_empty() {
            return committed;
        }
        format!("{committed} {cur}")
    }

    fn has_speech(&self) -> bool {
        !self.committed_segments.is_empty() || !self.current_partial.trim().is_empty()
    }

    fn apply_incoming(&mut self, raw: &str, promote_empty: bool) -> String {
        let next = if self.committed_segments.is_empty() || raw.trim().is_empty() {
            raw.to_owned()
        } else {
            strip_committed_segments_prefix(&self.committed_segments, raw)
                .unwrap_or_else(|| raw.to_owned())
        };
        if next.trim().is_empty() {
            if promote_empty && !self.current_partial.trim().is_empty() {
                self.committed_segments
                    .push(normalize_hangul_nfc(self.current_partial.trim()));
                self.current_partial.clear();
            }
            return self.current_text();
        }
        if is_disjoint_jump(&self.current_partial, &next) {
            self.committed_segments
                .push(normalize_hangul_nfc(self.current_partial.trim()));
        }
        self.current_partial = next;
        self.current_text()
    }

    fn on_partial(&mut self, raw: &str) -> String {
        self.apply_incoming(raw, true)
    }

    fn on_final(&mut self, raw: &str) -> String {
        self.apply_incoming(raw, false)
    }
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
        acc: SpeechAccumulator,
    },
    Stopping {
        id: u64,
        acc: SpeechAccumulator,
    },
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum StartError {
    Busy,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum StopAction {
    Stop(u64),
    AlreadyStopping,
    Idle,
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
            acc: SpeechAccumulator::default(),
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
            SessionPhase::Listening { id: cur, acc } | SessionPhase::Stopping { id: cur, acc }
                if *cur == id =>
            {
                let accumulated = acc.on_partial(&text);
                Some(SpeechEvent::Partial { text: accumulated })
            }
            _ => None,
        }
    }

    pub fn on_final(&mut self, id: u64, text: String) -> Option<SpeechEvent> {
        if !self.is_active(id) {
            return None;
        }
        let mut acc = match std::mem::take(&mut self.phase) {
            SessionPhase::Listening { acc, .. } | SessionPhase::Stopping { acc, .. } => acc,
            SessionPhase::Idle => return None,
        };
        let final_text = acc.on_final(&text);
        Some(SpeechEvent::Final { text: final_text })
    }

    /// Transitions `Listening -> Stopping` and returns the stop action without
    /// clobbering an already-stopping session on duplicate stop requests.
    pub fn begin_stop(&mut self) -> StopAction {
        match self.phase {
            SessionPhase::Idle => StopAction::Idle,
            SessionPhase::Stopping { .. } => StopAction::AlreadyStopping,
            SessionPhase::Listening { .. } => {
                let SessionPhase::Listening { id, acc } = std::mem::take(&mut self.phase) else {
                    unreachable!();
                };
                self.phase = SessionPhase::Stopping { id, acc };
                StopAction::Stop(id)
            }
        }
    }

    /// If session `id` is still in `Stopping`, finishes it with the accumulated
    /// partial text so the frontend never stalls waiting for a final event.
    pub fn finish_stopping_with_fallback(&mut self, id: u64) -> Option<SpeechEvent> {
        if !self.is_stopping(id) {
            return None;
        }
        let SessionPhase::Stopping { acc, .. } = std::mem::take(&mut self.phase) else {
            return None;
        };
        Some(SpeechEvent::Final {
            text: acc.current_text(),
        })
    }

    /// Records a fatal error for session `id`, returning to `Idle`.
    /// If non-cancelled accumulated speech is already present, rescues it into
    /// `SpeechEvent::Final` so a silence-pause error never wipes spoken text.
    pub fn on_error(
        &mut self,
        id: u64,
        code: impl Into<String>,
        reason: Option<String>,
    ) -> Option<SpeechEvent> {
        if !self.is_active(id) {
            return None;
        }
        let acc = match std::mem::take(&mut self.phase) {
            SessionPhase::Listening { acc, .. } | SessionPhase::Stopping { acc, .. } => acc,
            SessionPhase::Idle => return None,
        };
        let code_str = code.into();
        if code_str != "cancelled" && acc.has_speech() {
            return Some(SpeechEvent::Final {
                text: acc.current_text(),
            });
        }
        Some(SpeechEvent::Error {
            code: code_str,
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
                let action = self.state.lock().unwrap().begin_stop();
                match action {
                    StopAction::Stop(id) => self.backend.stop(self.clone(), app.clone(), id),
                    StopAction::AlreadyStopping => {}
                    StopAction::Idle => {
                        self.emit(
                            app,
                            &SpeechEvent::Error {
                                code: "not_recording".to_owned(),
                                reason: Some(
                                    "No active speech recognition session.".to_owned(),
                                ),
                            },
                        );
                    }
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
            parse_command(&msg(json!({"op": "stop", "locale": "en-US@x"}))),
            Some(Command::Stop)
        );
        assert_eq!(
            parse_command(&msg(json!({"op": "cancel"}))),
            Some(Command::Cancel)
        );
        assert_eq!(
            parse_command(&msg(json!({"op": "cancel", "locale": "en-US@x"}))),
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

        assert_eq!(s.begin_stop(), StopAction::Stop(id1));
        assert_eq!(
            s.begin_stop(),
            StopAction::AlreadyStopping,
            "duplicate stop must not reset Stopping state"
        );
        assert_eq!(s.start(), Err(StartError::Busy), "still busy while stopping");
        assert_eq!(
            s.on_final(id1, "hello world".into()),
            Some(SpeechEvent::Final {
                text: "hello world".into()
            })
        );
        assert_eq!(s.on_final(id1, "duplicate".into()), None);
        assert_eq!(s.begin_stop(), StopAction::Idle);

        // Fallback final when stop finishes without OS final callback.
        let id2 = s.start().expect("idle again");
        let _ = s.on_partial(id2, "partial text".into());
        assert_eq!(s.begin_stop(), StopAction::Stop(id2));
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

    #[test]
    fn session_state_accumulates_across_pause_clear_and_disjoint_jump() {
        let mut s = SessionState::default();

        // 1. Clear-to-empty followed by new phrase and empty final.
        let id1 = s.start().unwrap();
        assert_eq!(
            s.on_partial(id1, "첫 번째 문장입니다".into()),
            Some(SpeechEvent::Partial {
                text: "첫 번째 문장입니다".into()
            })
        );
        assert_eq!(
            s.on_partial(id1, "".into()),
            Some(SpeechEvent::Partial {
                text: "첫 번째 문장입니다".into()
            })
        );
        assert_eq!(
            s.on_partial(id1, "두 번째 문장".into()),
            Some(SpeechEvent::Partial {
                text: "첫 번째 문장입니다 두 번째 문장".into()
            })
        );
        assert_eq!(
            s.on_final(id1, "".into()),
            Some(SpeechEvent::Final {
                text: "첫 번째 문장입니다 두 번째 문장".into()
            })
        );

        // 2. Guarded disjoint jump without empty frame.
        let id2 = s.start().unwrap();
        assert_eq!(
            s.on_partial(id2, "첫 번째 문장입니다".into()),
            Some(SpeechEvent::Partial {
                text: "첫 번째 문장입니다".into()
            })
        );
        assert_eq!(
            s.on_partial(id2, "두 번째".into()),
            Some(SpeechEvent::Partial {
                text: "첫 번째 문장입니다 두 번째".into()
            })
        );
        assert_eq!(
            s.on_final(id2, "두 번째 문장".into()),
            Some(SpeechEvent::Final {
                text: "첫 번째 문장입니다 두 번째 문장".into()
            })
        );

        // 3. Fallback when stopping preserves all accumulated segments.
        let id3 = s.start().unwrap();
        let _ = s.on_partial(id3, "첫 문장입니다".into());
        let _ = s.on_partial(id3, "".into());
        let _ = s.on_partial(id3, "둘째 문장".into());
        assert_eq!(s.begin_stop(), StopAction::Stop(id3));
        assert_eq!(
            s.finish_stopping_with_fallback(id3),
            Some(SpeechEvent::Final {
                text: "첫 문장입니다 둘째 문장".into()
            })
        );
    }

    #[test]
    fn session_state_refines_minor_hangul_and_nfd_in_place_and_dedups_cumulative_reemission() {
        let mut s = SessionState::default();

        // Short hypothesis and Hangul refinement must NOT false-promote.
        let id1 = s.start().unwrap();
        assert_eq!(
            s.on_partial(id1, "고양이".into()),
            Some(SpeechEvent::Partial {
                text: "고양이".into()
            })
        );
        assert_eq!(
            s.on_partial(id1, "거야 이".into()),
            Some(SpeechEvent::Partial {
                text: "거야 이".into()
            })
        );
        assert_eq!(
            s.on_final(id1, "거야 이".into()),
            Some(SpeechEvent::Final {
                text: "거야 이".into()
            })
        );

        let id2 = s.start().unwrap();
        assert_eq!(
            s.on_partial(id2, "안녕 하세".into()),
            Some(SpeechEvent::Partial {
                text: "안녕 하세".into()
            })
        );
        assert_eq!(
            s.on_partial(id2, "안녕하세요".into()),
            Some(SpeechEvent::Partial {
                text: "안녕하세요".into()
            })
        );
        assert_eq!(
            s.on_final(id2, "안녕하세요".into()),
            Some(SpeechEvent::Final {
                text: "안녕하세요".into()
            })
        );

        // Transient empty flicker followed by NFD/NFC cumulative re-emission must not duplicate.
        let nfd_first = "\u{110E}\u{1165}\u{11BA} \u{1107}\u{1165}\u{11AB}\u{110D}\u{1162} \u{1106}\u{116E}\u{11AB}\u{110C}\u{1161}\u{11BC}\u{110B}\u{1175}\u{11B8}\u{1102}\u{1175}\u{1103}\u{1161}";
        let id3 = s.start().unwrap();
        assert_eq!(
            s.on_partial(id3, nfd_first.into()),
            Some(SpeechEvent::Partial {
                text: nfd_first.into()
            })
        );
        assert_eq!(
            s.on_partial(id3, "".into()),
            Some(SpeechEvent::Partial {
                text: "첫 번째 문장입니다".into()
            })
        );
        assert_eq!(
            s.on_partial(id3, "첫 번째 문장입니다 두 번째".into()),
            Some(SpeechEvent::Partial {
                text: "첫 번째 문장입니다 두 번째".into()
            })
        );
        assert_eq!(
            s.on_final(id3, "첫 번째 문장입니다 두 번째".into()),
            Some(SpeechEvent::Final {
                text: "첫 번째 문장입니다 두 번째".into()
            })
        );

        // Windows-style cumulative partials stay single and clean.
        let id4 = s.start().unwrap();
        assert_eq!(
            s.on_partial(id4, "hello".into()),
            Some(SpeechEvent::Partial {
                text: "hello".into()
            })
        );
        assert_eq!(
            s.on_partial(id4, "hello world".into()),
            Some(SpeechEvent::Partial {
                text: "hello world".into()
            })
        );
        assert_eq!(
            s.on_partial(id4, "hello world again".into()),
            Some(SpeechEvent::Partial {
                text: "hello world again".into()
            })
        );
        assert_eq!(
            s.on_final(id4, "hello world".into()),
            Some(SpeechEvent::Final {
                text: "hello world".into()
            })
        );
    }

    #[test]
    fn session_state_rescues_spoken_text_on_non_cancelled_error() {
        let mut s = SessionState::default();
        let id1 = s.start().unwrap();
        let _ = s.on_partial(id1, "첫 번째 문장입니다".into());
        let _ = s.on_partial(id1, "".into());
        let _ = s.on_partial(id1, "두 번째".into());
        assert_eq!(
            s.on_error(id1, "recognition_error", Some("No speech detected".into())),
            Some(SpeechEvent::Final {
                text: "첫 번째 문장입니다 두 번째".into()
            })
        );
        // Zero-speech error still emits Error (also verifies error rescue returned to idle).
        let id2 = s.start().expect("returns to idle after error rescue");
        let _ = s.on_partial(id2, "   ".into());
        assert_eq!(
            s.on_error(id2, "recognizer_unavailable", Some("LSR 201".into())),
            Some(SpeechEvent::Error {
                code: "recognizer_unavailable".into(),
                reason: Some("LSR 201".into())
            })
        );
    }

    #[test]
    fn session_state_multi_pause_suffix_dedup_and_punctuation_guard() {
        let mut s = SessionState::default();

        // 1. 2 pauses + Final(seg2) on Stop must not duplicate seg2.
        let id1 = s.start().unwrap();
        let _ = s.on_partial(id1, "첫 번째 문장입니다".into());
        let _ = s.on_partial(id1, "".into());
        let _ = s.on_partial(id1, "두 번째 문장입니다".into());
        let _ = s.on_partial(id1, "".into());
        assert_eq!(
            s.on_final(id1, "두 번째 문장입니다".into()),
            Some(SpeechEvent::Final {
                text: "첫 번째 문장입니다 두 번째 문장입니다".into()
            })
        );

        // 2. Pause 1 (reset) + Pause 2 (transient empty flicker) + continuation of seg2.
        let id2 = s.start().unwrap();
        let _ = s.on_partial(id2, "첫 번째 문장입니다".into());
        let _ = s.on_partial(id2, "".into());
        let _ = s.on_partial(id2, "두 번째".into());
        let _ = s.on_partial(id2, "".into());
        assert_eq!(
            s.on_partial(id2, "두 번째 문장입니다".into()),
            Some(SpeechEvent::Partial {
                text: "첫 번째 문장입니다 두 번째 문장입니다".into()
            })
        );
        assert_eq!(
            s.on_final(id2, "두 번째 문장입니다".into()),
            Some(SpeechEvent::Final {
                text: "첫 번째 문장입니다 두 번째 문장입니다".into()
            })
        );

        // 3. Disjoint jump + transient empty flicker + continuation.
        let id3 = s.start().unwrap();
        let _ = s.on_partial(id3, "첫 번째 문장입니다".into());
        let _ = s.on_partial(id3, "두 번째".into());
        let _ = s.on_partial(id3, "".into());
        assert_eq!(
            s.on_partial(id3, "두 번째 문장입니다".into()),
            Some(SpeechEvent::Partial {
                text: "첫 번째 문장입니다 두 번째 문장입니다".into()
            })
        );
        assert_eq!(
            s.on_final(id3, "두 번째 문장입니다".into()),
            Some(SpeechEvent::Final {
                text: "첫 번째 문장입니다 두 번째 문장입니다".into()
            })
        );

        // 4. Punctuation-only incoming partial must not falsely trigger disjoint jump.
        assert!(!is_disjoint_jump("첫 번째 문장입니다", "."));
        assert!(!is_disjoint_jump("첫 번째 문장입니다", "..."));
        assert!(!is_disjoint_jump("첫 번째 문장입니다", "?"));
        let id4 = s.start().unwrap();
        let _ = s.on_partial(id4, "첫 번째 문장입니다".into());
        assert_eq!(
            s.on_partial(id4, ".".into()),
            Some(SpeechEvent::Partial { text: ".".into() })
        );
        assert_eq!(
            s.on_final(id4, "첫 번째 문장입니다.".into()),
            Some(SpeechEvent::Final {
                text: "첫 번째 문장입니다.".into()
            })
        );
    }

    #[test]
    fn normalize_hangul_nfc_boundaries() {
        assert_eq!(normalize_hangul_nfc("hello"), "hello");
        assert_eq!(normalize_hangul_nfc("\u{1100}\u{1161}"), "가"); // L + V
        assert_eq!(normalize_hangul_nfc("\u{1100}\u{1161}\u{11A8}"), "각"); // L + V + T
        assert_eq!(normalize_hangul_nfc("\u{AC00}\u{11A8}"), "각"); // LV + T
        assert_eq!(normalize_hangul_nfc("\u{AC01}\u{11A8}"), "\u{AC01}\u{11A8}"); // LVT + T stays distinct
        assert_eq!(normalize_hangul_nfc("\u{1100}\u{11A8}"), "\u{1100}\u{11A8}"); // L + T stays distinct
        assert_eq!(normalize_hangul_nfc("e\u{0301}"), "e\u{0301}"); // Non-Hangul combining mark untouched
    }
}
