// Page zoom for the dashboard webview.
//
// WKWebView has no built-in Cmd+=/Cmd+-/Cmd+0 handling (Safari implements
// those in its own app menu), and Tauri's `zoom_hotkeys_enabled` on macOS
// injects a JS polyfill that calls the `set_webview_zoom` IPC command. The
// remote dashboard deliberately holds no native permissions, so that polyfill
// would be denied. Native menu accelerators drive `set_zoom` instead.

use std::{
    path::Path,
    sync::{
        mpsc::{self, Sender},
        Mutex,
    },
    thread::{self, JoinHandle},
};

use rusqlite::{Connection, OptionalExtension};

/// Browser-like zoom ladder (Safari/Chrome steps), 1.0 is actual size.
const LEVELS: [f64; 13] = [
    0.5, 0.67, 0.75, 0.8, 0.9, 1.0, 1.1, 1.25, 1.5, 1.75, 2.0, 2.5, 3.0,
];

#[derive(Default)]
pub struct Zoom {
    state: Mutex<ZoomState>,
}

struct ZoomState {
    level: f64,
    writer: Option<(Sender<f64>, JoinHandle<()>)>,
}

impl Default for ZoomState {
    fn default() -> Self {
        Self {
            level: 1.0,
            writer: None,
        }
    }
}

impl Zoom {
    pub fn open(directory: &Path) -> Result<Self, Box<dyn std::error::Error>> {
        std::fs::create_dir_all(directory)?;
        let database = Connection::open(directory.join("page-zoom.sqlite"))?;
        database.execute_batch(
            "PRAGMA journal_mode = WAL;
             CREATE TABLE IF NOT EXISTS zoom (id INTEGER PRIMARY KEY, level REAL NOT NULL);",
        )?;
        let level = database
            .query_row("SELECT level FROM zoom WHERE id = 1", [], |row| row.get(0))
            .optional()?
            .filter(|level| LEVELS.contains(level))
            .unwrap_or(1.0);
        let (sender, receiver) = mpsc::channel::<f64>();
        let worker = thread::Builder::new()
            .name("page-zoom".into())
            .spawn(move || {
                for level in receiver {
                    if database
                        .execute(
                            "INSERT INTO zoom VALUES (1, ?1)
                             ON CONFLICT(id) DO UPDATE SET level = excluded.level
                             WHERE level != excluded.level",
                            [level],
                        )
                        .is_err()
                    {
                        eprintln!("Werkschaft: Could not save the page zoom");
                    }
                }
            })?;
        Ok(Self {
            state: Mutex::new(ZoomState {
                level,
                writer: Some((sender, worker)),
            }),
        })
    }

    pub fn current(&self) -> f64 {
        self.state.lock().unwrap().level
    }

    // Native menu actions are serialized on the UI thread.
    pub fn change(&self, step: Step, apply: impl FnOnce(f64) -> bool) -> Option<f64> {
        let current = self.current();
        let level = next(current, step);
        if level == current || !apply(level) {
            return None;
        }
        let mut state = self.state.lock().unwrap();
        state.level = level;
        if let Some((sender, _)) = &state.writer {
            let _ = sender.send(level);
        }
        Some(level)
    }

    pub fn finish(&self) {
        let writer = self.state.lock().unwrap().writer.take();
        if let Some((sender, worker)) = writer {
            drop(sender);
            let _ = worker.join();
        }
    }
}

#[derive(Clone, Copy, Debug, PartialEq)]
pub enum Step {
    In,
    Out,
    Reset,
}

impl Step {
    /// Map a menu item id to a zoom step.
    pub fn from_menu_id(id: &str) -> Option<Step> {
        match id {
            "zoom_in" | "zoom_in_shifted" => Some(Step::In),
            "zoom_out" => Some(Step::Out),
            "zoom_reset" => Some(Step::Reset),
            _ => None,
        }
    }
}

/// The next ladder level from `current`, clamped at both ends.
pub fn next(current: f64, step: Step) -> f64 {
    match step {
        Step::Reset => 1.0,
        Step::In => LEVELS
            .iter()
            .copied()
            .find(|level| *level > current + 1e-6)
            .unwrap_or(LEVELS[LEVELS.len() - 1]),
        Step::Out => LEVELS
            .iter()
            .rev()
            .copied()
            .find(|level| *level < current - 1e-6)
            .unwrap_or(LEVELS[0]),
    }
}

#[cfg(test)]
mod tests {
    use super::{next, Connection, Step, Zoom};

    #[test]
    fn restores_last_changed_level_when_reopened() {
        let directory = tempfile::tempdir().unwrap();
        let zoom = Zoom::open(directory.path()).unwrap();
        assert_eq!(zoom.current(), 1.0);
        assert_eq!(zoom.change(Step::In, |_| true), Some(1.1));
        assert_eq!(zoom.change(Step::In, |_| true), Some(1.25));
        assert_eq!(zoom.change(Step::In, |_| false), None);
        zoom.finish();

        let reopened = Zoom::open(directory.path()).unwrap();
        assert_eq!(reopened.current(), 1.25);
        assert_eq!(reopened.change(Step::Reset, |_| true), Some(1.0));
        reopened.finish();
        let reopened = Zoom::open(directory.path()).unwrap();
        assert_eq!(reopened.current(), 1.0);
        reopened.finish();
    }

    #[test]
    fn unchanged_zoom_does_not_write() {
        let directory = tempfile::tempdir().unwrap();
        let zoom = Zoom::open(directory.path()).unwrap();
        assert_eq!(
            zoom.change(Step::Reset, |_| panic!("already at actual size")),
            None
        );
        zoom.finish();
        let database = Connection::open(directory.path().join("page-zoom.sqlite")).unwrap();
        let rows: u64 = database
            .query_row("SELECT count(*) FROM zoom", [], |row| row.get(0))
            .unwrap();
        assert_eq!(rows, 0);
    }

    #[test]
    fn steps_walk_the_ladder_and_clamp() {
        assert_eq!(next(1.0, Step::In), 1.1);
        assert_eq!(next(1.0, Step::Out), 0.9);
        assert_eq!(next(3.0, Step::In), 3.0);
        assert_eq!(next(0.5, Step::Out), 0.5);
        assert_eq!(next(2.5, Step::Reset), 1.0);
        // An off-ladder value snaps to the neighbouring level.
        assert_eq!(next(1.2, Step::In), 1.25);
        assert_eq!(next(1.2, Step::Out), 1.1);
    }

    #[test]
    fn menu_ids_map_to_steps() {
        assert_eq!(Step::from_menu_id("zoom_in"), Some(Step::In));
        assert_eq!(Step::from_menu_id("zoom_in_shifted"), Some(Step::In));
        assert_eq!(Step::from_menu_id("zoom_out"), Some(Step::Out));
        assert_eq!(Step::from_menu_id("zoom_reset"), Some(Step::Reset));
        assert_eq!(Step::from_menu_id("retry"), None);
    }
}
