// Composer long-paste text attachments (<textref>) and preview builders.
  // --- composer text attachments -------------------------------------------
  // A plain-text paste of >= TEXTREF_MIN characters collapses into an inline
  // "[Text #n]" marker at the caret plus a 56x56 .txtchip tile in #cimgs.
  // Ctrl+Shift+V / Cmd+Shift+V bypasses collapse via capsules.js's single
  // raw-paste flag accessor. On send, active text attachments serialize inline
  // as UTF-8 byte-framed <textref id="#n" bytes="N">...</textref> blocks with
  // zero schema changes (strictly T0).
  var textRefs = [], txtSeq = 0, txtTrash = {};
  var TEXTREF_MIN = 1024, TEXTREF_MAX_BYTES = 64 * 1024;
  var TEXT_MARKER = "\\[Text #([1-9][0-9]{0,5})\\]";
  var _utf8Enc = typeof TextEncoder === "function" ? new TextEncoder() : null;
  var _utf8Dec = typeof TextDecoder === "function" ? new TextDecoder("utf-8", { fatal: false }) : null;
  var rawPasteFallback = false;

  function encodeUTF8(s) {
    s = String(s || "");
    if (_utf8Enc) return _utf8Enc.encode(s);
    var out = [], i, c, c2;
    for (i = 0; i < s.length; i++) {
      c = s.charCodeAt(i);
      if (c < 0x80) out.push(c);
      else if (c < 0x800) out.push(0xc0 | (c >> 6), 0x80 | (c & 0x3f));
      else if (c >= 0xd800 && c <= 0xdbff && i + 1 < s.length) {
        c2 = s.charCodeAt(++i);
        c = 0x10000 + ((c - 0xd800) << 10) + (c2 - 0xdc00);
        out.push(0xf0 | (c >> 18), 0x80 | ((c >> 12) & 0x3f), 0x80 | ((c >> 6) & 0x3f), 0x80 | (c & 0x3f));
      } else out.push(0xe0 | (c >> 12), 0x80 | ((c >> 6) & 0x3f), 0x80 | (c & 0x3f));
    }
    return new Uint8Array(out);
  }
  function decodeUTF8(bytes, start, end) {
    var slice = bytes.subarray(start, end);
    if (_utf8Dec) return _utf8Dec.decode(slice);
    var out = "", i = 0, b0, b1, b2, b3, cp;
    while (i < slice.length) {
      b0 = slice[i++];
      if (b0 < 0x80) out += String.fromCharCode(b0);
      else if ((b0 & 0xe0) === 0xc0 && i < slice.length) {
        b1 = slice[i++];
        out += String.fromCharCode(((b0 & 0x1f) << 6) | (b1 & 0x3f));
      } else if ((b0 & 0xf0) === 0xe0 && i + 1 < slice.length) {
        b1 = slice[i++]; b2 = slice[i++];
        out += String.fromCharCode(((b0 & 0x0f) << 12) | ((b1 & 0x3f) << 6) | (b2 & 0x3f));
      } else if ((b0 & 0xf8) === 0xf0 && i + 2 < slice.length) {
        b1 = slice[i++]; b2 = slice[i++]; b3 = slice[i++];
        cp = (((b0 & 0x07) << 18) | ((b1 & 0x3f) << 12) | ((b2 & 0x3f) << 6) | (b3 & 0x3f)) - 0x10000;
        out += String.fromCharCode(0xd800 + (cp >> 10), 0xdc00 + (cp & 0x3ff));
      } else out += "\ufffd";
    }
    return out;
  }
  function utf8ByteLen(s) { return encodeUTF8(s).length; }
  function formatTextRefBytes(n) {
    if (n < 1024) return n + " B";
    if (n < 1024 * 1024) return (n / 1024).toFixed(1) + " KiB";
    return (n / (1024 * 1024)).toFixed(1) + " MiB";
  }
  function countTextRefLines(body) { return body ? String(body).split("\n").length : 0; }
  function textMarker(n) { return "[Text #" + n + "]"; }
  function textRefBlock(n, body) {
    body = String(body || "");
    return '<textref id="#' + n + '" bytes="' + utf8ByteLen(body) + '">' + body + "</textref>";
  }
  // Byte-framed parser matching wsinbox.ParseTextRefs: slices exact UTF-8 byte
  // lengths so embedded "</textref>" stays literal and malformed blocks fall
  // back to plain text.
  function parseTextRefs(text) {
    text = String(text || "");
    if (!text || text.indexOf("<textref ") < 0) return text ? [{ text: text }] : [];
    var raw = encodeUTF8(text), openSig = encodeUTF8("<textref "), closeSig = encodeUTF8("</textref>");
    var parts = [], plainStart = 0, pos = 0;
    function matchAt(at, sig) {
      if (at + sig.length > raw.length) return false;
      for (var k = 0; k < sig.length; k++) if (raw[at + k] !== sig[k]) return false;
      return true;
    }
    function findSig(from, sig) {
      var lim = raw.length - sig.length;
      for (var at = from; at <= lim; at++) if (matchAt(at, sig)) return at;
      return -1;
    }
    while (pos < raw.length) {
      var openIdx = findSig(pos, openSig);
      if (openIdx < 0) break;
      var gt = -1, scanLim = Math.min(raw.length, openIdx + 64);
      for (var j = openIdx + openSig.length; j < scanLim; j++) {
        if (raw[j] === 62) { gt = j; break; }
      }
      if (gt < 0) { pos = openIdx + 1; continue; }
      var header = decodeUTF8(raw, openIdx, gt + 1);
      var m = /^<textref id="#([1-9][0-9]{0,5})" bytes="(0|[1-9][0-9]{0,8})">$/.exec(header);
      if (!m) { pos = openIdx + 1; continue; }
      var num = +m[1], byteLen = +m[2], bodyStart = gt + 1, bodyEnd = bodyStart + byteLen;
      if (bodyEnd + closeSig.length > raw.length || !matchAt(bodyEnd, closeSig)) {
        pos = openIdx + 1;
        continue;
      }
      if (openIdx > plainStart) parts.push({ text: decodeUTF8(raw, plainStart, openIdx) });
      var body = decodeUTF8(raw, bodyStart, bodyEnd);
      parts.push({ ref: { n: num, body: body, bytes: byteLen, lines: countTextRefLines(body) } });
      pos = bodyEnd + closeSig.length;
      plainStart = pos;
    }
    if (plainStart < raw.length) parts.push({ text: decodeUTF8(raw, plainStart, raw.length) });
    return parts;
  }
  function stripTextRefs(text) {
    text = String(text || "");
    if (!text || text.indexOf("<textref ") < 0) return { text: text, refs: [] };
    var parts = parseTextRefs(text), refs = [], kept = "";
    parts.forEach(function (p) {
      if (p.ref) { refs.push(p.ref); return; }
      var seg = p.text;
      if (kept && kept.charAt(kept.length - 1) === "\n") seg = seg.replace(/^\r?\n+/, "");
      kept += seg;
    });
    if (!refs.length) return { text: text, refs: [] };
    return { text: kept.replace(/^(?:\r?\n)+|(?:\r?\n)+$/g, ""), refs: refs };
  }
  function serializeTextRefs(text, list) {
    var active = (list || []).slice().sort(function (a, b) { return a.n - b.n; });
    if (!active.length) return text || "";
    var blocks = active.map(function (r) { return textRefBlock(r.n, r.body); }).join("\n\n");
    return text ? text + "\n\n" + blocks : blocks;
  }
  function checkTextRefCap(text, mentionList, closeList) {
    var bytes = utf8ByteLen(String(text || "").trim());
    (mentionList || []).forEach(function (m) { bytes += utf8ByteLen(String(m && m.text || "").trim()); });
    (closeList || []).forEach(function (r) { bytes += utf8ByteLen(String(r && r.reason || "").trim()); });
    return { bytes: bytes, over: bytes > TEXTREF_MAX_BYTES };
  }
  function textMarkerNums(text) {
    var out = {}, re = new RegExp(TEXT_MARKER, "g"), m;
    while ((m = re.exec(String(text || "")))) out[+m[1]] = true;
    return out;
  }
  function allTextNums() { return textMarkerNums(ctext ? ctext.value : ""); }
  function nextTextNum() {
    Object.keys(allTextNums()).forEach(function (k) { txtSeq = Math.max(txtSeq, +k); });
    textRefs.forEach(function (r) { txtSeq = Math.max(txtSeq, r.n); });
    Object.keys(txtTrash).forEach(function (k) { txtSeq = Math.max(txtSeq, +k); });
    return ++txtSeq;
  }
  function insertTextMarker(n, el) {
    el = el || ctext;
    if (!el) return;
    var v = el.value, a = el.selectionStart, b = el.selectionEnd, s = textMarker(n);
    if (a > 0 && !/\s/.test(v.charAt(a - 1))) s = " " + s;
    if (b >= v.length || !/\s/.test(v.charAt(b))) s += " ";
    editField(el, a, b, s);
  }
  function deleteTextMarkers(n) {
    if (!ctext) return;
    var re = new RegExp("\\[Text #" + n + "\\] ?", "g"), v = ctext.value, spans = [], m;
    while ((m = re.exec(v))) spans.push([m.index, m.index + m[0].length]);
    spans.reverse().forEach(function (sp) { editField(ctext, sp[0], sp[1], ""); });
  }
  function dropTextRef(it) {
    var i = textRefs.indexOf(it);
    if (i >= 0) textRefs.splice(i, 1);
  }
  function attachTextRef(body, el) {
    body = String(body || "");
    var bytes = utf8ByteLen(body), lines = countTextRefLines(body);
    var it = { n: nextTextNum(), body: body, bytes: bytes, lines: lines };
    textRefs.push(it);
    renderImgs();
    insertTextMarker(it.n, el || ctext);
    queueDraft();
    if (bytes > TEXTREF_MAX_BYTES) {
      noteWarn(wsT("js.textref.overcap.paste", formatTextRefBytes(bytes), formatTextRefBytes(TEXTREF_MAX_BYTES)));
    }
    return it;
  }
  function unattachTextRef(n) {
    var it = textRefs.filter(function (r) { return r.n === n; })[0];
    if (!it) return;
    dropTextRef(it);
    txtTrash[it.n] = it;
    renderImgs();
    deleteTextMarkers(it.n);
    queueDraft();
  }
  function clearTextTrash() { txtTrash = {}; }
  function syncTextMarkers() {
    if (holdSync || (!textRefs.length && !Object.keys(txtTrash).length)) return;
    var have = allTextNums(), changed = false;
    textRefs.slice().forEach(function (it) {
      if (!have[it.n]) { dropTextRef(it); txtTrash[it.n] = it; changed = true; }
    });
    Object.keys(txtTrash).forEach(function (k) {
      var it = txtTrash[k];
      if (have[it.n]) { delete txtTrash[k]; textRefs.push(it); changed = true; }
    });
    if (!changed) return;
    textRefs.sort(function (a, b) { return a.n - b.n; });
    renderImgs();
  }
  function makeFileTextIcon() {
    var ns = "http://www.w3.org/2000/svg";
    if (typeof doc.createElementNS === "function") {
      var svg = doc.createElementNS(ns, "svg"), use = doc.createElementNS(ns, "use");
      svg.setAttribute("class", "li");
      svg.setAttribute("aria-hidden", "true");
      use.setAttribute("href", "#l-file-text");
      svg.appendChild(use);
      return svg;
    }
    var sp = doc.createElement("span");
    sp.className = "li";
    sp.setAttribute("aria-hidden", "true");
    return sp;
  }
  function makeTextRefPreviewTemplate(ref) {
    var bytes = typeof ref.bytes === "number" ? ref.bytes : utf8ByteLen(ref.body);
    var lines = typeof ref.lines === "number" ? ref.lines : countTextRefLines(ref.body);
    var tpl = doc.createElement("template");
    tpl.className = "icapsule-source";
    var rootNode = tpl.content || tpl;
    var hd = doc.createElement("div"), title = doc.createElement("b"), meta = doc.createElement("span"), pre = doc.createElement("pre");
    hd.className = "icapsule-pop-hd";
    title.className = "icapsule-pop-title";
    title.textContent = wsT("js.textref.pop.title", ref.n);
    meta.className = "icapsule-pop-meta-dim";
    meta.textContent = wsT("js.textref.pop.meta", formatTextRefBytes(bytes), lines);
    hd.appendChild(title);
    hd.appendChild(meta);
    pre.className = "icapsule-pop-textref";
    pre.textContent = String(ref.body || "");
    rootNode.appendChild(hd);
    rootNode.appendChild(pre);
    return tpl;
  }
  function makeImgPreviewTemplate(it) {
    var tpl = doc.createElement("template");
    tpl.className = "icapsule-source";
    var rootNode = tpl.content || tpl;
    var hd = doc.createElement("div"), title = doc.createElement("b"), wrap = doc.createElement("div"), im = doc.createElement("img");
    hd.className = "icapsule-pop-hd";
    title.className = "icapsule-pop-title";
    title.textContent = wsT("js.img.title", it.n, it.name);
    hd.appendChild(title);
    wrap.className = "icapsule-pop-imgwrap";
    im.className = "icapsule-pop-img";
    im.src = it.url;
    im.alt = it.name;
    wrap.appendChild(im);
    rootNode.appendChild(hd);
    rootNode.appendChild(wrap);
    return tpl;
  }
  function makeTextChip(it) {
    var bytes = typeof it.bytes === "number" ? it.bytes : utf8ByteLen(it.body);
    var lines = typeof it.lines === "number" ? it.lines : countTextRefLines(it.body);
    var el = doc.createElement("span"), ico = doc.createElement("span"), num = doc.createElement("span"), x = doc.createElement("button");
    el.className = "txtchip";
    el.setAttribute("data-txtchip", String(it.n));
    el.setAttribute("data-icapsule", "1");
    el.setAttribute("data-icapsule-click", "1");
    el.setAttribute("role", "button");
    el.setAttribute("tabindex", "0");
    el.setAttribute("aria-haspopup", "dialog");
    el.setAttribute("aria-expanded", "false");
    el.title = wsT("js.textref.title", it.n, formatTextRefBytes(bytes), lines);
    ico.className = "txtchip-ico";
    ico.appendChild(makeFileTextIcon());
    el.appendChild(ico);
    num.className = "inum";
    num.textContent = "#" + it.n;
    el.appendChild(num);
    x.type = "button";
    x.className = "untxt";
    x.setAttribute("data-untxt", String(it.n));
    x.title = wsT("js.textref.remove");
    x.textContent = "×";
    el.appendChild(x);
    el.appendChild(makeTextRefPreviewTemplate(it));
    return el;
  }
  function makeTextRefThumb(ref) {
    var bytes = typeof ref.bytes === "number" ? ref.bytes : utf8ByteLen(ref.body);
    var lines = typeof ref.lines === "number" ? ref.lines : countTextRefLines(ref.body);
    var el = doc.createElement("span"), ico = doc.createElement("span"), num = doc.createElement("span");
    el.className = "textref-thumb";
    el.setAttribute("data-textref", String(ref.n));
    el.setAttribute("data-icapsule", "1");
    el.setAttribute("data-icapsule-click", "1");
    el.setAttribute("role", "button");
    el.setAttribute("tabindex", "0");
    el.setAttribute("aria-haspopup", "dialog");
    el.setAttribute("aria-expanded", "false");
    el.title = wsT("js.textref.title", ref.n, formatTextRefBytes(bytes), lines);
    ico.className = "textref-ico";
    ico.appendChild(makeFileTextIcon());
    el.appendChild(ico);
    num.className = "tnum";
    num.textContent = "#" + ref.n;
    el.appendChild(num);
    el.appendChild(makeTextRefPreviewTemplate(ref));
    return el;
  }
  function makeTextTag(ref) {
    var bytes = typeof ref.bytes === "number" ? ref.bytes : utf8ByteLen(ref.body);
    var lines = typeof ref.lines === "number" ? ref.lines : countTextRefLines(ref.body);
    var el = doc.createElement("span"), lbl = doc.createElement("span");
    el.className = "txttag";
    el.setAttribute("data-icapsule", "1");
    el.setAttribute("data-icapsule-click", "1");
    el.setAttribute("role", "button");
    el.setAttribute("tabindex", "0");
    el.setAttribute("aria-haspopup", "dialog");
    el.setAttribute("aria-expanded", "false");
    el.title = wsT("js.textref.title", ref.n, formatTextRefBytes(bytes), lines);
    el.appendChild(makeFileTextIcon());
    lbl.textContent = "Text #" + ref.n;
    el.appendChild(lbl);
    el.appendChild(makeTextRefPreviewTemplate(ref));
    return el;
  }
  function isRawPaste(el) {
    if (el && el.wsc && window.WsCapsules) {
      if (typeof window.WsCapsules.rawPasteBypass === "function") return window.WsCapsules.rawPasteBypass();
      if (typeof window.WsCapsules.rawPaste === "function") return window.WsCapsules.rawPaste(el);
    }
    return rawPasteFallback;
  }
  function pasteTextRef(e, el) {
    if (e.defaultPrevented || !e.clipboardData) return;
    var raw = isRawPaste(el);
    rawPasteFallback = false;
    if (raw) return;
    var rich = e.clipboardData.getData("application/x-wsc-capsules+json");
    if (rich) return;
    var plain = e.clipboardData.getData("text/plain");
    if (typeof plain !== "string" || plain.length < TEXTREF_MIN) return;
    e.preventDefault();
    attachTextRef(plain, el);
  }
  if (ctext && cimgs) {
    ctext.addEventListener("keydown", function (e) {
      if (ctext.wsc) return;
      if (e.isComposing || e.keyCode === 229) return;
      if ((e.ctrlKey || e.metaKey) && e.shiftKey && !e.altKey && (e.code === "KeyV" || String(e.key || "").toLowerCase() === "v")) rawPasteFallback = true;
      else if (e.key !== "Shift" && e.key !== "Control" && e.key !== "Meta") rawPasteFallback = false;
    });
    ctext.addEventListener("keyup", function (e) {
      if (ctext.wsc) return;
      if (!e.shiftKey || !(e.ctrlKey || e.metaKey) || e.code === "KeyV" || String(e.key || "").toLowerCase() === "v") rawPasteFallback = false;
    });
    ctext.addEventListener("blur", function () { rawPasteFallback = false; });
    ctext.addEventListener("paste", function (e) { pasteTextRef(e, ctext); });
  }
