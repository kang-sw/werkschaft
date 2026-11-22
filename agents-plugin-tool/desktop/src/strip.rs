// The Windows title strip.
//
// The main window has no native title bar or menu bar on Windows. A bundled
// page (ui/strip.html) in its own 34px webview across the top of the window
// draws the icon, the title, the menu labels and the caption buttons, and the
// dashboard webview fills the rest. This module builds that webview, pushes
// window state into it, and answers its few commands. The page asks; the shell
// acts, and only this webview may ask (see `from_strip` and the title-strip
// capability in tauri.conf.json).

use std::sync::{mpsc, Arc, Mutex};

use serde_json::json;
use tauri::{
    menu::{ContextMenu, Submenu},
    webview::{NewWindowResponse, PageLoadEvent},
    window::Color,
    AppHandle, LogicalPosition, LogicalSize, Manager, Theme as OsTheme, Webview, WebviewBuilder,
    WebviewUrl, WebviewWindow, Window,
};
use url::Url;
use webview2_com::Microsoft::Web::WebView2::Win32::*;
use windows::{core::Interface, Win32::Foundation::HWND};

use crate::{
    accelerator, chrome, close,
    navigation::{destination, Destination},
    shell::Connection,
    snap,
    strip_msg::{self, State},
    windows::{install_accelerators, queue},
};

pub const LABEL: &str = "main-strip";
const PAGE: &str = "/strip.html";

/// What the strip page shows, and the way to hand it changes. Everything that
/// reaches the page goes through one channel and one worker, so the UI thread
/// never evaluates script from inside a window procedure.
struct Shared {
    state: Mutex<State>,
    to_page: mpsc::Sender<String>,
}

impl Shared {
    fn update(&self, change: impl FnOnce(&mut State)) {
        let mut state = self.state.lock().unwrap();
        let before = state.clone();
        change(&mut state);
        if *state != before {
            let patch = patch_between(&before, &state);
            let _ = self.to_page.send(strip_msg::set_script(&patch));
        }
    }

    fn patch(&self, patch: serde_json::Value) {
        let _ = self.to_page.send(strip_msg::set_script(&patch));
    }

    fn push_all(&self) {
        let state = self.state.lock().unwrap().json();
        let _ = self.to_page.send(strip_msg::set_script(&state));
    }
}

fn patch_between(before: &State, after: &State) -> serde_json::Value {
    let (old, new) = (before.json(), after.json());
    let mut patch = serde_json::Map::new();
    for (key, value) in new.as_object().into_iter().flatten() {
        if old.get(key) != Some(value) {
            patch.insert(key.clone(), value.clone());
        }
    }
    patch.into()
}

/// The current main window's strip, if it has one.
#[derive(Default)]
pub struct Strips(Mutex<Option<Arc<Shared>>>);

impl Strips {
    fn current(&self) -> Option<Arc<Shared>> {
        self.0.lock().unwrap().clone()
    }
}

const THEME_BRIDGE: &str = include_str!("theme-bridge.js");

/// Installs the theme bridge in the main window's pinned dashboard page.
pub fn inject_theme_bridge(window: &WebviewWindow, source: &Url, pin: Option<&Url>) {
    if !close::checks_draft(window.label()) || destination(source, pin) != Destination::Dashboard {
        return;
    }
    let origin = json!(source.origin().ascii_serialization());
    let _ = window.eval(format!(
        "(() => {{ {THEME_BRIDGE}\ninstallWerkschaftThemeBridge({origin}); }})()"
    ));
}

pub fn title_changed(window: &WebviewWindow, title: &str) {
    if !close::checks_draft(window.label()) {
        return;
    }
    if let Some(strip) = window.app_handle().state::<Strips>().current() {
        strip.update(|state| state.title = title.to_owned());
    }
}

/// Handles a theme message from the main window's top frame at `source`. The
/// message is trusted only from the pinned dashboard origin and only in the
/// window generation it was posted for.
pub fn receive_theme(
    window: &WebviewWindow,
    connection: &Connection,
    epoch: usize,
    source: &Url,
    message: &str,
) {
    let app = window.app_handle();
    if !close::checks_draft(window.label())
        || connection.window_epoch() != epoch
        || destination(source, connection.pinned_url().as_ref()) != Destination::Dashboard
    {
        return;
    }
    let Some(theme) = strip_msg::parse_theme(message) else {
        return;
    };
    if let Some(strip) = app.state::<Strips>().current() {
        strip.update(|state| state.theme = Some(theme));
    }
}

/// Whether `window` is the main window and its strip stands in for the menu bar.
pub fn active(app: &AppHandle, window: &WebviewWindow) -> bool {
    close::checks_draft(window.label()) && app.state::<Strips>().current().is_some()
}

