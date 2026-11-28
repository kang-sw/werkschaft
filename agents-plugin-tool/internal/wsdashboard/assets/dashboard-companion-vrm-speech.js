(function (C) {
  "use strict";

  // Tier-0 procedural 5-vowel VRM lip-sync (Hangul/Latin/CJK/digits), natural
  // speech-time blink modulation, and pluggable speechMouthDriver contract.
  // Pure frontend cosmetic layer (0 LLM tokens, zero network/file I/O).
  //
  // Pluggable speechMouthDriver contract (future TTS ready):
  //   { start(text, opts), stop(), isActive(), sample(dt, nowMs) -> { aa, ih, ou, ee, oh, speaking, boundary? } }

  const VISEMES = Object.freeze(["aa", "ih", "ou", "ee", "oh"]);
  const VISEME_ALIASES = Object.freeze({
    aa: Object.freeze(["aa", "a"]),
    ih: Object.freeze(["ih", "i"]),
    ou: Object.freeze(["ou", "u"]),
    ee: Object.freeze(["ee", "e"]),
    oh: Object.freeze(["oh", "o"])
  });

  const MIN_DURATION_MS = 900;
  const MAX_DURATION_MS = 8500;
  const EXPLICIT_MIN_MS = 180;
  const EXPLICIT_MAX_MS = 30000;
  const MAX_PEAK_WEIGHT = 0.84;
  const SILENT_SAMPLE = Object.freeze({ aa: 0, ih: 0, ou: 0, ee: 0, oh: 0, speaking: false, boundary: "" });

  // Expressions whose eye geometry closes/squints: speech-blink is strictly suppressed.
  const EYE_CLOSING_STATES = new Set([
    "idle_doze", "half_lid", "cry", "happy", "wink", "merong", "oo", "startled", "annoyed"
  ]);
  const EYE_CLOSING_OVERLAYS = new Set([
    "sleepy", "dizzy", "dizzy-shake", "knock-startle", "wakeup-jolt",
    "annoyed-tap", "phone-buzz", "phone-facedown-merong"
  ]);

  // Strong mouth-shaping facial expressions attenuate viseme peaks so faces never clip.
  const STATE_ATTENUATION = Object.freeze({
    happy: 0.56, surprised: 0.50, startled: 0.50, oo: 0.50,
    cry: 0.60, merong: 0.42, annoyed: 0.74, idle_doze: 0.46
  });

  // 21 Hangul Jungseong (U+1161..U+1175) -> { main, onset? } viseme weights.
  // 0:ㅏ 1:ㅐ 2:ㅑ 3:ㅒ 4:ㅓ 5:ㅔ 6:ㅕ 7:ㅖ 8:ㅗ 9:ㅘ 10:ㅙ 11:ㅚ 12:ㅛ 13:ㅜ 14:ㅝ 15:ㅞ 16:ㅟ 17:ㅠ 18:ㅡ 19:ㅢ 20:ㅣ
  const HANGUL_JUNGSEONG = Object.freeze([
    { main: { aa: 1.0 } },                                             // 0: ㅏ
    { main: { ee: 0.72, aa: 0.34 } },                                  // 1: ㅐ
    { onset: { ih: 0.56 }, main: { aa: 0.96 } },                       // 2: ㅑ
    { onset: { ih: 0.52 }, main: { ee: 0.68, aa: 0.32 } },             // 3: ㅒ
    { main: { oh: 0.68, aa: 0.42 } },                                  // 4: ㅓ
    { main: { ee: 0.94 } },                                            // 5: ㅔ
    { onset: { ih: 0.52 }, main: { oh: 0.66, aa: 0.36 } },             // 6: ㅕ
    { onset: { ih: 0.50 }, main: { ee: 0.90 } },                       // 7: ㅖ
    { main: { oh: 1.0 } },                                             // 8: ㅗ
    { onset: { oh: 0.74 }, main: { aa: 0.95 } },                       // 9: ㅘ
    { onset: { oh: 0.68 }, main: { ee: 0.70, aa: 0.32 } },             // 10: ㅙ
    { main: { oh: 0.58, ee: 0.50 } },                                  // 11: ㅚ
    { onset: { ih: 0.48 }, main: { oh: 0.95 } },                       // 12: ㅛ
    { main: { ou: 1.0 } },                                             // 13: ㅜ
    { onset: { ou: 0.74 }, main: { oh: 0.66, aa: 0.34 } },             // 14: ㅝ
    { onset: { ou: 0.68 }, main: { ee: 0.88 } },                       // 15: ㅞ
    { onset: { ou: 0.72 }, main: { ih: 0.92 } },                       // 16: ㅟ
    { onset: { ih: 0.48 }, main: { ou: 0.96 } },                       // 17: ㅠ
    { main: { ou: 0.52, ih: 0.48 } },                                  // 18: ㅡ
    { onset: { ou: 0.50 }, main: { ih: 0.92 } },                       // 19: ㅢ
    { main: { ih: 1.0 } }                                              // 20: ㅣ
  ]);
  const HANGUL_BILABIAL_CHOSEONG = new Set([6, 7, 8, 17]); // ㅁ, ㅂ, ㅃ, ㅍ

  const LATIN_DIGRAPHS = Object.freeze({
    oo: { main: { ou: 0.96 } }, ee: { main: { ih: 0.90, ee: 0.24 } }, ea: { main: { ih: 0.84, ee: 0.32 } },
    ou: { onset: { aa: 0.68 }, main: { ou: 0.92 } }, ow: { onset: { aa: 0.62 }, main: { oh: 0.86 } },
    ai: { onset: { ee: 0.84 }, main: { ih: 0.74 } }, ay: { onset: { ee: 0.84 }, main: { ih: 0.74 } },
    ei: { onset: { ee: 0.84 }, main: { ih: 0.74 } }, ey: { onset: { ee: 0.80 }, main: { ih: 0.74 } },
    oi: { onset: { oh: 0.86 }, main: { ih: 0.78 } }, oy: { onset: { oh: 0.86 }, main: { ih: 0.78 } },
    au: { main: { oh: 0.84, aa: 0.30 } }, aw: { main: { oh: 0.84, aa: 0.30 } },
    oa: { main: { oh: 0.94 } }, oe: { main: { oh: 0.62, ee: 0.44 } },
    ie: { onset: { ih: 0.72 }, main: { ee: 0.74 } }, ue: { main: { ou: 0.90 } },
    ui: { onset: { ou: 0.72 }, main: { ih: 0.82 } }, eu: { main: { ou: 0.76, ee: 0.26 } }
  });
  const LATIN_VOWELS = Object.freeze({
    a: { main: { aa: 1.0 } }, e: { main: { ee: 0.92 } }, i: { main: { ih: 0.95 } },
    o: { main: { oh: 0.96 } }, u: { main: { ou: 0.94 } }, y: { main: { ih: 0.80 } }
  });

  const SENTENCE_BREAK_RE = /^[.!?…\n。！？]$/;
  const CLAUSE_BREAK_RE = /^[,;:、\-—–/()[\]]$/;
  const WHITESPACE_RE = /^[\s\u00a0]$/;
  const BILABIAL_LATIN_RE = /^[bmp]$/;
  const NARROW_CONSONANT_RE = /^[fvsz]$/;

  function clamp01(v) {
    return !Number.isFinite(v) || v <= 0 ? 0 : (v >= 1 ? 1 : v);
  }

  function smoothstep(x) {
    const t = clamp01(x);
    return t * t * (3 - 2 * t);
  }

  function decomposeHangulSyllable(code) {
    if (code < 0xAC00 || code > 0xD7A3) return null;
    const s = code - 0xAC00;
    const choseong = Math.floor(s / 588);
    return {
      choseong,
      jungseong: Math.floor((s % 588) / 28),
      jongseong: s % 28,
      bilabialOnset: HANGUL_BILABIAL_CHOSEONG.has(choseong)
    };
  }

  function normalizeLatinChar(ch) {
    const code = ch.charCodeAt(0);
    if ((code >= 0x41 && code <= 0x5A) || (code >= 0x61 && code <= 0x7A)) return ch.toLowerCase();
    if (code >= 0x00C0 && code <= 0x024F) {
      const stripped = ch.normalize("NFD").replace(/[\u0300-\u036f]/g, "").toLowerCase();
      if (stripped.length === 1 && stripped >= "a" && stripped <= "z") return stripped;
    }
    return "";
  }

  function isVowelChar(ch) {
    return ch === "a" || ch === "e" || ch === "i" || ch === "o" || ch === "u";
  }

  function syllableAmp(idx, seed) {
    return MAX_PEAK_WEIGHT * (0.82 + 0.12 * Math.sin(idx * 1.65 + (seed % 7) * 0.7));
  }

  function makeToken(baseMs, main, amp, extra) {
    return {
      isPause: !!(extra && extra.isPause),
      baseMs,
      boundary: (extra && extra.boundary) || "",
      onset: (extra && extra.onset) || null,
      main: main || null,
      amp,
      bilabial: !!(extra && extra.bilabial)
    };
  }

  function analyzeText(rawText) {
    const text = typeof rawText === "string" ? rawText.trim() : "";
    if (!text) return { tokens: [], rawDurationMs: 0 };

    const chars = Array.from(text);
    const tokens = [];
    let speakIndex = 0;

    function pushPause(baseMs, boundary) {
      const prev = tokens[tokens.length - 1];
      if (prev && prev.isPause) {
        if (baseMs > prev.baseMs) prev.baseMs = baseMs;
        if (boundary === "sentence" || (boundary === "clause" && !prev.boundary)) prev.boundary = boundary;
        return;
      }
      if (!tokens.length) return;
      tokens.push(makeToken(baseMs, null, 0, { isPause: true, boundary }));
    }

    let i = 0;
    while (i < chars.length) {
      const ch = chars[i];
      const code = ch.codePointAt(0);

      // 1. Hangul syllable (U+AC00..U+D7A3) & compatibility jamo.
      const hangul = decomposeHangulSyllable(code);
      if (hangul) {
        const spec = HANGUL_JUNGSEONG[hangul.jungseong];
        tokens.push(makeToken(hangul.jongseong ? 175 : 158, spec.main, syllableAmp(speakIndex++, code),
          { onset: spec.onset, bilabial: hangul.bilabialOnset }));
        i++; continue;
      }
      if (code >= 0x314F && code <= 0x3163) {
        const spec = HANGUL_JUNGSEONG[code - 0x314F];
        tokens.push(makeToken(145, spec.main, syllableAmp(speakIndex++, code) * 0.92, { onset: spec.onset }));
        i++; continue;
      }
      if (code >= 0x3131 && code <= 0x314E) {
        const giggle = ch === "ㅋ" ? { ih: 0.56, ee: 0.24 } : { aa: 0.42, oh: 0.34 };
        tokens.push(makeToken(115, giggle, syllableAmp(speakIndex++, code) * 0.68));
        i++; continue;
      }

      // 2. Punctuation & whitespace pauses.
      if (SENTENCE_BREAK_RE.test(ch)) { pushPause(265, "sentence"); i++; continue; }
      if (CLAUSE_BREAK_RE.test(ch)) { pushPause(185, "clause"); i++; continue; }
      if (WHITESPACE_RE.test(ch)) { pushPause(55, ""); i++; continue; }

      // 3. Latin word run (vowels, digraphs, consonant closure dips).
      if (normalizeLatinChar(ch)) {
        let word = "";
        while (i < chars.length) {
          const nl = normalizeLatinChar(chars[i]);
          if (nl) { word += nl; i++; }
          else if (chars[i] === "'" || chars[i] === "’") { i++; }
          else break;
        }
        let j = 0, vowelsInWord = 0;
        const isVocalicAt = idx => isVowelChar(word[idx]) ||
          (word[idx] === "y" && (idx === word.length - 1 || !isVowelChar(word[idx + 1])));
        while (j < word.length) {
          const c = word[j];
          const pair = j + 1 < word.length ? word.slice(j, j + 2) : "";
          if (pair && Object.hasOwn(LATIN_DIGRAPHS, pair)) {
            const spec = LATIN_DIGRAPHS[pair];
            vowelsInWord++;
            tokens.push(makeToken(162, spec.main, syllableAmp(speakIndex++, pair.charCodeAt(0) + pair.charCodeAt(1)),
              { onset: spec.onset }));
            j += 2; continue;
          }
          if (isVocalicAt(j)) {
            const silentFinalE = c === "e" && j === word.length - 1 && word.length >= 4 &&
              vowelsInWord > 0 && !isVowelChar(word[j - 1]);
            if (silentFinalE) {
              tokens.push(makeToken(52, { ee: 0.24 }, 0.32));
            } else {
              vowelsInWord++;
              tokens.push(makeToken(140, LATIN_VOWELS[c].main, syllableAmp(speakIndex++, c.charCodeAt(0))));
            }
            j++; continue;
          }
          let hasBilabial = false, hasW = false, hasNarrow = false;
          while (j < word.length && !isVocalicAt(j)) {
            const cc = word[j++];
            if (BILABIAL_LATIN_RE.test(cc)) hasBilabial = true;
            else if (cc === "w") hasW = true;
            else if (NARROW_CONSONANT_RE.test(cc)) hasNarrow = true;
          }
          if (hasW) tokens.push(makeToken(64, { ou: 0.44 }, 0.56));
          else if (hasBilabial) tokens.push(makeToken(48, null, 0, { bilabial: true }));
          else tokens.push(makeToken(44, hasNarrow ? { ih: 0.20 } : { ee: 0.10 }, 0.34));
        }
        continue;
      }

      // 4. Digits & other CJK (Hiragana/Katakana/Hanzi) deterministic 5-vowel cadence.
      if ((code >= 0x30 && code <= 0x39) || (code >= 0x3040 && code <= 0x30FF) || (code >= 0x4E00 && code <= 0x9FFF)) {
        const key = VISEMES[(code + speakIndex) % VISEMES.length];
        tokens.push(makeToken(155, { [key]: 0.88 }, syllableAmp(speakIndex++, code) * 0.88));
        i++; continue;
      }
      i++;
    }

    let trailingBoundary = "";
    while (tokens.length && tokens[tokens.length - 1].isPause) {
      const popped = tokens.pop();
      if (popped.boundary === "sentence" || !trailingBoundary) trailingBoundary = popped.boundary;
    }
    if (!tokens.length) return { tokens: [], rawDurationMs: 0 };
    tokens.push(makeToken(75, null, 0, { isPause: true, boundary: trailingBoundary }));

    let rawDurationMs = 0;
    for (const tok of tokens) rawDurationMs += tok.baseMs;
    return { tokens, rawDurationMs };
  }

  function estimateDurationMs(text) {
    const { rawDurationMs } = analyzeText(text);
    return rawDurationMs <= 0 ? 0 : Math.round(Math.max(MIN_DURATION_MS, Math.min(MAX_DURATION_MS, rawDurationMs)));
  }

  function normalizeMouthSample(raw) {
    if (!raw || typeof raw !== "object") return SILENT_SAMPLE;
    return {
      aa: clamp01(raw.aa), ih: clamp01(raw.ih), ou: clamp01(raw.ou),
      ee: clamp01(raw.ee), oh: clamp01(raw.oh),
      speaking: !!raw.speaking,
      boundary: typeof raw.boundary === "string" ? raw.boundary : ""
    };
  }

  function isValidMouthDriver(driver) {
    return !!(driver && typeof driver === "object" && typeof driver.sample === "function");
  }

  function createProceduralMouthDriver() {
    let timeline = [], durationSec = 0, elapsedSec = 0, active = false, lastBoundaryIdx = -1;
    const smooth = { aa: 0, ih: 0, ou: 0, ee: 0, oh: 0 };
    const rawTarget = { aa: 0, ih: 0, ou: 0, ee: 0, oh: 0 };

    function stop() {
      active = false;
      timeline = [];
      durationSec = 0;
      elapsedSec = 0;
      lastBoundaryIdx = -1;
      smooth.aa = smooth.ih = smooth.ou = smooth.ee = smooth.oh = 0;
    }

    function start(text, opts = {}) {
      const analyzed = analyzeText(text);
      if (!analyzed.tokens.length || analyzed.rawDurationMs <= 0) { stop(); return; }
      const explicitMs = Number(opts?.durationMs);
      const targetMs = Number.isFinite(explicitMs) && explicitMs > 0
        ? Math.max(EXPLICIT_MIN_MS, Math.min(EXPLICIT_MAX_MS, explicitMs))
        : Math.max(MIN_DURATION_MS, Math.min(MAX_DURATION_MS, analyzed.rawDurationMs));
      const scale = targetMs / analyzed.rawDurationMs;
      timeline = [];
      let cursor = 0;
      for (const tok of analyzed.tokens) {
        const dur = (tok.baseMs / 1000) * scale;
        timeline.push({ ...tok, start: cursor, end: cursor + dur, dur });
        cursor += dur;
      }
      durationSec = cursor;
      elapsedSec = 0;
      lastBoundaryIdx = -1;
      active = true;
    }

    function sample(dt) {
      if (!active || !timeline.length) return SILENT_SAMPLE;
      const step = Number.isFinite(dt) && dt > 0 ? Math.min(dt, 0.12) : 0.016;
      elapsedSec += step;
      if (elapsedSec >= durationSec) { stop(); return SILENT_SAMPLE; }

      rawTarget.aa = rawTarget.ih = rawTarget.ou = rawTarget.ee = rawTarget.oh = 0;
      let boundary = "", tokIdx = 0;
      for (let k = 0; k < timeline.length; k++) {
        if (elapsedSec < timeline[k].end) { tokIdx = k; break; }
      }
      const tok = timeline[tokIdx];
      if (tok) {
        if (tok.boundary && tokIdx !== lastBoundaryIdx) {
          lastBoundaryIdx = tokIdx;
          boundary = tok.boundary;
        }
        if (!tok.isPause && tok.dur > 0 && tok.amp > 0) {
          const u = clamp01((elapsedSec - tok.start) / tok.dur);
          const uAdj = tok.bilabial ? clamp01((u - 0.14) / 0.86) : u;
          const env = smoothstep(uAdj / 0.24) * smoothstep((1 - uAdj) / 0.28) * (0.92 + 0.08 * Math.sin(uAdj * Math.PI));
          if (tok.onset && uAdj < 0.42) {
            const blend = smoothstep(uAdj / 0.42);
            const wOn = (1 - blend) * tok.amp * env, wMain = blend * tok.amp * env;
            for (const v of VISEMES) rawTarget[v] = (tok.onset[v] || 0) * wOn + ((tok.main && tok.main[v]) || 0) * wMain;
          } else if (tok.main) {
            const gain = tok.amp * env;
            for (const v of VISEMES) rawTarget[v] = (tok.main[v] || 0) * gain;
          }
        }
      }

      const attackAlpha = 1 - Math.exp(-24 * step);
      const releaseAlpha = 1 - Math.exp(-16 * step);
      for (const v of VISEMES) {
        const t = rawTarget[v], cur = smooth[v];
        const next = cur + (t - cur) * (t >= cur ? attackAlpha : releaseAlpha);
        smooth[v] = next < 0.002 ? 0 : (next > 1 ? 1 : next);
      }
      return { aa: smooth.aa, ih: smooth.ih, ou: smooth.ou, ee: smooth.ee, oh: smooth.oh, speaking: true, boundary };
    }

    return {
      name: "procedural",
      start,
      stop,
      isActive: () => active,
      sample,
      debugState: () => ({ name: "procedural", active, elapsedSec, durationSec, tokenCount: timeline.length })
    };
  }

  // Natural speech-time blink modulator: phrase-boundary micro-blinks + anti-stare cadence.
  function createSpeechBlinkModulator() {
    let progress = -1, duration = 0.15, peak = 0.84, timeSinceBlink = 0.9, nextPeriodicSec = 2.3, cycle = 0;

    function reset() {
      progress = -1;
      timeSinceBlink = 0.9;
      nextPeriodicSec = 2.3;
    }

    function trigger(isSentence) {
      progress = 0;
      cycle = (cycle + 1) % 5;
      duration = isSentence ? 0.16 : 0.14;
      peak = isSentence ? 0.90 : 0.78 + (cycle % 3) * 0.04;
      timeSinceBlink = 0;
      nextPeriodicSec = 2.1 + (cycle % 3) * 0.35;
    }

    function update(dt, ctx) {
      const step = Number.isFinite(dt) && dt > 0 ? Math.min(dt, 0.12) : 0.016;
      if (!ctx?.allowed) { progress = -1; return 0; }
      const autoBlink = Number.isFinite(ctx.autoBlink) ? ctx.autoBlink : 0;
      timeSinceBlink = autoBlink > 0.12 ? 0 : timeSinceBlink + step;

      if (progress < 0 && ctx.speaking && autoBlink < 0.05) {
        const b = ctx.boundary || "";
        if ((b === "sentence" && timeSinceBlink >= 0.85) || (b === "clause" && timeSinceBlink >= 1.25)) {
          trigger(b === "sentence");
        } else if (timeSinceBlink >= nextPeriodicSec) {
          trigger(false);
        }
      }
      if (progress >= 0) {
        progress += step;
        if (progress >= duration) { progress = -1; return 0; }
        return clamp01(peak * Math.pow(Math.sin(Math.PI * Math.pow(progress / duration, 0.80)), 1.32));
      }
      return 0;
    }

    return { reset, update };
  }

  function computeVisemeAttenuation(state, overlay, targets, keys) {
    let factor = Object.hasOwn(STATE_ATTENUATION, state) ? STATE_ATTENUATION[state] : 1.0;
    const mode = overlay?.mode || "";
    if (mode === "sleepy") factor = Math.min(factor, 0.45);
    else if (mode === "knock-startle" || mode === "wakeup-jolt" || mode === "dizzy") factor = Math.min(factor, 0.50);
    else if (mode === "phone-facedown-merong") factor = Math.min(factor, 0.42);
    else if (mode === "annoyed-tap") factor = Math.min(factor, 0.72);

    if (targets && keys) {
      const happyW = keys.happy ? (targets.get(keys.happy) || 0) : 0;
      const surpW = keys.surprised ? (targets.get(keys.surprised) || 0) : 0;
      const sadW = keys.sad ? (targets.get(keys.sad) || 0) : 0;
      if (happyW > 0.15) factor = Math.min(factor, 1.0 - happyW * 0.48);
      if (surpW > 0.15) factor = Math.min(factor, 1.0 - surpW * 0.52);
      if (sadW > 0.25) factor = Math.min(factor, 1.0 - sadW * 0.42);
    }
    return Math.max(0.38, Math.min(1.0, factor));
  }

  function isSpeechBlinkAllowed(state, overlay, allowBlink, targets, keys) {
    if (!allowBlink || EYE_CLOSING_STATES.has(state) || EYE_CLOSING_OVERLAYS.has(overlay?.mode || "")) return false;
    if (targets && keys) {
      if (keys.blinkLeft && (targets.get(keys.blinkLeft) || 0) > 0.05) return false;
      if (keys.blinkRight && (targets.get(keys.blinkRight) || 0) > 0.05) return false;
      if (keys.happy && (targets.get(keys.happy) || 0) >= 0.55) return false;
      if (keys.sad && (targets.get(keys.sad) || 0) >= 0.55) return false;
    }
    return true;
  }

  function applySpeechToTargets(opts) {
    const { dt, state, overlay, allowBlink, autoBlink, targets, keys, visemeKeys, sample, blinkModulator } = opts;
    const norm = sample ? normalizeMouthSample(sample) : SILENT_SAMPLE;
    const speechBlinkAllowed = isSpeechBlinkAllowed(state, overlay, allowBlink, targets, keys);
    const speechBlink = blinkModulator
      ? blinkModulator.update(dt, { allowed: speechBlinkAllowed, speaking: norm.speaking, boundary: norm.boundary, autoBlink })
      : 0;
    if (speechBlinkAllowed && speechBlink > 0 && keys?.blink) {
      targets.set(keys.blink, clamp01(Math.max(targets.get(keys.blink) || 0, speechBlink)));
    }
    const attenuation = computeVisemeAttenuation(state, overlay, targets, keys);
    if (norm.speaking && visemeKeys) {
      for (const v of VISEMES) {
        const resolved = visemeKeys[v];
        if (!resolved) continue;
        const val = clamp01(norm[v] * attenuation);
        if (val <= 0.001) continue;
        const existing = targets.get(resolved) || 0;
        targets.set(resolved, clamp01(existing > 0.001 ? Math.min(0.90, existing * 0.45 + val) : val));
      }
    }
    return { attenuation, speechBlink, speaking: norm.speaking };
  }

  C.vrmSpeech = {
    VISEMES, VISEME_ALIASES, MIN_DURATION_MS, MAX_DURATION_MS, SILENT_SAMPLE, EYE_CLOSING_STATES,
    decomposeHangulSyllable, analyzeText, estimateDurationMs, normalizeMouthSample, isValidMouthDriver,
    createProceduralMouthDriver, createSpeechBlinkModulator, computeVisemeAttenuation,
    isSpeechBlinkAllowed, applySpeechToTargets
  };
})(window.WsCompanion);
