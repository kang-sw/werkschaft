mod cache;
mod feed;

use std::{
    path::PathBuf,
    sync::{atomic::Ordering, Arc, Mutex},
    time::{Duration, Instant},
};

use serde_json::{json, Value};
use tauri::{async_runtime, menu::MenuItem, AppHandle, Manager, Runtime, WebviewWindow};
use tauri_plugin_dialog::MessageDialogKind;
use url::Url;

use crate::{
    close::{self, MAIN_WINDOW},
    navigation::{destination, local_view, Destination, CONNECT_VIEW, LOCAL_ORIGIN},
    shell::Connection,
};

use self::feed::{Action, Feed, Offer, Platform};

const SCRIPT: &str = include_str!("update-bridge.js");
const FIRST_CHECK: Duration = Duration::from_secs(8);
const INTERVAL: Duration = Duration::from_secs(6 * 60 * 60);
const MIN_CHECK_INTERVAL: Duration = Duration::from_secs(2);

/// The update item's label in the Connection menu and the tray.
pub const CHECK_LABEL: &str = "Check for Updates…";
const UNAVAILABLE: &str = "This build of Werkschaft does not update itself. Install a release build of Werkschaft to receive updates.";
const BUSY: &str = "An update check or download is already running. Try again in a moment.";

pub struct Controller<R: tauri::Runtime = tauri::Wry> {
    connection: Arc<Connection>,
    app: AppHandle<R>,
    feed: Feed,
    state: Mutex<State>,
    cache: Mutex<Option<cache::Cache>>,
}

struct Selection {
    offer: Arc<Offer>,
    bytes: Option<Arc<Vec<u8>>>,
}

#[derive(Default)]
struct State {
    checked: bool,
    busy: bool,
    last_check: Option<Instant>,
    skipped: Option<String>,
    selected: Option<Selection>,
}

impl State {
    fn phase(&self) -> Value {
        let Some(selected) = &self.selected else {
            return json!({ "phase": if self.checked { "none" } else { "unchecked" } });
        };
        let offer = &selected.offer;
        json!({
            "phase": if selected.bytes.is_some() { "ready" } else { "available" },
            "version": offer.update.version,
            "action": offer.action.label(),
            "skipped": self.skipped.as_deref() == Some(&offer.update.version)
        })
    }

    /// The version a restart would install.
    fn ready_version(&self) -> Option<String> {
        self.selected
            .as_ref()
            .filter(|selected| selected.bytes.is_some())
            .map(|selected| selected.offer.update.version.clone())
    }
}

#[derive(Debug, PartialEq, Eq)]
enum Command {
    Check(Action),
    Skip,
    Download,
    Restart,
    Recovery(Recovery),
}

/// The recovery page's fixed actions. None carries a path or a command.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Recovery {
    Retry,
    OpenLogs,
    CopyDiagnostics,
}

impl Command {
    fn parse(message: &str) -> Option<Self> {
        Some(match message {
            "werkschaft:update:check" => Self::Check(Action::Stable),
            "werkschaft:update:canary" => Self::Check(Action::Canary),
            "werkschaft:update:stable-return" => Self::Check(Action::StableReturn),
            "werkschaft:update:skip" => Self::Skip,
            "werkschaft:update:download" => Self::Download,
            "werkschaft:update:restart" => Self::Restart,
            "werkschaft:recovery:retry" => Self::Recovery(Recovery::Retry),
            "werkschaft:recovery:open-logs" => Self::Recovery(Recovery::OpenLogs),
            "werkschaft:recovery:copy-diagnostics" => Self::Recovery(Recovery::CopyDiagnostics),
            _ => return None,
        })
    }
}

/// The longest bridge message the native transports pass on.
pub const MAX_MESSAGE: usize = 40;

/// Whether a native transport passes a message on to [`receive`].
pub fn bridged(message: &str) -> bool {
    message.len() <= MAX_MESSAGE
        && (message.starts_with("werkschaft:update:") || message.starts_with("werkschaft:recovery:"))
}

/// Who asked for an install.
enum Requester {
    /// The pinned dashboard's Settings.
    Dashboard {
        window: WebviewWindow,
        pin: Url,
        epoch: usize,
    },
    /// The recovery page, with no dashboard loaded.
    Local { epoch: usize },
    /// The Connection menu or the tray, with the window possibly hidden.
    Native,
}

