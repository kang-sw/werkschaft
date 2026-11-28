#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

#[cfg(not(any(target_os = "macos", target_os = "windows")))]
compile_error!("Werkschaft supports macOS and Windows");

#[cfg(any(windows, test))]
mod accelerator;
mod capture;
mod chrome;
mod close;
mod daemon;
mod daemon_package;
mod diagnostics;
mod downloads;
mod endpoint;
#[cfg(all(test, unix))]
mod full_stop_e2e;
mod lkg;
#[cfg(target_os = "macos")]
mod macos;
mod navigation;
mod notify;
mod permissions;
mod python;
mod recovery;
mod shell;
mod shell_log;
#[cfg(windows)]
mod snap;
mod speech;
#[cfg(windows)]
mod strip;
#[cfg(any(windows, test))]
mod strip_msg;
mod tray;
mod updates;
#[cfg(windows)]
mod windows;
#[cfg(windows)]
mod windows_speech;
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
            if main && payload.event() == tauri::webview::PageLoadEvent::Started {
                speech::teardown(window.app_handle(), "navigation");
            }
            if payload.event() == tauri::webview::PageLoadEvent::Finished {
                let _ = window.set_zoom(window.state::<Zoom>().current());
                let pin = notice.pinned_url();
                let dashboard = destination(payload.url(), pin.as_ref()) == Destination::Dashboard;
                let recovery = updates::authorized(true, payload.url(), pin.as_ref())
                    == Some(updates::Origin::Local);
                if main && dashboard {
                    // The bridge first: the fallback notice offers its check.
                    updates::inject(&window, payload.url());
                    notice.show_refusal(&window);
                    notify::inject(&window, payload.url());
                    capture::inject(&window, payload.url());
                    speech::inject(&window, payload.url());
                    #[cfg(windows)]
                    strip::inject_theme_bridge(
                        &window,
                        payload.url(),
                        notice.pinned_url().as_ref(),
                    );
                } else if dashboard {
                    notify::mark_secondary(&window);
                } else if main && recovery {
                    updates::inject(&window, payload.url());
                    recovery::push(&window, &notice);
                }
            }
        })
        .on_download(downloads::handler(connection.clone()))
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
        // The main window draws its own title strip; see strip.rs. A window's
        // webview is a child webview here, so another window has it follow the
        // window's size.
        let builder = if main {
            builder.decorations(false)
        } else {
            builder.auto_resize()
        };
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

    #[cfg(windows)]
    {
        chrome::register(app, &window);
        if main {
            if let Err(error) = strip::attach(app, &window, connection.clone()) {
                eprintln!("Werkschaft: Could not build the title strip ({error}); using the system title bar");
            }
        }
    }
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
    // Let Tauri queue native creation and with_webview from this worker.
    // On the UI thread with_webview runs inline with its window ID locked,
    // and the macOS setup callback navigates using that same lock.
    std::thread::spawn(move || {
        if state.pinned_url().as_ref() == Some(&pin) {
            let number = NEXT_WINDOW.fetch_add(1, Ordering::Relaxed);
            let label = format!("secondary-{number}");
            if build_webview(&app, &label, state, Some((pin, url))).is_err() {
                eprintln!("Werkschaft: Could not open the secondary window");
            }
        }
    });
}

fn restore_main(app: &AppHandle, connection: &Arc<Connection>) {
    if let Some(window) = chrome::get(app, close::MAIN_WINDOW) {
        #[cfg(target_os = "macos")]
        macos::keep_shown();
        let _ = window.show();
        let _ = window.unminimize();
        let _ = window.set_focus();
    } else if !connection.rebuilding.load(Ordering::Acquire) {
        if build_window(app, connection.clone()).is_err() {
            eprintln!("Werkschaft: Could not reopen the dashboard");
        }
    }
}

