//! Last-known-good (LKG) daemon stages and the collection of stale ones.
//!
//! `<app_local_data_dir>/daemon-stages/lkg-index.json` (storage type 5, a
//! small pointer file) names the newest health-verified stages, at most one
//! per version and [`RETAIN`] in all. It is written only when a verified
//! stage's path or version changes; a launch of an already-recorded stage and
//! an attach-only start write nothing. Nothing is ever written inside a
//! `daemon-<rand>` stage: the package validation would reject it.

use std::{
    cmp::Ordering,
    fs, io,
    path::Path,
    time::{SystemTime, UNIX_EPOCH},
};

use serde::{Deserialize, Serialize};
use serde_json::Value;

use crate::daemon::version_core;

pub const INDEX: &str = "lkg-index.json";
/// The health-verified stages kept.
pub const RETAIN: usize = 2;
/// A stage renamed for removal; a failed removal is retried next time.
const TRASH: &str = ".gc";
const MAX_JSON: u64 = 64 * 1024;

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct Entry {
    /// The stage's `daemon-<rand>` directory name.
    pub stage: String,
    /// The version the healthy daemon reported.
    pub version: String,
    pub binary_sha256: String,
    /// Unix seconds of the health verification.
    pub verified_at: u64,
}

#[derive(Serialize, Deserialize)]
struct Index {
    schema_version: u32,
    entries: Vec<Entry>,
}

pub fn now() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |elapsed| elapsed.as_secs())
}

/// A directory name a stage may have; anything else is never touched.
pub fn valid_stage_name(name: &str) -> bool {
    name.strip_prefix("daemon-").is_some_and(|random| {
        !random.is_empty()
            && random
                .bytes()
                .all(|byte| byte.is_ascii_alphanumeric() || byte == b'_' || byte == b'-')
    })
}

fn valid_sha(value: &str) -> bool {
    value.len() == 64 && value.bytes().all(|byte| byte.is_ascii_hexdigit())
}

/// Highest version first; a tie goes to the most recent verification.
fn newest_first(a: &Entry, b: &Entry) -> Ordering {
    version_core(&b.version)
        .cmp(&version_core(&a.version))
        .then(b.verified_at.cmp(&a.verified_at))
}

fn read_small(path: &Path) -> io::Result<Vec<u8>> {
    use io::Read;
    let mut bytes = Vec::new();
    fs::File::open(path)?
        .take(MAX_JSON + 1)
        .read_to_end(&mut bytes)?;
    if bytes.len() as u64 > MAX_JSON {
        return Err(io::Error::new(io::ErrorKind::InvalidData, "too large"));
    }
    Ok(bytes)
}

/// The recorded stages, newest first. A missing or unreadable index is empty:
/// an upgrade from a shell that kept none starts with no fallback.
pub fn load(stages: &Path) -> Vec<Entry> {
    let mut entries: Vec<Entry> = read_small(&stages.join(INDEX))
        .ok()
        .and_then(|bytes| serde_json::from_slice::<Index>(&bytes).ok())
        .filter(|index| index.schema_version == 1)
        .map(|index| index.entries)
        .unwrap_or_default();
    entries.retain(|entry| {
        valid_stage_name(&entry.stage)
            && version_core(&entry.version).is_some()
            && valid_sha(&entry.binary_sha256)
    });
    entries.sort_by(newest_first);
    entries
}

/// The index after verifying `entry`, or None when it would not change.
pub fn upsert(entries: &[Entry], entry: Entry) -> Option<Vec<Entry>> {
    if entries
        .iter()
        .any(|known| known.stage == entry.stage && known.version == entry.version)
    {
        return None;
    }
    let mut next: Vec<Entry> = entries
        .iter()
        .filter(|known| known.stage != entry.stage && known.version != entry.version)
        .cloned()
        .collect();
    next.push(entry);
    next.sort_by(newest_first);
    next.truncate(RETAIN);
    let mut current = entries.to_vec();
    current.sort_by(newest_first);
    (next != current).then_some(next)
}

