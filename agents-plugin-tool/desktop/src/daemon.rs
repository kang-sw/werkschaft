use std::{
    cmp::Ordering,
    io::{Read, Write},
    path::{Path, PathBuf},
    process::{Command, Stdio},
    sync::mpsc,
    thread,
    time::{Duration, Instant},
};

use serde_json::Value;

use crate::{
    daemon_package::{Candidate, DaemonPackageError, Staged},
    diagnostics::{Diagnostics, LkgState, STDERR_TAIL},
    endpoint::{self, Daemon},
    lkg::{self, Floor},
    python,
};

const PYTHON_REQUIRED: &str = "Python 3 is required";
const STAGING_FAILED: &str = "Could not prepare the replacement daemon. Retry later.";
const VERSION_UNKNOWN: &str =
    "Cannot determine the daemon version. Update the plugin before retrying.";
const NEWER_RUNNING: &str =
    "The running daemon is newer than this app. Update Werkschaft before retrying.";
const REPLACEMENT_FAILED: &str =
    "No healthy replacement was found. Keeping the current connection.";
const HEALTH_UNCERTAIN: &str = "Could not recheck the daemon after staging. Retry later.";
const PACKAGE_REJECTED: &str = "Werkschaft could not start the bundled daemon";
const NOT_HEALTHY: &str = "Dashboard did not become healthy. Retry to reconnect.";
const START_FAILED: &str = "Could not start the dashboard daemon. Retry to reconnect.";

#[derive(Clone, Copy, PartialEq, Eq)]
pub enum Intent {
    Connect,
    Recover,
    Replace,
}

#[derive(Debug)]
pub struct ConnectionError {
    pub reason: &'static str,
    pub python_required: bool,
    /// What the recovery page shows for a failed start.
    pub diagnostics: Option<Box<Diagnostics>>,
}

impl From<&'static str> for ConnectionError {
    fn from(reason: &'static str) -> Self {
        Self {
            reason,
            python_required: false,
            diagnostics: None,
        }
    }
}

pub struct ConnectionResult {
    pub daemon: Daemon,
    pub refusal: Option<&'static str>,
    /// Set when the bundled daemon failed and a last-known-good stage is
    /// serving instead; `lkg_version` names it.
    pub fallback: Option<Box<Diagnostics>>,
}

impl From<Daemon> for ConnectionResult {
    fn from(daemon: Daemon) -> Self {
        Self {
            daemon,
            refusal: None,
            fallback: None,
        }
    }
}

fn refuse(daemon: Daemon, reason: &'static str) -> ConnectionResult {
    ConnectionResult {
        daemon,
        refusal: Some(reason),
        fallback: None,
    }
}

pub(crate) fn version_core(version: &str) -> Option<[u64; 3]> {
    let version = version.strip_prefix('v').unwrap_or(version);
    let core = match version.split_once('-') {
        Some((core, suffix))
            if !suffix.is_empty()
                && suffix
                    .bytes()
                    .all(|byte| byte.is_ascii_alphanumeric() || b".-".contains(&byte)) =>
        {
            core
        }
        Some(_) => return None,
        None => version,
    };
    let mut parts = core.split('.');
    let mut numeric = [0; 3];
    for number in &mut numeric {
        let part = parts.next()?;
        if part.is_empty() || !part.bytes().all(|byte| byte.is_ascii_digit()) {
            return None;
        }
        *number = part.parse().ok()?;
        if *number > (1 << 53) - 1 {
            return None;
        }
    }
    parts.next().is_none().then_some(numeric)
}

fn version_text([major, minor, patch]: [u64; 3]) -> String {
    format!("{major}.{minor}.{patch}")
}

fn replacement(candidate: [u64; 3], running: &str, intent: Intent) -> Result<bool, &'static str> {
    match candidate.cmp(&version_core(running).ok_or(VERSION_UNKNOWN)?) {
        Ordering::Greater => Ok(true),
        Ordering::Equal => Ok(intent == Intent::Replace),
        Ordering::Less if intent == Intent::Replace => Err(NEWER_RUNNING),
        Ordering::Less => Ok(false),
    }
}

pub fn connect(
    resource_root: Result<PathBuf, &'static str>,
    user_data: Result<PathBuf, &'static str>,
    intent: Intent,
) -> Result<ConnectionResult, ConnectionError> {
    connect_with(
        &NativeLifecycle {
            cache: endpoint::active_cache_root()?,
            resource_root,
            user_data,
        },
        intent,
    )
}

/// One starter run.
#[derive(Debug, Default)]
pub struct Launch {
    /// A starter process existed (so a stage may be in use).
    pub spawned: bool,
    /// The starter reported success.
    pub started: bool,
    pub exit_code: Option<i32>,
    /// The tail of the starter's stderr, at most [`STDERR_TAIL`] bytes.
    pub stderr: Vec<u8>,
}

/// A launched stage's identity.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct StageRef {
    /// The `daemon-<rand>` directory name.
    pub name: String,
    pub version: [u64; 3],
    pub binary_sha256: String,
}

trait Lifecycle {
    type Package;
    /// A stage owns its copy: dropping it unlaunched removes the copy, and
    /// launch takes it for good before any process can reference it.
    type Staged;
    fn discover(&self) -> Option<Daemon>;
    fn select(&self) -> Result<(Self::Package, [u64; 3]), DaemonPackageError>;
    /// The version the bundled package declares, for a rejection's diagnostic.
    fn bundled_version(&self) -> Option<String>;
    fn stage(&self, package: &Self::Package) -> Result<Self::Staged, &'static str>;
    fn python_available(&self) -> bool;
    fn still_healthy(&self, daemon: &Daemon) -> bool;
    /// The stage's identity, read before launch takes it.
    fn stage_ref(&self, staged: &Self::Staged) -> Option<StageRef>;
    fn launch(&self, staged: Self::Staged, identity: &Value, replace: bool) -> Launch;
    fn settle(&self) -> Option<Daemon>;
    /// Whether `daemon` is `stage`'s own process: its version and the
    /// singleton's last-running-executable digest both match the stage.
    fn became(&self, daemon: &Daemon, stage: &StageRef) -> bool;
    /// Records a health-verified stage as last known good.
    fn record(&self, stage: &StageRef, daemon: &Daemon);
    fn floor(&self) -> Floor;
    /// The recorded last-known-good stages, newest first.
    fn recorded(&self) -> Vec<lkg::Entry>;
    /// Revalidates a recorded stage where it is, without copying it.
    fn reopen(&self, entry: &lkg::Entry) -> Result<Self::Package, DaemonPackageError>;
    /// Starts a revalidated stage where it is, as a start with no daemon.
    fn launch_kept(&self, package: &Self::Package) -> Launch;
    /// Removes stale stages, keeping the one just launched and the running
    /// daemon's.
    fn collect(&self, launched: Option<&str>, running: Option<&Daemon>);
}

