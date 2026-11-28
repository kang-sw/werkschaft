// Native viewport screenshot capture for the local dashboard shell
// (ticket 261128-feat-ws-dashboard-control-mcp-api, Tier A). The main window's
// pinned dashboard page invokes `window.wsDaemonNativeCapture.capture()`, which
// posts a bounded request over the top-frame bridge (capture-bridge.js); the
// shell captures the live WKWebView (macOS) or WebView2 (Windows) surface as
// PNG bytes and answers back only to the pinned top frame.

use std::sync::{
    atomic::{AtomicBool, Ordering},
    Arc,
};

use serde::Deserialize;
use serde_json::json;
use tauri::{AppHandle, Manager, WebviewWindow};
use url::Url;

use crate::{
    close,
    navigation::{destination, Destination},
    shell::Connection,
};

const SCRIPT: &str = include_str!("capture-bridge.js");
pub const PREFIX: &str = "werkschaft:capture:";
/// The longest request message the capture bridge accepts, in bytes.
pub const MAX_MESSAGE: usize = 256;
/// Maximum PNG payload size accepted by the daemon's /api/daemon/bridge-reply
/// route (8 MiB). Captures larger than this settle with `payload_too_large`.
pub const MAX_PNG_BYTES: usize = 8 << 20;
const PNG_MAGIC: &[u8; 8] = b"\x89PNG\r\n\x1a\n";

pub struct Capturer {
    connection: Arc<Connection>,
    in_flight: AtomicBool,
}

impl Capturer {
    pub fn new(connection: Arc<Connection>) -> Self {
        Self {
            connection,
            in_flight: AtomicBool::new(false),
        }
    }

    /// Acquires the single in-flight capture slot. Returns `None` while another
    /// capture is already running.
    fn try_acquire(self: &Arc<Self>) -> Option<InFlightGuard> {
        self.in_flight
            .compare_exchange(false, true, Ordering::AcqRel, Ordering::Acquire)
            .ok()
            .map(|_| InFlightGuard {
                capturer: Arc::clone(self),
            })
    }
}

struct InFlightGuard {
    capturer: Arc<Capturer>,
}

impl Drop for InFlightGuard {
    fn drop(&mut self) {
        self.capturer.in_flight.store(false, Ordering::Release);
    }
}

pub fn manage(app: &AppHandle, connection: Arc<Connection>) {
    app.manage(Arc::new(Capturer::new(connection)));
}

/// One native screenshot request posted by the dashboard page.
#[derive(Debug, PartialEq, Eq)]
pub struct CaptureRequest {
    pub id: u64,
}

pub fn parse(message: &str) -> Option<CaptureRequest> {
    #[derive(Deserialize)]
    #[serde(deny_unknown_fields)]
    struct Payload {
        id: u64,
    }
    if message.len() > MAX_MESSAGE {
        return None;
    }
    let payload: Payload = serde_json::from_str(message.strip_prefix(PREFIX)?).ok()?;
    (payload.id > 0).then_some(CaptureRequest { id: payload.id })
}

/// Reports whether a bridge message from `window_label` at `epoch` and `source`
/// is authorized to trigger native capture for the pinned dashboard `pin`.
pub fn authorized(
    window_label: &str,
    expected_epoch: usize,
    epoch: usize,
    source: &Url,
    pin: Option<&Url>,
) -> bool {
    let Some(pin) = pin else {
        return false;
    };
    close::checks_draft(window_label)
        && expected_epoch == epoch
        && destination(source, Some(pin)) == Destination::Dashboard
}

/// Validates PNG header and 8 MiB payload limit and encodes to standard base64
/// for delivery back to the pinned top frame.
pub fn encode_png_result(png: &[u8]) -> Result<String, &'static str> {
    if png.len() > MAX_PNG_BYTES {
        return Err("payload_too_large");
    }
    if png.len() < PNG_MAGIC.len() || &png[..PNG_MAGIC.len()] != PNG_MAGIC {
        return Err("screenshot_unavailable");
    }
    Ok(encode_base64(png))
}