/// The Connection menu and tray items whose label follows the update state.
pub struct UpdateItems<R: Runtime>(Mutex<Vec<MenuItem<R>>>);

impl<R: Runtime> Default for UpdateItems<R> {
    fn default() -> Self {
        Self(Mutex::new(Vec::new()))
    }
}

impl<R: Runtime> UpdateItems<R> {
    pub fn add(app: &AppHandle<R>, item: MenuItem<R>) {
        if let Some(items) = app.try_state::<Self>() {
            items.0.lock().unwrap().push(item);
        }
    }
}

/// A native menu item whose text can change.
pub(crate) trait Label {
    fn set_label(&self, text: &str);
}

impl<R: Runtime> Label for MenuItem<R> {
    fn set_label(&self, text: &str) {
        let _ = self.set_text(text);
    }
}

/// `Check for Updates…`, or `Restart to Update (vX.Y.Z)` once an update is
/// downloaded and verified.
pub fn menu_label(ready: Option<&str>) -> String {
    ready.map_or_else(
        || CHECK_LABEL.to_owned(),
        |version| format!("Restart to Update (v{version})"),
    )
}

pub(crate) fn relabel(items: &[impl Label], ready: Option<&str>) {
    let label = menu_label(ready);
    for item in items {
        item.set_label(&label);
    }
}

impl<R: tauri::Runtime> Controller<R> {
    pub fn start(app: &AppHandle<R>, connection: Arc<Connection>) {
        let configured = app
            .config()
            .plugins
            .0
            .get("updater")
            .and_then(|value| value["pubkey"].as_str())
            .is_some_and(|key| !key.trim().is_empty());
        let Some(platform) = Platform::installed().filter(|_| configured) else {
            return;
        };
        let Ok(feed) = Feed::new(platform) else {
            crate::shell_log!("Could not initialize shell updates");
            return;
        };
        let controller = Arc::new(Self {
            connection,
            app: app.clone(),
            feed,
            state: Mutex::new(State {
                busy: true,
                ..State::default()
            }),
            cache: Mutex::new(None),
        });
        app.manage(controller.clone());
        let directory = app
            .path()
            .app_local_data_dir()
            .map(|path| path.join("shell-updates"));
        async_runtime::spawn(async move {
            if let Ok(directory) = directory {
                let restoring = controller.clone();
                let _ = async_runtime::spawn_blocking(move || restoring.restore(directory)).await;
            }
            controller.state.lock().unwrap().busy = false;
            controller.publish(json!({"state": "phase"}));
            tokio::time::sleep(FIRST_CHECK).await;
            loop {
                if controller.begin_check(false) {
                    controller.check(Action::Stable, false).await;
                }
                tokio::time::sleep(INTERVAL).await;
            }
        });
    }

    fn restore(&self, directory: PathBuf) {
        let Ok(cache) = cache::Cache::open(directory) else {
            crate::shell_log!("Could not open shell update storage");
            return;
        };
        let skipped = cache.skipped().unwrap_or_default();
        let staged = cache::load_header(&cache.stage);
        let staged_exists = !matches!(&staged, Ok(None));
        let restored = staged.ok().flatten().and_then(|(metadata, input)| {
            let offer = self.feed.restore(&self.app, metadata).ok()??;
            let bytes = cache::load_bytes(input, offer.size()).ok()?;
            offer.verify(&bytes).ok()?;
            Some(Selection {
                offer: Arc::new(offer),
                bytes: Some(Arc::new(bytes)),
            })
        });
        if staged_exists && restored.is_none() {
            // This also clears the former stage after the updated app relaunches.
            let _ = std::fs::remove_file(&cache.stage);
        }
        *self.cache.lock().unwrap() = Some(cache);
        let mut state = self.state.lock().unwrap();
        state.skipped = skipped;
        state.selected = restored;
    }

    fn begin_check(&self, manual: bool) -> bool {
        let mut state = self.state.lock().unwrap();
        if state.busy
            || (!manual
                && state
                    .selected
                    .as_ref()
                    .is_some_and(|s| s.bytes.is_some() || s.offer.action != Action::Stable))
            || state
                .last_check
                .is_some_and(|last| last.elapsed() < MIN_CHECK_INTERVAL)
        {
            return false;
        }
        state.last_check = Some(Instant::now());
        state.busy = true;
        true
    }

