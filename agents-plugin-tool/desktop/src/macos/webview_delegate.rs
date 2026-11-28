//! The WKWebView navigation/UI delegates and the update/notify script-message bridges.

use block2::Block;
use objc2::{
    define_class,
    rc::{Retained, Weak},
    runtime::{NSObject, ProtocolObject},
    DefinedClass, MainThreadOnly,
};
use objc2_foundation::{NSArray, NSError, NSObjectProtocol, NSString, NSURL};
use objc2_web_kit::{
    WKDownload, WKFrameInfo, WKMediaCaptureType, WKNavigation, WKNavigationAction,
    WKNavigationActionPolicy, WKNavigationDelegate, WKNavigationResponse,
    WKNavigationResponsePolicy, WKOpenPanelParameters, WKPermissionDecision, WKScriptMessage,
    WKScriptMessageHandler, WKSecurityOrigin, WKUIDelegate, WKUserContentController, WKWebView,
    WKWebViewConfiguration, WKWindowFeatures,
};
use tauri::{AppHandle, Manager};
use url::Url;

use crate::{
    capture, close,
    navigation::{self, Destination, Popup},
    notify,
    permissions::{self, MediaCaptureKind},
    shell::Connection,
    speech,
    updates,
};

pub(super) struct NavigationState {
    pub(super) original: Weak<ProtocolObject<dyn WKNavigationDelegate>>,
    pub(super) connection: std::sync::Weak<Connection>,
    pub(super) dashboard: Option<(AppHandle, usize)>,
}

pub(super) struct UIState {
    pub(super) original: Weak<ProtocolObject<dyn WKUIDelegate>>,
    pub(super) connection: std::sync::Weak<Connection>,
}

#[derive(Clone, Copy)]
pub(super) enum Bridge {
    Update,
    /// The recovery page's fixed actions, present with or without an updater.
    Recovery,
    Notify,
    Capture,
    Speech,
}

pub(super) struct BridgeState {
    pub(super) app: AppHandle,
    pub(super) epoch: usize,
    pub(super) bridge: Bridge,
}

define_class!(
    #[unsafe(super(NSObject))]
    #[thread_kind = MainThreadOnly]
    #[ivars = BridgeState]
    pub(super) struct BridgeMessages;

    unsafe impl NSObjectProtocol for BridgeMessages {}

    unsafe impl WKScriptMessageHandler for BridgeMessages {
        #[unsafe(method(userContentController:didReceiveScriptMessage:))]
        fn receive(&self, _: &WKUserContentController, message: &WKScriptMessage) {
            let frame = unsafe { message.frameInfo() };
            if !unsafe { frame.isMainFrame() } {
                return;
            }
            let raw = unsafe { frame.request().URL().and_then(|url| url.absoluteString()) };
            let Some(source) = raw.and_then(|url| Url::parse(&url.to_string()).ok()) else {
                return;
            };
            let Ok(text) = (unsafe { message.body() }).downcast::<NSString>() else {
                return;
            };
            let state = self.ivars();
            if let Some(window) = state.app.get_webview_window("main") {
                let text = text.to_string();
                match state.bridge {
                    Bridge::Update | Bridge::Recovery => {
                        updates::receive(window, state.epoch, true, source, &text)
                    }
                    Bridge::Notify => notify::receive(window, state.epoch, source, &text),
                    Bridge::Capture => capture::receive(window, state.epoch, source, &text),
                    Bridge::Speech => speech::receive(window, state.epoch, source, &text),
                }
            }
        }
    }
);

