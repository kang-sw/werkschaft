//! macOS native STT backend using `SFSpeechRecognizer` + `AVAudioEngine`.
//!
//! Audio buffers flow strictly in memory from `AVAudioEngine.inputNode`'s tap
//! directly into `SFSpeechAudioBufferRecognitionRequest.appendAudioPCMBuffer`:
//! zero audio bytes are ever written to disk. On stop, cancel, hide, minimize,
//! or system sleep the tap is removed and the engine is stopped and reset
//! immediately to release the microphone handle.

use std::{
    cell::RefCell,
    ptr::NonNull,
    sync::Arc,
    time::Duration,
};

use block2::RcBlock;
use objc2::{class, msg_send, rc::Retained, runtime::AnyObject, AnyThread};
use objc2_avf_audio::{AVAudioEngine, AVAudioPCMBuffer, AVAudioTime};
use objc2_foundation::{ns_string, NSError, NSLocale, NSString};
use objc2_speech::{
    SFSpeechAudioBufferRecognitionRequest, SFSpeechRecognitionResult, SFSpeechRecognitionTask,
    SFSpeechRecognizer, SFSpeechRecognizerAuthorizationStatus,
};
use tauri::AppHandle;

use crate::speech::{Controller, SpeechEvent};

const STOP_FINAL_TIMEOUT: Duration = Duration::from_millis(2500);

/// Objective-C capture state kept strictly on the macOS main thread.
struct ActiveCapture {
    id: u64,
    engine: Retained<AVAudioEngine>,
    request: Retained<SFSpeechAudioBufferRecognitionRequest>,
    task: Retained<SFSpeechRecognitionTask>,
    _recognizer: Retained<SFSpeechRecognizer>,
    _tap_block: RcBlock<dyn Fn(NonNull<AVAudioPCMBuffer>, NonNull<AVAudioTime>)>,
    _result_block: RcBlock<dyn Fn(*mut SFSpeechRecognitionResult, *mut NSError)>,
    audio_stopped: bool,
}

impl ActiveCapture {
    fn stop_audio(&mut self) {
        if self.audio_stopped {
            return;
        }
        self.audio_stopped = true;
        unsafe {
            let input = self.engine.inputNode();
            input.removeTapOnBus(0);
            self.engine.stop();
            self.engine.reset();
            self.request.endAudio();
        }
    }

    fn cancel_all(&mut self) {
        self.stop_audio();
        unsafe {
            self.task.cancel();
        }
    }
}

thread_local! {
    static ACTIVE: RefCell<Option<ActiveCapture>> = const { RefCell::new(None) };
    static LIFECYCLE_OBSERVERS: RefCell<Vec<Retained<AnyObject>>> = const { RefCell::new(Vec::new()) };
}

#[derive(Default)]
pub struct Backend;

impl Backend {
    pub fn probe_status(&self, locale: Option<&str>) -> SpeechEvent {
        let auth = unsafe { SFSpeechRecognizer::authorizationStatus() };
        if auth == SFSpeechRecognizerAuthorizationStatus::Denied {
            return SpeechEvent::Ready {
                available: false,
                on_device: false,
                locale: locale.map(str::to_owned),
                code: Some("permission_denied".to_owned()),
                reason: Some(
                    "macOS Speech Recognition permission is denied. Enable Werkschaft in System Settings > Privacy & Security > Speech Recognition."
                        .to_owned(),
                ),
            };
        }
        if auth == SFSpeechRecognizerAuthorizationStatus::Restricted {
            return SpeechEvent::Ready {
                available: false,
                on_device: false,
                locale: locale.map(str::to_owned),
                code: Some("permission_denied".to_owned()),
                reason: Some("Speech Recognition is restricted on this Mac.".to_owned()),
            };
        }
        let Some(recognizer) = create_recognizer(locale) else {
            return SpeechEvent::Ready {
                available: false,
                on_device: false,
                locale: locale.map(str::to_owned),
                code: Some("language_unavailable".to_owned()),
                reason: Some(
                    "macOS Speech Recognition does not support the requested language.".to_owned(),
                ),
            };
        };
        let resolved_locale = unsafe { recognizer.locale().localeIdentifier().to_string() };
        let on_device = unsafe { recognizer.supportsOnDeviceRecognition() };
        let available = unsafe { recognizer.isAvailable() };
        if !available {
            return SpeechEvent::Ready {
                available: false,
                on_device,
                locale: Some(resolved_locale),
                code: Some("recognizer_unavailable".to_owned()),
                reason: Some(
                    "macOS Speech Recognition is currently unavailable for this language. Check Dictation settings or network availability."
                        .to_owned(),
                ),
            };
        }
        SpeechEvent::Ready {
            available: true,
            on_device,
            locale: Some(resolved_locale),
            code: None,
            reason: None,
        }
    }