    fn accept_offer(&self, offer: Offer) -> Result<bool, &'static str> {
        let ready = {
            let state = self.state.lock().unwrap();
            match state.selected.as_ref().filter(|s| s.bytes.is_some()) {
                Some(s)
                    if s.offer.update.version == offer.update.version
                        && s.offer.action == offer.action =>
                {
                    return Ok(true);
                }
                Some(_) => true,
                None => false,
            }
        };
        if ready {
            let cache = self.cache.lock().unwrap();
            cache
                .as_ref()
                .ok_or("Shell update storage is unavailable.")?
                .discard_stage()
                .map_err(|_| "Could not clear the previous shell update.")?;
        }
        self.state.lock().unwrap().selected = Some(Selection {
            offer: Arc::new(offer),
            bytes: None,
        });
        Ok(false)
    }

    /// Checks the feed and publishes the outcome, which it also returns.
    async fn check(self: &Arc<Self>, action: Action, manual: bool) -> Value {
        let result = match self.feed.check(&self.app, action).await {
            Ok((Some(offer), limited)) => {
                let version = offer.update.version.clone();
                let controller = self.clone();
                async_runtime::spawn_blocking(move || controller.accept_offer(offer))
                    .await
                    .unwrap_or(Err("Could not select the shell update."))
                    .map(|ready| (Some((version, ready)), limited))
            }
            Ok((None, limited)) => Ok((None, limited)),
            Err(message) => Err(message),
        };
        let current = self.app.package_info().version.to_string();
        let event = {
            let mut state = self.state.lock().unwrap();
            state.busy = false;
            match result {
                Ok((Some((version, same_ready)), _)) => {
                    state.checked = true;
                    let skipped = state.skipped.as_deref() == Some(&version)
                        && action != Action::StableReturn;
                    json!({
                        "state": if same_ready { "ready" } else if skipped && !manual { "phase" } else { "available" },
                        "version": version, "current": current, "skipped": skipped, "action": action.label()
                    })
                }
                Ok((None, limited)) => {
                    state.checked = true;
                    if state
                        .selected
                        .as_ref()
                        .is_some_and(|s| s.bytes.is_none() && s.offer.action == action)
                    {
                        state.selected = None;
                    }
                    json!({
                        "state": if manual { "current" } else { "phase" },
                        "version": current, "action": action.label(), "limited": limited
                    })
                }
                Err(message) => {
                    crate::shell_log!("Shell update check failed: {message}");
                    json!({ "state": if manual { "error" } else { "phase" }, "action": action.label() })
                }
            }
        };
        self.publish(event.clone());
        event
    }

    fn verify_and_stage(&self, offer: &Offer, bytes: &[u8]) -> Result<(), &'static str> {
        offer.verify(bytes)?;
        let cache = self.cache.lock().unwrap();
        let cache = cache
            .as_ref()
            .ok_or("Shell update storage is unavailable.")?;
        cache::stage(&cache.stage, &offer.metadata, bytes)
            .map_err(|_| "Could not save the verified shell update.")
    }

    fn begin_selection(&self, command: &Command) -> Option<(Arc<Offer>, Option<Arc<Vec<u8>>>)> {
        let mut state = self.state.lock().unwrap();
        let selected = state.selected.as_ref()?;
        let valid = match command {
            Command::Download => selected.bytes.is_none(),
            Command::Restart => selected.bytes.is_some(),
            Command::Skip => {
                selected.bytes.is_none()
                    && selected.offer.action != Action::StableReturn
                    && state.skipped.as_deref() != Some(&selected.offer.update.version)
            }
            _ => false,
        };
        if state.busy || !valid {
            return None;
        }
        let selection = (selected.offer.clone(), selected.bytes.clone());
        state.busy = true;
        Some(selection)
    }

    /// Downloads and verifies an offer and publishes the outcome, which it
    /// also returns.
    async fn download(self: &Arc<Self>, offer: Arc<Offer>) -> Value {
        let version = &offer.update.version;
        self.publish(json!({"state": "downloading", "version": version}));
        let staged = match self.feed.download(&offer).await {
            Ok(bytes) => {
                let updater = self.clone();
                let selected = offer.clone();
                async_runtime::spawn_blocking(move || {
                    updater.verify_and_stage(&selected, &bytes)?;
                    Ok(bytes)
                })
                .await
                .unwrap_or(Err("Could not verify the shell update."))
            }
            Err(message) => Err(message),
        };
        let event = {
            let mut state = self.state.lock().unwrap();
            state.busy = false;
            match staged {
                Ok(bytes) => {
                    state
                        .selected
                        .as_mut()
                        .expect("selection stays while busy")
                        .bytes = Some(Arc::new(bytes));
                    json!({"state": "ready", "version": version, "action": offer.action.label()})
                }
                Err(message) => {
                    crate::shell_log!("Shell update download failed: {message}");
                    failure("download-error", version, message)
                }
            }
        };
        self.publish(event.clone());
        event
    }

    fn skip(&self, version: &str) {
        if let Some(cache) = self.cache.lock().unwrap().as_ref() {
            if cache.skip(version).is_err() {
                crate::shell_log!("Could not save the skipped shell update");
            }
        }
        let mut state = self.state.lock().unwrap();
        state.skipped = Some(version.to_owned());
        state.busy = false;
        drop(state);
        self.publish(json!({"state": "phase"}));
    }

    /// Sends the update state to the one page that may show it: the pinned
    /// dashboard while connected, the recovery page while disconnected. The
    /// Connection menu and tray labels follow it too.
    fn publish(&self, mut event: Value) {
        let (phase, ready) = {
            let state = self.state.lock().unwrap();
            (state.phase(), state.ready_version())
        };
        event["phase"] = phase;
        let pin = self.connection.pinned_url();
        let epoch = self.connection.window_epoch();
        let connection = self.connection.clone();
        let app = self.app.clone();
        let _ = self.app.run_on_main_thread(move || {
            if let Some(items) = app.try_state::<UpdateItems<R>>() {
                relabel(items.0.lock().unwrap().as_slice(), ready.as_deref());
            }
            if connection.window_epoch() != epoch || connection.pinned_url() != pin {
                return;
            }
            // The dashboard's webview: on Windows its window also holds the
            // title strip, so Tauri no longer lists it as a webview window.
            #[cfg(target_os = "macos")]
            let window = app.get_webview_window(MAIN_WINDOW);
            #[cfg(windows)]
            let window = app.get_webview(MAIN_WINDOW);
            let Some(window) = window else { return };
            let Some(origin) = window
                .url()
                .ok()
                .and_then(|source| page_origin(&source, pin.as_ref()))
            else {
                return;
            };
            let origin = json!(origin);
            let _ = window.eval(format!(
                "if (window === window.top && location.origin === {origin}) window.__werkschaftUpdateStatus?.({event});"
            ));
        });
    }

    /// Checks in the background unless a check ran or an update waits:
    /// a fallback start's way to a hotfix.
    fn check_now(self: &Arc<Self>) {
        if self.begin_check(false) {
            let controller = self.clone();
            async_runtime::spawn(async move {
                controller.check(Action::Stable, false).await;
            });
        }
    }
}

