// Common provider-agnostic voice input interface (window.WsVoiceInput) and
// composer integration. Inline span & Spacebar PTT live in dashboard-voice-ptt.js;
// OS native bridge adapter & frame relay live in dashboard-voice-native.js.
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

  // Provider-agnostic registry & optional post-processing hook.
  var registry = Object.create(null);
  var globalPostProcess = null;

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

  function setPostProcess(fn) {
    globalPostProcess = typeof fn === "function" ? fn : null;
  }

  function getPostProcess() {
    return globalPostProcess;
  }

  // Walk the effective order left-to-right and pick the first backend available
  // in the current environment; fall back to legacy selected ID when order is empty.
  function resolveOrder(order, fallbackId) {
    if (Array.isArray(order) && order.length) {
      for (var i = 0; i < order.length; i++) {
        var b = getBackend(order[i]);
        if (b && typeof b.available === "function" && b.available()) return b;
      }
      return null;
    }
    if (fallbackId) {
      var legacy = getBackend(fallbackId);
      if (legacy && typeof legacy.available === "function" && legacy.available()) return legacy;
    }
    return null;
  }

  function normalizeCapabilities(data, eligible) {
    var list = Array.isArray(data && data.backends) ? data.backends : [];
    var rawSelected = data && data.selected ? data.selected : null;
    var selectedId = "";
    var model = "";
    var language = "";
    if (rawSelected && typeof rawSelected === "object") {
      if (typeof rawSelected.backend === "string") selectedId = rawSelected.backend;
      if (typeof rawSelected.model === "string") model = rawSelected.model;
      if (typeof rawSelected.language === "string") language = rawSelected.language;
    } else if (typeof rawSelected === "string") {
      selectedId = rawSelected;
      if (data && typeof data.model === "string") model = data.model;
      if (data && typeof data.language === "string") language = data.language;
    }
    if (data && typeof data.language === "string" && data.language) language = data.language;
    var legacyBackend = (data && typeof data.legacy_backend === "string" && data.legacy_backend) || selectedId || "";
    var legacyModel = (data && typeof data.legacy_model === "string" && data.legacy_model) || model || "";
    var order = Array.isArray(data && data.order)
      ? data.order.filter(function (id) { return typeof id === "string" && id.length > 0; })
      : (selectedId ? [selectedId] : []);
    var resolved = resolveOrder(order, selectedId);
    return {
      eligible: !!eligible,
      backends: list,
      order: order,
      selected: resolved ? resolved.id : null,
      rawSelected: rawSelected,
      legacyBackend: legacyBackend,
      legacyModel: legacyModel,
      model: model,
      language: language,
      openrouterSharedKey: !!(data && data.openrouter_shared_key)
    };
  }

  function discoverCapabilities(opts) {
    opts = opts || {};
    var fetchFn = opts.fetch || window.fetch;
    if (typeof fetchFn !== "function") {
      return Promise.resolve(normalizeCapabilities(null, false));
    }
    return fetchFn("/api/voice/capabilities", {
      method: "GET",
      credentials: "same-origin",
      cache: "no-store"
    }).then(function (res) {
      if (!res || !res.ok) return { ok: false, data: null };
      return res.json().then(function (data) {
        return { ok: true, data: data };
      }, function () {
        return { ok: true, data: {} };
      });
    }, function () {
      return { ok: false, data: null };
    }).then(function (res) {
      if (!res.ok) return normalizeCapabilities(null, false);
      var data = res.data || {};
      var list = Array.isArray(data.backends) ? data.backends : [];
      var externalIDs = Object.create(null);
      for (var i = 0; i < list.length; i++) {
        var b = list[i];
        if (!b || !b.id) continue;
        if (b.kind === "external") {
          externalIDs[b.id] = true;
          registerBackend(createExternalBackend(b, opts));
        }
      }
      listBackends().forEach(function (existing) {
        if (existing.kind === "external" && !externalIDs[existing.id]) {
          unregisterBackend(existing.id);
        }
      });
      return normalizeCapabilities(data, true);
    });
  }

  function createController(opts) {
    opts = opts || {};
    var owner = {};
    var state = "idle";
    var eligible = false;
    var order = [];
    var fallbackSelectedId = null;
    var legacyBackend = "";
    var legacyModel = "";
    var configuredLanguage = "";
    var lastCaps = null;
    var activeBackend = null;
    var lastBackend = null;
    var generation = 0;

    function setState(next, detail) {
      state = next;
      if (typeof opts.onStateChange === "function") opts.onStateChange(state, detail || {});
    }

    function resolvedBackend() {
      return resolveOrder(order, fallbackSelectedId);
    }

    function modelForBackend(backend) {
      if (!backend) return "";
      if (legacyBackend && backend.id === legacyBackend) return legacyModel || "";
      return "";
    }

    function setCapabilities(caps) {
      var hasExplicitEligible = !!(caps && typeof caps.eligible === "boolean");
      var sel = caps && caps.selected ? caps.selected : null;
      var selId = "";
      var selModel = "";
      var selLang = "";
      if (sel && typeof sel === "object") {
        if (typeof sel.backend === "string") selId = sel.backend;
        if (typeof sel.model === "string") selModel = sel.model;
        if (typeof sel.language === "string") selLang = sel.language;
      } else if (typeof sel === "string") {
        selId = sel;
      }
      order = Array.isArray(caps && caps.order)
        ? caps.order.filter(function (id) { return typeof id === "string" && id.length > 0; })
        : (selId ? [selId] : []);
      fallbackSelectedId = selId || null;
      legacyBackend = (caps && typeof caps.legacyBackend === "string" && caps.legacyBackend) ||
        (caps && typeof caps.legacy_backend === "string" && caps.legacy_backend) || selId || "";
      legacyModel = (caps && typeof caps.legacyModel === "string" && caps.legacyModel) ||
        (caps && typeof caps.legacy_model === "string" && caps.legacy_model) ||
        selModel || (caps && caps.model) || "";
      configuredLanguage = selLang || (caps && caps.language) || "";
      eligible = hasExplicitEligible ? !!caps.eligible : !!(selId || order.length);
      lastCaps = caps || null;
      if (typeof opts.onCapabilities === "function") opts.onCapabilities(caps, resolvedBackend(), eligible);
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

    function finishResult(gen, backend, result) {
      if (gen !== generation) return null;
      var rawText = result && typeof result.text === "string" ? result.text : "";
      var pp = typeof opts.postProcess === "function" ? opts.postProcess : globalPostProcess;
      function commit(finalText) {
        if (gen !== generation) return null;
        releaseRecorderLock(owner);
        activeBackend = null;
        discardLast();
        setState("idle");
        if (typeof opts.onFinal === "function") opts.onFinal(finalText);
        return { text: finalText };
      }
      if (pp && rawText) {
        setState("refining", { backend: backend.id });
        return Promise.resolve().then(function () {
          return pp(rawText, { backend: backend.id });
        }).then(function (refined) {
          return commit(typeof refined === "string" ? refined : rawText);
        }, function () {
          return commit(rawText);
        });
      }
      return commit(rawText);
    }

    // fail ends the attempt in error (or idle for a user cancel); null for chaining.
    function fail(gen, err) {
      if (gen !== generation) return null;
      releaseRecorderLock(owner);
      activeBackend = null;
      if (err && err.code === "cancelled") { setState("idle"); return null; }
      setState("error", { error: err });
      if (typeof opts.onError === "function") opts.onError(err);
      return null;
    }

    // OS Native alone falls back, and only while the gesture is still held
    // (state "recording") and nothing was transcribed: when it is unavailable
    // (not mic-denied, which any recorder would hit), the next available backend
    // in order takes over. Fallbacks are never native, so each gesture falls back once.
    var partialSeen = false, startCtx;
    var UNAVAILABLE = /^(recognizer_unavailable|unavailable|privacy_disabled|language_unavailable|bridge_error)$/;
    function fallback(backend, gen, err) {
      if (gen !== generation || backend.id !== "native" || state !== "recording" || partialSeen || !err || !UNAVAILABLE.test(err.code)) return null;
      for (var i = order.indexOf("native") + 1; i > 0 && i < order.length; i++) {
        var next = getBackend(order[i]);
        if (!next || typeof next.available !== "function" || !next.available()) continue;
        generation++;
        try { if (backend.cancel) backend.cancel(); } catch (e) {}
        return begin(next, startCtx, false);
      }
      return null;
    }

    function wireBackendCallbacks(backend, gen) {
      if (typeof backend.onPartial === "function") {
        backend.onPartial(function (text) {
          if (gen === generation) partialSeen = true;
          if (gen !== generation || (state !== "recording" && state !== "transcribing")) return;
          if (typeof opts.onPartial === "function") opts.onPartial(text);
        });
      }
      if (typeof backend.onError === "function") {
        backend.onError(function (err) {
          if (gen !== generation || fallback(backend, gen, err)) return;
          releaseRecorderLock(owner);
          activeBackend = null;
          setState("error", { error: err });
          if (typeof opts.onError === "function") opts.onError(err);
        });
      }
      if (typeof backend.onAutoFinal === "function") backend.onAutoFinal(function (text) {
        if (gen === generation && state === "recording") finishResult(gen, backend, { text: text });
      });
      if (typeof backend.onHidden === "function") {
        backend.onHidden(function () {
          if (gen !== generation) return;
          generation++;
          releaseRecorderLock(owner);
          activeBackend = null;
          discardLast();
          if (state !== "idle") setState("idle", { cancelled: true, hidden: true });
        });
      }
    }

    function start(ctx) {
      if (state === "recording" || state === "transcribing" || state === "refining") {
        return Promise.reject(voiceError("recorder_busy"));
      }
      var backend = resolvedBackend();
      if (!backend) {
        return Promise.reject(voiceError("backend_unavailable"));
      }
      if (!acquireRecorderLock(owner)) {
        var busyErr = voiceError("recorder_busy");
        if (typeof opts.onError === "function") opts.onError(busyErr);
        return Promise.reject(busyErr);
      }
      discardLast();
      startCtx = ctx;
      return begin(backend, ctx, true);
    }

    // begin starts one backend attempt under a new generation; fresh enters
    // "recording", a fallback is already there.
    function begin(backend, ctx, fresh) {
      var gen = ++generation;
      partialSeen = false;
      activeBackend = backend;
      lastBackend = backend;
      wireBackendCallbacks(backend, gen);
      var p;
      try {
        // In the try so a throwing onStateChange cannot leave the lock held in "recording".
        if (fresh) setState("recording", { backend: backend.id });
        p = Promise.resolve(backend.start(Object.assign({ model: modelForBackend(backend), language: configuredLanguage }, ctx || {})));
      } catch (err) {
        var next = fallback(backend, gen, err);
        if (next) return next;
        releaseRecorderLock(owner);
        activeBackend = null;
        state = "error";
        try { setState("error", { error: err }); if (typeof opts.onError === "function") opts.onError(err); } catch (e) {}
        return Promise.reject(err);
      }
      return p.catch(function (err) { return fallback(backend, gen, err) || fail(gen, err); });
    }

    function stop() {
      if (state !== "recording" || !activeBackend) return Promise.resolve(null);
      var backend = activeBackend;
      var gen = generation;
      setState("transcribing", { backend: backend.id });
      var p;
      try { p = Promise.resolve(backend.stop()); } catch (err) { p = Promise.reject(err); }
      return p.then(function (result) {
        return finishResult(gen, backend, result);
      }, function (err) { return fail(gen, err); });
    }

    function retry() {
      if (state === "recording" || state === "transcribing" || state === "refining") return Promise.resolve(null);
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
      wireBackendCallbacks(backend, gen);
      setState("transcribing", { backend: backend.id, retry: true });
      var p;
      try { p = Promise.resolve(backend.retry()); } catch (err) { p = Promise.reject(err); }
      return p.then(function (result) {
        return finishResult(gen, backend, result);
      }, function (err) { return fail(gen, err); });
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
      isEligible: function () { return !!eligible; },
      getResolvedBackend: resolvedBackend,
      getSelectedBackend: resolvedBackend,
      getCapabilities: function () { return lastCaps; },
      getOrder: function () { return order.slice(); },
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

  function attachComposer(opts) {
    opts = opts || {};
    var doc = opts.document || document;
    var win = opts.window || window;
    var root = doc.documentElement;
    var composer = opts.composer || doc.getElementById("composer");
    var ctext = opts.ctext || doc.getElementById("ctext");
    var csend = opts.csend || doc.getElementById("csend");
    var btn = opts.button || doc.getElementById("cvox");
    var cap = opts.capsule || doc.getElementById("cvox-cap");
    if (!composer || !ctext || !btn || !cap) return null;

    var pttMod = win.WsVoicePTT || null;
    var pttHandle = null;
    var showWhenEligible = opts.showWhenEligible !== false;
    var setComposerLock = (pttMod && typeof pttMod.createComposerLock === "function")
      ? pttMod.createComposerLock(composer, ctext, csend, function () { return pttHandle; })
      : function () {};
    var syncButton = function (state, backend, isEligible) {
      if (pttMod && typeof pttMod.syncMicButton === "function") {
        pttMod.syncMicButton(btn, state, backend, isEligible, showWhenEligible);
      } else {
        btn.hidden = !(showWhenEligible ? !!isEligible : !!backend);
      }
    };
    var spanCtrl = (pttMod && typeof pttMod.createSpanController === "function")
      ? pttMod.createSpanController({
        document: doc,
        ctext: ctext,
        capsule: cap,
        errorLabel: pttMod.errorLabel,
        onCancel: function () { controller.cancel({ discardLast: true }); },
        onRetry: function () { controller.retry(); },
        onDismiss: function () { controller.cancel({ discardLast: true }); },
        canRetry: function () { return controller.canRetry(); }
      })
      : null;

    var controller = createController(Object.assign({}, opts, {
      onCapabilities: function (caps, backend, isEligible) {
        syncButton(controller.getState(), backend, isEligible);
      },
      onStateChange: function (next, detail) {
        var active = next === "recording" || next === "transcribing" || next === "refining";
        setComposerLock(active);
        syncButton(next, controller.getResolvedBackend(), controller.isEligible());
        if (!spanCtrl) return;
        if (next === "recording") spanCtrl.beginListening();
        else if (next === "transcribing" || next === "refining") spanCtrl.setRefining();
        else if (next === "error") spanCtrl.showError(detail && detail.error);
        else spanCtrl.reset();
      },
      onPartial: function (partialText) {
        if (spanCtrl) spanCtrl.setPartial(partialText);
      },
      onFinal: function (finalText) {
        setComposerLock(false);
        if (spanCtrl) spanCtrl.commitFinal(finalText);
      },
      onError: function (err) {
        setComposerLock(false);
        if (spanCtrl) spanCtrl.showError(err);
      }
    }));

    function startDictation() {
      if (spanCtrl) spanCtrl.captureCaret();
      controller.start().catch(function () {});
    }

    var micPTT = null;
    function onMicClick(e) {
      if (micPTT && micPTT.consumeClick(e)) return;
      var st = controller.getState();
      if (st === "recording") {
        controller.stop();
        return;
      }
      if (st === "transcribing" || st === "refining") {
        controller.cancel({ discardLast: false });
        return;
      }
      if (!controller.getResolvedBackend()) {
        if (typeof opts.onUnconfiguredClick === "function") opts.onUnconfiguredClick();
        else if (pttMod && typeof pttMod.openVoiceSettings === "function") pttMod.openVoiceSettings(win, btn);
        return;
      }
      startDictation();
    }

    function onKeyDown(e) {
      if (!e || e.key !== "Escape") return;
      var st = controller.getState();
      if (st === "recording" || st === "transcribing" || st === "refining") {
        if (typeof e.preventDefault === "function") e.preventDefault();
        if (pttHandle) pttHandle.disarm();
        controller.cancel({ discardLast: true });
      }
    }

    function isPageHidden() {
      return !!(doc.hidden || (root && typeof root.hasAttribute === "function" && root.hasAttribute("data-animation-paused")));
    }

    function teardownActive() {
      if (pttHandle) pttHandle.disarm();
      controller.cancel({ discardLast: true });
      setComposerLock(false);
      if (spanCtrl) spanCtrl.reset();
    }

    function onPageVisibility() {
      if (isPageHidden()) teardownActive();
    }

    function onNativeStatusChange() {
      syncButton(controller.getState(), controller.getResolvedBackend(), controller.isEligible());
    }

    if (win.WsVoicePTT && typeof win.WsVoicePTT.attachSpacebarPTT === "function") {
      pttHandle = win.WsVoicePTT.attachSpacebarPTT({
        document: doc,
        window: win,
        ctext: ctext,
        controller: controller,
        holdMs: opts.holdMs,
        setTimeout: opts.setTimeout,
        clearTimeout: opts.clearTimeout,
        onStartPTT: startDictation,
        onStopPTT: function () { controller.stop(); },
        onHoldingChange: opts.onHoldingChange
      });
    }

    // Pointer press-and-hold on the mic button; click stays for keyboard and the non-idle states.
    if (pttMod && typeof pttMod.attachMicPointerPTT === "function" && win.addEventListener) {
      micPTT = pttMod.attachMicPointerPTT({
        button: btn, window: win, setTimeout: opts.setTimeout, clearTimeout: opts.clearTimeout,
        canStart: function () { var st = controller.getState(); return (st === "idle" || st === "error") && !!controller.getResolvedBackend(); },
        onStart: startDictation, onStop: function () { controller.stop(); }
      });
    }

    function onCapabilitiesRefresh() { controller.refreshCapabilities(); }

    var wired = [[btn, "click", onMicClick], [doc, "keydown", onKeyDown], [doc, "ws-page-visibility", onPageVisibility],
      [doc, "visibilitychange", onPageVisibility], [doc, "ws-native-speech-hidden", teardownActive], [doc, "ws-account-changed", onCapabilitiesRefresh]];
    if (win.addEventListener) wired.push([win, "ws-native-speech-status", onNativeStatusChange], [win, "ws-voice-capabilities-refresh", onCapabilitiesRefresh],
      [win, "pagehide", teardownActive], [win, "beforeunload", teardownActive]);
    wired.forEach(function (w) { w[0].addEventListener(w[1], w[2]); });

    if (opts.autoDiscover !== false) controller.refreshCapabilities();

    return {
      controller: controller, span: spanCtrl, ptt: pttHandle,
      destroy: function () {
        if (pttHandle) pttHandle.destroy();
        if (micPTT) micPTT.destroy();
        controller.destroy();
        wired.forEach(function (w) { w[0].removeEventListener(w[1], w[2]); });
      }
    };
  }

  window.WsVoiceInput = {
    registerBackend: registerBackend,
    unregisterBackend: unregisterBackend,
    getBackend: getBackend,
    listBackends: listBackends,
    discoverCapabilities: discoverCapabilities,
    resolveOrder: resolveOrder,
    createController: createController,
    createExternalBackend: createExternalBackend,
    attachComposer: attachComposer,
    selectRecorderCodec: selectRecorderCodec,
    extensionForMime: extensionForMime,
    encodeWavPCM16: encodeWavPCM16,
    hasActiveRecorderLock: hasActiveRecorderLock,
    setPostProcess: setPostProcess,
    getPostProcess: getPostProcess
  };

  if (typeof document !== "undefined" && document.getElementById && document.getElementById("cvox")) {
    attachComposer();
  }
})();