    pub fn start(
        &self,
        controller: Arc<Controller>,
        app: AppHandle,
        id: u64,
        locale: Option<String>,
    ) {
        let target_app = app.clone();
        let _ = app.run_on_main_thread(move || {
            if !controller.is_active(id) {
                return;
            }
            let auth = unsafe { SFSpeechRecognizer::authorizationStatus() };
            match auth {
                SFSpeechRecognizerAuthorizationStatus::Authorized => {
                    begin_capture(controller, target_app, id, locale.as_deref());
                }
                SFSpeechRecognizerAuthorizationStatus::NotDetermined => {
                    let ctrl = controller.clone();
                    let app_cb = target_app.clone();
                    let locale_cb = locale.clone();
                    let block = RcBlock::new(move |status: SFSpeechRecognizerAuthorizationStatus| {
                        let ctrl2 = ctrl.clone();
                        let app2 = app_cb.clone();
                        let locale2 = locale_cb.clone();
                        let _ = app_cb.run_on_main_thread(move || {
                            if !ctrl2.is_active(id) {
                                return;
                            }
                            if status == SFSpeechRecognizerAuthorizationStatus::Authorized {
                                begin_capture(ctrl2, app2, id, locale2.as_deref());
                            } else {
                                ctrl2.deliver_error(
                                    &app2,
                                    id,
                                    "permission_denied",
                                    Some(
                                        "macOS Speech Recognition permission was denied. Enable Werkschaft in System Settings > Privacy & Security > Speech Recognition."
                                            .to_owned(),
                                    ),
                                );
                            }
                        });
                    });
                    unsafe {
                        SFSpeechRecognizer::requestAuthorization(&block);
                    }
                }
                _ => {
                    controller.deliver_error(
                        &target_app,
                        id,
                        "permission_denied",
                        Some(
                            "macOS Speech Recognition permission is denied or restricted. Enable Werkschaft in System Settings > Privacy & Security > Speech Recognition."
                                .to_owned(),
                        ),
                    );
                }
            }
        });
    }

    pub fn stop(&self, controller: Arc<Controller>, app: AppHandle, id: u64) {
        let _ = app.run_on_main_thread(move || {
            ACTIVE.with(|cell| {
                if let Some(active) = cell.borrow_mut().as_mut() {
                    if active.id == id {
                        active.stop_audio();
                    }
                }
            });
        });
        // Guarantee the frontend never hangs in `refining` if Speech.framework
        // finishes without emitting a final result after `endAudio()`.
        let timeout_app = app.clone();
        std::thread::spawn(move || {
            std::thread::sleep(STOP_FINAL_TIMEOUT);
            if !controller.is_stopping(id) {
                return;
            }
            let deliver_app = timeout_app.clone();
            let _ = timeout_app.run_on_main_thread(move || {
                clear_active(id, true);
                controller.deliver_stopped_fallback(&deliver_app, id);
            });
        });
    }

