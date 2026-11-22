// The main window's native layer on Windows: where each webview sits and the
// maximize button's hit test.
//
// The window is undecorated and holds two webviews: the title strip above and
// the dashboard below. Both are child windows of the main window's HWND, so
// this module subclasses that HWND to lay them out on every resize, and to put
// one small transparent child window over the maximize button. That child
// answers WM_NCHITTEST with HTMAXBUTTON, which is what makes Windows 11 offer
// its snap layouts flyout when the pointer rests there: a webview window
// belongs to another process and answers HTCLIENT for every point it covers.
// The page never sees the pointer over that button, so the hover and pressed
// states reach the strip page as events instead.
//
// A second child window, a thin band along the top edge, gives the window its
// top resize border and the top corners. Tauri would attach its own, but only
// for a window whose content webview is not a child webview, and with several
// webviews every one of them is. The left, right and bottom borders are the
// window's own, which the system hit-tests outside the client area.

use std::sync::OnceLock;

use webview2_com::Microsoft::Web::WebView2::Win32::ICoreWebView2Controller;
use windows::{
    core::{w, PCWSTR},
    Win32::{
        Foundation::{HINSTANCE, HWND, LPARAM, LRESULT, RECT, WPARAM},
        Graphics::Gdi::{BeginPaint, EndPaint, PAINTSTRUCT},
        System::{LibraryLoader::GetModuleHandleW, Threading::GetCurrentThreadId},
        UI::{
            HiDpi::{GetDpiForWindow, GetSystemMetricsForDpi},
            Input::KeyboardAndMouse::{TrackMouseEvent, TME_LEAVE, TME_NONCLIENT, TRACKMOUSEEVENT},
            Shell::{DefSubclassProc, RemoveWindowSubclass, SetWindowSubclass},
            WindowsAndMessaging::*,
        },
    },
};

/// The strip's height and a caption button's width, in logical pixels. The
/// strip page's CSS (`--strip-h`, `.cap`) draws the same sizes.
pub const STRIP_HEIGHT: f64 = 34.0;
pub const CAPTION_WIDTH: f64 = 46.0;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Event {
    Active(bool),
    Maximized(bool),
    MaxHover(bool),
    MaxPressed(bool),
}

struct Data {
    top: HWND,
    dashboard: ICoreWebView2Controller,
    strip: ICoreWebView2Controller,
    overlay: HWND,
    band: HWND,
    tracking: bool,
    emit: Box<dyn Fn(Event)>,
}

const SUBCLASS_ID: usize = 0x5753_0001;

/// Lays out `dashboard` and `strip` in `top` and reports window state through
/// `emit`. Call on the UI thread.
pub unsafe fn install(
    top: HWND,
    dashboard: ICoreWebView2Controller,
    strip: ICoreWebView2Controller,
    emit: impl Fn(Event) + 'static,
) -> windows::core::Result<()> {
    let step = |what: &'static str| {
        move |error: windows::core::Error| {
            // A failing call that left no last error reads as S_OK.
            let code = if error.code().is_ok() {
                windows::Win32::Foundation::E_FAIL
            } else {
                error.code()
            };
            windows::core::Error::new(code, format!("{what} failed"))
        }
    };
    let (here, owner) = (GetCurrentThreadId(), GetWindowThreadProcessId(top, None));
    if here != owner {
        return Err(windows::core::Error::new(
            windows::Win32::Foundation::E_FAIL,
            format!("window thread {owner}, called on {here}"),
        ));
    }
    let data = Box::into_raw(Box::new(Data {
        top,
        dashboard,
        strip,
        overlay: HWND::default(),
        band: HWND::default(),
        tracking: false,
        emit: Box::new(emit),
    }));
    let module = HINSTANCE(
        GetModuleHandleW(PCWSTR::null())
            .map_err(step("GetModuleHandleW"))?
            .0,
    );
    static CLASS: OnceLock<()> = OnceLock::new();
    CLASS.get_or_init(|| {
        // The class paints nothing (no background brush; see WM_ERASEBKGND and
        // WM_PAINT below): WS_EX_TRANSPARENT lets the webview underneath show.
        let class = WNDCLASSEXW {
            cbSize: std::mem::size_of::<WNDCLASSEXW>() as u32,
            lpfnWndProc: Some(overlay_proc),
            hInstance: module,
            lpszClassName: OVERLAY_CLASS,
            ..Default::default()
        };
        RegisterClassExW(&class);
    });
    let overlay = CreateWindowExW(
        WS_EX_TRANSPARENT,
        OVERLAY_CLASS,
        w!("Werkschaft maximize button"),
        WS_CHILD | WS_VISIBLE | WS_CLIPSIBLINGS,
        0,
        0,
        1,
        1,
        Some(top),
        None,
        Some(module),
        None,
    )
    .map_err(step("CreateWindowExW"))?;
    let band = CreateWindowExW(
        WS_EX_TRANSPARENT,
        OVERLAY_CLASS,
        w!("Werkschaft resize border"),
        WS_CHILD | WS_VISIBLE | WS_CLIPSIBLINGS,
        0,
        0,
        1,
        1,
        Some(top),
        None,
        Some(module),
        None,
    )
    .map_err(step("CreateWindowExW"))?;
    SetWindowLongPtrW(overlay, GWLP_USERDATA, data as isize);
    SetWindowLongPtrW(band, GWLP_USERDATA, data as isize);
    (*data).overlay = overlay;
    (*data).band = band;
    if !SetWindowSubclass(top, Some(subclass), SUBCLASS_ID, data as usize).as_bool() {
        let _ = DestroyWindow(overlay);
        drop(Box::from_raw(data));
        return Err(windows::core::Error::new(
            windows::Win32::Foundation::E_FAIL,
            "SetWindowSubclass refused",
        ));
    }
    layout(&*data);
    Ok(())
}

