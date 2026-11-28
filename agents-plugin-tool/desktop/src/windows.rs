use std::sync::Arc;

use tauri::{Manager, WebviewWindow};
use tauri_plugin_dialog::{DialogExt, MessageDialogKind};
use webview2_com::{
    take_pwstr, AcceleratorKeyPressedEventHandler, Microsoft::Web::WebView2::Win32::*,
    NavigationCompletedEventHandler, NavigationStartingEventHandler,
    PermissionRequestedEventHandler, ProcessFailedEventHandler, WebMessageReceivedEventHandler,
    WebResourceRequestedEventHandler,
};
use windows::{
    core::{w, Interface, BOOL, HRESULT, HSTRING, PWSTR},
    Win32::UI::Input::KeyboardAndMouse::{
        GetKeyState, VIRTUAL_KEY, VK_0, VK_ADD, VK_CONTROL, VK_F10, VK_F12, VK_I, VK_LWIN, VK_M,
        VK_MENU, VK_OEM_MINUS, VK_OEM_PLUS, VK_R, VK_RWIN, VK_SHIFT, VK_SUBTRACT,
    },
};

use crate::{
    accelerator, capture, close, menu_action, navigation, notify, permissions, shell::Connection,
    speech, strip, strip_msg, updates, webview_setup_failed,
};

pub fn copy_text(window: &WebviewWindow, text: &str) -> bool {
    let Ok(hwnd) = window.hwnd() else {
        eprintln!("Werkschaft: Could not get the clipboard owner's window handle");
        return false;
    };
    // EmptyClipboard assigns ownership to this HWND before SetClipboardData.
    let _clipboard = match clipboard_win::Clipboard::new_attempts_for(hwnd.0, 5) {
        Ok(clipboard) => clipboard,
        Err(error) => {
            eprintln!("Werkschaft: OpenClipboard failed (Win32 {})", error.raw_code());
            return false;
        }
    };
    let write = clipboard_win::raw::empty()
        .and_then(|()| clipboard_win::raw::set_string_with(text, clipboard_win::options::NoClear));
    match write {
        Ok(()) => true,
        Err(error) => {
            eprintln!("Werkschaft: Clipboard write failed (Win32 {})", error.raw_code());
            false
        }
    }
}

pub fn install(
    window: &WebviewWindow,
    connection: Arc<Connection>,
    ready: impl FnOnce(WebviewWindow) + Send + 'static,
) -> tauri::Result<()> {
    let window = window.clone();
    window.clone().with_webview(move |platform| {
        let result = unsafe { install_hooks(&platform.controller(), &window, &connection) };
        match result {
            Ok(()) => queue(&window, ready),
            Err(error) => {
                eprintln!("Could not install WebView2 hooks");
                let outdated = error.code() == E_NOINTERFACE;
                queue(&window, move |target| {
                    if outdated {
                        runtime_outdated(&target);
                    }
                    webview_setup_failed(&target, &connection)
                });
            }
        }
    })
}

const E_NOINTERFACE: HRESULT = HRESULT(0x8000_4002_u32 as i32);

const RUNTIME_OUTDATED: &str = "The Microsoft Edge WebView2 Runtime on this computer is too old for Werkschaft's page protection, so the dashboard cannot open. Update the WebView2 Runtime to version 122 or later (or ask your administrator to), then restart Werkschaft.";

// Every interface the hooks need comes from the installed runtime: a missing
// one means an older runtime, and Retry would fail the same way. The page
// boundary stays required; the user is told how to meet it.
fn runtime_outdated(window: &WebviewWindow) {
    eprintln!("Werkschaft: The installed WebView2 Runtime lacks a required interface");
    window
        .dialog()
        .message(RUNTIME_OUTDATED)
        .title("Update the WebView2 Runtime")
        .kind(MessageDialogKind::Warning)
        .parent(window)
        .show(|_| ());
}

// Dispatch from a worker so a WebView2 event has returned before shell code
// can rebuild it; see `chrome::queue`.
pub(crate) use crate::chrome::queue;