    pub fn cancel(&self, app: Option<&AppHandle>, id: u64) {
        // Cancel runs synchronously when already on the main thread (e.g.
        // hide/minimize/tray/close), ensuring immediate mic handle release.
        if objc2_foundation::MainThreadMarker::new().is_some() {
            clear_active(id, true);
        } else if let Some(app) = app {
            let _ = app.run_on_main_thread(move || clear_active(id, true));
        }
    }
}

fn clear_active(id: u64, cancel_task: bool) {
    ACTIVE.with(|cell| {
        let mut slot = cell.borrow_mut();
        if slot.as_ref().is_some_and(|active| active.id == id) {
            if let Some(mut active) = slot.take() {
                if cancel_task {
                    active.cancel_all();
                } else {
                    active.stop_audio();
                }
            }
        }
    });
}

fn create_recognizer(locale: Option<&str>) -> Option<Retained<SFSpeechRecognizer>> {
    unsafe {
        if let Some(tag) = locale.filter(|s| !s.trim().is_empty()) {
            let ns_tag = NSString::from_str(tag);
            let ns_locale = NSLocale::initWithLocaleIdentifier(NSLocale::alloc(), &ns_tag);
            SFSpeechRecognizer::initWithLocale(SFSpeechRecognizer::alloc(), &ns_locale)
        } else {
            SFSpeechRecognizer::init(SFSpeechRecognizer::alloc())
        }
    }
}

fn begin_capture(
    controller: Arc<Controller>,
    app: AppHandle,
    id: u64,
    locale: Option<&str>,
) {
    // Drop any leftover capture before starting.
    ACTIVE.with(|cell| {
        if let Some(mut prev) = cell.borrow_mut().take() {
            prev.cancel_all();
        }
    });

    let Some(recognizer) = create_recognizer(locale) else {
        controller.deliver_error(
            &app,
            id,
            "language_unavailable",
            Some("macOS Speech Recognition does not support the requested language.".to_owned()),
        );
        return;
    };

    if !unsafe { recognizer.isAvailable() } {
        controller.deliver_error(
            &app,
            id,
            "recognizer_unavailable",
            Some("macOS Speech Recognition is currently unavailable for this language.".to_owned()),
        );
        return;
    }

    let request = unsafe { SFSpeechAudioBufferRecognitionRequest::new() };
    unsafe {
        request.setShouldReportPartialResults(true);
        request.setAddsPunctuation(true);
        if recognizer.supportsOnDeviceRecognition() {
            request.setRequiresOnDeviceRecognition(true);
        }
    }

    let engine = unsafe { AVAudioEngine::init(AVAudioEngine::alloc()) };
    let input = unsafe { engine.inputNode() };
    let format = unsafe { input.outputFormatForBus(0) };
    let sample_rate = unsafe { format.sampleRate() };
    let channels = unsafe { format.channelCount() };
    if sample_rate <= 0.0 || channels == 0 {
        controller.deliver_error(
            &app,
            id,
            "audio_error",
            Some(
                "Microphone input is unavailable. Enable microphone access for Werkschaft in System Settings > Privacy & Security > Microphone."
                    .to_owned(),
            ),
        );
        return;
    }

    let request_for_tap = request.clone();
    let tap_block = RcBlock::new(
        move |buffer: NonNull<AVAudioPCMBuffer>, _when: NonNull<AVAudioTime>| {
            unsafe {
                request_for_tap.appendAudioPCMBuffer(buffer.as_ref());
            }
        },
    );

    unsafe {
        input.installTapOnBus_bufferSize_format_block(
            0,
            1024,
            Some(&format),
            &*tap_block as *const _ as *mut _,
        );
        engine.prepare();
    }

    if let Err(err) = unsafe { engine.startAndReturnError() } {
        unsafe {
            input.removeTapOnBus(0);
            engine.stop();
            engine.reset();
        }
        let reason = err.localizedDescription().to_string();
        controller.deliver_error(&app, id, "audio_error", Some(reason));
        return;
    }

    let ctrl_cb = controller.clone();
    let app_cb = app.clone();
    let result_block = RcBlock::new(
        move |result_ptr: *mut SFSpeechRecognitionResult, error_ptr: *mut NSError| {
            let mut finished = false;
            if !result_ptr.is_null() {
                let result = unsafe { &*result_ptr };
                let text = unsafe { result.bestTranscription().formattedString().to_string() };
                let is_final = unsafe { result.isFinal() };
                if is_final {
                    finished = true;
                    clear_active(id, false);
                    ctrl_cb.deliver_final(&app_cb, id, text);
                } else {
                    ctrl_cb.deliver_partial(&app_cb, id, text);
                }
            }
            if !error_ptr.is_null() && !finished {
                let error = unsafe { &*error_ptr };
                let code = error.code();
                let desc = error.localizedDescription().to_string();
                clear_active(id, false);
                if ctrl_cb.is_stopping(id) {
                    // Stopping with silence or Apple's post-endAudio cancel
                    // settles cleanly with the last partial hypothesis.
                    ctrl_cb.deliver_stopped_fallback(&app_cb, id);
                } else if ctrl_cb.is_active(id) {
                    let mapped = match code {
                        102 => "language_unavailable",
                        201 => "recognizer_unavailable",
                        1700 => "permission_denied",
                        _ => "recognition_error",
                    };
                    ctrl_cb.deliver_error(&app_cb, id, mapped, Some(desc));
                }
            }
        },
    );

    let task = unsafe {
        recognizer.recognitionTaskWithRequest_resultHandler(&request, &result_block)
    };

    ACTIVE.with(|cell| {
        *cell.borrow_mut() = Some(ActiveCapture {
            id,
            engine,
            request,
            task,
            _recognizer: recognizer,
            _tap_block: tap_block,
            _result_block: result_block,
            audio_stopped: false,
        });
    });
}

