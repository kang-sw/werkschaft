//! The WKWebView navigation delegate and the update/notify script-message bridges.

use block2::Block;
use objc2::{
    define_class,
    rc::Weak,
    runtime::{NSObject, ProtocolObject},
    DefinedClass, MainThreadOnly,
};
use objc2_foundation::{NSError, NSObjectProtocol, NSString};
use objc2_web_kit::{
    WKDownload, WKNavigation, WKNavigationAction, WKNavigationActionPolicy, WKNavigationDelegate,
    WKNavigationResponse, WKNavigationResponsePolicy, WKScriptMessage, WKScriptMessageHandler,
    WKUserContentController, WKWebView,
};
use tauri::{AppHandle, Manager};
use url::Url;

use crate::{
    close,
    navigation::{self, Destination, Popup},
    notify,
    shell::Connection,
    updates,
};

pub(super) struct NavigationState {
    pub(super) original: Weak<ProtocolObject<dyn WKNavigationDelegate>>,
    pub(super) connection: std::sync::Weak<Connection>,
    pub(super) dashboard: Option<(AppHandle, usize)>,
}

#[derive(Clone, Copy)]
pub(super) enum Bridge {
    Update,
    Notify,
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
                    Bridge::Update => updates::receive(window, state.epoch, true, source, &text),
                    Bridge::Notify => notify::receive(window, state.epoch, source, &text),
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