impl Controller<tauri::Wry> {
    /// Installs a verified update and restarts, or returns why it did not.
    fn install(&self, offer: &Offer, bytes: &[u8], from: &Requester) -> Option<&'static str> {
        let connection = &self.connection;
        let current = |epoch: &usize| connection.window_epoch() == *epoch;
        let owns_close = match from {
            Requester::Dashboard { pin, epoch, .. } => {
                current(epoch) && connection.pinned_url().as_ref() == Some(pin)
            }
            Requester::Local { epoch } => current(epoch) && connection.pinned_url().is_none(),
            Requester::Native => true,
        } && !connection.closing.swap(true, Ordering::AcqRel);
        let message = if !owns_close {
            "The window is closing or has changed. Try again."
        } else if !self.drafts_ready(from) {
            "Could not save the dashboard draft. Send or discard it, then try again."
        } else if offer.update.install(bytes).is_err() {
            "Could not install the shell update. Retry or reinstall Werkschaft."
        } else {
            // On Windows the updater exits after starting the installer,
            // which relaunches the installed app. macOS has swapped the bundle.
            self.app.request_restart();
            return None;
        };
        crate::shell_log!("Shell update not applied: {message}");
        if owns_close {
            match from {
                Requester::Dashboard { window, .. } => close::release_document(window),
                Requester::Local { .. } => (),
                Requester::Native => {
                    for window in crate::chrome::all(&self.app) {
                        close::release_document(&window);
                    }
                }
            }
            connection.closing.store(false, Ordering::Release);
        }
        self.state.lock().unwrap().busy = false;
        self.publish(failure("apply-error", &offer.update.version, message));
        Some(message)
    }

    // The recovery page has no dashboard draft. A menu or tray restart while
    // connected checks every window, as quitting does.
    fn drafts_ready(&self, from: &Requester) -> bool {
        match from {
            Requester::Dashboard { window, pin, .. } => {
                close::draft_ready(window, &pin.origin().ascii_serialization())
            }
            Requester::Local { .. } => true,
            Requester::Native => {
                self.connection.pinned_url().is_none()
                    || close::every(&crate::chrome::all(&self.app), |window| {
                        close::drafts_saved(window, &self.connection)
                    })
            }
        }
    }

    async fn ask(&self, title: String, message: String, proceed: &'static str) -> bool {
        let app = self.app.clone();
        async_runtime::spawn_blocking(move || {
            let main = crate::chrome::get(&app, MAIN_WINDOW);
            close::confirm(&app, main.as_ref(), &title, &message, "Later", proceed)
        })
        .await
        .unwrap_or(false)
    }

    async fn tell(&self, kind: MessageDialogKind, title: &'static str, message: String) {
        let app = self.app.clone();
        let _ = async_runtime::spawn_blocking(move || {
            let main = crate::chrome::get(&app, MAIN_WINDOW);
            close::inform(&app, main.as_ref(), kind, title, &message);
        })
        .await;
    }

    /// The Connection menu's and the tray's update item. The window may be
    /// hidden, blank, or disconnected, so every answer is a native dialog.
    async fn from_menu(self: Arc<Self>) {
        if self.state.lock().unwrap().ready_version().is_some() {
            return self.offer_restart().await;
        }
        if !self.begin_check(true) {
            return self
                .tell(MessageDialogKind::Info, "Updates", BUSY.to_owned())
                .await;
        }
        let event = self.check(Action::Stable, true).await;
        let version = event["version"].as_str().unwrap_or_default().to_owned();
        match event["state"].as_str() {
            Some("ready") => self.offer_restart().await,
            Some("available") => {
                if !self
                    .ask(
                        format!("Werkschaft v{version} is available"),
                        format!("Download and install Werkschaft v{version}? Werkschaft asks before it restarts."),
                        "Download",
                    )
                    .await
                {
                    return;
                }
                let Some((offer, _)) = self.begin_selection(&Command::Download) else {
                    return self
                        .tell(MessageDialogKind::Info, "Updates", BUSY.to_owned())
                        .await;
                };
                let event = self.download(offer).await;
                if event["state"] == "ready" {
                    self.offer_restart().await;
                } else {
                    let message = event["message"]
                        .as_str()
                        .unwrap_or("Could not download the shell update.");
                    self.tell(
                        MessageDialogKind::Warning,
                        "Update not downloaded",
                        message.to_owned(),
                    )
                    .await;
                }
            }
            Some("current") => {
                self.tell(
                    MessageDialogKind::Info,
                    "Werkschaft is up to date",
                    format!("Werkschaft v{version} is the newest version."),
                )
                .await;
            }
            _ => {
                self.tell(
                    MessageDialogKind::Warning,
                    "Update check failed",
                    "Could not check for updates. Check the network connection and try again."
                        .to_owned(),
                )
                .await;
            }
        }
    }

    async fn offer_restart(self: &Arc<Self>) {
        let Some(version) = self.state.lock().unwrap().ready_version() else {
            return;
        };
        if !self
            .ask(
                format!("Restart to Update (v{version})"),
                format!("Werkschaft v{version} is downloaded and verified. Restart Werkschaft now to finish the update?"),
                "Restart",
            )
            .await
        {
            return;
        }
        let Some((offer, Some(bytes))) = self.begin_selection(&Command::Restart) else {
            return;
        };
        let controller = self.clone();
        let failed = async_runtime::spawn_blocking(move || {
            controller.install(&offer, &bytes, &Requester::Native)
        })
        .await
        .ok()
        .flatten();
        if let Some(message) = failed {
            self.tell(
                MessageDialogKind::Warning,
                "Update not applied",
                message.to_owned(),
            )
            .await;
        }
    }
}