const OVERLAY_CLASS: PCWSTR = w!("WERKSCHAFT_MAXIMIZE_BUTTON");

fn scaled(logical: f64, dpi: u32) -> i32 {
    (logical * f64::from(dpi) / 96.0).round() as i32
}

unsafe fn place(controller: &ICoreWebView2Controller, x: i32, y: i32, width: i32, height: i32) {
    let mut host = HWND::default();
    // The webview's container window; the window procedure of the wry crate
    // moves it asynchronously on every resize, so this does too, after it.
    if controller.ParentWindow(&mut host).is_ok() && !host.is_invalid() {
        let _ = SetWindowPos(
            host,
            None,
            x,
            y,
            width,
            height,
            SWP_ASYNCWINDOWPOS | SWP_NOACTIVATE | SWP_NOZORDER,
        );
    }
    let _ = controller.SetBounds(RECT {
        left: 0,
        top: 0,
        right: width,
        bottom: height,
    });
    let _ = controller.NotifyParentWindowPositionChanged();
}

unsafe fn layout(data: &Data) {
    let mut client = RECT::default();
    if GetClientRect(data.top, &mut client).is_err() {
        return;
    }
    let (width, height) = (client.right - client.left, client.bottom - client.top);
    let dpi = GetDpiForWindow(data.top).max(96);
    let strip = scaled(STRIP_HEIGHT, dpi).min(height);
    place(&data.strip, 0, 0, width, strip);
    place(&data.dashboard, 0, strip, width, (height - strip).max(0));
    let button = scaled(CAPTION_WIDTH, dpi);
    let _ = SetWindowPos(
        data.overlay,
        Some(HWND_TOP),
        (width - 2 * button).max(0),
        0,
        button,
        strip,
        SWP_NOACTIVATE,
    );
    // The border band stays above the maximize button's window, and goes away
    // when the window cannot be resized by its edge.
    let (frame, _) = frame_size(data.top);
    let _ = SetWindowPos(
        data.band,
        Some(HWND_TOP),
        0,
        0,
        width,
        frame,
        SWP_NOACTIVATE,
    );
    let _ = ShowWindow(
        data.band,
        if IsZoomed(data.top).as_bool() {
            SW_HIDE
        } else {
            SW_SHOWNA
        },
    );
}

// The window's resize border thickness and the width of a top corner, in
// physical pixels.
unsafe fn frame_size(top: HWND) -> (i32, i32) {
    let dpi = GetDpiForWindow(top).max(96);
    let frame =
        GetSystemMetricsForDpi(SM_CYFRAME, dpi) + GetSystemMetricsForDpi(SM_CXPADDEDBORDER, dpi);
    (frame, scaled(CORNER, dpi))
}

/// How far from a top corner the border still resizes diagonally, in logical pixels.
const CORNER: f64 = 12.0;

