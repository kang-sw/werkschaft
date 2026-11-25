use serde_json::Value;
use sha2::{Digest, Sha256};
use std::collections::{BTreeMap, BTreeSet};
use std::fmt;
use std::fs;
use std::io::{BufReader, Read};
use std::path::{Path, PathBuf};

const STAGE: &str = "Cannot stage daemon package";
const MAX_JSON: u64 = 4 * 1024 * 1024;
const PREFIX: &str = "${CLAUDE_PLUGIN_ROOT}/";
const PLUGIN: &str = ".claude-plugin/plugin.json";
const RUNTIME: &str = "runtime.json";

/// Why a daemon package was rejected. Every path is package-relative with
/// `/` separators, so a diagnostic never carries the user's directories.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum DaemonPackageError {
    /// The package root is not an absolute plain directory, or its path
    /// cannot be passed to the daemon.
    InvalidRoot,
    /// A required file or directory is missing or cannot be read.
    MissingFile { path: String },
    /// A symbolic link or reparse point, which a package never contains.
    SymlinkRejected { path: String },
    /// A name that is not a plain relative path, or an entry that is neither
    /// a file nor a directory.
    UnsafePath { path: String },
    /// `rsrc/manifest.json` or `skills/manifest.json` is missing or invalid.
    MissingManifest { tree: String },
    /// A file under `rsrc/` or `skills/` that its manifest does not list.
    UnexpectedFile { tree: String, path: String },
    /// A file whose content does not match its recorded SHA-256.
    HashMismatch { path: String },
    /// A JSON contract file is malformed or contradicts another.
    InvalidMetadata { path: String, detail: &'static str },
    /// The app's resources could not be located.
    Unavailable(&'static str),
}

use DaemonPackageError::*;

impl DaemonPackageError {
    /// The variant's name, as the recovery page shows it.
    pub fn variant(&self) -> &'static str {
        match self {
            InvalidRoot => "InvalidRoot",
            MissingFile { .. } => "MissingFile",
            SymlinkRejected { .. } => "SymlinkRejected",
            UnsafePath { .. } => "UnsafePath",
            MissingManifest { .. } => "MissingManifest",
            UnexpectedFile { .. } => "UnexpectedFile",
            HashMismatch { .. } => "HashMismatch",
            InvalidMetadata { .. } => "InvalidMetadata",
            Unavailable(_) => "Unavailable",
        }
    }

    /// The offending package-relative path.
    pub fn path(&self) -> Option<String> {
        match self {
            MissingFile { path }
            | SymlinkRejected { path }
            | UnsafePath { path }
            | UnexpectedFile { path, .. }
            | HashMismatch { path }
            | InvalidMetadata { path, .. } => Some(path.clone()),
            MissingManifest { tree } => Some(format!("{tree}/manifest.json")),
            InvalidRoot | Unavailable(_) => None,
        }
    }

    pub fn tree(&self) -> Option<&str> {
        match self {
            UnexpectedFile { tree, .. } | MissingManifest { tree } => Some(tree),
            _ => None,
        }
    }
}

impl fmt::Display for DaemonPackageError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            InvalidRoot => f.write_str("The daemon package directory cannot be used"),
            MissingFile { path } => write!(f, "{path} is missing or unreadable"),
            SymlinkRejected { path } => write!(f, "{path} is a link, which a package may not contain"),
            UnsafePath { path } => write!(f, "{path} is not a plain package file"),
            MissingManifest { tree } => write!(f, "{tree}/manifest.json is missing or invalid"),
            UnexpectedFile { tree, path } => {
                write!(f, "{path} is not listed in {tree}/manifest.json")
            }
            HashMismatch { path } => write!(f, "{path} does not match its recorded SHA-256"),
            InvalidMetadata { path, detail } => write!(f, "{path} {detail}"),
            Unavailable(reason) => f.write_str(reason),
        }
    }
}

type Result<T, E = DaemonPackageError> = std::result::Result<T, E>;

fn shown(root: &Path, path: &Path) -> String {
    let relative = path.strip_prefix(root).unwrap_or(path);
    let text = relative.to_string_lossy().replace('\\', "/");
    if text.is_empty() {
        ".".to_owned()
    } else {
        text
    }
}

fn invalid(path: impl Into<String>, detail: &'static str) -> DaemonPackageError {
    InvalidMetadata {
        path: path.into(),
        detail,
    }
}

