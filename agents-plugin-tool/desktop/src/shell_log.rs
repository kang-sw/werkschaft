//! The persistent shell log, `<app_local_data_dir>/logs/shell.log`.
//!
//! A release build on Windows has no console (`windows_subsystem`), so
//! startup, staging, validation, fallback, and updater diagnostics would be
//! lost with stderr. Storage type 1 (append-only log): one file handle is
//! opened at startup and held; each line is one append; at 256 KiB the file
//! becomes `shell.log.1` (replacing the previous one) and a new file starts,
//! so at most two segments exist.

use std::{
    fs::{self, File, OpenOptions},
    io::{self, Write},
    path::{Path, PathBuf},
    sync::{Mutex, OnceLock},
    time::{SystemTime, UNIX_EPOCH},
};

pub const FILE: &str = "shell.log";
pub const ROTATED: &str = "shell.log.1";
pub const SEGMENT: u64 = 256 * 1024;

static LOG: OnceLock<Mutex<Log>> = OnceLock::new();

/// Logs one line to stderr and, once opened, to shell.log.
#[macro_export]
macro_rules! shell_log {
    ($($arg:tt)*) => {
        $crate::shell_log::emit(&format!($($arg)*))
    };
}

pub struct Log {
    directory: PathBuf,
    file: Option<File>,
    size: u64,
}

impl Log {
    pub fn open(directory: &Path) -> io::Result<Self> {
        fs::create_dir_all(directory)?;
        let file = OpenOptions::new()
            .create(true)
            .append(true)
            .open(directory.join(FILE))?;
        let size = file.metadata()?.len();
        Ok(Self {
            directory: directory.to_path_buf(),
            file: Some(file),
            size,
        })
    }

    pub fn append(&mut self, line: &str) {
        let bytes = line.len() as u64 + 1;
        if self.size > 0 && self.size + bytes > SEGMENT {
            self.rotate();
        }
        let Some(file) = self.file.as_mut() else {
            return;
        };
        let mut record = String::with_capacity(line.len() + 1);
        record.push_str(line);
        record.push('\n');
        if file.write_all(record.as_bytes()).is_ok() {
            self.size += bytes;
        }
    }

    // Best effort: a failed rotation keeps appending to the current file
    // rather than losing the line.
    fn rotate(&mut self) {
        // Windows cannot rename a file this process holds open.
        drop(self.file.take());
        let current = self.directory.join(FILE);
        let rotated = self.directory.join(ROTATED);
        let _ = fs::remove_file(&rotated);
        let renamed = fs::rename(&current, &rotated).is_ok();
        let options = || OpenOptions::new().create(true).append(true).open(&current);
        self.file = if renamed {
            File::create(&current).ok().or_else(|| options().ok())
        } else {
            options().ok()
        };
        // A failed rename (a scanner holding the old segment) is retried only
        // after another segment's worth of lines, never on every line.
        self.size = if renamed {
            self.file
                .as_ref()
                .and_then(|file| file.metadata().ok())
                .map_or(0, |meta| meta.len())
        } else {
            0
        };
    }
}

/// Opens shell.log for the rest of the run. A second call is ignored.
pub fn init(directory: &Path) {
    match Log::open(directory) {
        Ok(log) => {
            let _ = LOG.set(Mutex::new(log));
        }
        Err(error) => eprintln!("Werkschaft: Could not open the shell log: {error}"),
    }
}

/// The log directory, once opened.
pub fn directory() -> Option<PathBuf> {
    Some(LOG.get()?.lock().ok()?.directory.clone())
}

/// The current log file, once opened.
pub fn path() -> Option<PathBuf> {
    directory().map(|directory| directory.join(FILE))
}

pub fn emit(message: &str) {
    eprintln!("Werkschaft: {message}");
    if let Some(log) = LOG.get() {
        if let Ok(mut log) = log.lock() {
            log.append(&format!("[{}] {message}", timestamp(SystemTime::now())));
        }
    }
}

/// RFC 3339 UTC to the second.
fn timestamp(time: SystemTime) -> String {
    let seconds = time
        .duration_since(UNIX_EPOCH)
        .map_or(0, |elapsed| elapsed.as_secs());
    let (days, rest) = (seconds / 86_400, seconds % 86_400);
    // Howard Hinnant's civil-from-days.
    let z = days as i64 + 719_468;
    let era = z.div_euclid(146_097);
    let doe = z.rem_euclid(146_097);
    let yoe = (doe - doe / 1_460 + doe / 36_524 - doe / 146_096) / 365;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let day = doy - (153 * mp + 2) / 5 + 1;
    let month = if mp < 10 { mp + 3 } else { mp - 9 };
    let year = yoe + era * 400 + i64::from(month <= 2);
    format!(
        "{year:04}-{month:02}-{day:02}T{:02}:{:02}:{:02}Z",
        rest / 3_600,
        rest % 3_600 / 60,
        rest % 60
    )
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::time::Duration;

    #[test]
    fn appends_through_one_handle_and_keeps_two_segments_of_256_kib() {
        let temp = tempfile::tempdir().unwrap();
        let directory = temp.path().join("logs");
        let mut log = Log::open(&directory).unwrap();
        let line = "x".repeat(1023);
        for _ in 0..256 {
            log.append(&line);
        }
        // Exactly one segment's worth: no rotation yet.
        assert_eq!(fs::metadata(directory.join(FILE)).unwrap().len(), SEGMENT);
        assert!(!directory.join(ROTATED).exists());
        log.append("first of the next segment");
        assert_eq!(fs::metadata(directory.join(ROTATED)).unwrap().len(), SEGMENT);
        assert_eq!(
            fs::read_to_string(directory.join(FILE)).unwrap(),
            "first of the next segment\n"
        );
        for _ in 0..600 {
            log.append(&line);
        }
        let names: Vec<_> = fs::read_dir(&directory)
            .unwrap()
            .map(|entry| entry.unwrap().file_name().into_string().unwrap())
            .collect();
        assert_eq!(names.len(), 2, "{names:?}");
        for name in [FILE, ROTATED] {
            assert!(fs::metadata(directory.join(name)).unwrap().len() <= SEGMENT);
        }
        // Reopening continues the current segment instead of truncating it.
        let before = fs::metadata(directory.join(FILE)).unwrap().len();
        drop(log);
        let mut log = Log::open(&directory).unwrap();
        log.append("after restart");
        assert_eq!(
            fs::metadata(directory.join(FILE)).unwrap().len(),
            before + "after restart\n".len() as u64
        );
    }

    #[test]
    fn timestamps_are_rfc3339_utc() {
        assert_eq!(timestamp(UNIX_EPOCH), "1970-01-01T00:00:00Z");
        let time = UNIX_EPOCH + Duration::from_secs(1_795_610_468);
        assert_eq!(timestamp(time), "2026-11-25T12:41:08Z");
        let leap = UNIX_EPOCH + Duration::from_secs(951_782_400);
        assert_eq!(timestamp(leap), "2000-02-29T00:00:00Z");
    }
}
