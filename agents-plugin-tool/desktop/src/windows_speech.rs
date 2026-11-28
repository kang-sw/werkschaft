//! Windows native STT backend using WinRT `Windows.Media.SpeechRecognition`.
//!
//! Uses `SpeechRecognizer` + `ContinuousRecognitionSession` with zero audio
//! written to disk. Availability depends on installed Windows speech language
//! packs and the Windows Online Speech Recognition privacy setting (`0x80045509`);
//! Windows is never claimed to be offline (`on_device: false`).

use std::sync::{Arc, Mutex};

use tauri::AppHandle;
use windows::{
    core::{HRESULT, HSTRING},
    Foundation::TypedEventHandler,
    Globalization::Language,
    Media::SpeechRecognition::{
        SpeechContinuousRecognitionCompletedEventArgs,
        SpeechContinuousRecognitionResultGeneratedEventArgs, SpeechContinuousRecognitionSession,
        SpeechRecognitionHypothesisGeneratedEventArgs, SpeechRecognitionResultStatus,
        SpeechRecognizer,
    },
};

use crate::speech::{match_supported_locale, Controller, SpeechEvent};

/// WinRT `SPERR_SPEECH_PRIVACY_POLICY_NOT_DECLINED` (Online speech recognition
/// privacy toggle is off in Windows Settings -> Privacy & security -> Speech).
const HRES_PRIVACY_DISABLED: HRESULT = HRESULT(0x8004_5509_u32 as i32);
/// `SPERR_NOT_FOUND` (no matching installed speech language pack).
const HRES_LANGUAGE_NOT_FOUND: HRESULT = HRESULT(0x8004_503A_u32 as i32);

pub(crate) struct ActiveSession {
    id: u64,
    recognizer: SpeechRecognizer,
    session: SpeechContinuousRecognitionSession,
    hyp_token: i64,
    res_token: i64,
    done_token: i64,
    committed: Arc<Mutex<String>>,
}

impl ActiveSession {
    fn detach_and_close(self, cancel: bool) {
        let _ = self.recognizer.RemoveHypothesisGenerated(self.hyp_token);
        let _ = self.session.RemoveResultGenerated(self.res_token);
        let _ = self.session.RemoveCompleted(self.done_token);
        if cancel {
            if let Ok(op) = self.session.CancelAsync() {
                let _ = op.join();
            }
        }
        let _ = self.recognizer.Close();
    }
}

#[derive(Default)]
pub struct Backend {
    active: Mutex<Option<ActiveSession>>,
}

fn supported_language_tags() -> Vec<String> {
    let Ok(list) = SpeechRecognizer::SupportedTopicLanguages() else {
        return Vec::new();
    };
    let mut tags = Vec::new();
    if let Ok(count) = list.Size() {
        for idx in 0..count {
            if let Ok(lang) = list.GetAt(idx) {
                if let Ok(tag) = lang.LanguageTag() {
                    tags.push(tag.to_string());
                }
            }
        }
    }
    tags
}

fn resolve_language(requested: Option<&str>) -> Result<(Language, String), (&'static str, String)> {
    let supported = supported_language_tags();
    if let Some(req) = requested.filter(|s| !s.trim().is_empty()) {
        if let Some(matched) = match_supported_locale(req, &supported) {
            let lang = Language::CreateLanguage(&HSTRING::from(matched)).map_err(|_| {
                (
                    "language_unavailable",
                    format!("Windows Speech language '{matched}' could not be initialized."),
                )
            })?;
            return Ok((lang, matched.to_owned()));
        }
        return Err((
            "language_unavailable",
            format!(
                "Windows Speech language pack for '{req}' is not installed. Install it in Settings > Time & language > Speech."
            ),
        ));
    }

    let sys = SpeechRecognizer::SystemSpeechLanguage().map_err(|_| {
        (
            "language_unavailable",
            "No Windows Speech language is configured. Configure one in Settings > Time & language > Speech."
                .to_owned(),
        )
    })?;
    let tag = sys.LanguageTag().map(|t| t.to_string()).unwrap_or_default();
    if !supported.is_empty() && !tag.is_empty() && match_supported_locale(&tag, &supported).is_none()
    {
        return Err((
            "language_unavailable",
            format!(
                "Windows Speech language pack for '{tag}' is not installed. Install it in Settings > Time & language > Speech."
            ),
        ));
    }
    Ok((sys, tag))
}