/// Installs macOS window/app/sleep observers so minimize, app hide, and system
/// sleep immediately tear down native speech recognition and release mic handles.
pub(super) fn install_lifecycle_observers(app: AppHandle) {
    LIFECYCLE_OBSERVERS.with(|slot| {
        if !slot.borrow().is_empty() {
            return;
        }
        let default_center: Retained<AnyObject> =
            unsafe { msg_send![class!(NSNotificationCenter), defaultCenter] };
        let workspace: Option<Retained<AnyObject>> =
            unsafe { msg_send![class!(NSWorkspace), sharedWorkspace] };
        let workspace_center: Option<Retained<AnyObject>> = workspace
            .as_deref()
            .and_then(|ws| unsafe { msg_send![ws, notificationCenter] });

        let add = |center: &AnyObject, name: &NSString, reason: &'static str, app: AppHandle| -> Option<Retained<AnyObject>> {
            let block = RcBlock::new(move |_: *mut AnyObject| {
                crate::speech::teardown(&app, reason);
            });
            unsafe {
                msg_send![
                    center,
                    addObserverForName: name,
                    object: None::<&AnyObject>,
                    queue: None::<&AnyObject>,
                    usingBlock: &*block
                ]
            }
        };

        let mut tokens = Vec::new();
        if let Some(tok) = add(
            &default_center,
            ns_string!("NSWindowWillMiniaturizeNotification"),
            "minimized",
            app.clone(),
        ) {
            tokens.push(tok);
        }
        if let Some(tok) = add(
            &default_center,
            ns_string!("NSApplicationWillHideNotification"),
            "hidden",
            app.clone(),
        ) {
            tokens.push(tok);
        }
        if let Some(ws_center) = workspace_center.as_deref() {
            if let Some(tok) = add(
                ws_center,
                ns_string!("NSWorkspaceWillSleepNotification"),
                "suspend",
                app,
            ) {
                tokens.push(tok);
            }
        }
        *slot.borrow_mut() = tokens;
    });
}
