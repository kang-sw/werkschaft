use std::{
    sync::{
        atomic::{AtomicBool, AtomicUsize, Ordering},
        Arc, Mutex,
    },
    thread,
    time::{Duration, Instant},
};

use tauri::{
    webview::cookie::{time, Cookie, SameSite},
    AppHandle, Manager, WebviewWindow,
};
use url::Url;

use crate::{
    close::MAIN_WINDOW,
    daemon::{self, Intent},
    diagnostics::Diagnostics,
    endpoint::{BrowserCookie, Daemon},
    navigation::{self, destination, Destination, CONNECT_VIEW, PYTHON_VIEW},
};

/// What the dashboard is told once it loads.
#[derive(Clone, Debug, PartialEq, Eq)]
enum Notice {
    /// A replacement was refused; the current daemon keeps serving.
    Refusal(&'static str),
    /// The bundled daemon failed and this last-known-good version serves.
    Fallback(String),
}

impl Notice {
    fn title(&self) -> String {
        match self {
            Self::Refusal(reason) => format!("Werkschaft — {reason}"),
            Self::Fallback(version) => fallback_title(version),
        }
    }

    fn text(&self) -> String {
        match self {
            Self::Refusal(reason) => (*reason).to_owned(),
            Self::Fallback(version) => format!(
                "Running on Safe Fallback v{version}. The bundled runtime failed to start, so Werkschaft recovered with the last known good version and is checking for a hotfix in the background."
            ),
        }
    }
}

/// The window title badge while a fallback daemon serves.
pub fn fallback_title(version: &str) -> String {
    format!("Werkschaft — Safe Fallback v{version}")
}

/// The title of the bundled recovery views.
fn local_title(page: &str, diagnostics: &Diagnostics) -> String {
    if page == PYTHON_VIEW {
        format!("Werkschaft — {} — Connection → Retry", diagnostics.reason)
    } else if diagnostics.floor_blocked {
        "Werkschaft — Recovery (Fallback Blocked)".to_owned()
    } else {
        "Werkschaft — Recovery".to_owned()
    }
}

#[derive(Default)]
pub struct Connection {
    daemon: Mutex<Option<Daemon>>,
    refusal: Mutex<Option<(usize, Notice)>>,
    /// The failure the recovery page shows, cleared when an attempt starts.
    failure: Mutex<Option<Diagnostics>>,
    next_refusal: AtomicUsize,
    recovery: Mutex<Option<(Instant, u8)>>,
    connecting: AtomicBool,
    copying: AtomicBool,
    hooks: Mutex<Hooks>,
    window_epoch: AtomicUsize,
    pub closing: AtomicBool,
    pub rebuilding: AtomicBool,
}

#[derive(Default, PartialEq, Eq)]
enum Hooks {
    #[default]
    Installing,
    Ready,
    Failed,
}

impl Connection {
    pub fn window_epoch(&self) -> usize {
        self.window_epoch.load(Ordering::Acquire)
    }

    fn daemon(&self) -> Option<Daemon> {
        self.daemon.lock().unwrap().clone()
    }

    /// The daemon the dashboard is pinned to, for the full quit.
    pub fn pinned_daemon(&self) -> Option<Daemon> {
        self.daemon()
    }

    #[cfg(test)]
    pub(crate) fn pin_for_test(&self, daemon: Daemon) {
        *self.daemon.lock().unwrap() = Some(daemon);
    }

    pub fn pinned_url(&self) -> Option<Url> {
        self.daemon.lock().unwrap().as_ref().map(|d| d.url.clone())
    }

    /// The failure the recovery page shows, if any.
    pub fn failure(&self) -> Option<Diagnostics> {
        self.failure.lock().unwrap().clone()
    }

    fn show_local(&self, window: &WebviewWindow, page: &str, diagnostics: Diagnostics) {
        *self.daemon.lock().unwrap() = None;
        *self.refusal.lock().unwrap() = None;
        crate::chrome::set_title(window, &local_title(page, &diagnostics));
        // The page URL stays exact: the payload arrives by evaluation once it
        // loads, never in a query or fragment.
        *self.failure.lock().unwrap() = Some(diagnostics);
        if window.navigate(navigation::local_view(page)).is_err() {
            crate::shell_log!("Could not load the bundled recovery view");
        }
    }