/// Records a verified stage; true when the index was rewritten.
pub fn record(stages: &Path, entry: Entry) -> io::Result<bool> {
    let Some(entries) = upsert(&load(stages), entry) else {
        return Ok(false);
    };
    let bytes = serde_json::to_vec_pretty(&Index {
        schema_version: 1,
        entries,
    })
    .map_err(io::Error::other)?;
    let partial = stages.join(format!("{INDEX}.tmp"));
    fs::write(&partial, bytes)?;
    fs::rename(&partial, stages.join(INDEX))?;
    Ok(true)
}

/// `<cache>/dashboard/daemon-minor-floor`, read the way the daemon reads it.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Floor {
    /// No file: legacy state, every version is admitted.
    Absent,
    /// The numeric major.minor floor.
    At([u64; 2]),
    /// The file exists but cannot be read or parsed: nothing is admitted.
    Unreadable,
}

impl Floor {
    pub fn read(dashboard: &Path) -> Self {
        match fs::read(dashboard.join("daemon-minor-floor")) {
            Err(error) if error.kind() == io::ErrorKind::NotFound => Self::Absent,
            Err(_) => Self::Unreadable,
            Ok(bytes) => std::str::from_utf8(&bytes)
                .ok()
                .and_then(Self::parse)
                .map_or(Self::Unreadable, Self::At),
        }
    }

    /// One numeric `major.minor` pair, optionally surrounded by whitespace.
    pub fn parse(raw: &str) -> Option<[u64; 2]> {
        let (major, minor) = raw.trim().split_once('.')?;
        let number = |part: &str| {
            (!part.is_empty() && part.bytes().all(|byte| byte.is_ascii_digit()))
                .then(|| part.parse::<u64>().ok())
                .flatten()
        };
        Some([number(major)?, number(minor)?])
    }

    /// Whether a stage of `version` may start under this floor: its
    /// major.minor alone is compared, as the daemon does.
    pub fn admits(self, version: &str) -> bool {
        let Some(core) = version_core(version) else {
            return false;
        };
        match self {
            Self::Absent => true,
            Self::At(floor) => [core[0], core[1]] >= floor,
            Self::Unreadable => false,
        }
    }

    pub fn label(self) -> Option<String> {
        match self {
            Self::Absent => None,
            Self::At([major, minor]) => Some(format!("{major}.{minor}")),
            Self::Unreadable => Some("unreadable".to_owned()),
        }
    }
}

/// The stages the fallback may try, newest first.
pub fn eligible(entries: &[Entry], floor: Floor) -> Vec<&Entry> {
    let mut eligible: Vec<&Entry> = entries
        .iter()
        .filter(|entry| floor.admits(&entry.version))
        .collect();
    eligible.sort_by(|a, b| newest_first(a, b));
    eligible
}

/// The singleton daemon's `last-running-executable.json` digest: the daemon
/// writes it before it publishes its endpoint, so a discoverable daemon's
/// digest is current.
pub fn running_sha(dashboard: &Path) -> Option<String> {
    let bytes = read_small(&dashboard.join("last-running-executable.json")).ok()?;
    let value: Value = serde_json::from_slice(&bytes).ok()?;
    value["binary_sha256"]
        .as_str()
        .filter(|sha| valid_sha(sha))
        .map(str::to_ascii_lowercase)
}

/// A stage directory as found on disk.
#[derive(Clone, Debug, Default)]
pub struct Stage {
    pub name: String,
    pub version: Option<String>,
    pub binary_sha256: Option<String>,
}

/// What collection must keep beyond the recorded stages.
#[derive(Clone, Copy, Debug, Default)]
pub struct Protect<'a> {
    /// `last-running-executable.json`'s digest. That file names the daemon's
    /// content-addressed copy, never a stage, so stages are matched by digest
    /// (several stages may share one).
    pub running_sha: Option<&'a str>,
    /// The discovered daemon's version.
    pub running_version: Option<&'a str>,
    /// The stage just launched, healthy, pending, or timed out: a late start
    /// may still be running from it.
    pub launched: Option<&'a str>,
}

