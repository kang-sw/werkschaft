// Cosmetic inline markdown and @ mention highlighting for the composer via
// the CSS Custom Highlight API (CSS.highlights + ::highlight(...)).
// Keeps .wsc-editor's flat DOM untouched for Korean/CJK IME safety, undo stack
// preservation, and zero keystroke DOM churn or layout reads.
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
  var mdComposing = false, mdRaf = 0;

  // scanComposerMarkdown returns canonical value-offset [start, end] spans
  // grouped by highlight name. Pure and side-effect free for unit testing.
  function scanComposerMarkdown(text, mentionList) {
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
        if (text.charCodeAt(k) === 96) {
          var run = 1;
          while (k + run < end && text.charCodeAt(k + run) === 96) run++;
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
          // Skip inline code span inside outer formatting.
          var run = 1;
          while (k + run < end && text.charCodeAt(k + run) === 96) run++;
          var closeCode = findClosingBackticks(text, k + run, end, run);
          k = closeCode >= 0 ? closeCode + run : k + run;
          continue;
        }
        if (text.slice(k, k + dlen) === delim) {
          // Avoid matching inside a longer run of the same char for single-char delims.
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
        // 1. Inline code spans suppress all inner markdown parsing.
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
        // 2. Attachment/reference markers ([Image #N], [Text #N]) are never links.
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
        // 3. Strikethrough ~~text~~
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
        // 4. Bold **text** or __text__
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
        // 5. Italic *text* or _text_
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

  // buildEditorTextMap walks .wsc-editor using the same canonical offset rules
  // as capsules.js read(), returning only highlightable Text node segments and
  // skipping atomic .wsc-capsule subtrees.
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

  function clearComposerHighlights() {
    if (!(window.CSS && CSS.highlights)) return;
    for (var i = 0; i < MD_HIGHLIGHT_GROUPS.length; i++) {
      CSS.highlights.delete(MD_HIGHLIGHT_GROUPS[i]);
    }
  }

  function applyComposerHighlights() {
    if (!(window.CSS && CSS.highlights && typeof window.Highlight === "function")) return;
    var editor = ctext && ctext.wsc && ctext.wsc.editor;
    if (!editor || !ctext.value || (typeof bangMode === "function" && bangMode())) {
      clearComposerHighlights();
      return;
    }
    var groups = scanComposerMarkdown(ctext.value, typeof mentions !== "undefined" ? mentions : []);
    var segs = buildEditorTextMap(editor);
    for (var g = 0; g < MD_HIGHLIGHT_GROUPS.length; g++) {
      var name = MD_HIGHLIGHT_GROUPS[g];
      var spans = groups[name], ranges = [];
      for (var s = 0; s < spans.length; s++) {
        var mapped = valueSpanToDomRanges(doc, segs, spans[s][0], spans[s][1]);
        for (var k = 0; k < mapped.length; k++) ranges.push(mapped[k]);
      }
      if (ranges.length) {
        var hl = new Highlight();
        hl.priority = MD_HIGHLIGHT_PRIORITY[name];
        for (var j = 0; j < ranges.length; j++) hl.add(ranges[j]);
        CSS.highlights.set(name, hl);
      } else {
        CSS.highlights.delete(name);
      }
    }
  }

  function scheduleComposerHighlights() {
    if (!(window.CSS && CSS.highlights && typeof window.Highlight === "function")) return;
    if (mdComposing || mdRaf) return;
    var raf = typeof requestAnimationFrame === "function" ? requestAnimationFrame : function (fn) { return setTimeout(fn, 0); };
    mdRaf = raf(function () {
      mdRaf = 0;
      if (!mdComposing) applyComposerHighlights();
    });
  }

  if (ctext) {
    ctext.addEventListener("compositionstart", function () { mdComposing = true; });
    ctext.addEventListener("compositionend", function () { mdComposing = false; scheduleComposerHighlights(); });
  }
