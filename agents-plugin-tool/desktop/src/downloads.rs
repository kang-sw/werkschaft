// Downloads from the pinned dashboard origin, saved to the user's Downloads
// folder. WKWebView drops a download no delegate answers; WebView2 shows its
// own UI unless the app answers. Both are answered here: a request from any
// other origin is refused (the navigation boundary), an accepted one gets a
// sanitized, never-overwriting path, and its end is announced natively.

use std::{
    path::{Path, PathBuf},
    sync::{Arc, Mutex},
    thread,
    time::{Duration, Instant},
};

use tauri::{webview::DownloadEvent, Manager, Runtime, Webview};
use url::Url;

use crate::{
    navigation::{destination, Destination},
    notify,
    shell::Connection,
};

const FALLBACK_NAME: &str = "download";
/// The longest file name kept, in bytes; filesystems allow 255.
const NAME_MAX: usize = 200;
/// How long a reservation is held without its completion arriving.
const HOLD: Duration = Duration::from_secs(600);
/// The most " (n)" suffixes tried before the download is refused; each try
/// is a file probe under the ledger lock.
const SUFFIX_MAX: u32 = 100;

/// Every window shares one ledger, so two downloads never pick one name.
static ACTIVE: Mutex<Active> = Mutex::new(Active::new());