fn encode_base64(input: &[u8]) -> String {
    const TABLE: &[u8; 64] = b"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
    let mut out = String::with_capacity(input.len().div_ceil(3) * 4);
    for chunk in input.chunks(3) {
        let b0 = u32::from(chunk[0]);
        let b1 = if chunk.len() > 1 {
            u32::from(chunk[1])
        } else {
            0
        };
        let b2 = if chunk.len() > 2 {
            u32::from(chunk[2])
        } else {
            0
        };
        let triple = (b0 << 16) | (b1 << 8) | b2;
        out.push(TABLE[((triple >> 18) & 0x3f) as usize] as char);
        out.push(TABLE[((triple >> 12) & 0x3f) as usize] as char);
        if chunk.len() > 1 {
            out.push(TABLE[((triple >> 6) & 0x3f) as usize] as char);
        } else {
            out.push('=');
        }
        if chunk.len() > 2 {
            out.push(TABLE[(triple & 0x3f) as usize] as char);
        } else {
            out.push('=');
        }
    }
    out
}

fn answer_script(origin: &str, id: u64, result: Result<&str, &str>) -> String {
    let origin = json!(origin);
    let payload = match result {
        Ok(png_base64) => json!({ "ok": true, "png_base64": png_base64 }),
        Err(error) => json!({ "ok": false, "error": error }),
    };
    format!(
        "if (window === window.top && location.origin === {origin}) window.__werkschaftCaptureResult?.({id}, {payload});"
    )
}

/// Installs the capture bridge in the main window's pinned dashboard page.
pub fn inject(window: &WebviewWindow, source: &Url) {
    let Some(capturer) = window.try_state::<Arc<Capturer>>() else {
        return;
    };
    let pin = capturer.connection.pinned_url();
    if !close::checks_draft(window.label())
        || destination(source, pin.as_ref()) != Destination::Dashboard
    {
        return;
    }
    let origin = json!(source.origin().ascii_serialization());
    let _ = window.eval(format!(
        "(() => {{ {SCRIPT}\ninstallWerkschaftCaptureBridge({origin}); }})()"
    ));
}

/// Handles a capture bridge message from the main window's top frame at `source`.
pub fn receive(window: WebviewWindow, epoch: usize, source: Url, message: &str) {
    let Some(capturer) = window.try_state::<Arc<Capturer>>() else {
        return;
    };
    let capturer = capturer.inner().clone();
    let pin = capturer.connection.pinned_url();
    if !authorized(
        window.label(),
        capturer.connection.window_epoch(),
        epoch,
        &source,
        pin.as_ref(),
    ) {
        return;
    }
    let Some(req) = parse(message) else {
        return;
    };
    let Some(pin) = pin else {
        return;
    };
    let origin = pin.origin().ascii_serialization();
    let Some(guard) = capturer.try_acquire() else {
        let _ = window.eval(answer_script(&origin, req.id, Err("busy")));
        return;
    };
    capture_platform(window, origin, req.id, guard);
}

#[cfg(target_os = "macos")]
fn capture_platform(window: WebviewWindow, origin: String, id: u64, guard: InFlightGuard) {
    use block2::RcBlock;
    use objc2::{
        msg_send,
        runtime::{AnyClass, AnyObject},
    };
    use objc2_foundation::{NSData, NSError};
    use objc2_web_kit::WKWebView;

    let reply_window = window.clone();
    let fail_window = window.clone();
    let fail_origin = origin.clone();
    if window
        .with_webview(move |platform| {
            let webview = unsafe { &*platform.inner().cast::<WKWebView>() };
            let block = RcBlock::new(move |image: *mut AnyObject, error: *mut NSError| {
                // Keep the in-flight guard bound to the completion block so
                // dropping or completing always releases the slot.
                let _guard = &guard;
                let encoded: Result<String, &'static str> = (|| {
                    if !error.is_null() || image.is_null() {
                        return Err("screenshot_unavailable");
                    }
                    unsafe {
                        let tiff: *mut NSData = msg_send![image, TIFFRepresentation];
                        if tiff.is_null() {
                            return Err("screenshot_unavailable");
                        }
                        let Some(rep_cls) = AnyClass::get(c"NSBitmapImageRep") else {
                            return Err("screenshot_unavailable");
                        };
                        let rep: *mut AnyObject = msg_send![rep_cls, imageRepWithData: tiff];
                        if rep.is_null() {
                            return Err("screenshot_unavailable");
                        }
                        let Some(dict_cls) = AnyClass::get(c"NSDictionary") else {
                            return Err("screenshot_unavailable");
                        };
                        let props: *mut AnyObject = msg_send![dict_cls, dictionary];
                        // NSBitmapImageFileTypePNG = 4.
                        let png_data: *mut NSData = msg_send![
                            rep,
                            representationUsingType: 4usize,
                            properties: props
                        ];
                        if png_data.is_null() {
                            return Err("screenshot_unavailable");
                        }
                        let len: usize = msg_send![png_data, length];
                        if len > MAX_PNG_BYTES {
                            return Err("payload_too_large");
                        }
                        let ptr: *const u8 = msg_send![png_data, bytes];
                        if ptr.is_null() || len == 0 {
                            return Err("screenshot_unavailable");
                        }
                        let bytes = std::slice::from_raw_parts(ptr, len);
                        encode_png_result(bytes)
                    }
                })();
                let script = match &encoded {
                    Ok(b64) => answer_script(&origin, id, Ok(b64.as_str())),
                    Err(code) => answer_script(&origin, id, Err(code)),
                };
                let _ = reply_window.eval(script);
            });
            unsafe {
                let _: () = msg_send![
                    webview,
                    takeSnapshotWithConfiguration: std::ptr::null::<AnyObject>(),
                    completionHandler: &*block
                ];
            }
        })
        .is_err()
    {
        let _ = fail_window.eval(answer_script(&fail_origin, id, Err("screenshot_unavailable")));
    }
}