    pub fn setup_failed(&self, window: &WebviewWindow) {
        *self.hooks.lock().unwrap() = Hooks::Failed;
        self.show_local(
            window,
            CONNECT_VIEW,
            Diagnostics::reason("Could not initialize the webview"),
        );
    }

    pub fn retry_hooks(&self) -> bool {
        let mut hooks = self.hooks.lock().unwrap();
        if *hooks != Hooks::Failed {
            return false;
        }
        *hooks = Hooks::Installing;
        *self.recovery.lock().unwrap() = None;
        true
    }

    pub fn start(self: &Arc<Self>, window: WebviewWindow) {
        *self.hooks.lock().unwrap() = Hooks::Ready;
        self.connect(window, Intent::Connect);
    }

    pub fn main_window_closed(&self) {
        self.window_epoch.fetch_add(1, Ordering::AcqRel);
        self.connecting.store(false, Ordering::Release);
        self.closing.store(false, Ordering::Release);
        *self.hooks.lock().unwrap() = Hooks::Installing;
    }

    // Starts an attempt unless one runs: every attempt, a Retry from the
    // recovery page included, runs the whole sequence from no failure.
    fn begin(&self, intent: Intent) -> bool {
        if *self.hooks.lock().unwrap() != Hooks::Ready {
            return false;
        }
        if self.closing.load(Ordering::Acquire) || self.connecting.swap(true, Ordering::AcqRel) {
            return false;
        }
        if intent != Intent::Recover {
            *self.recovery.lock().unwrap() = None;
        }
        *self.failure.lock().unwrap() = None;
        true
    }

    pub fn connect(self: &Arc<Self>, window: WebviewWindow, intent: Intent) {
        if !self.begin(intent) {
            return;
        }
        let retained = self.daemon();
        if retained.is_none() {
            crate::chrome::set_title(&window, "Werkschaft — Connecting…");
            crate::recovery::push(&window, self);
        }
        let epoch = self.window_epoch();
        let state = self.clone();
        thread::spawn(move || {
            let paths = window.app_handle().path();
            let result = daemon::connect(
                paths
                    .resource_dir()
                    .map_err(|_| "Could not locate app resources"),
                paths
                    .app_local_data_dir()
                    .map_err(|_| "Could not locate user data"),
                intent,
            )
            .and_then(|answer| {
                let keep_document =
                    answer.refusal.is_some() && retained.as_ref() == Some(&answer.daemon);
                let cookie = if keep_document {
                    Ok(None)
                } else {
                    answer.daemon.browser_cookie()
                };
                let still_matches = if keep_document {
                    // A failed endpoint read does not evict an already-open
                    // document whose pinned listener still has this identity.
                    answer.daemon.is_healthy()
                } else {
                    answer.daemon.is_current()
                };
                if still_matches {
                    Ok((answer, keep_document, cookie))
                } else {
                    Err("Dashboard changed during connection".into())
                }
            });

            let target = window.clone();
            let ui_state = state.clone();
            if window
                .run_on_main_thread(move || {
                    if ui_state.window_epoch() != epoch {
                        return;
                    }
                    if !ui_state.closing.load(Ordering::Acquire) {
                        match result {
                            Ok((answer, keep_document, cookie)) => {
                                let notice = match (&answer.fallback, answer.refusal) {
                                    (Some(fallback), _) => Some(Notice::Fallback(
                                        fallback.lkg_version.clone().unwrap_or_default(),
                                    )),
                                    (None, reason) => reason.map(Notice::Refusal),
                                };
                                if answer.fallback.is_some() {
                                    // A hotfix is the way off the fallback.
                                    crate::updates::check_soon(target.app_handle());
                                }
                                let title = notice
                                    .as_ref()
                                    .map_or_else(|| "Werkschaft".to_owned(), Notice::title);
                                *ui_state.refusal.lock().unwrap() = notice.map(|notice| {
                                    (
                                        ui_state.next_refusal.fetch_add(1, Ordering::Relaxed),
                                        notice,
                                    )
                                });
                                if keep_document {
                                    ui_state.show_refusal(&target);
                                } else {
                                    ui_state.attach(&target, answer.daemon, cookie, &title);
                                }
                            }
                            Err(error) => {
                                let page = if error.python_required {
                                    PYTHON_VIEW
                                } else {
                                    CONNECT_VIEW
                                };
                                crate::shell_log!("{}", error.reason);
                                let diagnostics = error.diagnostics.map_or_else(
                                    || Diagnostics::reason(error.reason),
                                    |diagnostics| *diagnostics,
                                );
                                ui_state.show_local(&target, page, diagnostics);
                            }
                        }
                    }
                    ui_state.connecting.store(false, Ordering::Release);
                })
                .is_err()
                && state.window_epoch() == epoch
            {
                state.connecting.store(false, Ordering::Release);
            }
        });
    }

