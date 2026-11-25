//! What a failed or fallback daemon start reports: the local recovery page's
//! payload, the fallback notice, Copy Diagnostics, and shell.log all read
//! this one record. Every free-text field is redacted before it is stored.

use serde::Serialize;

/// The bounded `daemon-start` stderr capture.
pub const STDERR_TAIL: usize = 16 * 1024;

/// Where the last-known-good fallback stands for this start.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum LkgState {
    /// The failure never reached the fallback: a daemon was running, or the
    /// start failed before the bundled package was selected.
    #[default]
    NotTried,
    /// No health-verified stage is recorded.
    None,
    /// Every recorded stage is below `daemon-minor-floor`, or the floor file
    /// could not be read.
    Blocked,
    /// Every eligible stage failed its in-place revalidation.
    Invalid,
    /// The stage launched but did not become healthy.
    Failed,
    /// The stage is serving.
    Running,
}

/// The structured diagnostic of one start. Serialized in camelCase for the
/// recovery page (`window.__werkschaftRecovery`).
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Diagnostics {
    /// The headline: what failed.
    pub reason: String,
    /// The `DaemonPackageError` variant name, when the package was rejected.
    pub variant: Option<String>,
    /// The manifest tree (`rsrc` or `skills`) an unexpected file was in.
    pub tree: Option<String>,
    /// The offending package-relative path.
    pub path: Option<String>,
    /// The package error's own sentence.
    pub detail: Option<String>,
    pub bundled_version: Option<String>,
    pub lkg: LkgState,
    /// The stage the fallback used, or the newest one it could not use.
    pub lkg_version: Option<String>,
    /// `daemon-minor-floor` as `major.minor`, or `unreadable`.
    pub minor_floor: Option<String>,
    /// The fallback was refused by the minor floor.
    pub floor_blocked: bool,
    pub exit_code: Option<i32>,
    pub stderr_tail: Option<String>,
    pub log_path: Option<String>,
}

impl Diagnostics {
    pub fn reason(reason: &str) -> Self {
        Self {
            reason: reason.to_owned(),
            log_path: crate::shell_log::path().map(|path| redact(&path.to_string_lossy())),
            ..Self::default()
        }
    }

    /// Adds a starter's exit code and the redacted tail of its stderr.
    pub fn starter(&mut self, exit_code: Option<i32>, stderr: &[u8]) {
        self.exit_code = exit_code;
        let tail = tail(stderr, STDERR_TAIL);
        self.stderr_tail = (!tail.trim().is_empty()).then(|| redact(&tail));
    }

    /// The text Copy Diagnostics puts on the clipboard.
    pub fn report(&self) -> String {
        serde_json::to_string_pretty(self).expect("diagnostics serialize")
    }
}

/// The last `max` bytes of `bytes` as text, starting on a character boundary.
pub fn tail(bytes: &[u8], max: usize) -> String {
    let mut start = bytes.len().saturating_sub(max);
    // Skip UTF-8 continuation bytes so a cut never splits a character.
    while start < bytes.len() && bytes[start] & 0xC0 == 0x80 {
        start += 1;
    }
    String::from_utf8_lossy(&bytes[start..]).into_owned()
}

/// Redacts credentials and the user's home directory from diagnostic text.
pub fn redact(text: &str) -> String {
    let home = std::env::var("HOME")
        .ok()
        .filter(|home| !home.is_empty())
        .or_else(|| std::env::var("USERPROFILE").ok())
        .filter(|home| home.len() > 3);
    redact_with(text, home.as_deref())
}

const SECRET_KEYS: [&str; 7] = [
    "token", "secret", "password", "cookie", "bearer", "auth", "credential",
];

