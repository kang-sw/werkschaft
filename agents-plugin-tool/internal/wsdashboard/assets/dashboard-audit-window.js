  // --- audit view: floating window / bottom sheet renderer & body formatting ---
  var AU_TABS = ["in", "out", "meta", "raw", "events"];
  var AU_RANGE_CHUNK = 65536;

  function auEsc(s) {
    return String(s == null ? "" : s)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;")
      .replace(/'/g, "&#39;");
  }

  function auFmtBytes(n) {
    if (typeof n !== "number" || !isFinite(n) || n < 0) return "0 B";
    if (n < 1024) return Math.round(n) + " B";
    if (n < 1048576) return (n / 1024).toFixed(1) + " KB";
    return (n / 1048576).toFixed(1) + " MB";
  }

  function auFmtSpan(ms) {
    if (typeof ms !== "number" || !isFinite(ms) || ms <= 0) return "";
    if (ms < 100) return "<0.1s";
    if (ms < 10000) return (ms / 1000).toFixed(1) + "s";
    var s = Math.round(ms / 1000);
    if (s < 60) return s + "s";
    var m = Math.floor(s / 60);
    if (m < 60) return m + "m " + (s % 60) + "s";
    return Math.floor(m / 60) + "h " + (m % 60) + "m";
  }

  function auFmtCompactTok(n) {
    var abs = Math.abs(n);
    var sign = n < 0 ? "-" : "+";
    if (abs < 1000) return (n === 0 ? "0" : sign + String(Math.round(abs)));
    if (abs < 99950) return sign + (abs / 1000).toFixed(1) + "k";
    if (abs < 999500) return sign + Math.round(abs / 1000) + "k";
    return sign + (abs / 1e6).toFixed(1) + "M";
  }

  // Decision (610): token growth shows ctx_after - ctx_before on the first call
  // of a request, "·" on parallel siblings sharing request_id, and blank if null.
  function auTokenGrowthDisplay(summary, seenRequests) {
    if (!summary) return { text: "", sibling: false, delta: null };
    var cb = summary.ctx_before, ca = summary.ctx_after;
    if (typeof cb !== "number" || typeof ca !== "number" || !isFinite(cb) || !isFinite(ca)) {
      return { text: "", sibling: false, delta: null };
    }
    var reqId = typeof summary.request_id === "string" ? summary.request_id : "";
    if (reqId && seenRequests) {
      if (seenRequests[reqId]) return { text: "\u00b7", sibling: true, delta: null };
      seenRequests[reqId] = true;
    }
    var delta = ca - cb;
    return { text: auFmtCompactTok(delta), sibling: false, delta: delta };
  }

  function auUtf8Len(ch, code) {
    if (code <= 0x7f) return 1;
    if (code <= 0x7ff) return 2;
    if (code >= 0xd800 && code <= 0xdbff) return 4; // surrogate pair -> 4 UTF-8 bytes
    return 3;
  }

  function auUtf8ByteToCharIndex(str, byteOffset) {
    if (!str || byteOffset <= 0) return 0;
    var b = 0, i = 0, len = str.length;
    while (i < len && b < byteOffset) {
      var code = str.charCodeAt(i);
      var step = auUtf8Len(str, code);
      b += step;
      i += (step === 4 ? 2 : 1);
    }
    return i;
  }

  function auSplitLinesWithBytes(text, baseByte) {
    var src = String(text || "");
    var rawLines = src ? src.split("\n") : [];
    if (src.endsWith("\n")) rawLines.pop();
    var out = [], off = baseByte || 0;
    for (var i = 0; i < rawLines.length; i++) {
      var line = rawLines[i];
      var bytes = 0;
      for (var k = 0; k < line.length; k++) {
        var c = line.charCodeAt(k);
        var step = auUtf8Len(line, c);
        bytes += step;
        if (step === 4) k++;
      }
      out.push({ text: line.replace(/\r$/, ""), startByte: off, endByte: off + bytes });
      off += bytes + 1; // +1 for '\n'
    }
    return out;
  }

  function auRedChipHtml(rule, length, count) {
    var extra = count && count > 1 ? " \u00d7" + count : "";
    return '<span class="au-red" title="' + auEsc(wsT("js.audit.redacted.title", rule || "secret", length || 0)) + '">\ud83d\udd12 ' + auEsc(rule || "secret") + " \u00b7 " + auEsc(String(length || 0)) + "B" + auEsc(extra) + "</span>";
  }

  // The inspector's match marks are the server's spans (the event route's
  // "matches", see dashboard-audit.js): each is {line, start, end} with the
  // line's number in the server's numbering and start/end in UTF-16 code units
  // within that line, so this renderer only slices. It runs no matcher of its
  // own: RE2 syntax, rune width and the stored_trunc seam rule are the
  // server's.
  function auSpansByLine(spans) {
    var byLine = Object.create(null);
    for (var i = 0; i < (spans ? spans.length : 0); i++) {
      var sp = spans[i];
      if (!sp || typeof sp.line !== "number" || typeof sp.start !== "number" || typeof sp.end !== "number" || sp.end <= sp.start) continue;
      (byLine[sp.line] = byLine[sp.line] || []).push(sp);
    }
    return byLine;
  }

  // Applies highlight spans AND literal redacted markers onto a plain line.
  // lineSpans are the server's spans on this line, sorted, in UTF-16 units of
  // text. A span is drawn around redaction chips, never inside a mask; one
  // wholly inside a mask draws nothing and is not counted. Pieces of one span
  // share its hit index.
  function auRenderLineText(text, lineStartByte, lineRedactions, lineSpans, matchState) {
    // 1. Replace stored "***" redactions whose byte offset falls inside this line,
    // and any inline ⟦REDACTED:rule:len⟧ markers if present.
    var segments = [];
    if (lineRedactions && lineRedactions.length) {
      var sorted = lineRedactions.slice().sort(function (a, b) { return a.localChar - b.localChar; });
      var pos = 0;
      for (var i = 0; i < sorted.length; i++) {
        var r = sorted[i];
        if (r.localChar < pos || r.localChar >= text.length) continue;
        if (text.slice(r.localChar, r.localChar + 3) === "***") {
          if (r.localChar > pos) segments.push({ type: "text", value: text.slice(pos, r.localChar), off: pos });
          segments.push({ type: "red", rule: r.rule, length: r.length, count: r.count });
          pos = r.localChar + 3;
        }
      }
      if (pos < text.length) segments.push({ type: "text", value: text.slice(pos), off: pos });
    } else {
      segments.push({ type: "text", value: text, off: 0 });
    }
    // Expand inline ⟦REDACTED:kind:len⟧ tokens if emitted directly.
    var expanded = [];
    var litRe = /\u27e6REDACTED:([^:\u27e7]+):(\d+)\u27e7/g;
    for (var s = 0; s < segments.length; s++) {
      var seg = segments[s];
      if (seg.type !== "text") { expanded.push(seg); continue; }
      var last = 0, m;
      litRe.lastIndex = 0;
      while ((m = litRe.exec(seg.value)) !== null) {
        if (m.index > last) expanded.push({ type: "text", value: seg.value.slice(last, m.index), off: seg.off + last });
        expanded.push({ type: "red", rule: m[1], length: Number(m[2]), count: 1 });
        last = m.index + m[0].length;
      }
      if (last < seg.value.length) expanded.push({ type: "text", value: seg.value.slice(last), off: seg.off + last });
    }
    var html = "", hitIdx = [], first = 0;
    for (var e = 0; e < expanded.length; e++) {
      var part = expanded[e];
      if (part.type === "red") {
        html += auRedChipHtml(part.rule, part.length, part.count);
        continue;
      }
      var val = part.value, from = part.off, to = part.off + val.length, cursor = 0;
      if (lineSpans && matchState) {
        while (first < lineSpans.length && lineSpans[first].end <= from) first++;
        for (var k = first; k < lineSpans.length && lineSpans[k].start < to; k++) {
          var a = Math.max(lineSpans[k].start, from, from + cursor), b = Math.min(lineSpans[k].end, to);
          if (b <= a) continue;
          if (hitIdx[k] == null) hitIdx[k] = matchState.total++;
          var isCur = hitIdx[k] === matchState.activeIndex;
          html += auEsc(val.slice(cursor, a - from)) +
            '<mark class="au-hit' + (isCur ? " au-hit-cur" : "") + '" data-au-hit="' + hitIdx[k] + '">' + auEsc(val.slice(a - from, b - from)) + "</mark>";
          cursor = b - from;
        }
      }
      html += auEsc(val.slice(cursor));
    }
    return html;
  }

  function auBuildRedactionsIndex(ev, field, storedTrunc) {
    var all = Array.isArray(ev && ev.redactions) ? ev.redactions : [];
    var matching = [];
    for (var i = 0; i < all.length; i++) {
      if (all[i] && all[i].field === field) matching.push(all[i]);
    }
    var head = [], tail = [], omitted = [];
    if (!storedTrunc) {
      for (var j = 0; j < matching.length; j++) {
        if (matching[j].count > 1) omitted.push(matching[j]);
        else head.push({ byteOffset: matching[j].offset, rule: matching[j].rule, length: matching[j].length, count: matching[j].count });
      }
      return { head: head, tail: tail, omitted: omitted };
    }
    var orig = storedTrunc.original_bytes || 0;
    var hb = storedTrunc.head_bytes || 0;
    var tb = storedTrunc.tail_bytes || 0;
    var tailStartOrig = orig - tb;
    for (var k = 0; k < matching.length; k++) {
      var r = matching[k];
      if (r.count > 1) { omitted.push(r); continue; }
      if (r.offset < hb) {
        head.push({ byteOffset: r.offset, rule: r.rule, length: r.length, count: r.count });
      } else if (r.offset >= tailStartOrig) {
        tail.push({ byteOffset: r.offset - tailStartOrig, rule: r.rule, length: r.length, count: r.count });
      } else {
        omitted.push(r);
      }
    }
    return { head: head, tail: tail, omitted: omitted };
  }

  function auRenderLinesBlock(lines, startLineNo, redactionsList, seenLocalByte, wholeUnseen, spansByLine, matchState, gap) {
    var html = gap && gap.line === 0 ? gap.html : "", seenDrawn = false;
    for (var i = 0; i < lines.length; i++) {
      var ln = lines[i], no = startLineNo + i;
      if (!seenDrawn && seenLocalByte != null && seenLocalByte >= 0 && ln.startByte >= seenLocalByte) {
        html += '<div class="au-seencut">' + auEsc(wsT("js.audit.seen.cut")) + "</div>";
        seenDrawn = true;
      }
      var lineReds = [];
      for (var r = 0; r < redactionsList.length; r++) {
        var rd = redactionsList[r];
        if (rd.byteOffset >= ln.startByte && rd.byteOffset <= ln.endByte) {
          var charIdx = auUtf8ByteToCharIndex(ln.text, rd.byteOffset - ln.startByte);
          lineReds.push({ localChar: charIdx, rule: rd.rule, length: rd.length, count: rd.count });
        }
      }
      var unseen = wholeUnseen || (seenLocalByte != null && seenLocalByte >= 0 && ln.startByte >= seenLocalByte);
      var lineHtml = auRenderLineText(ln.text, ln.startByte, lineReds, spansByLine && spansByLine[no], matchState);
      html += '<div class="au-line' + (unseen ? " au-unseen" : "") + '" data-line="' + no + '"><span class="au-lnum">' + no + '</span><span class="au-ltxt">' + (lineHtml || " ") + "</span></div>";
      if (gap && gap.line === no) html += gap.html;
      if (!seenDrawn && seenLocalByte != null && seenLocalByte > ln.startByte && seenLocalByte <= ln.endByte) {
        html += '<div class="au-seencut">' + auEsc(wsT("js.audit.seen.cut")) + "</div>";
        seenDrawn = true;
      }
    }
    return html;
  }

  function auRenderBodyContent(ev, field, opts) {
    opts = opts || {};
    var body = typeof (ev && ev[field]) === "string" ? ev[field] : "";
    var storedTrunc = ev && ev.stored_trunc && ev.stored_trunc[field];
    var rangeInfo = ev && ev.truncated && ev.range && ev.range[field];
    var modelSeen = ev ? ev.model_seen !== false : true;
    var seenBound = field === "in" ? (ev && ev.seen && ev.seen.in) : field === "out" ? (ev && ev.seen && ev.seen.out) : null;
    if (seenBound != null && typeof seenBound !== "number") seenBound = null;

    if (!body && !storedTrunc && !rangeInfo) {
      return {
        html: '<div class="au-state"><strong>' + auEsc(wsT("js.audit.part.empty")) + "</strong></div>",
        matchCount: 0,
        plainText: ""
      };
    }

    var reds = auBuildRedactionsIndex(ev, field, storedTrunc);
    // The server's spans for this body (opts.matches[field]); none while they
    // are pending, refused or not asked for.
    var bodyMatches = opts.matches && opts.matches[field];
    var spansByLine = bodyMatches ? auSpansByLine(bodyMatches.spans) : null;
    var matchState = { total: 0, activeIndex: typeof opts.activeMatch === "number" ? opts.activeMatch : 0 };
    var outHtml = "";

    if (!modelSeen) {
      outHtml += '<div class="au-banner"><strong>' + auEsc(wsT("js.audit.unseen.title")) + "</strong> " + auEsc(wsT("js.audit.unseen.note")) + "</div>";
    }
    if (reds.omitted.length) {
      outHtml += '<div class="au-red-summary">';
      for (var ri = 0; ri < reds.omitted.length; ri++) {
        outHtml += auRedChipHtml(reds.omitted[ri].rule, reds.omitted[ri].length, reds.omitted[ri].count);
      }
      outHtml += "</div>";
    }

    outHtml += '<div class="au-code' + (opts.wrap ? " au-wrap" : "") + '">';
    if (storedTrunc && typeof storedTrunc.head_bytes === "number") {
      // Number the concatenation exactly as the server does, including a line
      // spanning the stored head/tail seam (the server's spans never cross
      // it). Keep the gap as a separate row.
      var lines = auSplitLinesWithBytes(body, 0);
      var hb = storedTrunc.head_bytes;
      var seam = auUtf8ByteToCharIndex(body, hb);
      var gapLine = hb === 0 ? 0 : Math.max(1, (body.slice(0, seam).match(/\n/g) || []).length + (body.charAt(seam - 1) === "\n" ? 0 : 1));
      var combinedReds = reds.head.concat(reds.tail.map(function (r) {
        return { byteOffset: hb + r.byteOffset, rule: r.rule, length: r.length, count: r.count };
      }));
      var omitted = Math.max(0, (storedTrunc.original_bytes || 0) - hb - (storedTrunc.tail_bytes || 0));
      var gap = '<div class="au-trunc-row"><span>' + auEsc(wsT("js.audit.stored.trunc", auFmtBytes(omitted))) + "</span>" + (modelSeen && seenBound === hb ? '<span class="au-seencut-badge">' + auEsc(wsT("js.audit.seen.cut")) + "</span>" : "") + "</div>";
      outHtml += auRenderLinesBlock(lines, 1, combinedReds, modelSeen ? seenBound : null, !modelSeen, spansByLine, matchState, { line: gapLine, html: gap });
      if (!lines.length && hb !== 0) outHtml += gap;
    } else {
      var lines = auSplitLinesWithBytes(body, 0);
      outHtml += auRenderLinesBlock(lines, 1, reds.head, modelSeen ? seenBound : null, !modelSeen, spansByLine, matchState);
    }
    outHtml += "</div>";

    if (rangeInfo && typeof rangeInfo.offset === "number") {
      var remaining = Math.max(0, (rangeInfo.total || 0) - rangeInfo.offset);
      outHtml += '<button type="button" class="au-loadmore" data-au-more="' + auEsc(field) + '" data-au-offset="' + auEsc(String(rangeInfo.offset)) + '">' +
        auEsc(wsT("js.audit.loadmore", auFmtBytes(remaining))) + "</button>";
    }

    return { html: outHtml, matchCount: matchState.total, matchCapped: !!(bodyMatches && bodyMatches.capped), plainText: body };
  }

  function auEnvelopeHasError(ev) {
    return !!(ev && ((ev.meta && (ev.meta.is_error || ev.meta.error)) || ev.subkind === "error"));
  }

  function auRenderMetaGrid(win) {
    var ev = win.activeEnvelope;
    var resEv = !win.inspectEventId ? win.resultEnvelope : null;
    var summary = win.inspectEventId ? win.activeSummary : win.callSummary;
    var rows = [];
    var push = function (k, v) {
      if (v == null || v === "") return;
      rows.push([k, String(v)]);
    };
    if (ev) {
      push(wsT("js.audit.meta.kind"), ev.kind + (ev.subkind ? " (" + ev.subkind + ")" : ""));
      push(wsT("js.audit.meta.id"), ev.id);
      push(wsT("js.audit.meta.call"), ev.call_id);
      push(wsT("js.audit.meta.harness"), ev.harness);
      push(wsT("js.audit.meta.at"), ev.at);
      push(wsT("js.audit.meta.model_seen"), ev.model_seen ? wsT("js.audit.meta.yes") : wsT("js.audit.meta.no"));
    }
    if (summary) {
      if (summary.duration_ms != null) push(wsT("js.audit.meta.duration"), summary.duration_ms + " ms");
      if (summary.request_id) push(wsT("js.audit.meta.request"), summary.request_id);
      if (summary.ctx_before != null || summary.ctx_after != null) {
        push(wsT("js.audit.meta.ctx"), (summary.ctx_before != null ? summary.ctx_before : "\u2014") + " \u2192 " + (summary.ctx_after != null ? summary.ctx_after : "\u2014"));
      }
    }
    var mergedMeta = Object.create(null);
    if (ev && ev.meta) {
      Object.keys(ev.meta).forEach(function (k) { mergedMeta[k] = ev.meta[k]; });
    }
    if (resEv && resEv.meta) {
      Object.keys(resEv.meta).forEach(function (k) {
        if (!(k in mergedMeta) || ((k === "is_error" || k === "error") && resEv.meta[k])) {
          mergedMeta[k] = resEv.meta[k];
        }
      });
    }
    if (mergedMeta.signature_only != null) push(wsT("js.audit.meta.sigonly"), String(mergedMeta.signature_only));
    var keys = Object.keys(mergedMeta).sort();
    for (var i = 0; i < keys.length; i++) {
      var k = keys[i];
      if (k === "signature_only") continue;
      var val = mergedMeta[k];
      push("meta." + k, typeof val === "object" ? JSON.stringify(val) : String(val));
    }
    if (ev || resEv) {
      var inBytes = ev ? (ev.in_bytes || 0) : 0;
      var outBytes = resEv ? (resEv.out_bytes || 0) : (ev ? (ev.out_bytes || 0) : 0);
      var rawBytes = ev ? (ev.raw_bytes || 0) : 0;
      push(wsT("js.audit.meta.bytes"), "in " + inBytes + " \u00b7 out " + outBytes + " \u00b7 raw " + rawBytes);
    }
    if (!rows.length) return '<div class="au-state">' + auEsc(wsT("js.audit.part.empty")) + "</div>";
    return '<div class="au-meta">' + rows.map(function (r) {
      return '<div class="au-kv"><span class="au-k">' + auEsc(r[0]) + '</span><span class="au-v">' + auEsc(r[1]) + "</span></div>";
    }).join("") + "</div>";
  }

  function auRenderRawTab(ev, opts) {
    if (!ev) return { html: '<div class="au-state">' + auEsc(wsT("js.audit.part.empty")) + "</div>", matchCount: 0, plainText: "" };
    if (ev.kind === "other") {
      var banner = '<div class="au-banner"><strong>' + auEsc(wsT("js.audit.raw.other.title")) + "</strong> " + auEsc(wsT("js.audit.raw.other.sub")) + "</div>";
      var res = auRenderBodyContent(ev, "raw", opts);
      return { html: banner + res.html, matchCount: res.matchCount, plainText: res.plainText };
    }
    var clone = Object.assign({}, ev);
    delete clone.raw;
    var normJson = JSON.stringify(clone, null, 2);
    var synthetic = { in: normJson, model_seen: true };
    var normBanner = '<div class="au-banner"><strong>' + auEsc(wsT("js.audit.raw.normalized.title")) + "</strong> " + auEsc(wsT("js.audit.raw.normalized.sub")) + "</div>";
    // The normalized view is built here, not stored: the server has no text to
    // match, so it carries no marks.
    var bodyRes = auRenderBodyContent(synthetic, "in", Object.assign({}, opts, { matches: null }));
    return { html: normBanner + bodyRes.html, matchCount: bodyRes.matchCount, plainText: normJson };
  }

  function auRenderLinkedEventsTab(win) {
    var evs = Array.isArray(win.linkedEvents) ? win.linkedEvents : [];
    if (!evs.length) {
      return '<div class="au-state"><strong>' + auEsc(wsT("js.audit.events.empty")) + "</strong></div>";
    }
    return '<ul class="au-evlist">' + evs.map(function (ev) {
      var title = ev.title || ev.subkind || ev.kind || ev.id;
      var meta = (ev.in_bytes || ev.out_bytes) ? auFmtBytes((ev.in_bytes || 0) + (ev.out_bytes || 0)) : "";
      return '<li><button type="button" class="au-evrow" data-au-ev="' + auEsc(ev.id) + '">' +
        '<span class="au-evkind">' + auEsc(ev.kind + (ev.subkind ? ":" + ev.subkind : "")) + "</span>" +
        '<span class="au-evtitle" title="' + auEsc(title) + '">' + auEsc(title) + "</span>" +
        (meta ? '<span class="au-evmeta">' + auEsc(meta) + "</span>" : "") +
        "</button></li>";
    }).join("") + "</ul>";
  }

  function auRenderWindowError(errState) {
    if (!errState) return "";
    if (errState.status === 410) {
      return '<div class="au-state"><strong>' + auEsc(wsT("js.audit.err.stale.title")) + "</strong><span>" + auEsc(wsT("js.audit.err.stale.sub")) + '</span><button type="button" class="au-retry-btn" data-au-restart>' + auEsc(wsT("js.audit.err.stale.action")) + "</button></div>";
    }
    if (errState.status === 404) {
      return '<div class="au-state"><strong>' + auEsc(wsT("js.audit.err.gone.title")) + "</strong><span>" + auEsc(wsT("js.audit.err.gone.sub")) + "</span></div>";
    }
    if (errState.status === 503) {
      return '<div class="au-state"><strong>' + auEsc(wsT("js.audit.err.unavail.title")) + "</strong><span>" + auEsc(wsT("js.audit.err.unavail.sub")) + '</span><button type="button" class="au-retry-btn" data-au-retry>' + auEsc(wsT("js.audit.err.retry")) + "</button></div>";
    }
    return '<div class="au-state"><strong>' + auEsc(wsT("js.audit.err.generic.title")) + "</strong><span>" + auEsc(errState.message || wsT("js.audit.err.generic.sub")) + '</span><button type="button" class="au-retry-btn" data-au-retry>' + auEsc(wsT("js.audit.err.retry")) + "</button></div>";
  }

  function auBuildWindowDOM(win) {
    var el = doc.createElement("section");
    el.className = "au-win";
    el.setAttribute("role", "dialog");
    el.setAttribute("aria-modal", "false");
    el.tabIndex = -1;
    el.innerHTML =
      '<header class="au-whd">' +
        '<span class="au-hbadge"></span>' +
        '<div class="au-wtitle"><span class="au-wname"></span><span class="au-wstatus" hidden></span></div>' +
        '<div class="au-wacts">' +
          '<button type="button" class="au-wbtn au-prev" aria-label="' + auEsc(wsT("js.audit.nav.prev")) + '" title="' + auEsc(wsT("js.audit.nav.prev")) + '">\u2190</button>' +
          '<button type="button" class="au-wbtn au-next" aria-label="' + auEsc(wsT("js.audit.nav.next")) + '" title="' + auEsc(wsT("js.audit.nav.next")) + '">\u2192</button>' +
          '<button type="button" class="au-wbtn au-pin" aria-pressed="false" aria-label="' + auEsc(wsT("js.audit.pin")) + '" title="' + auEsc(wsT("js.audit.pin")) + '">\ud83d\udccc</button>' +
          '<button type="button" class="au-wbtn au-close" aria-label="' + auEsc(wsT("js.audit.close")) + '" title="' + auEsc(wsT("js.audit.close")) + '">\u2715</button>' +
        "</div>" +
      "</header>" +
      '<div class="au-crumb" hidden><button type="button" class="au-crumb-back">' + auEsc(wsT("js.audit.back")) + '</button><span class="au-crumb-label"></span></div>' +
      '<nav class="au-tabs" role="tablist">' +
        '<button type="button" class="au-tab" role="tab" data-au-tab="in" aria-selected="true">' + auEsc(wsT("js.audit.tab.in")) + "</button>" +
        '<button type="button" class="au-tab" role="tab" data-au-tab="out" aria-selected="false">' + auEsc(wsT("js.audit.tab.out")) + "</button>" +
        '<button type="button" class="au-tab" role="tab" data-au-tab="meta" aria-selected="false">' + auEsc(wsT("js.audit.tab.meta")) + "</button>" +
        '<button type="button" class="au-tab" role="tab" data-au-tab="raw" aria-selected="false">' + auEsc(wsT("js.audit.tab.raw")) + "</button>" +
        '<button type="button" class="au-tab" role="tab" data-au-tab="events" aria-selected="false">' + auEsc(wsT("js.audit.tab.events")) + '<span class="au-tab-cnt" hidden>0</span></button>' +
      "</nav>" +
      '<div class="au-wbody">' +
        '<div class="au-tbar">' +
          '<div class="au-sbox"><input type="search" class="au-sinp" placeholder="' + auEsc(wsT("js.audit.search.placeholder")) + '" aria-label="' + auEsc(wsT("js.audit.search.aria")) + '"><span class="au-scnt"></span></div>' +
          '<button type="button" class="au-wbtn au-sprev" title="' + auEsc(wsT("js.audit.search.prev")) + '">\u2191</button>' +
          '<button type="button" class="au-wbtn au-snext" title="' + auEsc(wsT("js.audit.search.next")) + '">\u2193</button>' +
          '<span class="au-tbar-sp"></span>' +
          '<button type="button" class="au-wbtn au-wrap-btn" aria-pressed="false">' + auEsc(wsT("js.audit.wrap")) + "</button>" +
          '<button type="button" class="au-wbtn au-copy-btn">' + auEsc(wsT("js.audit.copy")) + "</button>" +
        "</div>" +
        '<div class="au-panel" role="tabpanel"></div>' +
      "</div>" +
      '<div class="au-resize" aria-hidden="true"></div>';
    win.el = el;
    return el;
  }