    pub fn show_refusal(self: &Arc<Self>, window: &WebviewWindow) {
        let Some(pin) = self.pinned_url() else { return };
        let Some((sequence, notice)) = self.refusal.lock().unwrap().clone() else {
            return;
        };
        crate::chrome::set_title(window, &notice.title());
        if !window
            .url()
            .is_ok_and(|url| destination(&url, Some(&pin)) == Destination::Dashboard)
        {
            return;
        }
        let epoch = self.window_epoch();
        let script = refusal_script(&pin, &notice);
        let state = self.clone();
        let target = window.clone();
        let _ = window.eval_with_callback(script, move |value| {
            if value != "1" {
                return;
            }
            let result = target.clone();
            let state = state.clone();
            let pin = pin.clone();
            let notice = notice.clone();
            let _ = target.run_on_main_thread(move || {
                if state.window_epoch() != epoch || state.pinned_url() != Some(pin) {
                    return;
                }
                let mut refusal = state.refusal.lock().unwrap();
                if refusal.as_ref() == Some(&(sequence, notice.clone())) {
                    *refusal = None;
                    // The fallback badge stays while the fallback serves.
                    if let Notice::Refusal(_) = notice {
                        crate::chrome::set_title(&result, "Werkschaft");
                    }
                }
            });
        });
    }

    fn attach(
        &self,
        window: &WebviewWindow,
        daemon: Daemon,
        cookie: Result<Option<BrowserCookie>, &'static str>,
        title: &str,
    ) {
        let root = &daemon.url;
        let target = window
            .url()
            .ok()
            .filter(|current| destination(current, Some(root)) == Destination::Dashboard)
            .unwrap_or_else(|| root.clone());
        let cookie_ready = match cookie {
            Ok(Some(cookie)) => {
                let cookie = Cookie::build((cookie.name, cookie.value))
                    .domain("127.0.0.1")
                    .path("/")
                    .http_only(true)
                    .same_site(SameSite::Strict)
                    .expires(
                        time::OffsetDateTime::now_utc()
                            + time::Duration::seconds(cookie.max_age as i64),
                    )
                    .build();
                window.set_cookie(cookie).is_ok()
            }
            Ok(None) | Err(_) => false,
        };
        if !cookie_ready {
            crate::shell_log!("Browser cookie unavailable; loading the daemon login gate if needed");
        }
        if self.closing.load(Ordering::Acquire) {
            return;
        }
        *self.daemon.lock().unwrap() = Some(daemon);
        crate::chrome::set_title(window, title);
        if window.navigate(target).is_err() {
            self.show_local(
                window,
                CONNECT_VIEW,
                Diagnostics::reason("Could not open the dashboard"),
            );
        }
    }

    // A bad renderer or a healthy listener that fails document loads can
    // repeatedly fail across successful health checks. Bound the whole burst.
    fn allow_recovery(&self, now: Instant) -> bool {
        let mut recovery = self.recovery.lock().unwrap();
        let (start, count) = recovery.get_or_insert((now, 0));
        if now.duration_since(*start) >= Duration::from_secs(30) {
            (*start, *count) = (now, 0);
        }
        if *count >= 2 {
            return false;
        }
        *count += 1;
        true
    }

    pub fn recover(self: &Arc<Self>, window: WebviewWindow, reason: &'static str) {
        if self.closing.load(Ordering::Acquire) || self.connecting.load(Ordering::Acquire) {
            return;
        }
        crate::shell_log!("{reason}");
        if self.allow_recovery(Instant::now()) {
            self.connect(window, Intent::Recover);
        } else {
            self.show_local(
                &window,
                CONNECT_VIEW,
                Diagnostics::reason("Lost connection to the dashboard"),
            );
        }
    }