#[derive(Debug, Clone)]
pub struct Candidate {
    pub root: PathBuf,
    pub binary: PathBuf,
    pub version: [u64; 3],
    pub config_manifests: Vec<PathBuf>,
    fingerprints: BTreeMap<PathBuf, [u8; 32]>,
}

/// A staged copy that is removed when dropped, unless a launch takes it with
/// [`Staged::keep`] first: a stage no process ever used does not accumulate.
pub struct Staged {
    candidate: Candidate,
    directory: tempfile::TempDir,
}

impl Staged {
    pub fn candidate(&self) -> &Candidate {
        &self.candidate
    }

    /// Keeps the directory for good, before any process may reference it.
    pub fn keep(self) -> Candidate {
        let _ = self.directory.keep();
        self.candidate
    }
}

fn safe_relative(value: &str) -> bool {
    !value.is_empty()
        && !value.contains(['\\', ':'])
        && value
            .split('/')
            .all(|s| !s.is_empty() && s != "." && s != "..")
}

fn plain_metadata(root: &Path, path: &Path) -> Result<fs::Metadata> {
    let meta = fs::symlink_metadata(path).map_err(|_| MissingFile {
        path: shown(root, path),
    })?;
    #[cfg(windows)]
    {
        use std::os::windows::fs::MetadataExt;
        if meta.file_attributes() & 0x400 != 0 {
            return Err(SymlinkRejected {
                path: shown(root, path),
            });
        }
    }
    if meta.file_type().is_symlink() {
        return Err(SymlinkRejected {
            path: shown(root, path),
        });
    }
    Ok(meta)
}

fn files(root: &Path) -> Result<BTreeSet<PathBuf>> {
    fn walk(root: &Path, relative: &Path, result: &mut BTreeSet<PathBuf>) -> Result<()> {
        let dir = root.join(relative);
        if !plain_metadata(root, &dir)?.is_dir() {
            return Err(if relative.as_os_str().is_empty() {
                InvalidRoot
            } else {
                UnsafePath {
                    path: shown(root, &dir),
                }
            });
        }
        let unreadable = || MissingFile {
            path: shown(root, &dir),
        };
        for entry in fs::read_dir(&dir).map_err(|_| unreadable())? {
            let entry = entry.map_err(|_| unreadable())?;
            let name = entry.file_name();
            let Some(name) = name.to_str().filter(|n| safe_relative(n) && !n.contains('/'))
            else {
                return Err(UnsafePath {
                    path: shown(root, &dir.join(&name)),
                });
            };
            let rel = relative.join(name);
            let meta = plain_metadata(root, &root.join(&rel))?;
            if meta.is_dir() {
                walk(root, &rel, result)?;
            } else if meta.is_file() {
                result.insert(rel);
            } else {
                return Err(UnsafePath {
                    path: shown(root, &rel),
                });
            }
        }
        Ok(())
    }
    let mut result = BTreeSet::new();
    walk(root, Path::new(""), &mut result)?;
    Ok(result)
}

fn json(root: &Path, relative: &str) -> Result<Value> {
    let path = root.join(relative);
    let meta = plain_metadata(root, &path)?;
    if !meta.is_file() || meta.len() > MAX_JSON {
        return Err(invalid(relative, "is not a JSON file within the size limit"));
    }
    let bytes = fs::read(&path).map_err(|_| MissingFile {
        path: relative.to_owned(),
    })?;
    serde_json::from_slice(&bytes).map_err(|_| invalid(relative, "is not valid JSON"))
}