#[cfg(windows)]
fn capture_platform(window: WebviewWindow, origin: String, id: u64, guard: InFlightGuard) {
    use webview2_com::{
        CapturePreviewCompletedHandler,
        Microsoft::Web::WebView2::Win32::COREWEBVIEW2_CAPTURE_PREVIEW_IMAGE_FORMAT_PNG,
    };
    use windows::Win32::{System::Com::IStream, UI::Shell::SHCreateMemStream};

    let reply_window = window.clone();
    let fail_window = window.clone();
    let fail_origin = origin.clone();
    if window
        .with_webview(move |platform| {
            let start = unsafe {
                let webview = platform.controller().CoreWebView2().ok();
                let stream = SHCreateMemStream(None);
                match (webview, stream) {
                    (Some(webview), Some(stream)) => {
                        let stream_for_cb: IStream = stream.clone();
                        let reply_window = reply_window.clone();
                        let origin = origin.clone();
                        let handler = CapturePreviewCompletedHandler::create(Box::new(
                            move |result: windows::core::Result<()>| {
                                let _guard = guard;
                                let encoded: Result<String, &'static str> = match result {
                                    Err(_) => Err("screenshot_unavailable"),
                                    Ok(()) => read_win_stream_png(&stream_for_cb),
                                };
                                let script = match &encoded {
                                    Ok(b64) => answer_script(&origin, id, Ok(b64.as_str())),
                                    Err(code) => answer_script(&origin, id, Err(code)),
                                };
                                crate::windows::queue(&reply_window, move |target| {
                                    let _ = target.eval(script);
                                });
                                Ok(())
                            },
                        ));
                        webview
                            .CapturePreview(
                                COREWEBVIEW2_CAPTURE_PREVIEW_IMAGE_FORMAT_PNG,
                                &stream,
                                &handler,
                            )
                            .is_ok()
                    }
                    _ => false,
                }
            };
            if !start {
                let _ = fail_window.eval(answer_script(
                    &fail_origin,
                    id,
                    Err("screenshot_unavailable"),
                ));
            }
        })
        .is_err()
    {
        let _ = window.eval(answer_script(&fail_origin, id, Err("screenshot_unavailable")));
    }
}

#[cfg(windows)]
unsafe fn read_win_stream_png(stream: &windows::Win32::System::Com::IStream) -> Result<String, &'static str> {
    use windows::Win32::System::Com::STREAM_SEEK_SET;

    if stream.Seek(0, STREAM_SEEK_SET, None).is_err() {
        return Err("screenshot_unavailable");
    }
    let mut out = Vec::new();
    let mut chunk = [0u8; 64 * 1024];
    loop {
        let mut read = 0u32;
        let hr = stream.Read(
            chunk.as_mut_ptr().cast(),
            chunk.len() as u32,
            Some(&mut read),
        );
        if hr.is_err() {
            return Err("screenshot_unavailable");
        }
        if read == 0 {
            break;
        }
        out.extend_from_slice(&chunk[..read as usize]);
        if out.len() > MAX_PNG_BYTES {
            return Err("payload_too_large");
        }
    }
    encode_png_result(&out)
}

#[cfg(test)]
mod tests {
    use super::*;
    use base64::{engine::general_purpose::STANDARD, Engine as _};

