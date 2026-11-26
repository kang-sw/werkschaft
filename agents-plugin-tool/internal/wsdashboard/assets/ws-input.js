(function () {
  // Unified, instance-scoped multi-line input controller (window.WsInput) and
  // legacy CSS Custom Highlight fallback (window.WsMdEditor, installed only
  // when a richer markdown editor has not registered itself).
  //
  // Every attached surface owns isolated state: value/draft, mentions,
  // capsules, @/#/\/ popups, markdown reconciliation, keymap, and attachments.
  // Multiple simultaneous instances in one document never share module-level
  // editor globals and never clobber each other's CSS.highlights entries.

  var doc = document;
  var MD_HIGHLIGHT_GROUPS = ["ws-md-marker", "ws-md-head", "ws-md-quote", "ws-md-em", "ws-md-strong", "ws-md-strike", "ws-md-link", "ws-md-mention", "ws-md-code"];
  var MD_HIGHLIGHT_PRIORITY = {
    "ws-md-marker": 1,
    "ws-md-head": 2,
    "ws-md-quote": 2,
    "ws-md-em": 3,
    "ws-md-strong": 4,
    "ws-md-strike": 4,
    "ws-md-link": 5,
    "ws-md-mention": 6,
    "ws-md-code": 7
  };

  function wsT(key, vars) {
    return typeof window.wsT === "function" ? window.wsT(key, vars) : key;
  }

  // --- Pure markdown scanner & DOM range mapper -----------------------------
  function scanMarkdown(text, mentionList) {
    var out = {};
    for (var g = 0; g < MD_HIGHLIGHT_GROUPS.length; g++) out[MD_HIGHLIGHT_GROUPS[g]] = [];
    if (typeof text !== "string" || !text) return out;
    var fencedRanges = [];
    var len = text.length, pos = 0;
    var inFence = false, fenceChar = "", fenceLen = 0;

    function push(group, s, e) {
      if (e > s) out[group].push([s, e]);
    }
    function isWordChar(ch) {
      return /[A-Za-z0-9_]/.test(ch);
    }
    function findClosingBackticks(s, from, end, count) {
      var k = from;
      while (k < end) {
        if (s.charCodeAt(k) === 96) {
          var run = 1;
          while (k + run < end && s.charCodeAt(k + run) === 96) run++;
          if (run === count) return k;
          k += run;
        } else {
          k++;
        }
      }
      return -1;
    }
    function findClosingDelim(from, end, delim, wordBound) {
      var dlen = delim.length, k = from;
      while (k + dlen <= end) {
        if (text.charCodeAt(k) === 96) {
          var run = 1;
          while (k + run < end && text.charCodeAt(k + run) === 96) run++;
          var closeCode = findClosingBackticks(text, k + run, end, run);
          k = closeCode >= 0 ? closeCode + run : k + run;
          continue;
        }
        if (text.slice(k, k + dlen) === delim) {
          if (dlen === 1 && (k + 1 < end && text.charAt(k + 1) === delim)) {
            k += 2;
            continue;
          }
          if (k > from && !/[ \t]/.test(text.charAt(k - 1))) {
            if (!wordBound || k + dlen >= end || !isWordChar(text.charAt(k + dlen))) return k;
          }
        }
        k++;
      }
      return -1;
    }
    function scanInline(start, end, depth) {
      if (depth > 3 || start >= end) return;
      var i = start;
      while (i < end) {
        var ch = text.charAt(i);
        if (ch === "`") {
          var ticks = 1;
          while (i + ticks < end && text.charCodeAt(i + ticks) === 96) ticks++;
          var closeTick = findClosingBackticks(text, i + ticks, end, ticks);
          if (closeTick >= 0) {
            push("ws-md-marker", i, i + ticks);
            push("ws-md-marker", closeTick, closeTick + ticks);
            push("ws-md-code", i, closeTick + ticks);
            i = closeTick + ticks;
            continue;
          }
          i += ticks;
          continue;
        }
        if (ch === "[") {
          var sub = text.slice(i, end);
          var refM = /^\[(?:Image|Text) #[1-9][0-9]{0,5}\]/.exec(sub);
          if (refM) {
            i += refM[0].length;
            continue;
          }
          var linkM = /^\[([^\]\r\n]+)\]\(([^)\s]+)\)/.exec(sub);
          if (linkM) {
            var labelLen = linkM[1].length;
            push("ws-md-marker", i, i + 1);
            push("ws-md-link", i + 1, i + 1 + labelLen);
            push("ws-md-marker", i + 1 + labelLen, i + linkM[0].length);
            i += linkM[0].length;
            continue;
          }
        }
        if (ch === "~" && i + 1 < end && text.charAt(i + 1) === "~" && text.charAt(i + 2) !== "~" && i + 2 < end && !/[ \t]/.test(text.charAt(i + 2))) {
          var closeStrike = findClosingDelim(i + 2, end, "~~", false);
          if (closeStrike > i + 2) {
            push("ws-md-marker", i, i + 2);
            push("ws-md-strike", i + 2, closeStrike);
            push("ws-md-marker", closeStrike, closeStrike + 2);
            scanInline(i + 2, closeStrike, depth + 1);
            i = closeStrike + 2;
            continue;
          }
        }
        if ((ch === "*" || ch === "_") && i + 1 < end && text.charAt(i + 1) === ch && i + 2 < end && !/[ \t]/.test(text.charAt(i + 2))) {
          var wb2 = ch === "_";
          if (!wb2 || i === start || !isWordChar(text.charAt(i - 1))) {
            var closeBold = findClosingDelim(i + 2, end, ch + ch, wb2);
            if (closeBold > i + 2) {
              push("ws-md-marker", i, i + 2);
              push("ws-md-strong", i + 2, closeBold);
              push("ws-md-marker", closeBold, closeBold + 2);
              scanInline(i + 2, closeBold, depth + 1);
              i = closeBold + 2;
              continue;
            }
          }
        }
        if ((ch === "*" || ch === "_") && i + 1 < end && text.charAt(i + 1) !== ch && !/[ \t]/.test(text.charAt(i + 1))) {
          var wb1 = ch === "_";
          if (!wb1 || i === start || !isWordChar(text.charAt(i - 1))) {
            var closeEm = findClosingDelim(i + 1, end, ch, wb1);
            if (closeEm > i + 1) {
              push("ws-md-marker", i, i + 1);
              push("ws-md-em", i + 1, closeEm);
              push("ws-md-marker", closeEm, closeEm + 1);
              scanInline(i + 1, closeEm, depth + 1);
              i = closeEm + 1;
              continue;
            }
          }
        }
        i++;
      }
    }

    while (pos <= len) {
      var nl = text.indexOf("\n", pos);
      var lineEnd = nl < 0 ? len : nl;
      var line = text.slice(pos, lineEnd);
      var fenceM = /^([ \t]{0,3})(`{3,}|~{3,})(.*)$/.exec(line);
      if (fenceM) {
        var fch = fenceM[2].charAt(0), flen = fenceM[2].length, finfo = fenceM[3];
        if (!inFence && (fch === "~" || finfo.indexOf("`") < 0)) {
          inFence = true; fenceChar = fch; fenceLen = flen;
          push("ws-md-marker", pos + fenceM[1].length, lineEnd);
          fencedRanges.push([pos, lineEnd]);
          if (nl < 0) break;
          pos = nl + 1;
          continue;
        }
        if (inFence && fch === fenceChar && flen >= fenceLen && /^[ \t]*$/.test(finfo)) {
          inFence = false; fenceChar = ""; fenceLen = 0;
          push("ws-md-marker", pos + fenceM[1].length, lineEnd);
          fencedRanges.push([pos, lineEnd]);
          if (nl < 0) break;
          pos = nl + 1;
          continue;
        }
      }
      if (inFence) {
        push("ws-md-code", pos, lineEnd);
        fencedRanges.push([pos, lineEnd]);
        if (nl < 0) break;
        pos = nl + 1;
        continue;
      }
      var leadLen = /^[ \t]*/.exec(line)[0].length;
      var p = pos + leadLen;
      var rest = text.slice(p, lineEnd);
      var qm = /^(?:>[ \t]?)+/.exec(rest);
      if (qm) {
        push("ws-md-marker", p, p + qm[0].length);
        push("ws-md-quote", p + qm[0].length, lineEnd);
        p += qm[0].length;
        rest = text.slice(p, lineEnd);
      }
      var hm = /^(#{1,6})(?=[ \t])/.exec(rest);
      if (hm) {
        push("ws-md-marker", p, p + hm[1].length);
        push("ws-md-head", p + hm[1].length, lineEnd);
        p += hm[1].length;
      } else {
        var lm = /^([-*+][ \t]+\[[ xX]\])(?=[ \t]|$)/.exec(rest) || /^([-*+])(?=[ \t])/.exec(rest) || /^(\d+\.)(?=[ \t])/.exec(rest);
        if (lm) {
          push("ws-md-marker", p, p + lm[1].length);
          p += lm[1].length;
        }
      }
      scanInline(p, lineEnd, 0);
      if (nl < 0) break;
      pos = nl + 1;
    }

    if (Array.isArray(mentionList) && mentionList.length) {
      for (var mi = 0; mi < mentionList.length; mi++) {
        var m = mentionList[mi];
        if (!m || !Number.isInteger(m.start) || !Number.isInteger(m.end) || m.start < 0 || m.end <= m.start || m.end > len) continue;
        if (typeof m.text === "string" && text.slice(m.start, m.end) !== m.text) continue;
        var inFenced = false;
        for (var fi = 0; fi < fencedRanges.length; fi++) {
          if (m.start < fencedRanges[fi][1] && m.end > fencedRanges[fi][0]) { inFenced = true; break; }
        }
        if (!inFenced) push("ws-md-mention", m.start, m.end);
      }
    }
    return out;
  }

  function buildEditorTextMap(root) {
    var segs = [], outLen = 0, lastChar = "";
    function isMeaningful(node) { return node.nodeType === 1 || (node.nodeType === 3 && node.nodeValue.length > 0); }
    function prevMeaningful(node) { for (var s = node.previousSibling; s; s = s.previousSibling) if (isMeaningful(s)) return s; return null; }
    function nextMeaningful(node) { for (var s = node.nextSibling; s; s = s.nextSibling) if (isMeaningful(s)) return s; return null; }
    function capsuleMarkerLen(el) {
      var kind = el.getAttribute("data-wsc-kind"), id = el.getAttribute("data-wsc-id") || "";
      if (kind === "skill") return id.length;
      if (kind === "topic") return id.length + 2;
      try {
        var p = JSON.parse(el.getAttribute("data-wsc-payload") || "null");
        return p && typeof p.anchor === "string" ? p.anchor.length : 0;
      } catch (_) { return 0; }
    }
    function walk(n) {
      if (n.nodeType === 3) {
        var v = n.nodeValue || "";
        if (v.length) {
          segs.push({ node: n, start: outLen, end: outLen + v.length });
          outLen += v.length;
          lastChar = v.charAt(v.length - 1);
        }
        return;
      }
      if (n.nodeType !== 1 && n !== root) return;
      if (n.nodeType === 1 && n.hasAttribute && n.hasAttribute("data-wsc-kind")) {
        var mlen = capsuleMarkerLen(n);
        outLen += mlen;
        if (mlen > 0) lastChar = "]";
        return;
      }
      if (n.nodeName === "BR") {
        if (!nextMeaningful(n) && (n.parentNode === root || /^(DIV|P)$/.test(n.parentNode.nodeName))) return;
        outLen += 1; lastChar = "\n";
        return;
      }
      var block = n !== root && /^(DIV|P)$/.test(n.nodeName);
      var prev = block ? prevMeaningful(n) : null;
      if (block && prev && (/^(DIV|P)$/.test(prev.nodeName) || (outLen > 0 && lastChar !== "\n"))) {
        outLen += 1; lastChar = "\n";
      }
      var kids = n.childNodes || [];
      for (var i = 0; i < kids.length; i++) walk(kids[i]);
      var next = block ? nextMeaningful(n) : null;
      if (block && next && !/^(DIV|P)$/.test(next.nodeName) && lastChar !== "\n") {
        outLen += 1; lastChar = "\n";
      }
    }
    if (root) walk(root);
    return segs;
  }

  function valueSpanToDomRanges(documentObj, segs, start, end) {
    var ranges = [];
    if (end <= start || !segs.length) return ranges;
    for (var i = 0; i < segs.length; i++) {
      var seg = segs[i];
      if (seg.end <= start) continue;
      if (seg.start >= end) break;
      var rs = Math.max(start, seg.start) - seg.start;
      var re = Math.min(end, seg.end) - seg.start;
      if (re > rs) {
        var r = documentObj.createRange();
        r.setStart(seg.node, rs);
        r.setEnd(seg.node, re);
        ranges.push(r);
      }
    }
    return ranges;
  }

  // --- Legacy WsMdEditor fallback (per-document CSS.highlights aggregation) -
  // Multiple editors in one document register their own range maps and the
  // aggregator unions ranges per highlight group so updating or detaching one
  // editor never clobbers another editor's highlights.
  var docRegistries = typeof WeakMap === "function" ? new WeakMap() : null;
  var fallbackRegistry = [];
  var mdEntrySeq = 0;

  function entriesForDoc(d) {
    if (docRegistries) {
      var list = docRegistries.get(d);
      if (!list) { list = []; docRegistries.set(d, list); }
      return list;
    }
    for (var i = 0; i < fallbackRegistry.length; i++) {
      if (fallbackRegistry[i].doc === d) return fallbackRegistry[i].entries;
    }
    var rec = { doc: d, entries: [] };
    fallbackRegistry.push(rec);
    return rec.entries;
  }

  function flushDocHighlights(d) {
    var win = (d && d.defaultView) || window;
    var cssObj = (win && win.CSS) || window.CSS;
    var HLCtor = (win && win.Highlight) || window.Highlight;
    if (!(cssObj && cssObj.highlights && typeof HLCtor === "function")) return;
    var entries = entriesForDoc(d);
    for (var p = entries.length - 1; p >= 0; p--) {
      if (entries[p].editor && entries[p].editor.isConnected === false) entries.splice(p, 1);
    }
    for (var g = 0; g < MD_HIGHLIGHT_GROUPS.length; g++) {
      var name = MD_HIGHLIGHT_GROUPS[g], ranges = [];
      for (var i = 0; i < entries.length; i++) {
        var eg = entries[i].groups[name];
        if (eg && eg.length) {
          for (var k = 0; k < eg.length; k++) ranges.push(eg[k]);
        }
      }
      if (ranges.length) {
        var hl = new HLCtor();
        hl.priority = MD_HIGHLIGHT_PRIORITY[name];
        for (var j = 0; j < ranges.length; j++) hl.add(ranges[j]);
        cssObj.highlights.set(name, hl);
      } else {
        cssObj.highlights.delete(name);
      }
    }
  }

  function readCanonicalText(editorEl, opts) {
    if (opts && typeof opts.getValue === "function") return String(opts.getValue() || "");
    if (window.WsCapsules && typeof window.WsCapsules.fieldFor === "function") {
      var f = window.WsCapsules.fieldFor(editorEl);
      if (f) return String(f.value || "");
    }
    return String((editorEl && editorEl.textContent) || "");
  }

  if (!window.WsMdEditor) {
    window.WsMdEditor = {
      _legacy: true,
      groups: MD_HIGHLIGHT_GROUPS,
      scan: scanMarkdown,
      buildEditorTextMap: buildEditorTextMap,
      valueSpanToDomRanges: valueSpanToDomRanges,
      attach: function (editorEl, opts) {
        opts = opts || {};
        var ownerDoc = (editorEl && editorEl.ownerDocument) || doc;
        var entries = entriesForDoc(ownerDoc);
        var entry = { id: ++mdEntrySeq, editor: editorEl, groups: {} };
        var detached = false;
        entries.push(entry);

        function clear() {
          entry.groups = {};
          flushDocHighlights(ownerDoc);
        }

        function reconcile() {
          if (detached || !editorEl) return;
          if (opts.isComposing && opts.isComposing()) return;
          var text = readCanonicalText(editorEl, opts);
          if (!text || (opts.isBang && opts.isBang())) {
            clear();
            if (typeof opts.onReconciled === "function") opts.onReconciled();
            return;
          }
          var mentions = typeof opts.getMentions === "function" ? opts.getMentions() : [];
          var groups = scanMarkdown(text, mentions);
          var segs = buildEditorTextMap(editorEl);
          var next = {};
          for (var g = 0; g < MD_HIGHLIGHT_GROUPS.length; g++) {
            var name = MD_HIGHLIGHT_GROUPS[g], spans = groups[name], ranges = [];
            for (var s = 0; s < spans.length; s++) {
              var mapped = valueSpanToDomRanges(ownerDoc, segs, spans[s][0], spans[s][1]);
              for (var k = 0; k < mapped.length; k++) ranges.push(mapped[k]);
            }
            if (ranges.length) next[name] = ranges;
          }
          entry.groups = next;
          flushDocHighlights(ownerDoc);
          if (typeof opts.onReconciled === "function") opts.onReconciled();
        }

        function detach() {
          if (detached) return;
          detached = true;
          var idx = entries.indexOf(entry);
          if (idx >= 0) entries.splice(idx, 1);
          entry.groups = {};
          flushDocHighlights(ownerDoc);
        }

        return { reconcile: reconcile, clear: clear, detach: detach };
      }
    };
  }

  // --- Shared editing helpers -----------------------------------------------
  function indentLines(field, out, editFn) {
    if (!field) return false;
    var v = field.value, s = field.selectionStart, e = field.selectionEnd;
    var apply = editFn || function (a, b, text) {
      field.setRangeText(text, a, b, "end");
      field.dispatchEvent(new Event("input", { bubbles: true }));
    };
    if (!out && s === e) {
      apply(s, e, "  ");
      return true;
    }
    var ls = v.lastIndexOf("\n", s - 1) + 1;
    var le = e > s && v.charAt(e - 1) === "\n" ? e - 1 : v.indexOf("\n", e);
    if (le < 0) le = v.length;
    var lines = v.slice(ls, le).split("\n"), firstDelta = 0, totalDelta = 0;
    var next = lines.map(function (line, i) {
      if (!out) {
        if (i === 0) firstDelta = 2;
        totalDelta += 2;
        return "  " + line;
      }
      var m = /^(?: {1,2}|\t)/.exec(line);
      var drop = m ? m[0].length : 0;
      if (i === 0) firstDelta = -drop;
      totalDelta -= drop;
      return line.slice(drop);
    }).join("\n");
    if (next === v.slice(ls, le)) return true;
    apply(ls, le, next);
    var ns = Math.max(ls, s + firstDelta);
    var ne = s === e ? ns : Math.max(ns, e + totalDelta);
    field.setSelectionRange(ns, ne);
    return true;
  }

  // listContinuation inspects the current line at caret and returns an edit
  // descriptor { start, end, insert, caret } to continue or exit a markdown
  // list or blockquote on Enter, or null when the line is not a list/quote.
  function listContinuation(value, caret) {
    if (typeof value !== "string" || caret < 0 || caret > value.length) return null;
    var ls = value.lastIndexOf("\n", caret - 1) + 1;
    var le = value.indexOf("\n", caret);
    if (le < 0) le = value.length;
    var before = value.slice(ls, caret);
    var after = value.slice(caret, le);
    var m;

    // Task list: - [ ] / * [x] / + [X]
    m = /^([ \t]*)([-*+])([ \t]+)\[[ xX]\]([ \t]+)(.*)$/.exec(before);
    if (m) {
      if (!m[5].trim() && !after.trim()) return { start: ls, end: le, insert: "", caret: ls };
      var tIns = "\n" + m[1] + m[2] + m[3] + "[ ]" + m[4];
      return { start: caret, end: caret, insert: tIns, caret: caret + tIns.length };
    }
    // Unordered list: -, *, +
    m = /^([ \t]*)([-*+])([ \t]+)(.*)$/.exec(before);
    if (m) {
      if (!m[4].trim() && !after.trim()) return { start: ls, end: le, insert: "", caret: ls };
      var uIns = "\n" + m[1] + m[2] + m[3];
      return { start: caret, end: caret, insert: uIns, caret: caret + uIns.length };
    }
    // Ordered list: 1. or 1)
    m = /^([ \t]*)(\d+)([.)])([ \t]+)(.*)$/.exec(before);
    if (m) {
      if (!m[5].trim() && !after.trim()) return { start: ls, end: le, insert: "", caret: ls };
      var nIns = "\n" + m[1] + (parseInt(m[2], 10) + 1) + m[3] + m[4];
      return { start: caret, end: caret, insert: nIns, caret: caret + nIns.length };
    }
    // Blockquote: > ...
    m = /^([ \t]*(?:>[ \t]?)+)(.*)$/.exec(before);
    if (m) {
      if (!m[2].trim() && !after.trim()) return { start: ls, end: le, insert: "", caret: ls };
      var qPrefix = m[1];
      if (!/[ \t]$/.test(qPrefix)) qPrefix += " ";
      var qIns = "\n" + qPrefix;
      return { start: caret, end: caret, insert: qIns, caret: caret + qIns.length };
    }
    return null;
  }

  // formatCommandRows filters and sorts slash commands matching q.
  function formatCommandRows(list, q) {
    var needle = String(q || "").toLowerCase();
    var hits = (list || []).filter(function (c) {
      return !needle || (c.name || "").toLowerCase().indexOf(needle) >= 0 || (c.description || "").toLowerCase().indexOf(needle) >= 0;
    });
    hits.sort(function (a, b) {
      var ga = a.group || "", gb = b.group || "";
      if (ga !== gb) return ga === "User" ? -1 : gb === "User" ? 1 : ga.localeCompare(gb);
      var pa = (a.name || "").toLowerCase().indexOf(needle) === 0 ? 0 : 1;
      var pb = (b.name || "").toLowerCase().indexOf(needle) === 0 ? 0 : 1;
      return pa - pb || (a.name || "").localeCompare(b.name || "");
    });
    return hits;
  }

  function renderCommandRow(li, it) {
    var ownerDoc = (li && li.ownerDocument) || doc;
    var name = ownerDoc.createElement("span"), desc = ownerDoc.createElement("span");
    name.className = "wsm-name"; name.textContent = "/" + it.name;
    desc.className = "wsm-desc"; desc.textContent = it.description || "";
    li.appendChild(name); li.appendChild(desc);
    if (it.source) {
      var src = ownerDoc.createElement("span");
      src.className = "wsm-src"; src.textContent = it.source;
      li.appendChild(src);
    }
    li.title = "/" + it.name + (it.description ? " — " + it.description : "");
  }

  // defaultSlashToken matches a `/command` token at start of line or after
  // whitespace so paths like `foo/bar` never open the slash picker.
  function defaultSlashToken(v, caret) {
    var ls = v.lastIndexOf("\n", caret - 1) + 1;
    var le = v.indexOf("\n", caret);
    if (le < 0) le = v.length;
    var line = v.slice(ls, le), c = caret - ls;
    for (var i = line.lastIndexOf("/", c - 1); i >= 0; i = i > 0 ? line.lastIndexOf("/", i - 1) : -1) {
      if (i > 0 && !/\s/.test(line.charAt(i - 1))) continue;
      var body = line.slice(i + 1, c);
      if (/\s/.test(body)) return null;
      var tail = /^\S*/.exec(line.slice(c))[0];
      return { start: ls + i, end: ls + c + tail.length, q: body };
    }
    return null;
  }

  // --- WsInput core ---------------------------------------------------------
  var DEFAULTS = {
    markdown: true,
    capsules: true,
    mentions: true,
    topics: true,
    slash: true,
    imagePaste: false,
    longPaste: false,
    listContinuation: true,
    bangMode: false,
    autoGrow: true,
    voice: false
  };

  var instances = [];

  function mergeFeatures(raw) {
    var out = {};
    Object.keys(DEFAULTS).forEach(function (k) {
      out[k] = raw && Object.prototype.hasOwnProperty.call(raw, k) ? !!raw[k] : DEFAULTS[k];
    });
    return out;
  }

  function clipboardImageFiles(dt) {
    var files = [];
    if (!dt) return files;
    if (dt.items) {
      for (var i = 0; i < dt.items.length; i++) {
        var it = dt.items[i];
        if (it.kind === "file" && /^image\//.test(it.type)) {
          var f = it.getAsFile && it.getAsFile();
          if (f) files.push(f);
        }
      }
    }
    if (!files.length && dt.files) {
      for (var j = 0; j < dt.files.length; j++) {
        if (/^image\//.test(dt.files[j].type)) files.push(dt.files[j]);
      }
    }
    return files;
  }

  function attach(target, options) {
    if (!target) return null;
    if (target.wsInput) {
      if (options) target.wsInput.updateConfig(options);
      return target.wsInput;
    }
    var opts = options || {};
    var ownerDoc = target.ownerDocument || doc;
    var field = target, ownsField = false;
    if (target.tagName !== "TEXTAREA") {
      field = ownerDoc.createElement("textarea");
      if (opts.className) field.className = opts.className;
      if (opts.placeholder) field.setAttribute("placeholder", opts.placeholder);
      if (opts.ariaLabel) field.setAttribute("aria-label", opts.ariaLabel);
      if (opts.rows) field.setAttribute("rows", String(opts.rows));
      if (typeof opts.initialValue === "string") field.value = opts.initialValue;
      target.appendChild(field);
      ownsField = true;
    }

    var features = mergeFeatures(opts.features);
    var providers = opts.providers || {};
    var draftAdapter = opts.draftAdapter || null;
    var attachmentHandler = opts.attachmentHandler || null;

    // Per-instance isolated state
    var mentions = [];
    var mentionWas = field.value || "";
    var composing = false;
    var tabOut = false;
    var allowLineBreak = false;
    var mdRaf = 0;
    var growRaf = 0;
    var destroyed = false;

    var wsc = null;
    var mdEditor = null;
    var mentionPop = null;
    var topicPop = null;
    var slashPop = null;
    var ownsTopicPop = false;

    if (features.capsules && window.WsCapsules) {
      wsc = window.WsCapsules.attach(field);
    }

    function isBangActive() {
      if (!features.bangMode) return false;
      if (typeof opts.isBang === "function") return !!opts.isBang();
      return /^!/.test(field.value || "");
    }

    function currentKeymap() {
      var km = typeof opts.keymap === "function" ? opts.keymap() : opts.keymap;
      return km || "newline-only";
    }

    function editText(start, end, text) {
      var prevLen = field.value.length;
      if (typeof field.focus === "function") field.focus();
      if (typeof field.setSelectionRange === "function") field.setSelectionRange(start, end);
      var ok = false;
      try { ok = ownerDoc.execCommand && ownerDoc.execCommand("insertText", false, text); } catch (_) { ok = false; }
      if (!ok) {
        if (typeof field.setRangeText === "function") field.setRangeText(text, start, end, "end");
        else field.value = field.value.slice(0, start) + text + field.value.slice(end);
        field.dispatchEvent(new Event("input", { bubbles: true }));
      } else if (field.value.length === prevLen && (text.length !== end - start || field.value.slice(start, start + text.length) !== text)) {
        if (typeof field.setRangeText === "function") field.setRangeText(text, start, end, "end");
        else field.value = field.value.slice(0, start) + text + field.value.slice(end);
        field.dispatchEvent(new Event("input", { bubbles: true }));
      }
      if (wsc && text.indexOf("\n") >= 0 && typeof wsc.revealCaret === "function") wsc.revealCaret();
    }

    // --- Per-instance mention tracking ---
    function liveMention(m, v) {
      var before = m.start > 0 ? v.charAt(m.start - 1) : "", after = v.charAt(m.end);
      return v.slice(m.start, m.end) === m.text && (!before || /\s/.test(before)) && (!after || /\s/.test(after));
    }

    function shiftMentions(atOrDelta, maybeDelta) {
      var v = field.value || "";
      if (maybeDelta === undefined) {
        var d = atOrDelta;
        mentions = mentions.filter(function (m) { m.start += d; m.end += d; return liveMention(m, v); });
      } else {
        mentions = mentions.filter(function (m) {
          if (m.start >= atOrDelta) { m.start += maybeDelta; m.end += maybeDelta; }
          return liveMention(m, v);
        });
      }
      mentionWas = v;
      syncMarkdown();
    }

    function trackMentions() {
      var v = field.value || "", was = mentionWas, a = 0, s = 0;
      mentionWas = v;
      if (!mentions.length) return;
      while (a < was.length && a < v.length && was.charCodeAt(a) === v.charCodeAt(a)) a++;
      while (s < was.length - a && s < v.length - a && was.charCodeAt(was.length - 1 - s) === v.charCodeAt(v.length - 1 - s)) s++;
      var end = was.length - s, d = v.length - was.length;
      mentions = mentions.filter(function (m) {
        if (end <= m.start) { m.start += d; m.end += d; } else if (a < m.end) return false;
        return liveMention(m, v);
      });
    }

    function sendText(raw) {
      var v = typeof raw === "string" ? raw : (field.value || "");
      return stripMentions(v, mentions);
    }

    // --- Per-instance markdown highlighting via WsMdEditor seam ---
    function clearMarkdown() {
      if (mdEditor && typeof mdEditor.clear === "function") mdEditor.clear();
    }

    function reconcileMarkdown() {
      if (destroyed || !features.markdown || !mdEditor) return;
      mdEditor.reconcile();
    }

    function syncMarkdown() {
      if (destroyed || !features.markdown || !mdEditor) return;
      if (composing || mdRaf) return;
      var raf = typeof requestAnimationFrame === "function" ? requestAnimationFrame : function (fn) { return setTimeout(fn, 0); };
      mdRaf = raf(function () {
        mdRaf = 0;
        if (!destroyed && !composing) reconcileMarkdown();
      });
    }

    function attachMarkdown() {
      if (mdEditor) { mdEditor.detach(); mdEditor = null; }
      if (!features.markdown || !window.WsMdEditor || typeof window.WsMdEditor.attach !== "function") return;
      var editorEl = wsc && wsc.editor ? wsc.editor : null;
      if (!editorEl) return;
      mdEditor = window.WsMdEditor.attach(editorEl, {
        getMentions: function () { return mentions; },
        isBang: isBangActive,
        isComposing: function () { return composing; },
        getValue: function () { return field.value || ""; },
        onReconciled: opts.onMarkdownReconciled
      });
    }

    // --- Per-instance autoGrow (no layout thrashing on .wsc-backing) ---
    function syncGrow() {
      if (!features.autoGrow) return;
      if (field.classList && field.classList.contains("wsc-backing")) return;
      if (typeof CSS !== "undefined" && CSS.supports && CSS.supports("field-sizing", "content")) return;
      if (growRaf) return;
      var raf = typeof requestAnimationFrame === "function" ? requestAnimationFrame : function (fn) { return setTimeout(fn, 0); };
      growRaf = raf(function () {
        growRaf = 0;
        if (destroyed || (field.classList && field.classList.contains("wsc-backing"))) return;
        field.style.height = "auto";
        if (field.scrollHeight) field.style.height = field.scrollHeight + "px";
      });
    }

    // --- Per-instance popups (@, #, /) ---
    function attachMentionPopup() {
      if (mentionPop) { mentionPop.destroy(); mentionPop = null; }
      if (!features.mentions || !window.wsMention || typeof providers.files !== "function") return;
      mentionPop = window.wsMention.attach(field, {
        label: opts.mentionLabel || wsT("js.composer.files"),
        token: function (v, c) {
          if (!features.mentions || isBangActive()) return null;
          return window.wsMention.tokenAt(v, c);
        },
        source: providers.files,
        insert: editText,
        onPick: function (m) {
          trackMentions();
          if (!m.dir) {
            mentions = mentions.filter(function (x) { return x.end <= m.start || x.start >= m.end; });
            mentions.push({ start: m.start, end: m.end, text: m.text });
          }
          if (typeof opts.onMentionPick === "function") opts.onMentionPick(m, api);
          syncMarkdown();
        }
      });
    }

    function attachTopicPopup() {
      if (topicPop && ownsTopicPop) { topicPop.destroy(); topicPop = null; ownsTopicPop = false; }
      if (!features.topics || !window.WsCapsules || typeof providers.topics !== "function") return;
      if (wsc && wsc.topicPop) {
        topicPop = wsc.topicPop;
        ownsTopicPop = false;
        return;
      }
      topicPop = window.WsCapsules.topicPicker(field, providers.topics);
      ownsTopicPop = !!topicPop;
    }

    function attachSlashPopup() {
      if (slashPop) { slashPop.destroy(); slashPop = null; }
      if (!features.slash || !window.wsMention || typeof providers.commands !== "function") return;
      var slashTok = opts.slashToken || defaultSlashToken;
      var slashOpts = {
        label: opts.slashLabel || wsT("js.composer.commands"),
        max: opts.slashMax || 50,
        token: function (v, c) {
          if (!features.slash || isBangActive()) return null;
          return slashTok(v, c);
        },
        source: function (q, done) {
          providers.commands(q, function (rows) {
            done(opts.preformattedCommands ? (rows || []) : formatCommandRows(rows || [], q));
          });
        },
        render: opts.renderCommand || renderCommandRow,
        choose: function (it, at) {
          if (typeof opts.onSlashPick === "function") {
            opts.onSlashPick(it, at, api);
            return;
          }
          var v = field.value || "";
          var spaced = /\s/.test(v.charAt(at.end));
          var text = "/" + it.name + (spaced ? "" : " ");
          editText(at.start, at.end, text);
          var caret = at.start + ("/" + it.name).length + (spaced ? 0 : 1);
          if (typeof field.setSelectionRange === "function") field.setSelectionRange(caret, caret);
        }
      };
      if (typeof opts.slashExact === "function") slashOpts.exact = opts.slashExact;
      slashPop = window.wsMention.attach(field, slashOpts);
    }

    function handlePopupKey(e) {
      if (slashPop && slashPop.key(e)) return true;
      if (mentionPop && mentionPop.key(e)) return true;
      if (topicPop && topicPop.key(e)) return true;
      return false;
    }

    function closePopups() {
      if (slashPop) slashPop.close();
      if (mentionPop) mentionPop.close();
      if (topicPop) topicPop.close();
    }

    function syncPopups() {
      if (slashPop) slashPop.sync();
      if (mentionPop) mentionPop.sync();
      if (topicPop) topicPop.sync();
    }

    // --- Event handlers ---
    function onInput() {
      tabOut = false;
      trackMentions();
      syncGrow();
      syncMarkdown();
      if (draftAdapter && typeof draftAdapter.set === "function") draftAdapter.set(field.value);
      if (typeof opts.onChange === "function") opts.onChange(field.value, api);
    }

    function onCompStart() {
      composing = true;
    }

    function onCompEnd() {
      composing = false;
      syncMarkdown();
    }

    function onBeforeInput(e) {
      if (e.inputType !== "insertParagraph" && e.inputType !== "insertLineBreak") return;
      var km = currentKeymap();
      if ((km === "enter-send" || km === "enter-save") && !allowLineBreak) e.preventDefault();
      allowLineBreak = false;
    }

    function onKeyDown(e) {
      if (e.key === "Enter" || e.code === "Enter" || e.code === "NumpadEnter") allowLineBreak = !!(e.shiftKey || e.altKey);
      else if (e.key !== "Shift" && e.key !== "Alt") allowLineBreak = false;
      if (e.defaultPrevented || e.isComposing || e.keyCode === 229) return;
      if (typeof opts.onBeforeKeyDown === "function" && opts.onBeforeKeyDown(e, api) === true) return;

      if (handlePopupKey(e)) return;

      if (e.key === "Escape") {
        if (typeof opts.onEscape === "function" && opts.onEscape(e, api) === true) return;
        if (opts.smartTab) tabOut = true;
        if (typeof opts.onCancel === "function") {
          e.preventDefault();
          opts.onCancel(api, e);
        }
        return;
      }

      if (e.key === "Tab" && !e.ctrlKey && !e.metaKey && !e.altKey) {
        if (opts.smartTab) {
          if (tabOut) { tabOut = false; return; }
          e.preventDefault();
          indentLines(field, e.shiftKey, editText);
        }
        return;
      }

      tabOut = false;

      if (e.key === "Enter") {
        var km = currentKeymap();
        var hasMod = !!(e.ctrlKey || e.metaKey);
        var plainEnter = !e.shiftKey && !e.ctrlKey && !e.metaKey && !e.altKey;
        var shiftEnter = e.shiftKey && !e.ctrlKey && !e.metaKey && !e.altKey;
        var submit = false;
        if (km === "enter-send" || km === "enter-save") {
          submit = plainEnter || (hasMod && !e.altKey && opts.modEnterSubmits !== false);
        } else if (km === "mod-send") {
          submit = hasMod && !e.altKey;
        }
        if (submit && typeof opts.onSubmit === "function") {
          e.preventDefault();
          if (!e.repeat) opts.onSubmit(api, e);
          return;
        }
        // Newline path (Shift+Enter in enter-send/enter-save, plain Enter in mod-send/newline-only)
        var insertingNewline = (km === "enter-send" || km === "enter-save") ? shiftEnter : (km === "mod-send" || km === "newline-only") ? (plainEnter || shiftEnter) : false;
        if (insertingNewline && features.listContinuation && field.selectionStart === field.selectionEnd) {
          var cont = listContinuation(field.value || "", field.selectionStart);
          if (cont) {
            e.preventDefault();
            editText(cont.start, cont.end, cont.insert);
            if (typeof field.setSelectionRange === "function") field.setSelectionRange(cont.caret, cont.caret);
            return;
          }
        }
      }

      if (typeof opts.onKeyDown === "function") opts.onKeyDown(e, api);
    }

    function onPaste(e) {
      if (e.defaultPrevented) return;
      var dt = e.clipboardData;
      if (!dt) return;
      if (features.imagePaste && attachmentHandler && typeof attachmentHandler.onImagePaste === "function") {
        var imgs = clipboardImageFiles(dt);
        if (imgs.length) {
          if (attachmentHandler.onImagePaste(imgs, e, api) !== false) return;
        }
      }
      if (features.longPaste && attachmentHandler && typeof attachmentHandler.onLongPaste === "function") {
        var plain = dt.getData ? dt.getData("text/plain") : "";
        if (plain && attachmentHandler.onLongPaste(plain, e, api) === true) return;
      }
    }

    function onBlur() {
      tabOut = false;
      allowLineBreak = false;
      if (draftAdapter && typeof draftAdapter.persist === "function") draftAdapter.persist();
      if (typeof opts.onBlur === "function") opts.onBlur(api);
    }

    var api = {
      field: field,
      target: target,
      editor: wsc ? wsc.editor : field,
      wsc: wsc,
      features: features,
      allowsTopic: function () { return features.topics && !isBangActive(); },
      allowsMention: function () { return features.mentions && !isBangActive(); },
      allowsSlash: function () { return features.slash && !isBangActive(); },
      isBang: isBangActive,
      getValue: function () { return field.value || ""; },
      setValue: function (val, optsSet) {
        var next = String(val == null ? "" : val);
        field.value = next;
        mentionWas = next;
        if (!(optsSet && optsSet.keepMentions)) mentions = [];
        syncGrow();
        syncMarkdown();
        if (draftAdapter && typeof draftAdapter.set === "function") draftAdapter.set(next);
      },
      getSelection: function () { return [field.selectionStart || 0, field.selectionEnd || 0]; },
      setSelection: function (a, b) {
        if (typeof field.setSelectionRange === "function") field.setSelectionRange(a, b == null ? a : b);
      },
      focus: function () { if (typeof field.focus === "function") field.focus(); },
      blur: function () { if (typeof field.blur === "function") field.blur(); },
      editText: editText,
      getMentions: function () { return mentions.slice(); },
      setMentions: function (list) {
        mentions = Array.isArray(list) ? list.map(function (m) { return Object.assign({}, m); }) : [];
        mentionWas = field.value || "";
        syncMarkdown();
      },
      shiftMentions: shiftMentions,
      clearMentions: function () { mentions = []; mentionWas = field.value || ""; syncMarkdown(); },
      trackMentions: trackMentions,
      sendText: sendText,
      syncMarkdown: syncMarkdown,
      reconcileMarkdown: reconcileMarkdown,
      clearMarkdown: clearMarkdown,
      syncGrow: syncGrow,
      syncPopups: syncPopups,
      closePopups: closePopups,
      handlePopupKey: handlePopupKey,
      handleIndent: function (out) { return indentLines(field, !!out, editText); },
      getPopups: function () { return { mention: mentionPop, topic: topicPop, slash: slashPop }; },
      updateConfig: function (nextOpts) {
        if (!nextOpts) return;
        opts = Object.assign({}, opts, nextOpts);
        if (nextOpts.features) {
          var prevFeatures = features;
          features = mergeFeatures(Object.assign({}, prevFeatures, nextOpts.features));
          api.features = features;
        }
        if (nextOpts.providers) providers = Object.assign({}, providers, nextOpts.providers);
        if (Object.prototype.hasOwnProperty.call(nextOpts, "draftAdapter")) draftAdapter = nextOpts.draftAdapter;
        if (Object.prototype.hasOwnProperty.call(nextOpts, "attachmentHandler")) attachmentHandler = nextOpts.attachmentHandler;
        attachMarkdown();
        attachMentionPopup();
        attachTopicPopup();
        attachSlashPopup();
        syncMarkdown();
      },
      destroy: function () {
        if (destroyed) return;
        destroyed = true;
        if (typeof field.removeEventListener === "function") {
          field.removeEventListener("input", onInput);
          field.removeEventListener("compositionstart", onCompStart);
          field.removeEventListener("compositionend", onCompEnd);
          if (!opts.managedKeydown) {
            field.removeEventListener("beforeinput", onBeforeInput);
            field.removeEventListener("keydown", onKeyDown);
          }
          field.removeEventListener("paste", onPaste);
          field.removeEventListener("blur", onBlur);
        }
        if (mdEditor) { mdEditor.detach(); mdEditor = null; }
        if (mentionPop) { mentionPop.destroy(); mentionPop = null; }
        if (topicPop && ownsTopicPop) { topicPop.destroy(); topicPop = null; ownsTopicPop = false; }
        if (slashPop) { slashPop.destroy(); slashPop = null; }
        if (field.wsInput === api) delete field.wsInput;
        if (target !== field && target.wsInput === api) delete target.wsInput;
        if (ownsField && field.parentNode) field.parentNode.removeChild(field);
        var idx = instances.indexOf(api);
        if (idx >= 0) instances.splice(idx, 1);
      }
    };

    field.wsInput = api;
    if (target !== field) target.wsInput = api;

    field.addEventListener("input", onInput);
    field.addEventListener("compositionstart", onCompStart);
    field.addEventListener("compositionend", onCompEnd);
    if (!opts.managedKeydown) {
      field.addEventListener("beforeinput", onBeforeInput);
      field.addEventListener("keydown", onKeyDown);
    }
    field.addEventListener("paste", onPaste);
    field.addEventListener("blur", onBlur);

    attachMarkdown();
    attachMentionPopup();
    attachTopicPopup();
    attachSlashPopup();
    if (field.value) reconcileMarkdown();

    instances.push(api);
    return api;
  }

  function get(target) {
    return target && target.wsInput ? target.wsInput : null;
  }

  function sweep(root) {
    for (var i = instances.length - 1; i >= 0; i--) {
      var inst = instances[i];
      if (!inst || !inst.field) { instances.splice(i, 1); continue; }
      if (inst.field.isConnected === false || (root && root.contains && root.contains(inst.field))) {
        inst.destroy();
      }
    }
  }

  function liveMentionSpan(m, v) {
    if (!m || typeof m.text !== "string") return false;
    var before = m.start > 0 ? v.charAt(m.start - 1) : "", after = v.charAt(m.end);
    return v.slice(m.start, m.end) === m.text && (!before || /\s/.test(before)) && (!after || /\s/.test(after));
  }

  function stripMentions(raw, list) {
    var v = String(raw || ""), out = "", at = 0;
    if (!Array.isArray(list) || !list.length) return v;
    list.slice().sort(function (a, b) { return a.start - b.start; }).forEach(function (m) {
      if (m.start < at || !liveMentionSpan(m, v)) return;
      out += v.slice(at, m.start) + m.text.slice(1);
      at = m.end;
    });
    return out + v.slice(at);
  }

  window.WsInput = {
    attach: attach,
    get: get,
    sweep: sweep,
    DEFAULTS: DEFAULTS,
    indentLines: indentLines,
    listContinuation: listContinuation,
    formatCommandRows: formatCommandRows,
    renderCommandRow: renderCommandRow,
    defaultSlashToken: defaultSlashToken,
    stripMentions: stripMentions
  };
})();