/// The update item in the Connection menu or the tray was chosen.
pub fn menu_check(app: &AppHandle) {
    match app.try_state::<Arc<Controller>>() {
        Some(controller) => {
            let controller = controller.inner().clone();
            async_runtime::spawn(controller.from_menu());
        }
        // A development or unpackaged build has no updater: say so.
        None => {
            let app = app.clone();
            std::thread::spawn(move || {
                let main = crate::chrome::get(&app, MAIN_WINDOW);
                close::inform(
                    &app,
                    main.as_ref(),
                    MessageDialogKind::Info,
                    "Updates unavailable",
                    UNAVAILABLE,
                );
            });
        }
    }
}

/// Starts a background update check, as soon as the updater is ready.
pub fn check_soon(app: &AppHandle) {
    if let Some(controller) = app.try_state::<Arc<Controller>>() {
        controller.check_now();
    }
}

fn failure(state: &str, version: &str, message: &str) -> Value {
    json!({ "state": state, "version": version, "message": message, "downloadPage": feed::release_page(version) })
}

/// Which page may drive the bridges.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Origin {
    Dashboard,
    Local,
}

/// The one top-level page authorized by connection state, never both: the
/// pinned dashboard while connected; while disconnected, the bundled recovery
/// page at exactly `/index.html` (no query, no fragment, not the Python page).
pub fn authorized(main_frame: bool, source: &Url, pin: Option<&Url>) -> Option<Origin> {
    if !main_frame {
        return None;
    }
    match pin {
        Some(pin) => {
            (destination(source, Some(pin)) == Destination::Dashboard).then_some(Origin::Dashboard)
        }
        None => (*source == local_view(CONNECT_VIEW)).then_some(Origin::Local),
    }
}