    fn message(payload: serde_json::Value) -> String {
        format!("{PREFIX}{payload}")
    }

    fn url(raw: &str) -> Url {
        Url::parse(raw).unwrap()
    }

    #[test]
    fn parse_accepts_positive_id_and_rejects_malformed_or_oversized_messages() {
        assert_eq!(
            parse(&message(json!({ "id": 42 }))),
            Some(CaptureRequest { id: 42 })
        );
        for bad in [
            message(json!({ "id": 0 })),
            message(json!({ "id": -1 })),
            message(json!({})),
            message(json!({ "id": 1, "extra": true })),
            "werkschaft:notify:{\"id\":1}".to_owned(),
            format!("{PREFIX}not json"),
            format!("{PREFIX}{{\"id\":1}}{}", " ".repeat(MAX_MESSAGE)),
        ] {
            assert_eq!(parse(&bad), None, "{bad:.80}");
        }
    }

    #[test]
    fn authorized_requires_main_window_matching_epoch_and_pinned_dashboard_origin() {
        let pin = url("http://127.0.0.1:49015/");
        let dash = url("http://127.0.0.1:49015/s/abc");
        assert!(authorized(close::MAIN_WINDOW, 3, 3, &dash, Some(&pin)));
        // Secondary window is always refused.
        assert!(!authorized("secondary-1", 3, 3, &dash, Some(&pin)));
        // Stale epoch is refused.
        assert!(!authorized(close::MAIN_WINDOW, 4, 3, &dash, Some(&pin)));
        // Missing pin is refused.
        assert!(!authorized(close::MAIN_WINDOW, 3, 3, &dash, None));
        // Wrong port / origin / scheme is refused.
        assert!(!authorized(
            close::MAIN_WINDOW,
            3,
            3,
            &url("http://127.0.0.1:49016/s/abc"),
            Some(&pin)
        ));
        assert!(!authorized(
            close::MAIN_WINDOW,
            3,
            3,
            &url("https://example.com/"),
            Some(&pin)
        ));
        assert!(!authorized(
            close::MAIN_WINDOW,
            3,
            3,
            &url("tauri://localhost/index.html"),
            Some(&pin)
        ));
    }

    #[test]
    fn encode_png_result_enforces_png_magic_and_8mib_limit() {
        let mut valid = PNG_MAGIC.to_vec();
        valid.extend_from_slice(b"\x00\x00\x00\rIHDRtestpayload");
        let encoded = encode_png_result(&valid).unwrap();
        assert_eq!(encoded, STANDARD.encode(&valid));

        // Exact 8 MiB boundary passes.
        let mut exact = vec![0u8; MAX_PNG_BYTES];
        exact[..PNG_MAGIC.len()].copy_from_slice(PNG_MAGIC);
        assert!(encode_png_result(&exact).is_ok());

        // 8 MiB + 1 byte yields payload_too_large.
        let mut too_large = vec![0u8; MAX_PNG_BYTES + 1];
        too_large[..PNG_MAGIC.len()].copy_from_slice(PNG_MAGIC);
        assert_eq!(encode_png_result(&too_large), Err("payload_too_large"));

        // Non-PNG or truncated bytes yield screenshot_unavailable.
        assert_eq!(encode_png_result(b""), Err("screenshot_unavailable"));
        assert_eq!(encode_png_result(b"not a png"), Err("screenshot_unavailable"));
    }

    #[test]
    fn answer_script_targets_only_the_pinned_top_frame() {
        let ok = answer_script("http://127.0.0.1:49015", 9, Ok("iVBORw=="));
        assert!(ok.starts_with(
            "if (window === window.top && location.origin === \"http://127.0.0.1:49015\") window.__werkschaftCaptureResult?.(9, "
        ));
        assert!(ok.contains("\"ok\":true"));
        assert!(ok.contains("\"png_base64\":\"iVBORw==\""));

        let err = answer_script("http://127.0.0.1:49015", 10, Err("payload_too_large"));
        assert!(err.contains("\"ok\":false"));
        assert!(err.contains("\"error\":\"payload_too_large\""));
    }

    #[test]
    fn in_flight_guard_serializes_captures_and_releases_on_drop() {
        let capturer = Arc::new(Capturer::new(Arc::new(Connection::default())));
        let g1 = capturer.try_acquire();
        assert!(g1.is_some());
        assert!(capturer.try_acquire().is_none());
        drop(g1);
        assert!(capturer.try_acquire().is_some());
    }
}