fn hash(bytes: &[u8]) -> String {
    format!("{:x}", Sha256::digest(bytes))
}
fn hex(digest: &[u8; 32]) -> String {
    digest.iter().map(|b| format!("{b:02x}")).collect()
}
fn hash_file(root: &Path, path: &Path) -> Result<[u8; 32]> {
    if !plain_metadata(root, path)?.is_file() {
        return Err(UnsafePath {
            path: shown(root, path),
        });
    }
    let unreadable = || MissingFile {
        path: shown(root, path),
    };
    let mut reader = BufReader::new(fs::File::open(path).map_err(|_| unreadable())?);
    let mut digest = Sha256::new();
    let mut buffer = [0; 65536];
    loop {
        let n = reader.read(&mut buffer).map_err(|_| unreadable())?;
        if n == 0 {
            break;
        }
        digest.update(&buffer[..n]);
    }
    Ok(digest.finalize().into())
}
fn fingerprints(root: &Path, names: &BTreeSet<PathBuf>) -> Result<BTreeMap<PathBuf, [u8; 32]>> {
    names
        .iter()
        .map(|p| Ok((p.clone(), hash_file(root, &root.join(p))?)))
        .collect()
}
fn valid_hash(s: &str) -> bool {
    s.len() == 64
        && s.bytes()
            .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
}
fn file_bytes(root: &Path, relative: &str) -> Result<Vec<u8>> {
    if !safe_relative(relative) {
        return Err(UnsafePath {
            path: relative.to_owned(),
        });
    }
    let path = root.join(relative);
    if !plain_metadata(root, &path)?.is_file() {
        return Err(UnsafePath {
            path: relative.to_owned(),
        });
    }
    fs::read(path).map_err(|_| MissingFile {
        path: relative.to_owned(),
    })
}
fn reference(root: &Path, value: &str) -> Result<PathBuf> {
    let rel = value
        .strip_prefix(PREFIX)
        .or_else(|| value.strip_prefix("./"))
        .ok_or_else(|| UnsafePath {
            path: value.to_owned(),
        })?;
    let bytes = file_bytes(root, rel)?;
    if rel.ends_with(".json") {
        serde_json::from_slice::<Value>(&bytes).map_err(|_| invalid(rel, "is not valid JSON"))?;
    }
    Ok(root.join(rel))
}
fn tree(root: &Path, name: &str, all: &BTreeSet<PathBuf>) -> Result<()> {
    let manifest_name = format!("{name}/manifest.json");
    let missing = || MissingManifest {
        tree: name.to_owned(),
    };
    let manifest = json(root, &manifest_name).map_err(|_| missing())?;
    if manifest["schema_version"].as_u64() != Some(1) {
        return Err(invalid(manifest_name, "has an unsupported schema_version"));
    }
    let entries = manifest["files"]
        .as_object()
        .filter(|m| !m.is_empty())
        .ok_or_else(|| invalid(&manifest_name, "lists no files"))?;
    let mut listed = BTreeSet::new();
    let mut content = false;
    for (rel, expected) in entries {
        let expected = expected
            .as_str()
            .filter(|s| valid_hash(s))
            .ok_or_else(|| invalid(&manifest_name, "has an invalid SHA-256"))?;
        let path = format!("{name}/{rel}");
        let bytes = file_bytes(root, &path)?;
        let text = String::from_utf8_lossy(&bytes).replace("\r\n", "\n");
        if hash(text.as_bytes()) != expected {
            return Err(HashMismatch { path });
        }
        content |= if name == "skills" {
            rel.ends_with("/SKILL.md")
        } else {
            rel.ends_with(".md")
        };
        listed.insert(PathBuf::from(name).join(rel));
    }
    if !content {
        return Err(invalid(manifest_name, "lists no Markdown content"));
    }
    let manifest_path = PathBuf::from(name).join("manifest.json");
    for rel in all.iter().filter(|p| p.starts_with(name)) {
        if rel != &manifest_path && !listed.contains(rel) {
            return Err(UnexpectedFile {
                tree: name.to_owned(),
                path: shown(Path::new(""), rel),
            });
        }
    }
    Ok(())
}

fn metadata_name() -> &'static str {
    if cfg!(windows) {
        "ws-mcp.exe.metadata.json"
    } else {
        "ws-mcp.metadata.json"
    }
}