unsafe fn install_hooks(
    controller: &ICoreWebView2Controller,
    window: &WebviewWindow,
    connection: &Arc<Connection>,
) -> windows::core::Result<()> {
    let webview = controller.CoreWebView2()?;
    install_document_guard(&webview, connection.clone())?;
    let mut token = 0;
    let frames = connection.clone();
    webview.add_FrameNavigationStarting(
        &NavigationStartingEventHandler::create(Box::new(move |_, args| {
            if let Some(args) = args {
                // Cancel first so malformed URLs and failed property reads fail closed.
                args.SetCancel(true)?;
                let mut raw = PWSTR::null();
                args.Uri(&mut raw)?;
                let uri = take_pwstr(raw);
                if url::Url::parse(&uri).ok().is_some_and(|url| {
                    navigation::frame_allowed(&url, frames.pinned_url().as_ref())
                }) {
                    args.SetCancel(false)?;
                }
            }
            Ok(())
        })),
        &mut token,
    )?;

    let settings = webview.Settings()?;
    // Disable the control's built-in zoom keys and retain its Edit shortcuts.
    settings.SetIsZoomControlEnabled(false)?;
    let settings = settings.cast::<ICoreWebView2Settings2>()?;
    let mut raw = PWSTR::null();
    settings.UserAgent(&mut raw)?;
    let default_agent = take_pwstr(raw);
    let agent = format!(
        "{default_agent} Werkschaft/{}",
        window.app_handle().package_info().version
    );
    settings.SetUserAgent(&HSTRING::from(agent))?;
    install_accelerators(controller, window.clone(), connection.clone())?;

    let perm_connection = connection.clone();
    webview.add_PermissionRequested(
        &PermissionRequestedEventHandler::create(Box::new(move |_, args| {
            if let Some(args) = args {
                let mut kind = COREWEBVIEW2_PERMISSION_KIND::default();
                args.PermissionKind(&mut kind)?;
                let perm_kind = if kind == COREWEBVIEW2_PERMISSION_KIND_CLIPBOARD_READ {
                    permissions::WindowsPermissionKind::ClipboardRead
                } else if kind == COREWEBVIEW2_PERMISSION_KIND_MICROPHONE {
                    permissions::WindowsPermissionKind::Microphone
                } else {
                    permissions::WindowsPermissionKind::Other
                };
                if matches!(
                    perm_kind,
                    permissions::WindowsPermissionKind::ClipboardRead
                        | permissions::WindowsPermissionKind::Microphone
                ) {
                    let transient = args
                        .cast::<ICoreWebView2PermissionRequestedEventArgs3>()
                        .ok();
                    if let Some(args3) = &transient {
                        args3.SetSavesInProfile(false)?;
                    }
                    args.SetState(COREWEBVIEW2_PERMISSION_STATE_DENY)?;
                    let mut initiated = BOOL::default();
                    args.IsUserInitiated(&mut initiated)?;
                    let mut raw = PWSTR::null();
                    args.Uri(&mut raw)?;
                    let uri = take_pwstr(raw);
                    // A saved grant would bypass future user-gesture and pin checks.
                    if permissions::windows_permission_allowed(
                        perm_kind,
                        &uri,
                        perm_connection.pinned_url().as_ref(),
                        initiated.as_bool(),
                        transient.is_some(),
                    ) {
                        args.SetState(COREWEBVIEW2_PERMISSION_STATE_ALLOW)?;
                    }
                }
            }
            Ok(())
        })),
        &mut token,
    )?;

    if !close::checks_draft(window.label()) {
        return Ok(());
    }

    let failed = connection.clone();
    let epoch = failed.window_epoch();
    let notify_bridge = window.try_state::<Arc<notify::Notifier>>().is_some();
    let capture_bridge = window.try_state::<Arc<capture::Capturer>>().is_some();
    let speech_bridge = window.try_state::<Arc<speech::Controller>>().is_some();
    // The theme message needs the strip, which exists only on the main window.
    let theme_bridge = window.app_handle().try_state::<strip::Strips>().is_some();
    // Always registered: the recovery page's actions work in a build with no
    // updater, and the update and recovery gate decides per message.
    {
        let target = window.clone();
        let themes = connection.clone();
        // The core event receives the top-level document only. Frame messages
        // have a separate WebView2 event, which the shell does not register.
        webview.add_WebMessageReceived(
            &WebMessageReceivedEventHandler::create(Box::new(move |_, args| {
                if let Some(args) = args {
                    let mut raw = PWSTR::null();
                    if args.TryGetWebMessageAsString(&mut raw).is_err() {
                        return Ok(());
                    }
                    // No bridge accepts more than notify's cap, so a longer
                    // payload is dropped before it is copied into a String.
                    // (UTF-16 units never outnumber the UTF-8 bytes.)
                    if raw.as_wide().len() > notify::MAX_MESSAGE {
                        windows::Win32::System::Com::CoTaskMemFree(Some(raw.0 as *const _));
                        return Ok(());
                    }
                    let message = take_pwstr(raw);
                    let theme = theme_bridge
                        && message.starts_with(strip_msg::THEME_PREFIX)
                        && message.len() <= strip_msg::THEME_MESSAGE_MAX;
                    let update = updates::bridged(&message);
                    let notice = notify_bridge
                        && message.starts_with(notify::PREFIX)
                        && message.len() <= notify::MAX_MESSAGE;
                    let cap = capture_bridge
                        && message.starts_with(capture::PREFIX)
                        && message.len() <= capture::MAX_MESSAGE;
                    let speech_msg = speech_bridge
                        && message.starts_with(speech::PREFIX)
                        && message.len() <= speech::MAX_MESSAGE;
                    if !update && !notice && !theme && !cap && !speech_msg {
                        return Ok(());
                    }
                    let mut raw = PWSTR::null();
                    args.Source(&mut raw)?;
                    if let Ok(source) = url::Url::parse(&take_pwstr(raw)) {
                        if theme {
                            strip::receive_theme(&target, &themes, epoch, &source, &message);
                        } else if update {
                            updates::receive(target.clone(), epoch, true, source, &message);
                        } else if cap {
                            capture::receive(target.clone(), epoch, source, &message);
                        } else if speech_msg {
                            speech::receive(target.clone(), epoch, source, &message);
                        } else {
                            notify::receive(target.clone(), epoch, source, &message);
                        }
                    }
                }
                Ok(())
            })),
            &mut token,
        )?;
    }
    let failed_window = window.clone();
    webview.add_NavigationCompleted(
        &NavigationCompletedEventHandler::create(Box::new(move |_, args| {
            if let Some(args) = args {
                let mut success = BOOL::default();
                args.IsSuccess(&mut success)?;
                let mut status = COREWEBVIEW2_WEB_ERROR_STATUS::default();
                args.WebErrorStatus(&mut status)?;
                if !success.as_bool()
                    && status != COREWEBVIEW2_WEB_ERROR_STATUS_OPERATION_CANCELED
                    && failed.pinned_url().is_some()
                {
                    let connection = failed.clone();
                    queue(&failed_window, move |target| {
                        if connection.window_epoch() == epoch {
                            connection.recover(target, "Dashboard navigation failed");
                        }
                    });
                }
            }
            Ok(())
        })),
        &mut token,
    )?;

    let failed = connection.clone();
    let failed_window = window.clone();
    webview.add_ProcessFailed(
        &ProcessFailedEventHandler::create(Box::new(move |_, args| {
            if let Some(args) = args {
                let mut kind = COREWEBVIEW2_PROCESS_FAILED_KIND::default();
                args.ProcessFailedKind(&mut kind)?;
                if kind == COREWEBVIEW2_PROCESS_FAILED_KIND_BROWSER_PROCESS_EXITED
                    || kind == COREWEBVIEW2_PROCESS_FAILED_KIND_RENDER_PROCESS_EXITED
                    || kind == COREWEBVIEW2_PROCESS_FAILED_KIND_RENDER_PROCESS_UNRESPONSIVE
                {
                    eprintln!("WebView2 process failed");
                    let connection = failed.clone();
                    queue(&failed_window, move |target| {
                        if connection.window_epoch() == epoch {
                            connection.webview_failed(target);
                        }
                    });
                }
            }
            Ok(())
        })),
        &mut token,
    )?;

    Ok(())
}