unsafe extern "system" fn subclass(
    hwnd: HWND,
    message: u32,
    wparam: WPARAM,
    lparam: LPARAM,
    _id: usize,
    data: usize,
) -> LRESULT {
    let state = &*(data as *const Data);
    match message {
        WM_SIZE => {
            let result = DefSubclassProc(hwnd, message, wparam, lparam);
            if wparam.0 as u32 != SIZE_MINIMIZED {
                layout(state);
                (state.emit)(Event::Maximized(IsZoomed(hwnd).as_bool()));
            }
            result
        }
        WM_MOVE | WM_MOVING => {
            // Popups of the pages (a select list, an input method) follow the window.
            let _ = state.dashboard.NotifyParentWindowPositionChanged();
            let _ = state.strip.NotifyParentWindowPositionChanged();
            DefSubclassProc(hwnd, message, wparam, lparam)
        }
        WM_ACTIVATE => {
            (state.emit)(Event::Active((wparam.0 & 0xFFFF) as u32 != WA_INACTIVE));
            DefSubclassProc(hwnd, message, wparam, lparam)
        }
        WM_NCDESTROY => {
            let _ = RemoveWindowSubclass(hwnd, Some(subclass), SUBCLASS_ID);
            let result = DefSubclassProc(hwnd, message, wparam, lparam);
            drop(Box::from_raw(data as *mut Data));
            result
        }
        _ => DefSubclassProc(hwnd, message, wparam, lparam),
    }
}

unsafe extern "system" fn overlay_proc(
    hwnd: HWND,
    message: u32,
    wparam: WPARAM,
    lparam: LPARAM,
) -> LRESULT {
    let data = GetWindowLongPtrW(hwnd, GWLP_USERDATA) as *mut Data;
    if data.is_null() {
        return DefWindowProcW(hwnd, message, wparam, lparam);
    }
    let state = &mut *data;
    if hwnd == state.band {
        return border_proc(state, hwnd, message, wparam, lparam);
    }
    match message {
        WM_NCHITTEST => LRESULT(HTMAXBUTTON as isize),
        WM_ERASEBKGND => LRESULT(1),
        WM_PAINT => {
            let mut paint = PAINTSTRUCT::default();
            let _ = BeginPaint(hwnd, &mut paint);
            let _ = EndPaint(hwnd, &paint);
            LRESULT(0)
        }
        WM_NCMOUSEMOVE => {
            if !state.tracking {
                let mut track = TRACKMOUSEEVENT {
                    cbSize: std::mem::size_of::<TRACKMOUSEEVENT>() as u32,
                    dwFlags: TME_LEAVE | TME_NONCLIENT,
                    hwndTrack: hwnd,
                    dwHoverTime: 0,
                };
                state.tracking = TrackMouseEvent(&mut track).is_ok();
                (state.emit)(Event::MaxHover(true));
            }
            LRESULT(0)
        }
        WM_NCMOUSELEAVE => {
            state.tracking = false;
            (state.emit)(Event::MaxHover(false));
            (state.emit)(Event::MaxPressed(false));
            LRESULT(0)
        }
        WM_NCLBUTTONDOWN => {
            (state.emit)(Event::MaxPressed(true));
            LRESULT(0)
        }
        WM_NCLBUTTONUP => {
            (state.emit)(Event::MaxPressed(false));
            let command = if IsZoomed(state.top).as_bool() {
                SC_RESTORE
            } else {
                SC_MAXIMIZE
            };
            let _ = PostMessageW(
                Some(state.top),
                WM_SYSCOMMAND,
                WPARAM(command as usize),
                LPARAM(0),
            );
            LRESULT(0)
        }
        _ => DefWindowProcW(hwnd, message, wparam, lparam),
    }
}

// The top resize border: a hit test result for the edge or a top corner, and
// the click handed to the main window as its own non-client click, which starts
// the system's resize loop.
unsafe fn border_proc(
    state: &Data,
    hwnd: HWND,
    message: u32,
    wparam: WPARAM,
    lparam: LPARAM,
) -> LRESULT {
    match message {
        WM_NCHITTEST => {
            let (x, _) = (lparam.0 as i16 as i32, (lparam.0 >> 16) as i16 as i32);
            let mut window = RECT::default();
            let _ = GetWindowRect(hwnd, &mut window);
            let (_, corner) = frame_size(state.top);
            LRESULT(if x < window.left + corner {
                HTTOPLEFT
            } else if x >= window.right - corner {
                HTTOPRIGHT
            } else {
                HTTOP
            } as isize)
        }
        WM_NCLBUTTONDOWN => {
            let _ = PostMessageW(Some(state.top), WM_NCLBUTTONDOWN, wparam, lparam);
            LRESULT(0)
        }
        WM_ERASEBKGND => LRESULT(1),
        WM_PAINT => {
            let mut paint = PAINTSTRUCT::default();
            let _ = BeginPaint(hwnd, &mut paint);
            let _ = EndPaint(hwnd, &paint);
            LRESULT(0)
        }
        _ => DefWindowProcW(hwnd, message, wparam, lparam),
    }
}