struct NativeLifecycle {
    cache: PathBuf,
    resource_root: Result<PathBuf, &'static str>,
    user_data: Result<PathBuf, &'static str>,
}

impl NativeLifecycle {
    fn development() -> Option<PathBuf> {
        std::env::var_os("WERKSCHAFT_DAEMON_ROOT")
            .filter(|path| !path.is_empty())
            .map(PathBuf::from)
    }

    fn stages(&self) -> Option<PathBuf> {
        let stages = self.user_data.as_ref().ok()?.join("daemon-stages");
        // Stages are created under the canonical container.
        Some(std::fs::canonicalize(&stages).unwrap_or(stages))
    }

    fn dashboard(&self) -> PathBuf {
        self.cache.join("dashboard")
    }
}

impl Lifecycle for NativeLifecycle {
    type Package = Candidate;
    type Staged = Staged;

    fn discover(&self) -> Option<Daemon> {
        endpoint::discover_at(&self.cache).ok()
    }

    fn select(&self) -> Result<(Candidate, [u64; 3]), DaemonPackageError> {
        let root = self
            .resource_root
            .as_ref()
            .map_err(|error| DaemonPackageError::Unavailable(*error))?;
        let development = Self::development();
        let candidate = Candidate::select(&root.join("daemon"), development.as_deref())?;
        let core = candidate.version;
        Ok((candidate, core))
    }

    fn bundled_version(&self) -> Option<String> {
        let root = match Self::development() {
            Some(root) => root,
            None => self.resource_root.as_ref().ok()?.join("daemon"),
        };
        Candidate::declared_version(&root)
    }

    fn stage(&self, candidate: &Candidate) -> Result<Staged, &'static str> {
        let data = self.user_data.as_ref().map_err(|error| *error)?;
        let resources = self.resource_root.as_ref().map_err(|error| *error)?;
        candidate.stage(data, package_boundary(resources))
    }

    fn python_available(&self) -> bool {
        python::available()
    }

    fn still_healthy(&self, daemon: &Daemon) -> bool {
        daemon.is_healthy()
    }

    fn stage_ref(&self, staged: &Staged) -> Option<StageRef> {
        let candidate = staged.candidate();
        Some(StageRef {
            name: candidate.stage_name()?,
            version: candidate.version,
            binary_sha256: candidate.binary_sha256()?,
        })
    }

    fn launch(&self, staged: Staged, identity: &Value, replace: bool) -> Launch {
        launch(staged, &self.cache, identity, replace)
    }

    fn settle(&self) -> Option<Daemon> {
        let deadline = Instant::now() + Duration::from_secs(15);
        loop {
            let daemon = self.discover();
            if daemon.is_some() || Instant::now() >= deadline {
                return daemon;
            }
            thread::sleep(Duration::from_millis(200));
        }
    }

    fn became(&self, daemon: &Daemon, stage: &StageRef) -> bool {
        version_core(&daemon.version) == Some(stage.version)
            && lkg::running_sha(&self.dashboard())
                .is_some_and(|sha| sha.eq_ignore_ascii_case(&stage.binary_sha256))
    }

    fn record(&self, stage: &StageRef, daemon: &Daemon) {
        let Some(stages) = self.stages() else { return };
        let entry = lkg::Entry {
            stage: stage.name.clone(),
            version: daemon.version.clone(),
            binary_sha256: stage.binary_sha256.clone(),
            verified_at: lkg::now(),
        };
        match lkg::record(&stages, entry) {
            Ok(true) => crate::shell_log!(
                "Recorded stage {} (v{}) as last known good",
                stage.name,
                daemon.version
            ),
            Ok(false) => (),
            Err(error) => crate::shell_log!("Could not record the last known good stage: {error}"),
        }
    }

    fn floor(&self) -> Floor {
        Floor::read(&self.dashboard())
    }

    fn recorded(&self) -> Vec<lkg::Entry> {
        self.stages()
            .map(|stages| lkg::load(&stages))
            .unwrap_or_default()
    }

    fn reopen(&self, entry: &lkg::Entry) -> Result<Candidate, DaemonPackageError> {
        let stages = self
            .stages()
            .ok_or(DaemonPackageError::Unavailable("Could not locate user data"))?;
        Candidate::reopen(&stages.join(&entry.stage), &entry.binary_sha256)
    }

    fn launch_kept(&self, candidate: &Candidate) -> Launch {
        launch_candidate(candidate, &mut || (), &self.cache, &Value::Null, false)
    }

    fn collect(&self, launched: Option<&str>, running: Option<&Daemon>) {
        let Some(stages) = self.stages() else { return };
        let running_sha = lkg::running_sha(&self.dashboard());
        lkg::collect(
            &stages,
            &lkg::Protect {
                running_sha: running_sha.as_deref(),
                running_version: running.map(|daemon| daemon.version.as_str()),
                launched,
            },
        );
    }
}

