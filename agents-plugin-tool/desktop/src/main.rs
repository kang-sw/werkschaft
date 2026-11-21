#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

#[cfg(not(any(target_os = "macos", target_os = "windows")))]
compile_error!("Werkschaft supports macOS and Windows");

#[cfg(any(windows, test))]
mod accelerator;
mod close;
mod daemon;
mod daemon_package;
mod endpoint;
#[cfg(target_os = "macos")]
mod macos;
mod navigation;
mod python;
mod shell;
mod updates;
#[cfg(windows)]
mod windows;
mod zoom;

use std::sync::{
    atomic::{AtomicUsize, Ordering},
    Arc,
};

use tauri::{
    menu::{Menu, MenuItem, PredefinedMenuItem, Submenu},
    webview::NewWindowResponse,
    AppHandle, Manager, RunEvent, WebviewUrl, WebviewWindow, WebviewWindowBuilder, WindowEvent,
};

use daemon::Intent;
use navigation::{destination, popup, Destination, Popup};
use shell::Connection;
use url::Url;
use zoom::Zoom;

#[cfg(target_os = "macos")]
const USER_AGENT: &str =
    "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Werkschaft/";

fn build_window(app: &AppHandle, connection: Arc<Connection>) -> tauri::Result<()> {
    build_webview(app, close::MAIN_WINDOW, connection, None)
}

fn build_webview(
    app: &AppHandle,
    label: &str,
    connection: Arc<Connection>,
    target: Option<(Url, Url)>,
) -> tauri::Result<()> {
    let main = close::checks_draft(label);
    let state = connection.clone();
    let notice = connection.clone();
    let popup_app = app.clone();
    let title = if main {
        "Werkschaft — Connecting…"
    } else {
        "Werkschaft"
    };
    let builder = WebviewWindowBuilder::new(app, label, WebviewUrl::App("index.html".into()))
        .title(title)
        .visible(main)
        .inner_size(1280.0, 860.0)
        .min_inner_size(760.0, 520.0)
        .disable_drag_drop_handler()
        .devtools(true)
        .on_page_load(move |window, payload| {
            if payload.event() == tauri::webview::PageLoadEvent::Finished {
                let _ = window.set_zoom(window.state::<Zoom>().current());
                if main
                    && destination(payload.url(), notice.pinned_url().as_ref())
                        == Destination::Dashboard
                {
                    notice.show_refusal(&window);
                    updates::inject(&window, payload.url());
                }
            }
        })
        .on_new_window(move |url, _features| {
            let pin = state.pinned_url();
            match popup(&url, pin.as_ref()) {
                Popup::Secondary => {
                    if let Some(pin) = pin {
                        open_secondary(popup_app.clone(), state.clone(), pin, url);
                    }
                }
                Popup::Browser => navigation::open_browser(url),
                Popup::Block => (),
            }
            NewWindowResponse::Deny
        });

    #[cfg(target_os = "macos")]
    let window = builder
        .user_agent(&format!("{USER_AGENT}{}", app.package_info().version))
        .build()?;

    #[cfg(windows)]
    let window = {
        let state = connection.clone();
        builder
            .on_navigation(
                move |url| match destination(url, state.pinned_url().as_ref()) {
                    Destination::Dashboard | Destination::Local => true,
                    Destination::Browser => {
                        navigation::open_browser(url.clone());
                        false
                    }
                    Destination::Block => false,
                },
            )
            .build()?
    };

    let _ = window.set_zoom(app.state::<Zoom>().current());
    if let Err(error) = install_webview(&window, connection.clone(), target) {
        webview_setup_failed(&window, &connection);
        return Err(error);
    }
    Ok(())
}

fn webview_setup_failed(window: &WebviewWindow, connection: &Connection) {
    if close::checks_draft(window.label()) {
        connection.setup_failed(window);
    } else {
        let _ = window.destroy();
    }
}

fn install_webview(
    window: &WebviewWindow,
    connection: Arc<Connection>,
    target: Option<(Url, Url)>,
) -> tauri::Result<()> {
    let state = connection.clone();
    let ready = move |window: WebviewWindow| {
        let Some((pin, url)) = target else {
            state.start(window);
            return;
        };
        // Both native guards are installed before the secondary can load a page.
        if state.pinned_url().as_ref() == Some(&pin) && window.navigate(url).is_ok() {
            let _ = window.show();
            let _ = window.set_focus();
        } else {
            let _ = window.destroy();
        }
    };
    #[cfg(target_os = "macos")]
    return macos::install(window, connection, ready);
    #[cfg(windows)]
    return windows::install(window, connection, ready);
}