/// The page origin the bridge scripts compare `location.origin` with.
fn page_origin(source: &Url, pin: Option<&Url>) -> Option<String> {
    Some(match authorized(true, source, pin)? {
        Origin::Dashboard => source.origin().ascii_serialization(),
        // A custom scheme has an opaque URL origin; the page reports this.
        Origin::Local => LOCAL_ORIGIN.to_owned(),
    })
}

/// Whether a message from `source` is admitted now: its frame and page are
/// the authorized one, the window it was bound to is still current, and no
/// close is under way.
fn admit(
    connection: &Connection,
    epoch: usize,
    main_frame: bool,
    source: &Url,
    current: Option<&Url>,
) -> Option<Origin> {
    let pin = connection.pinned_url();
    let origin = authorized(main_frame, source, pin.as_ref())?;
    let unchanged = current.and_then(|url| authorized(true, url, pin.as_ref())) == Some(origin);
    (epoch == connection.window_epoch()
        && !connection.closing.load(Ordering::Acquire)
        && unchanged)
        .then_some(origin)
}

#[derive(Debug, PartialEq, Eq)]
enum Route {
    Recovery(Recovery),
    Update(Command),
    Drop,
}

/// Recovery actions exist only on the recovery page; update commands go to
/// the updater from either authorized page.
fn route(command: Command, origin: Origin) -> Route {
    match command {
        Command::Recovery(action) if origin == Origin::Local => Route::Recovery(action),
        Command::Recovery(_) => Route::Drop,
        command => Route::Update(command),
    }
}

pub fn inject(window: &WebviewWindow, source: &Url) {
    let Some(controller) = window.try_state::<Arc<Controller>>() else {
        return;
    };
    let pin = controller.connection.pinned_url();
    let Some(origin) = page_origin(source, pin.as_ref()) else {
        return;
    };
    let version = &controller.app.package_info().version;
    let origin = json!(origin);
    let channel = json!({ "version": version.to_string(), "canary": feed::canary(version) });
    let phase = controller.state.lock().unwrap().phase();
    let _ = window.eval(format!(
        "(() => {{ {SCRIPT}\ninstallWerkschaftUpdateBridge({origin}, {channel}, {phase}); }})()"
    ));
}