fn connect_with(
    lifecycle: &impl Lifecycle,
    intent: Intent,
) -> Result<ConnectionResult, ConnectionError> {
    let running = lifecycle.discover();
    if intent == Intent::Recover {
        if let Some(daemon) = running {
            return Ok(daemon.into());
        }
    }
    if running.is_none() && !lifecycle.python_available() {
        return Err(ConnectionError {
            reason: PYTHON_REQUIRED,
            python_required: true,
            diagnostics: None,
        });
    }
    let (selected, core) = match (lifecycle.select(), running.as_ref()) {
        (Ok(candidate), _) => candidate,
        (Err(_), Some(daemon)) => {
            return Ok(if intent == Intent::Replace {
                refuse(
                    daemon.clone(),
                    "Cannot select the replacement daemon. Keeping the current connection.",
                )
            } else {
                daemon.clone().into()
            });
        }
        (Err(error), None) => {
            crate::shell_log!("The bundled daemon package was rejected: {error}");
            let mut diagnostics = Diagnostics::reason(PACKAGE_REJECTED);
            diagnostics.variant = Some(error.variant().to_owned());
            diagnostics.tree = error.tree().map(str::to_owned);
            diagnostics.path = error.path();
            diagnostics.detail = Some(error.to_string());
            diagnostics.bundled_version = lifecycle.bundled_version();
            return fall_back(lifecycle, PACKAGE_REJECTED, diagnostics, None);
        }
    };
    let identity = if let Some(daemon) = &running {
        match replacement(core, &daemon.version, intent) {
            Ok(false) => return Ok(daemon.clone().into()),
            Err(reason) => return Ok(refuse(daemon.clone(), reason)),
            Ok(true) => match daemon.predecessor() {
                Ok(identity) => identity,
                Err(_) => {
                    return Ok(refuse(
                        daemon.clone(),
                        "Cannot authenticate the replacement. Keeping the current connection.",
                    ));
                }
            },
        }
    } else {
        Value::Null
    };
    if let Some(daemon) = &running {
        if !lifecycle.python_available() {
            return Ok(refuse(
                daemon.clone(),
                "Python 3 is required to replace the daemon. Keeping the current connection.",
            ));
        }
    }
    let staged = match lifecycle.stage(&selected) {
        Ok(candidate) => candidate,
        Err(_) if running.is_some() => return Ok(refuse(running.unwrap(), STAGING_FAILED)),
        Err(reason) => {
            crate::shell_log!("Could not stage the bundled daemon: {reason}");
            let mut diagnostics = Diagnostics::reason(reason);
            diagnostics.bundled_version = Some(version_text(core));
            return fall_back(lifecycle, reason, diagnostics, None);
        }
    };
    let current = lifecycle.discover();
    if current != running {
        return match current {
            Some(daemon) => attach_winner(daemon, running.as_ref(), core),
            None => running
                .filter(|daemon| lifecycle.still_healthy(daemon))
                .map(|daemon| refuse(daemon, HEALTH_UNCERTAIN))
                .ok_or(HEALTH_UNCERTAIN.into()),
        };
    }
    let stage = lifecycle.stage_ref(&staged);
    let launch = lifecycle.launch(staged, &identity, intent == Intent::Replace);
    let settled = lifecycle.settle();
    // Only the stage whose own process became the daemon is last known good:
    // never the incumbent, and never a concurrent starter's daemon.
    if let (Some(daemon), Some(stage)) = (&settled, &stage) {
        if launch.spawned && Some(daemon) != running.as_ref() && lifecycle.became(daemon, stage) {
            lifecycle.record(stage, daemon);
        }
    }
    let launched = stage.as_ref().map(|stage| stage.name.as_str());
    let result = match settled {
        Some(daemon) if Some(&daemon) == running.as_ref() => Ok(refuse(daemon, REPLACEMENT_FAILED)),
        Some(daemon) => attach_winner(daemon, running.as_ref(), core),
        None if running.is_some() => {
            Err("No healthy daemon was found after replacement. Retry to reconnect.".into())
        }
        None => {
            let reason = if launch.started {
                NOT_HEALTHY
            } else {
                START_FAILED
            };
            crate::shell_log!(
                "The bundled daemon v{} did not start: {reason} (exit code {:?})",
                version_text(core),
                launch.exit_code
            );
            let mut diagnostics = Diagnostics::reason(reason);
            diagnostics.bundled_version = Some(version_text(core));
            diagnostics.starter(launch.exit_code, &launch.stderr);
            if let Some(tail) = &diagnostics.stderr_tail {
                crate::shell_log!("daemon-start stderr (tail):\n{tail}");
            }
            return fall_back(lifecycle, reason, diagnostics, launched);
        }
    };
    lifecycle.collect(launched, result.as_ref().ok().map(|answer| &answer.daemon));
    result
}

/// The bundled daemon could not start and none is running: start the newest
/// health-verified stage that `daemon-minor-floor` admits, revalidated where
/// it is. This runs only on the no-daemon path, so it never replaces or stops
/// a running daemon.
fn fall_back<L: Lifecycle>(
    lifecycle: &L,
    reason: &'static str,
    mut diagnostics: Diagnostics,
    launched: Option<&str>,
) -> Result<ConnectionResult, ConnectionError> {
    let floor = lifecycle.floor();
    diagnostics.minor_floor = floor.label();
    let recorded = lifecycle.recorded();
    let eligible = lkg::eligible(&recorded, floor);
    if recorded.is_empty() {
        diagnostics.lkg = LkgState::None;
        crate::shell_log!("No last known good stage is recorded");
    } else if eligible.is_empty() {
        // A cross-minor downgrade could corrupt state a newer minor already
        // migrated; an unreadable floor is treated the same way.
        let newest = lkg::eligible(&recorded, Floor::Absent)
            .first()
            .map(|entry| entry.version.clone());
        diagnostics.lkg = LkgState::Blocked;
        diagnostics.floor_blocked = true;
        crate::shell_log!(
            "The fallback to v{} is blocked by daemon-minor-floor ({})",
            newest.as_deref().unwrap_or("?"),
            floor.label().unwrap_or_default()
        );
        diagnostics.lkg_version = newest;
    } else {
        diagnostics.lkg = LkgState::Invalid;
        for entry in eligible {
            diagnostics.lkg_version = Some(entry.version.clone());
            let package = match lifecycle.reopen(entry) {
                Ok(package) => package,
                Err(error) => {
                    crate::shell_log!(
                        "Last known good stage {} (v{}) failed revalidation: {error}",
                        entry.stage,
                        entry.version
                    );
                    continue;
                }
            };
            crate::shell_log!(
                "Starting last known good stage {} (v{})",
                entry.stage,
                entry.version
            );
            let launch = lifecycle.launch_kept(&package);
            match lifecycle.settle() {
                Some(daemon) => {
                    lifecycle.collect(launched, Some(&daemon));
                    let fallback = version_core(&daemon.version) == version_core(&entry.version);
                    diagnostics.lkg = LkgState::Running;
                    return Ok(ConnectionResult {
                        daemon,
                        refusal: None,
                        fallback: fallback.then(|| Box::new(diagnostics)),
                    });
                }
                None => {
                    diagnostics.lkg = LkgState::Failed;
                    crate::shell_log!(
                        "Last known good v{} did not become healthy (exit code {:?})",
                        entry.version,
                        launch.exit_code
                    );
                    break;
                }
            }
        }
    }
    lifecycle.collect(launched, None);
    Err(ConnectionError {
        reason,
        python_required: false,
        diagnostics: Some(Box::new(diagnostics)),
    })
}

fn package_boundary(resource_root: &Path) -> &Path {
    if cfg!(target_os = "macos") && resource_root.ends_with("Contents/Resources") {
        if let Some(app) = resource_root.ancestors().nth(2) {
            if app.extension().is_some_and(|extension| extension == "app") {
                return app;
            }
        }
    }
    resource_root
}

fn attach_winner(
    daemon: Daemon,
    running: Option<&Daemon>,
    candidate: [u64; 3],
) -> Result<ConnectionResult, ConnectionError> {
    let Some(predecessor) = running else {
        return Ok(daemon.into());
    };
    if let Some(core) = version_core(&daemon.version) {
        if core >= candidate {
            return Ok(daemon.into());
        }
        if version_core(&predecessor.version).is_some_and(|previous| core >= previous) {
            return Ok(refuse(
                daemon,
                "Another daemon is serving an older core than this app. Attaching to it.",
            ));
        }
    }
    Err("An older or unidentified daemon won the handoff. Retry to reconnect.".into())
}

// The stage is kept once a starter process exists, even if that start later
// fails: the process may already run from it. A refused spawn drops it.
fn launch(staged: Staged, cache: &Path, identity: &Value, replace: bool) -> Launch {
    let candidate = staged.candidate().clone();
    let mut stage = Some(staged);
    let mut spawned = || {
        if let Some(staged) = stage.take() {
            staged.keep();
        }
    };
    launch_candidate(&candidate, &mut spawned, cache, identity, replace)
}

