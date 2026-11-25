//! The bundled recovery page's native side: the diagnostics it shows, sent
//! by evaluation so its URL stays exactly `/index.html`, and its three fixed
//! actions. The page reaches them through the same transports and gate as
//! the update bridge (see `updates::receive`).

use std::sync::Arc;

use serde_json::{json, Value};
use tauri::WebviewWindow;

use crate::{
    daemon::Intent,
    diagnostics::{redact, Diagnostics},
    navigation::{local_view, CONNECT_VIEW, LOCAL_ORIGIN},
    shell::Connection,
    updates::Recovery,
};

fn on_page(window: &WebviewWindow, connection: &Connection) -> bool {
    connection.pinned_url().is_none()
        && window
            .url()
            .is_ok_and(|url| url == local_view(CONNECT_VIEW))
}

fn call(window: &WebviewWindow, function: &str, argument: &Value) {
    let origin = json!(LOCAL_ORIGIN);
    let path = json!(CONNECT_VIEW);
    let _ = window.eval(format!(
        "if (window === window.top && location.origin === {origin} && location.pathname === {path}) window.{function}?.({argument});"
    ));
}

/// Sends the recorded failure to the recovery page, or `null` while an
/// attempt runs, when that page is what the window shows.
pub fn push(window: &WebviewWindow, connection: &Connection) {
    if !on_page(window, connection) {
        return;
    }
    let payload = connection
        .failure()
        .and_then(|diagnostics| serde_json::to_value(diagnostics).ok())
        .unwrap_or(Value::Null);
    call(window, "__werkschaftRecovery", &payload);
}

fn notice(window: &WebviewWindow, text: &str) {
    call(window, "__werkschaftRecoveryNotice", &json!(text));
}

pub fn run(window: WebviewWindow, connection: Arc<Connection>, action: Recovery) {
    match action {
        Recovery::Retry => {
            crate::shell_log!("Retry from the recovery page");
            // Leave the webview's message callback before navigating.
            std::thread::spawn(move || {
                let target = window.clone();
                let _ = window
                    .run_on_main_thread(move || connection.connect(target, Intent::Connect));
            });
        }
        Recovery::OpenLogs => {
            let Some(directory) = crate::shell_log::directory() else {
                notice(&window, "The log folder is not available.");
                return;
            };
            std::thread::spawn(move || {
                if open::that(&directory).is_err() {
                    crate::shell_log!("Could not open the log folder");
                }
            });
        }
        Recovery::CopyDiagnostics => {
            let report = redact(
                &connection
                    .failure()
                    .unwrap_or_else(|| Diagnostics::reason("No failure is recorded"))
                    .report(),
            );
            let copied = copy(&window, &report);
            notice(
                &window,
                if copied {
                    "Diagnostics copied to the clipboard"
                } else {
                    "Could not copy the diagnostics"
                },
            );
        }
    }
}

fn copy(window: &WebviewWindow, text: &str) -> bool {
    #[cfg(windows)]
    return crate::windows::copy_text(window, text);
    #[cfg(target_os = "macos")]
    {
        let _ = window;
        arboard::Clipboard::new()
            .and_then(|mut clipboard| clipboard.set_text(text))
            .is_ok()
    }
}