/// The handler for `WebviewWindowBuilder::on_download`.
pub fn handler<R: Runtime>(
    connection: Arc<Connection>,
) -> impl Fn(Webview<R>, DownloadEvent<'_>) -> bool + Send + Sync + 'static {
    move |webview, event| match event {
        DownloadEvent::Requested { url, destination } => {
            if !accepts(&url, connection.pinned_url().as_ref()) {
                return false;
            }
            let Ok(folder) = webview.path().download_dir() else {
                announce(&webview, false, "The Downloads folder is unavailable");
                return false;
            };
            let name = pick_name(destination.file_name().and_then(|name| name.to_str()), &url);
            let begun = ACTIVE.lock().unwrap().begin(
                url,
                &folder,
                &name,
                |path| path.exists(),
                Instant::now(),
            );
            match begun {
                Begin::Reserved(path) => {
                    *destination = path;
                    true
                }
                Begin::NoName => {
                    announce(&webview, false, &name);
                    false
                }
            }
        }
        DownloadEvent::Finished { url, success, .. } => {
            let path = ACTIVE.lock().unwrap().finish(&url, Instant::now());
            let name = path
                .as_deref()
                .and_then(Path::file_name)
                .map(|name| name.to_string_lossy().into_owned());
            announce(&webview, success, name.as_deref().unwrap_or(""));
            true
        }
        _ => true,
    }
}

/// What a download request gets.
#[derive(Debug, PartialEq, Eq)]
pub enum Begin {
    Reserved(PathBuf),
    /// Every candidate name is taken.
    NoName,
}

/// Downloads accepted and not yet finished. A completion names only its URL
/// (WKWebView gives no path, and completions of one URL can arrive in any
/// order), so a file is named only when the match is certain: its URL has had
/// one download at a time. Nothing is held past `HOLD`.
pub struct Active {
    reserved: Vec<Reservation>,
    /// URLs that have had two reservations at once; their completions name no
    /// file until the count returns to zero.
    ambiguous: Vec<Url>,
}

struct Reservation {
    url: Url,
    path: PathBuf,
    at: Instant,
}

impl Active {
    pub const fn new() -> Self {
        Self {
            reserved: Vec::new(),
            ambiguous: Vec::new(),
        }
    }

    fn expire(&mut self, now: Instant) {
        self.reserved
            .retain(|held| now.saturating_duration_since(held.at) < HOLD);
        self.clear_idle();
    }

    /// An ambiguity ends when its URL has no reservation left.
    fn clear_idle(&mut self) {
        let reserved = &self.reserved;
        self.ambiguous
            .retain(|url| reserved.iter().any(|held| held.url == *url));
    }

    /// Reserves a path for `url`, unique among files and reservations;
    /// `exists` tells whether a file is there.
    pub fn begin(
        &mut self,
        url: Url,
        folder: &Path,
        name: &str,
        exists: impl Fn(&Path) -> bool,
        now: Instant,
    ) -> Begin {
        self.expire(now);
        let reserved = &self.reserved;
        let Some(path) = unique_path(folder, name, |candidate| {
            exists(candidate) || reserved.iter().any(|held| held.path == candidate)
        }) else {
            return Begin::NoName;
        };
        if self.reserved.iter().any(|held| held.url == url) && !self.ambiguous.contains(&url) {
            self.ambiguous.push(url.clone());
        }
        self.reserved.push(Reservation {
            url,
            path: path.clone(),
            at: now,
        });
        Begin::Reserved(path)
    }

    /// Ends the oldest download of `url`; every completion is announced.
    /// Returns its file only when that is certain: the URL's one reservation,
    /// never overlapped. None for several, an ambiguous URL, an unknown URL,
    /// or an expired reservation.
    pub fn finish(&mut self, url: &Url, now: Instant) -> Option<PathBuf> {
        self.expire(now);
        let count = self.reserved.iter().filter(|held| held.url == *url).count();
        let certain = count == 1 && !self.ambiguous.contains(url);
        let oldest = self.reserved.iter().position(|held| held.url == *url)?;
        let path = self.reserved.remove(oldest).path;
        self.clear_idle();
        certain.then_some(path)
    }
}

fn announce<R: Runtime>(webview: &Webview<R>, success: bool, detail: &str) {
    let identifier = webview.app_handle().config().identifier.clone();
    let title = if success {
        "Download complete"
    } else {
        "Download failed"
    };
    let detail = detail.to_owned();
    // The end event runs on the UI thread; showing may block.
    thread::spawn(move || {
        if !notify::show(&identifier, title, &detail) {
            eprintln!("Werkschaft: Could not show the download notification");
        }
    });
}

/// Whether a download from `url` is allowed: the pinned dashboard origin only.
pub fn accepts(url: &Url, dashboard: Option<&Url>) -> bool {
    destination(url, dashboard) == Destination::Dashboard
}

/// The file name to save as: the suggested one, else the URL's last path
/// segment, else "download". Always a single, safe path component.
pub fn pick_name(suggested: Option<&str>, url: &Url) -> String {
    suggested
        .and_then(sanitize)
        .or_else(|| {
            let segment = url.path_segments()?.next_back()?;
            sanitize(&percent_decode(segment))
        })
        .unwrap_or_else(|| FALLBACK_NAME.into())
}

/// Reduces `name` to one path component safe on macOS and Windows, or None
/// when nothing usable is left.
pub fn sanitize(name: &str) -> Option<String> {
    let leaf = name.rsplit(['/', '\\']).next().unwrap_or_default();
    let cleaned: String = leaf
        .chars()
        .filter(|c| !c.is_control() && !matches!(c, '<' | '>' | ':' | '"' | '|' | '?' | '*'))
        .collect();
    // No hidden files, and no trailing dot or space, which Windows drops.
    let cleaned = cleaned
        .trim_start_matches('.')
        .trim_end_matches(['.', ' '])
        .trim_start();
    if cleaned.is_empty() {
        return None;
    }
    let (stem, ext) = split_extension(cleaned);
    let stem = if is_reserved_device(stem) {
        format!("_{stem}")
    } else {
        stem.to_owned()
    };
    Some(clip(&stem, ext, NAME_MAX))
}

/// Windows device names, with or without an extension, name no file.
fn is_reserved_device(stem: &str) -> bool {
    let stem = stem.trim_end().to_ascii_uppercase();
    matches!(stem.as_str(), "CON" | "PRN" | "AUX" | "NUL")
        || ["COM", "LPT"].iter().any(|prefix| {
            stem.strip_prefix(prefix)
                .is_some_and(|n| matches!(n.as_bytes(), [b'1'..=b'9']))
        })
}

/// Splits at the last dot; ".tar.gz" and the like keep both parts together.
fn split_extension(name: &str) -> (&str, &str) {
    let Some(dot) = name.rfind('.').filter(|dot| *dot > 0) else {
        return (name, "");
    };
    let (stem, ext) = name.split_at(dot);
    if ext.len() > 16 {
        return (name, "");
    }
    match stem.strip_suffix(".tar").or_else(|| {
        let lower = stem.to_ascii_lowercase();
        lower.ends_with(".tar").then(|| &stem[..stem.len() - 4])
    }) {
        Some(base) if !base.is_empty() => name.split_at(base.len()),
        _ => (stem, ext),
    }
}

/// `stem` + `tail` within `limit` bytes, cutting the stem on a char boundary.
fn clip(stem: &str, tail: &str, limit: usize) -> String {
    let room = limit.saturating_sub(tail.len());
    let mut end = stem.len().min(room);
    while !stem.is_char_boundary(end) {
        end -= 1;
    }
    format!("{}{tail}", &stem[..end])
}

fn percent_decode(segment: &str) -> String {
    let bytes = segment.as_bytes();
    let mut out = Vec::with_capacity(bytes.len());
    let mut at = 0;
    while at < bytes.len() {
        let decoded = (bytes[at] == b'%')
            .then(|| bytes.get(at + 1..at + 3))
            .flatten()
            .and_then(|hex| std::str::from_utf8(hex).ok())
            .and_then(|hex| u8::from_str_radix(hex, 16).ok());
        match decoded {
            Some(byte) => {
                out.push(byte);
                at += 3;
            }
            None => {
                out.push(bytes[at]);
                at += 1;
            }
        }
    }
    String::from_utf8_lossy(&out).into_owned()
}

/// `folder/name`, or the first free `folder/stem (n).ext` for n from 1, the
/// way a browser numbers a repeat. None when every number up to the limit is
/// taken. `taken` answers whether a path is in use.
pub fn unique_path(folder: &Path, name: &str, taken: impl Fn(&Path) -> bool) -> Option<PathBuf> {
    let first = folder.join(name);
    if !taken(&first) {
        return Some(first);
    }
    let (stem, ext) = split_extension(name);
    (1..=SUFFIX_MAX)
        // Cut the stem, not the tail, so the number and extension survive.
        .map(|n| folder.join(clip(stem, &format!(" ({n}){ext}"), NAME_MAX)))
        .find(|candidate| !taken(candidate))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn url(raw: &str) -> Url {
        Url::parse(raw).unwrap()
    }

    #[test]
    fn only_the_pinned_dashboard_origin_may_download() {
        let pin = url("http://127.0.0.1:39417/");
        assert!(accepts(
            &url("http://127.0.0.1:39417/s/abc/board.zip"),
            Some(&pin)
        ));
        for raw in [
            "http://127.0.0.1:39418/s/abc/board.zip",
            "https://127.0.0.1:39417/s/abc/board.zip",
            "http://localhost:39417/board.zip",
            "https://example.com/board.zip",
            "http://user@127.0.0.1:39417/board.zip",
            "blob:http://127.0.0.1:39417/1234",
            "data:text/plain,hi",
            "file:///etc/passwd",
            "tauri://localhost/index.html",
            "http://tauri.localhost/index.html",
        ] {
            assert!(!accepts(&url(raw), Some(&pin)), "{raw}");
        }
        // No pinned dashboard: nothing is the dashboard.
        assert!(!accepts(&url("http://127.0.0.1:39417/board.zip"), None));
    }

    #[test]
    fn a_suggested_name_becomes_one_safe_path_component() {
        for (raw, want) in [
            ("board.zip", "board.zip"),
            ("../../etc/passwd", "passwd"),
            ("..\\..\\Windows\\evil.dll", "evil.dll"),
            ("/abs/path/report.pdf", "report.pdf"),
            ("a:b*c?.txt", "abc.txt"),
            ("  spaced name .txt", "spaced name .txt"),
            (".bashrc", "bashrc"),
            ("trailing. . ", "trailing"),
            ("tab\tnew\nline.md", "tabnewline.md"),
            ("한글 보드.zip", "한글 보드.zip"),
            ("CON", "_CON"),
            ("nul.txt", "_nul.txt"),
            ("com1.log", "_com1.log"),
            ("com10.log", "com10.log"),
        ] {
            assert_eq!(sanitize(raw).as_deref(), Some(want), "{raw:?}");
        }
        for raw in ["", ".", "..", "/", "\\", "...", " ", "<>:\"|?*", "dir/"] {
            assert_eq!(sanitize(raw), None, "{raw:?}");
        }
    }

    #[test]
    fn a_long_name_is_cut_in_its_stem_and_keeps_the_extension() {
        let name = sanitize(&format!("{}.zip", "名".repeat(200))).unwrap();
        assert!(name.len() <= NAME_MAX);
        assert!(name.ends_with(".zip"));
        let name = sanitize(&format!("{}.tar.gz", "a".repeat(400))).unwrap();
        assert_eq!(name.len(), NAME_MAX);
        assert!(name.ends_with(".tar.gz"));
    }

    #[test]
    fn the_name_falls_back_to_the_url_then_to_download() {
        let page = url("https://x.test/s/abc/board%20one.zip?x=1");
        assert_eq!(pick_name(Some("given.zip"), &page), "given.zip");
        assert_eq!(pick_name(None, &page), "board one.zip");
        assert_eq!(pick_name(Some(""), &page), "board one.zip");
        assert_eq!(pick_name(Some("../"), &page), "board one.zip");
        assert_eq!(pick_name(None, &url("https://x.test/")), "download");
        assert_eq!(pick_name(None, &url("https://x.test/a/%2e%2e")), "download");
        assert_eq!(
            pick_name(None, &url("https://x.test/a/%2F%2Fb.txt")),
            "b.txt"
        );
        assert_eq!(pick_name(None, &url("https://x.test/a/%zz.txt")), "%zz.txt");
    }

    #[test]
    fn a_taken_name_gets_a_browser_style_number() {
        let folder = Path::new("/dl");
        let free = |_: &Path| false;
        assert_eq!(
            unique_path(folder, "a.zip", free).unwrap(),
            folder.join("a.zip")
        );
        let taken = |names: &'static [&'static str]| {
            move |path: &Path| names.iter().any(|name| path == folder.join(name))
        };
        assert_eq!(
            unique_path(folder, "a.zip", taken(&["a.zip"])).unwrap(),
            folder.join("a (1).zip")
        );
        assert_eq!(
            unique_path(folder, "a.zip", taken(&["a.zip", "a (1).zip", "a (2).zip"])).unwrap(),
            folder.join("a (3).zip")
        );
        assert_eq!(
            unique_path(folder, "a.tar.gz", taken(&["a.tar.gz"])).unwrap(),
            folder.join("a (1).tar.gz")
        );
        assert_eq!(
            unique_path(folder, "README", taken(&["README"])).unwrap(),
            folder.join("README (1)")
        );
        // A gap is not reused before the first free number.
        assert_eq!(
            unique_path(folder, "a.zip", taken(&["a.zip", "a (2).zip"])).unwrap(),
            folder.join("a (1).zip")
        );
        assert_eq!(unique_path(folder, "a.zip", |_| true), None);
    }

    #[test]
    fn an_existing_file_is_never_chosen() {
        let dir = tempfile::tempdir().unwrap();
        std::fs::write(dir.path().join("board.zip"), "mine").unwrap();
        std::fs::write(dir.path().join("board (1).zip"), "mine").unwrap();
        let path = unique_path(dir.path(), "board.zip", |path| path.exists()).unwrap();
        assert_eq!(path, dir.path().join("board (2).zip"));
        assert_eq!(
            std::fs::read_to_string(dir.path().join("board.zip")).unwrap(),
            "mine"
        );
    }

    #[test]
    fn a_long_taken_name_still_gets_its_number_and_extension() {
        let folder = Path::new("/dl");
        let long = sanitize(&format!("{}.zip", "a".repeat(400))).unwrap();
        assert_eq!(long.len(), NAME_MAX);
        let numbered = unique_path(folder, &long, |path| path == folder.join(&long)).unwrap();
        let name = numbered.file_name().unwrap().to_str().unwrap();
        assert_ne!(name, long);
        assert!(name.ends_with(" (1).zip"), "{name}");
        assert!(name.len() <= NAME_MAX);
        let wide = sanitize(&format!("{}.tar.gz", "名".repeat(100))).unwrap();
        let numbered = unique_path(folder, &wide, |path| path == folder.join(&wide)).unwrap();
        let name = numbered.file_name().unwrap().to_str().unwrap();
        assert!(
            name.ends_with(" (1).tar.gz") && name.len() <= NAME_MAX,
            "{name}"
        );
    }

    #[test]
    fn the_number_search_is_short() {
        let probes = std::cell::Cell::new(0);
        let taken = |_: &Path| {
            probes.set(probes.get() + 1);
            true
        };
        assert_eq!(unique_path(Path::new("/dl"), "a.zip", taken), None);
        assert!(probes.get() <= 101, "{}", probes.get());
    }

    #[test]
    fn each_request_for_one_url_reserves_its_own_name() {
        let folder = Path::new("/dl");
        let board = url("http://127.0.0.1:1/s/a/board.zip");
        let now = Instant::now();
        let mut active = Active::new();
        assert_eq!(
            active.begin(board.clone(), folder, "board.zip", |_| false, now),
            Begin::Reserved(folder.join("board.zip"))
        );
        assert_eq!(
            active.begin(board.clone(), folder, "board.zip", |_| false, now),
            Begin::Reserved(folder.join("board (1).zip"))
        );
        // Two in flight: no completion names a file, even the last one.
        assert_eq!(active.finish(&board, now), None);
        assert_eq!(active.finish(&board, now), None);
    }

    #[test]
    fn a_lone_download_is_named_and_its_url_can_start_over() {
        let folder = Path::new("/dl");
        let board = url("http://127.0.0.1:1/board.zip");
        let now = Instant::now();
        let mut active = Active::new();
        active.begin(board.clone(), folder, "board.zip", |_| false, now);
        // Another URL's completion releases nothing here.
        active.finish(&url("https://x.test/z"), now);
        assert_eq!(active.finish(&board, now), Some(folder.join("board.zip")));
        active.begin(board.clone(), folder, "board.zip", |_| false, now);
        assert_eq!(active.finish(&board, now), Some(folder.join("board.zip")));
    }

    #[test]
    fn overlapping_downloads_of_a_url_stay_unnamed_until_none_is_left() {
        let folder = Path::new("/dl");
        let board = url("http://127.0.0.1:1/board.zip");
        let now = Instant::now();
        let mut active = Active::new();
        active.begin(board.clone(), folder, "board.zip", |_| false, now);
        active.begin(board.clone(), folder, "board.zip", |_| false, now);
        // The newer one finishes first, then the older one: with one
        // reservation left the name would be the wrong file, so none is given.
        assert_eq!(active.finish(&board, now), None);
        assert_eq!(active.finish(&board, now), None);
        // The count reached zero, so the mark is gone.
        active.begin(board.clone(), folder, "board.zip", |_| false, now);
        assert_eq!(active.finish(&board, now), Some(folder.join("board.zip")));
    }

    #[test]
    fn an_unknown_completion_is_generic_and_takes_nothing() {
        let folder = Path::new("/dl");
        let board = url("http://127.0.0.1:1/board.zip");
        let now = Instant::now();
        let mut active = Active::new();
        // Announced with no name by the handler; the ledger holds nothing.
        assert_eq!(active.finish(&board, now), None);
        active.begin(board.clone(), folder, "board.zip", |_| false, now);
        assert_eq!(active.finish(&board, now), Some(folder.join("board.zip")));
        assert_eq!(active.finish(&board, now), None);
    }

    #[test]
    fn a_reservation_expires_after_ten_minutes() {
        let folder = Path::new("/dl");
        let board = url("http://127.0.0.1:1/board.zip");
        let start = Instant::now();
        let mut active = Active::new();
        active.begin(board.clone(), folder, "board.zip", |_| false, start);
        let late = start + HOLD;
        // The held name is free again, and the expired one leaves no overlap.
        assert_eq!(
            active.begin(board.clone(), folder, "board.zip", |_| false, late),
            Begin::Reserved(folder.join("board.zip"))
        );
        assert_eq!(active.finish(&board, late), Some(folder.join("board.zip")));
        // Just before expiry the reservation still answers.
        active.begin(board.clone(), folder, "board.zip", |_| false, start);
        let before = start + HOLD - Duration::from_secs(1);
        assert_eq!(
            active.finish(&board, before),
            Some(folder.join("board.zip"))
        );
        // The completion of an expired download is generic.
        active.begin(board.clone(), folder, "board.zip", |_| false, start);
        assert_eq!(active.finish(&board, late), None);
    }

    #[test]
    fn a_full_name_space_refuses_without_reserving() {
        let mut active = Active::new();
        let board = url("http://127.0.0.1:1/board.zip");
        let now = Instant::now();
        assert_eq!(
            active.begin(board.clone(), Path::new("/dl"), "a.zip", |_| true, now),
            Begin::NoName
        );
        assert_eq!(active.finish(&board, now), None);
    }
}