fn validate(root: &Path, development: bool) -> Result<Candidate> {
    if !root.is_absolute() {
        return Err(InvalidRoot);
    }
    let all = files(root)?;
    let plugin = json(root, PLUGIN)?;
    let name = plugin["name"]
        .as_str()
        .filter(|s| *s == "ws" || *s == "wsflow")
        .ok_or(invalid(PLUGIN, "does not name the ws or wsflow plugin"))?;
    let agents = plugin["agents"]
        .as_array()
        .filter(|a| !a.is_empty())
        .ok_or(invalid(PLUGIN, "lists no agents"))?;
    let server = &plugin["mcpServers"][name];
    let args = server["args"]
        .as_array()
        .ok_or(invalid(PLUGIN, "has no MCP server arguments"))?;
    let manifests: Vec<&str> = server["env"]["WS_MCP_CONFIG_MANIFESTS"]
        .as_str()
        .ok_or(invalid(PLUGIN, "names no config manifests"))?
        .split(';')
        .collect();
    if !args
        .iter()
        .any(|a| a.as_str() == Some(&format!("{PREFIX}bin/ws-mcp-launcher.py")))
        || !manifests.contains(&format!("{PREFIX}config-manifest.json").as_str())
        || !manifests.contains(&format!("{PREFIX}dashboard-config-manifest.json").as_str())
    {
        return Err(invalid(
            PLUGIN,
            "does not start the ws-mcp launcher with both config manifests",
        ));
    }
    for agent in agents {
        reference(root, agent.as_str().ok_or(invalid(PLUGIN, "lists an invalid agent"))?)?;
    }
    for arg in args {
        if let Some(s) = arg.as_str() {
            if s.starts_with(PREFIX) {
                reference(root, s)?;
            }
        }
    }
    let config_manifests = manifests
        .into_iter()
        .map(|s| reference(root, s))
        .collect::<Result<Vec<_>>>()?;
    if config_manifests
        .iter()
        .any(|p| p.to_str().map_or(true, |s| s.contains(';')))
    {
        return Err(InvalidRoot);
    }
    let contract = json(root, RUNTIME)?;
    if contract["plugin"].as_str() != Some(name) {
        return Err(invalid(RUNTIME, "names a different plugin"));
    }
    let binary = root.join(if cfg!(windows) {
        "ws-mcp.exe"
    } else {
        "ws-mcp"
    });
    let meta_name = metadata_name();
    let metadata = json(root, meta_name)?;
    let version = metadata["version"]
        .as_str()
        .and_then(crate::daemon::version_core)
        .ok_or(invalid(meta_name, "has no valid version"))?;
    let expected = contract["plugin_version"]
        .as_str()
        .and_then(crate::daemon::version_core)
        .ok_or(invalid(RUNTIME, "has no valid plugin_version"))?;
    if metadata["schema_version"].as_u64() != Some(1) {
        return Err(invalid(meta_name, "has an unsupported schema_version"));
    }
    if version[..2] != expected[..2] || (!development && version != expected) {
        return Err(invalid(
            meta_name,
            "does not match the plugin_version in runtime.json",
        ));
    }
    let digest = metadata["binary_sha256"]
        .as_str()
        .filter(|s| valid_hash(s))
        .ok_or(invalid(meta_name, "has no valid binary_sha256"))?;
    if !plain_metadata(root, &binary)?.is_file() {
        return Err(UnsafePath {
            path: shown(root, &binary),
        });
    }
    if hex(&hash_file(root, &binary)?) != digest {
        return Err(HashMismatch {
            path: shown(root, &binary),
        });
    }
    if let Some(protocol) = metadata.get("mcp_protocol") {
        if protocol.as_str() != Some(contract["mcp_protocol"].as_str().unwrap_or("")) {
            return Err(invalid(
                meta_name,
                "does not match the mcp_protocol in runtime.json",
            ));
        }
    }
    let exact = contract["runtime_capabilities"]["match"]
        .as_str()
        .unwrap_or("")
        .eq_ignore_ascii_case("exact");
    for field in ["tools", "commands"] {
        if let Some(actual) = metadata.get(field).filter(|v| !v.is_null()) {
            let list = || invalid(meta_name, "has an invalid tools or commands list");
            let actual = actual
                .as_array()
                .ok_or_else(list)?
                .iter()
                .map(|v| v.as_str().ok_or_else(list))
                .collect::<Result<BTreeSet<_>>>()?;
            let required: BTreeSet<&str> = contract[field]
                .as_object()
                .map(|o| o.keys().map(String::as_str).collect())
                .unwrap_or_default();
            if !required.is_subset(&actual) || (exact && actual != required) {
                return Err(invalid(
                    meta_name,
                    "does not provide the tools and commands runtime.json requires",
                ));
            }
        }
    }
    tree(root, "rsrc", &all)?;
    tree(root, "skills", &all)?;
    Ok(Candidate {
        root: root.to_path_buf(),
        binary,
        version,
        config_manifests,
        fingerprints: fingerprints(root, &all)?,
    })
}

impl Candidate {
    pub fn select(bundled: &Path, development: Option<&Path>) -> Result<Self> {
        validate(development.unwrap_or(bundled), development.is_some())
    }