define_class!(
    #[unsafe(super(NSObject))]
    #[thread_kind = MainThreadOnly]
    #[ivars = NavigationState]
    pub(super) struct NavigationDelegate;

    unsafe impl NSObjectProtocol for NavigationDelegate {}

    unsafe impl WKNavigationDelegate for NavigationDelegate {
        #[unsafe(method(webView:decidePolicyForNavigationAction:decisionHandler:))]
        fn action(
            &self,
            webview: &WKWebView,
            action: &WKNavigationAction,
            reply: &Block<dyn Fn(WKNavigationActionPolicy)>,
        ) {
            let raw = unsafe { action.request().URL().and_then(|url| url.absoluteString()) };
            let allowed = raw
                .and_then(|url| Url::parse(&url.to_string()).ok())
                .is_some_and(|url| {
                    let target = unsafe { action.targetFrame().map(|frame| frame.isMainFrame()) };
                    self.allows(url, target)
                });
            if allowed {
                if let Some(original) = self.ivars().original.load() {
                    unsafe {
                        original.webView_decidePolicyForNavigationAction_decisionHandler(
                            webview, action, reply,
                        );
                    }
                    return;
                }
            }
            reply.call((WKNavigationActionPolicy::Cancel,));
        }

        #[unsafe(method(webView:decidePolicyForNavigationResponse:decisionHandler:))]
        fn response(
            &self,
            webview: &WKWebView,
            response: &WKNavigationResponse,
            reply: &Block<dyn Fn(WKNavigationResponsePolicy)>,
        ) {
            let raw = unsafe {
                response
                    .response()
                    .URL()
                    .and_then(|url| url.absoluteString())
            };
            let allowed = raw
                .and_then(|url| Url::parse(&url.to_string()).ok())
                .is_some_and(|url| self.allows(url, Some(unsafe { response.isForMainFrame() })));
            if allowed {
                if let Some(original) = self.ivars().original.load() {
                    unsafe {
                        original.webView_decidePolicyForNavigationResponse_decisionHandler(
                            webview, response, reply,
                        );
                    }
                    return;
                }
            }
            reply.call((WKNavigationResponsePolicy::Cancel,));
        }

        // Wry's delegate attaches its download delegate here. Without these
        // forwards WebKit starts the download with no delegate and drops it.
        // Only navigations `action`/`response` allowed can reach a download.
        #[unsafe(method(webView:navigationAction:didBecomeDownload:))]
        fn action_download(
            &self,
            webview: &WKWebView,
            action: &WKNavigationAction,
            download: &WKDownload,
        ) {
            if let Some(original) = self.ivars().original.load() {
                unsafe {
                    original.webView_navigationAction_didBecomeDownload(webview, action, download);
                }
            }
        }

        #[unsafe(method(webView:navigationResponse:didBecomeDownload:))]
        fn response_download(
            &self,
            webview: &WKWebView,
            response: &WKNavigationResponse,
            download: &WKDownload,
        ) {
            if let Some(original) = self.ivars().original.load() {
                unsafe {
                    original
                        .webView_navigationResponse_didBecomeDownload(webview, response, download);
                }
            }
        }

        #[unsafe(method(webView:didCommitNavigation:))]
        fn committed(&self, webview: &WKWebView, navigation: Option<&WKNavigation>) {
            if let Some((app, _)) = self.ivars().dashboard.as_ref() {
                speech::teardown(app, "navigation");
            }
            if let Some(original) = self.ivars().original.load() {
                unsafe {
                    original.webView_didCommitNavigation(webview, navigation);
                }
            }
        }

        #[unsafe(method(webView:didFinishNavigation:))]
        fn finished(&self, webview: &WKWebView, navigation: Option<&WKNavigation>) {
            if let Some(original) = self.ivars().original.load() {
                unsafe {
                    original.webView_didFinishNavigation(webview, navigation);
                }
            }
        }

        #[unsafe(method(webView:didFailProvisionalNavigation:withError:))]
        fn provisional_failed(&self, _: &WKWebView, _: Option<&WKNavigation>, error: &NSError) {
            self.load_failed(error);
        }

        #[unsafe(method(webView:didFailNavigation:withError:))]
        fn failed(&self, _: &WKWebView, _: Option<&WKNavigation>, error: &NSError) {
            self.load_failed(error);
        }

        #[unsafe(method(webViewWebContentProcessDidTerminate:))]
        fn renderer_failed(&self, _: &WKWebView) {
            self.recover("Dashboard renderer stopped");
        }
    }
);

