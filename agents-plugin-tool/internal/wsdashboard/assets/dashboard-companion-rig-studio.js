(function (C) {
  "use strict";

  // Control-Rig & Pack Clip Studio controller + pure testable helpers (C.rigStudio).

  const SENTINEL_KEYS = Object.freeze([
    "idle", "laptop_work", "phone_hold", "phone_typing",
    "phone_putdown", "knock_startle", "wakeup_jolt"
  ]);
  const SENTINEL_DEFAULTS = Object.freeze({
    idle:          { loop: true,  prop: "none",       beat: "idle / ambient" },
    laptop_work:   { loop: true,  prop: "laptop",     beat: "laptop working" },
    phone_hold:    { loop: false, prop: "smartphone", beat: "phone held" },
    phone_typing:  { loop: true,  prop: "smartphone", beat: "phone typing" },
    phone_putdown: { loop: false, prop: "smartphone", beat: "phone putdown" },
    knock_startle: { loop: false, prop: "none",       beat: "glass knock" },
    wakeup_jolt:   { loop: false, prop: "none",       beat: "wake from doze" }
  });
  const BUILTIN_PROPS = Object.freeze({
    smartphone: Object.freeze(["grip", "screen"]),
    laptop: Object.freeze(["keyboard", "screen"])
  });
  const COMMON_BONES = Object.freeze(["head", "chest", "leftHand", "rightHand", "hips"]);
  const EASING_OPTIONS = Object.freeze(["ease_in_out", "linear", "ease_in", "ease_out", "ease_out_back", "spring"]);
  const SPEED_OPTIONS = Object.freeze([0.5, 1.0, 1.5]);
  const DURATION_PRESETS = Object.freeze([0.8, 1.2, 1.4, 2.0, 3.0]);
  const EXPRESSION_CHIPS = Object.freeze(["happy", "relaxed", "surprised", "angry", "sad", "half_lid", "wink", "blink"]);
  const FINGER_PRESET_NAMES = Object.freeze(["relaxed", "typing", "phone-grip", "point"]);
  const FINGER_DIGIT_LABELS = Object.freeze(["Thumb", "Index", "Middle", "Ring", "Little"]);

  const MAX_STUDIO_KEYFRAMES = 8;
  const MAX_DESC_RUNES = 120;
  const MAX_EXPOSED_ACTIONS = 24;
  const ACTIONS_BUDGET_RUNES = 3200;
  const CLIP_KEY_RE = /^[a-z][a-z0-9_]{0,31}$/;
  const RESERVED_WIN_RE = /^(con|prn|aux|nul|com[1-9]|lpt[1-9])$/;
  const BAD_DESC_CHAR_RE = /[\u0000-\u001F\u007F-\u009F\u200B-\u200F\u2028\u2029\u202A-\u202E\u2060-\u206F\uFEFF]/;

  function tr(key, ...args) {
    if (typeof window !== "undefined" && typeof window.wsT === "function") return window.wsT(key, ...args);
    let s = key;
    for (let i = 0; i < args.length; i++) s += " " + args[i];
    return s;
  }

  function countRunes(str) {
    return typeof str === "string" ? Array.from(str).length : 0;
  }

  function trimRunes(str, maxRunes) {
    const runes = Array.from(String(str || ""));
    if (maxRunes <= 0 || runes.length <= maxRunes) return runes.join("");
    if (maxRunes === 1) return "…";
    return runes.slice(0, maxRunes - 1).join("") + "…";
  }

  function quoteDelimiters(str) {
    return String(str || "").replace(/<<</g, "‹‹‹").replace(/>>>/g, "›››");
  }

  function validateActionKey(key) {
    if (typeof key !== "string" || !CLIP_KEY_RE.test(key)) return { ok: false, reason: "invalid_pattern" };
    if (RESERVED_WIN_RE.test(key)) return { ok: false, reason: "reserved_name" };
    return { ok: true };
  }

  function validateDescription(str) {
    const s = typeof str === "string" ? str : "";
    const runes = countRunes(s);
    if (runes > MAX_DESC_RUNES) return { ok: false, reason: "too_long", runes };
    if (BAD_DESC_CHAR_RE.test(s)) return { ok: false, reason: "bad_chars", runes };
    return { ok: true, runes };
  }

  function sanitizeActionDescription(str) {
    const collapsed = String(str || "").trim().split(/\s+/).filter(Boolean).join(" ");
    const safeBrackets = collapsed.replace(/\[/g, "(").replace(/\]/g, ")");
    return quoteDelimiters(trimRunes(safeBrackets, MAX_DESC_RUNES));
  }

  // Matches Go formatActionsEnvelopeBody in companion_soul_prompt.go byte-for-byte.
  function formatActionsPreview(actions) {
    const exposed = (Array.isArray(actions) ? actions : []).filter(a => a && a.expose_to_agent === true && CLIP_KEY_RE.test(a.key || ""));
    const lines = [];
    for (const a of exposed) {
      if (lines.length === MAX_EXPOSED_ACTIONS) break;
      let line = "- " + a.key;
      const desc = sanitizeActionDescription(a.description || "");
      if (desc) line += ": " + desc;
      if (CLIP_KEY_RE.test(a.expression || "")) line += " [expression: " + a.expression + "]";
      if (a.prop && a.prop !== "none" && CLIP_KEY_RE.test(a.prop)) line += " [prop: " + a.prop + "]";
      if (a.loop) line += " [loop]";
      lines.push({ key: a.key, text: line });
    }
    const valid = exposed.length;
    const fits = (arr, more) => {
      let n = 0;
      for (let i = 0; i < arr.length; i++) {
        if (i > 0) n++;
        n += countRunes(arr[i].text);
      }
      if (more > 0) {
        if (arr.length > 0) n++;
        n += countRunes("(+" + more + " more not shown)");
      }
      return n <= ACTIONS_BUDGET_RUNES;
    };
    while (lines.length > 0 && !fits(lines, valid - lines.length)) lines.pop();
    const omittedCount = valid - lines.length;
    const outLines = lines.slice();
    if (omittedCount > 0) outLines.push({ key: "", text: "(+" + omittedCount + " more not shown)", meta: true });
    const text = outLines.length ? outLines.map(l => l.text).join("\n") : "(none)";
    return {
      text,
      lines: outLines,
      exposedCount: valid,
      shownCount: lines.length,
      omittedCount,
      runeCount: countRunes(text)
    };
  }

  function clonePreservingUnknown(obj) {
    return obj === undefined ? undefined : JSON.parse(JSON.stringify(obj));
  }

  function isPackAuthorable(pack) {
    if (!pack || typeof pack !== "object") return { ok: false, reason: "not_found" };
    if (pack.id === "default" || pack.builtin === true || pack.source === "builtin" || pack.source === "embedded") {
      return { ok: false, reason: "builtin" };
    }
    if (pack.renderer !== "vrm") return { ok: false, reason: "not_vrm" };
    if (pack.origin && pack.origin.kind && pack.origin.kind !== "local") {
      return { ok: false, reason: "remote" };
    }
    return { ok: true };
  }

  function makeDefaultKeyframe(t, label) {
    return {
      t: Number(t.toFixed(2)),
      label: label || "pose",
      easing: "ease_in_out",
      expression: {},
      head: { yaw_deg: 0, pitch_deg: 0, roll_deg: 0, lean_deg: 0, turn_deg: 0 },
      hands: {
        left:  { ik: [0.38, -0.42, 0.28], pole: [0.3, -0.4, -0.85], fingers: { preset: "relaxed" } },
        right: { ik: [-0.38, -0.42, 0.28], pole: [-0.3, -0.4, -0.85], fingers: { preset: "relaxed" } }
      }
    };
  }

  function makeDefaultClip(key, opts) {
    const isSentinel = SENTINEL_KEYS.includes(key);
    const sDef = isSentinel ? SENTINEL_DEFAULTS[key] : null;
    const dur = Number.isFinite(opts?.duration_s) ? opts.duration_s : 1.4;
    const loop = opts?.loop !== undefined ? !!opts.loop : (sDef ? sDef.loop : false);
    const kf0 = makeDefaultKeyframe(0, "start");
    const kf1 = makeDefaultKeyframe(dur * 0.5, "peak");
    kf1.head.yaw_deg = 6;
    kf1.head.pitch_deg = -4;
    kf1.hands.right.ik = [-0.28, -0.05, 0.44];
    const clip = {
      schema: "ws-companion-clip/v1",
      key,
      duration_s: dur,
      loop,
      easing: "ease_in_out",
      keyframes: [kf0, kf1]
    };
    if (sDef && sDef.prop && sDef.prop !== "none") {
      clip.prop = { id: sDef.prop, offset: [0, 0, 0], tilt_deg: 0 };
    }
    return clip;
  }

  function addKeyframeSnapshot(clip, timeSec, sample) {
    if (!clip || !Array.isArray(clip.keyframes)) return { ok: false, reason: "invalid_clip" };
    if (clip.keyframes.length >= MAX_STUDIO_KEYFRAMES) return { ok: false, reason: "max_keyframes" };
    const dur = Math.max(0.1, Number(clip.duration_s) || 1.4);
    let t = Math.max(0, Math.min(dur, Number(Number(timeSec || 0).toFixed(2))));
    // Keep t strictly ascending; nudge by 0.02s if colliding with an existing keyframe.
    const taken = new Set(clip.keyframes.map(k => Number(k.t).toFixed(2)));
    let guard = 0;
    while (taken.has(t.toFixed(2)) && guard++ < 100) {
      t = Number(Math.min(dur, t + 0.02).toFixed(2));
      if (taken.has(t.toFixed(2)) && t >= dur) {
        t = Number(Math.max(0, (Number(timeSec) || 0) - 0.02 * guard).toFixed(2));
      }
    }
    if (taken.has(t.toFixed(2))) return { ok: false, reason: "time_collision" };

    const kf = {
      t,
      label: "kf_" + (clip.keyframes.length + 1),
      easing: "ease_in_out",
      expression: sample?.expression ? clonePreservingUnknown(sample.expression) : {},
      head: sample?.head ? clonePreservingUnknown(sample.head) : { yaw_deg: 0, pitch_deg: 0, roll_deg: 0, lean_deg: 0, turn_deg: 0 },
      hands: {
        left: {
          ik: sample?.hands?.left?.ik ? sample.hands.left.ik.map(v => Number(v.toFixed(3))) : [0.38, -0.42, 0.28],
          pole: sample?.hands?.left?.pole ? sample.hands.left.pole.map(v => Number(v.toFixed(3))) : [0.3, -0.4, -0.85],
          fingers: sample?.hands?.left?.fingers ? clonePreservingUnknown(sample.hands.left.fingers) : { preset: "relaxed" }
        },
        right: {
          ik: sample?.hands?.right?.ik ? sample.hands.right.ik.map(v => Number(v.toFixed(3))) : [-0.38, -0.42, 0.28],
          pole: sample?.hands?.right?.pole ? sample.hands.right.pole.map(v => Number(v.toFixed(3))) : [-0.3, -0.4, -0.85],
          fingers: sample?.hands?.right?.fingers ? clonePreservingUnknown(sample.hands.right.fingers) : { preset: "relaxed" }
        }
      }
    };
    if (sample?.target_attach) kf.target_attach = clonePreservingUnknown(sample.target_attach);
    clip.keyframes.push(kf);
    clip.keyframes.sort((a, b) => a.t - b.t);
    const index = clip.keyframes.indexOf(kf);
    return { ok: true, index, keyframe: kf };
  }

  function formatAttachSpaceOptions(currentSpace) {
    const opts = [
      { value: "free", label: "free" },
      { value: "camera", label: "camera" }
    ];
    for (const [propId, anchors] of Object.entries(BUILTIN_PROPS)) {
      for (const anchor of anchors) {
        const val = "prop:" + propId + "#" + anchor;
        opts.push({ value: val, label: val });
      }
    }
    for (const bone of COMMON_BONES) {
      const val = "bone:" + bone;
      opts.push({ value: val, label: val });
    }
    if (currentSpace && !opts.some(o => o.value === currentSpace)) {
      opts.push({ value: currentSpace, label: currentSpace + " (custom)" });
    }
    return opts;
  }

  // Build PUT /api/companion/packs/{id}/clips/{key} body while preserving all
  // unknown fields from original entry & clip JSON.
  function buildPutPayload(item) {
    const isSentinel = !!item.isSentinel;
    const baseEntry = item.rawEntry && typeof item.rawEntry === "object" ? clonePreservingUnknown(item.rawEntry) : {};
    const kind = item.kind === "vrma" ? "vrma" : "clip";
    const file = "clips/" + item.key + (kind === "vrma" ? ".vrma" : ".clip.json");
    baseEntry.kind = kind;
    baseEntry.file = file;
    if (typeof item.loop === "boolean") baseEntry.loop = item.loop;
    if (Number.isFinite(item.duration_s)) baseEntry.duration_s = Number(item.duration_s);
    if (!isSentinel) {
      baseEntry.key = item.key;
      baseEntry.description = String(item.description || "");
      baseEntry.expose_to_agent = item.expose_to_agent === true;
      if (item.expression) baseEntry.expression = item.expression;
      else delete baseEntry.expression;
      if (item.prop && item.prop !== "none") baseEntry.prop = item.prop;
      else delete baseEntry.prop;
    } else {
      delete baseEntry.key;
    }
    const payload = { entry: baseEntry };
    if (kind === "clip" && item.clip) {
      const clipOut = clonePreservingUnknown(item.clip);
      clipOut.schema = clipOut.schema || "ws-companion-clip/v1";
      clipOut.key = item.key;
      clipOut.duration_s = Number(clipOut.duration_s || item.duration_s || 1.4);
      clipOut.loop = !!clipOut.loop;
      payload.clip = clipOut;
    }
    return payload;
  }

  function rescaleClipDuration(clip, nextDur) {
    if (!clip || typeof clip !== "object") return clip;
    const cur = Math.max(0.1, Number(clip.duration_s) || 1.4);
    const targetDur = Math.max(0.1, Number(nextDur) || cur);
    const scale = targetDur / cur;
    clip.duration_s = targetDur;
    const kfs = Array.isArray(clip.keyframes) ? clip.keyframes : [];
    if (!kfs.length) return clip;
    const maxTicks = Math.floor(targetDur * 100 + 1e-9);
    if (kfs.length - 1 <= maxTicks) {
      const ticks = kfs.map(kf => Math.max(0, Math.min(maxTicks, Math.round((Number(kf.t) || 0) * scale * 100))));
      for (let i = 1; i < ticks.length; i++) {
        if (ticks[i] <= ticks[i - 1]) ticks[i] = ticks[i - 1] + 1;
      }
      if (ticks[ticks.length - 1] > maxTicks) ticks[ticks.length - 1] = maxTicks;
      for (let i = ticks.length - 2; i >= 0; i--) {
        if (ticks[i] >= ticks[i + 1]) ticks[i] = ticks[i + 1] - 1;
      }
      for (let i = 0; i < kfs.length; i++) kfs[i].t = Number((ticks[i] / 100).toFixed(2));
    } else {
      const step = targetDur / Math.max(1, kfs.length - 1);
      for (let i = 0; i < kfs.length; i++) kfs[i].t = Number(Math.min(targetDur, i * step).toFixed(4));
    }
    return clip;
  }

  function buildManifestItems(manifest) {
    const fresh = new Map();
    const rawSentinels = manifest?.sentinels && typeof manifest.sentinels === "object" ? manifest.sentinels : {};
    for (const key of SENTINEL_KEYS) {
      const raw = rawSentinels[key];
      if (raw && typeof raw === "object") {
        fresh.set(key, {
          key,
          isSentinel: true,
          saved: true,
          kind: raw.kind === "vrma" ? "vrma" : "clip",
          file: raw.file || ("clips/" + key + (raw.kind === "vrma" ? ".vrma" : ".clip.json")),
          loop: typeof raw.loop === "boolean" ? raw.loop : SENTINEL_DEFAULTS[key].loop,
          duration_s: Number.isFinite(raw.duration_s) ? raw.duration_s : undefined,
          rawEntry: clonePreservingUnknown(raw),
          clip: null,
          vrmaBuffer: null,
          dirty: false
        });
      }
    }
    const rawActions = Array.isArray(manifest?.actions) ? manifest.actions : [];
    for (const raw of rawActions) {
      if (!raw || typeof raw !== "object" || !CLIP_KEY_RE.test(raw.key || "")) continue;
      if (fresh.has(raw.key) || SENTINEL_KEYS.includes(raw.key)) continue;
      fresh.set(raw.key, {
        key: raw.key,
        isSentinel: false,
        saved: true,
        kind: raw.kind === "vrma" ? "vrma" : "clip",
        file: raw.file || ("clips/" + raw.key + (raw.kind === "vrma" ? ".vrma" : ".clip.json")),
        description: typeof raw.description === "string" ? raw.description : "",
        expression: typeof raw.expression === "string" ? raw.expression : "",
        prop: typeof raw.prop === "string" ? raw.prop : "",
        expose_to_agent: raw.expose_to_agent === true,
        loop: typeof raw.loop === "boolean" ? raw.loop : false,
        duration_s: Number.isFinite(raw.duration_s) ? raw.duration_s : undefined,
        rawEntry: clonePreservingUnknown(raw),
        clip: null,
        vrmaBuffer: null,
        dirty: false
      });
    }
    return fresh;
  }

  function mergeManifestIntoItems(items, manifest, opts) {
    const fresh = buildManifestItems(manifest);
    const preserveDirty = !!opts?.preserveDirty;
    const replaceKey = typeof opts?.replaceKey === "string" ? opts.replaceKey : "";
    if (!preserveDirty) {
      items.clear();
      for (const [k, v] of fresh.entries()) items.set(k, v);
      return {
        calibration: manifest?.calibration && typeof manifest.calibration === "object"
          ? clonePreservingUnknown(manifest.calibration)
          : { props: {} },
        calibrationDirty: false
      };
    }
    const prev = new Map(items);
    items.clear();
    for (const [k, v] of fresh.entries()) {
      const existing = prev.get(k);
      if (existing && existing.dirty && k !== replaceKey) {
        items.set(k, existing);
      } else {
        items.set(k, v);
      }
    }
    for (const [k, existing] of prev.entries()) {
      if (!items.has(k) && existing.dirty && k !== replaceKey) {
        items.set(k, existing);
      }
    }
    if (opts?.calibrationDirty) {
      return {
        calibration: opts.calibration && typeof opts.calibration === "object" ? opts.calibration : { props: {} },
        calibrationDirty: true
      };
    }
    return {
      calibration: manifest?.calibration && typeof manifest.calibration === "object"
        ? clonePreservingUnknown(manifest.calibration)
        : { props: {} },
      calibrationDirty: false
    };
  }

  function sanitizeKeyFromFilename(name) {
    let base = String(name || "").replace(/^.*[/\\]/, "").replace(/\.vrma$/i, "").toLowerCase();
    base = base.replace(/[^a-z0-9_]+/g, "_").replace(/^_+|_+$/g, "");
    if (!base || !/^[a-z]/.test(base)) base = "clip_" + base;
    base = base.slice(0, 32);
    if (RESERVED_WIN_RE.test(base)) base = base + "_clip";
    return base;
  }

  Object.assign(C, {
    rigStudio: {
      SENTINEL_KEYS,
      SENTINEL_DEFAULTS,
      BUILTIN_PROPS,
      COMMON_BONES,
      EASING_OPTIONS,
      MAX_STUDIO_KEYFRAMES,
      MAX_DESC_RUNES,
      MAX_EXPOSED_ACTIONS,
      ACTIONS_BUDGET_RUNES,
      countRunes,
      validateActionKey,
      validateDescription,
      sanitizeActionDescription,
      formatActionsPreview,
      clonePreservingUnknown,
      isPackAuthorable,
      makeDefaultKeyframe,
      makeDefaultClip,
      addKeyframeSnapshot,
      rescaleClipDuration,
      mergeManifestIntoItems,
      formatAttachSpaceOptions,
      buildPutPayload,
      sanitizeKeyFromFilename
    }
  });

  // ───────────────────────────── Browser UI Controller ─────────────────────────────
  if (typeof document === "undefined" || !document.getElementById("studioApp")) return;

  const $ = id => document.getElementById(id);
  const el = (tag, cls, text) => {
    const node = document.createElement(tag);
    if (cls) node.className = cls;
    if (text !== undefined && text !== null) node.textContent = String(text);
    return node;
  };

  const ui = {
    packPill: $("packPill"), packDot: $("packDot"), packName: $("packName"), packMenu: $("packMenu"),
    kitChip: $("kitChip"), sumBadge: $("sumBadge"),
    vrmaFileInput: $("vrmaFileInput"), btnImport: $("btnImport"), btnExport: $("btnExport"),
    btnSave: $("btnSave"), saveLbl: $("saveLbl"), saveCnt: $("saveCnt"),
    banner: $("studioBanner"), bannerMsg: $("studioBannerMsg"),
    bannerReload: $("studioBannerReload"), bannerDismiss: $("studioBannerDismiss"),
    errorSec: $("studioError"), errorTitle: $("studioErrorTitle"), errorBody: $("studioErrorBody"), errorPacks: $("studioErrorPacks"),
    work: $("studioWork"), railScroll: $("railScroll"), railFootL: $("railFootL"), railFootR: $("railFootR"),
    vp: $("vp"), canvas: $("studioCanvas"), overlay: $("studioOverlay"),
    vpHint: $("vpHint"), vpLoading: $("vpLoading"), toast: $("toast"),
    segModes: $("segModes"), segSlot: $("segSlot"), segProp: $("segProp"), segTheme: $("segTheme"), btnCamReset: $("btnCamReset"),
    btnPlay: $("btnPlay"), btnLoop: $("btnLoop"), tlTime: $("tlTime"),
    pSpeed: $("pSpeed"), vSpeed: $("vSpeed"), pDur: $("pDur"), vDur: $("vDur"), pEase: $("pEase"), vEase: $("vEase"),
    btnSnap: $("btnSnap"), track: $("track"), ruler: $("ruler"), lane: $("lane"), playhead: $("playhead"),
    inspHead: $("inspHead"), inspBody: $("inspBody"), expoStrip: $("expoStrip"), tabBadge: $("tabBadge"),
    exportScrim: $("exportScrim"), mdTitle: $("mdTitle"), mdPre: $("mdPre"),
    mdDownload: $("mdDownload"), mdCopy: $("mdCopy"), mdClose: $("mdClose"),
    importScrim: $("importScrim"), impFileName: $("impFileName"), impPickBtn: $("impPickBtn"),
    impKeyInp: $("impKeyInp"), impSentinelRow: $("impSentinelRow"), impSentinelSwt: $("impSentinelSwt"), impSentinelChk: $("impSentinelChk"),
    impReplaceRow: $("impReplaceRow"), impReplaceSwt: $("impReplaceSwt"), impReplaceChk: $("impReplaceChk"),
    impErr: $("impErr"), impCancel: $("impCancel"), impSubmit: $("impSubmit")
  };

  const state = {
    boot: window.WsRigStudioBoot || {},
    packs: [],
    packInfo: null,
    manifest: null,
    rev: "",
    items: new Map(), // key -> { key, isSentinel, saved, kind, file, description, expression, prop, expose_to_agent, loop, duration_s, rawEntry, clip, vrmaBuffer, dirty, loading }
    calibration: { props: {} },
    calibrationDirty: false,
    selectedKey: "idle",
    selectedKfIdx: 0,
    inspectorTab: "rig",
    mode: "ik",
    handSide: "right",
    slotPx: 280,
    theme: (document.documentElement.dataset.theme === "light" ? "light" : "dark"),
    propId: "none",
    playing: false,
    playTime: 0,
    speedIdx: 1,
    saving: false,
    pendingVrmaFile: null,
    holdHandle: null,
    toastTimer: 0,
    vrmMeta: null
  };

  let stage = null;
  let selectGen = 0;
  let broadcast = null;
  try { if (typeof BroadcastChannel === "function") broadcast = new BroadcastChannel("ws-companion-packs"); } catch (_) {}

  function notifyBroadcast(packId, rev) {
    try { broadcast?.postMessage({ type: "companion-packs-changed", pack: packId, rev }); } catch (_) {}
  }

  function showToast(msg) {
    ui.toast.textContent = String(msg || "");
    ui.toast.classList.add("show");
    clearTimeout(state.toastTimer);
    state.toastTimer = setTimeout(() => ui.toast.classList.remove("show"), 2600);
  }

  function showBanner(msg, opts) {
    ui.bannerMsg.textContent = String(msg || "");
    ui.banner.classList.toggle("err", !!opts?.error);
    ui.bannerReload.hidden = !opts?.reload;
    ui.banner.hidden = false;
  }

  function hideBanner() { ui.banner.hidden = true; }

  function showErrorState(title, body, authorablePacks) {
    ui.work.hidden = true;
    ui.errorSec.hidden = false;
    ui.errorTitle.textContent = title;
    ui.errorBody.textContent = body;
    ui.errorPacks.replaceChildren();
    for (const p of authorablePacks || []) {
      const a = el("a", "rs-tbtn", p.name || p.id);
      a.href = "/companion/studio?pack=" + encodeURIComponent(p.id);
      ui.errorPacks.appendChild(a);
    }
    ui.btnImport.disabled = true;
    ui.btnExport.disabled = true;
    ui.btnSave.disabled = true;
  }

  function getSelectedItem() { return state.items.get(state.selectedKey) || null; }

  function dirtyItems() {
    const out = [];
    for (const it of state.items.values()) if (it.dirty) out.push(it);
    return out;
  }

  function customActionsList() {
    const out = [];
    for (const it of state.items.values()) if (!it.isSentinel) out.push(it);
    return out;
  }

  function currentSpeed() { return SPEED_OPTIONS[state.speedIdx] || 1.0; }

  function syncTopBar() {
    const dirtyCount = dirtyItems().length + (state.calibrationDirty ? 1 : 0);
    ui.btnSave.disabled = state.saving || !state.packInfo || dirtyCount === 0;
    ui.saveCnt.textContent = String(dirtyCount);
    ui.saveCnt.hidden = dirtyCount === 0;
    const item = getSelectedItem();
    ui.btnExport.disabled = !item || item.kind !== "clip" || !item.clip;
    ui.btnImport.disabled = !state.packInfo;

    const preview = formatActionsPreview(customActionsList());
    ui.tabBadge.textContent = String(preview.exposedCount);
    ui.tabBadge.classList.toggle("off", preview.exposedCount === 0);
    ui.sumBadge.textContent = tr("js.companion.rig_studio.exposed_summary", preview.exposedCount, MAX_EXPOSED_ACTIONS);
    ui.sumBadge.hidden = !state.packInfo;
  }

  function applyTheme(next) {
    state.theme = next === "light" ? "light" : "dark";
    document.documentElement.dataset.theme = state.theme;
    ui.vp.dataset.theme = state.theme;
    for (const b of ui.segTheme.querySelectorAll("button")) b.classList.toggle("on", b.dataset.theme === state.theme);
    stage?.setTheme(state.theme);
  }

  function applySlot(px) {
    state.slotPx = Number(px) || 280;
    ui.vp.dataset.slot = String(state.slotPx);
    for (const b of ui.segSlot.querySelectorAll("button")) b.classList.toggle("on", Number(b.dataset.slot) === state.slotPx);
    stage?.setSlot(state.slotPx);
  }

  function applyProp(propId) {
    state.propId = propId === "laptop" || propId === "smartphone" ? propId : "none";
    for (const b of ui.segProp.querySelectorAll("button")) b.classList.toggle("on", b.dataset.prop === state.propId);
    stage?.setProp(state.propId);
    if (state.inspectorTab === "rig") renderInspector();
  }

  function applyMode(mode) {
    state.mode = mode;
    for (const b of ui.segModes.querySelectorAll("button")) b.classList.toggle("on", b.dataset.mode === mode);
    stage?.setMode(mode);
    const hints = {
      ik: tr("js.companion.rig_studio.hint.ik"),
      pole: tr("js.companion.rig_studio.hint.pole"),
      look: tr("js.companion.rig_studio.hint.look"),
      prop: tr("js.companion.rig_studio.hint.prop")
    };
    ui.vpHint.textContent = hints[mode] || "";
  }

  function evaluateCurrentPose() {
    const item = getSelectedItem();
    if (!item || !stage) return;
    if (item.kind === "vrma") return;
    if (!item.clip) {
      stage.applyClipSample(C.vrmClip.sampleClip(makeDefaultClip(item.key), 0), null);
      return;
    }
    const sample = C.vrmClip.sampleClip(item.clip, state.playTime, { loop: !!item.loop });
    const kf = item.clip.keyframes[state.selectedKfIdx] || item.clip.keyframes[0] || null;
    stage.applyClipSample(sample, state.playing ? null : kf);
  }

  function renderPackMenu(authorable) {
    ui.packMenu.replaceChildren();
    for (const p of authorable) {
      const btn = el("button", "rs-pack-opt" + (state.packInfo && p.id === state.packInfo.id ? " on" : ""));
      btn.type = "button";
      btn.dataset.packId = p.id;
      btn.setAttribute("role", "menuitem");
      btn.append(el("span", "", p.name || p.id), el("span", "rs-mono", p.id));
      btn.addEventListener("click", () => {
        ui.packMenu.hidden = true;
        ui.packPill.setAttribute("aria-expanded", "false");
        if (state.packInfo && p.id === state.packInfo.id) return;
        window.location.href = "/companion/studio?pack=" + encodeURIComponent(p.id);
      });
      ui.packMenu.appendChild(btn);
    }
  }

  function renderRail() {
    const frag = document.createDocumentFragment();
    const sentOverrides = SENTINEL_KEYS.filter(k => {
      const it = state.items.get(k);
      return it && (it.saved || it.dirty);
    }).length;
    const sLbl = el("div", "rs-sec-lbl");
    sLbl.append(el("span", "", tr("js.companion.rig_studio.sentinels")), el("small", "", sentOverrides + "/" + SENTINEL_KEYS.length));
    frag.appendChild(sLbl);
    frag.appendChild(el("div", "rs-sec-hint", tr("js.companion.rig_studio.sentinels_hint")));

    for (const key of SENTINEL_KEYS) {
      const it = state.items.get(key);
      const isOver = !!(it && (it.saved || it.dirty));
      const btn = el("button", "rs-clip-row" + (state.selectedKey === key ? " cur" : ""));
      btn.type = "button";
      btn.dataset.clipKey = key;
      const dotCls = isOver ? (it.kind === "vrma" ? "vrma" : "override") : "";
      const main = el("span", "rs-cr-main");
      main.append(
        el("span", "rs-cr-key", key),
        el("span", "rs-cr-meta", isOver ? (it.kind === "vrma" ? ".vrma" : (it.duration_s || it.clip?.duration_s || 1.4) + "s") : SENTINEL_DEFAULTS[key].beat)
      );
      const end = el("span", "rs-cr-end");
      if (it?.dirty) end.appendChild(el("span", "rs-cr-dirty"));
      end.appendChild(el("span", "rs-cr-tag", isOver ? tr("js.companion.rig_studio.tag.override") : tr("js.companion.rig_studio.tag.builtin")));
      btn.append(el("span", "rs-cr-dot " + dotCls), main, end);
      btn.addEventListener("click", () => selectClip(key));
      frag.appendChild(btn);
    }

    const actions = customActionsList();
    const cLbl = el("div", "rs-sec-lbl spaced");
    cLbl.append(el("span", "", tr("js.companion.rig_studio.custom_actions")), el("small", "", String(actions.length)));
    frag.appendChild(cLbl);

    for (const it of actions) {
      const btn = el("button", "rs-clip-row" + (state.selectedKey === it.key ? " cur" : ""));
      btn.type = "button";
      btn.dataset.clipKey = it.key;
      const dotCls = !it.saved ? "draft" : (it.kind === "vrma" ? "vrma" : "clip");
      const main = el("span", "rs-cr-main");
      main.append(
        el("span", "rs-cr-key", it.key),
        el("span", "rs-cr-meta", it.description || (it.kind === "vrma" ? ".vrma" : (it.clip?.duration_s || it.duration_s || 1.4) + "s"))
      );
      const end = el("span", "rs-cr-end");
      if (it.dirty) end.appendChild(el("span", "rs-cr-dirty"));
      end.appendChild(el("span", "rs-cr-tag ex" + (it.expose_to_agent ? "" : " off"),
        it.expose_to_agent ? tr("js.companion.rig_studio.tag.exposed") : tr("js.companion.rig_studio.tag.private")));
      btn.append(el("span", "rs-cr-dot " + dotCls), main, end);
      btn.addEventListener("click", () => selectClip(it.key));
      frag.appendChild(btn);
    }

    const addBtn = el("button", "rs-add-row", "+ " + tr("js.companion.rig_studio.new_clip"));
    addBtn.type = "button";
    addBtn.id = "btnNewClip";
    addBtn.addEventListener("click", createNewActionClip);
    frag.appendChild(addBtn);

    ui.railScroll.replaceChildren(frag);
    const exposedN = actions.filter(a => a.expose_to_agent).length;
    ui.railFootL.textContent = tr("js.companion.rig_studio.rail_foot_clips", actions.length + sentOverrides);
    ui.railFootR.textContent = tr("js.companion.rig_studio.rail_foot_exposed", exposedN);
  }

  function createNewActionClip() {
    let idx = customActionsList().length + 1;
    let key = "custom_action_" + idx;
    while (state.items.has(key) || SENTINEL_KEYS.includes(key)) key = "custom_action_" + (++idx);
    const clip = makeDefaultClip(key);
    const item = {
      key,
      isSentinel: false,
      saved: false,
      kind: "clip",
      file: "clips/" + key + ".clip.json",
      description: "",
      expression: "",
      prop: "",
      expose_to_agent: false,
      loop: false,
      duration_s: clip.duration_s,
      rawEntry: null,
      clip,
      dirty: true
    };
    state.items.set(key, item);
    selectClip(key);
    showToast(tr("js.companion.rig_studio.toast.new_clip", key));
  }

  async function selectClip(key) {
    const gen = ++selectGen;
    state.selectedKey = key;
    state.selectedKfIdx = 0;
    state.playTime = 0;
    setPlaying(false);
    stage?.stopVrma();

    let item = state.items.get(key);
    if (!item && SENTINEL_KEYS.includes(key)) {
      item = {
        key,
        isSentinel: true,
        saved: false,
        kind: "builtin",
        file: "",
        loop: SENTINEL_DEFAULTS[key].loop,
        duration_s: 1.4,
        rawEntry: null,
        clip: null,
        dirty: false
      };
      state.items.set(key, item);
    }

    if (item && item.saved && item.kind === "clip" && !item.clip && !item.loading) {
      item.loading = true;
      try {
        const res = await C.fetchPackClip({ id: state.packInfo.id, rev: state.rev }, item.file);
        if (res?.rev) state.rev = res.rev;
        if (res?.json) item.clip = res.json;
      } catch (err) {
        if (gen === selectGen) showBanner(tr("js.companion.rig_studio.err.load_clip", item.key), { error: true });
      } finally {
        item.loading = false;
      }
      if (gen !== selectGen) return;
    } else if (item && item.saved && item.kind === "vrma") {
      try {
        if (!item.vrmaBuffer) {
          const res = await C.fetchPackClip({ id: state.packInfo.id, rev: state.rev }, item.file);
          if (res?.rev) state.rev = res.rev;
          item.vrmaBuffer = res?.buffer || null;
          if (gen !== selectGen) return;
        }
        if (item.vrmaBuffer && stage) {
          const info = await stage.loadVrmaBuffer(item.vrmaBuffer, !!item.loop);
          if (gen !== selectGen) return;
          if (info?.duration_s && !item.duration_s) item.duration_s = info.duration_s;
        }
      } catch (err) {
        if (gen === selectGen) showBanner(tr("js.companion.rig_studio.err.load_clip", item.key), { error: true });
      }
      if (gen !== selectGen) return;
    }

    if (gen !== selectGen) return;
    if (item?.prop && item.prop !== "none") applyProp(item.prop);
    else if (item?.clip?.prop?.id) applyProp(item.clip.prop.id);

    renderRail();
    renderTimeline();
    renderInspector();
    renderExpoStrip();
    syncTopBar();
    evaluateCurrentPose();
  }

  function setPlaying(flag) {
    const item = getSelectedItem();
    if (!item || (item.kind !== "clip" && item.kind !== "vrma") || (item.kind === "clip" && !item.clip)) flag = false;
    state.playing = !!flag;
    ui.btnPlay.textContent = state.playing ? "❚❚" : "▶";
    stage?.setPlaying(state.playing, item?.kind === "vrma" ? "vrma" : "clip");
  }

  function renderTimeline() {
    const item = getSelectedItem();
    const isEditableClip = !!(item && item.kind === "clip" && item.clip);
    const dur = isEditableClip ? (Number(item.clip.duration_s) || 1.4) : (Number(item?.duration_s) || 1.4);
    ui.btnPlay.disabled = !item || (item.kind !== "clip" && item.kind !== "vrma");
    ui.btnLoop.classList.toggle("on", !!item?.loop);
    ui.btnLoop.disabled = !item || item.kind === "builtin";
    ui.pDur.disabled = !isEditableClip;
    ui.pEase.disabled = !isEditableClip;
    ui.btnSnap.disabled = !isEditableClip || (item.clip.keyframes.length >= MAX_STUDIO_KEYFRAMES);
    ui.vSpeed.textContent = currentSpeed().toFixed(1) + "x";
    ui.vDur.textContent = dur.toFixed(1) + "s";
    ui.vEase.textContent = isEditableClip ? (item.clip.easing || "ease_in_out") : (item?.kind === "vrma" ? ".vrma" : "—");
    updateTimelineTimeReadout(dur);

    // Ruler ticks
    ui.ruler.replaceChildren();
    const steps = 8;
    for (let i = 0; i <= steps; i++) {
      const pct = (i / steps) * 100;
      const major = i % 2 === 0;
      const tick = el("span", "rs-tick" + (major ? " maj" : ""));
      tick.style.left = pct + "%";
      ui.ruler.appendChild(tick);
      if (major) {
        const lbl = el("span", "rs-tick-l", ((dur * i) / steps).toFixed(1) + "s");
        lbl.style.left = pct + "%";
        ui.ruler.appendChild(lbl);
      }
    }

    // Keyframe cards
    ui.lane.replaceChildren();
    if (isEditableClip) {
      item.clip.keyframes.forEach((kf, idx) => {
        const pct = Math.max(0, Math.min(100, (kf.t / Math.max(0.1, dur)) * 100));
        const card = el("button", "rs-kfc" + (idx === state.selectedKfIdx ? " sel" : ""));
        card.type = "button";
        card.style.left = pct + "%";
        card.dataset.kfIdx = String(idx);
        const kt = el("span", "rs-kt");
        kt.append(el("b", "", kf.label || ("kf_" + (idx + 1))), el("span", "", kf.t.toFixed(2) + "s"));
        card.append(el("span", "rs-kfc-dot"), kt);
        if (item.clip.keyframes.length > 1) {
          const del = el("span", "rs-kx", "×");
          del.title = tr("js.companion.rig_studio.remove_kf");
          del.addEventListener("click", ev => {
            ev.stopPropagation();
            item.clip.keyframes.splice(idx, 1);
            state.selectedKfIdx = Math.max(0, Math.min(state.selectedKfIdx, item.clip.keyframes.length - 1));
            item.dirty = true;
            renderTimeline();
            renderInspector();
            syncTopBar();
            evaluateCurrentPose();
          });
          card.appendChild(del);
        }
        card.addEventListener("click", ev => {
          ev.stopPropagation();
          state.selectedKfIdx = idx;
          state.playTime = kf.t;
          setPlaying(false);
          renderTimeline();
          renderInspector();
          evaluateCurrentPose();
        });
        ui.lane.appendChild(card);
      });
    }
  }

  function updateTimelineTimeReadout(dur) {
    const d = Math.max(0.1, Number(dur) || 1.4);
    ui.tlTime.replaceChildren(el("b", "", state.playTime.toFixed(2)), document.createTextNode(" / " + d.toFixed(2) + "s"));
    const laneRect = ui.lane.getBoundingClientRect();
    const trackRect = ui.track.getBoundingClientRect();
    if (laneRect.width > 0 && trackRect.width > 0) {
      const leftPx = (laneRect.left - trackRect.left) + (Math.max(0, Math.min(1, state.playTime / d)) * laneRect.width);
      ui.playhead.style.left = leftPx.toFixed(1) + "px";
    } else {
      ui.playhead.style.left = "52px";
    }
  }

  function makeSlider(label, value, min, max, step, unit, onInput) {
    const row = el("div", "rs-sl-row");
    const lbl = el("label", "", label);
    const inp = document.createElement("input");
    inp.type = "range";
    inp.min = String(min);
    inp.max = String(max);
    inp.step = String(step);
    inp.value = String(value);
    const out = el("output", "", formatVal(value, step, unit));
    inp.addEventListener("input", () => {
      const v = Number(inp.value);
      out.textContent = formatVal(v, step, unit);
      onInput(v);
    });
    row.append(lbl, inp, out);
    return row;
  }

  function formatVal(v, step, unit) {
    const n = Number(v) || 0;
    const dec = step < 0.05 ? 2 : (step < 1 ? 1 : 0);
    return n.toFixed(dec) + (unit || "");
  }

  function ensureKf(item) {
    if (!item?.clip?.keyframes?.length) return null;
    const kf = item.clip.keyframes[state.selectedKfIdx] || item.clip.keyframes[0];
    if (!kf.head) kf.head = { yaw_deg: 0, pitch_deg: 0, roll_deg: 0, lean_deg: 0, turn_deg: 0 };
    if (!kf.hands) kf.hands = {};
    if (!kf.hands.left) kf.hands.left = { ik: [0.38, -0.42, 0.28], pole: [0.3, -0.4, -0.85], fingers: { preset: "relaxed" } };
    if (!kf.hands.right) kf.hands.right = { ik: [-0.38, -0.42, 0.28], pole: [-0.3, -0.4, -0.85], fingers: { preset: "relaxed" } };
    if (!kf.expression) kf.expression = {};
    return kf;
  }

  function renderInspectorHeader(item) {
    if (state.holdHandle) { state.holdHandle.destroy(); state.holdHandle = null; }
    ui.inspHead.replaceChildren();
    if (!item) return;
    ui.inspHead.appendChild(el("span", "rs-ck", item.key));
    const kindLbl = item.isSentinel
      ? (item.kind === "builtin" ? tr("js.companion.rig_studio.badge.builtin") : tr("js.companion.rig_studio.badge.sentinel_override"))
      : (item.kind === "vrma" ? ".vrma" : ".clip.json");
    ui.inspHead.appendChild(el("span", "rs-badge " + (item.kind === "vrma" ? "info" : "ok"), kindLbl));
    ui.inspHead.appendChild(el("span", "rs-grow"));

    if (item.saved) {
      const delBtn = el("button", "rs-hold-btn");
      delBtn.type = "button";
      delBtn.id = "btnHoldDelete";
      delBtn.appendChild(el("span", "", tr("js.companion.rig_studio.hold_delete")));
      ui.inspHead.appendChild(delBtn);
      if (window.WsHoldConfirm) {
        state.holdHandle = window.WsHoldConfirm.attach(delBtn, {
          duration: 1100,
          onConfirm: () => deleteClipFromPack(item)
        });
      }
    } else if (!item.isSentinel || item.dirty) {
      const disc = el("button", "rs-linkbtn", tr("js.companion.rig_studio.discard"));
      disc.type = "button";
      disc.addEventListener("click", () => {
        if (item.isSentinel) {
          item.kind = "builtin"; item.clip = null; item.dirty = false;
        } else {
          state.items.delete(item.key);
          state.selectedKey = "idle";
        }
        renderRail(); renderTimeline(); renderInspector(); renderExpoStrip(); syncTopBar(); evaluateCurrentPose();
      });
      ui.inspHead.appendChild(disc);
    }
  }

  function renderInspector() {
    const item = getSelectedItem();
    renderInspectorHeader(item);
    ui.inspBody.replaceChildren();
    if (!item) return;

    if (state.inspectorTab === "agent") {
      renderAgentTab(item);
      return;
    }

    // Rig tab
    if (item.isSentinel && item.kind === "builtin") {
      const sec = el("div", "rs-insp-sec");
      sec.append(
        el("p", "rs-note rs-mb-8", tr("js.companion.rig_studio.sentinel_builtin_desc", item.key)),
        (() => {
          const btn = el("button", "rs-tbtn primary", tr("js.companion.rig_studio.create_override"));
          btn.type = "button";
          btn.id = "btnCreateOverride";
          btn.addEventListener("click", () => {
            const clip = makeDefaultClip(item.key);
            item.kind = "clip";
            item.file = "clips/" + item.key + ".clip.json";
            item.clip = clip;
            item.loop = clip.loop;
            item.duration_s = clip.duration_s;
            item.dirty = true;
            renderRail(); renderTimeline(); renderInspector(); renderExpoStrip(); syncTopBar(); evaluateCurrentPose();
          });
          return btn;
        })()
      );
      ui.inspBody.appendChild(sec);
      appendPropCalibrationSection();
      appendWarningsSection();
      return;
    }

    if (item.kind === "vrma") {
      const sec = el("div", "rs-insp-sec");
      sec.appendChild(el("p", "rs-note", tr("js.companion.rig_studio.vrma_readonly_note")));
      ui.inspBody.appendChild(sec);
      appendPropCalibrationSection();
      appendWarningsSection();
      return;
    }

    const kf = ensureKf(item);
    if (!kf) return;

    // 1. Keyframe label & easing
    {
      const sec = el("div", "rs-insp-sec");
      const sh = el("div", "rs-sh");
      sh.append(el("span", "", tr("js.companion.rig_studio.sec.keyframe")), el("span", "rs-mono", kf.t.toFixed(2) + "s"));
      const lblInp = el("input", "rs-inp rs-mb-7");
      lblInp.type = "text";
      lblInp.maxLength = 32;
      lblInp.value = kf.label || "";
      lblInp.placeholder = "label";
      lblInp.addEventListener("input", () => {
        kf.label = lblInp.value;
        item.dirty = true;
        renderTimeline(); syncTopBar();
      });
      const chips = el("div", "rs-chips");
      for (const ez of EASING_OPTIONS) {
        const c = el("button", "rs-chip" + ((kf.easing || "ease_in_out") === ez ? " on" : ""), ez);
        c.type = "button";
        c.addEventListener("click", () => {
          kf.easing = ez;
          item.dirty = true;
          renderInspector(); syncTopBar(); evaluateCurrentPose();
        });
        chips.appendChild(c);
      }
      sec.append(sh, lblInp, chips);
      ui.inspBody.appendChild(sec);
    }

    // 2. Head & Torso
    {
      const sec = el("div", "rs-insp-sec");
      sec.appendChild(el("div", "rs-sh", tr("js.companion.rig_studio.sec.head")));
      for (const [k, lbl, lo, hi] of [
        ["yaw_deg", "Yaw", -45, 45], ["pitch_deg", "Pitch", -30, 30], ["roll_deg", "Roll", -25, 25],
        ["lean_deg", "Lean", -20, 20], ["turn_deg", "Turn", -30, 30]
      ]) {
        sec.appendChild(makeSlider(lbl, kf.head[k] || 0, lo, hi, 1, "°", v => {
          kf.head[k] = v; item.dirty = true; syncTopBar(); evaluateCurrentPose();
        }));
      }
      ui.inspBody.appendChild(sec);
    }

    // 3. Hand IK & Elbow Pole
    {
      const sec = el("div", "rs-insp-sec");
      const sh = el("div", "rs-sh");
      sh.appendChild(el("span", "", tr("js.companion.rig_studio.sec.hand_ik")));
      const lr = el("span", "rs-lr");
      for (const side of ["left", "right"]) {
        const b = el("button", state.handSide === side ? "on" : "", side === "left" ? "L" : "R");
        b.type = "button";
        b.addEventListener("click", () => {
          state.handSide = side;
          stage?.setSide(side);
          renderInspector();
        });
        lr.appendChild(b);
      }
      sh.appendChild(lr);
      sec.appendChild(sh);
      const hand = kf.hands[state.handSide];
      if (!Array.isArray(hand.ik)) hand.ik = [state.handSide === "left" ? 0.38 : -0.38, -0.42, 0.28];
      if (!Array.isArray(hand.pole)) hand.pole = [state.handSide === "left" ? 0.3 : -0.3, -0.4, -0.85];
      ["X", "Y", "Z"].forEach((ax, i) => {
        sec.appendChild(makeSlider("IK " + ax, hand.ik[i], -1.2, 1.2, 0.01, "", v => {
          hand.ik[i] = v; item.dirty = true; syncTopBar(); evaluateCurrentPose();
        }));
      });
      ["X", "Y", "Z"].forEach((ax, i) => {
        sec.appendChild(makeSlider("Pole " + ax, hand.pole[i], -1, 1, 0.05, "", v => {
          hand.pole[i] = v; item.dirty = true; syncTopBar(); evaluateCurrentPose();
        }));
      });
      ui.inspBody.appendChild(sec);
    }

    // 4. Target Attach (D7)
    {
      const sec = el("div", "rs-insp-sec");
      sec.appendChild(el("div", "rs-sh", tr("js.companion.rig_studio.sec.target_attach")));
      for (const slot of ["gaze", "head", "hand_l", "hand_r"]) {
        const spec = kf.target_attach?.[slot] || { space: "free", weight: 1, offset: [0, 0, 0] };
        const card = el("div", "rs-ta-card");
        card.dataset.attachSlot = slot;
        const head = el("div", "rs-ta-head");
        head.appendChild(el("span", "rs-ta-slot rs-mono", slot));
        const sel = el("select", "rs-sel");
        sel.dataset.attachSelect = slot;
        for (const opt of formatAttachSpaceOptions(spec.space)) {
          const o = document.createElement("option");
          o.value = opt.value;
          o.textContent = opt.label;
          if (opt.value === (spec.space || "free")) o.selected = true;
          sel.appendChild(o);
        }
        sel.addEventListener("change", () => {
          if (!kf.target_attach) kf.target_attach = {};
          if (sel.value === "free") {
            delete kf.target_attach[slot];
            if (!Object.keys(kf.target_attach).length) delete kf.target_attach;
          } else {
            kf.target_attach[slot] = {
              space: sel.value,
              weight: Number.isFinite(spec.weight) ? spec.weight : 1,
              offset: Array.isArray(spec.offset) ? spec.offset.slice(0, 3) : [0, 0, 0]
            };
            if (sel.value.startsWith("prop:smartphone")) applyProp("smartphone");
            else if (sel.value.startsWith("prop:laptop")) applyProp("laptop");
          }
          item.dirty = true;
          renderInspector(); syncTopBar(); evaluateCurrentPose();
        });
        head.appendChild(sel);
        card.appendChild(head);

        if (spec.space && spec.space !== "free") {
          const body = el("div", "rs-ta-body");
          const wVal = Number.isFinite(spec.weight) ? spec.weight : 1;
          const off = Array.isArray(spec.offset) ? spec.offset : [0, 0, 0];
          body.appendChild(makeSlider("Weight", wVal, 0, 1, 0.05, "", v => {
            kf.target_attach[slot].weight = v; item.dirty = true; syncTopBar(); evaluateCurrentPose();
          }));
          ["Off X", "Off Y", "Off Z"].forEach((lbl, i) => {
            body.appendChild(makeSlider(lbl, off[i] || 0, -0.5, 0.5, 0.01, "m", v => {
              if (!Array.isArray(kf.target_attach[slot].offset)) kf.target_attach[slot].offset = [0, 0, 0];
              kf.target_attach[slot].offset[i] = v;
              item.dirty = true; syncTopBar(); evaluateCurrentPose();
            }));
          });
          card.appendChild(body);
        }
        sec.appendChild(card);
      }
      ui.inspBody.appendChild(sec);
    }

    // 5. 30-bone Fingers (reusing FINGER_PRESETS)
    {
      const sec = el("div", "rs-insp-sec");
      const hand = kf.hands[state.handSide];
      if (!hand.fingers) hand.fingers = { preset: "relaxed" };
      const resolved = C.vrmIk.resolveFingerSpec(hand.fingers);
      sec.appendChild(el("div", "rs-sh", tr("js.companion.rig_studio.sec.fingers") + " (" + state.handSide.toUpperCase() + ")"));
      const chips = el("div", "rs-chips rs-mb-7");
      for (const pn of FINGER_PRESET_NAMES) {
        const c = el("button", "rs-chip" + (hand.fingers.preset === pn && !hand.fingers.curl ? " on" : ""), pn);
        c.type = "button";
        c.addEventListener("click", () => {
          hand.fingers = { preset: pn };
          item.dirty = true;
          renderInspector(); syncTopBar(); evaluateCurrentPose();
        });
        chips.appendChild(c);
      }
      sec.appendChild(chips);
      sec.appendChild(makeSlider("ThumbOpp", resolved.thumb_opp, -0.5, 1.5, 0.05, "", v => {
        hand.fingers.thumb_opp = v; item.dirty = true; syncTopBar(); evaluateCurrentPose();
      }));
      sec.appendChild(makeSlider("Spread", resolved.spread, -0.3, 0.5, 0.02, "", v => {
        hand.fingers.spread = v; item.dirty = true; syncTopBar(); evaluateCurrentPose();
      }));
      FINGER_DIGIT_LABELS.forEach((digit, dIdx) => {
        const avg = (resolved.curl[dIdx * 3] + resolved.curl[dIdx * 3 + 1] + resolved.curl[dIdx * 3 + 2]) / 3;
        sec.appendChild(makeSlider(digit, avg, -0.3, 1.6, 0.05, "", v => {
          const nextCurl = (Array.isArray(hand.fingers.curl) && hand.fingers.curl.length === 15)
            ? hand.fingers.curl.slice()
            : resolved.curl.slice();
          nextCurl[dIdx * 3] = Number(v.toFixed(2));
          nextCurl[dIdx * 3 + 1] = Number(Math.min(1.8, v * 1.05).toFixed(2));
          nextCurl[dIdx * 3 + 2] = Number(Math.min(1.8, v * 0.65).toFixed(2));
          hand.fingers.curl = nextCurl;
          item.dirty = true; syncTopBar(); evaluateCurrentPose();
        }));
      });
      ui.inspBody.appendChild(sec);
    }

    // 6. Expressions
    {
      const sec = el("div", "rs-insp-sec");
      sec.appendChild(el("div", "rs-sh", tr("js.companion.rig_studio.sec.expressions")));
      const chips = el("div", "rs-chips rs-mb-7");
      for (const ex of EXPRESSION_CHIPS) {
        const active = (kf.expression[ex] || 0) > 0.001;
        const c = el("button", "rs-chip" + (active ? " on" : ""), ex);
        c.type = "button";
        c.addEventListener("click", () => {
          if (active) delete kf.expression[ex];
          else if (Object.keys(kf.expression).length < 8) kf.expression[ex] = 0.8;
          item.dirty = true;
          renderInspector(); syncTopBar(); evaluateCurrentPose();
        });
        chips.appendChild(c);
      }
      sec.appendChild(chips);
      for (const [k, v] of Object.entries(kf.expression)) {
        sec.appendChild(makeSlider(k, v, 0, 1, 0.05, "", nv => {
          if (nv <= 0.001) delete kf.expression[k];
          else kf.expression[k] = nv;
          item.dirty = true; syncTopBar(); evaluateCurrentPose();
        }));
      }
      ui.inspBody.appendChild(sec);
    }

    appendPropCalibrationSection();
    appendWarningsSection();
  }

  function appendPropCalibrationSection() {
    if (state.propId === "none") return;
    const sec = el("div", "rs-insp-sec");
    sec.appendChild(el("div", "rs-sh", tr("js.companion.rig_studio.sec.prop_cal") + " (" + state.propId + ")"));
    if (!state.calibration.props) state.calibration.props = {};
    if (!state.calibration.props[state.propId]) {
      state.calibration.props[state.propId] = { offset: [0, 0, 0], rotation_deg: [0, 0, 0], scale: 1 };
    }
    const cal = state.calibration.props[state.propId];
    if (!Array.isArray(cal.offset)) cal.offset = [0, 0, 0];
    if (!Array.isArray(cal.rotation_deg)) cal.rotation_deg = [0, 0, 0];
    const onCal = () => {
      state.calibrationDirty = true;
      stage?.setCalibration(state.calibration);
      syncTopBar();
      evaluateCurrentPose();
    };
    ["Off X", "Off Y", "Off Z"].forEach((lbl, i) => {
      sec.appendChild(makeSlider(lbl, cal.offset[i] || 0, -0.5, 0.5, 0.01, "m", v => { cal.offset[i] = v; onCal(); }));
    });
    ["Rot X", "Rot Y", "Rot Z"].forEach((lbl, i) => {
      sec.appendChild(makeSlider(lbl, cal.rotation_deg[i] || 0, -180, 180, 1, "°", v => { cal.rotation_deg[i] = v; onCal(); }));
    });
    sec.appendChild(makeSlider("Scale", Number.isFinite(cal.scale) ? cal.scale : 1, 0.25, 4, 0.05, "x", v => { cal.scale = v; onCal(); }));
    ui.inspBody.appendChild(sec);
  }

  function appendWarningsSection() {
    const warns = state.packInfo?.clipWarnings;
    if (!Array.isArray(warns) || !warns.length) return;
    const sec = el("div", "rs-insp-sec");
    sec.appendChild(el("div", "rs-sh", tr("js.companion.rig_studio.sec.warnings")));
    const ul = el("ul", "rs-warn-list");
    for (const w of warns) ul.appendChild(el("li", "", String(w)));
    sec.appendChild(ul);
    ui.inspBody.appendChild(sec);
  }

  function renderAgentTab(item) {
    if (item.isSentinel) {
      const sec = el("div", "rs-insp-sec");
      sec.appendChild(el("p", "rs-note", tr("js.companion.rig_studio.sentinel_agent_note")));
      ui.inspBody.appendChild(sec);
    } else {
      const sec = el("div", "rs-insp-sec");
      // Expose toggle
      const swt = el("label", "rs-swt" + (item.expose_to_agent ? " on" : ""));
      const chk = document.createElement("input");
      chk.type = "checkbox";
      chk.className = "rs-sr-only";
      chk.id = "agentExposeChk";
      chk.checked = !!item.expose_to_agent;
      chk.addEventListener("change", () => {
        item.expose_to_agent = chk.checked;
        item.dirty = true;
        renderRail(); renderInspector(); renderExpoStrip(); syncTopBar();
      });
      swt.append(chk, el("span", "rs-trk"), el("span", "", tr("js.companion.rig_studio.expose_to_agent")));
      sec.appendChild(swt);

      // Editable action key for unsaved drafts
      if (!item.saved) {
        const kf = el("div", "rs-field rs-mt-6");
        kf.appendChild(el("div", "rs-fl", tr("js.companion.rig_studio.action_key")));
        const kinp = el("input", "rs-inp");
        kinp.id = "agentKeyInp";
        kinp.value = item.key;
        kinp.maxLength = 32;
        const kerr = el("div", "rs-field-err");
        kerr.hidden = true;
        kinp.addEventListener("change", () => {
          const next = kinp.value.trim();
          const check = validateActionKey(next);
          if (!check.ok || SENTINEL_KEYS.includes(next) || (next !== item.key && state.items.has(next))) {
            kerr.textContent = tr("js.companion.rig_studio.err.bad_key");
            kerr.hidden = false;
            return;
          }
          kerr.hidden = true;
          state.items.delete(item.key);
          item.key = next;
          item.file = "clips/" + next + ".clip.json";
          if (item.clip) item.clip.key = next;
          state.items.set(next, item);
          state.selectedKey = next;
          renderRail(); renderInspector(); renderExpoStrip(); syncTopBar();
        });
        kf.append(kinp, kerr);
        sec.appendChild(kf);
      }

      // Description with live 120-rune counter
      const df = el("div", "rs-field rs-mt-6");
      const fl = el("div", "rs-fl");
      const vDesc = validateDescription(item.description || "");
      const cntEl = el("em", vDesc.ok ? "" : "over", vDesc.runes + " / " + MAX_DESC_RUNES);
      fl.append(el("span", "", tr("js.companion.rig_studio.description")), cntEl);
      const ta = el("textarea", "rs-ta");
      ta.id = "agentDescTa";
      ta.value = item.description || "";
      ta.placeholder = tr("js.companion.rig_studio.description_placeholder");
      const derr = el("div", "rs-field-err");
      derr.hidden = vDesc.ok;
      if (!vDesc.ok) derr.textContent = tr("js.companion.rig_studio.err.bad_desc");
      ta.addEventListener("input", () => {
        item.description = ta.value;
        item.dirty = true;
        const check = validateDescription(item.description);
        cntEl.textContent = check.runes + " / " + MAX_DESC_RUNES;
        cntEl.classList.toggle("over", !check.ok);
        derr.hidden = check.ok;
        if (!check.ok) derr.textContent = tr("js.companion.rig_studio.err.bad_desc");
        renderRail(); renderExpoStrip(); syncTopBar();
        updateCatalogPreviewBox(item.key);
      });
      df.append(fl, ta, derr);
      sec.appendChild(df);

      // Optional prop tag
      const pf = el("div", "rs-field");
      pf.appendChild(el("div", "rs-fl", tr("js.companion.rig_studio.prop_tag")));
      const pchips = el("div", "rs-chips");
      for (const pid of ["none", "smartphone", "laptop"]) {
        const cur = item.prop || "none";
        const c = el("button", "rs-chip" + (cur === pid ? " on" : ""), pid);
        c.type = "button";
        c.addEventListener("click", () => {
          item.prop = pid === "none" ? "" : pid;
          item.dirty = true;
          renderInspector(); syncTopBar();
        });
        pchips.appendChild(c);
      }
      pf.appendChild(pchips);
      sec.appendChild(pf);
      ui.inspBody.appendChild(sec);
    }

    // Live D5 <<<ACTIONS>>> prompt catalog preview
    const pSec = el("div", "rs-insp-sec");
    pSec.appendChild(el("div", "rs-sh", tr("js.companion.rig_studio.agent_preview_title")));
    const code = el("div", "rs-code");
    code.id = "agentPreviewCode";
    pSec.appendChild(code);
    ui.inspBody.appendChild(pSec);
    updateCatalogPreviewBox(item.key);
  }

  function updateCatalogPreviewBox(highlightKey) {
    const box = $("agentPreviewCode");
    if (!box) return;
    const prev = formatActionsPreview(customActionsList());
    const frag = document.createDocumentFragment();
    const mkLine = (cls, text) => el("span", "ln " + cls, text);
    frag.appendChild(mkLine("c", "<<<ACTIONS>>>"));
    if (!prev.lines.length) {
      frag.appendChild(mkLine("dim", "(none)"));
    } else {
      for (const ln of prev.lines) {
        const hl = ln.key && ln.key === highlightKey ? "hl" : (ln.meta ? "c" : "");
        frag.appendChild(mkLine(hl, ln.text));
      }
    }
    frag.appendChild(mkLine("c", "<<</ACTIONS>>>"));
    box.replaceChildren(frag);
  }

  function renderExpoStrip() {
    const item = getSelectedItem();
    ui.expoStrip.replaceChildren();
    if (!item) return;
    ui.expoStrip.classList.toggle("off", !item.isSentinel && !item.expose_to_agent);
    ui.expoStrip.classList.toggle("sent", !!item.isSentinel);
    const ico = el("span", "rs-es-ico", item.isSentinel ? "⚙" : (item.expose_to_agent ? "✓" : "○"));
    const t = el("span", "rs-es-t");
    if (item.isSentinel) {
      t.append(el("b", "", tr("js.companion.rig_studio.strip.sentinel_title")), el("span", "", tr("js.companion.rig_studio.strip.sentinel_sub")));
    } else if (item.expose_to_agent) {
      t.append(el("b", "", tr("js.companion.rig_studio.strip.exposed_title")), el("span", "", item.description || item.key));
    } else {
      t.append(el("b", "", tr("js.companion.rig_studio.strip.private_title")), el("span", "", tr("js.companion.rig_studio.strip.private_sub")));
    }
    ui.expoStrip.append(ico, t, el("span", "rs-es-go", "→"));
  }

  async function saveAllDirty() {
    if (state.saving || !state.packInfo) return;
    const dirty = dirtyItems();
    for (const it of dirty) {
      if (!it.isSentinel) {
        const kc = validateActionKey(it.key);
        if (!kc.ok) { showBanner(tr("js.companion.rig_studio.err.bad_key"), { error: true }); return; }
        const dc = validateDescription(it.description || "");
        if (!dc.ok) { showBanner(tr("js.companion.rig_studio.err.bad_desc"), { error: true }); return; }
      }
    }
    state.saving = true;
    syncTopBar();
    hideBanner();
    try {
      for (const it of dirty) {
        const body = buildPutPayload(it);
        const resp = await fetch("/api/companion/packs/" + encodeURIComponent(state.packInfo.id) + "/clips/" + encodeURIComponent(it.key), {
          method: "PUT",
          credentials: "same-origin",
          headers: { "Content-Type": "application/json", "If-Match": '"' + state.rev + '"' },
          body: JSON.stringify(body)
        });
        const data = await resp.json().catch(() => null);
        if (resp.status === 412 || resp.status === 409) {
          showBanner(data?.error || tr("js.companion.rig_studio.err.conflict"), { error: true, reload: true });
          return;
        }
        if (!resp.ok) {
          showBanner(data?.error || ("Save failed (" + resp.status + ")"), { error: true });
          return;
        }
        if (data?.pack?.rev) state.rev = data.pack.rev;
        if (data?.pack) state.packInfo = data.pack;
        it.saved = true;
        it.dirty = false;
        it.rawEntry = body.entry;
      }
      if (state.calibrationDirty) {
        const resp = await fetch("/api/companion/packs/" + encodeURIComponent(state.packInfo.id) + "/calibration", {
          method: "PUT",
          credentials: "same-origin",
          headers: { "Content-Type": "application/json", "If-Match": '"' + state.rev + '"' },
          body: JSON.stringify(state.calibration)
        });
        const data = await resp.json().catch(() => null);
        if (resp.status === 412 || resp.status === 409) {
          showBanner(data?.error || tr("js.companion.rig_studio.err.conflict"), { error: true, reload: true });
          return;
        }
        if (!resp.ok) {
          showBanner(data?.error || ("Calibration save failed (" + resp.status + ")"), { error: true });
          return;
        }
        if (data?.pack?.rev) state.rev = data.pack.rev;
        if (data?.pack) state.packInfo = data.pack;
        state.calibrationDirty = false;
      }
      notifyBroadcast(state.packInfo.id, state.rev);
      showToast(tr("js.companion.rig_studio.toast.saved"));
      renderRail(); renderInspector(); renderExpoStrip();
    } catch (err) {
      showBanner(err?.message || "Save failed", { error: true });
    } finally {
      state.saving = false;
      syncTopBar();
    }
  }

  async function deleteClipFromPack(item) {
    if (!state.packInfo || !item?.saved) return;
    hideBanner();
    try {
      const resp = await fetch("/api/companion/packs/" + encodeURIComponent(state.packInfo.id) + "/clips/" + encodeURIComponent(item.key), {
        method: "DELETE",
        credentials: "same-origin",
        headers: { "If-Match": '"' + state.rev + '"' }
      });
      const data = await resp.json().catch(() => null);
      if (resp.status === 412 || resp.status === 409) {
        showBanner(data?.error || tr("js.companion.rig_studio.err.conflict"), { error: true, reload: true });
        return;
      }
      if (!resp.ok) {
        showBanner(data?.error || ("Delete failed (" + resp.status + ")"), { error: true });
        return;
      }
      if (data?.pack?.rev) state.rev = data.pack.rev;
      if (data?.pack) state.packInfo = data.pack;
      if (item.isSentinel) {
        item.saved = false; item.dirty = false; item.kind = "builtin"; item.clip = null; item.vrmaBuffer = null;
      } else {
        state.items.delete(item.key);
        state.selectedKey = "idle";
      }
      notifyBroadcast(state.packInfo.id, state.rev);
      showToast(tr("js.companion.rig_studio.toast.deleted", item.key));
      await selectClip(state.selectedKey);
    } catch (err) {
      showBanner(err?.message || "Delete failed", { error: true });
    }
  }

  function syncImportModalState() {
    const key = ui.impKeyInp.value.trim();
    const isSent = SENTINEL_KEYS.includes(key);
    const existing = state.items.get(key);
    const existsSaved = !!(existing && existing.saved);
    ui.impSentinelRow.hidden = !isSent;
    ui.impSentinelSwt.classList.toggle("on", ui.impSentinelChk.checked);
    ui.impReplaceRow.hidden = !existsSaved;
    ui.impReplaceSwt.classList.toggle("on", ui.impReplaceChk.checked);
    const keyOk = validateActionKey(key).ok;
    const ok = !!state.pendingVrmaFile && keyOk && (!isSent || ui.impSentinelChk.checked) && (!existsSaved || ui.impReplaceChk.checked);
    ui.impSubmit.disabled = !ok;
  }

  async function submitVrmaImport() {
    if (!state.packInfo || !state.pendingVrmaFile) return;
    const key = ui.impKeyInp.value.trim();
    const qs = new URLSearchParams({ key });
    if (ui.impReplaceChk.checked) qs.set("replace", "1");
    if (SENTINEL_KEYS.includes(key) && ui.impSentinelChk.checked) qs.set("sentinel", "1");
    ui.impErr.hidden = true;
    ui.impSubmit.disabled = true;
    try {
      const resp = await fetch("/api/companion/packs/" + encodeURIComponent(state.packInfo.id) + "/clips/import-vrma?" + qs.toString(), {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "model/gltf-binary", "If-Match": '"' + state.rev + '"' },
        body: state.pendingVrmaFile
      });
      const data = await resp.json().catch(() => null);
      if (resp.status === 412 || resp.status === 409) {
        ui.impErr.textContent = data?.error || tr("js.companion.rig_studio.err.conflict");
        ui.impErr.hidden = false;
        if (resp.status === 412) showBanner(data?.error || tr("js.companion.rig_studio.err.conflict"), { error: true, reload: true });
        return;
      }
      if (!resp.ok) {
        ui.impErr.textContent = data?.error || ("Import failed (" + resp.status + ")");
        ui.impErr.hidden = false;
        return;
      }
      if (data?.pack?.rev) state.rev = data.pack.rev;
      if (data?.pack) state.packInfo = data.pack;
      ui.importScrim.hidden = true;
      state.pendingVrmaFile = null;
      notifyBroadcast(state.packInfo.id, state.rev);
      await reloadPackCatalogOnly(key, { preserveDirty: true, replaceKey: key });
      showToast(tr("js.companion.rig_studio.toast.imported", key));
    } catch (err) {
      ui.impErr.textContent = err?.message || "Import failed";
      ui.impErr.hidden = false;
    } finally {
      syncImportModalState();
    }
  }

  function populateItemsFromManifest(manifest, opts) {
    const res = mergeManifestIntoItems(state.items, manifest, {
      preserveDirty: !!opts?.preserveDirty,
      replaceKey: opts?.replaceKey || "",
      calibration: state.calibration,
      calibrationDirty: state.calibrationDirty
    });
    state.calibration = res.calibration;
    state.calibrationDirty = res.calibrationDirty;
  }

  async function reloadPackCatalogOnly(focusKey, opts) {
    const resp = await fetch("/api/companion/packs/" + encodeURIComponent(state.packInfo.id) + "/pack.json?rev=" + encodeURIComponent(state.rev), { credentials: "same-origin" });
    if (!resp.ok) throw new Error("Failed to load pack.json");
    state.manifest = await resp.json();
    populateItemsFromManifest(state.manifest, opts);
    stage?.setCalibration(state.calibration);
    await selectClip(focusKey || state.selectedKey || "idle");
  }

  async function loadPackAndBoot(requestedId) {
    hideBanner();
    ui.vpLoading.hidden = false;
    const catResp = await fetch("/api/companion/packs", { credentials: "same-origin", cache: "no-store" });
    if (!catResp.ok) {
      showErrorState(tr("js.companion.rig_studio.error_title"), tr("js.companion.rig_studio.err.load_packs"), []);
      return;
    }
    const cat = await catResp.json();
    state.packs = Array.isArray(cat?.packs) ? cat.packs : [];
    const authorable = state.packs.filter(p => isPackAuthorable(p).ok);

    let target = null;
    if (requestedId) {
      target = state.packs.find(p => p.id === requestedId) || null;
      const check = isPackAuthorable(target);
      if (!check.ok) {
        ui.packName.textContent = requestedId;
        ui.packDot.classList.add("warn");
        const PACK_REASON_KEYS = {
          not_found: "js.companion.rig_studio.err.pack_not_found",
          builtin: "js.companion.rig_studio.err.pack_builtin",
          not_vrm: "js.companion.rig_studio.err.pack_not_vrm",
          remote: "js.companion.rig_studio.err.pack_remote"
        };
        const reasonKey = PACK_REASON_KEYS[check.reason] || "js.companion.rig_studio.err.pack_not_found";
        showErrorState(tr("js.companion.rig_studio.error_title"), tr(reasonKey, requestedId), authorable);
        return;
      }
    } else {
      target = authorable[0] || null;
      if (!target) {
        ui.packName.textContent = "—";
        ui.packDot.classList.add("warn");
        showErrorState(tr("js.companion.rig_studio.error_title"), tr("js.companion.rig_studio.err.no_vrm_pack"), []);
        return;
      }
    }

    ui.errorSec.hidden = true;
    ui.work.hidden = false;
    state.packInfo = target;
    state.rev = target.rev || "";
    ui.packName.textContent = target.name || target.id;
    ui.packDot.classList.remove("warn");
    renderPackMenu(authorable);

    const mResp = await fetch("/api/companion/packs/" + encodeURIComponent(target.id) + "/pack.json?rev=" + encodeURIComponent(state.rev), { credentials: "same-origin" });
    if (!mResp.ok) {
      showErrorState(tr("js.companion.rig_studio.error_title"), tr("js.companion.rig_studio.err.load_packs"), authorable);
      return;
    }
    state.manifest = await mResp.json();
    populateItemsFromManifest(state.manifest);

    if (!stage) {
      stage = C.rigStudioStage.createStage({
        canvas: ui.canvas,
        overlaySvg: ui.overlay,
        viewportEl: ui.vp,
        getSpeed: () => currentSpeed(),
        onGizmoSelect: ({ mode, side }) => {
          state.mode = mode;
          if (side) state.handSide = side;
          applyMode(mode);
          if (state.inspectorTab === "rig") renderInspector();
        },
        onGizmoDrag: ev => {
          const item = getSelectedItem();
          if (ev.kind === "prop") {
            if (!state.calibration.props) state.calibration.props = {};
            if (!state.calibration.props[ev.propId]) state.calibration.props[ev.propId] = { offset: [0, 0, 0], rotation_deg: [0, 0, 0], scale: 1 };
            state.calibration.props[ev.propId].offset = ev.offset;
            state.calibrationDirty = true;
            stage.setCalibration(state.calibration);
            syncTopBar();
            evaluateCurrentPose();
            return;
          }
          if (!item || item.kind !== "clip" || !item.clip) return;
          const kf = ensureKf(item);
          if (!kf) return;
          if (ev.kind === "ik") kf.hands[ev.side].ik = ev.value;
          else if (ev.kind === "pole") kf.hands[ev.side].pole = ev.value;
          else if (ev.kind === "look") { kf.head.yaw_deg = ev.value.yaw_deg; kf.head.pitch_deg = ev.value.pitch_deg; }
          item.dirty = true;
          syncTopBar();
          evaluateCurrentPose();
          if (state.inspectorTab === "rig") renderInspector();
        },
        onPlayStep: (dtOrTime, vrmaFinished, kind) => {
          const item = getSelectedItem();
          if (kind === "vrma" || item?.kind === "vrma") {
            if (!item || item.kind !== "vrma") { setPlaying(false); return; }
            const dur = Math.max(0.1, Number(item.duration_s) || 1.4);
            state.playTime = Math.max(0, Math.min(dur, Number(dtOrTime) || 0));
            if (vrmaFinished) setPlaying(false);
            updateTimelineTimeReadout(dur);
            return;
          }
          if (!item || item.kind !== "clip" || !item.clip) { setPlaying(false); return; }
          const dur = Math.max(0.1, Number(item.clip.duration_s) || 1.4);
          state.playTime += dtOrTime * currentSpeed();
          if (state.playTime >= dur) {
            if (item.loop) state.playTime = state.playTime % dur;
            else { state.playTime = dur; setPlaying(false); }
          }
          updateTimelineTimeReadout(dur);
          evaluateCurrentPose();
        }
      });
    }

    const modelUrl = "/api/companion/packs/" + encodeURIComponent(target.id) + "/assets/" + encodeURIComponent(state.manifest.model) + "?rev=" + encodeURIComponent(state.rev);
    try {
      state.vrmMeta = await stage.loadVrm(modelUrl, state.manifest.expressionMap, state.calibration);
      ui.vpLoading.hidden = true;
      if (state.vrmMeta) {
        const verLabel = state.vrmMeta.isVrm0 ? "VRM 0.x" : "VRM 1.0";
        const armCm = Math.round(((state.vrmMeta.chains?.right?.chainLen || 0.48)) * 100);
        ui.kitChip.textContent = verLabel + " · arm " + armCm + "cm";
        ui.kitChip.hidden = false;
      }
    } catch (err) {
      ui.vpLoading.textContent = tr("js.companion.rig_studio.err.load_vrm");
      showBanner(err?.message || tr("js.companion.rig_studio.err.load_vrm"), { error: true });
    }

    const firstAction = customActionsList()[0];
    await selectClip(firstAction ? firstAction.key : "idle");
  }

  function wireEvents() {
    ui.packPill.addEventListener("click", () => {
      const open = !ui.packMenu.hidden;
      ui.packMenu.hidden = open;
      ui.packPill.setAttribute("aria-expanded", String(!open));
    });
    document.addEventListener("click", ev => {
      if (!ui.packMenu.hidden && !ui.packPill.contains(ev.target) && !ui.packMenu.contains(ev.target)) {
        ui.packMenu.hidden = true;
        ui.packPill.setAttribute("aria-expanded", "false");
      }
    });
    ui.bannerDismiss.addEventListener("click", hideBanner);
    ui.bannerReload.addEventListener("click", () => loadPackAndBoot(state.packInfo?.id || state.boot.packId));

    ui.segModes.addEventListener("click", ev => {
      const btn = ev.target.closest("button[data-mode]");
      if (btn) applyMode(btn.dataset.mode);
    });
    ui.segSlot.addEventListener("click", ev => {
      const btn = ev.target.closest("button[data-slot]");
      if (btn) applySlot(Number(btn.dataset.slot));
    });
    ui.segProp.addEventListener("click", ev => {
      const btn = ev.target.closest("button[data-prop]");
      if (btn) applyProp(btn.dataset.prop);
    });
    ui.segTheme.addEventListener("click", ev => {
      const btn = ev.target.closest("button[data-theme]");
      if (btn) applyTheme(btn.dataset.theme);
    });
    ui.btnCamReset.addEventListener("click", () => stage?.resetCamera());

    ui.btnPlay.addEventListener("click", () => {
      const item = getSelectedItem();
      if (!item) return;
      const dur = Number(item.clip?.duration_s || item.duration_s || 1.4);
      if (!state.playing && state.playTime >= dur - 0.01) {
        state.playTime = 0;
        if (item.kind === "vrma") stage?.seekVrma(0);
      }
      setPlaying(!state.playing);
    });
    ui.btnLoop.addEventListener("click", () => {
      const item = getSelectedItem();
      if (!item || item.kind === "builtin") return;
      item.loop = !item.loop;
      if (item.clip) item.clip.loop = item.loop;
      if (item.kind === "vrma") stage?.setVrmaLoop?.(item.loop);
      item.dirty = true;
      renderTimeline(); syncTopBar();
    });
    ui.pSpeed.addEventListener("click", () => {
      state.speedIdx = (state.speedIdx + 1) % SPEED_OPTIONS.length;
      ui.vSpeed.textContent = currentSpeed().toFixed(1) + "x";
    });
    ui.pDur.addEventListener("click", () => {
      const item = getSelectedItem();
      if (!item || item.kind !== "clip" || !item.clip) return;
      const cur = Number(item.clip.duration_s) || 1.4;
      const idx = DURATION_PRESETS.findIndex(d => Math.abs(d - cur) < 0.05);
      const next = DURATION_PRESETS[(idx + 1) % DURATION_PRESETS.length];
      rescaleClipDuration(item.clip, next);
      item.duration_s = next;
      state.playTime = Math.min(state.playTime, next);
      item.dirty = true;
      renderTimeline(); syncTopBar(); evaluateCurrentPose();
    });
    ui.pEase.addEventListener("click", () => {
      const item = getSelectedItem();
      if (!item || item.kind !== "clip" || !item.clip) return;
      const idx = EASING_OPTIONS.indexOf(item.clip.easing || "ease_in_out");
      item.clip.easing = EASING_OPTIONS[(idx + 1) % EASING_OPTIONS.length];
      item.dirty = true;
      renderTimeline(); syncTopBar(); evaluateCurrentPose();
    });
    ui.btnSnap.addEventListener("click", () => {
      const item = getSelectedItem();
      if (!item || item.kind !== "clip" || !item.clip) return;
      const sample = C.vrmClip.sampleClip(item.clip, state.playTime, { loop: !!item.loop });
      const res = addKeyframeSnapshot(item.clip, state.playTime, sample);
      if (!res.ok) { showToast(tr("js.companion.rig_studio.err.max_kf")); return; }
      state.selectedKfIdx = res.index;
      item.dirty = true;
      renderTimeline(); renderInspector(); syncTopBar(); evaluateCurrentPose();
    });

    // Scrubber pointer drag on track
    let scrubbing = false;
    const scrubAt = clientX => {
      const item = getSelectedItem();
      if (!item || (item.kind !== "clip" && item.kind !== "vrma")) return;
      const rect = ui.lane.getBoundingClientRect();
      if (!rect.width) return;
      const dur = Number(item.clip?.duration_s || item.duration_s || 1.4);
      const u = Math.max(0, Math.min(1, (clientX - rect.left) / rect.width));
      state.playTime = Number((u * dur).toFixed(2));
      updateTimelineTimeReadout(dur);
      if (item.kind === "vrma") stage?.seekVrma(state.playTime);
      else evaluateCurrentPose();
    };
    ui.track.addEventListener("pointerdown", ev => {
      if (ev.target.closest(".rs-kfc")) return;
      scrubbing = true;
      setPlaying(false);
      scrubAt(ev.clientX);
    });
    window.addEventListener("pointermove", ev => { if (scrubbing) scrubAt(ev.clientX); });
    const stopScrub = () => { scrubbing = false; };
    window.addEventListener("pointerup", stopScrub);
    window.addEventListener("pointercancel", stopScrub);

    for (const tabBtn of document.querySelectorAll(".rs-tab")) {
      tabBtn.addEventListener("click", () => {
        state.inspectorTab = tabBtn.dataset.tab === "agent" ? "agent" : "rig";
        for (const b of document.querySelectorAll(".rs-tab")) {
          const on = b.dataset.tab === state.inspectorTab;
          b.classList.toggle("on", on);
          b.setAttribute("aria-selected", String(on));
        }
        renderInspector();
      });
    }
    ui.expoStrip.addEventListener("click", () => {
      const agentTab = document.querySelector('.rs-tab[data-tab="agent"]');
      agentTab?.click();
    });

    ui.btnSave.addEventListener("click", saveAllDirty);

    // Export modal
    ui.btnExport.addEventListener("click", () => {
      const item = getSelectedItem();
      if (!item || item.kind !== "clip" || !item.clip) return;
      ui.mdTitle.textContent = item.key + ".clip.json";
      ui.mdPre.textContent = JSON.stringify(item.clip, null, 2);
      ui.exportScrim.hidden = false;
    });
    ui.mdClose.addEventListener("click", () => { ui.exportScrim.hidden = true; });
    ui.mdCopy.addEventListener("click", () => {
      navigator.clipboard?.writeText(ui.mdPre.textContent || "").then(() => showToast(tr("js.companion.rig_studio.toast.copied"))).catch(() => {});
    });
    ui.mdDownload.addEventListener("click", () => {
      const item = getSelectedItem();
      if (!item?.clip) return;
      const blob = new Blob([JSON.stringify(item.clip, null, 2) + "\n"], { type: "application/json" });
      const a = document.createElement("a");
      a.href = URL.createObjectURL(blob);
      a.download = item.key + ".clip.json";
      a.click();
      setTimeout(() => URL.revokeObjectURL(a.href), 1000);
    });

    // Import .vrma modal
    ui.btnImport.addEventListener("click", () => {
      state.pendingVrmaFile = null;
      ui.impFileName.textContent = "";
      ui.impKeyInp.value = "";
      ui.impSentinelChk.checked = false;
      ui.impReplaceChk.checked = false;
      ui.impErr.hidden = true;
      syncImportModalState();
      ui.importScrim.hidden = false;
    });
    ui.impPickBtn.addEventListener("click", () => ui.vrmaFileInput.click());
    ui.vrmaFileInput.addEventListener("change", () => {
      const f = ui.vrmaFileInput.files?.[0];
      ui.vrmaFileInput.value = "";
      if (!f) return;
      state.pendingVrmaFile = f;
      ui.impFileName.textContent = f.name;
      if (!ui.impKeyInp.value.trim()) ui.impKeyInp.value = sanitizeKeyFromFilename(f.name);
      syncImportModalState();
    });
    ui.impKeyInp.addEventListener("input", syncImportModalState);
    ui.impSentinelChk.addEventListener("change", syncImportModalState);
    ui.impReplaceChk.addEventListener("change", syncImportModalState);
    ui.impCancel.addEventListener("click", () => { ui.importScrim.hidden = true; });
    ui.impSubmit.addEventListener("click", submitVrmaImport);
  }

  // Automation / QA handle
  window.WsRigStudio = {
    getState() {
      return {
        packId: state.packInfo?.id || "",
        rev: state.rev,
        selectedKey: state.selectedKey,
        dirtyCount: dirtyItems().length + (state.calibrationDirty ? 1 : 0),
        itemKeys: Array.from(state.items.keys()),
        theme: state.theme,
        slotPx: state.slotPx,
        propId: state.propId,
        mode: state.mode,
        stage: stage?.getDebugState?.() || null,
        errorVisible: !ui.errorSec.hidden
      };
    },
    selectClip,
    applyTheme,
    applySlot,
    applyProp,
    applyMode,
    saveAllDirty
  };

  wireEvents();
  applyTheme(state.theme);
  applySlot(state.slotPx);
  applyMode("ik");
  loadPackAndBoot(state.boot.packId || "");
})(window.WsCompanion);