fn launch_candidate(
    candidate: &Candidate,
    spawned: &mut impl FnMut(),
    cache: &Path,
    identity: &Value,
    replace: bool,
) -> Launch {
    let mut command = Command::new(&candidate.binary);
    command.current_dir(&candidate.root);
    for key in [
        "WS_HOSTED_SESSION",
        "WS_HOSTED_SESSION_KEY",
        "WS_HOSTED_SESSION_ROW",
        "WS_MCP_HARNESS_PID",
        "WS_MCP_PROJECT_ROOT",
        "WS_MCP_DASHBOARD_DEV_SOURCE",
        "WS_RSRC_DEV_CHECKOUT",
    ] {
        command.env_remove(key);
    }
    let mut manifests = std::ffi::OsString::new();
    for path in &candidate.config_manifests {
        if !manifests.is_empty() {
            manifests.push(";");
        }
        manifests.push(path);
    }
    command
        .env("WS_CACHE_HOME", cache)
        .env("WS_RSRC_ROOT", candidate.root.join("rsrc"))
        .env("WS_SKILLS_ROOT", candidate.root.join("skills"))
        .env("WS_MCP_CONFIG_MANIFESTS", manifests)
        .env("WS_MCP_RUNTIME_BINARY", &candidate.binary)
        .env("WS_MCP_RUNTIME_DIR", &candidate.root)
        .env("CLAUDE_PLUGIN_ROOT", &candidate.root)
        .env("WS_MCP_DAEMON_JOB_LIFETIME", "0")
        .args(["daemon-start", "--desktop-start", "--start-wait", "13s"])
        .stdin(Stdio::piped())
        .stdout(Stdio::null())
        // Captured on every platform for the recovery diagnostics. The
        // daemon itself does not inherit it, so the pipe ends with the starter.
        .stderr(Stdio::piped());
    if replace && !identity.is_null() {
        command.arg("--replace");
    }
    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt;
        const CREATE_NO_WINDOW: u32 = 0x0800_0000;
        command.creation_flags(CREATE_NO_WINDOW);
        command.arg("--require-independent-lifetime");
    }
    let input = identity.to_string();
    let first = run_starter(&mut command, input.as_bytes(), spawned);
    if cfg!(windows) && !first.started && breakaway_denied(&first.stderr) {
        crate::shell_log!("WS_DAEMON_BREAKAWAY_DENIED=5. Retrying within the containing job.");
        command.env("WS_MCP_DAEMON_JOB_LIFETIME", "1");
        let retry = run_starter(&mut command, input.as_bytes(), spawned);
        return Launch {
            spawned: first.spawned || retry.spawned,
            ..retry
        };
    }
    first
}

fn breakaway_denied(stderr: &[u8]) -> bool {
    stderr
        .split(|byte| *byte == b'\n')
        .any(|line| line.strip_suffix(b"\r").unwrap_or(line) == b"WS_DAEMON_BREAKAWAY_DENIED=5")
}

/// Reads a pipe to its end, keeping the last [`STDERR_TAIL`] bytes: a starter
/// that writes more never blocks on, or loses, its pipe.
fn read_tail(mut input: impl Read) -> Vec<u8> {
    let mut tail = Vec::new();
    let mut buffer = [0; 4096];
    loop {
        match input.read(&mut buffer) {
            Ok(0) | Err(_) => break,
            Ok(count) => {
                tail.extend_from_slice(&buffer[..count]);
                if tail.len() > 2 * STDERR_TAIL {
                    tail.drain(..tail.len() - STDERR_TAIL);
                }
            }
        }
    }
    if tail.len() > STDERR_TAIL {
        tail.drain(..tail.len() - STDERR_TAIL);
    }
    tail
}

fn run_starter(command: &mut Command, input: &[u8], spawned: &mut impl FnMut()) -> Launch {
    // The native starter's own budget is 2*DefaultStartWait + 13 s.
    run_starter_with_timeout(command, input, spawned, Duration::from_secs(42))
}

