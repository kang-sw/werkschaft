// Common provider-agnostic voice input interface (window.WsVoiceInput) and
// composer push-to-talk integration.
(function () {
  "use strict";

  var MIME_PREFERENCES = [
    { mime: "audio/webm;codecs=opus", ext: "webm" },
    { mime: "audio/webm", ext: "webm" },
    { mime: "audio/mp4", ext: "m4a" },
    { mime: "audio/ogg;codecs=opus", ext: "ogg" }
  ];

  function translate(key) {
    if (typeof window.wsT === "function") return window.wsT(key);
    return key;
  }

  function extensionForMime(mime) {
    var base = String(mime || "").split(";")[0].trim().toLowerCase();
    switch (base) {
      case "audio/webm": return "webm";
      case "audio/mp4":
      case "audio/m4a":
      case "audio/x-m4a": return "m4a";
      case "audio/ogg": return "ogg";
      case "audio/mpeg":
      case "audio/mp3": return "mp3";
      case "audio/wav":
      case "audio/x-wav":
      case "audio/wave": return "wav";
      default: return "webm";
    }
  }

  function selectRecorderCodec(MediaRecorderCtor) {
    if (!MediaRecorderCtor) return { mode: "pcm", mime: "audio/wav", ext: "wav" };
    if (typeof MediaRecorderCtor.isTypeSupported !== "function") {
      return { mode: "recorder", mime: "", ext: "webm" };
    }
    for (var i = 0; i < MIME_PREFERENCES.length; i++) {
      var pref = MIME_PREFERENCES[i];
      try {
        if (MediaRecorderCtor.isTypeSupported(pref.mime)) {
          return { mode: "recorder", mime: pref.mime, ext: pref.ext };
        }
      } catch (e) {}
    }
    return { mode: "pcm", mime: "audio/wav", ext: "wav" };
  }

  function encodeWavPCM16(chunks, sampleRate) {
    var total = 0;
    for (var i = 0; i < chunks.length; i++) total += chunks[i].length;
    var buffer = new ArrayBuffer(44 + total * 2);
    var view = new DataView(buffer);
    function writeStr(off, s) {
      for (var k = 0; k < s.length; k++) view.setUint8(off + k, s.charCodeAt(k));
    }
    writeStr(0, "RIFF");
    view.setUint32(4, 36 + total * 2, true);
    writeStr(8, "WAVE");
    writeStr(12, "fmt ");
    view.setUint32(16, 16, true);
    view.setUint16(20, 1, true);
    view.setUint16(22, 1, true);
    view.setUint32(24, sampleRate || 16000, true);
    view.setUint32(28, (sampleRate || 16000) * 2, true);
    view.setUint16(32, 2, true);
    view.setUint16(34, 16, true);
    writeStr(36, "data");
    view.setUint32(40, total * 2, true);
    var offset = 44;
    for (var c = 0; c < chunks.length; c++) {
      var data = chunks[c];
      for (var j = 0; j < data.length; j++) {
        var sample = Math.max(-1, Math.min(1, data[j]));
        view.setInt16(offset, sample < 0 ? sample * 0x8000 : sample * 0x7fff, true);
        offset += 2;
      }
    }
    return new Blob([buffer], { type: "audio/wav" });
  }

  function stopStreamTracks(stream) {
    if (!stream || typeof stream.getTracks !== "function") return;
    try {
      var tracks = stream.getTracks();
      for (var i = 0; i < tracks.length; i++) {
        try { tracks[i].stop(); } catch (e) {}
      }
    } catch (e) {}
  }

  // Single active recorder lock across top-level and same-origin ?frame=1 composers.
  var fallbackLockHost = {};
  function lockHost() {
    try {
      if (window.top && window.top.document) return window.top;
    } catch (e) {}
    return window || fallbackLockHost;
  }

  function acquireRecorderLock(owner) {
    var host = lockHost();
    if (host.__wsVoiceActiveOwner && host.__wsVoiceActiveOwner !== owner) return false;
    host.__wsVoiceActiveOwner = owner;
    return true;
  }

  function releaseRecorderLock(owner) {
    var host = lockHost();
    if (host.__wsVoiceActiveOwner === owner) {
      host.__wsVoiceActiveOwner = null;
    }
  }

  function hasActiveRecorderLock() {
    var host = lockHost();
    return !!host.__wsVoiceActiveOwner;
  }

  function voiceError(code, message) {
    var err = new Error(message || code || "upstream_error");
    err.code = code || "upstream_error";
    return err;
  }

  function parseSSEFrames(buffer, onEvent) {
    var normalized = buffer.replace(/\r\n/g, "\n");
    var idx;
    while ((idx = normalized.indexOf("\n\n")) !== -1) {
      var block = normalized.slice(0, idx);
      normalized = normalized.slice(idx + 2);
      if (!block.trim()) continue;
      var eventName = "";
      var dataLines = [];
      var lines = block.split("\n");
      for (var i = 0; i < lines.length; i++) {
        var line = lines[i];
        if (!line || line.charAt(0) === ":") continue;
        if (line.indexOf("event:") === 0) eventName = line.slice(6).trim();
        else if (line.indexOf("data:") === 0) dataLines.push(line.slice(5).trim());
      }
      var data = dataLines.join("\n").trim();
      if (!data || data === "[DONE]") continue;
      onEvent(eventName, data);
    }
    return normalized;
  }

  function readTranscribeResponse(response, onPartial) {
    var ctype = "";
    try {
      ctype = response.headers && typeof response.headers.get === "function"
        ? String(response.headers.get("content-type") || "")
        : "";
    } catch (e) {}
    var isSSE = ctype.toLowerCase().indexOf("text/event-stream") !== -1;
    if (!response.ok) {
      return response.json().catch(function () { return {}; }).then(function (body) {
        var code = (body && (body.code || body.error)) || (response.status === 504 ? "upstream_timeout" : response.status === 413 ? "audio_too_large" : "upstream_error");
        throw voiceError(code, (body && body.message) || code);
      });
    }
    if (!isSSE) {
      return response.json().then(function (body) {
        return { text: String((body && body.text) || "") };
      });
    }
    var accumulated = "";
    var finalText = null;
    var streamErr = null;
    function handleEvent(eventName, raw) {
      var payload = null;
      try { payload = JSON.parse(raw); } catch (e) { return; }
      var kind = (eventName && eventName !== "message") ? eventName : ((payload && payload.type) || "");
      if (kind === "transcript.text.delta") {
        var delta = typeof payload.delta === "string" ? payload.delta : "";
        if (delta) {
          accumulated += delta;
          if (typeof onPartial === "function") onPartial(accumulated);
        }
      } else if (kind === "transcript.text.done") {
        finalText = typeof payload.text === "string" ? payload.text : accumulated;
      } else if (kind === "error") {
        streamErr = voiceError((payload && payload.code) || "upstream_error", payload && payload.message);
      }
    }
    if (response.body && typeof response.body.getReader === "function") {
      var reader = response.body.getReader();
      var decoder = typeof TextDecoder === "function" ? new TextDecoder("utf-8") : null;
      var pending = "";
      function pump() {
        return reader.read().then(function (chunk) {
          if (chunk.done) {
            if (decoder) pending += decoder.decode();
            pending = parseSSEFrames(pending + "\n\n", handleEvent);
            if (streamErr) throw streamErr;
            if (finalText === null) throw voiceError("stream_incomplete", "Transcription stream closed before completion");
            return { text: finalText };
          }
          var text = typeof chunk.value === "string"
            ? chunk.value
            : decoder
              ? decoder.decode(chunk.value, { stream: true })
              : String(chunk.value || "");
          pending = parseSSEFrames(pending + text, handleEvent);
          if (streamErr) {
            try { reader.cancel(); } catch (e) {}
            throw streamErr;
          }
          return pump();
        });
      }
      return pump();
    }
    return response.text().then(function (full) {
      parseSSEFrames(full + "\n\n", handleEvent);
      if (streamErr) throw streamErr;
      if (finalText === null) throw voiceError("stream_incomplete", "Transcription stream closed before completion");
      return { text: finalText };
    });
  }

  function createExternalBackend(descriptor, deps) {
    deps = deps || {};
    var getFetch = function () { return deps.fetch || window.fetch; };
    var getMediaDevices = function () { return deps.mediaDevices || (window.navigator && window.navigator.mediaDevices); };
    var getMediaRecorder = function () { return deps.MediaRecorder !== undefined ? deps.MediaRecorder : window.MediaRecorder; };
    var getAudioContext = function () { return deps.AudioContext || window.AudioContext || window.webkitAudioContext; };

    var partialCb = null;
    var activeStream = null;
    var activeRecorder = null;
    var activePCM = null;
    var activeAbort = null;
    var recordedChunks = [];
    var chosenCodec = null;
    var startPromise = null;
    var cancelled = false;
    var lastAudio = null; // Memory-only; never written to storage or disk.
    var activeCtx = null;

    function pickModel(ctx) {
      if (ctx && ctx.model) return ctx.model;
      if (descriptor.models && descriptor.models.length) return descriptor.models[0].id;
      return "";
    }

    function cleanupCapture() {
      if (activePCM) {
        try { activePCM.processor.disconnect(); } catch (e) {}
        try { activePCM.source.disconnect(); } catch (e) {}
        try { activePCM.ctx.close(); } catch (e) {}
        activePCM = null;
      }
      activeRecorder = null;
      if (activeStream) {
        stopStreamTracks(activeStream);
        activeStream = null;
      }
    }

    function uploadBlob(entry) {
      var fetchFn = getFetch();
      if (typeof fetchFn !== "function") return Promise.reject(voiceError("no_fetch"));
      var form = new FormData();
      var uploadFile = entry.blob;
      if (typeof File === "function" && !(uploadFile instanceof File)) {
        try { uploadFile = new File([entry.blob], entry.filename, { type: entry.contentType }); } catch (e) {}
      }
      form.append("audio", uploadFile, entry.filename);
      form.append("backend", descriptor.id);
      if (entry.model) form.append("model", entry.model);
      if (entry.language) form.append("language", entry.language);
      var controller = typeof AbortController === "function" ? new AbortController() : null;
      activeAbort = controller;
      var init = {
        method: "POST",
        credentials: "same-origin",
        cache: "no-store",
        body: form
      };
      if (controller) init.signal = controller.signal;
      return fetchFn("/api/voice/transcribe", init).then(function (res) {
        return readTranscribeResponse(res, function (partial) {
          if (typeof partialCb === "function") partialCb(partial);
        });
      }).finally(function () {
        if (activeAbort === controller) activeAbort = null;
      });
    }

    return {
      id: descriptor.id,
      label: descriptor.label || descriptor.id,
      kind: "external",
      models: descriptor.models || [],
      available: function () { return !!descriptor.available; },
      onPartial: function (cb) { partialCb = cb; },
      hasLastAudio: function () { return !!lastAudio; },
      discardLast: function () { lastAudio = null; },
      start: function (ctx) {
        // Discard any previous recording as soon as a new recording starts.
        lastAudio = null;
        cancelled = false;
        recordedChunks = [];
        activeCtx = ctx || {};
        var md = getMediaDevices();
        if (!md || typeof md.getUserMedia !== "function") {
          return Promise.reject(voiceError("mic_unavailable"));
        }
        // Invoke getUserMedia synchronously inside the user gesture call stack
        // before any async await/fetch so WebView2 IsUserInitiated stays true.
        var gumPromise;
        try {
          gumPromise = md.getUserMedia({ audio: true });
        } catch (err) {
          return Promise.reject(voiceError("mic_denied", err && err.message));
        }
        startPromise = Promise.resolve(gumPromise).then(function (stream) {
          if (cancelled) {
            stopStreamTracks(stream);
            throw voiceError("cancelled");
          }
          activeStream = stream;
          var MRCtor = getMediaRecorder();
          chosenCodec = selectRecorderCodec(MRCtor);
          if (chosenCodec.mode === "recorder" && MRCtor) {
            var opts = chosenCodec.mime ? { mimeType: chosenCodec.mime } : undefined;
            var rec = opts ? new MRCtor(stream, opts) : new MRCtor(stream);
            rec.ondataavailable = function (ev) {
              if (ev && ev.data && ev.data.size > 0) recordedChunks.push(ev.data);
            };
            activeRecorder = rec;
            rec.start();
            return;
          }
          var AudioCtx = getAudioContext();
          if (!AudioCtx) {
            cleanupCapture();
            throw voiceError("recorder_unsupported");
          }
          var audioCtx = new AudioCtx();
          var source = audioCtx.createMediaStreamSource(stream);
          var processor = audioCtx.createScriptProcessor(4096, 1, 1);
          var pcmChunks = [];
          processor.onaudioprocess = function (ev) {
            if (!ev || !ev.inputBuffer) return;
            var channel = ev.inputBuffer.getChannelData(0);
            pcmChunks.push(new Float32Array(channel));
          };
          source.connect(processor);
          processor.connect(audioCtx.destination);
          activePCM = { ctx: audioCtx, source: source, processor: processor, chunks: pcmChunks, sampleRate: audioCtx.sampleRate || 16000 };
        }, function (err) {
          cleanupCapture();
          throw voiceError("mic_denied", err && err.message);
        });
        return startPromise;
      },
      stop: function () {
        var ready = startPromise || Promise.resolve();
        startPromise = null;
        return ready.then(function () {
          if (cancelled) throw voiceError("cancelled");
          return new Promise(function (resolve, reject) {
            if (activeRecorder) {
              var rec = activeRecorder;
              var mime = rec.mimeType || (chosenCodec && chosenCodec.mime) || "audio/webm";
              var done = function () {
                var blob = new Blob(recordedChunks, { type: mime });
                cleanupCapture();
                resolve({ blob: blob, contentType: mime, ext: extensionForMime(mime) });
              };
              rec.onstop = done;
              rec.onerror = function (ev) {
                cleanupCapture();
                reject(voiceError("recorder_error", ev && ev.error && ev.error.message));
              };
              if (rec.state === "inactive") done();
              else {
                try { rec.stop(); } catch (e) { cleanupCapture(); reject(voiceError("recorder_error", e.message)); }
              }
              return;
            }
            if (activePCM) {
              var pcm = activePCM;
              var wavBlob = encodeWavPCM16(pcm.chunks, pcm.sampleRate);
              cleanupCapture();
              resolve({ blob: wavBlob, contentType: "audio/wav", ext: "wav" });
              return;
            }
            cleanupCapture();
            reject(voiceError("not_recording"));
          });
        }).then(function (captured) {
          if (cancelled) throw voiceError("cancelled");
          var entry = {
            blob: captured.blob,
            contentType: captured.contentType,
            filename: "recording." + captured.ext,
            model: pickModel(activeCtx),
            language: (activeCtx && activeCtx.language) || ""
          };
          lastAudio = entry;
          return uploadBlob(entry);
        });
      },
      retry: function () {
        if (!lastAudio) return Promise.reject(voiceError("no_recording"));
        cancelled = false;
        return uploadBlob(lastAudio);
      },
      cancel: function () {
        cancelled = true;
        startPromise = null;
        if (activeAbort) {
          try { activeAbort.abort(); } catch (e) {}
          activeAbort = null;
        }
        if (activeRecorder && activeRecorder.state !== "inactive") {
          activeRecorder.onstop = null;
          activeRecorder.onerror = null;
          try { activeRecorder.stop(); } catch (e) {}
        }
        cleanupCapture();
      }
    };
  }

  // Provider-agnostic registry & controller factory.
  var registry = Object.create(null);

  function registerBackend(backend) {
    if (!backend || typeof backend.id !== "string" || !backend.id) {
      throw new Error("Voice backend must have a non-empty id");
    }
    registry[backend.id] = backend;
    return backend;
  }

  function unregisterBackend(id) {
    delete registry[id];
  }

  function getBackend(id) {
    return id && registry[id] ? registry[id] : null;
  }

  function listBackends() {
    return Object.keys(registry).map(function (k) { return registry[k]; });
  }

  function discoverCapabilities(opts) {
    opts = opts || {};
    var fetchFn = opts.fetch || window.fetch;
    if (typeof fetchFn !== "function") {
      return Promise.resolve({ backends: [], selected: null });
    }
    return fetchFn("/api/voice/capabilities", {
      method: "GET",
      credentials: "same-origin",
      cache: "no-store"
    }).then(function (res) {
      if (!res.ok) return { backends: [], selected: null };
      return res.json().catch(function () { return { backends: [], selected: null }; });
    }, function () {
      return { backends: [], selected: null };
    }).then(function (data) {
      var list = Array.isArray(data && data.backends) ? data.backends : [];
      var rawSelected = data && data.selected ? data.selected : null;
      var selected = null;
      var model = "";
      var language = "";
      if (rawSelected && typeof rawSelected === "object") {
        if (typeof rawSelected.backend === "string" && rawSelected.backend) selected = rawSelected.backend;
        if (typeof rawSelected.model === "string") model = rawSelected.model;
        if (typeof rawSelected.language === "string") language = rawSelected.language;
      } else if (typeof rawSelected === "string" && rawSelected) {
        selected = rawSelected;
        if (data && typeof data.model === "string") model = data.model;
        if (data && typeof data.language === "string") language = data.language;
      }
      var externalIDs = Object.create(null);
      for (var i = 0; i < list.length; i++) {
        var b = list[i];
        if (!b || !b.id) continue;
        if (b.kind === "external") {
          externalIDs[b.id] = true;
          registerBackend(createExternalBackend(b, opts));
        }
      }
      // Drop stale daemon external backends no longer reported by capabilities.
      listBackends().forEach(function (existing) {
        if (existing.kind === "external" && !externalIDs[existing.id]) {
          unregisterBackend(existing.id);
        }
      });
      var resolvedSelected = null;
      if (selected) {
        var chosen = getBackend(selected);
        if (chosen && typeof chosen.available === "function" && chosen.available()) {
          resolvedSelected = selected;
        }
      }
      return {
        backends: list,
        selected: resolvedSelected,
        rawSelected: rawSelected,
        model: model,
        language: language
      };
    });
  }

  function createController(opts) {
    opts = opts || {};
    var owner = {};
    var state = "idle";
    var selectedId = null;
    var configuredModel = "";
    var configuredLanguage = "";
    var activeBackend = null;
    var lastBackend = null;
    var generation = 0;

    function setState(next, detail) {
      state = next;
      if (typeof opts.onStateChange === "function") opts.onStateChange(state, detail || {});
    }

    function selectedBackend() {
      var b = selectedId ? getBackend(selectedId) : null;
      if (!b || typeof b.available !== "function" || !b.available()) return null;
      return b;
    }

    function setCapabilities(caps) {
      var sel = caps && caps.selected ? caps.selected : null;
      if (sel && typeof sel === "object") {
        selectedId = typeof sel.backend === "string" && sel.backend ? sel.backend : null;
        configuredModel = (typeof sel.model === "string" ? sel.model : "") || (caps && caps.model) || "";
        configuredLanguage = (typeof sel.language === "string" ? sel.language : "") || (caps && caps.language) || "";
      } else {
        selectedId = typeof sel === "string" && sel ? sel : null;
        configuredModel = (caps && caps.model) || "";
        configuredLanguage = (caps && caps.language) || "";
      }
      if (typeof opts.onCapabilities === "function") opts.onCapabilities(caps, selectedBackend());
    }

    function refreshCapabilities() {
      return discoverCapabilities(opts).then(function (caps) {
        setCapabilities(caps);
        return caps;
      });
    }

    function discardLast() {
      if (lastBackend && typeof lastBackend.discardLast === "function") {
        lastBackend.discardLast();
      }
      lastBackend = null;
    }

    function start(ctx) {
      if (state === "recording" || state === "transcribing") {
        return Promise.reject(voiceError("recorder_busy"));
      }
      var backend = selectedBackend();
      if (!backend) {
        return Promise.reject(voiceError("backend_unavailable"));
      }
      if (!acquireRecorderLock(owner)) {
        var busyErr = voiceError("recorder_busy");
        if (typeof opts.onError === "function") opts.onError(busyErr);
        return Promise.reject(busyErr);
      }
      discardLast();
      var gen = ++generation;
      activeBackend = backend;
      lastBackend = backend;
      if (typeof backend.onPartial === "function") {
        backend.onPartial(function (text) {
          if (gen !== generation || (state !== "recording" && state !== "transcribing")) return;
          if (typeof opts.onPartial === "function") opts.onPartial(text);
        });
      }
      setState("recording", { backend: backend.id });
      var mergedCtx = Object.assign({ model: configuredModel, language: configuredLanguage }, ctx || {});
      // Synchronously invoke backend.start so getUserMedia stays inside the user gesture.
      var p;
      try {
        p = Promise.resolve(backend.start(mergedCtx));
      } catch (err) {
        releaseRecorderLock(owner);
        activeBackend = null;
        setState("error", { error: err });
        if (typeof opts.onError === "function") opts.onError(err);
        return Promise.reject(err);
      }
      return p.catch(function (err) {
        if (gen !== generation) return;
        releaseRecorderLock(owner);
        activeBackend = null;
        if (err && err.code === "cancelled") {
          setState("idle");
          return;
        }
        setState("error", { error: err });
        if (typeof opts.onError === "function") opts.onError(err);
      });
    }

    function stop() {
      if (state !== "recording" || !activeBackend) return Promise.resolve(null);
      var backend = activeBackend;
      var gen = generation;
      setState("transcribing", { backend: backend.id });
      var p;
      try { p = Promise.resolve(backend.stop()); } catch (err) { p = Promise.reject(err); }
      return p.then(function (result) {
        if (gen !== generation) return null;
        releaseRecorderLock(owner);
        activeBackend = null;
        discardLast();
        setState("idle");
        var text = result && typeof result.text === "string" ? result.text : "";
        if (typeof opts.onFinal === "function") opts.onFinal(text);
        return result;
      }, function (err) {
        if (gen !== generation) return null;
        releaseRecorderLock(owner);
        activeBackend = null;
        if (err && err.code === "cancelled") {
          setState("idle");
          return null;
        }
        setState("error", { error: err });
        if (typeof opts.onError === "function") opts.onError(err);
        return null;
      });
    }

    function retry() {
      if (state === "recording" || state === "transcribing") return Promise.resolve(null);
      var backend = lastBackend;
      if (!backend || typeof backend.retry !== "function" || (typeof backend.hasLastAudio === "function" && !backend.hasLastAudio())) {
        return Promise.resolve(null);
      }
      if (!acquireRecorderLock(owner)) {
        var busyErr = voiceError("recorder_busy");
        if (typeof opts.onError === "function") opts.onError(busyErr);
        return Promise.resolve(null);
      }
      var gen = ++generation;
      activeBackend = backend;
      if (typeof backend.onPartial === "function") {
        backend.onPartial(function (text) {
          if (gen !== generation || state !== "transcribing") return;
          if (typeof opts.onPartial === "function") opts.onPartial(text);
        });
      }
      setState("transcribing", { backend: backend.id, retry: true });
      var p;
      try { p = Promise.resolve(backend.retry()); } catch (err) { p = Promise.reject(err); }
      return p.then(function (result) {
        if (gen !== generation) return null;
        releaseRecorderLock(owner);
        activeBackend = null;
        discardLast();
        setState("idle");
        var text = result && typeof result.text === "string" ? result.text : "";
        if (typeof opts.onFinal === "function") opts.onFinal(text);
        return result;
      }, function (err) {
        if (gen !== generation) return null;
        releaseRecorderLock(owner);
        activeBackend = null;
        if (err && err.code === "cancelled") {
          setState("idle");
          return null;
        }
        setState("error", { error: err });
        if (typeof opts.onError === "function") opts.onError(err);
        return null;
      });
    }

    function cancel(options) {
      options = options || {};
      generation++;
      var backend = activeBackend;
      activeBackend = null;
      if (backend && typeof backend.cancel === "function") {
        try { backend.cancel(); } catch (e) {}
      }
      releaseRecorderLock(owner);
      if (options.discardLast) discardLast();
      if (state !== "idle") setState("idle", { cancelled: true });
    }

    function canRetry() {
      return !!(lastBackend && typeof lastBackend.retry === "function" &&
        (typeof lastBackend.hasLastAudio !== "function" || lastBackend.hasLastAudio()));
    }

    return {
      getState: function () { return state; },
      getSelectedBackend: selectedBackend,
      setCapabilities: setCapabilities,
      refreshCapabilities: refreshCapabilities,
      start: start,
      stop: stop,
      retry: retry,
      cancel: cancel,
      discardLast: discardLast,
      canRetry: canRetry,
      destroy: function () { cancel({ discardLast: true }); }
    };
  }

  function errorLabel(err) {
    var code = (err && err.code) || "";
    if (code === "mic_denied" || code === "mic_unavailable") return translate("js.voice.err.mic");
    if (code === "recorder_busy") return translate("js.voice.err.busy");
    if (code === "upstream_timeout") return translate("js.voice.err.timeout");
    if (code === "provider_auth_failed") return translate("js.voice.err.auth");
    if (code === "audio_too_large") return translate("js.voice.err.too_large");
    return translate("js.voice.err.failed");
  }

  function attachComposer(opts) {
    opts = opts || {};
    var doc = opts.document || document;
    var root = doc.documentElement;
    var composer = opts.composer || doc.getElementById("composer");
    var ctext = opts.ctext || doc.getElementById("ctext");
    var csend = opts.csend || doc.getElementById("csend");
    var btn = opts.button || doc.getElementById("cvox");
    var cap = opts.capsule || doc.getElementById("cvox-cap");
    if (!composer || !ctext || !btn || !cap) return null;

    var caretStart = 0;
    var caretEnd = 0;
    var locked = false;
    var prevCtextDisabled = false;
    var prevCsendDisabled = false;

    function captureCaret() {
      var start = typeof ctext.selectionStart === "number" ? ctext.selectionStart : (ctext.value || "").length;
      var end = typeof ctext.selectionEnd === "number" ? ctext.selectionEnd : start;
      caretStart = Math.max(0, Math.min((ctext.value || "").length, start));
      caretEnd = Math.max(caretStart, Math.min((ctext.value || "").length, end));
    }

    function positionCapsule() {
      var field = cap.parentNode;
      var anchor = null;
      if (ctext.wsc && typeof ctext.wsc.anchor === "function") {
        try { anchor = ctext.wsc.anchor(caretStart); } catch (e) { anchor = null; }
      }
      var fieldRect = field && typeof field.getBoundingClientRect === "function"
        ? field.getBoundingClientRect()
        : { left: 0, top: 0, width: 320, height: 40 };
      var left = 8;
      var top = 4;
      if (anchor && typeof anchor.x === "number") {
        left = Math.max(4, Math.round(anchor.x - fieldRect.left));
        if (fieldRect.width > 80) left = Math.min(left, Math.max(4, Math.round(fieldRect.width - 140)));
        if (typeof anchor.bottom === "number") {
          top = Math.max(2, Math.round(anchor.bottom - fieldRect.top + 4));
        }
      }
      cap.style.left = left + "px";
      cap.style.top = top + "px";
      cap.setAttribute("data-caret", String(caretStart));
    }

    function setComposerLock(next) {
      if (locked === next) return;
      locked = next;
      if (next) {
        prevCtextDisabled = !!ctext.disabled;
        prevCsendDisabled = !!(csend && csend.disabled);
        composer.setAttribute("data-voice-locked", "1");
        ctext.disabled = true;
        if (ctext.wsc && ctext.wsc.editor) {
          ctext.wsc.editor.setAttribute("contenteditable", "false");
          ctext.wsc.editor.setAttribute("aria-disabled", "true");
        }
        if (csend) csend.disabled = true;
      } else {
        composer.removeAttribute("data-voice-locked");
        ctext.disabled = prevCtextDisabled;
        if (ctext.wsc && ctext.wsc.editor) {
          ctext.wsc.editor.setAttribute("contenteditable", prevCtextDisabled ? "false" : "true");
          ctext.wsc.editor.setAttribute("aria-disabled", String(prevCtextDisabled));
        }
        if (csend) csend.disabled = prevCsendDisabled;
      }
    }

    function insertFinalText(text) {
      if (!text) return;
      var val = String(ctext.value || "");
      var a = Math.max(0, Math.min(val.length, caretStart));
      var b = Math.max(a, Math.min(val.length, caretEnd));
      if (typeof ctext.setRangeText === "function") {
        ctext.setRangeText(text, a, b, "end");
      } else {
        ctext.value = val.slice(0, a) + text + val.slice(b);
        if (typeof ctext.setSelectionRange === "function") {
          ctext.setSelectionRange(a + text.length, a + text.length);
        }
      }
      if (typeof Event === "function" && typeof ctext.dispatchEvent === "function") {
        ctext.dispatchEvent(new Event("input", { bubbles: true }));
      }
      if (typeof ctext.focus === "function") ctext.focus();
    }

    function renderCapsule(state, text, err) {
      cap.textContent = "";
      if (state === "idle") {
        cap.hidden = true;
        cap.removeAttribute("data-state");
        return;
      }
      cap.hidden = false;
      cap.setAttribute("data-state", state);
      positionCapsule();
      var dot = doc.createElement("span");
      dot.className = "cvox-dot";
      dot.setAttribute("aria-hidden", "true");
      cap.appendChild(dot);

      var span = doc.createElement("span");
      span.className = "cvox-text";
      if (state === "recording") span.textContent = text || translate("js.voice.recording");
      else if (state === "transcribing") span.textContent = text || translate("js.voice.transcribing");
      else if (state === "error") span.textContent = errorLabel(err);
      cap.appendChild(span);

      var acts = doc.createElement("span");
      acts.className = "cvox-acts";
      if (state === "recording" || state === "transcribing") {
        var cancelBtn = doc.createElement("button");
        cancelBtn.type = "button";
        cancelBtn.className = "cvox-btn cvox-muted";
        cancelBtn.setAttribute("data-voice-action", "cancel");
        cancelBtn.textContent = translate("js.voice.cancel");
        cancelBtn.addEventListener("click", function () { controller.cancel({ discardLast: true }); });
        acts.appendChild(cancelBtn);
      } else if (state === "error") {
        if (controller.canRetry()) {
          var resendBtn = doc.createElement("button");
          resendBtn.type = "button";
          resendBtn.className = "cvox-btn";
          resendBtn.setAttribute("data-voice-action", "resend");
          resendBtn.textContent = translate("js.voice.resend");
          resendBtn.addEventListener("click", function () { controller.retry(); });
          acts.appendChild(resendBtn);
        }
        var dismissBtn = doc.createElement("button");
        dismissBtn.type = "button";
        dismissBtn.className = "cvox-btn cvox-muted";
        dismissBtn.setAttribute("data-voice-action", "dismiss");
        dismissBtn.textContent = translate("js.voice.dismiss");
        dismissBtn.addEventListener("click", function () {
          controller.discardLast();
          renderCapsule("idle", "");
        });
        acts.appendChild(dismissBtn);
      }
      cap.appendChild(acts);
    }

    function syncButton(state, available) {
      btn.hidden = !available;
      if (state === "recording") {
        btn.setAttribute("data-state", "recording");
        btn.setAttribute("aria-pressed", "true");
        btn.title = translate("js.voice.stop");
        btn.setAttribute("aria-label", translate("js.voice.stop"));
      } else if (state === "transcribing") {
        btn.setAttribute("data-state", "transcribing");
        btn.setAttribute("aria-pressed", "true");
        btn.title = translate("js.voice.transcribing");
        btn.setAttribute("aria-label", translate("js.voice.transcribing"));
      } else {
        btn.removeAttribute("data-state");
        btn.setAttribute("aria-pressed", "false");
        btn.title = translate("js.voice.start");
        btn.setAttribute("aria-label", translate("js.voice.start"));
      }
    }

    var controller = createController(Object.assign({}, opts, {
      onCapabilities: function (caps, backend) {
        syncButton(controller.getState(), !!backend);
      },
      onStateChange: function (next, detail) {
        var active = next === "recording" || next === "transcribing";
        setComposerLock(active);
        syncButton(next, !!controller.getSelectedBackend());
        if (next === "recording") renderCapsule("recording", "");
        else if (next === "transcribing") renderCapsule("transcribing", "");
        else if (next === "error") renderCapsule("error", "", detail && detail.error);
        else renderCapsule("idle", "");
      },
      onPartial: function (partialText) {
        if (cap.hidden) return;
        var node = cap.querySelector(".cvox-text");
        if (node) node.textContent = partialText;
      },
      onFinal: function (finalText) {
        setComposerLock(false);
        renderCapsule("idle", "");
        insertFinalText(finalText);
      },
      onError: function (err) {
        setComposerLock(false);
        renderCapsule("error", "", err);
      }
    }));

    function onMicClick() {
      var st = controller.getState();
      if (st === "recording") {
        controller.stop();
        return;
      }
      if (st === "transcribing") {
        controller.cancel({ discardLast: false });
        return;
      }
      captureCaret();
      controller.start().catch(function () {});
    }

    function onKeyDown(e) {
      if (!e || e.key !== "Escape") return;
      var st = controller.getState();
      if (st === "recording" || st === "transcribing") {
        if (typeof e.preventDefault === "function") e.preventDefault();
        controller.cancel({ discardLast: true });
      }
    }

    function isPageHidden() {
      return !!(doc.hidden || (root && typeof root.hasAttribute === "function" && root.hasAttribute("data-animation-paused")));
    }

    function onPageVisibility() {
      if (isPageHidden()) {
        // Foreground interaction only: cancel active recording, release mic,
        // drop composer lock & capsule, and discard in-memory last recording.
        controller.cancel({ discardLast: true });
        setComposerLock(false);
        renderCapsule("idle", "");
      }
    }

    function onUnload() {
      controller.cancel({ discardLast: true });
      setComposerLock(false);
      renderCapsule("idle", "");
    }

    btn.addEventListener("click", onMicClick);
    doc.addEventListener("keydown", onKeyDown);
    doc.addEventListener("ws-page-visibility", onPageVisibility);
    doc.addEventListener("visibilitychange", onPageVisibility);
    if (window.addEventListener) {
      window.addEventListener("pagehide", onUnload);
      window.addEventListener("beforeunload", onUnload);
    }

    if (opts.autoDiscover !== false) {
      controller.refreshCapabilities();
    }

    return {
      controller: controller,
      destroy: function () {
        controller.destroy();
        btn.removeEventListener("click", onMicClick);
        doc.removeEventListener("keydown", onKeyDown);
        doc.removeEventListener("ws-page-visibility", onPageVisibility);
        doc.removeEventListener("visibilitychange", onPageVisibility);
        if (window.removeEventListener) {
          window.removeEventListener("pagehide", onUnload);
          window.removeEventListener("beforeunload", onUnload);
        }
      }
    };
  }

  window.WsVoiceInput = {
    registerBackend: registerBackend,
    unregisterBackend: unregisterBackend,
    getBackend: getBackend,
    listBackends: listBackends,
    discoverCapabilities: discoverCapabilities,
    createController: createController,
    createExternalBackend: createExternalBackend,
    attachComposer: attachComposer,
    selectRecorderCodec: selectRecorderCodec,
    extensionForMime: extensionForMime,
    encodeWavPCM16: encodeWavPCM16,
    hasActiveRecorderLock: hasActiveRecorderLock
  };

  if (typeof document !== "undefined" && document.getElementById && document.getElementById("cvox")) {
    attachComposer();
  }
})();