    /// Revalidates a kept stage where it is, without copying it: its
    /// manifests, its file inventory, and its binary against the digest
    /// recorded when it was verified healthy.
    pub fn reopen(root: &Path, binary_sha256: &str) -> Result<Self> {
        let candidate = validate(root, true)?;
        if candidate.binary_sha256().as_deref() != Some(binary_sha256.to_ascii_lowercase().as_str()) {
            return Err(HashMismatch {
                path: shown(root, &candidate.binary),
            });
        }
        Ok(candidate)
    }

    /// The version the package declares, read without validating it, for a
    /// diagnostic about a package that failed validation.
    pub fn declared_version(root: &Path) -> Option<String> {
        let metadata = json(root, metadata_name()).ok()?;
        metadata["version"].as_str().map(str::to_owned)
    }

    /// The binary's SHA-256 as validated.
    pub fn binary_sha256(&self) -> Option<String> {
        let relative = self.binary.strip_prefix(&self.root).ok()?;
        self.fingerprints.get(relative).map(hex)
    }

    /// The stage directory's name, for a staged candidate.
    pub fn stage_name(&self) -> Option<String> {
        self.root
            .file_name()
            .and_then(|name| name.to_str())
            .map(str::to_owned)
    }

    pub fn stage(
        &self,
        user_data: &Path,
        app_resource_root: &Path,
    ) -> std::result::Result<Staged, &'static str> {
        if !user_data.is_absolute() || !app_resource_root.is_absolute() {
            return Err(STAGE);
        }
        let result = || -> std::result::Result<Staged, &'static str> {
            let container = user_data.join("daemon-stages");
            fs::create_dir_all(&container).map_err(|_| STAGE)?;
            let actual = fs::canonicalize(&container).map_err(|_| STAGE)?;
            for origin in [&self.root, app_resource_root] {
                let origin = fs::canonicalize(origin).map_err(|_| STAGE)?;
                if actual.starts_with(origin) {
                    return Err(STAGE);
                }
            }
            let partial = tempfile::Builder::new()
                .prefix("daemon-")
                .tempdir_in(&actual)
                .map_err(|_| STAGE)?;
            let before = files(&self.root).map_err(|_| STAGE)?;
            if before.iter().ne(self.fingerprints.keys()) {
                return Err(STAGE);
            }
            for rel in &before {
                let source = self.root.join(rel);
                let target = partial.path().join(rel);
                fs::create_dir_all(target.parent().ok_or(STAGE)?).map_err(|_| STAGE)?;
                fs::copy(&source, &target).map_err(|_| STAGE)?;
                let permissions = plain_metadata(&self.root, &source)
                    .map_err(|_| STAGE)?
                    .permissions();
                fs::set_permissions(&target, permissions).map_err(|_| STAGE)?;
            }
            let staged = validate(partial.path(), true).map_err(|_| STAGE)?;
            if staged.version != self.version
                || staged.fingerprints != self.fingerprints
                || self.binary.strip_prefix(&self.root).ok()
                    != staged.binary.strip_prefix(&staged.root).ok()
                || self
                    .config_manifests
                    .iter()
                    .map(|p| p.strip_prefix(&self.root).ok())
                    .collect::<Vec<_>>()
                    != staged
                        .config_manifests
                        .iter()
                        .map(|p| p.strip_prefix(&staged.root).ok())
                        .collect::<Vec<_>>()
            {
                return Err(STAGE);
            }
            let after = files(&self.root).map_err(|_| STAGE)?;
            if fingerprints(&self.root, &after).map_err(|_| STAGE)? != self.fingerprints {
                return Err(STAGE);
            }
            let root = partial.path().to_path_buf();
            let candidate = Candidate {
                binary: root.join(
                    staged
                        .binary
                        .strip_prefix(&staged.root)
                        .map_err(|_| STAGE)?,
                ),
                config_manifests: staged
                    .config_manifests
                    .iter()
                    .map(|p| {
                        p.strip_prefix(&staged.root)
                            .map(|p| root.join(p))
                            .map_err(|_| STAGE)
                    })
                    .collect::<std::result::Result<_, _>>()?,
                root,
                version: staged.version,
                fingerprints: staged.fingerprints,
            };
            Ok(Staged {
                candidate,
                directory: partial,
            })
        };
        result().map_err(|_| STAGE)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;
    use std::io::Write;

    fn write(root: &Path, path: &str, bytes: &[u8]) {
        let path = root.join(path);
        fs::create_dir_all(path.parent().unwrap()).unwrap();
        fs::write(path, bytes).unwrap();
    }
    fn save(root: &Path, path: &str, value: Value) {
        write(root, path, value.to_string().as_bytes());
    }
    fn fixture(root: &Path, version: &str) {
        let bin = if cfg!(windows) {
            "ws-mcp.exe"
        } else {
            "ws-mcp"
        };
        write(root, bin, b"binary fixture");
        save(
            root,
            &format!("{bin}.metadata.json"),
            json!({"schema_version":1,"version":version,"binary_sha256":hash(b"binary fixture"),"mcp_protocol":"p","tools":["a"],"commands":["b"]}),
        );
        save(
            root,
            "runtime.json",
            json!({"plugin":"ws","plugin_version":"1.2.3","mcp_protocol":"p","tools":{"a":"*"},"commands":{"b":"*"}}),
        );
        save(
            root,
            ".claude-plugin/plugin.json",
            json!({"name":"ws","agents":["./agent.md"],"mcpServers":{"ws":{"args":[format!("{PREFIX}bin/ws-mcp-launcher.py")],"env":{"WS_MCP_CONFIG_MANIFESTS":format!("{PREFIX}config-manifest.json;{PREFIX}dashboard-config-manifest.json;{PREFIX}extra.json")}}}}),
        );
        write(root, "agent.md", b"agent");
        write(root, "bin/ws-mcp-launcher.py", b"launcher");
        for name in [
            "config-manifest.json",
            "dashboard-config-manifest.json",
            "extra.json",
        ] {
            save(root, name, json!({}));
        }
        for (tree, file) in [("rsrc", "x/a.md"), ("skills", "x/SKILL.md")] {
            write(root, &format!("{tree}/{file}"), b"hello\r\n");
            save(
                root,
                &format!("{tree}/manifest.json"),
                json!({"schema_version":1,"files":{file:hash(b"hello\n")}}),
            );
        }
    }
    #[test]
    fn selection_and_retained_stages() {
        let temp = tempfile::tempdir().unwrap();
        let bundled = temp.path().join("app/daemon");
        let dev = temp.path().join("dev");
        fixture(&bundled, "1.2.3-dev");
        fixture(&dev, "1.2.9-dev");
        let b = Candidate::select(&bundled, None).unwrap();
        assert_eq!(b.version, [1, 2, 3]);
        assert_eq!(b.config_manifests.len(), 3);
        assert!(Candidate::select(&dev, None).is_err());
        assert_eq!(
            Candidate::select(&bundled, Some(&dev)).unwrap().version,
            [1, 2, 9]
        );
        assert!(Candidate::select(&bundled, Some(&temp.path().join("missing"))).is_err());
        let user = temp.path().join("user");
        let a = b.stage(&user, &temp.path().join("app")).unwrap().keep();
        let c = b.stage(&user, &temp.path().join("app")).unwrap().keep();
        assert_ne!(a.root, c.root);
        assert!(b
            .stage(&temp.path().join("app"), &temp.path().join("app"))
            .is_err());
        fs::remove_dir_all(temp.path().join("app")).unwrap();
        assert_eq!(fs::read(&a.binary).unwrap(), b"binary fixture");
        assert!(a
            .config_manifests
            .iter()
            .all(|p| p.starts_with(&a.root) && p.is_file()));
        #[cfg(unix)]
        {
            use std::os::unix::fs::MetadataExt;
            assert_ne!(
                fs::metadata(&a.binary).unwrap().ino(),
                fs::metadata(&c.binary).unwrap().ino()
            );
        }
    }
    #[test]
    fn a_stage_no_launch_took_is_removed() {
        let temp = tempfile::tempdir().unwrap();
        let bundled = temp.path().join("app/daemon");
        fixture(&bundled, "1.2.3");
        let candidate = Candidate::select(&bundled, None).unwrap();
        let user = temp.path().join("user");
        let abandoned = candidate.stage(&user, &temp.path().join("app")).unwrap();
        let root = abandoned.candidate().root.clone();
        assert!(root.starts_with(fs::canonicalize(user.join("daemon-stages")).unwrap()));
        assert!(abandoned.candidate().binary.is_file());
        drop(abandoned);
        assert!(!root.exists());
        let kept = candidate
            .stage(&user, &temp.path().join("app"))
            .unwrap()
            .keep();
        assert!(kept.binary.is_file());
        assert_eq!(fs::read_dir(user.join("daemon-stages")).unwrap().count(), 1);
    }
    #[test]
    fn rejects_tampering_inventory_and_unsafe_references() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().join("daemon");
        fixture(&root, "1.2.3");
        let meta = if cfg!(windows) {
            "ws-mcp.exe.metadata.json"
        } else {
            "ws-mcp.metadata.json"
        };
        let original = fs::read(root.join(meta)).unwrap();
        let mut m: Value = serde_json::from_slice(&original).unwrap();
        m["tools"] = json!([]);
        save(&root, meta, m.clone());
        let capabilities = invalid(
            meta,
            "does not provide the tools and commands runtime.json requires",
        );
        assert_eq!(Candidate::select(&root, None).unwrap_err(), capabilities);
        m["tools"] = json!(["a", "extra"]);
        save(&root, meta, m);
        let mut contract = json(&root, "runtime.json").unwrap();
        contract["runtime_capabilities"] = json!({"match":"exact"});
        save(&root, "runtime.json", contract);
        assert_eq!(Candidate::select(&root, None).unwrap_err(), capabilities);
        fs::write(root.join(meta), original).unwrap();
        write(&root, "rsrc/unlisted.md", b"extra");
        assert_eq!(
            Candidate::select(&root, None).unwrap_err(),
            UnexpectedFile {
                tree: "rsrc".into(),
                path: "rsrc/unlisted.md".into()
            }
        );
        fs::remove_file(root.join("rsrc/unlisted.md")).unwrap();
        let mut plugin = json(&root, ".claude-plugin/plugin.json").unwrap();
        plugin["agents"] = json!(["./../outside"]);
        save(&root, ".claude-plugin/plugin.json", plugin);
        assert_eq!(
            Candidate::select(&root, None).unwrap_err(),
            UnsafePath {
                path: "../outside".into()
            }
        );
    }

    // The v0.52.0 upgrade left files a newer release dropped under rsrc/ and
    // skills/. The rejection names the tree and the file, never an opaque
    // "Invalid daemon package".
    #[test]
    fn a_residual_file_is_reported_by_tree_and_path() {
        for (tree, file) in [("rsrc", "rsrc/x/.pi.md"), ("skills", "skills/old/SKILL.md")] {
            let temp = tempfile::tempdir().unwrap();
            let root = temp.path().join("daemon");
            fixture(&root, "1.2.3");
            write(&root, file, b"stale");
            let error = Candidate::select(&root, None).unwrap_err();
            assert_eq!(error.variant(), "UnexpectedFile");
            assert_eq!(error.tree(), Some(tree));
            assert_eq!(error.path().as_deref(), Some(file));
            assert_eq!(
                error.to_string(),
                format!("{file} is not listed in {tree}/manifest.json")
            );
            assert!(!error.to_string().contains(&*temp.path().to_string_lossy()));
        }
    }

    #[test]
    fn each_rejection_names_its_variant_and_package_path() {
        let meta = if cfg!(windows) {
            "ws-mcp.exe.metadata.json"
        } else {
            "ws-mcp.metadata.json"
        };
        let bin = if cfg!(windows) { "ws-mcp.exe" } else { "ws-mcp" };
        let cases: [(&str, fn(&Path), DaemonPackageError); 4] = [
            (
                "missing manifest",
                |root| fs::remove_file(root.join("skills/manifest.json")).unwrap(),
                MissingManifest {
                    tree: "skills".into(),
                },
            ),
            (
                "content hash",
                |root| write(root, "rsrc/x/a.md", b"edited\n"),
                HashMismatch {
                    path: "rsrc/x/a.md".into(),
                },
            ),
            (
                "binary hash",
                |root| {
                    let bin = if cfg!(windows) { "ws-mcp.exe" } else { "ws-mcp" };
                    write(root, bin, b"other binary")
                },
                HashMismatch { path: bin.into() },
            ),
            (
                "metadata",
                |root| {
                    let meta = if cfg!(windows) {
                        "ws-mcp.exe.metadata.json"
                    } else {
                        "ws-mcp.metadata.json"
                    };
                    write(root, meta, b"{")
                },
                invalid(meta, "is not valid JSON"),
            ),
        ];
        for (name, change, expected) in cases {
            let temp = tempfile::tempdir().unwrap();
            let root = temp.path().join("daemon");
            fixture(&root, "1.2.3");
            change(&root);
            assert_eq!(Candidate::select(&root, None).unwrap_err(), expected, "{name}");
        }
        assert_eq!(
            Candidate::select(Path::new("relative"), None).unwrap_err(),
            InvalidRoot
        );
    }

    #[test]
    fn a_kept_stage_is_revalidated_in_place_against_its_recorded_digest() {
        let temp = tempfile::tempdir().unwrap();
        let bundled = temp.path().join("app/daemon");
        fixture(&bundled, "1.2.3");
        let candidate = Candidate::select(&bundled, None).unwrap();
        assert_eq!(
            Candidate::declared_version(&bundled).as_deref(),
            Some("1.2.3")
        );
        let digest = candidate.binary_sha256().unwrap();
        assert_eq!(digest, hash(b"binary fixture"));
        let kept = candidate
            .stage(&temp.path().join("user"), &temp.path().join("app"))
            .unwrap()
            .keep();
        assert!(kept.stage_name().unwrap().starts_with("daemon-"));
        let reopened = Candidate::reopen(&kept.root, &digest.to_ascii_uppercase()).unwrap();
        assert_eq!(reopened.root, kept.root);
        assert_eq!(reopened.binary, kept.binary);
        assert!(matches!(
            Candidate::reopen(&kept.root, &"0".repeat(64)),
            Err(HashMismatch { .. })
        ));
        write(&kept.root, "skills/x/extra.md", b"residue");
        assert!(matches!(
            Candidate::reopen(&kept.root, &digest),
            Err(UnexpectedFile { .. })
        ));
    }
    #[test]
    fn staging_revalidates_selected_bytes() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().join("app/daemon");
        fixture(&root, "1.2.3");
        let candidate = Candidate::select(&root, None).unwrap();
        fs::OpenOptions::new()
            .append(true)
            .open(&candidate.binary)
            .unwrap()
            .write_all(b"tamper")
            .unwrap();
        assert!(candidate
            .stage(&temp.path().join("user"), &temp.path().join("app"))
            .is_err());
    }
    #[test]
    fn rejects_valid_same_core_changes_after_selection() {
        for change in ["binary", "resource", "contract", "reference"] {
            let temp = tempfile::tempdir().unwrap();
            let root = temp.path().join("app/daemon");
            fixture(&root, "1.2.3");
            let candidate = Candidate::select(&root, None).unwrap();
            match change {
                "binary" => {
                    fs::write(&candidate.binary, b"new binary").unwrap();
                    let mut value = json(&root, metadata_name()).unwrap();
                    value["binary_sha256"] = json!(hash(b"new binary"));
                    fs::write(root.join(metadata_name()), value.to_string()).unwrap();
                }
                "resource" => {
                    write(&root, "rsrc/x/a.md", b"new content\n");
                    save(
                        &root,
                        "rsrc/manifest.json",
                        json!({"schema_version":1,"files":{"x/a.md":hash(b"new content\n")}}),
                    );
                }
                "contract" => {
                    let mut value = json(&root, "runtime.json").unwrap();
                    value["extra"] = json!(true);
                    save(&root, "runtime.json", value);
                }
                _ => save(&root, "extra.json", json!({"new":true})),
            }
            assert!(
                Candidate::select(&root, None).is_ok(),
                "fixture remains valid: {change}"
            );
            assert!(
                candidate
                    .stage(&temp.path().join("user"), &temp.path().join("app"))
                    .is_err(),
                "{change}"
            );
        }
    }
    #[test]
    fn rejects_semicolon_in_manifest_absolute_paths() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().join("bad;root/daemon");
        fixture(&root, "1.2.3");
        assert_eq!(Candidate::select(&root, None).unwrap_err(), InvalidRoot);
        let root = temp.path().join("app/daemon");
        fixture(&root, "1.2.3");
        let candidate = Candidate::select(&root, None).unwrap();
        assert!(candidate
            .stage(&temp.path().join("bad;user"), &temp.path().join("app"))
            .is_err());
    }
    #[cfg(unix)]
    #[test]
    fn rejects_links_even_outside_manifests() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().join("daemon");
        fixture(&root, "1.2.3");
        std::os::unix::fs::symlink(root.join("agent.md"), root.join("extra-link")).unwrap();
        assert_eq!(
            Candidate::select(&root, None).unwrap_err(),
            SymlinkRejected {
                path: "extra-link".into()
            }
        );
    }
}
