use tauri::{
    menu::{Menu, MenuItem, PredefinedMenuItem},
    tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent},
    AppHandle,
};

use crate::{close::Exit, updates};

// The tray menu's items. Their events reach the app's menu handler, which
// acts on them without a focused window.
pub const SHOW: &str = "tray_show";
pub const CHECK_UPDATES: &str = "tray_check_updates";
pub const CLOSE: &str = "tray_close";
pub const FULL_QUIT: &str = "tray_full_quit";

const SHOW_LABEL: &str = "Show Werkschaft";
const CLOSE_LABEL: &str = "Close Werkschaft";
const FULL_QUIT_LABEL: &str = "Quit Werkschaft and Stop the Background…";

/// What a tray menu item asks for.
#[derive(Debug, PartialEq, Eq)]
pub enum Action {
    Show,
    /// Check for, download, or apply a shell update; labeled
    /// "Restart to Update (vX.Y.Z)" once one is ready.
    CheckUpdates,
    Exit(Exit),
}

pub fn action(id: &str) -> Option<Action> {
    match id {
        SHOW => Some(Action::Show),
        CHECK_UPDATES => Some(Action::CheckUpdates),
        CLOSE => Some(Action::Exit(Exit::Close)),
        FULL_QUIT => Some(Action::Exit(Exit::FullQuit)),
        _ => None,
    }
}

// A left click restores the window, except on macOS, where the menu bar
// convention opens the menu instead.
const MENU_ON_LEFT_CLICK: bool = cfg!(target_os = "macos");

fn restores(event: &TrayIconEvent) -> bool {
    !MENU_ON_LEFT_CLICK
        && matches!(
            event,
            TrayIconEvent::Click {
                button: MouseButton::Left,
                button_state: MouseButtonState::Up,
                ..
            }
        )
}

/// Installs the tray icon. `restore` shows and focuses the main window.
pub fn install(
    app: &AppHandle,
    restore: impl Fn(&AppHandle) + Send + Sync + 'static,
) -> tauri::Result<()> {
    let update = MenuItem::with_id(app, CHECK_UPDATES, updates::CHECK_LABEL, true, None::<&str>)?;
    updates::UpdateItems::add(app, update.clone());
    let menu = Menu::with_items(
        app,
        &[
            &MenuItem::with_id(app, SHOW, SHOW_LABEL, true, None::<&str>)?,
            &update,
            &PredefinedMenuItem::separator(app)?,
            &MenuItem::with_id(app, CLOSE, CLOSE_LABEL, true, None::<&str>)?,
            &MenuItem::with_id(app, FULL_QUIT, FULL_QUIT_LABEL, true, None::<&str>)?,
        ],
    )?;
    let mut tray = TrayIconBuilder::with_id("werkschaft")
        .tooltip("Werkschaft")
        .menu(&menu)
        .show_menu_on_left_click(MENU_ON_LEFT_CLICK)
        .on_tray_icon_event(move |tray, event| {
            if restores(&event) {
                restore(tray.app_handle());
            }
        });
    if let Some(icon) = app.default_window_icon() {
        tray = tray.icon(icon.clone());
    }
    tray.build(app)?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use tauri::{tray::TrayIconId, PhysicalPosition, Rect};

    #[test]
    fn menu_items_map_to_the_two_lifetimes() {
        assert_eq!(action(SHOW), Some(Action::Show));
        assert_eq!(action(CHECK_UPDATES), Some(Action::CheckUpdates));
        assert_eq!(action(CLOSE), Some(Action::Exit(Exit::Close)));
        assert_eq!(action(FULL_QUIT), Some(Action::Exit(Exit::FullQuit)));
        assert_eq!(action("retry"), None);
        // The labels name the app and the background, never the holders.
        let ready = updates::menu_label(Some("0.52.1"));
        for label in [
            SHOW_LABEL,
            updates::CHECK_LABEL,
            ready.as_str(),
            CLOSE_LABEL,
            FULL_QUIT_LABEL,
        ] {
            assert!(!label.to_lowercase().contains("holder"));
            assert!(!label.to_lowercase().contains("daemon"));
        }
        assert!(FULL_QUIT_LABEL.contains("Background"));
    }

    fn click(button: MouseButton, button_state: MouseButtonState) -> TrayIconEvent {
        TrayIconEvent::Click {
            id: TrayIconId::new("werkschaft"),
            position: PhysicalPosition::default(),
            rect: Rect::default(),
            button,
            button_state,
        }
    }

    #[test]
    fn left_click_restores_except_where_it_opens_the_menu() {
        let left = click(MouseButton::Left, MouseButtonState::Up);
        assert_eq!(restores(&left), !cfg!(target_os = "macos"));
        assert!(!restores(&click(MouseButton::Left, MouseButtonState::Down)));
        assert!(!restores(&click(MouseButton::Right, MouseButtonState::Up)));
    }
}