pub fn receive(window: WebviewWindow, epoch: usize, main_frame: bool, source: Url, message: &str) {
    let Some(command) = Command::parse(message) else {
        return;
    };
    let Some(connection) = window
        .try_state::<Arc<Connection>>()
        .map(|state| state.inner().clone())
    else {
        return;
    };
    let current = window.url().ok();
    let Some(origin) = admit(&connection, epoch, main_frame, &source, current.as_ref()) else {
        return;
    };
    let command = match route(command, origin) {
        Route::Recovery(action) => return crate::recovery::run(window, connection, action),
        Route::Drop => return,
        Route::Update(command) => command,
    };
    let Some(controller) = window
        .try_state::<Arc<Controller>>()
        .map(|state| state.inner().clone())
    else {
        return;
    };
    match command {
        Command::Check(action) => {
            if (action == Action::StableReturn
                && !feed::canary(&controller.app.package_info().version))
                || !controller.begin_check(true)
            {
                return;
            }
            async_runtime::spawn(async move {
                controller.check(action, true).await;
            });
        }
        other => {
            let Some((offer, bytes)) = controller.begin_selection(&other) else {
                return;
            };
            match other {
                Command::Download => {
                    async_runtime::spawn(async move {
                        controller.download(offer).await;
                    });
                }
                Command::Skip => {
                    async_runtime::spawn_blocking(move || controller.skip(&offer.update.version));
                }
                Command::Restart => {
                    let from = match (origin, connection.pinned_url()) {
                        (Origin::Dashboard, Some(pin)) => Requester::Dashboard {
                            window,
                            pin,
                            epoch,
                        },
                        _ => Requester::Local { epoch },
                    };
                    async_runtime::spawn_blocking(move || {
                        controller.install(
                            &offer,
                            bytes.expect("verified staged bytes").as_slice(),
                            &from,
                        );
                    });
                }
                _ => unreachable!("selection command"),
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::cell::RefCell;

    #[test]
    fn fixed_commands_and_verified_top_level_origin() {
        let pinned = Url::parse("http://127.0.0.1:52315/").unwrap();
        assert_eq!(
            authorized(true, &pinned.join("/sessions").unwrap(), Some(&pinned)),
            Some(Origin::Dashboard)
        );
        assert_eq!(authorized(false, &pinned, Some(&pinned)), None);
        assert_eq!(authorized(true, &pinned, None), None);
        for source in [
            "http://127.0.0.1:52316/",
            "http://localhost:52315/",
            "tauri://localhost/index.html",
            "http://tauri.localhost/index.html",
            "https://example.com/",
            "blob:http://127.0.0.1:52315/object",
        ] {
            assert_eq!(
                authorized(true, &Url::parse(source).unwrap(), Some(&pinned)),
                None,
                "{source}"
            );
        }
        assert_eq!(
            Command::parse("werkschaft:update:download"),
            Some(Command::Download)
        );
        assert_eq!(
            Command::parse("werkschaft:update:download:https://example.com"),
            None
        );
        assert_eq!(Command::parse("werkschaft:update:skip:1.2.3"), None);
    }

    // Connected, only the dashboard; disconnected, only the exact recovery
    // page. Never both at once.
    #[test]
    fn the_dashboard_and_recovery_page_gates_are_mutually_exclusive() {
        let pin = Url::parse("http://127.0.0.1:52315/").unwrap();
        let local = local_view(CONNECT_VIEW);
        assert_eq!(authorized(true, &local, None), Some(Origin::Local));
        assert_eq!(authorized(true, &local, Some(&pin)), None);
        assert_eq!(authorized(true, &pin, None), None);
        assert_eq!(authorized(false, &local, None), None);
        for suffix in ["?retry=1", "#retry", "/../python-missing.html"] {
            let source = Url::parse(&format!("{LOCAL_ORIGIN}{CONNECT_VIEW}{suffix}")).unwrap();
            assert_eq!(authorized(true, &source, None), None, "{suffix}");
        }
        for path in ["/python-missing.html", "/strip.html", "/", "/index.htm"] {
            let source = Url::parse(&format!("{LOCAL_ORIGIN}{path}")).unwrap();
            assert_eq!(authorized(true, &source, None), None, "{path}");
        }
        let other = if LOCAL_ORIGIN.starts_with("tauri") {
            "http://tauri.localhost/index.html"
        } else {
            "tauri://localhost/index.html"
        };
        assert_eq!(authorized(true, &Url::parse(other).unwrap(), None), None);
        assert_eq!(page_origin(&local, None).as_deref(), Some(LOCAL_ORIGIN));
        assert_eq!(
            page_origin(&pin, Some(&pin)).as_deref(),
            Some("http://127.0.0.1:52315")
        );
    }

    #[test]
    fn a_recovery_message_needs_the_current_window_and_no_close() {
        let connection = Connection::default();
        let local = local_view(CONNECT_VIEW);
        let epoch = connection.window_epoch();
        assert_eq!(
            admit(&connection, epoch, true, &local, Some(&local)),
            Some(Origin::Local)
        );
        // A subframe, a stale window, and a window that left the page.
        assert_eq!(admit(&connection, epoch, false, &local, Some(&local)), None);
        assert_eq!(admit(&connection, epoch + 1, true, &local, Some(&local)), None);
        let python = local_view(crate::navigation::PYTHON_VIEW);
        assert_eq!(admit(&connection, epoch, true, &local, Some(&python)), None);
        assert_eq!(admit(&connection, epoch, true, &local, None), None);
        connection.closing.store(true, Ordering::Release);
        assert_eq!(admit(&connection, epoch, true, &local, Some(&local)), None);
    }

    #[test]
    fn while_connected_only_the_dashboard_is_admitted_and_never_for_recovery() {
        let connection = Connection::default();
        let daemon = crate::endpoint::Daemon::fixture(7, "0.52.1");
        let pin = daemon.url.clone();
        connection.pin_for_test(daemon);
        let epoch = connection.window_epoch();
        let page = pin.join("/settings").unwrap();
        let local = local_view(CONNECT_VIEW);
        assert_eq!(
            admit(&connection, epoch, true, &page, Some(&page)),
            Some(Origin::Dashboard)
        );
        // The recovery page is not admitted while connected, even as current.
        assert_eq!(admit(&connection, epoch, true, &local, Some(&local)), None);
        // The window left the dashboard after the message was sent.
        assert_eq!(admit(&connection, epoch, true, &page, Some(&local)), None);
        assert_eq!(admit(&connection, epoch, false, &page, Some(&page)), None);
        assert_eq!(
            route(Command::Recovery(Recovery::Retry), Origin::Dashboard),
            Route::Drop
        );
        assert_eq!(
            route(Command::Recovery(Recovery::OpenLogs), Origin::Local),
            Route::Recovery(Recovery::OpenLogs)
        );
        assert_eq!(
            route(Command::Download, Origin::Local),
            Route::Update(Command::Download)
        );
        assert_eq!(
            route(Command::Restart, Origin::Dashboard),
            Route::Update(Command::Restart)
        );
    }

    #[test]
    fn recovery_actions_are_a_fixed_set_the_transports_pass_on() {
        for (message, action) in [
            ("werkschaft:recovery:retry", Recovery::Retry),
            ("werkschaft:recovery:open-logs", Recovery::OpenLogs),
            (
                "werkschaft:recovery:copy-diagnostics",
                Recovery::CopyDiagnostics,
            ),
        ] {
            assert_eq!(Command::parse(message), Some(Command::Recovery(action)));
            assert!(bridged(message), "{message}");
        }
        for message in [
            "werkschaft:recovery:open-logs:/etc",
            "werkschaft:recovery:open",
            "werkschaft:recovery:run:cmd.exe",
            "werkschaft:recovery:",
        ] {
            assert_eq!(Command::parse(message), None, "{message}");
        }
        assert!(bridged("werkschaft:update:stable-return"));
        assert!(!bridged("werkschaft:recovery:copy-diagnostics-and-more-text"));
        assert!(!bridged("werkschaft:notify:x"));
    }

    struct Item(RefCell<String>);

    impl Label for Item {
        fn set_label(&self, text: &str) {
            *self.0.borrow_mut() = text.to_owned();
        }
    }

    #[test]
    fn the_menu_and_tray_items_follow_the_update_state() {
        let items = [
            Item(RefCell::new(CHECK_LABEL.into())),
            Item(RefCell::new(CHECK_LABEL.into())),
        ];
        relabel(&items[..], Some("0.52.1"));
        for item in &items {
            assert_eq!(*item.0.borrow(), "Restart to Update (v0.52.1)");
        }
        relabel(&items[..], None);
        for item in &items {
            assert_eq!(*item.0.borrow(), "Check for Updates…");
        }
        // The tray shows these labels: they name no holder and no daemon.
        for label in [CHECK_LABEL.to_owned(), menu_label(Some("1.2.3"))] {
            assert!(!label.to_lowercase().contains("holder"));
            assert!(!label.to_lowercase().contains("daemon"));
        }
    }
}