/// Opens a menu from the keyboard: the page measures its label and asks for
/// the popup like a click does.
pub fn open_menu(app: &AppHandle, menu: &str) {
    if let Some(strip) = app.state::<Strips>().current() {
        let _ = strip.to_page.send(strip_msg::open_script(menu));
    }
}

fn page_url() -> Url {
    crate::navigation::local_view(PAGE)
}

// Both the dashboard's controller and the strip's are needed in one place, and
// each is only reachable inside its own `with_webview` closure. Both closures
// run on the UI thread, in order, and the controller never leaves it.
struct UiThread<T>(T);
unsafe impl<T> Send for UiThread<T> {}

/// Builds the strip into `window`, the dashboard's window, and takes over its
/// layout. On error the window gets its native title bar and menu back.
pub fn attach(
    app: &AppHandle,
    window: &WebviewWindow,
    connection: Arc<Connection>,
) -> tauri::Result<()> {
    let result = build(app, window, connection);
    if result.is_err() {
        fallback(window);
    }
    result
}

/// The system title bar and menu bar again, with the dashboard following the
/// window's size on its own.
fn fallback(window: &WebviewWindow) {
    eprintln!("Werkschaft: Using the system title bar");
    let _ = window.set_decorations(true);
    let _ = window.show_menu();
    let dashboard = AsRef::<Webview>::as_ref(window);
    if let Ok(size) = window.inner_size() {
        let _ = dashboard.set_bounds(tauri::Rect {
            position: tauri::PhysicalPosition::new(0, 0).into(),
            size: size.into(),
        });
    }
    let _ = dashboard.set_auto_resize(true);
}

fn build(
    app: &AppHandle,
    window: &WebviewWindow,
    connection: Arc<Connection>,
) -> tauri::Result<()> {
    // With `unstable`, Tauri keeps the dashboard webview at the full client
    // area and resets it on every Resized event, which would put it under the
    // strip. From here on only `snap::layout` places it. `fallback` hands the
    // sizing back to Tauri.
    AsRef::<Webview>::as_ref(window).set_auto_resize(false)?;
    let size = window.inner_size()?;
    let logical = size.to_logical::<f64>(window.scale_factor()?);
    let (to_page, from_shell) = mpsc::channel();
    let shared = Arc::new(Shared {
        state: Mutex::new(State {
            title: window.title()?,
            ..State::default()
        }),
        to_page,
    });
    let loaded = shared.clone();
    let background = match window.theme()? {
        OsTheme::Dark => Color(20, 21, 24, 255),
        _ => Color(243, 243, 240, 255),
    };
    let builder = WebviewBuilder::new(LABEL, WebviewUrl::App(PAGE.trim_start_matches('/').into()))
        .devtools(false)
        .disable_drag_drop_handler()
        .background_color(background)
        .on_navigation(|url| *url == page_url())
        .on_new_window(|_, _| NewWindowResponse::Deny)
        .on_page_load(move |_, payload| {
            if payload.event() == PageLoadEvent::Finished {
                loaded.push_all();
            }
        });
    let top: Window = AsRef::<Webview>::as_ref(window).window();
    let webview = top.add_child(
        builder,
        LogicalPosition::new(0.0, 0.0),
        LogicalSize::new(logical.width, snap::STRIP_HEIGHT),
    )?;

    let worker = webview.clone();
    std::thread::spawn(move || {
        for script in from_shell {
            let _ = worker.eval(script);
        }
    });

    let dashboard = Arc::new(Mutex::new(None));
    let stash = dashboard.clone();
    window.with_webview(move |platform| {
        *stash.lock().unwrap() = Some(UiThread(platform.controller()));
    })?;
    let owner = window.clone();
    let events = shared.clone();
    let hwnd = window.hwnd()?.0 as isize;
    webview.with_webview(move |platform| {
        let Some(UiThread(dashboard)) = dashboard.lock().unwrap().take() else {
            return;
        };
        let strip = platform.controller();
        unsafe {
            if let Err(error) = harden(&strip) {
                eprintln!("Werkschaft: Could not configure the title strip ({error})");
            }
            if let Err(error) = install_accelerators(&strip, owner.clone(), connection) {
                eprintln!("Werkschaft: Could not install the title strip's keys ({error})");
            }
            let top = HWND(hwnd as _);
            let layout = snap::install(top, dashboard, strip, move |event| match event {
                snap::Event::Active(focused) => events.update(|state| state.focused = focused),
                snap::Event::Maximized(maximized) => {
                    events.update(|state| state.maximized = maximized)
                }
                snap::Event::MaxHover(hover) => events.patch(json!({ "maxHover": hover })),
                snap::Event::MaxPressed(pressed) => events.patch(json!({ "maxPressed": pressed })),
            });
            if let Err(error) = layout {
                eprintln!("Werkschaft: Could not lay out the title strip ({error})");
                // Never inside this callback: the window's state is borrowed.
                queue(&owner, |window| fallback(&window));
            }
        }
    })?;
    window.hide_menu()?;
    *app.state::<Strips>().0.lock().unwrap() = Some(shared);
    Ok(())
}