/// The stages collection removes: every stage that is neither among the
/// [`RETAIN`] newest recorded ones nor protected.
pub fn doomed(stages: &[Stage], index: &[Entry], protect: &Protect) -> Vec<String> {
    let recorded: Vec<&str> = index
        .iter()
        .take(RETAIN)
        .map(|entry| entry.stage.as_str())
        .collect();
    let running_core = protect.running_version.and_then(version_core);
    stages
        .iter()
        .filter(|stage| {
            let same_binary = stage
                .binary_sha256
                .as_deref()
                .zip(protect.running_sha)
                .is_some_and(|(stage, running)| stage.eq_ignore_ascii_case(running));
            // The running daemon's stage is matched by its version too, a
            // superset of version-plus-digest: a version match alone keeps it.
            let same_version = running_core.is_some()
                && stage.version.as_deref().and_then(version_core) == running_core;
            !(recorded.contains(&stage.name.as_str())
                || protect.launched == Some(stage.name.as_str())
                || same_binary
                || same_version)
        })
        .map(|stage| stage.name.clone())
        .collect()
}

fn stage_metadata(root: &Path) -> (Option<String>, Option<String>) {
    let name = if cfg!(windows) {
        "ws-mcp.exe.metadata.json"
    } else {
        "ws-mcp.metadata.json"
    };
    let value = read_small(&root.join(name))
        .ok()
        .and_then(|bytes| serde_json::from_slice::<Value>(&bytes).ok())
        .unwrap_or_default();
    let field = |key: &str| value[key].as_str().map(str::to_owned);
    (field("version"), field("binary_sha256"))
}