unsafe fn install_document_guard(
    webview: &ICoreWebView2,
    connection: Arc<Connection>,
) -> windows::core::Result<()> {
    let filter = webview.cast::<ICoreWebView2_22>()?;
    let environment = webview.cast::<ICoreWebView2_2>()?.Environment()?;
    let forbidden = environment.CreateWebResourceResponse(
        None,
        403,
        w!("Forbidden"),
        w!("Cache-Control: no-store"),
    )?;
    // Navigation cancellation may arrive after the request has been dispatched.
    // The source-kind overload also intercepts cross-origin iframe requests.
    filter.AddWebResourceRequestedFilterWithRequestSourceKinds(
        w!("*"),
        COREWEBVIEW2_WEB_RESOURCE_CONTEXT_DOCUMENT,
        COREWEBVIEW2_WEB_RESOURCE_REQUEST_SOURCE_KINDS_DOCUMENT,
    )?;
    let mut token = 0;
    webview.add_WebResourceRequested(
        &WebResourceRequestedEventHandler::create(Box::new(move |_, args| {
            let Some(args) = args else { return Ok(()) };
            let mut context = COREWEBVIEW2_WEB_RESOURCE_CONTEXT::default();
            args.ResourceContext(&mut context)?;
            // Wry's bundle filter also raises this event for assets.
            if context != COREWEBVIEW2_WEB_RESOURCE_CONTEXT_DOCUMENT {
                return Ok(());
            }
            let allowed = (|| {
                let mut raw = PWSTR::null();
                args.Request().ok()?.Uri(&mut raw).ok()?;
                let url = url::Url::parse(&take_pwstr(raw)).ok()?;
                let pin = connection.pinned_url();
                Some(matches!(
                    navigation::destination(&url, pin.as_ref()),
                    navigation::Destination::Dashboard | navigation::Destination::Local
                ))
            })();
            if allowed != Some(true) {
                args.SetResponse(&forbidden)?;
            }
            Ok(())
        })),
        &mut token,
    )
}