    #[cfg(windows)]
    pub fn webview_failed(self: &Arc<Self>, window: WebviewWindow) {
        if self.closing.load(Ordering::Acquire) || self.rebuilding.swap(true, Ordering::AcqRel) {
            return;
        }
        if self.allow_recovery(Instant::now()) {
            // A dead WebView2 browser process invalidates its controllers.
            // RunEvent keeps the application alive until the replacement exists.
            *self.daemon.lock().unwrap() = None;
            *self.refusal.lock().unwrap() = None;
            *self.hooks.lock().unwrap() = Hooks::Installing;
            self.window_epoch.fetch_add(1, Ordering::AcqRel);
            self.connecting.store(false, Ordering::Release);
            if window.destroy().is_err() {
                self.rebuilding.store(false, Ordering::Release);
                self.setup_failed(&window);
            }
        } else {
            self.rebuilding.store(false, Ordering::Release);
            self.setup_failed(&window);
        }
    }

    pub fn watch(self: &Arc<Self>, app: AppHandle) {
        let state = Arc::downgrade(self);
        thread::spawn(move || loop {
            thread::sleep(Duration::from_secs(8));
            let Some(state) = state.upgrade() else { break };
            if state.connecting.load(Ordering::Acquire) || state.closing.load(Ordering::Acquire) {
                continue;
            }
            let Some(daemon) = state.daemon() else {
                continue;
            };
            if daemon.is_healthy() {
                continue;
            }
            let handle = app.clone();
            let _ = app.run_on_main_thread(move || {
                let mut pinned = state.daemon.lock().unwrap();
                if pinned.as_ref() != Some(&daemon) {
                    return;
                }
                if let Some(window) = crate::chrome::get(&handle, MAIN_WINDOW) {
                    drop(pinned);
                    state.recover(window, "Dashboard stopped responding");
                } else {
                    // A secondary can outlive the dashboard. Retire the pin
                    // on failure even when there is no main view to recover.
                    *pinned = None;
                }
            });
        });
    }