fn open_secondary(app: AppHandle, state: Arc<Connection>, pin: Url, url: Url) {
    static NEXT_WINDOW: AtomicUsize = AtomicUsize::new(1);
    // Keep the renderer popup denied and give the target a guarded native window
    // without a JavaScript opener. Build after the native callback returns.
    std::thread::spawn(move || {
        let handle = app.clone();
        let _ = app.run_on_main_thread(move || {
            if state.pinned_url().as_ref() == Some(&pin) {
                let number = NEXT_WINDOW.fetch_add(1, Ordering::Relaxed);
                if build_webview(&handle, &format!("secondary-{number}"), state, Some((pin, url)))
                    .is_err()
                {
                    eprintln!("Werkschaft: Could not open the secondary window");
                }
            }
        });
    });
}

fn restore_main(app: &AppHandle, connection: &Arc<Connection>) {
    if let Some(window) = app.get_webview_window(close::MAIN_WINDOW) {
        let _ = window.show();
        let _ = window.unminimize();
        let _ = window.set_focus();
    } else if !connection.rebuilding.load(Ordering::Acquire) {
        if build_window(app, connection.clone()).is_err() {
            eprintln!("Werkschaft: Could not reopen the dashboard");
        }
    }
}

fn install_menu(app: &AppHandle) -> tauri::Result<()> {
    let menu = Menu::default(app)?;
    let retry = MenuItem::with_id(app, "retry", "Retry", true, Some("CmdOrCtrl+R"))?;
    let replace = MenuItem::with_id(
        app,
        "replace_daemon",
        "Replace daemon (same core)",
        true,
        None::<&str>,
    )?;
    let copy = MenuItem::with_id(
        app,
        "copy_login",
        "Copy Browser Login Link to Clipboard",
        true,
        None::<&str>,
    )?;
    menu.append(&Submenu::with_items(
        app,
        "Connection",
        true,
        &[&retry, &replace, &copy],
    )?)?;

    #[cfg(target_os = "macos")]
    let (devtools, accelerator) = ("Toggle Developer Tools", "Cmd+Alt+I");
    #[cfg(windows)]
    let (devtools, accelerator) = ("Open Developer Tools", "Ctrl+Shift+I");
    let view = menu.items()?.iter().find_map(|item| {
        item.as_submenu()
            .filter(|submenu| submenu.text().ok().as_deref() == Some("View"))
            .cloned()
    });
    let view = match view {
        Some(view) => view,
        None => {
            let view = Submenu::new(app, "View", true)?;
            menu.append(&view)?;
            view
        }
    };
    view.append_items(&[
        &MenuItem::with_id(app, "devtools", devtools, true, Some(accelerator))?,
        &MenuItem::with_id(app, "devtools_f12", devtools, true, Some("F12"))?,
        &PredefinedMenuItem::separator(app)?,
        &MenuItem::with_id(app, "zoom_reset", "Actual Size", true, Some("CmdOrCtrl+0"))?,
        &MenuItem::with_id(app, "zoom_in", "Zoom In", true, Some("CmdOrCtrl+="))?,
        &MenuItem::with_id(
            app,
            "zoom_in_shifted",
            "Zoom In",
            true,
            Some("CmdOrCtrl+Shift+="),
        )?,
        &MenuItem::with_id(app, "zoom_out", "Zoom Out", true, Some("CmdOrCtrl+-"))?,
    ])?;
    app.set_menu(menu)?;
    Ok(())
}

fn menu_action(window: WebviewWindow, state: &Arc<Connection>, id: &str) {
    let window = if matches!(id, "retry" | "replace_daemon" | "copy_login") {
        let Some(main) = window.app_handle().get_webview_window(close::MAIN_WINDOW) else {
            if id == "retry" {
                restore_main(window.app_handle(), state);
            }
            return;
        };
        main
    } else {
        window
    };
    match id {
        "retry" if state.retry_hooks() => {
            #[cfg(target_os = "macos")]
            if install_webview(&window, state.clone(), None).is_err() {
                state.setup_failed(&window);
            }
            #[cfg(windows)]
            state.webview_failed(window);
        }
        "retry" => state.connect(window, Intent::Connect),
        "replace_daemon" => state.connect(window, Intent::Replace),
        "copy_login" => state.copy_login_link(window),
        "devtools" | "devtools_f12" => {
            #[cfg(target_os = "macos")]
            if window.is_devtools_open() {
                window.close_devtools();
                return;
            }
            window.open_devtools();
        }
        _ => {
            if let Some(step) = zoom::Step::from_menu_id(id) {
                if let Some(level) = window
                    .state::<Zoom>()
                    .change(step, |level| window.set_zoom(level).is_ok())
                {
                    for other in window.app_handle().webview_windows().into_values() {
                        if other.label() != window.label() {
                            let _ = other.set_zoom(level);
                        }
                    }
                }
            }
        }
    }
}