pub(crate) unsafe fn install_accelerators(
    controller: &ICoreWebView2Controller,
    window: WebviewWindow,
    connection: Arc<Connection>,
) -> windows::core::Result<()> {
    let epoch = connection.window_epoch();
    let mut token = 0;
    let mut alt = accelerator::AltTap::default();
    controller.add_AcceleratorKeyPressed(
        &AcceleratorKeyPressedEventHandler::create(Box::new(move |_, args| {
            let Some(args) = args else { return Ok(()) };
            let mut kind = COREWEBVIEW2_KEY_EVENT_KIND::default();
            args.KeyEventKind(&mut kind)?;
            let down = kind == COREWEBVIEW2_KEY_EVENT_KIND_KEY_DOWN
                || kind == COREWEBVIEW2_KEY_EVENT_KIND_SYSTEM_KEY_DOWN;
            let up = kind == COREWEBVIEW2_KEY_EVENT_KIND_KEY_UP
                || kind == COREWEBVIEW2_KEY_EVENT_KIND_SYSTEM_KEY_UP;
            if !down && !up {
                return Ok(());
            }
            let mut raw = 0;
            args.VirtualKey(&mut raw)?;
            let vk = VIRTUAL_KEY(raw as u16);
            let mut status = COREWEBVIEW2_PHYSICAL_KEY_STATUS::default();
            args.PhysicalKeyStatus(&mut status)?;
            let repeat = status.WasKeyDown.as_bool();
            let (ctrl, shift) = (key_down(VK_CONTROL), key_down(VK_SHIFT));

            // The main window has no menu bar to answer F10 and Alt: the strip
            // opens the menu instead.
            if strip::active(window.app_handle(), &window) {
                let opened = if vk == VK_MENU {
                    if down {
                        alt.alt_down(repeat);
                        false
                    } else {
                        alt.alt_up()
                    }
                } else {
                    if down {
                        alt.other_key();
                    }
                    false
                };
                let menu = if opened {
                    Some(accelerator::MENUS[0])
                } else if down && vk == VK_F10 && !ctrl && !shift && !key_down(VK_MENU) {
                    Some(accelerator::MENUS[0])
                } else if down && key_down(VK_MENU) && !ctrl && !shift && !repeat {
                    // VK_A..VK_Z are the letters' ASCII codes.
                    (0x41..=0x5A)
                        .contains(&vk.0)
                        .then(|| char::from(vk.0 as u8))
                        .and_then(accelerator::mnemonic_menu)
                } else {
                    None
                };
                if let Some(menu) = menu {
                    args.SetHandled(true)?;
                    strip::open_menu(window.app_handle(), menu);
                    return Ok(());
                }
                if vk == VK_MENU {
                    return Ok(());
                }
            }
            if !down {
                return Ok(());
            }

            let key = match vk {
                VK_R => accelerator::Key::R,
                VK_I => accelerator::Key::I,
                VK_M => accelerator::Key::M,
                VK_F12 => accelerator::Key::F12,
                VK_0 => accelerator::Key::Zero,
                VK_OEM_PLUS | VK_ADD => accelerator::Key::Plus,
                VK_OEM_MINUS | VK_SUBTRACT => accelerator::Key::Minus,
                _ => return Ok(()),
            };
            if [VK_MENU, VK_LWIN, VK_RWIN].into_iter().any(key_down) {
                return Ok(());
            }
            if let Some(id) = accelerator::menu_id(key, ctrl, shift) {
                args.SetHandled(true)?;
                if !repeat {
                    // WebView2 blocks the browser process during this callback.
                    // Dispatch after returning so Retry can safely navigate.
                    let connection = connection.clone();
                    queue(&window, move |target| {
                        if !close::checks_draft(target.label())
                            || connection.window_epoch() == epoch
                        {
                            menu_action(target, &connection, id);
                        }
                    });
                }
            }
            Ok(())
        })),
        &mut token,
    )
}

fn key_down(key: VIRTUAL_KEY) -> bool {
    unsafe { GetKeyState(i32::from(key.0)) < 0 }
}