const CHECK_UPDATES: &str = "check_updates";

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
    // Reachable when the dashboard cannot load: the label becomes
    // "Restart to Update (vX.Y.Z)" once an update is ready.
    let update = MenuItem::with_id(
        app,
        CHECK_UPDATES,
        updates::CHECK_LABEL,
        true,
        None::<&str>,
    )?;
    updates::UpdateItems::add(app, update.clone());
    menu.append(&Submenu::with_items(
        app,
        "Connection",
        true,
        &[
            &retry,
            &replace,
            &copy,
            &PredefinedMenuItem::separator(app)?,
            &update,
        ],
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
        let Some(main) = chrome::get(window.app_handle(), close::MAIN_WINDOW) else {
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
        #[cfg(windows)]
        "minimize" => {
            speech::teardown(window.app_handle(), "minimized");
            let _ = window.minimize();
        }
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
                    for other in chrome::all(window.app_handle()) {
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
    let builder = tauri::Builder::default();
    #[cfg(windows)]
    let builder = builder.invoke_handler(tauri::generate_handler![
        strip::strip_popup,
        strip::strip_minimize,
        strip::strip_toggle_maximize,
        strip::strip_close,
        strip::strip_drag,
    ]);
    builder
        .plugin(tauri_plugin_single_instance::init(
            move |app, _args, _cwd| {
                let target = app.clone();
                let state = instance_state.clone();
                let _ = app.run_on_main_thread(move || restore_main(&target, &state));
            },
        ))
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_updater::Builder::new().build())
        .on_menu_event(move |app, event| {
            match tray::action(event.id().as_ref()) {
                Some(tray::Action::Show) => return restore_main(app, &menu_state),
                Some(tray::Action::Exit(kind)) => {
                    return close::exit(app.clone(), menu_state.clone(), kind)
                }
                Some(tray::Action::CheckUpdates) => return updates::menu_check(app),
                None if event.id().as_ref() == CHECK_UPDATES => return updates::menu_check(app),
                None => (),
            }
            let window = chrome::all(app)
                .into_iter()
                .find(|window| window.is_focused().unwrap_or(false))
                .or_else(|| chrome::get(app, close::MAIN_WINDOW));
            if let Some(window) = window {
                menu_action(window, &menu_state, event.id().as_ref());
            }
        })
        .setup(move |app| {
            if let Ok(directory) = app.path().app_local_data_dir() {
                shell_log::init(&directory.join("logs"));
            }
            shell_log!("Werkschaft {} starting", app.package_info().version);
            app.manage(setup_state.clone());
            app.manage(updates::UpdateItems::<tauri::Wry>::default());
            let zoom = app
                .path()
                .app_local_data_dir()
                .map_err(|error| -> Box<dyn std::error::Error> { error.into() })
                .and_then(|directory| Zoom::open(&directory))
                .unwrap_or_else(|error| {
                    shell_log!("Could not restore the page zoom: {error}");
                    Zoom::default()
                });
            app.manage(zoom);
            #[cfg(windows)]
            {
                chrome::manage(app.handle());
                strip::manage(app.handle());
            }
            install_menu(app.handle())?;
            notify::manage(app.handle(), setup_state.clone());
            capture::manage(app.handle(), setup_state.clone());
            speech::manage(app.handle(), setup_state.clone());
            let tray_state = setup_state.clone();
            tray::install(app.handle(), move |app| restore_main(app, &tray_state))?;
            updates::Controller::start(app.handle(), setup_state.clone());
            build_window(app.handle(), setup_state.clone())?;
            setup_state.watch(app.handle().clone());
            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("Unable to initialize Werkschaft")
        .run(move |app, event| match event {
            // The main window's close button hides it to the tray: no draft
            // check, since the page keeps running.
            RunEvent::WindowEvent {
                label,
                event: WindowEvent::CloseRequested { api, .. },
                ..
            } if close::checks_draft(&label) => {
                speech::teardown(app, "tray");
                if let Some(window) = chrome::get(app, &label) {
                    api.prevent_close();
                    #[cfg(target_os = "macos")]
                    macos::hide_to_tray(&window);
                    #[cfg(not(target_os = "macos"))]
                    let _ = window.hide();
                }
            }
            RunEvent::WindowEvent {
                label,
                event: WindowEvent::Resized(_),
                ..
            } if close::checks_draft(&label) => {
                if let Some(window) = chrome::get(app, &label) {
                    speech::on_window_resized(app, &window);
                }
            }
            RunEvent::ExitRequested { code, api, .. } => {
                match close::exit_request(code, state.rebuilding.load(Ordering::Acquire)) {
                    close::ExitRequest::Proceed => speech::shutdown(app),
                    close::ExitRequest::Prevent => api.prevent_exit(),
                    close::ExitRequest::Close => {
                        api.prevent_exit();
                        close::exit(app.clone(), state.clone(), close::Exit::Close);
                    }
                }
            }
            RunEvent::Exit => {
                speech::shutdown(app);
                app.state::<Zoom>().finish();
            }
            #[cfg(target_os = "macos")]
            RunEvent::Reopen {
                has_visible_windows,
                ..
            } if !has_visible_windows || chrome::get(app, close::MAIN_WINDOW).is_none() => {
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
            } if close::checks_draft(&label) => {
                speech::shutdown(app);
                state.main_window_closed();
            }
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
        assert_eq!(capabilities.len(), 2);
        // No page but the strip has a permission: this capability covers the
        // whole main window, the dashboard included, and grants nothing.
        assert_eq!(capabilities[0]["local"], false);
        assert!(capabilities[0].get("remote").is_none());
        assert!(capabilities[0]["permissions"]
            .as_array()
            .unwrap()
            .is_empty());
    }

    // The strip page is the one bundled page with IPC. It is granted by webview
    // label, not by window (the dashboard shares the window), only the shell's
    // fixed strip commands, and no plugin or core permission.
    #[test]
    fn only_the_title_strip_webview_has_permissions() {
        let config: serde_json::Value =
            serde_json::from_str(include_str!("../tauri.conf.json")).unwrap();
        let capabilities = config["app"]["security"]["capabilities"]
            .as_array()
            .unwrap();
        let strip = &capabilities[1];
        assert_eq!(strip["identifier"], "title-strip");
        assert_eq!(strip["webviews"], serde_json::json!(["main-strip"]));
        assert!(strip.get("windows").is_none());
        assert!(strip.get("remote").is_none());
        let granted: Vec<&str> = strip["permissions"]
            .as_array()
            .unwrap()
            .iter()
            .map(|permission| permission.as_str().unwrap())
            .collect();
        let build = include_str!("../build.rs");
        for permission in &granted {
            let command = permission.strip_prefix("allow-").unwrap().replace('-', "_");
            assert!(
                build.contains(&format!("\"{command}\"")),
                "{permission} grants a command build.rs does not declare"
            );
        }
        assert_eq!(granted.len(), 5);
        // The strip page may reach only the shell's own origin for IPC.
        let csp = config["app"]["security"]["csp"].as_str().unwrap();
        assert!(csp.contains("default-src 'none'"));
        assert!(csp.contains("connect-src ipc: http://ipc.localhost;"));
        assert!(!csp.contains("unsafe-"));
    }
}
