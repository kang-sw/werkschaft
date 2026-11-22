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