fn run_starter_with_timeout(
    command: &mut Command,
    input: &[u8],
    spawned: &mut impl FnMut(),
    timeout: Duration,
) -> Launch {
    let Ok(mut child) = command.spawn() else {
        return Launch::default();
    };
    spawned();
    let output = child.stderr.take().map(|stderr| {
        let (send, receive) = mpsc::channel();
        thread::spawn(move || {
            let _ = send.send(read_tail(stderr));
        });
        receive
    });
    let wrote_identity = child
        .stdin
        .take()
        .is_some_and(|mut stdin| stdin.write_all(input).is_ok());
    let drain_stderr = |rx: Option<mpsc::Receiver<Vec<u8>>>| {
        rx.and_then(|rx| rx.recv_timeout(Duration::from_millis(500)).ok())
            .unwrap_or_default()
    };
    // If writing stdin failed (e.g. BrokenPipe from an immediate crash on launch),
    // still poll briefly so we capture the real exit status before falling back to kill.
    let wait_budget = if wrote_identity {
        timeout
    } else {
        timeout.min(Duration::from_millis(500))
    };
    let deadline = Instant::now() + wait_budget;
    loop {
        match child.try_wait() {
            Ok(Some(status)) => {
                return Launch {
                    spawned: true,
                    started: wrote_identity && status.success(),
                    exit_code: status.code(),
                    stderr: drain_stderr(output),
                };
            }
            Ok(None) => {
                let Some(remaining) = deadline.checked_duration_since(Instant::now()) else {
                    break;
                };
                if remaining.is_zero() {
                    break;
                }
                thread::sleep(remaining.min(Duration::from_millis(50)));
            }
            Err(_) => break,
        }
    }
    let _ = child.kill();
    let exit_code = child.wait().ok().and_then(|status| status.code());
    Launch {
        spawned: true,
        started: false,
        exit_code,
        stderr: drain_stderr(output),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::{
        cell::{Cell, RefCell},
        collections::VecDeque,
        rc::Rc,
    };

    // Counts a drop that no launch took.
    struct Stage(Option<Rc<Cell<u32>>>);

    impl Drop for Stage {
        fn drop(&mut self) {
            if let Some(discarded) = &self.0 {
                discarded.set(discarded.get() + 1);
            }
        }
    }

    const NEW_STAGE: &str = "daemon-new";

    struct Fixture {
        observations: RefCell<VecDeque<Option<Daemon>>>,
        settles: RefCell<VecDeque<Option<Daemon>>>,
        python: bool,
        healthy: bool,
        select_error: Option<DaemonPackageError>,
        stage_fails: bool,
        spawns: bool,
        start_succeeds: bool,
        running_sha: String,
        floor: Floor,
        recorded: Vec<lkg::Entry>,
        invalid_stages: Vec<&'static str>,
        actions: RefCell<Vec<&'static str>>,
        start: RefCell<Option<(Value, bool)>>,
        discarded: Rc<Cell<u32>>,
        kept_launches: RefCell<Vec<String>>,
        records: RefCell<Vec<(String, String)>>,
        collections: RefCell<Vec<(Option<String>, Option<String>)>>,
    }

    impl Fixture {
        fn with(
            before: Option<Daemon>,
            after_stage: Option<Daemon>,
            settled: Option<Daemon>,
        ) -> Self {
            Self {
                observations: RefCell::new([before, after_stage].into()),
                settles: RefCell::new([settled].into()),
                python: true,
                healthy: true,
                select_error: None,
                stage_fails: false,
                spawns: true,
                start_succeeds: true,
                running_sha: sha('n'),
                floor: Floor::Absent,
                recorded: Vec::new(),
                invalid_stages: Vec::new(),
                actions: RefCell::default(),
                start: RefCell::default(),
                discarded: Rc::default(),
                kept_launches: RefCell::default(),
                records: RefCell::default(),
                collections: RefCell::default(),
            }
        }

        /// No daemon runs: the bundled start is the only attempt before the
        /// fallback, whose settles follow.
        fn alone(settles: impl IntoIterator<Item = Option<Daemon>>) -> Self {
            let mut fixture = Self::with(None, None, None);
            *fixture.settles.get_mut() = settles.into_iter().collect();
            fixture
        }

        fn lkg_launches(&self) -> Vec<String> {
            self.kept_launches.borrow().clone()
        }
    }

    fn sha(digit: char) -> String {
        digit.to_string().repeat(64)
    }

    fn entry(stage: &str, version: &str) -> lkg::Entry {
        lkg::Entry {
            stage: stage.into(),
            version: version.into(),
            binary_sha256: sha('a'),
            verified_at: 1,
        }
    }

    fn residual_file() -> DaemonPackageError {
        DaemonPackageError::UnexpectedFile {
            tree: "rsrc".into(),
            path: "rsrc/x/.pi.md".into(),
        }
    }

    impl Lifecycle for Fixture {
        type Package = String;
        type Staged = Stage;

        fn discover(&self) -> Option<Daemon> {
            self.observations
                .borrow_mut()
                .pop_front()
                .expect("unexpected discovery")
        }
        fn select(&self) -> Result<(String, [u64; 3]), DaemonPackageError> {
            self.actions.borrow_mut().push("select");
            match &self.select_error {
                Some(error) => Err(error.clone()),
                None => Ok(("bundled".into(), [0, 50, 8])),
            }
        }
        fn bundled_version(&self) -> Option<String> {
            Some("0.50.8".into())
        }
        fn stage(&self, _: &String) -> Result<Stage, &'static str> {
            self.actions.borrow_mut().push("stage");
            if self.stage_fails {
                Err("staging failed")
            } else {
                Ok(Stage(Some(self.discarded.clone())))
            }
        }
        fn python_available(&self) -> bool {
            self.actions.borrow_mut().push("python");
            self.python
        }
        fn still_healthy(&self, _: &Daemon) -> bool {
            self.actions.borrow_mut().push("health");
            self.healthy
        }
        fn stage_ref(&self, _: &Stage) -> Option<StageRef> {
            Some(StageRef {
                name: NEW_STAGE.into(),
                version: [0, 50, 8],
                binary_sha256: sha('n'),
            })
        }
        fn launch(&self, mut staged: Stage, identity: &Value, replace: bool) -> Launch {
            staged.0 = None;
            self.actions.borrow_mut().push("launch");
            *self.start.borrow_mut() = Some((identity.clone(), replace));
            Launch {
                spawned: self.spawns,
                started: self.start_succeeds,
                exit_code: Some(if self.start_succeeds { 0 } else { 78 }),
                stderr: b"daemon-start: token=0123 refused\n".to_vec(),
            }
        }
        fn settle(&self) -> Option<Daemon> {
            self.actions.borrow_mut().push("settle");
            self.settles.borrow_mut().pop_front().flatten()
        }
        fn became(&self, daemon: &Daemon, stage: &StageRef) -> bool {
            version_core(&daemon.version) == Some(stage.version)
                && self.running_sha == stage.binary_sha256
        }
        fn record(&self, stage: &StageRef, daemon: &Daemon) {
            self.records
                .borrow_mut()
                .push((stage.name.clone(), daemon.version.clone()));
        }
        fn floor(&self) -> Floor {
            self.actions.borrow_mut().push("floor");
            self.floor
        }
        fn recorded(&self) -> Vec<lkg::Entry> {
            self.recorded.clone()
        }
        fn reopen(&self, entry: &lkg::Entry) -> Result<String, DaemonPackageError> {
            self.actions.borrow_mut().push("reopen");
            if self.invalid_stages.contains(&entry.stage.as_str()) {
                Err(DaemonPackageError::HashMismatch {
                    path: "ws-mcp".into(),
                })
            } else {
                Ok(entry.stage.clone())
            }
        }
        fn launch_kept(&self, package: &String) -> Launch {
            self.actions.borrow_mut().push("lkg-launch");
            self.kept_launches.borrow_mut().push(package.clone());
            Launch {
                spawned: true,
                started: true,
                ..Launch::default()
            }
        }
        fn collect(&self, launched: Option<&str>, running: Option<&Daemon>) {
            self.collections.borrow_mut().push((
                launched.map(str::to_owned),
                running.map(|daemon| daemon.version.clone()),
            ));
        }
    }

    #[test]
    fn missing_start_and_raced_attachment_require_no_drain() {
        let winner = Daemon::fixture(31, "0.50.8");
        let lifecycle = Fixture::with(None, None, Some(winner.clone()));
        let answer = connect_with(&lifecycle, Intent::Connect).unwrap();
        assert!(answer.daemon == winner && answer.refusal.is_none() && answer.fallback.is_none());
        assert_eq!(*lifecycle.start.borrow(), Some((Value::Null, false)));
        assert_eq!(
            *lifecycle.actions.borrow(),
            ["python", "select", "stage", "launch", "settle"]
        );
        // The launched stage became the daemon: it is recorded, and kept.
        assert_eq!(
            *lifecycle.records.borrow(),
            [(NEW_STAGE.to_owned(), "0.50.8".to_owned())]
        );
        assert_eq!(
            *lifecycle.collections.borrow(),
            [(Some(NEW_STAGE.to_owned()), Some("0.50.8".to_owned()))]
        );

        let raced = Fixture::with(None, Some(winner.clone()), None);
        let answer = connect_with(&raced, Intent::Connect).unwrap();
        assert!(answer.daemon == winner && answer.refusal.is_none());
        assert!(raced.start.borrow().is_none());
        // The stage the raced start never launched is removed.
        assert_eq!(lifecycle.discarded.get(), 0);
        assert_eq!(raced.discarded.get(), 1);
        // An attach-only start records nothing and collects nothing.
        assert!(raced.records.borrow().is_empty());
        assert!(raced.collections.borrow().is_empty());
    }

    #[test]
    fn automatic_minor_and_explicit_same_core_replacement_use_the_observed_identity() {
        let successor = Daemon::fixture(31, "0.50.8");
        for (running, intent, replace) in [
            ("0.49.7", Intent::Connect, false),
            ("0.50.8-dev", Intent::Replace, true),
        ] {
            let prior = Daemon::fixture(12, running);
            let lifecycle =
                Fixture::with(Some(prior.clone()), Some(prior), Some(successor.clone()));
            let answer = connect_with(&lifecycle, intent).unwrap();
            assert!(answer.daemon == successor && answer.refusal.is_none());
            let data = lifecycle.start.borrow();
            let (identity, explicit) = data.as_ref().unwrap();
            assert_eq!(identity["pid"], 12);
            assert_eq!(identity["version"], running);
            assert!(identity.get("token").is_none());
            assert_eq!(*explicit, replace);
            assert_eq!(lifecycle.records.borrow().len(), 1);
        }
    }

    #[test]
    fn newer_daemon_and_recovery_attach_without_staging() {
        for (version, intent, refusal, actions) in [
            ("0.51.0", Intent::Connect, None, &["select"][..]),
            (
                "0.51.0",
                Intent::Replace,
                Some(NEWER_RUNNING),
                &["select"][..],
            ),
            ("0.49.7", Intent::Recover, None, &[][..]),
        ] {
            let prior = Daemon::fixture(12, version);
            let lifecycle = Fixture::with(Some(prior.clone()), None, None);
            let answer = connect_with(&lifecycle, intent).unwrap();
            assert!(answer.daemon == prior);
            assert_eq!(answer.refusal, refusal);
            assert_eq!(lifecycle.actions.borrow().as_slice(), actions);
        }
    }

    #[test]
    fn staging_refusal_uncertain_endpoint_and_failed_start_keep_the_incumbent() {
        let prior = Daemon::fixture(12, "0.50.8");
        let mut failed_stage = Fixture::with(Some(prior.clone()), None, None);
        failed_stage.stage_fails = true;
        let uncertain = Fixture::with(Some(prior.clone()), None, None);
        let mut failed_start = Fixture::with(
            Some(prior.clone()),
            Some(prior.clone()),
            Some(prior.clone()),
        );
        failed_start.start_succeeds = false;
        for (fixture, reason, launched) in [
            (failed_stage, STAGING_FAILED, false),
            (uncertain, HEALTH_UNCERTAIN, false),
            (failed_start, REPLACEMENT_FAILED, true),
        ] {
            let answer = connect_with(&fixture, Intent::Replace).unwrap();
            assert!(answer.daemon == prior);
            assert_eq!(answer.refusal, Some(reason));
            assert_eq!(fixture.start.borrow().is_some(), launched);
            // A started process may use its stage even when the start failed.
            let staged = reason != STAGING_FAILED;
            assert_eq!(fixture.discarded.get(), u32::from(staged && !launched));
        }
    }

    // With a daemon running, every bundled failure keeps today's outcome:
    // the fallback never starts a stage beside, or instead of, a daemon.
    #[test]
    fn a_running_daemon_is_never_joined_or_replaced_by_a_fallback() {
        let prior = Daemon::fixture(12, "0.49.7");
        let mut rejected = Fixture::with(Some(prior.clone()), None, None);
        rejected.select_error = Some(residual_file());
        let mut failed_stage = Fixture::with(Some(prior.clone()), None, None);
        failed_stage.stage_fails = true;
        let failed_start = Fixture::with(Some(prior.clone()), Some(prior.clone()), None);
        let incumbent = Fixture::with(
            Some(prior.clone()),
            Some(prior.clone()),
            Some(prior.clone()),
        );
        // Automatic replacement of an older daemon takes the same paths.
        let mut connect_rejected = Fixture::with(Some(prior.clone()), None, None);
        connect_rejected.select_error = Some(residual_file());
        let mut connect_failed_stage = Fixture::with(Some(prior.clone()), None, None);
        connect_failed_stage.stage_fails = true;
        let connect_failed_start = Fixture::with(Some(prior.clone()), Some(prior.clone()), None);
        for (name, mut fixture, intent) in [
            ("rejected", rejected, Intent::Replace),
            ("failed stage", failed_stage, Intent::Replace),
            ("failed start", failed_start, Intent::Replace),
            ("incumbent survives", incumbent, Intent::Replace),
            ("connect rejected", connect_rejected, Intent::Connect),
            ("connect failed stage", connect_failed_stage, Intent::Connect),
            ("connect failed start", connect_failed_start, Intent::Connect),
        ] {
            fixture.recorded = vec![entry("daemon-lkg", "0.50.7")];
            let _ = connect_with(&fixture, intent);
            let actions = fixture.actions.borrow();
            assert!(
                !actions.contains(&"floor") && !actions.contains(&"lkg-launch"),
                "{name}: {actions:?}"
            );
            // The incumbent answering the settle is not the new stage's.
            assert!(fixture.records.borrow().is_empty(), "{name}");
        }
    }

    #[test]
    fn a_concurrent_starters_daemon_is_not_recorded_as_last_known_good() {
        // A different version won the start.
        let other = Daemon::fixture(40, "0.51.0");
        let lifecycle = Fixture::alone([Some(other.clone())]);
        let answer = connect_with(&lifecycle, Intent::Connect).unwrap();
        assert!(answer.daemon == other);
        assert!(lifecycle.records.borrow().is_empty());
        // The same version, but the singleton runs another binary.
        let mut lifecycle = Fixture::alone([Some(Daemon::fixture(41, "0.50.8"))]);
        lifecycle.running_sha = sha('o');
        connect_with(&lifecycle, Intent::Connect).unwrap();
        assert!(lifecycle.records.borrow().is_empty());
        // A matching daemon that no process of this start could have become.
        let mut lifecycle = Fixture::alone([Some(Daemon::fixture(42, "0.50.8"))]);
        lifecycle.spawns = false;
        connect_with(&lifecycle, Intent::Connect).unwrap();
        assert!(lifecycle.records.borrow().is_empty());
    }

    #[test]
    fn a_residual_file_falls_back_to_the_last_known_good_stage_in_place() {
        let lkg = Daemon::fixture(50, "0.50.7");
        let mut lifecycle = Fixture::alone([Some(lkg.clone())]);
        lifecycle.select_error = Some(residual_file());
        lifecycle.recorded = vec![entry("daemon-lkg", "0.50.7")];
        let answer = connect_with(&lifecycle, Intent::Connect).unwrap();
        assert!(answer.daemon == lkg && answer.refusal.is_none());
        let fallback = answer.fallback.expect("fallback notice");
        assert_eq!(fallback.variant.as_deref(), Some("UnexpectedFile"));
        assert_eq!(fallback.tree.as_deref(), Some("rsrc"));
        assert_eq!(fallback.path.as_deref(), Some("rsrc/x/.pi.md"));
        assert_eq!(fallback.bundled_version.as_deref(), Some("0.50.8"));
        assert_eq!(fallback.lkg, LkgState::Running);
        assert_eq!(fallback.lkg_version.as_deref(), Some("0.50.7"));
        // Revalidated and launched where it is: no stage, no copy.
        assert_eq!(
            *lifecycle.actions.borrow(),
            ["python", "select", "floor", "reopen", "lkg-launch", "settle"]
        );
        assert_eq!(lifecycle.lkg_launches(), ["daemon-lkg"]);
        assert!(lifecycle.records.borrow().is_empty());
    }

    #[test]
    fn a_failed_bundled_start_falls_back_and_collects_around_the_pending_stage() {
        let lkg = Daemon::fixture(50, "0.50.7");
        let mut lifecycle = Fixture::alone([None, Some(lkg)]);
        lifecycle.start_succeeds = false;
        lifecycle.recorded = vec![entry("daemon-a", "0.49.0"), entry("daemon-b", "0.50.7")];
        let answer = connect_with(&lifecycle, Intent::Connect).unwrap();
        let fallback = answer.fallback.unwrap();
        assert_eq!(fallback.reason, START_FAILED);
        assert_eq!(fallback.exit_code, Some(78));
        // The starter's stderr is kept, redacted.
        let tail = fallback.stderr_tail.unwrap();
        assert!(tail.contains("refused") && !tail.contains("0123"), "{tail}");
        assert_eq!(lifecycle.lkg_launches(), ["daemon-b"]);
        // The failed stage may still come up late: collection keeps it.
        assert_eq!(
            *lifecycle.collections.borrow(),
            [(Some(NEW_STAGE.to_owned()), Some("0.50.7".to_owned()))]
        );

        // The newest stage fails revalidation; the next eligible one is tried.
        let mut lifecycle = Fixture::alone([None, Some(Daemon::fixture(51, "0.50.1"))]);
        lifecycle.recorded = vec![entry("daemon-b", "0.50.7"), entry("daemon-a", "0.50.1")];
        lifecycle.invalid_stages = vec!["daemon-b"];
        let answer = connect_with(&lifecycle, Intent::Connect).unwrap();
        assert_eq!(answer.fallback.unwrap().reason, NOT_HEALTHY);
        assert_eq!(lifecycle.lkg_launches(), ["daemon-a"]);
    }

    #[test]
    fn the_minor_floor_blocks_a_cross_minor_fallback_and_fails_closed() {
        for floor in [Floor::At([0, 51]), Floor::Unreadable] {
            let mut lifecycle = Fixture::alone([]);
            lifecycle.select_error = Some(residual_file());
            lifecycle.floor = floor;
            lifecycle.recorded = vec![entry("daemon-lkg", "0.50.7")];
            let error = connect_with(&lifecycle, Intent::Connect).err().unwrap();
            assert_eq!(error.reason, PACKAGE_REJECTED);
            let diagnostics = error.diagnostics.unwrap();
            assert!(diagnostics.floor_blocked);
            assert_eq!(diagnostics.lkg, LkgState::Blocked);
            assert_eq!(diagnostics.lkg_version.as_deref(), Some("0.50.7"));
            assert_eq!(diagnostics.minor_floor, floor.label());
            assert!(lifecycle.lkg_launches().is_empty());
            assert!(!lifecycle.actions.borrow().contains(&"reopen"));
        }
        // A stage at the floor's minor is admitted.
        let mut lifecycle = Fixture::alone([Some(Daemon::fixture(50, "0.51.2"))]);
        lifecycle.select_error = Some(residual_file());
        lifecycle.floor = Floor::At([0, 51]);
        lifecycle.recorded = vec![entry("daemon-lkg", "0.51.2")];
        assert!(connect_with(&lifecycle, Intent::Connect)
            .unwrap()
            .fallback
            .is_some());
    }

    #[test]
    fn without_a_usable_stage_the_failure_reaches_the_recovery_view() {
        // A fresh upgrade has no index yet.
        let mut fresh = Fixture::alone([]);
        fresh.select_error = Some(residual_file());
        let error = connect_with(&fresh, Intent::Connect).err().unwrap();
        let diagnostics = error.diagnostics.unwrap();
        assert_eq!(diagnostics.lkg, LkgState::None);
        assert_eq!(diagnostics.variant.as_deref(), Some("UnexpectedFile"));
        assert!(!diagnostics.floor_blocked);
        assert_eq!(*fresh.collections.borrow(), [(None, None)]);

        let mut invalid = Fixture::alone([None]);
        invalid.recorded = vec![entry("daemon-lkg", "0.50.7")];
        invalid.invalid_stages = vec!["daemon-lkg"];
        let error = connect_with(&invalid, Intent::Connect).err().unwrap();
        assert_eq!(error.diagnostics.unwrap().lkg, LkgState::Invalid);

        let mut failed = Fixture::alone([None, None]);
        failed.stage_fails = true;
        failed.recorded = vec![entry("daemon-lkg", "0.50.7")];
        let error = connect_with(&failed, Intent::Connect).err().unwrap();
        assert_eq!(error.reason, "staging failed");
        let diagnostics = error.diagnostics.unwrap();
        assert_eq!(diagnostics.lkg, LkgState::Failed);
        assert_eq!(diagnostics.bundled_version.as_deref(), Some("0.50.8"));
        assert_eq!(failed.lkg_launches(), ["daemon-lkg"]);
    }

    #[test]
    fn missing_python_prevents_staging_and_start() {
        let mut missing = Fixture::with(None, None, None);
        missing.python = false;
        missing.recorded = vec![entry("daemon-lkg", "0.50.7")];
        let error = connect_with(&missing, Intent::Connect).err().unwrap();
        assert!(error.python_required);
        assert_eq!(*missing.actions.borrow(), ["python"]);

        let prior = Daemon::fixture(12, "0.49.7");
        let mut replacing = Fixture::with(Some(prior.clone()), None, None);
        replacing.python = false;
        let answer = connect_with(&replacing, Intent::Connect).unwrap();
        assert!(answer.daemon == prior && answer.refusal.unwrap().contains("Python 3"));
        assert_eq!(*replacing.actions.borrow(), ["select", "python"]);
    }

    #[test]
    fn numeric_core_controls_automatic_and_explicit_replacement() {
        let candidate = [0, 50, 8];
        for (running, intent, want) in [
            ("0.49.99", Intent::Connect, Ok(true)),
            ("0.50.7", Intent::Connect, Ok(true)),
            ("v0.50.8-canary.1", Intent::Connect, Ok(false)),
            ("0.50.8-dev", Intent::Replace, Ok(true)),
            ("0.51.0", Intent::Connect, Ok(false)),
            ("1.0.0", Intent::Replace, Err(NEWER_RUNNING)),
            ("garbage", Intent::Connect, Err(VERSION_UNKNOWN)),
        ] {
            assert_eq!(replacement(candidate, running, intent), want, "{running}");
        }
        for invalid in [
            "0.50",
            "0.50.8-",
            "0.50.+8",
            "0.50.8/extra",
            "0.50.9007199254740992",
        ] {
            assert!(version_core(invalid).is_none(), "{invalid}");
        }
    }

    // The production half of the recording rule: the digest comes from the
    // singleton's last-running-executable.json under <cache>/dashboard.
    #[test]
    fn the_native_lifecycle_records_only_its_own_stage_and_collects_around_it() {
        use std::fs;
        let temp = tempfile::tempdir().unwrap();
        let cache = temp.path().join("cache");
        let dashboard = cache.join("dashboard");
        let user = temp.path().join("user");
        let stages = user.join("daemon-stages");
        fs::create_dir_all(&dashboard).unwrap();
        fs::create_dir_all(&stages).unwrap();
        let native = NativeLifecycle {
            cache,
            resource_root: Err("no resources"),
            user_data: Ok(user),
        };
        let running = |digest: &str| {
            let record = serde_json::json!({"schema_version": 1, "executable": "ws-mcp",
                "version": "0.52.1", "binary_sha256": digest, "build_time": 0});
            fs::write(
                dashboard.join("last-running-executable.json"),
                record.to_string(),
            )
            .unwrap();
        };
        let stage = StageRef {
            name: "daemon-new".into(),
            version: [0, 52, 1],
            binary_sha256: sha('a'),
        };
        let daemon = Daemon::fixture(7, "0.52.1-dev");
        // No record of the running binary: never this stage's process.
        assert!(!native.became(&daemon, &stage));
        running(&sha('a').to_ascii_uppercase());
        assert!(native.became(&daemon, &stage));
        assert!(!native.became(&Daemon::fixture(7, "0.52.2"), &stage));
        running(&sha('b'));
        assert!(!native.became(&daemon, &stage));

        native.record(&stage, &daemon);
        let recorded = native.recorded();
        assert_eq!(recorded.len(), 1);
        assert_eq!(
            (recorded[0].stage.as_str(), recorded[0].version.as_str()),
            ("daemon-new", "0.52.1-dev")
        );
        assert_eq!(recorded[0].binary_sha256, sha('a'));

        let metadata = if cfg!(windows) {
            "ws-mcp.exe.metadata.json"
        } else {
            "ws-mcp.metadata.json"
        };
        for (name, version, digest) in [
            ("daemon-new", "0.52.1", 'a'),
            ("daemon-running", "0.40.0", 'c'),
            ("daemon-old", "0.30.0", 'd'),
        ] {
            fs::create_dir_all(stages.join(name).join("rsrc")).unwrap();
            let value = serde_json::json!({"version": version, "binary_sha256": sha(digest)});
            fs::write(stages.join(name).join(metadata), value.to_string()).unwrap();
        }
        running(&sha('c'));
        native.collect(Some("daemon-new"), Some(&Daemon::fixture(9, "0.41.0")));
        assert!(stages.join("daemon-new").is_dir());
        assert!(stages.join("daemon-running").is_dir());
        assert!(!stages.join("daemon-old").exists());
    }

    #[test]
    fn the_stderr_capture_keeps_only_the_tail() {
        let mut bytes = vec![b'a'; 3 * STDERR_TAIL];
        bytes.extend_from_slice(b"last line\n");
        let tail = read_tail(&bytes[..]);
        assert_eq!(tail.len(), STDERR_TAIL);
        assert!(tail.ends_with(b"last line\n"));
        assert_eq!(read_tail(&b"short"[..]), b"short");
    }

    #[cfg(unix)]
    #[test]
    fn only_a_spawned_starter_takes_its_stage() {
        let mut taken = 0;
        let refused = run_starter(&mut Command::new("/nonexistent/ws-mcp"), b"{}", &mut || {
            taken += 1
        });
        assert!(!refused.spawned && !refused.started);
        assert_eq!(taken, 0);
        let mut command = Command::new("sh");
        command
            .args(["-c", "cat >/dev/null; echo starter-said >&2; exit 3"])
            .stdin(Stdio::piped())
            .stdout(Stdio::null())
            .stderr(Stdio::piped());
        let launch = run_starter(&mut command, b"{}", &mut || taken += 1);
        assert!(launch.spawned && !launch.started);
        assert_eq!(launch.exit_code, Some(3));
        assert_eq!(launch.stderr, b"starter-said\n");
        assert_eq!(taken, 1);
    }

    #[cfg(unix)]
    #[test]
    fn immediate_crash_before_reading_stdin_captures_exit_code_and_stderr() {
        let mut command = Command::new("sh");
        command
            .args(["-c", "echo crash-on-launch >&2; exit 42"])
            .stdin(Stdio::piped())
            .stdout(Stdio::null())
            .stderr(Stdio::piped());
        // Larger than the OS pipe buffer so write_all fails with BrokenPipe when
        // the child exits immediately without reading stdin.
        let oversized_input = vec![b'x'; 256 * 1024];
        let launch = run_starter_with_timeout(
            &mut command,
            &oversized_input,
            &mut || {},
            Duration::from_secs(5),
        );
        assert!(launch.spawned && !launch.started);
        assert_eq!(launch.exit_code, Some(42));
        assert_eq!(launch.stderr, b"crash-on-launch\n");
    }

    #[cfg(unix)]
    #[test]
    fn starter_timeout_kills_child_and_still_captures_stderr() {
        let mut command = Command::new("sh");
        // Use `exec` so killing the child closes stderr directly with no grandchild holding it open.
        command
            .args(["-c", "echo hung-before-timeout >&2; exec sleep 10"])
            .stdin(Stdio::piped())
            .stdout(Stdio::null())
            .stderr(Stdio::piped());
        let launch = run_starter_with_timeout(
            &mut command,
            b"{}",
            &mut || {},
            Duration::from_millis(200),
        );
        assert!(launch.spawned && !launch.started);
        assert_eq!(launch.exit_code, None);
        assert_eq!(launch.stderr, b"hung-before-timeout\n");
    }

    #[test]
    fn only_the_exact_os_signal_permits_a_job_lifetime_retry() {
        assert!(breakaway_denied(b"log\r\nWS_DAEMON_BREAKAWAY_DENIED=5\r\n"));
        assert!(!breakaway_denied(b"WS_DAEMON_BREAKAWAY_DENIED=50\n"));
        assert!(!breakaway_denied(
            b"spawn error: WS_DAEMON_BREAKAWAY_DENIED=5\n"
        ));
        assert!(!breakaway_denied(b"Access denied\n"));
    }
}