fn map_winrt_error(err: &windows::core::Error) -> (&'static str, String) {
    let code = err.code();
    if code == HRES_PRIVACY_DISABLED {
        return (
            "privacy_disabled",
            "Online speech recognition is disabled. Enable it in Windows Settings > Privacy & security > Speech."
                .to_owned(),
        );
    }
    if code == HRES_LANGUAGE_NOT_FOUND {
        return (
            "language_unavailable",
            "Required Windows Speech language pack is missing. Install it in Settings > Time & language > Speech."
                .to_owned(),
        );
    }
    (
        "recognizer_unavailable",
        format!("Windows Speech Recognition failed ({code:?})."),
    )
}

fn append_utterance(buf: &mut String, piece: &str) {
    let piece = piece.trim();
    if piece.is_empty() {
        return;
    }
    if !buf.is_empty() {
        buf.push(' ');
    }
    buf.push_str(piece);
}

impl Backend {
    pub fn probe_status(&self, locale: Option<&str>) -> SpeechEvent {
        let (lang, tag) = match resolve_language(locale) {
            Ok(resolved) => resolved,
            Err((code, reason)) => {
                return SpeechEvent::Ready {
                    available: false,
                    on_device: false,
                    locale: locale.map(str::to_owned),
                    code: Some(code.to_owned()),
                    reason: Some(reason),
                };
            }
        };
        let recognizer = match SpeechRecognizer::Create(&lang) {
            Ok(r) => r,
            Err(err) => {
                let (code, reason) = map_winrt_error(&err);
                return SpeechEvent::Ready {
                    available: false,
                    on_device: false,
                    locale: Some(tag),
                    code: Some(code.to_owned()),
                    reason: Some(reason),
                };
            }
        };
        let compiled = recognizer.CompileConstraintsAsync().and_then(|op| op.join());
        let _ = recognizer.Close();
        match compiled {
            Ok(result) => match result.Status() {
                Ok(SpeechRecognitionResultStatus::Success) => SpeechEvent::Ready {
                    available: true,
                    on_device: false,
                    locale: Some(tag),
                    code: None,
                    reason: None,
                },
                Ok(SpeechRecognitionResultStatus::TopicLanguageNotSupported) => {
                    SpeechEvent::Ready {
                        available: false,
                        on_device: false,
                        locale: Some(tag),
                        code: Some("language_unavailable".to_owned()),
                        reason: Some(
                            "The selected Windows Speech language does not support dictation."
                                .to_owned(),
                        ),
                    }
                }
                _ => SpeechEvent::Ready {
                    available: false,
                    on_device: false,
                    locale: Some(tag),
                    code: Some("recognizer_unavailable".to_owned()),
                    reason: Some(
                        "Windows Speech Recognition could not compile dictation constraints."
                            .to_owned(),
                    ),
                },
            },
            Err(err) => {
                let (code, reason) = map_winrt_error(&err);
                SpeechEvent::Ready {
                    available: false,
                    on_device: false,
                    locale: Some(tag),
                    code: Some(code.to_owned()),
                    reason: Some(reason),
                }
            }
        }
    }