fn main() {
    let state = Arc::new(Connection::default());
    let menu_state = state.clone();
    let setup_state = state.clone();
    let instance_state = state.clone();
    tauri::Builder::default()
        .plugin(tauri_plugin_single_instance::init(move |app, _args, _cwd| {
            let target = app.clone();
            let state = instance_state.clone();
            let _ = app.run_on_main_thread(move || restore_main(&target, &state));
        }))
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_updater::Builder::new().build())
        .on_menu_event(move |app, event| {
            let window = app
                .webview_windows()
                .into_values()
                .find(|window| window.is_focused().unwrap_or(false))
                .or_else(|| app.get_webview_window(close::MAIN_WINDOW));
            if let Some(window) = window {
                menu_action(window, &menu_state, event.id().as_ref());
            }
        })
        .setup(move |app| {
            let zoom = app
                .path()
                .app_local_data_dir()
                .map_err(|error| -> Box<dyn std::error::Error> { error.into() })
                .and_then(|directory| Zoom::open(&directory))
                .unwrap_or_else(|error| {
                    eprintln!("Werkschaft: Could not restore the page zoom: {error}");
                    Zoom::default()
                });
            app.manage(zoom);
            install_menu(app.handle())?;
            updates::Controller::start(app.handle(), setup_state.clone());
            build_window(app.handle(), setup_state.clone())?;
            setup_state.watch(app.handle().clone());
            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("Unable to initialize Werkschaft")
        .run(move |app, event| match event {
            RunEvent::WindowEvent {
                label,
                event: WindowEvent::CloseRequested { api, .. },
                ..
            } if close::checks_draft(&label) => {
                if let Some(window) = app.get_webview_window(&label) {
                    api.prevent_close();
                    close::request(window, state.clone());
                }
            }
            RunEvent::ExitRequested { api, .. } if state.rebuilding.load(Ordering::Acquire) => {
                api.prevent_exit();
            }
            RunEvent::Exit => app.state::<Zoom>().finish(),
            #[cfg(target_os = "macos")]
            RunEvent::Reopen { has_visible_windows, .. }
                if !has_visible_windows || app.get_webview_window(close::MAIN_WINDOW).is_none() =>
            {
                restore_main(app, &state);
            }
            #[cfg(windows)]
            RunEvent::WindowEvent {
                label,
                event: WindowEvent::Destroyed,
                ..
            } if close::checks_draft(&label) && state.rebuilding.load(Ordering::Acquire) => {
                let app = app.clone();
                let state = state.clone();
                // Defer until Tauri removes the destroyed window from its manager.
                std::thread::spawn(move || {
                    let target = app.clone();
                    let _ = app.run_on_main_thread(move || {
                        state.rebuilding.store(false, Ordering::Release);
                        if build_window(&target, state).is_err() {
                            eprintln!("Werkschaft: Could not recreate the webview");
                            target.exit(1);
                        }
                    });
                });
            }
            RunEvent::WindowEvent {
                label,
                event: WindowEvent::Destroyed,
                ..
            } if close::checks_draft(&label) => state.main_window_closed(),
            _ => (),
        });
}

#[cfg(test)]
mod tests {
    #[test]
    fn pages_receive_no_native_command_permissions() {
        let config: serde_json::Value =
            serde_json::from_str(include_str!("../tauri.conf.json")).unwrap();
        let app = &config["app"];
        assert_eq!(app["withGlobalTauri"], false);
        let capabilities = app["security"]["capabilities"].as_array().unwrap();
        assert_eq!(capabilities.len(), 1);
        assert_eq!(capabilities[0]["local"], false);
        assert!(capabilities[0].get("remote").is_none());
        assert!(capabilities[0]["permissions"]
            .as_array()
            .unwrap()
            .is_empty());
    }
}