// The strip page is a fixed bundled document: no context menu, browser keys,
// zoom, swipe or status bar.
unsafe fn harden(controller: &ICoreWebView2Controller) -> windows::core::Result<()> {
    let settings = controller.CoreWebView2()?.Settings()?;
    settings.SetAreDefaultContextMenusEnabled(false)?;
    settings.SetIsZoomControlEnabled(false)?;
    settings.SetIsStatusBarEnabled(false)?;
    settings
        .cast::<ICoreWebView2Settings3>()?
        .SetAreBrowserAcceleratorKeysEnabled(false)?;
    settings
        .cast::<ICoreWebView2Settings5>()?
        .SetIsPinchZoomEnabled(false)?;
    settings
        .cast::<ICoreWebView2Settings6>()?
        .SetIsSwipeNavigationEnabled(false)?;
    Ok(())
}

// Only the strip's own webview may call these. The capability already grants
// them to it alone; the label check keeps that true if a capability changes.
fn from_strip(webview: &Webview) -> Result<WebviewWindow, String> {
    if webview.label() != LABEL {
        return Err("not allowed".into());
    }
    chrome::get(webview.app_handle(), close::MAIN_WINDOW).ok_or_else(|| "no window".into())
}

fn dashboard_webview(main: &WebviewWindow) -> &Webview {
    AsRef::<Webview>::as_ref(main)
}

fn submenu(app: &AppHandle, name: &str) -> Option<Submenu<tauri::Wry>> {
    if !accelerator::MENUS.contains(&name) {
        return None;
    }
    app.menu()?.items().ok()?.into_iter().find_map(|item| {
        item.as_submenu()
            .filter(|submenu| submenu.text().ok().as_deref() == Some(name))
            .cloned()
    })
}

/// Pops a menu at the strip's label: `x` and `y` are logical pixels in the
/// window. Returns when the menu closes. An async command, so the modal menu
/// loop runs on the UI thread and not inside the page's message.
#[tauri::command(async)]
pub fn strip_popup(webview: Webview, menu: String, x: f64, y: f64) -> Result<(), String> {
    let main = from_strip(&webview)?;
    let within = |v: f64| v.is_finite() && (0.0..=16384.0).contains(&v);
    if !within(x) || !within(y) {
        return Err("bad position".into());
    }
    let submenu = submenu(webview.app_handle(), &menu).ok_or("unknown menu")?;
    // The Edit items act on whatever has the keyboard focus, so that is the
    // dashboard while the menu is open and after it closes.
    let _ = dashboard_webview(&main).set_focus();
    let window: Window = AsRef::<Webview>::as_ref(&main).window();
    let shown = submenu
        .popup_at(window, LogicalPosition::new(x, y))
        .map_err(|error| error.to_string());
    let _ = dashboard_webview(&main).set_focus();
    shown
}

#[tauri::command(async)]
pub fn strip_minimize(webview: Webview) -> Result<(), String> {
    let main = from_strip(&webview)?;
    let _ = dashboard_webview(&main).set_focus();
    main.minimize().map_err(|error| error.to_string())
}

#[tauri::command(async)]
pub fn strip_toggle_maximize(webview: Webview) -> Result<(), String> {
    let main = from_strip(&webview)?;
    let _ = dashboard_webview(&main).set_focus();
    let maximized = main.is_maximized().map_err(|error| error.to_string())?;
    if maximized {
        main.unmaximize()
    } else {
        main.maximize()
    }
    .map_err(|error| error.to_string())
}

/// The close button takes the window's usual close path, which hides the app
/// to the tray.
#[tauri::command(async)]
pub fn strip_close(webview: Webview) -> Result<(), String> {
    from_strip(&webview)?
        .close()
        .map_err(|error| error.to_string())
}

#[tauri::command(async)]
pub fn strip_drag(webview: Webview) -> Result<(), String> {
    from_strip(&webview)?
        .start_dragging()
        .map_err(|error| error.to_string())
}

pub fn manage(app: &AppHandle) {
    app.manage(Strips::default());
}