    pub fn start(
        &self,
        controller: Arc<Controller>,
        app: AppHandle,
        id: u64,
        locale: Option<String>,
    ) {
        std::thread::spawn(move || {
            if !controller.is_active(id) {
                return;
            }
            let (lang, _tag) = match resolve_language(locale.as_deref()) {
                Ok(pair) => pair,
                Err((code, reason)) => {
                    controller.deliver_error(&app, id, code, Some(reason));
                    return;
                }
            };
            let recognizer = match SpeechRecognizer::Create(&lang) {
                Ok(r) => r,
                Err(err) => {
                    let (code, reason) = map_winrt_error(&err);
                    controller.deliver_error(&app, id, code, Some(reason));
                    return;
                }
            };
            match recognizer.CompileConstraintsAsync().and_then(|op| op.join()) {
                Ok(res) if res.Status().ok() == Some(SpeechRecognitionResultStatus::Success) => {}
                Ok(res) => {
                    let _ = recognizer.Close();
                    let (code, reason) = match res.Status().ok() {
                        Some(SpeechRecognitionResultStatus::TopicLanguageNotSupported) => (
                            "language_unavailable",
                            "The selected Windows Speech language does not support dictation."
                                .to_owned(),
                        ),
                        _ => (
                            "recognizer_unavailable",
                            "Windows Speech Recognition failed to prepare dictation.".to_owned(),
                        ),
                    };
                    controller.deliver_error(&app, id, code, Some(reason));
                    return;
                }
                Err(err) => {
                    let _ = recognizer.Close();
                    let (code, reason) = map_winrt_error(&err);
                    controller.deliver_error(&app, id, code, Some(reason));
                    return;
                }
            }
            let session = match recognizer.ContinuousRecognitionSession() {
                Ok(s) => s,
                Err(err) => {
                    let _ = recognizer.Close();
                    let (code, reason) = map_winrt_error(&err);
                    controller.deliver_error(&app, id, code, Some(reason));
                    return;
                }
            };

            let committed = Arc::new(Mutex::new(String::new()));

            let hyp_ctrl = controller.clone();
            let hyp_app = app.clone();
            let hyp_committed = committed.clone();
            let hyp_token = match recognizer.HypothesisGenerated(&TypedEventHandler::new(
                move |_: windows::core::Ref<SpeechRecognizer>,
                      args: windows::core::Ref<SpeechRecognitionHypothesisGeneratedEventArgs>| {
                    if let Some(args) = args.as_ref() {
                        if let Ok(hyp) = args.Hypothesis() {
                            if let Ok(text) = hyp.Text() {
                                let mut combined = hyp_committed.lock().unwrap().clone();
                                append_utterance(&mut combined, &text.to_string());
                                hyp_ctrl.deliver_partial(&hyp_app, id, combined);
                            }
                        }
                    }
                    Ok(())
                },
            )) {
                Ok(tok) => tok,
                Err(err) => {
                    let _ = recognizer.Close();
                    let (code, reason) = map_winrt_error(&err);
                    controller.deliver_error(&app, id, code, Some(reason));
                    return;
                }
            };

            let res_ctrl = controller.clone();
            let res_app = app.clone();
            let res_committed = committed.clone();
            let res_token = match session.ResultGenerated(&TypedEventHandler::new(
                move |_: windows::core::Ref<SpeechContinuousRecognitionSession>,
                      args: windows::core::Ref<SpeechContinuousRecognitionResultGeneratedEventArgs>| {
                    if let Some(args) = args.as_ref() {
                        if let Ok(result) = args.Result() {
                            if let Ok(text) = result.Text() {
                                let mut buf = res_committed.lock().unwrap();
                                append_utterance(&mut buf, &text.to_string());
                                let snapshot = buf.clone();
                                drop(buf);
                                res_ctrl.deliver_partial(&res_app, id, snapshot);
                            }
                        }
                    }
                    Ok(())
                },
            )) {
                Ok(tok) => tok,
                Err(err) => {
                    let _ = recognizer.RemoveHypothesisGenerated(hyp_token);
                    let _ = recognizer.Close();
                    let (code, reason) = map_winrt_error(&err);
                    controller.deliver_error(&app, id, code, Some(reason));
                    return;
                }
            };

            let done_ctrl = controller.clone();
            let done_app = app.clone();
            let done_committed = committed.clone();
            let done_token = match session.Completed(&TypedEventHandler::new(
                move |_: windows::core::Ref<SpeechContinuousRecognitionSession>,
                      args: windows::core::Ref<SpeechContinuousRecognitionCompletedEventArgs>| {
                    let status = args
                        .as_ref()
                        .and_then(|a| a.Status().ok())
                        .unwrap_or(SpeechRecognitionResultStatus::Unknown);
                    if done_ctrl.is_stopping(id) || status == SpeechRecognitionResultStatus::Success
                    {
                        let final_text = done_committed.lock().unwrap().clone();
                        done_ctrl.deliver_final(&done_app, id, final_text);
                    } else if done_ctrl.is_active(id) {
                        let (code, reason) = match status {
                            SpeechRecognitionResultStatus::MicrophoneUnavailable => (
                                "audio_error",
                                "Microphone is unavailable. Check Windows Microphone privacy settings."
                                    .to_owned(),
                            ),
                            SpeechRecognitionResultStatus::TopicLanguageNotSupported => (
                                "language_unavailable",
                                "Windows Speech language pack is not available.".to_owned(),
                            ),
                            SpeechRecognitionResultStatus::NetworkFailure => (
                                "recognizer_unavailable",
                                "Windows online speech recognition requires network connectivity."
                                    .to_owned(),
                            ),
                            SpeechRecognitionResultStatus::UserCanceled => {
                                return Ok(());
                            }
                            _ => (
                                "recognition_error",
                                "Windows Speech Recognition stopped unexpectedly.".to_owned(),
                            ),
                        };
                        done_ctrl.deliver_error(&done_app, id, code, Some(reason));
                    }
                    Ok(())
                },
            )) {
                Ok(tok) => tok,
                Err(err) => {
                    let _ = recognizer.RemoveHypothesisGenerated(hyp_token);
                    let _ = session.RemoveResultGenerated(res_token);
                    let _ = recognizer.Close();
                    let (code, reason) = map_winrt_error(&err);
                    controller.deliver_error(&app, id, code, Some(reason));
                    return;
                }
            };

            if !controller.is_active(id) {
                let _ = recognizer.RemoveHypothesisGenerated(hyp_token);
                let _ = session.RemoveResultGenerated(res_token);
                let _ = session.RemoveCompleted(done_token);
                let _ = recognizer.Close();
                return;
            }

            {
                let mut slot = controller.backend_active().lock().unwrap();
                if let Some(prev) = slot.take() {
                    prev.detach_and_close(true);
                }
                *slot = Some(ActiveSession {
                    id,
                    recognizer: recognizer.clone(),
                    session: session.clone(),
                    hyp_token,
                    res_token,
                    done_token,
                    committed,
                });
            }

            if let Err(err) = session.StartAsync().and_then(|op| op.join()) {
                controller.backend_cancel(id);
                let (code, reason) = map_winrt_error(&err);
                controller.deliver_error(&app, id, code, Some(reason));
            }
        });
    }

    pub fn stop(&self, controller: Arc<Controller>, app: AppHandle, id: u64) {
        let session = {
            let slot = self.active.lock().unwrap();
            slot.as_ref()
                .filter(|active| active.id == id)
                .map(|active| active.session.clone())
        };
        std::thread::spawn(move || {
            if let Some(session) = session {
                if let Ok(op) = session.StopAsync() {
                    let _ = op.join();
                }
            }
            // Release the recognizer and settle with any buffered partial if
            // `Completed` has not already finalized the session.
            controller.deliver_stopped_fallback(&app, id);
            controller.backend_close_if(id, false);
        });
    }

    pub fn cancel(&self, _app: Option<&AppHandle>, id: u64) {
        self.close_if(id, true);
    }

    pub(crate) fn close_if(&self, id: u64, cancel: bool) {
        let prev = {
            let mut slot = self.active.lock().unwrap();
            if slot.as_ref().is_some_and(|active| active.id == id) {
                slot.take()
            } else {
                None
            }
        };
        if let Some(active) = prev {
            active.detach_and_close(cancel);
        }
    }

    pub(crate) fn active_slot(&self) -> &Mutex<Option<ActiveSession>> {
        &self.active
    }
}