fn redact_with(text: &str, home: Option<&str>) -> String {
    let mut text = text.to_owned();
    if let Some(home) = home {
        text = text.replace(home, "~");
        let slashed = home.replace('\\', "/");
        if slashed != home {
            text = text.replace(&slashed, "~");
        }
    }
    let mut out = String::with_capacity(text.len());
    let mut rest = text.as_str();
    while let Some(c) = rest.chars().next() {
        // A key such as `token=`, `"auth_token": "`, or `Bearer ` hides the
        // value that follows it.
        if let Some(skip) = secret_key(rest) {
            let (key, after) = rest.split_at(skip);
            out.push_str(key);
            let mut value = value_end(after);
            // `Authorization: Bearer <credential>` hides the credential too.
            let scheme = after[..value].to_ascii_lowercase();
            if (scheme == "bearer" || scheme == "basic") && after[value..].starts_with(' ') {
                value += 1 + value_end(&after[value + 1..]);
            }
            if value > 0 {
                out.push_str("[REDACTED]");
            }
            rest = &after[value..];
            continue;
        }
        // A bare 32+ hex digit run is a credential (or a digest nobody needs).
        let hex = rest.bytes().take_while(u8::is_ascii_hexdigit).count();
        if hex >= 32 && !out.ends_with(|c: char| c.is_ascii_alphanumeric()) {
            out.push_str("[REDACTED]");
            rest = &rest[hex..];
            continue;
        }
        let word = if c.is_ascii_alphanumeric() {
            rest.find(|c: char| !c.is_ascii_alphanumeric())
                .unwrap_or(rest.len())
        } else {
            c.len_utf8()
        };
        out.push_str(&rest[..word]);
        rest = &rest[word..];
    }
    out
}

fn value_end(text: &str) -> usize {
    text.find(|c: char| c.is_whitespace() || matches!(c, '"' | '\'' | ',' | '}' | ';' | '&'))
        .unwrap_or(text.len())
}

/// The length of a secret key and its separator at the start of `text`.
fn secret_key(text: &str) -> Option<usize> {
    let end = (0..=text.len().min(40))
        .rev()
        .find(|&index| text.is_char_boundary(index))
        .unwrap_or(0);
    let lower = text[..end].to_ascii_lowercase();
    let mut name = 0;
    for c in lower.chars() {
        if c.is_ascii_alphanumeric() || c == '_' || c == '-' {
            name += 1;
        } else {
            break;
        }
    }
    let key = &lower[..name];
    if !SECRET_KEYS.iter().any(|secret| key.ends_with(secret) || key.starts_with(secret)) {
        return None;
    }
    let separator = lower[name..]
        .find(|c: char| !matches!(c, '"' | '\'' | ' ' | ':' | '='))
        .unwrap_or(lower.len() - name);
    let between = &lower[name..name + separator];
    let assigned = between.contains(['=', ':']) || (key == "bearer" && between == " ");
    assigned.then_some(name + separator)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn credentials_and_the_home_directory_are_redacted() {
        let token = "a".repeat(64);
        let text = format!(
            "C:\\Users\\kim\\AppData\\x token={token} \"auth_token\": \"abc123\" Authorization: Bearer xyz C:/Users/kim/y {token}"
        );
        let redacted = redact_with(&text, Some("C:\\Users\\kim"));
        assert!(!redacted.contains("kim"), "{redacted}");
        assert!(!redacted.contains(&token), "{redacted}");
        assert!(!redacted.contains("abc123") && !redacted.contains("xyz"), "{redacted}");
        assert!(redacted.starts_with("~\\AppData\\x token=[REDACTED]"), "{redacted}");
        assert!(redacted.contains("~/y"), "{redacted}");
        // Ordinary words and short numbers stay.
        assert_eq!(
            redact_with("exit status 78 at rsrc/x.md", None),
            "exit status 78 at rsrc/x.md"
        );
        assert_eq!(redact_with("authority granted", None), "authority granted");
    }

    #[test]
    fn the_stderr_tail_is_bounded_and_never_splits_a_character() {
        let bytes = "é".repeat(10).into_bytes();
        assert_eq!(tail(&bytes, 5), "éé");
        assert_eq!(tail(b"abc", 16), "abc");
        let mut diagnostics = Diagnostics::default();
        let long = vec![b'x'; STDERR_TAIL + 100];
        diagnostics.starter(Some(78), &long);
        assert_eq!(diagnostics.exit_code, Some(78));
        assert_eq!(diagnostics.stderr_tail.unwrap().len(), STDERR_TAIL);
        diagnostics.stderr_tail = None;
        diagnostics.starter(None, b" \n");
        assert!(diagnostics.stderr_tail.is_none());
    }

    #[test]
    fn the_page_payload_uses_camel_case_and_kebab_lkg_states() {
        let diagnostics = Diagnostics {
            reason: "r".into(),
            bundled_version: Some("0.52.0".into()),
            lkg: LkgState::NotTried,
            floor_blocked: true,
            ..Diagnostics::default()
        };
        let value = serde_json::to_value(&diagnostics).unwrap();
        assert_eq!(value["bundledVersion"], "0.52.0");
        assert_eq!(value["lkg"], "not-tried");
        assert_eq!(value["floorBlocked"], true);
    }
}
