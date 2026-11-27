/* UI Jank Flight Recorder: passive >=400ms LoAF/longtask & Layer C span ring buffer. */
var WsJank = (function () {
  "use strict";

  var STALL_THRESHOLD_MS = 400;
  var MINOR_LOAF_MIN_MS = 100;
  var CRUMB_CAP = 128;
  var STALL_CAP = 10;
  var STALL_CRUMB_TAIL = 25;
  var TOP_SCRIPTS_CAP = 5;
  var RE_HEX32 = /[0-9a-fA-F]{32,}/g;
  var RE_BEARER = /Bearer\s+[A-Za-z0-9._~+/=-]+/gi;
  var RE_WIN_HOME = /(?:[A-Za-z]:)?[\\/](?:Users|users)[\\/][^\\/\s"'`?#]+/g;
  var RE_NIX_HOME = /\/(?:home|Users)\/[^/\s"'`?#]+/g;
  var RE_BUNDLE_ASSET = /^dashboard-[0-9a-f]{8,28}\.(?:js|css)$/;

  var frameInstanceId = Math.random().toString(36).slice(2, 8);
  var stallSeq = 0;

  // Fixed-size circular buffer for breadcrumbs (zero shifting or DOM reads on hot paths).
  var crumbRing = new Array(CRUMB_CAP);
  var crumbHead = 0;
  var crumbCount = 0;

  // Chronological [oldest .. newest] capped FIFO buffer (window.top or standalone holds 10).
  var stalls = [];
  var localWindowRefs = []; // Parallel local monotonic windows { id, startPerf, endPerf } on active page.

  // Zero-allocation running summary counters for 100-399ms LoAFs (never evicts stalls[]).
  var minorCount = 0;
  var minorMaxMs = 0;
  var minorTopInvoker = "";
  var minorTopInvokerLoafMs = 0;

  var started = false;
  var observerMode = "none"; // "loaf" | "longtask" | "none"
  var observer = null;
  var relayFn = null;
  var minorRelayFn = null;
  var listeners = [];

  var modalEls = null;
  var modalOpen = false;
  var selectedId = "";
  var activeOpts = null;
  var prevFocus = null;

  function nowPerf() {
    if (typeof performance !== "undefined" && typeof performance.now === "function") {
      return performance.now();
    }
    return Date.now();
  }

  function round1(v) {
    return typeof v === "number" && isFinite(v) ? Math.round(v * 10) / 10 : 0;
  }

  function roundMs(v) {
    return typeof v === "number" && isFinite(v) && v >= 0 ? Math.round(v) : 0;
  }

  function clampStr(v, max) {
    if (typeof v !== "string") return "";
    return v.length > max ? v.slice(0, max) : v;
  }

  function t(key, fallback) {
    var args = [];
    for (var i = 2; i < arguments.length; i++) args.push(arguments[i]);
    if (typeof window !== "undefined" && typeof window.wsT === "function") {
      var res = window.wsT.apply(null, [key].concat(args));
      if (res && res !== key) return res;
    }
    if (!fallback) return key;
    return fallback.replace(/\{(\d+)\}/g, function (_, idx) {
      var n = Number(idx);
      return n >= 0 && n < args.length && args[n] !== undefined && args[n] !== null ? String(args[n]) : "";
    });
  }

  function frameKind() {
    if (typeof document !== "undefined" && document.documentElement) {
      var mode = document.documentElement.getAttribute("data-mode");
      if (mode === "shell" || mode === "session" || mode === "worktree" || mode === "index" || mode === "settings") {
        return mode;
      }
    }
    if (typeof location !== "undefined" && typeof location.pathname === "string") {
      var p = location.pathname;
      if (p === "/") return "index";
      if (p === "/shell" || p.indexOf("/shell/") === 0) return "shell";
      if (p === "/settings") return "settings";
      if (p.indexOf("/w/") === 0) return "worktree";
      if (p.indexOf("/s/") === 0) return "session";
    }
    return "unknown";
  }

  function scrubString(raw) {
    if (typeof raw !== "string" || !raw) return "";
    var s = raw
      .replace(RE_BEARER, "Bearer <redacted>")
      .replace(RE_WIN_HOME, "<home>")
      .replace(RE_NIX_HOME, "<home>")
      .replace(RE_HEX32, "<redacted>");
    return clampStr(s, 160);
  }

  function scrubUrlPath(rawUrl) {
    if (typeof rawUrl !== "string" || !rawUrl) return "";
    var noQuery = rawUrl.replace(/[?#].*$/, "");
    var pathOnly = noQuery.replace(/^[a-zA-Z][a-zA-Z0-9+.-]*:\/\/[^/]*/, "");
    // Redact live session/worktree slugs while keeping hashed dashboard-<24hex>.js|css names.
    pathOnly = pathOnly.replace(/^\/s\/([^/]+)(?=\/|$)/, function (_, seg) {
      return RE_BUNDLE_ASSET.test(seg) ? "/s/" + seg : "/s/<session>";
    });
    pathOnly = pathOnly.replace(/^\/w\/[^/]+(?=\/|$)/, "/w/<worktree>");
    return scrubString(pathOnly);
  }

  function basename(path) {
    if (!path) return "";
    var i = path.lastIndexOf("/");
    return i >= 0 ? path.slice(i + 1) : path;
  }

  function getSourceMap() {
    if (typeof window !== "undefined" && window.wsSourceMap && typeof window.wsSourceMap === "object") {
      return window.wsSourceMap;
    }
    if (typeof globalThis !== "undefined" && globalThis.wsSourceMap && typeof globalThis.wsSourceMap === "object") {
      return globalThis.wsSourceMap;
    }
    return null;
  }

  function isBundlePageUrl(url, map) {
    if (!url) return true;
    var clean = url.replace(/[?#].*$/, "");
    var base = basename(clean);
    if (map && map.scriptFile && base === map.scriptFile) return true;
    if (/\.js$/i.test(base)) return false;
    // Inline script on shell / session / worktree / index / settings document URL.
    return /^https?:\/\/[^/]+\/(?:$|shell(?:\/|$)|s\/[^/]+$|w\/[^/]+(?:\/.*)?$|settings(?:\/|$))/i.test(clean) ||
      clean === "/" || clean === "/shell" || /^\/[sw]\//.test(clean);
  }

  function binarySearchChar(files, charPos) {
    var lo = 0;
    var hi = files.length - 1;
    while (lo <= hi) {
      var mid = (lo + hi) >> 1;
      var f = files[mid];
      if (charPos < f.startChar) {
        hi = mid - 1;
      } else if (charPos >= f.endChar) {
        lo = mid + 1;
      } else {
        return f;
      }
    }
    return null;
  }

  function binarySearchLine(files, line) {
    var lo = 0;
    var hi = files.length - 1;
    while (lo <= hi) {
      var mid = (lo + hi) >> 1;
      var f = files[mid];
      if (line < f.startLine) {
        hi = mid - 1;
      } else if (line > f.endLine) {
        lo = mid + 1;
      } else {
        return f;
      }
    }
    return null;
  }

  function resolveRangeList(ranges, line, charPos, col, out) {
    if (!Array.isArray(ranges) || ranges.length === 0) return false;
    var entry = null;
    if (line > 0) {
      entry = binarySearchLine(ranges, line);
      if (entry && (charPos < 0 || (charPos >= entry.startChar && charPos < entry.endChar))) {
        var localLine = line - entry.startLine + 1;
        out.mappedFile = entry.file;
        out.localLine = localLine;
        out.loc = entry.file + ":" + localLine + (col ? ":" + col : "");
        if (charPos >= entry.startChar && charPos < entry.endChar) {
          out.charOffset = charPos - entry.startChar;
        }
        return true;
      }
      entry = null;
    }
    if (charPos >= 0) {
      entry = binarySearchChar(ranges, charPos);
      if (entry) {
        var spanChars = Math.max(1, entry.endChar - entry.startChar);
        var spanLines = Math.max(1, entry.endLine - entry.startLine + 1);
        var charOffset = charPos - entry.startChar;
        var approxLine = Math.min(spanLines, Math.max(1, Math.floor((charOffset / spanChars) * spanLines) + 1));
        out.mappedFile = entry.file;
        out.charOffset = charOffset;
        out.approxLine = approxLine;
        out.loc = entry.file + ":~" + approxLine + "+" + charOffset;
        return true;
      }
    }
    return false;
  }

  function mapLocation(info, overrideMap) {
    var map = overrideMap || getSourceMap();
    var rawUrl = info && typeof info.url === "string" ? info.url : "";
    var cleanPath = scrubUrlPath(rawUrl);
    var base = basename(cleanPath);
    var charPos = info && typeof info.charPos === "number" && isFinite(info.charPos) && info.charPos >= 0 ? Math.floor(info.charPos) : -1;
    var line = info && typeof info.line === "number" && isFinite(info.line) && info.line >= 1 ? Math.floor(info.line) : 0;
    var col = info && typeof info.col === "number" && isFinite(info.col) && info.col >= 0 ? Math.floor(info.col) : 0;

    var out = {
      sourceURL: cleanPath,
      mappedFile: "",
      charOffset: null,
      approxLine: null,
      localLine: null,
      col: col > 0 ? col : null,
      loc: cleanPath
    };

    if (map) {
      if (base && map.termFile && base === map.termFile) {
        out.mappedFile = "terminal.js";
        out.localLine = line || null;
        out.loc = line ? ("terminal.js:" + line + (col ? ":" + col : "")) : "terminal.js";
        return out;
      }
      if (base && map.webFile && base === map.webFile) {
        out.mappedFile = "webview.js";
        out.localLine = line || null;
        out.loc = line ? ("webview.js:" + line + (col ? ":" + col : "")) : "webview.js";
        return out;
      }
      if (base && map.selectFile && base === map.selectFile) {
        out.mappedFile = "select-all.js";
        out.localLine = line || null;
        out.loc = line ? ("select-all.js:" + line + (col ? ":" + col : "")) : "select-all.js";
        return out;
      }
      if (base && map.frameScriptFile && base === map.frameScriptFile) {
        if (resolveRangeList(map.frameFiles, line, charPos, col, out)) return out;
        out.mappedFile = "frame-core.js";
        out.localLine = line || null;
        out.loc = line ? ("frame-core.js:" + line + (col ? ":" + col : "")) : "frame-core.js";
        return out;
      }

      if (isBundlePageUrl(rawUrl, map) && resolveRangeList(map.files, line, charPos, col, out)) {
        return out;
      }
    }

    if (base) {
      out.loc = line ? (base + ":" + line + (col ? ":" + col : "")) : base;
    } else if (charPos >= 0) {
      out.loc = "char+" + charPos;
    }
    return out;
  }

  function pushCrumb(rec) {
    crumbRing[crumbHead] = rec;
    crumbHead = (crumbHead + 1) % CRUMB_CAP;
    if (crumbCount < CRUMB_CAP) crumbCount++;
  }

  // Whitelist-only structural crumb sanitization. Drops any non-whitelisted properties.
  function sanitizeCrumb(kind, meta, startPerf, endPerf, startWall) {
    var rec = {
      ts: round1(startPerf),
      endTs: round1(endPerf),
      wallTs: typeof startWall === "number" ? startWall : Date.now(),
      durMs: round1(Math.max(0, endPerf - startPerf)),
      frameKind: frameKind(),
      kind: scrubString(String(kind || "span")),
      op: ""
    };
    if (typeof meta === "string") {
      rec.op = scrubString(meta);
    } else if (meta && typeof meta === "object") {
      if (typeof meta.op === "string") rec.op = scrubString(meta.op);
      else if (typeof meta.phase === "string") rec.op = scrubString(meta.phase);
      if (typeof meta.phase === "string") rec.phase = scrubString(meta.phase);
      if (typeof meta.event === "string") rec.event = scrubString(meta.event);
      if (typeof meta.region === "string") rec.region = scrubString(meta.region);
      if (typeof meta.bytes === "number" && isFinite(meta.bytes)) rec.bytes = Math.max(0, Math.floor(meta.bytes));
      if (typeof meta.count === "number" && isFinite(meta.count)) rec.count = Math.max(0, Math.floor(meta.count));
    }
    if (!rec.op) rec.op = rec.kind;
    return rec;
  }

  function crumb(kind, meta) {
    var t0 = nowPerf();
    pushCrumb(sanitizeCrumb(kind, meta, t0, t0, Date.now()));
  }

  function mergeMeta(base, extra) {
    if (!extra || typeof extra !== "object") return base;
    if (typeof base === "string") base = { op: base };
    var out = {};
    var k;
    if (base && typeof base === "object") {
      for (k in base) if (Object.prototype.hasOwnProperty.call(base, k)) out[k] = base[k];
    }
    for (k in extra) if (Object.prototype.hasOwnProperty.call(extra, k)) out[k] = extra[k];
    return out;
  }

  function begin(kind, meta) {
    return {
      kind: kind || "span",
      meta: meta || null,
      startPerf: nowPerf(),
      startWall: Date.now()
    };
  }

  function end(tok, extra) {
    if (!tok) return;
    var endPerf = nowPerf();
    var merged = extra ? mergeMeta(tok.meta, extra) : tok.meta;
    var rec = sanitizeCrumb(tok.kind, merged, tok.startPerf, endPerf, tok.startWall);
    pushCrumb(rec);
    if (rec.durMs >= STALL_THRESHOLD_MS) {
      recordSpanStall(rec, tok.startPerf, endPerf);
    }
  }

  function span(kind, meta) {
    var tok = begin(kind, meta);
    return {
      end: function (extra) {
        end(tok, extra);
      }
    };
  }

  function collectTailCrumbs(startPerf, endPerf) {
    if (crumbCount === 0) return [];
    var all = [];
    var idx = crumbCount < CRUMB_CAP ? 0 : crumbHead;
    for (var i = 0; i < crumbCount; i++) {
      var item = crumbRing[(idx + i) % CRUMB_CAP];
      if (!item) continue;
      // Include crumbs up through the end of the stall (with 2ms tolerance for nested end ordering).
      if (typeof endPerf !== "number" || item.ts <= endPerf + 2) {
        all.push(copyWhitelistedCrumb(item));
      }
    }
    if (all.length > STALL_CRUMB_TAIL) {
      all = all.slice(all.length - STALL_CRUMB_TAIL);
    }
    // Keep any matching inner start/end ordered by ts.
    all.sort(function (a, b) { return a.ts - b.ts; });
    return all;
  }

  function copyWhitelistedCrumb(c) {
    var out = {
      ts: round1(c.ts),
      wallTs: roundMs(c.wallTs),
      durMs: round1(c.durMs),
      frameKind: typeof c.frameKind === "string" ? clampStr(c.frameKind, 24) : "unknown",
      kind: scrubString(c.kind),
      op: scrubString(c.op)
    };
    if (c.phase) out.phase = scrubString(c.phase);
    if (c.event) out.event = scrubString(c.event);
    if (c.region) out.region = scrubString(c.region);
    if (typeof c.bytes === "number" && isFinite(c.bytes)) out.bytes = Math.max(0, Math.floor(c.bytes));
    if (typeof c.count === "number" && isFinite(c.count)) out.count = Math.max(0, Math.floor(c.count));
    return out;
  }

  function findOverlappingLocalStallIndex(startPerf, endPerf) {
    // Match against local windows in [oldest..newest] from newest backwards.
    for (var i = localWindowRefs.length - 1; i >= 0; i--) {
      var w = localWindowRefs[i];
      if (Math.min(endPerf, w.endPerf) > Math.max(startPerf, w.startPerf)) {
        for (var j = stalls.length - 1; j >= 0; j--) {
          if (stalls[j].id === w.id) return { stallIdx: j, winIdx: i };
        }
      }
    }
    return null;
  }

  function notify(stall) {
    for (var i = 0; i < listeners.length; i++) {
      try { listeners[i](stalls.length, stall); } catch (x) {}
    }
    if (modalOpen) renderModal();
  }

  function emitRelay(stall) {
    if (typeof relayFn === "function") {
      try { relayFn(cloneStall(stall)); } catch (x) {}
    }
  }

  function pushOrUpsertLocal(stall, startPerf, endPerf) {
    var match = findOverlappingLocalStallIndex(startPerf, endPerf);
    if (match) {
      var existing = stalls[match.stallIdx];
      var merged = mergeStalls(existing, stall);
      stalls[match.stallIdx] = merged;
      var w = localWindowRefs[match.winIdx];
      w.startPerf = Math.min(w.startPerf, startPerf);
      w.endPerf = Math.max(w.endPerf, endPerf);
      notify(merged);
      emitRelay(merged);
      return merged;
    }

    stall.id = frameInstanceId + "-" + (++stallSeq);
    stalls.push(stall);
    localWindowRefs.push({ id: stall.id, startPerf: startPerf, endPerf: endPerf });
    while (stalls.length > STALL_CAP) {
      var dropped = stalls.shift();
      for (var i = localWindowRefs.length - 1; i >= 0; i--) {
        if (localWindowRefs[i].id === dropped.id) {
          localWindowRefs.splice(i, 1);
        }
      }
    }
    notify(stall);
    emitRelay(stall);
    return stall;
  }

  function ingest(rawStall) {
    var clean = sanitizeStall(rawStall);
    if (!clean || clean.durMs < STALL_THRESHOLD_MS) return null;
    if (!clean.id) clean.id = "ext-" + (++stallSeq);

    for (var i = stalls.length - 1; i >= 0; i--) {
      if (stalls[i].id === clean.id) {
        stalls[i] = mergeStalls(stalls[i], clean);
        notify(stalls[i]);
        return stalls[i];
      }
    }
    stalls.push(clean);
    while (stalls.length > STALL_CAP) {
      stalls.shift();
    }
    notify(clean);
    return clean;
  }

  function mergeStalls(prev, next) {
    var out = cloneStall(prev);
    if (next.durMs > out.durMs) out.durMs = next.durMs;
    if (next.wallTs && (!out.wallTs || next.wallTs < out.wallTs)) out.wallTs = next.wallTs;
    if (typeof next.blockingMs === "number" && (out.blockingMs === null || next.blockingMs > out.blockingMs)) {
      out.blockingMs = next.blockingMs;
    }
    if (typeof next.renderMs === "number" && (out.renderMs === null || next.renderMs > out.renderMs)) {
      out.renderMs = next.renderMs;
    }
    if (typeof next.forcedStyleMs === "number" && (out.forcedStyleMs === null || next.forcedStyleMs > out.forcedStyleMs)) {
      out.forcedStyleMs = next.forcedStyleMs;
    }
    if (typeof next.firstUIEventDeltaMs === "number" && out.firstUIEventDeltaMs === null) {
      out.firstUIEventDeltaMs = next.firstUIEventDeltaMs;
    }
    if (next.source === "loaf" || (out.source === "span" && next.source === "longtask")) {
      out.source = next.source;
    }
    var srcList = Array.isArray(out.sources) ? out.sources.slice() : [out.source];
    var addSrc = Array.isArray(next.sources) ? next.sources : [next.source];
    for (var i = 0; i < addSrc.length; i++) {
      if (addSrc[i] && srcList.indexOf(addSrc[i]) === -1) srcList.push(addSrc[i]);
    }
    out.sources = srcList;
    if (next.trigger && (!out.trigger || next.trigger.durMs >= out.trigger.durMs)) {
      out.trigger = next.trigger;
    }
    if (Array.isArray(next.scripts) && next.scripts.length > 0 && (!Array.isArray(out.scripts) || out.scripts.length === 0 || next.source === "loaf")) {
      out.scripts = next.scripts.slice(0, TOP_SCRIPTS_CAP);
    }
    if (Array.isArray(next.crumbs) && next.crumbs.length >= (out.crumbs ? out.crumbs.length : 0)) {
      out.crumbs = next.crumbs.slice(0, STALL_CRUMB_TAIL);
    }
    return out;
  }

  function extractScripts(entry) {
    if (!entry || !Array.isArray(entry.scripts) || entry.scripts.length === 0) return [];
    var sorted = entry.scripts.slice().sort(function (a, b) {
      return (b.duration || 0) - (a.duration || 0);
    });
    var list = [];
    for (var i = 0; i < sorted.length && list.length < TOP_SCRIPTS_CAP; i++) {
      var s = sorted[i];
      if (!s) continue;
      var loc = mapLocation({
        url: typeof s.sourceURL === "string" ? s.sourceURL : "",
        charPos: typeof s.sourceCharPosition === "number" ? s.sourceCharPosition : -1,
        line: typeof s.sourceLine === "number" ? s.sourceLine : 0,
        col: typeof s.sourceColumn === "number" ? s.sourceColumn : 0
      });
      var item = {
        invoker: scrubString(typeof s.invoker === "string" ? s.invoker : ""),
        invokerType: scrubString(typeof s.invokerType === "string" ? s.invokerType : ""),
        sourceURL: loc.sourceURL,
        mappedFile: loc.mappedFile,
        charOffset: loc.charOffset,
        approxLine: loc.approxLine,
        localLine: loc.localLine,
        col: loc.col,
        loc: loc.loc,
        fn: scrubString(typeof s.sourceFunctionName === "string" ? s.sourceFunctionName : ""),
        durMs: round1(typeof s.duration === "number" ? s.duration : 0),
        forcedStyleMs: round1(typeof s.forcedStyleAndLayoutDuration === "number" ? s.forcedStyleAndLayoutDuration : 0),
        pauseMs: round1(typeof s.pauseDuration === "number" ? s.pauseDuration : 0),
        windowAttribution: scrubString(typeof s.windowAttribution === "string" ? s.windowAttribution : "")
      };
      list.push(item);
    }
    return list;
  }

  function recordLoafEntry(entry) {
    var dur = typeof entry.duration === "number" ? entry.duration : 0;
    if (dur < STALL_THRESHOLD_MS) return;

    var startPerf = typeof entry.startTime === "number" ? entry.startTime : nowPerf();
    var endPerf = startPerf + dur;
    var perfNow = nowPerf();
    var wallTs = Math.round(Date.now() - Math.max(0, perfNow - startPerf));

    var renderMs = null;
    if (typeof entry.renderStart === "number" && entry.renderStart > 0 && endPerf >= entry.renderStart) {
      renderMs = round1(endPerf - entry.renderStart);
    }
    var forcedStyleMs = null;
    if (typeof entry.styleAndLayoutStart === "number" && entry.styleAndLayoutStart > 0 && endPerf >= entry.styleAndLayoutStart) {
      forcedStyleMs = round1(endPerf - entry.styleAndLayoutStart);
    } else if (Array.isArray(entry.scripts)) {
      var styleSum = 0;
      for (var i = 0; i < entry.scripts.length; i++) {
        if (entry.scripts[i] && typeof entry.scripts[i].forcedStyleAndLayoutDuration === "number") {
          styleSum += entry.scripts[i].forcedStyleAndLayoutDuration;
        }
      }
      if (styleSum > 0) forcedStyleMs = round1(styleSum);
    }

    var firstUIEventDeltaMs = null;
    if (typeof entry.firstUIEventTimestamp === "number" && entry.firstUIEventTimestamp > 0 && startPerf >= entry.firstUIEventTimestamp) {
      firstUIEventDeltaMs = round1(startPerf - entry.firstUIEventTimestamp);
    }

    var stall = {
      id: "",
      wallTs: wallTs,
      frameKind: frameKind(),
      source: "loaf",
      sources: ["loaf"],
      durMs: round1(dur),
      blockingMs: typeof entry.blockingDuration === "number" ? round1(entry.blockingDuration) : null,
      renderMs: renderMs,
      forcedStyleMs: forcedStyleMs,
      firstUIEventDeltaMs: firstUIEventDeltaMs,
      trigger: null,
      scripts: extractScripts(entry),
      crumbs: collectTailCrumbs(startPerf, endPerf)
    };
    pushOrUpsertLocal(stall, startPerf, endPerf);
  }

  function recordLongTaskEntry(entry) {
    var dur = typeof entry.duration === "number" ? entry.duration : 0;
    if (dur < STALL_THRESHOLD_MS) return;

    var startPerf = typeof entry.startTime === "number" ? entry.startTime : nowPerf();
    var endPerf = startPerf + dur;
    var perfNow = nowPerf();
    var wallTs = Math.round(Date.now() - Math.max(0, perfNow - startPerf));
    var containerKind = "";
    if (Array.isArray(entry.attribution) && entry.attribution.length > 0 && entry.attribution[0]) {
      containerKind = scrubString(entry.attribution[0].name || entry.attribution[0].containerType || "");
    }

    var stall = {
      id: "",
      wallTs: wallTs,
      frameKind: frameKind(),
      source: "longtask",
      sources: ["longtask"],
      durMs: round1(dur),
      blockingMs: round1(Math.max(0, dur - 50)),
      renderMs: null,
      forcedStyleMs: null,
      firstUIEventDeltaMs: null,
      trigger: containerKind ? { kind: "longtask", op: containerKind, durMs: round1(dur) } : null,
      scripts: [],
      crumbs: collectTailCrumbs(startPerf, endPerf)
    };
    pushOrUpsertLocal(stall, startPerf, endPerf);
  }

  function recordSpanStall(crumbRec, startPerf, endPerf) {
    var stall = {
      id: "",
      wallTs: crumbRec.wallTs,
      frameKind: crumbRec.frameKind,
      source: "span",
      sources: ["span"],
      durMs: crumbRec.durMs,
      blockingMs: null,
      renderMs: null,
      forcedStyleMs: null,
      firstUIEventDeltaMs: null,
      trigger: {
        kind: crumbRec.kind,
        op: crumbRec.op,
        durMs: crumbRec.durMs
      },
      scripts: [],
      crumbs: collectTailCrumbs(startPerf, endPerf)
    };
    pushOrUpsertLocal(stall, startPerf, endPerf);
  }

  function extractMinorTopLabel(entry) {
    if (!entry || !Array.isArray(entry.scripts) || entry.scripts.length === 0) return "";
    var best = null;
    var bestDur = -1;
    for (var i = 0; i < entry.scripts.length; i++) {
      var s = entry.scripts[i];
      if (!s) continue;
      var d = typeof s.duration === "number" && isFinite(s.duration) ? s.duration : 0;
      if (!best || d > bestDur) {
        best = s;
        bestDur = d;
      }
    }
    if (!best) return "";
    var inv = scrubString(typeof best.invoker === "string" ? best.invoker : "");
    var fn = scrubString(typeof best.sourceFunctionName === "string" ? best.sourceFunctionName : "");
    var label = "";
    if (inv && fn && inv.indexOf(fn) === -1) label = inv + " (" + fn + ")";
    else label = inv || fn;
    if (!label && typeof best.sourceURL === "string" && best.sourceURL) {
      var loc = mapLocation({
        url: best.sourceURL,
        charPos: typeof best.sourceCharPosition === "number" ? best.sourceCharPosition : -1,
        line: typeof best.sourceLine === "number" ? best.sourceLine : 0,
        col: typeof best.sourceColumn === "number" ? best.sourceColumn : 0
      });
      label = loc.mappedFile || loc.loc || basename(loc.sourceURL);
    }
    return label ? clampStr(label, 96) : "";
  }

  function applyMinorLoaf(durMs, topInvoker) {
    var dur = round1(Number(durMs));
    if (!isFinite(dur) || dur < MINOR_LOAF_MIN_MS || dur >= STALL_THRESHOLD_MS) return null;
    var label = typeof topInvoker === "string" ? clampStr(scrubString(topInvoker), 96) : "";
    minorCount += 1;
    if (dur > minorMaxMs) minorMaxMs = dur;
    if (label && dur >= minorTopInvokerLoafMs) {
      minorTopInvokerLoafMs = dur;
      minorTopInvoker = label;
    }
    return { durMs: dur, topInvoker: label };
  }

  function recordMinorLoaf(entry) {
    var rec = applyMinorLoaf(entry.duration, extractMinorTopLabel(entry));
    if (!rec) return;
    if (minorRelayFn) {
      try { minorRelayFn(rec); } catch (x) {}
    }
    if (modalOpen && modalEls) syncModalSubtitle(modalEls, stalls.length);
  }

  function ingestMinor(raw) {
    if (!raw || typeof raw !== "object") return false;
    var rec = applyMinorLoaf(raw.durMs, raw.topInvoker);
    if (!rec) return false;
    if (modalOpen && modalEls) syncModalSubtitle(modalEls, stalls.length);
    return true;
  }

  function ingestEntries(entries) {
    if (!Array.isArray(entries)) return;
    for (var i = 0; i < entries.length; i++) {
      var e = entries[i];
      if (!e || typeof e.duration !== "number") continue;
      if (e.entryType === "long-animation-frame") {
        if (e.duration >= STALL_THRESHOLD_MS) recordLoafEntry(e);
        else if (e.duration >= MINOR_LOAF_MIN_MS) recordMinorLoaf(e);
      } else if (e.entryType === "longtask") {
        if (e.duration >= STALL_THRESHOLD_MS) recordLongTaskEntry(e);
      }
    }
  }

  function sanitizeStall(s) {
    if (!s || typeof s !== "object") return null;
    var durMs = round1(Number(s.durMs));
    if (!isFinite(durMs) || durMs < STALL_THRESHOLD_MS) return null;

    var out = {
      id: typeof s.id === "string" ? clampStr(scrubString(s.id), 48) : "",
      wallTs: roundMs(s.wallTs),
      frameKind: typeof s.frameKind === "string" ? clampStr(scrubString(s.frameKind), 24) : "unknown",
      source: (s.source === "loaf" || s.source === "longtask" || s.source === "span") ? s.source : "span",
      sources: [],
      durMs: durMs,
      blockingMs: typeof s.blockingMs === "number" && isFinite(s.blockingMs) ? round1(s.blockingMs) : null,
      renderMs: typeof s.renderMs === "number" && isFinite(s.renderMs) ? round1(s.renderMs) : null,
      forcedStyleMs: typeof s.forcedStyleMs === "number" && isFinite(s.forcedStyleMs) ? round1(s.forcedStyleMs) : null,
      firstUIEventDeltaMs: typeof s.firstUIEventDeltaMs === "number" && isFinite(s.firstUIEventDeltaMs) ? round1(s.firstUIEventDeltaMs) : null,
      trigger: null,
      scripts: [],
      crumbs: []
    };

    if (Array.isArray(s.sources)) {
      for (var k = 0; k < s.sources.length && out.sources.length < 4; k++) {
        if (s.sources[k] === "loaf" || s.sources[k] === "longtask" || s.sources[k] === "span") {
          out.sources.push(s.sources[k]);
        }
      }
    }
    if (out.sources.length === 0) out.sources.push(out.source);

    if (s.trigger && typeof s.trigger === "object") {
      out.trigger = {
        kind: scrubString(String(s.trigger.kind || "")),
        op: scrubString(String(s.trigger.op || "")),
        durMs: round1(Number(s.trigger.durMs) || 0)
      };
    }

    if (Array.isArray(s.scripts)) {
      for (var i = 0; i < s.scripts.length && out.scripts.length < TOP_SCRIPTS_CAP; i++) {
        var sc = s.scripts[i];
        if (!sc || typeof sc !== "object") continue;
        out.scripts.push({
          invoker: scrubString(String(sc.invoker || "")),
          invokerType: scrubString(String(sc.invokerType || "")),
          sourceURL: scrubUrlPath(String(sc.sourceURL || "")),
          mappedFile: scrubString(String(sc.mappedFile || "")),
          charOffset: typeof sc.charOffset === "number" && isFinite(sc.charOffset) ? Math.floor(sc.charOffset) : null,
          approxLine: typeof sc.approxLine === "number" && isFinite(sc.approxLine) ? Math.floor(sc.approxLine) : null,
          localLine: typeof sc.localLine === "number" && isFinite(sc.localLine) ? Math.floor(sc.localLine) : null,
          col: typeof sc.col === "number" && isFinite(sc.col) ? Math.floor(sc.col) : null,
          loc: scrubString(String(sc.loc || "")),
          fn: scrubString(String(sc.fn || "")),
          durMs: round1(Number(sc.durMs) || 0),
          forcedStyleMs: round1(Number(sc.forcedStyleMs) || 0),
          pauseMs: round1(Number(sc.pauseMs) || 0),
          windowAttribution: scrubString(String(sc.windowAttribution || ""))
        });
      }
    }

    if (Array.isArray(s.crumbs)) {
      var tail = s.crumbs.slice(-STALL_CRUMB_TAIL);
      for (var j = 0; j < tail.length; j++) {
        if (tail[j] && typeof tail[j] === "object") {
          out.crumbs.push(copyWhitelistedCrumb(tail[j]));
        }
      }
    }
    return out;
  }

  function cloneStall(s) {
    return sanitizeStall(s);
  }

  function getStalls() {
    var out = [];
    for (var i = 0; i < stalls.length; i++) out.push(cloneStall(stalls[i]));
    return out;
  }

  function getCrumbs() {
    var out = [];
    var idx = crumbCount < CRUMB_CAP ? 0 : crumbHead;
    for (var i = 0; i < crumbCount; i++) {
      var item = crumbRing[(idx + i) % CRUMB_CAP];
      if (item) out.push(copyWhitelistedCrumb(item));
    }
    return out;
  }

  function getMinorStats() {
    return {
      count100to399: minorCount,
      maxMs100to399: minorMaxMs,
      topInvoker: minorTopInvoker
    };
  }

  function clear() {
    stalls = [];
    localWindowRefs = [];
    minorCount = 0;
    minorMaxMs = 0;
    minorTopInvoker = "";
    minorTopInvokerLoafMs = 0;
    crumbRing = new Array(CRUMB_CAP);
    crumbHead = 0;
    crumbCount = 0;
    selectedId = "";
    notify(null);
  }

  function setRelay(fn) {
    relayFn = typeof fn === "function" ? fn : null;
    if (relayFn && stalls.length > 0) {
      for (var i = 0; i < stalls.length; i++) emitRelay(stalls[i]);
    }
  }

  function setMinorRelay(fn) {
    minorRelayFn = typeof fn === "function" ? fn : null;
  }

  function onChange(fn) {
    if (typeof fn === "function" && listeners.indexOf(fn) === -1) {
      listeners.push(fn);
    }
  }

  function start() {
    if (started) return observerMode;
    started = true;
    if (typeof PerformanceObserver !== "function") return observerMode;
    var supported = PerformanceObserver.supportedEntryTypes;
    var hasLoaf = Array.isArray(supported) && supported.indexOf("long-animation-frame") !== -1;
    var hasLongTask = !Array.isArray(supported) || supported.indexOf("longtask") !== -1;

    if (hasLoaf) {
      try {
        observer = new PerformanceObserver(function (list) {
          if (list && typeof list.getEntries === "function") ingestEntries(list.getEntries());
        });
        observer.observe({ type: "long-animation-frame", buffered: false });
        observerMode = "loaf";
        return observerMode;
      } catch (x) {
        observer = null;
      }
    }

    if (hasLongTask) {
      try {
        observer = new PerformanceObserver(function (list) {
          if (list && typeof list.getEntries === "function") ingestEntries(list.getEntries());
        });
        try {
          observer.observe({ type: "longtask", buffered: false });
        } catch (y) {
          observer.observe({ entryTypes: ["longtask"] });
        }
        observerMode = "longtask";
      } catch (z) {
        observer = null;
        observerMode = "none";
      }
    }
    return observerMode;
  }

  function stop() {
    if (observer && typeof observer.disconnect === "function") {
      try { observer.disconnect(); } catch (x) {}
    }
    observer = null;
    started = false;
    observerMode = "none";
  }

  function formatWallTime(ms) {
    if (!ms) return "--:--:--";
    try {
      var d = new Date(ms);
      var hh = String(d.getHours()).padStart(2, "0");
      var mm = String(d.getMinutes()).padStart(2, "0");
      var ss = String(d.getSeconds()).padStart(2, "0");
      var mmm = String(d.getMilliseconds()).padStart(3, "0");
      return hh + ":" + mm + ":" + ss + "." + mmm;
    } catch (x) {
      return String(ms);
    }
  }

  function formatCrumbSummary(c) {
    var parts = [c.op || c.kind];
    if (c.phase && c.phase !== c.op) parts.push("phase=" + c.phase);
    if (c.event) parts.push("ev=" + c.event);
    if (c.region) parts.push("reg=" + c.region);
    if (typeof c.bytes === "number") parts.push(c.bytes + "B");
    if (typeof c.count === "number") parts.push("n=" + c.count);
    if (c.durMs > 0) parts.push(c.durMs + "ms");
    return "[" + c.frameKind + "] " + parts.join(" ");
  }

  function formatJSON() {
    return JSON.stringify({
      schema: "wsdashboard.jank.v1",
      thresholdMs: STALL_THRESHOLD_MS,
      mode: observerMode,
      minorLoaf: getMinorStats(),
      stalls: stalls.slice().reverse().map(cloneStall)
    }, null, 2);
  }

  function formatMarkdown() {
    var rev = stalls.slice().reverse();
    var minor = getMinorStats();
    var minorLine = minor.count100to399 > 0
      ? (minor.count100to399 + " (max " + minor.maxMs100to399 + "ms" + (minor.topInvoker ? (", top: `" + minor.topInvoker + "`") : "") + ")")
      : "0";
    var lines = [
      "## Dashboard UI Stall Report",
      "",
      "- **Captured**: " + rev.length + " (last " + STALL_CAP + ")",
      "- **Threshold**: >=" + STALL_THRESHOLD_MS + "ms",
      "- **100-399ms LoAFs**: " + minorLine,
      "- **Observer mode**: `" + observerMode + "`",
      ""
    ];
    if (rev.length === 0) {
      lines.push("_No UI stalls recorded._");
      return lines.join("\n");
    }
    for (var i = 0; i < rev.length; i++) {
      var s = rev[i];
      var srcLabel = Array.isArray(s.sources) ? s.sources.join("+") : s.source;
      lines.push("### #" + (i + 1) + " · " + s.durMs + "ms (`" + srcLabel + "`, `" + s.frameKind + "` @ " + formatWallTime(s.wallTs) + ")");
      var metrics = [];
      if (s.blockingMs !== null) metrics.push("blocking=" + s.blockingMs + "ms");
      if (s.renderMs !== null) metrics.push("render=" + s.renderMs + "ms");
      if (s.forcedStyleMs !== null) metrics.push("styleLayout=" + s.forcedStyleMs + "ms");
      if (s.trigger && s.trigger.op) metrics.push("span=`" + s.trigger.op + "` (" + s.trigger.durMs + "ms)");
      if (metrics.length) lines.push("- **Metrics**: " + metrics.join(" · "));
      if (s.scripts && s.scripts.length) {
        lines.push("- **Top scripts**:");
        for (var j = 0; j < s.scripts.length; j++) {
          var sc = s.scripts[j];
          var where = sc.loc || sc.mappedFile || sc.sourceURL || "<unknown>";
          var fn = sc.fn ? (" `" + sc.fn + "`") : "";
          var inv = sc.invoker ? (" [" + sc.invoker + "]") : "";
          lines.push("  - `" + where + "`" + fn + inv + " — **" + sc.durMs + "ms**" +
            (sc.forcedStyleMs > 0 ? (" (style " + sc.forcedStyleMs + "ms)") : "") +
            (sc.pauseMs > 0 ? (" (pause " + sc.pauseMs + "ms)") : ""));
        }
      }
      if (s.crumbs && s.crumbs.length) {
        lines.push("- **Leading breadcrumbs (" + s.crumbs.length + ")**:");
        for (var k = 0; k < s.crumbs.length; k++) {
          lines.push("  - `" + formatWallTime(s.crumbs[k].wallTs) + "` " + formatCrumbSummary(s.crumbs[k]));
        }
      }
      lines.push("");
    }
    return lines.join("\n");
  }

  function el(doc, tag, cls, text) {
    var n = doc.createElement(tag);
    if (cls) n.className = cls;
    if (text !== undefined && text !== null) n.textContent = text;
    return n;
  }

  function ensureModal() {
    if (modalEls) return modalEls;
    var doc = typeof document !== "undefined" ? document : null;
    if (!doc || !doc.body) return null;

    var scrim = el(doc, "div", "d-jank-scrim");
    scrim.hidden = true;

    var dialog = el(doc, "div", "d-jank-modal");
    dialog.setAttribute("role", "dialog");
    dialog.setAttribute("aria-modal", "true");
    dialog.setAttribute("aria-labelledby", "d-jank-title");
    dialog.tabIndex = -1;

    var head = el(doc, "div", "d-jank-head");
    var headInfo = el(doc, "div", "d-jank-head-info");
    var title = el(doc, "h2", "d-jank-title", t("js.jank.title", "UI stall recorder"));
    title.id = "d-jank-title";
    var sub = el(doc, "p", "d-jank-sub");
    headInfo.appendChild(title);
    headInfo.appendChild(sub);

    var acts = el(doc, "div", "d-jank-acts");
    var copyMdBtn = el(doc, "button", "d-jank-btn", t("js.jank.copy.md", "Copy Markdown"));
    copyMdBtn.type = "button";
    var copyJsonBtn = el(doc, "button", "d-jank-btn d-jank-btn-muted", t("js.jank.copy.json", "Copy JSON"));
    copyJsonBtn.type = "button";
    var clearBtn = el(doc, "button", "d-jank-btn d-jank-btn-danger", t("js.jank.clear", "Clear"));
    clearBtn.type = "button";
    var closeBtn = el(doc, "button", "d-jank-btn d-jank-btn-muted", t("js.jank.close", "Close"));
    closeBtn.type = "button";
    acts.appendChild(copyMdBtn);
    acts.appendChild(copyJsonBtn);
    acts.appendChild(clearBtn);
    acts.appendChild(closeBtn);
    head.appendChild(headInfo);
    head.appendChild(acts);

    var empty = el(doc, "div", "d-jank-empty");
    empty.appendChild(el(doc, "p", "d-jank-empty-title", t("js.jank.empty.title", "No UI stalls >= {0}ms recorded yet", STALL_THRESHOLD_MS)));
    empty.appendChild(el(doc, "p", "d-jank-empty-sub", t("js.jank.empty.body", "Monitoring Long Animation Frames and UI spans passively in memory. Slow interactions will appear here automatically.")));

    var body = el(doc, "div", "d-jank-body");
    var list = el(doc, "div", "d-jank-list");
    list.setAttribute("role", "listbox");
    list.setAttribute("aria-label", t("js.jank.title", "UI stall recorder"));
    var detail = el(doc, "div", "d-jank-detail");
    body.appendChild(list);
    body.appendChild(detail);

    var foot = el(doc, "div", "d-jank-foot");
    var footNote = el(doc, "p", "d-jank-note");
    foot.appendChild(footNote);

    dialog.appendChild(head);
    dialog.appendChild(empty);
    dialog.appendChild(body);
    dialog.appendChild(foot);
    scrim.appendChild(dialog);
    doc.body.appendChild(scrim);

    scrim.addEventListener("click", function (ev) {
      if (ev.target === scrim) closeModal();
    });
    closeBtn.addEventListener("click", closeModal);
    clearBtn.addEventListener("click", function () { clear(); });
    copyMdBtn.addEventListener("click", function () {
      performCopy(formatMarkdown(), t("js.jank.copied.md", "Copied stall report (Markdown)"));
    });
    copyJsonBtn.addEventListener("click", function () {
      performCopy(formatJSON(), t("js.jank.copied.json", "Copied stall log (JSON)"));
    });
    dialog.addEventListener("keydown", function (ev) {
      if (ev.key === "Escape") {
        ev.preventDefault();
        closeModal();
        return;
      }
      if ((ev.key === "ArrowDown" || ev.key === "ArrowUp") && stalls.length > 0) {
        ev.preventDefault();
        var rev = stalls.slice().reverse();
        var cur = 0;
        for (var i = 0; i < rev.length; i++) {
          if (rev[i].id === selectedId) { cur = i; break; }
        }
        var next = ev.key === "ArrowDown" ? Math.min(rev.length - 1, cur + 1) : Math.max(0, cur - 1);
        selectedId = rev[next].id;
        renderModal();
        var btns = list.querySelectorAll(".d-jank-item");
        if (btns[next] && typeof btns[next].focus === "function") btns[next].focus();
      }
    });

    modalEls = {
      scrim: scrim, dialog: dialog, sub: sub,
      copyMdBtn: copyMdBtn, copyJsonBtn: copyJsonBtn, clearBtn: clearBtn, closeBtn: closeBtn,
      empty: empty, body: body, list: list, detail: detail, footNote: footNote
    };
    return modalEls;
  }

  function performCopy(text, toastMsg) {
    if (activeOpts && typeof activeOpts.copyText === "function") {
      try { activeOpts.copyText(text, true); } catch (x) {}
    } else if (typeof navigator !== "undefined" && navigator.clipboard && typeof navigator.clipboard.writeText === "function") {
      try { navigator.clipboard.writeText(text); } catch (y) {}
    }
    if (typeof window !== "undefined" && window.WsToast && typeof window.WsToast.show === "function") {
      try { window.WsToast.show(toastMsg); } catch (z) {}
    }
  }

  function renderKV(doc, wrap, label, val) {
    var kv = el(doc, "div", "d-jank-kv");
    kv.appendChild(el(doc, "span", "d-jank-k", label));
    kv.appendChild(el(doc, "span", "d-jank-v", val));
    wrap.appendChild(kv);
  }

  function formatMinorSummaryText() {
    if (minorCount <= 0) {
      return t("js.jank.minor.zero", "100–399ms LoAFs: 0");
    }
    var topPart = minorTopInvoker ? (" · " + minorTopInvoker) : "";
    return t("js.jank.minor.summary", "100–399ms LoAFs: {0} (max {1}ms{2})", minorCount, minorMaxMs, topPart);
  }

  function syncModalSubtitle(m, stallCount) {
    if (!m || !m.sub) return;
    var base = stallCount === 0
      ? t("js.jank.subtitle.zero", "Passive monitor active · threshold {0}ms · 0 stalls recorded", STALL_THRESHOLD_MS)
      : t("js.jank.subtitle.count", "{0} stalls captured (last {1}) · threshold {2}ms", stallCount, STALL_CAP, STALL_THRESHOLD_MS);
    m.sub.textContent = base + " · " + formatMinorSummaryText();
    var hasAnyExport = stallCount > 0 || minorCount > 0;
    m.copyMdBtn.disabled = !hasAnyExport;
    m.copyJsonBtn.disabled = !hasAnyExport;
    m.clearBtn.disabled = !hasAnyExport;
  }

  function renderModal() {
    var m = modalEls;
    var doc = typeof document !== "undefined" ? document : null;
    if (!m || !doc) return;

    var modeLabel = observerMode === "loaf"
      ? t("js.jank.mode.loaf", "LoAF + spans")
      : t("js.jank.mode.fallback", "Longtask + spans");
    m.footNote.textContent = t("js.jank.note", "In-memory only · metadata scrubbed · {0}", modeLabel);

    var rev = stalls.slice().reverse();
    syncModalSubtitle(m, rev.length);

    var hasData = rev.length > 0;
    m.empty.hidden = hasData;
    m.body.hidden = !hasData;

    if (!hasData) {
      m.list.textContent = "";
      m.detail.textContent = "";
      return;
    }

    var selected = null;
    for (var i = 0; i < rev.length; i++) {
      if (rev[i].id === selectedId) { selected = rev[i]; break; }
    }
    if (!selected) {
      selected = rev[0];
      selectedId = selected.id;
    }

    m.list.textContent = "";
    for (var idx = 0; idx < rev.length; idx++) {
      (function (st) {
        var btn = el(doc, "button", "d-jank-item");
        btn.type = "button";
        btn.setAttribute("role", "option");
        btn.setAttribute("aria-selected", st.id === selected.id ? "true" : "false");

        var top = el(doc, "div", "d-jank-item-top");
        var durCls = "d-jank-dur " + (st.durMs >= 1000 ? "d-jank-sev-crit" : "d-jank-sev-warn");
        top.appendChild(el(doc, "span", durCls, st.durMs + "ms"));
        top.appendChild(el(doc, "span", "d-jank-time", formatWallTime(st.wallTs)));

        var meta = el(doc, "div", "d-jank-item-meta");
        meta.appendChild(el(doc, "span", "d-jank-tag", st.frameKind));
        var primarySrc = (st.scripts && st.scripts[0] && (st.scripts[0].loc || st.scripts[0].mappedFile || st.scripts[0].fn || st.scripts[0].invoker)) ||
          (st.trigger && st.trigger.op) ||
          (Array.isArray(st.sources) ? st.sources.join("+") : st.source);
        meta.appendChild(el(doc, "span", "d-jank-item-src", primarySrc));

        btn.appendChild(top);
        btn.appendChild(meta);
        btn.addEventListener("click", function () {
          selectedId = st.id;
          renderModal();
        });
        m.list.appendChild(btn);
      })(rev[idx]);
    }

    m.detail.textContent = "";
    var summary = el(doc, "div", "d-jank-summary");
    renderKV(doc, summary, t("js.jank.col.duration", "Duration"), selected.durMs + "ms");
    renderKV(doc, summary, t("js.jank.col.blocking", "Blocking"), selected.blockingMs !== null ? (selected.blockingMs + "ms") : "—");
    var rsText = (selected.renderMs !== null ? (selected.renderMs + "ms") : "—") + " / " +
      (selected.forcedStyleMs !== null ? (selected.forcedStyleMs + "ms") : "—");
    renderKV(doc, summary, t("js.jank.col.render", "Render / Style"), rsText);
    var srcListText = (Array.isArray(selected.sources) ? selected.sources.join("+") : selected.source) + " · " + selected.frameKind;
    renderKV(doc, summary, t("js.jank.col.source", "Capture / Frame"), srcListText);
    m.detail.appendChild(summary);

    var scSec = el(doc, "div", "d-jank-sec");
    scSec.appendChild(el(doc, "h3", "d-jank-sec-h", t("js.jank.sec.scripts", "Top culprit scripts")));
    if (!selected.scripts || selected.scripts.length === 0) {
      scSec.appendChild(el(doc, "p", "d-jank-note", t("js.jank.sec.scripts.empty", "No script timings captured for this stall.")));
    } else {
      var scWrap = el(doc, "div", "d-jank-scripts");
      for (var j = 0; j < selected.scripts.length; j++) {
        var sc = selected.scripts[j];
        var card = el(doc, "div", "d-jank-script");
        var scTop = el(doc, "div", "d-jank-script-top");
        var label = (sc.fn ? (sc.fn + " · ") : "") + (sc.invoker || sc.invokerType || "script");
        scTop.appendChild(el(doc, "span", "d-jank-script-inv", label));
        scTop.appendChild(el(doc, "span", "d-jank-time", sc.durMs + "ms"));
        card.appendChild(scTop);
        if (sc.loc || sc.sourceURL) {
          card.appendChild(el(doc, "div", "d-jank-script-loc", sc.loc || sc.sourceURL));
        }
        var extra = [];
        if (sc.invokerType) extra.push(sc.invokerType);
        if (sc.forcedStyleMs > 0) extra.push("style " + sc.forcedStyleMs + "ms");
        if (sc.pauseMs > 0) extra.push("pause " + sc.pauseMs + "ms");
        if (sc.windowAttribution) extra.push(sc.windowAttribution);
        if (extra.length) card.appendChild(el(doc, "div", "d-jank-script-meta", extra.join(" · ")));
        scWrap.appendChild(card);
      }
      scSec.appendChild(scWrap);
    }
    m.detail.appendChild(scSec);

    var crSec = el(doc, "div", "d-jank-sec");
    var crumbs = selected.crumbs || [];
    crSec.appendChild(el(doc, "h3", "d-jank-sec-h", t("js.jank.sec.crumbs", "Leading breadcrumbs ({0})", crumbs.length)));
    if (crumbs.length === 0) {
      crSec.appendChild(el(doc, "p", "d-jank-note", t("js.jank.sec.crumbs.empty", "No instrumented breadcrumbs recorded right before this stall.")));
    } else {
      var crWrap = el(doc, "div", "d-jank-crumbs");
      for (var k = 0; k < crumbs.length; k++) {
        var row = el(doc, "div", "d-jank-crumb");
        row.appendChild(el(doc, "span", "d-jank-crumb-main", formatCrumbSummary(crumbs[k])));
        row.appendChild(el(doc, "span", "d-jank-crumb-ts", formatWallTime(crumbs[k].wallTs)));
        crWrap.appendChild(row);
      }
      crSec.appendChild(crWrap);
    }
    m.detail.appendChild(crSec);

    var rawDet = el(doc, "details", "d-jank-raw-wrap");
    rawDet.appendChild(el(doc, "summary", "", t("js.jank.sec.raw", "Raw scrubbed JSON")));
    rawDet.appendChild(el(doc, "pre", "d-jank-raw", JSON.stringify(selected, null, 2)));
    m.detail.appendChild(rawDet);
  }

  function openModal(opts) {
    activeOpts = opts || null;
    if (activeOpts && typeof activeOpts.closeMenu === "function") {
      try { activeOpts.closeMenu(); } catch (x) {}
    }
    prevFocus = (opts && opts.triggerEl) || (typeof document !== "undefined" ? document.activeElement : null);
    var m = ensureModal();
    if (!m) return;
    modalOpen = true;
    m.scrim.hidden = false;
    renderModal();
    if (typeof m.dialog.focus === "function") m.dialog.focus();
  }

  function closeModal() {
    modalOpen = false;
    if (modalEls) modalEls.scrim.hidden = true;
    if (prevFocus && prevFocus.isConnected !== false && typeof prevFocus.focus === "function") {
      try { prevFocus.focus(); } catch (x) {}
    }
    prevFocus = null;
  }

  return {
    THRESHOLD_MS: STALL_THRESHOLD_MS,
    start: start,
    stop: stop,
    crumb: crumb,
    begin: begin,
    end: end,
    span: span,
    ingest: ingest,
    ingestMinor: ingestMinor,
    ingestEntries: ingestEntries,
    setRelay: setRelay,
    setMinorRelay: setMinorRelay,
    onChange: onChange,
    getStalls: getStalls,
    getMinorStats: getMinorStats,
    getCrumbs: getCrumbs,
    clear: clear,
    mapLocation: mapLocation,
    scrubString: scrubString,
    scrubUrlPath: scrubUrlPath,
    formatJSON: formatJSON,
    formatMarkdown: formatMarkdown,
    openModal: openModal,
    closeModal: closeModal,
    mode: function () { return observerMode; },
    count: function () { return stalls.length; }
  };
})();
if (typeof window !== "undefined") window.WsJank = WsJank;
if (typeof globalThis !== "undefined") globalThis.WsJank = WsJank;
if (typeof window !== "undefined" && typeof PerformanceObserver === "function") WsJank.start();