/// Removes stale stages, best effort. A stage is first renamed aside; on
/// Windows a stage a process still runs from cannot be renamed, so it is
/// skipped whole rather than deleted piecemeal. Removal errors (sharing
/// violations, access denied) are logged and retried by the next collection.
pub fn collect(stages: &Path, protect: &Protect) {
    let Ok(listing) = fs::read_dir(stages) else {
        return;
    };
    let index = load(stages);
    let mut found = Vec::new();
    let mut trash = Vec::new();
    for entry in listing.flatten() {
        let Ok(name) = entry.file_name().into_string() else {
            continue;
        };
        if !entry.file_type().is_ok_and(|kind| kind.is_dir()) {
            continue;
        }
        if name
            .strip_suffix(TRASH)
            .is_some_and(valid_stage_name)
        {
            trash.push(name);
        } else if valid_stage_name(&name) {
            let (version, binary_sha256) = stage_metadata(&entry.path());
            found.push(Stage {
                name,
                version,
                binary_sha256,
            });
        }
    }
    for name in doomed(&found, &index, protect) {
        let aside = format!("{name}{TRASH}");
        match fs::rename(stages.join(&name), stages.join(&aside)) {
            Ok(()) => trash.push(aside),
            Err(error) => crate::shell_log!("Kept stage {name}: it is in use ({error})"),
        }
    }
    for name in trash {
        if let Err(error) = fs::remove_dir_all(stages.join(&name)) {
            crate::shell_log!("Could not remove stale stage {name} yet ({error})");
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn entry(stage: &str, version: &str, verified_at: u64) -> Entry {
        Entry {
            stage: stage.into(),
            version: version.into(),
            binary_sha256: format!("{:064x}", verified_at),
            verified_at,
        }
    }

    #[test]
    fn an_unchanged_verification_writes_nothing() {
        let temp = tempfile::tempdir().unwrap();
        let stages = temp.path();
        assert!(load(stages).is_empty());
        assert!(record(stages, entry("daemon-a", "0.52.0", 10)).unwrap());
        let written = fs::metadata(stages.join(INDEX)).unwrap().modified().unwrap();
        // Relaunching the recorded stage, even verified later, is zero-write.
        assert!(!record(stages, entry("daemon-a", "0.52.0", 99)).unwrap());
        assert_eq!(
            fs::metadata(stages.join(INDEX)).unwrap().modified().unwrap(),
            written
        );
        assert_eq!(load(stages), [entry("daemon-a", "0.52.0", 10)]);
        // A fresh stage of the same version replaces the recorded one.
        assert!(record(stages, entry("daemon-b", "0.52.0", 20)).unwrap());
        assert_eq!(load(stages), [entry("daemon-b", "0.52.0", 20)]);
        // At most RETAIN, newest version first, then latest verification.
        assert!(record(stages, entry("daemon-c", "0.52.1", 30)).unwrap());
        let written = fs::metadata(stages.join(INDEX)).unwrap().modified().unwrap();
        // An older version past RETAIN changes nothing and writes nothing.
        assert!(!record(stages, entry("daemon-d", "0.51.9", 40)).unwrap());
        assert_eq!(
            fs::metadata(stages.join(INDEX)).unwrap().modified().unwrap(),
            written
        );
        let kept: Vec<_> = load(stages).into_iter().map(|e| e.stage).collect();
        assert_eq!(kept, ["daemon-c", "daemon-b"]);
        assert!(!stages.join(format!("{INDEX}.tmp")).exists());
    }

    #[test]
    fn a_tampered_or_foreign_index_is_ignored() {
        let temp = tempfile::tempdir().unwrap();
        for bytes in [
            &b"not json"[..],
            br#"{"schema_version":2,"entries":[]}"#,
        ] {
            fs::write(temp.path().join(INDEX), bytes).unwrap();
            assert!(load(temp.path()).is_empty());
        }
        let hostile = serde_json::json!({"schema_version": 1, "entries": [
            {"stage": "../outside", "version": "0.52.0", "binary_sha256": "0".repeat(64), "verified_at": 1},
            {"stage": "daemon-x", "version": "garbage", "binary_sha256": "0".repeat(64), "verified_at": 1},
            {"stage": "daemon-y", "version": "0.52.0", "binary_sha256": "short", "verified_at": 1},
            {"stage": "daemon-z", "version": "0.52.0", "binary_sha256": "0".repeat(64), "verified_at": 1},
        ]});
        fs::write(temp.path().join(INDEX), hostile.to_string()).unwrap();
        let stages: Vec<_> = load(temp.path()).into_iter().map(|e| e.stage).collect();
        assert_eq!(stages, ["daemon-z"]);
    }

    #[test]
    fn the_floor_compares_major_minor_and_fails_closed() {
        let temp = tempfile::tempdir().unwrap();
        assert_eq!(Floor::read(temp.path()), Floor::Absent);
        for (raw, floor) in [
            (" 0.53\n", Floor::At([0, 53])),
            ("1.0", Floor::At([1, 0])),
            ("0.53.1", Floor::Unreadable),
            ("0.", Floor::Unreadable),
            ("+0.53", Floor::Unreadable),
            ("v0.53", Floor::Unreadable),
            ("", Floor::Unreadable),
        ] {
            fs::write(temp.path().join("daemon-minor-floor"), raw).unwrap();
            assert_eq!(Floor::read(temp.path()), floor, "{raw:?}");
        }
        fs::write(temp.path().join("daemon-minor-floor"), [0xff, b'.', b'1']).unwrap();
        assert_eq!(Floor::read(temp.path()), Floor::Unreadable);
        let floor = Floor::At([0, 53]);
        assert!(floor.admits("0.53.0") && floor.admits("0.53.7-dev") && floor.admits("1.0.0"));
        assert!(!floor.admits("0.52.99") && !floor.admits("garbage"));
        assert!(!Floor::Unreadable.admits("9.9.9"));
        assert!(Floor::Absent.admits("0.1.0"));
        assert_eq!(Floor::Unreadable.label().as_deref(), Some("unreadable"));
    }

    #[test]
    fn the_running_digest_is_read_from_the_singletons_record() {
        let temp = tempfile::tempdir().unwrap();
        let file = temp.path().join("last-running-executable.json");
        assert_eq!(running_sha(temp.path()), None);
        let digest = "Ab".repeat(32);
        let record = |sha: &str| {
            serde_json::json!({"schema_version": 1, "executable": "/x/ws-mcp",
                "version": "0.52.1", "binary_sha256": sha, "build_time": 1})
            .to_string()
        };
        fs::write(&file, record(&digest)).unwrap();
        assert_eq!(running_sha(temp.path()), Some(digest.to_ascii_lowercase()));
        for bad in [record("short"), "{".to_owned(), "x".repeat(70 * 1024)] {
            fs::write(&file, bad).unwrap();
            assert_eq!(running_sha(temp.path()), None);
        }
    }

    #[test]
    fn eligible_stages_are_newest_first_above_the_floor() {
        let entries = [
            entry("daemon-a", "0.52.1", 5),
            entry("daemon-b", "0.53.0", 1),
            entry("daemon-c", "0.53.0", 9),
        ];
        let order: Vec<_> = eligible(&entries, Floor::At([0, 53]))
            .into_iter()
            .map(|e| e.stage.as_str())
            .collect();
        assert_eq!(order, ["daemon-c", "daemon-b"]);
        assert!(eligible(&entries, Floor::At([0, 54])).is_empty());
        assert!(eligible(&entries, Floor::Unreadable).is_empty());
        assert_eq!(eligible(&entries, Floor::Absent).len(), 3);
    }

    #[test]
    fn collection_keeps_recorded_running_and_launched_stages() {
        let stage = |name: &str, version: &str, sha: char| Stage {
            name: name.into(),
            version: Some(version.into()),
            binary_sha256: Some(sha.to_string().repeat(64)),
        };
        let stages = [
            stage("daemon-lkg1", "0.52.1", '1'),
            stage("daemon-lkg2", "0.52.0", '2'),
            stage("daemon-old", "0.51.0", '3'),
            stage("daemon-same-binary-a", "0.50.0", '4'),
            stage("daemon-same-binary-b", "0.50.0", '4'),
            stage("daemon-running", "0.49.0", '5'),
            stage("daemon-pending", "0.53.0", '6'),
            stage("daemon-failed", "0.53.0", '6'),
            Stage {
                name: "daemon-unreadable".into(),
                ..Stage::default()
            },
        ];
        let index = [
            entry("daemon-lkg1", "0.52.1", 2),
            entry("daemon-lkg2", "0.52.0", 1),
            entry("daemon-old", "0.51.0", 0),
        ];
        let running = "4".repeat(64);
        let protect = Protect {
            running_sha: Some(&running),
            running_version: Some("0.49.0"),
            launched: Some("daemon-pending"),
        };
        assert_eq!(
            doomed(&stages, &index, &protect),
            ["daemon-old", "daemon-failed", "daemon-unreadable"]
        );
        // Without protections only the retained LKG stages survive.
        assert_eq!(doomed(&stages, &index, &Protect::default()).len(), 7);
    }

    #[test]
    fn collection_removes_stale_stages_on_disk_and_never_foreign_entries() {
        let temp = tempfile::tempdir().unwrap();
        let stages = temp.path();
        for name in ["daemon-keep", "daemon-stale", "daemon-old.gc", "other"] {
            fs::create_dir_all(stages.join(name).join("rsrc")).unwrap();
        }
        fs::write(stages.join("daemon-loose-file"), b"x").unwrap();
        record(stages, entry("daemon-keep", "0.52.0", 1)).unwrap();
        collect(stages, &Protect::default());
        let mut left: Vec<_> = fs::read_dir(stages)
            .unwrap()
            .map(|entry| entry.unwrap().file_name().into_string().unwrap())
            .collect();
        left.sort();
        assert_eq!(left, ["daemon-keep", "daemon-loose-file", INDEX, "other"]);
    }
}