define_class!(
    #[unsafe(super(NSObject))]
    #[thread_kind = MainThreadOnly]
    #[ivars = UIState]
    pub(super) struct UIDelegate;

    unsafe impl NSObjectProtocol for UIDelegate {}

    unsafe impl WKUIDelegate for UIDelegate {
        #[unsafe(method(webView:requestMediaCapturePermissionForOrigin:initiatedByFrame:type:decisionHandler:))]
        fn media_capture_permission(
            &self,
            _: &WKWebView,
            origin: &WKSecurityOrigin,
            _: &WKFrameInfo,
            capture_type: WKMediaCaptureType,
            decision_handler: &Block<dyn Fn(WKPermissionDecision)>,
        ) {
            let kind = match capture_type {
                WKMediaCaptureType::Microphone => MediaCaptureKind::Microphone,
                WKMediaCaptureType::Camera => MediaCaptureKind::Camera,
                WKMediaCaptureType::CameraAndMicrophone => MediaCaptureKind::CameraAndMicrophone,
                _ => MediaCaptureKind::Other,
            };
            let protocol = unsafe { origin.protocol() }.to_string();
            let host = unsafe { origin.host() }.to_string();
            let raw_port = unsafe { origin.port() };
            let port = u16::try_from(raw_port).ok().filter(|p| *p > 0);
            let pin = self
                .ivars()
                .connection
                .upgrade()
                .and_then(|state| state.pinned_url());
            let decision = if permissions::macos_media_capture_allowed(
                &protocol,
                &host,
                port,
                kind,
                pin.as_ref(),
            ) {
                WKPermissionDecision::Grant
            } else {
                WKPermissionDecision::Deny
            };
            decision_handler.call((decision,));
        }

        // Forward Wry's file-open panel and popup-window hooks so replacing
        // WKUIDelegate preserves existing upload and secondary-window behavior.
        #[unsafe(method(webView:runOpenPanelWithParameters:initiatedByFrame:completionHandler:))]
        fn run_open_panel(
            &self,
            webview: &WKWebView,
            parameters: &WKOpenPanelParameters,
            frame: &WKFrameInfo,
            completion_handler: &Block<dyn Fn(*mut NSArray<NSURL>)>,
        ) {
            if let Some(original) = self.ivars().original.load() {
                unsafe {
                    original
                        .webView_runOpenPanelWithParameters_initiatedByFrame_completionHandler(
                            webview,
                            parameters,
                            frame,
                            completion_handler,
                        );
                }
                return;
            }
            completion_handler.call((std::ptr::null_mut(),));
        }

        #[unsafe(method_id(webView:createWebViewWithConfiguration:forNavigationAction:windowFeatures:))]
        fn create_web_view(
            &self,
            webview: &WKWebView,
            configuration: &WKWebViewConfiguration,
            navigation_action: &WKNavigationAction,
            window_features: &WKWindowFeatures,
        ) -> Option<Retained<WKWebView>> {
            self.ivars().original.load().and_then(|original| unsafe {
                original
                    .webView_createWebViewWithConfiguration_forNavigationAction_windowFeatures(
                        webview,
                        configuration,
                        navigation_action,
                        window_features,
                    )
            })
        }
    }
);

impl NavigationDelegate {
    fn allows(&self, url: Url, target: Option<bool>) -> bool {
        let pin = self
            .ivars()
            .connection
            .upgrade()
            .and_then(|state| state.pinned_url());
        if target == Some(false) {
            return navigation::frame_allowed(&url, pin.as_ref());
        }
        if target.is_none() {
            // WKUIDelegate handles these through the shared popup policy.
            return navigation::popup(&url, pin.as_ref()) != Popup::Block;
        }
        match navigation::destination(&url, pin.as_ref()) {
            Destination::Dashboard | Destination::Local => true,
            Destination::Browser => {
                navigation::open_browser(url);
                false
            }
            Destination::Block => false,
        }
    }

    fn load_failed(&self, error: &NSError) {
        // NSURLErrorCancelled and WebKit's policy-change interruption occur
        // when an otherwise healthy navigation was deliberately canceled.
        let code = error.code();
        if !matches!(code, -999 | 102) {
            self.recover("Dashboard navigation failed");
        }
    }

    fn recover(&self, reason: &'static str) {
        let (Some(connection), Some((app, epoch))) = (
            self.ivars().connection.upgrade(),
            self.ivars().dashboard.clone(),
        ) else {
            return;
        };
        // Wait until WebKit returns from its delegate callback before loading.
        std::thread::spawn(move || {
            let target = app.clone();
            let _ = app.run_on_main_thread(move || {
                if connection.window_epoch() == epoch {
                    if let Some(window) = target.get_webview_window(close::MAIN_WINDOW) {
                        connection.recover(window, reason);
                    }
                }
            });
        });
    }
}