    pub fn copy_login_link(self: &Arc<Self>, window: WebviewWindow) {
        let Some(daemon) = self.daemon() else { return };
        if self.connecting.load(Ordering::Acquire) || self.copying.swap(true, Ordering::AcqRel) {
            return;
        }
        let current = window
            .url()
            .ok()
            .filter(|url| destination(url, Some(&daemon.url)) == Destination::Dashboard);
        let path = current.map_or_else(
            || "/".to_owned(),
            |mut url| {
                // A consumed login code is never part of the next destination.
                if url.query_pairs().any(|(key, _)| key == "login") {
                    let query: Vec<_> = url
                        .query_pairs()
                        .into_owned()
                        .filter(|(key, _)| key != "login")
                        .collect();
                    url.set_query(None);
                    if !query.is_empty() {
                        url.query_pairs_mut().extend_pairs(query);
                    }
                }
                url[url::Position::BeforePath..].to_owned()
            },
        );
        let state = self.clone();
        thread::spawn(move || {
            let link = daemon.browser_login_link(&path).ok().flatten();
            let target = window.clone();
            let ui_state = state.clone();
            if window
                .run_on_main_thread(move || {
                    if !ui_state.connecting.load(Ordering::Acquire)
                        && !ui_state.closing.load(Ordering::Acquire)
                        && ui_state.daemon().as_ref() == Some(&daemon)
                    {
                        let copied = link.is_some_and(|link| {
                            #[cfg(windows)]
                            return crate::windows::copy_text(&target, link.as_str());
                            #[cfg(target_os = "macos")]
                            arboard::Clipboard::new()
                                .and_then(|mut clipboard| clipboard.set_text(link.as_str()))
                                .is_ok()
                        });
                        let status = if copied {
                            "Browser login link copied"
                        } else {
                            "Could not copy browser login link"
                        };
                        crate::chrome::set_title(&target, &format!("Werkschaft — {status}"));
                    }
                    ui_state.copying.store(false, Ordering::Release);
                })
                .is_err()
            {
                state.copying.store(false, Ordering::Release);
            }
        });
    }
}

fn refusal_script(pin: &Url, notice: &Notice) -> String {
    let origin = serde_json::json!(pin.origin().ascii_serialization());
    let text = serde_json::json!(notice.text());
    let fallback = match notice {
        Notice::Refusal(_) => serde_json::Value::Null,
        Notice::Fallback(version) => serde_json::json!({ "version": version }),
    };
    format!(
        "({})({origin}, {text}, {fallback})",
        include_str!("daemon-notice.js")
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn automatic_recovery_is_bounded_across_reconnections() {
        let connection = Connection::default();
        let now = Instant::now();
        assert!(connection.allow_recovery(now));
        assert!(connection.allow_recovery(now + Duration::from_secs(1)));
        assert!(!connection.allow_recovery(now + Duration::from_secs(29)));
        assert!(connection.allow_recovery(now + Duration::from_secs(30)));
    }

    #[test]
    fn failed_hooks_can_be_retried_without_duplicate_installation() {
        let connection = Connection::default();
        assert!(!connection.retry_hooks());
        *connection.hooks.lock().unwrap() = Hooks::Failed;
        assert!(connection.retry_hooks());
        assert!(!connection.retry_hooks());
        *connection.hooks.lock().unwrap() = Hooks::Ready;
        assert!(!connection.retry_hooks());
    }

    #[test]
    fn every_attempt_starts_from_no_failure_and_runs_alone() {
        let connection = Connection::default();
        *connection.failure.lock().unwrap() = Some(Diagnostics::reason("old"));
        // Not before the webview hooks are ready.
        assert!(!connection.begin(Intent::Connect));
        assert!(connection.failure().is_some());
        *connection.hooks.lock().unwrap() = Hooks::Ready;
        assert!(connection.begin(Intent::Connect));
        assert!(connection.failure().is_none());
        // A Retry while an attempt runs does not start a second one.
        *connection.failure.lock().unwrap() = Some(Diagnostics::reason("new"));
        assert!(!connection.begin(Intent::Connect));
        assert!(connection.failure().is_some());
        connection.connecting.store(false, Ordering::Release);
        connection.closing.store(true, Ordering::Release);
        assert!(!connection.begin(Intent::Connect));
    }

    const REFUSED: &str = "No healthy replacement was found. Keeping the current connection.";

    #[test]
    fn notices_title_the_window_and_only_the_fallback_carries_a_version() {
        let pin = Url::parse("http://127.0.0.1:49015/").unwrap();
        let refusal = Notice::Refusal(REFUSED);
        assert_eq!(refusal.title(), format!("Werkschaft — {REFUSED}"));
        assert!(refusal_script(&pin, &refusal)
            .ends_with(&format!("(\"http://127.0.0.1:49015\", \"{REFUSED}\", null)")));
        let fallback = Notice::Fallback("0.52.0".into());
        assert_eq!(fallback.title(), "Werkschaft — Safe Fallback v0.52.0");
        assert!(fallback.text().starts_with("Running on Safe Fallback v0.52.0."));
        assert!(refusal_script(&pin, &fallback).ends_with(r#", {"version":"0.52.0"})"#));
        let mut blocked = Diagnostics::reason("x");
        assert_eq!(local_title(CONNECT_VIEW, &blocked), "Werkschaft — Recovery");
        blocked.floor_blocked = true;
        assert_eq!(
            local_title(CONNECT_VIEW, &blocked),
            "Werkschaft — Recovery (Fallback Blocked)"
        );
        assert_eq!(
            local_title(PYTHON_VIEW, &Diagnostics::reason("Python 3 is required")),
            "Werkschaft — Python 3 is required — Connection → Retry"
        );
    }

    #[test]
    fn closing_main_releases_the_connection_for_a_later_window() {
        let connection = Connection::default();
        let old_epoch = connection.window_epoch();
        *connection.hooks.lock().unwrap() = Hooks::Ready;
        connection.closing.store(true, Ordering::Release);
        connection.connecting.store(true, Ordering::Release);

        connection.main_window_closed();

        assert_ne!(connection.window_epoch(), old_epoch);
        assert!(!connection.closing.load(Ordering::Acquire));
        assert!(!connection.connecting.load(Ordering::Acquire));
        assert!(*connection.hooks.lock().unwrap() == Hooks::Installing);
    }
}
