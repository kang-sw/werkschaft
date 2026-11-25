use url::Url;

use crate::navigation::{destination, Destination};

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum MediaCaptureKind {
    Microphone,
    Camera,
    CameraAndMicrophone,
    Other,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum WindowsPermissionKind {
    ClipboardRead,
    Microphone,
    Other,
}

/// Decides whether WKWebView on macOS may grant a media-capture permission request.
/// Only microphone capture on the pinned local dashboard origin is allowed;
/// camera, combined camera+microphone, and all non-dashboard origins are denied.
#[cfg_attr(not(target_os = "macos"), allow(dead_code))]
pub fn macos_media_capture_allowed(
    protocol: &str,
    host: &str,
    port: Option<u16>,
    kind: MediaCaptureKind,
    pinned: Option<&Url>,
) -> bool {
    if kind != MediaCaptureKind::Microphone {
        return false;
    }
    let Some(origin) = security_origin_url(protocol, host, port) else {
        return false;
    };
    destination(&origin, pinned) == Destination::Dashboard
}

/// Decides whether WebView2 on Windows may grant a permission request.
/// Only user-initiated clipboard-read and microphone requests on the pinned
/// local dashboard origin with transient (non-persisted) permission support are allowed.
#[cfg_attr(not(windows), allow(dead_code))]
pub fn windows_permission_allowed(
    kind: WindowsPermissionKind,
    uri: &str,
    pinned: Option<&Url>,
    user_initiated: bool,
    supports_transient: bool,
) -> bool {
    if !matches!(
        kind,
        WindowsPermissionKind::ClipboardRead | WindowsPermissionKind::Microphone
    ) {
        return false;
    }
    if !user_initiated || !supports_transient {
        return false;
    }
    let Ok(url) = Url::parse(uri) else {
        return false;
    };
    destination(&url, pinned) == Destination::Dashboard
}

#[cfg_attr(not(target_os = "macos"), allow(dead_code))]
fn security_origin_url(protocol: &str, host: &str, port: Option<u16>) -> Option<Url> {
    if protocol.is_empty() || host.is_empty() {
        return None;
    }
    if protocol.chars().any(|c| !c.is_ascii_alphanumeric() && c != '+' && c != '-' && c != '.') {
        return None;
    }
    if host.chars().any(|c| c.is_ascii_whitespace() || matches!(c, '@' | '/' | '?' | '#')) {
        return None;
    }
    let formatted_host = if host.contains(':') && !host.starts_with('[') && !host.ends_with(']') {
        format!("[{host}]")
    } else {
        host.to_string()
    };
    let raw = match port {
        Some(port) if port > 0 => format!("{protocol}://{formatted_host}:{port}/"),
        _ => format!("{protocol}://{formatted_host}/"),
    };
    let url = Url::parse(&raw).ok()?;
    if !url.username().is_empty() || url.password().is_some() {
        return None;
    }
    Some(url)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn pin() -> Url {
        Url::parse("http://127.0.0.1:39417/").unwrap()
    }

    #[test]
    fn macos_grants_microphone_only_for_pinned_dashboard_origin() {
        let dashboard = pin();
        assert!(macos_media_capture_allowed(
            "http",
            "127.0.0.1",
            Some(39417),
            MediaCaptureKind::Microphone,
            Some(&dashboard),
        ));

        let ipv6_pin = Url::parse("http://[::1]:39417/").unwrap();
        assert!(macos_media_capture_allowed(
            "http",
            "::1",
            Some(39417),
            MediaCaptureKind::Microphone,
            Some(&ipv6_pin),
        ));
        assert!(macos_media_capture_allowed(
            "http",
            "[::1]",
            Some(39417),
            MediaCaptureKind::Microphone,
            Some(&ipv6_pin),
        ));

        let default_port_pin = Url::parse("http://127.0.0.1/").unwrap();
        assert!(macos_media_capture_allowed(
            "http",
            "127.0.0.1",
            None,
            MediaCaptureKind::Microphone,
            Some(&default_port_pin),
        ));
        assert!(macos_media_capture_allowed(
            "http",
            "127.0.0.1",
            Some(0),
            MediaCaptureKind::Microphone,
            Some(&default_port_pin),
        ));
    }

    #[test]
    fn macos_denies_non_microphone_kinds_even_on_dashboard_origin() {
        let dashboard = pin();
        for kind in [
            MediaCaptureKind::Camera,
            MediaCaptureKind::CameraAndMicrophone,
            MediaCaptureKind::Other,
        ] {
            assert!(!macos_media_capture_allowed(
                "http",
                "127.0.0.1",
                Some(39417),
                kind,
                Some(&dashboard),
            ));
        }
    }

    #[test]
    fn macos_denies_non_dashboard_and_malformed_origins() {
        let dashboard = pin();
        let denied = [
            ("http", "127.0.0.1", Some(39418)),
            ("http", "127.0.0.1", None),
            ("http", "localhost", Some(39417)),
            ("http", "::1", Some(39417)),
            ("https", "127.0.0.1", Some(39417)),
            ("http", "127.0.0.1.evil.example", Some(39417)),
            ("https", "example.com", Some(443)),
            ("tauri", "localhost", None),
            ("file", "", None),
            ("about", "blank", None),
            ("http", "user@127.0.0.1", Some(39417)),
            ("http", "127.0.0.1/path", Some(39417)),
            ("", "127.0.0.1", Some(39417)),
            ("http", "", Some(39417)),
        ];
        for (protocol, host, port) in denied {
            assert!(
                !macos_media_capture_allowed(
                    protocol,
                    host,
                    port,
                    MediaCaptureKind::Microphone,
                    Some(&dashboard),
                ),
                "expected deny for {protocol}://{host}:{port:?}"
            );
        }
        assert!(!macos_media_capture_allowed(
            "http",
            "127.0.0.1",
            Some(39417),
            MediaCaptureKind::Microphone,
            None,
        ));
    }

    #[test]
    fn windows_grants_user_initiated_transient_clipboard_and_microphone_on_dashboard() {
        let dashboard = pin();
        for kind in [
            WindowsPermissionKind::ClipboardRead,
            WindowsPermissionKind::Microphone,
        ] {
            assert!(windows_permission_allowed(
                kind,
                "http://127.0.0.1:39417/s/session-1",
                Some(&dashboard),
                true,
                true,
            ));
        }
    }

    #[test]
    fn windows_denies_non_user_initiated_non_transient_other_kinds_and_other_origins() {
        let dashboard = pin();
        // Not user-initiated
        assert!(!windows_permission_allowed(
            WindowsPermissionKind::Microphone,
            "http://127.0.0.1:39417/",
            Some(&dashboard),
            false,
            true,
        ));
        // Lacks SetSavesInProfile(false) support
        assert!(!windows_permission_allowed(
            WindowsPermissionKind::Microphone,
            "http://127.0.0.1:39417/",
            Some(&dashboard),
            true,
            false,
        ));
        // Unsupported permission kind (e.g., camera/geolocation)
        assert!(!windows_permission_allowed(
            WindowsPermissionKind::Other,
            "http://127.0.0.1:39417/",
            Some(&dashboard),
            true,
            true,
        ));
        // No pinned dashboard URL
        assert!(!windows_permission_allowed(
            WindowsPermissionKind::Microphone,
            "http://127.0.0.1:39417/",
            None,
            true,
            true,
        ));
        // Non-dashboard or malformed URIs
        for uri in [
            "http://127.0.0.1:39418/",
            "http://localhost:39417/",
            "https://127.0.0.1:39417/",
            "https://example.com/",
            "http://tauri.localhost/index.html",
            "tauri://localhost/index.html",
            "about:blank",
            "blob:http://127.0.0.1:39417/abc",
            "http://user@127.0.0.1:39417/",
            "not a url",
            "",
        ] {
            assert!(
                !windows_permission_allowed(
                    WindowsPermissionKind::Microphone,
                    uri,
                    Some(&dashboard),
                    true,
                    true,
                ),
                "expected deny for {uri}"
            );
        }
    }
}
