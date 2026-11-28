// The window set the shell knows, and the title strip's hook into titles.
//
// On macOS a window is one `WebviewWindow` and Tauri finds it by label. A
// Windows main window also carries the title strip's webview, and Tauri stops
// treating a window with two webviews as a webview window: `get_webview_window`
// and `webview_windows` no longer return it. The shell therefore keeps its own
// registry of the dashboard windows it builds and every lookup goes through
// here, so the dashboard webview stays what each caller means by "the window".

use tauri::WebviewWindow;

#[cfg(target_os = "macos")]
pub fn get(app: &tauri::AppHandle, label: &str) -> Option<WebviewWindow> {
    use tauri::Manager;
    app.get_webview_window(label)
}

#[cfg(target_os = "macos")]
pub fn all(app: &tauri::AppHandle) -> Vec<WebviewWindow> {
    use tauri::Manager;
    app.webview_windows().into_values().collect()
}

/// Sets the window title, which the Windows title strip also shows.
pub fn set_title(window: &WebviewWindow, title: &str) {
    let _ = window.set_title(title);
    #[cfg(windows)]
    crate::strip::title_changed(window, title);
}

/// Runs `action` on the main thread after the caller's stack has unwound.
///
/// Tauri runs a main-thread dispatch inline when the caller already is on the
/// main thread, so a task sent from inside a native callback runs nested in
/// it. `with_webview` holds the webview's window-ID lock for the whole inline
/// call, and `url`, `eval`, `navigate` and `show` take that same non-reentrant
/// lock: shell code that touches the webview from inside the callback
/// deadlocks the main thread. Dispatching from a worker queues the task on the
/// event loop, which runs it once the callback has returned.
pub(crate) fn queue<R: tauri::Runtime>(
    window: &tauri::WebviewWindow<R>,
    action: impl FnOnce(tauri::WebviewWindow<R>) + Send + 'static,
) {
    let window = window.clone();
    std::thread::spawn(move || {
        let target = window.clone();
        if window.run_on_main_thread(move || action(target)).is_err() {
            crate::shell_log!("Could not queue a shell action");
        }
    });
}

#[cfg(windows)]
pub use registry::{all, get, register};

#[cfg(windows)]
mod registry {
    use std::{collections::HashMap, sync::Mutex};

    use tauri::{AppHandle, Manager, WebviewWindow};

    #[derive(Default)]
    pub struct Windows(Mutex<HashMap<String, WebviewWindow>>);

    /// Adds a window; it leaves the registry when destroyed, before the shell
    /// can rebuild one with the same label.
    pub fn register(app: &AppHandle, window: &WebviewWindow) {
        let windows = app.state::<Windows>();
        windows
            .0
            .lock()
            .unwrap()
            .insert(window.label().to_owned(), window.clone());
        let (app, label) = (app.clone(), window.label().to_owned());
        window.on_window_event(move |event| {
            if matches!(event, tauri::WindowEvent::Destroyed) {
                app.state::<Windows>().0.lock().unwrap().remove(&label);
                if crate::close::checks_draft(&label) {
                    crate::strip::detach(&app);
                }
            }
        });
    }

    pub fn get(app: &AppHandle, label: &str) -> Option<WebviewWindow> {
        app.state::<Windows>().0.lock().unwrap().get(label).cloned()
    }

    pub fn all(app: &AppHandle) -> Vec<WebviewWindow> {
        app.state::<Windows>()
            .0
            .lock()
            .unwrap()
            .values()
            .cloned()
            .collect()
    }

    pub fn manage(app: &AppHandle) {
        app.manage(Windows::default());
    }
}

#[cfg(windows)]
pub use registry::manage;

#[cfg(test)]
mod tests {
    use std::{sync::mpsc, thread, time::Duration};

    use tauri::{test::mock_app, webview::WebviewWindowBuilder, WebviewUrl};

    use super::queue;

    // The shell's `ready` hook runs the first connect, which reads the webview
    // URL. Run on the caller's stack inside `with_webview`, that read waits
    // on a lock the caller holds. The queued action must never run there, and
    // must still run once the caller has finished.
    #[test]
    fn queued_action_runs_after_the_caller_returns_and_never_on_its_stack() {
        let app = mock_app();
        let window = WebviewWindowBuilder::new(&app, "main", WebviewUrl::App("index.html".into()))
            .build()
            .unwrap();
        let caller = thread::current().id();
        let (sender, receiver) = mpsc::channel();

        queue(&window, move |target| {
            let url = target.url().map(|url| url.to_string());
            let _ = sender.send((thread::current().id(), url));
        });

        let (ran_on, url) = receiver
            .recv_timeout(Duration::from_secs(5))
            .expect("the queued action runs");
        assert_ne!(ran_on, caller);
        assert!(url.is_ok());
    }
}
