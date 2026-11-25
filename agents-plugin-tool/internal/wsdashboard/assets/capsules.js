/* Atomic client-local references over an existing textarea's plain-string contract. */
(function () {
  "use strict";
  var doc = document, topicPickers = [], rawPasteArmed = false, rawPasteActive = false;
  var kinds = { skill: true, topic: true, answer: true, "topic-note": true, mention: true, ticket: true, pick: true, console: true, panel: true, selection: true, mailbox: true };
  function rawPasteBypass() { return rawPasteActive || rawPasteArmed; }
  function isRawPasteChord(e) {
    return !!(e && (e.ctrlKey || e.metaKey) && e.shiftKey && !e.altKey && (e.key === "v" || e.key === "V" || e.code === "KeyV"));
  }
  function isRawPasteRelease(e) {
    return !!(e && (e.key === "v" || e.key === "V" || e.code === "KeyV" || e.key === "Shift" || e.key === "Control" || e.key === "Meta"));
  }
  function unescapeMailboxXml(s, allowQuot) {
    if (!s || s.indexOf("&") < 0) return s;
    return s.replace(allowQuot ? /&(amp|lt|gt|quot);/g : /&(amp|lt|gt);/g, function (m, name) {
      return name === "amp" ? "&" : name === "lt" ? "<" : name === "gt" ? ">" : '"';
    });
  }
  function skipTextref(s, start) {
    if (!s || s.slice(start, start + 14) !== '<textref id="#') return -1;
    var m = /^<textref id="#([1-9][0-9]*)" bytes="([0-9]+)">/.exec(s.slice(start));
    if (!m) return -1;
    var targetBytes = Number(m[2]);
    if (!Number.isFinite(targetBytes) || targetBytes < 0) return -1;
    var pos = start + m[0].length, bytes = 0;
    while (bytes < targetBytes && pos < s.length) {
      var code = s.charCodeAt(pos);
      if (code <= 0x7f) { bytes += 1; pos += 1; }
      else if (code <= 0x7ff) { bytes += 2; pos += 1; }
      else if (code >= 0xd800 && code <= 0xdbff && pos + 1 < s.length && s.charCodeAt(pos + 1) >= 0xdc00 && s.charCodeAt(pos + 1) <= 0xdfff) { bytes += 4; pos += 2; }
      else { bytes += 3; pos += 1; }
    }
    if (bytes !== targetBytes || s.slice(pos, pos + 10) !== "</textref>") return -1;
    return pos + 10;
  }
  function parseMailboxTag(s, start) {
    start = start || 0;
    if (!s || s.slice(start, start + 9) !== "<mailbox ") return null;
    var p = start + 9, seen = {}, tag = { id: "", dir: "", root: "", branch: "", harness: "", label: "" };
    while (p < s.length) {
      while (p < s.length && s.charAt(p) === " ") p++;
      if (p >= s.length) return null;
      if (s.slice(p, p + 2) === "/>") {
        if (!tag.id) return null;
        return { start: start, end: p + 2, raw: s.slice(start, p + 2), tag: tag };
      }
      if (s.charAt(p) === ">") {
        if (!tag.id) return null;
        var bodyStart = p + 1, closeAt = s.indexOf("</mailbox>", bodyStart);
        if (closeAt < 0) return null;
        var rawLabel = s.slice(bodyStart, closeAt);
        if (/[<>\r\n]/.test(rawLabel)) return null;
        tag.label = unescapeMailboxXml(rawLabel, false);
        var end = closeAt + 10;
        return { start: start, end: end, raw: s.slice(start, end), tag: tag };
      }
      var nm = /^[a-z][a-z0-9_-]*/.exec(s.slice(p));
      if (!nm) return null;
      var name = nm[0];
      p += name.length;
      if (seen[name] || s.slice(p, p + 2) !== '="') return null;
      seen[name] = true;
      p += 2;
      var valStart = p;
      while (p < s.length && s.charAt(p) !== '"') {
        var ch = s.charAt(p);
        if (ch === "<" || ch === ">" || ch === "\r" || ch === "\n") return null;
        p++;
      }
      if (p >= s.length || s.charAt(p) !== '"') return null;
      var val = unescapeMailboxXml(s.slice(valStart, p), true);
      p++;
      if (name === "id" || name === "dir" || name === "root" || name === "branch" || name === "harness") tag[name] = val;
      if (p < s.length && s.charAt(p) !== " " && s.charAt(p) !== ">" && s.slice(p, p + 2) !== "/>") return null;
    }
    return null;
  }
  function scanMailboxTags(s) {
    var out = [];
    if (!s || s.indexOf("<mailbox ") < 0) return out;
    for (var i = 0; i < s.length;) {
      if (s.charAt(i) === "<") {
        if (s.slice(i, i + 9) === "<textref ") {
          var tend = skipTextref(s, i);
          if (tend > i) { i = tend; continue; }
        }
        if (s.slice(i, i + 9) === "<mailbox ") {
          var m = parseMailboxTag(s, i);
          if (m) { out.push(m); i = m.end; continue; }
        }
      }
      i++;
    }
    return out;
  }
  function normalizeMailboxDir(dir) {
    var s = String(dir || "").replace(/\\/g, "/");
    while (s.length > 1 && s.charAt(s.length - 1) === "/") {
      if (s.length === 3 && /^[A-Za-z]:\/$/.test(s)) break;
      s = s.slice(0, -1);
    }
    if (/^[A-Za-z]:/.test(s)) s = s.toLowerCase();
    return s;
  }
  function mailboxDirBase(dir) {
    var s = String(dir || "").replace(/\\/g, "/");
    while (s.length > 1 && s.charAt(s.length - 1) === "/") s = s.slice(0, -1);
    var idx = s.lastIndexOf("/");
    return idx >= 0 && idx + 1 < s.length ? s.slice(idx + 1) : s;
  }
  function shortMailboxId(id) {
    id = String(id || "");
    var hasPrefix = id.indexOf("id:") === 0;
    var rest = hasPrefix ? id.slice(3) : id;
    var runes = Array.from(rest);
    var short = runes.length > 6 ? runes.slice(0, 6).join("") : rest;
    return hasPrefix ? "id:" + short : short;
  }
  function formatMailboxLabel(tag, ctx) {
    ctx = ctx || {};
    var sameDir = !tag.dir || (!!ctx.dir && normalizeMailboxDir(tag.dir) === normalizeMailboxDir(ctx.dir));
    var sameBranch = (tag.branch || "") === (ctx.branch || "");
    var prefix = "";
    if (!sameDir) {
      var base = mailboxDirBase(tag.dir);
      prefix = tag.branch ? base + ":" + tag.branch : base;
    } else if (!sameBranch && tag.branch) {
      prefix = tag.branch;
    }
    if (prefix && tag.label) return prefix + " · " + tag.label;
    if (prefix) return prefix;
    if (tag.label) return tag.label;
    return shortMailboxId(tag.id);
  }
  function formatMailboxTooltip(tag) {
    var lines = ["id: " + (tag.id || "")];
    if (tag.dir) lines.push("dir: " + tag.dir);
    if (tag.root) lines.push("root: " + tag.root);
    if (tag.branch) lines.push("branch: " + tag.branch);
    if (tag.harness) lines.push("harness: " + tag.harness);
    return lines.join("\n");
  }
  function viewingContext(root) {
    root = root || doc;
    var btn = root && root.querySelector ? root.querySelector("#r-head .snm[data-mailbox-open], .snm[data-mailbox-open]") : null;
    var open = btn && btn.getAttribute("data-mailbox-open");
    if (!open) return { dir: "", branch: "" };
    var parsed = parseMailboxTag(open + "/>", 0);
    return parsed ? { dir: parsed.tag.dir, branch: parsed.tag.branch } : { dir: "", branch: "" };
  }
  if (doc.addEventListener) doc.addEventListener("selectionchange", function () {
    var editor = doc.activeElement;
    if (editor && editor.wscField && editor.wscField.wsc) editor.wscField.wsc.rememberCaret();
  });
  function sweep() {
    topicPickers = topicPickers.filter(function (p) {
      if (p.field.isConnected !== false) return true;
      p.popup.destroy(); return false;
    });
  }
  function token(value, caret, sigil) {
    if (!/^\s*$/.test(value.slice(caret))) return null;
    var before = value.slice(0, caret), re = new RegExp("(^|\\s)\\" + sigil + "([^\\s" + sigil + "]*)$"), m = re.exec(before);
    return m ? { start: m.index + m[1].length, end: caret, q: m[2] } : null;
  }
  // Stable subsequence ranking: exact, prefix, contiguous, then compact fuzzy matches.
  function rank(rows, query, label) {
    // Composed and decomposed Hangul (NFC/NFD) must match each other.
    var needle = query.normalize("NFC").toLocaleLowerCase();
    return rows.map(function (row, index) {
      var text = label(row).normalize("NFC").toLocaleLowerCase(), at = -1, first = -1, gaps = 0;
      for (var i = 0; i < needle.length; i++) {
        var next = text.indexOf(needle.charAt(i), at + 1);
        if (next < 0) return null;
        if (first < 0) first = next;
        if (at >= 0) gaps += next - at - 1;
        at = next;
      }
      var score = !needle ? 0 : text === needle ? -3000 : text.indexOf(needle) === 0 ? -2000 : text.indexOf(needle) >= 0 ? -1000 + text.indexOf(needle) : gaps * 10 + first;
      return { row: row, index: index, score: score };
    }).filter(Boolean).sort(function (a, b) { return a.score - b.score || a.index - b.index; }).map(function (r) { return r.row; });
  }
  function valid(text, ranges) {
    var end = 0;
    return (Array.isArray(ranges) ? ranges : []).filter(function (r) {
      if (!r || !Object.prototype.hasOwnProperty.call(kinds, r.kind) || typeof r.id !== "string" || !r.id || typeof r.label !== "string" ||
          !Number.isInteger(r.start) || !Number.isInteger(r.end) || r.start < end || r.end <= r.start || r.end > text.length ||
          text.slice(r.start, r.end) !== marker(r) ||
          (r.kind !== "skill" && r.kind !== "topic" && (typeof r.key !== "string" || !r.key || !r.payload || typeof r.payload !== "object" || typeof r.payload.anchor !== "string"))) return false;
      end = r.end; return true;
    }).map(function (r) {
      var copy = { start: r.start, end: r.end, kind: r.kind, id: r.id, label: r.label };
      if (r.kind !== "skill" && r.kind !== "topic") { copy.key = r.key; copy.payload = Object.assign({}, r.payload); }
      return copy;
    });
  }
  function marker(r) { return r.kind === "skill" ? r.id : r.kind === "topic" ? "[" + r.id + "]" : r.payload && r.payload.anchor; }
  function shifted(was, text, ranges) {
    var a = 0, s = 0;
    while (a < was.length && a < text.length && was.charAt(a) === text.charAt(a)) a++;
    while (s < was.length - a && s < text.length - a && was.charAt(was.length - s - 1) === text.charAt(text.length - s - 1)) s++;
    return valid(text, ranges.map(function (r) {
      r = Object.assign({}, r);
      if (was.length - s <= r.start) { r.start += text.length - was.length; r.end += text.length - was.length; }
      else if (a < r.end) return null;
      return r;
    }));
  }
  function stripMarkers(text, ranges, lost) {
    ranges = valid(text, ranges);
    var cuts = [], re = /\[Image #([1-9][0-9]{0,5})\] ?/g, m;
    while ((m = re.exec(text))) if (lost.indexOf(Number(m[1])) >= 0) cuts.push([m.index, m.index + m[0].length]);
    cuts.reverse().forEach(function (cut) {
      ranges = ranges.filter(function (r) { return r.end <= cut[0] || r.start >= cut[1]; }).map(function (r) {
        r = Object.assign({}, r);
        if (r.start >= cut[1]) { r.start -= cut[1] - cut[0]; r.end -= cut[1] - cut[0]; }
        return r;
      });
      text = text.slice(0, cut[0]) + text.slice(cut[1]);
    });
    return { text: text, ranges: ranges };
  }
  function command(field) {
    var r = field && field.wsc && field.wsc.snapshot()[0];
    return r && r.kind === "skill" && r.start === 0 ? { name: r.id, end: r.end } : null;
  }
  function focused(field) { return doc.activeElement === field || !!(field && field.wsc && doc.activeElement === field.wsc.editor); }
  function fieldFor(node) { return node && node.closest && node.closest(".wsc-editor") ? node.closest(".wsc-editor").wscField : node; }
  function attach(field) {
    if (!field || field.wsc) return field && field.wsc;
    var editor = doc.createElement("div"), text = field.value, ranges = [], selection = [0, 0], seenCaret = false, composing = false, nextKey = 0;
    var nativeValue = Object.getOwnPropertyDescriptor(Object.getPrototypeOf(field), "value");
    // Board snapshots clone DOM, not expando properties or event handlers.
    if (field.nextElementSibling && field.nextElementSibling.classList.contains("wsc-editor")) field.nextElementSibling.remove();
    function cache() { if (nativeValue && nativeValue.set) nativeValue.set.call(field, text); }
    editor.className = "wsc-editor";
    editor.setAttribute("contenteditable", "true");
    editor.setAttribute("role", "textbox"); editor.setAttribute("aria-multiline", "true");
    editor.wscField = field;
    field.classList.add("wsc-backing"); field.insertAdjacentElement("afterend", editor);
    function attrs() {
      ["aria-label", "aria-description", "aria-controls", "aria-expanded", "aria-activedescendant", "aria-autocomplete"].forEach(function (key) {
        var value = field.getAttribute(key);
        if (value === null) editor.removeAttribute(key); else editor.setAttribute(key, value);
      });
      editor.setAttribute("data-placeholder", field.getAttribute("placeholder") || "");
      editor.setAttribute("contenteditable", field.disabled ? "false" : "true");
      editor.setAttribute("aria-disabled", String(!!field.disabled));
      editor.hidden = field.hidden;
    }
    new MutationObserver(attrs).observe(field, { attributes: true }); attrs();
    // Reading the editable DOM emits canonical text, never the displayed label.
    // Block boundaries also handle native IME/undo mutations without rewriting them.
    function isMeaningful(node) { return node.nodeType === 1 || (node.nodeType === 3 && node.nodeValue.length > 0); }
    function prevMeaningful(node) { for (var s = node.previousSibling; s; s = s.previousSibling) if (isMeaningful(s)) return s; return null; }
    function nextMeaningful(node) { for (var s = node.nextSibling; s; s = s.nextSibling) if (isMeaningful(s)) return s; return null; }
    function read(root, endpoint) {
      var out = "", marks = [], units = [], position = 0;
      function walk(n) {
        var start = out.length;
        if (n.nodeType === 3) {
          out += n.nodeValue;
          if (endpoint && endpoint.node === n) position = start + Math.min(endpoint.offset, n.nodeValue.length);
          units.push({ node: n, start: start, end: out.length }); return;
        }
        if (n.nodeType !== 1 && n !== root) return;
        if (n.nodeType === 1 && n.hasAttribute("data-wsc-kind")) {
          var kind = n.getAttribute("data-wsc-kind"), id = n.getAttribute("data-wsc-id"), label = n.getAttribute("data-wsc-label");
          var mark = { start: start, kind: kind, id: id, label: label };
          if (kind !== "skill" && kind !== "topic") {
            mark.key = n.getAttribute("data-wsc-key");
            try { mark.payload = JSON.parse(n.getAttribute("data-wsc-payload")); } catch (_) { mark.payload = null; }
          }
          out += marker(mark) || ""; mark.end = out.length; marks.push(mark);
          if (endpoint && n.contains(endpoint.node)) position = endpoint.offset ? out.length : start;
          units.push({ node: n, start: start, end: out.length, atomic: true }); return;
        }
        if (n.nodeName === "BR") {
          // A trailing BR (with no meaningful following sibling) is the native caret filler, not another blank line.
          if (!nextMeaningful(n) && (n.parentNode === root || /^(DIV|P)$/.test(n.parentNode.nodeName))) return;
          out += "\n"; units.push({ node: n, start: start, end: out.length, atomic: true }); return;
        }
        var block = n !== root && /^(DIV|P)$/.test(n.nodeName);
        var prev = block ? prevMeaningful(n) : null;
        if (block && prev && (/^(DIV|P)$/.test(prev.nodeName) || (out && out.slice(-1) !== "\n"))) {
          var boundary = out.length; out += "\n";
          units.push({ node: n, start: boundary, end: out.length, atomic: true, boundary: true });
        }
        Array.prototype.forEach.call(n.childNodes, function (child, index) {
          if (endpoint && endpoint.node === n && endpoint.offset === index) position = out.length;
          walk(child);
        });
        if (endpoint && endpoint.node === n && endpoint.offset === n.childNodes.length) position = out.length;
        var next = block ? nextMeaningful(n) : null;
        if (block && next && !/^(DIV|P)$/.test(next.nodeName) && out.slice(-1) !== "\n") out += "\n";
      }
      walk(root); return { text: out, ranges: valid(out, marks), units: units, position: position };
    }
    function offsets() {
      var sel = window.getSelection();
      if (!sel || !sel.rangeCount || !editor.contains(sel.anchorNode) || !editor.contains(sel.focusNode)) return selection;
      seenCaret = true;
      var r = sel.getRangeAt(0);
      function offset(node, at) { return read(editor, { node: node, offset: at }).position; }
      selection = [offset(r.startContainer, r.startOffset), offset(r.endContainer, r.endOffset)];
      return selection;
    }
    function point(at) {
      var units = read(editor).units;
      for (var i = 0; i < units.length; i++) {
        var u = units[i];
        if (at > u.end) continue;
        if (!u.atomic) return [u.node, Math.max(0, at - u.start)];
        if (u.boundary && at > u.start) return [u.node, 0];
        var index = Array.prototype.indexOf.call(u.node.parentNode.childNodes, u.node);
        return [u.node.parentNode, index + (at > u.start ? 1 : 0)];
      }
      return [editor, editor.childNodes.length];
    }
    function select(a, b) {
      selection = [Math.max(0, Math.min(text.length, a)), Math.max(0, Math.min(text.length, b))];
      if (!focused(field)) return;
      var start = point(selection[0]), end = point(selection[1]), r = doc.createRange(), sel = window.getSelection();
      r.setStart(start[0], start[1]); r.setEnd(end[0], end[1]); sel.removeAllRanges(); sel.addRange(r);
    }
    function capsule(kind, id, label, key, payload) {
      var chip = doc.createElement("span"); chip.className = "wsc-capsule wsc-" + kind;
      chip.setAttribute("contenteditable", "false");
      chip.setAttribute("data-wsc-kind", kind); chip.setAttribute("data-wsc-id", id); chip.setAttribute("data-wsc-label", label);
      if (kind !== "skill" && kind !== "topic") {
        chip.setAttribute("data-wsc-key", key); chip.setAttribute("data-wsc-payload", JSON.stringify(payload));
        if (payload && typeof payload.title === "string" && payload.title) chip.setAttribute("title", payload.title);
      }
      chip.textContent = (kind === "skill" ? "/" : kind === "topic" ? "#" : "") + label;
      return chip;
    }
    function render() {
      cache(); editor.textContent = ""; var at = 0;
      ranges.forEach(function (r) {
        editor.appendChild(doc.createTextNode(text.slice(at, r.start)));
        editor.appendChild(capsule(r.kind, r.id, r.label, r.key, r.payload)); at = r.end;
      });
      var tail = text.slice(at);
      if (tail || ranges.length) editor.appendChild(doc.createTextNode(tail));
      select(selection[0], selection[1]);
    }
    function changed(before, action) {
      var after = valid(text, ranges), left = before.slice(), added = [], removed = [], updated = [];
      after.forEach(function (r) {
        var index = left.findIndex(function (old) { return old.kind === r.kind && (r.key ? old.key === r.key : old.id === r.id); });
        if (index < 0) { added.push(r); return; }
        var old = left.splice(index, 1)[0];
        if (old.label !== r.label || JSON.stringify(old.payload) !== JSON.stringify(r.payload)) updated.push({ before: old, after: r });
      });
      removed = left;
      if (added.length || removed.length || updated.length) {
        var detail = { action: action || (removed.length ? "remove" : updated.length ? "update" : "insert"), added: added, removed: removed, updated: updated, capsules: after };
        var event = typeof CustomEvent === "function" ? new CustomEvent("wsc:change", { bubbles: true, detail: detail }) : new Event("wsc:change", { bubbles: true });
        if (!event.detail) Object.defineProperty(event, "detail", { value: detail });
        field.dispatchEvent(event);
      }
    }
    function notify(before, action) {
      cache();
      var event = new Event("input", { bubbles: true });
      Object.defineProperty(event, "isComposing", { value: composing });
      field.dispatchEvent(event);
      if (before) changed(before, action);
    }
    function spliceText(s, a, b, mode) {
      // A partial range through a capsule edits the whole capsule.
      ranges.forEach(function (r) { if (a < r.end && b > r.start) { a = Math.min(a, r.start); b = Math.max(b, r.end); } });
      var next = text.slice(0, a) + s + text.slice(b);
      ranges = ranges.filter(function (r) { return r.end <= a || r.start >= b; }).map(function (r) {
        r = Object.assign({}, r); if (r.start >= b) { r.start += s.length - (b - a); r.end += s.length - (b - a); } return r;
      });
      text = next; selection = mode === "select" ? [a, a + s.length] : [a + s.length, a + s.length];
    }
    function replace(s, a, b, mode) { spliceText(s, a, b, mode); render(); }
    function expanded(a, b) {
      ranges.forEach(function (r) {
        if (a === b && a > r.start && a < r.end) { a = r.end; b = r.end; }
        else if (a < r.end && b > r.start) { a = Math.min(a, r.start); b = Math.max(b, r.end); }
      });
      return [a, b];
    }
    function movedSelection(saved, a, b, length) {
      return saved.map(function (pos) {
        if (pos <= a) return pos;
        if (pos >= b) return pos + length - (b - a);
        return a + length;
      });
    }
    function insertPlain(value) {
      var sel = offsets(), before = valid(text, ranges);
      if (!doc.execCommand("insertText", false, value)) { replace(value, sel[0], sel[1]); notify(before); }
    }
    Object.defineProperties(field, {
      value: { configurable: true, get: function () { return text; }, set: function (v) { var before = valid(text, ranges); v = String(v); ranges = shifted(text, v, ranges); text = v; render(); changed(before); } },
      selectionStart: { configurable: true, get: function () { return offsets()[0]; } },
      selectionEnd: { configurable: true, get: function () { return offsets()[1]; } }
    });
    field.setSelectionRange = function (a, b) { seenCaret = true; select(a, b); };
    field.setRangeText = function (s, a, b, mode) { var before = valid(text, ranges); replace(s, a, b, mode); changed(before); };
    field.focus = function () { attrs(); if (!field.hidden && !field.disabled) { if (doc.activeElement === editor) offsets(); seenCaret = true; editor.focus(); select(selection[0], selection[1]); } };
    field.blur = function () { editor.blur(); };
    field.select = function () { field.focus(); select(0, text.length); };
    editor.addEventListener("beforeinput", function (e) {
      e.stopPropagation();
      if (e.defaultPrevented) return;
      var forward = new Event("beforeinput", { bubbles: true, cancelable: true });
      Object.defineProperty(forward, "inputType", { value: e.inputType });
      Object.defineProperty(forward, "data", { value: e.data });
      Object.defineProperty(forward, "isComposing", { value: composing || !!e.isComposing });
      if (!field.dispatchEvent(forward)) e.preventDefault();
    });
    editor.addEventListener("input", function (e) {
      e.stopPropagation(); var before = valid(text, ranges), state = read(editor); text = state.text; ranges = state.ranges;
      var meaningful = Array.prototype.filter.call(editor.childNodes, isMeaningful);
      if (meaningful.length === 1 && meaningful[0].nodeName === "BR") text = "";
      offsets(); notify(before);
    });
    editor.addEventListener("compositionstart", function () { composing = true; field.dispatchEvent(new Event("compositionstart")); });
    // compositionupdate fires before the DOM shows the new text, and an IME
    // may send no input event: read the live text after it, then forward.
    editor.addEventListener("compositionupdate", function () {
      setTimeout(function () {
        if (!composing) return;
        var state = read(editor); text = state.text; ranges = state.ranges; offsets();
        field.dispatchEvent(new Event("compositionupdate"));
      }, 0);
    });
    editor.addEventListener("compositionend", function () { composing = false; field.dispatchEvent(new Event("compositionend")); });
    ["keydown", "keyup"].forEach(function (type) {
      editor.addEventListener(type, function (e) {
        e.stopPropagation();
        if (e.defaultPrevented) return;
        if (type === "keydown") {
          if (!composing && !e.isComposing && e.keyCode !== 229) {
            if (isRawPasteChord(e)) rawPasteArmed = true;
            else if (e.key !== "Shift" && e.key !== "Control" && e.key !== "Meta") rawPasteArmed = false;
          }
        } else if (rawPasteArmed && isRawPasteRelease(e)) {
          rawPasteArmed = false;
        }
        var forwarded = new KeyboardEvent(type, { key: e.key, code: e.code, keyCode: e.keyCode, repeat: e.repeat, shiftKey: e.shiftKey, ctrlKey: e.ctrlKey, metaKey: e.metaKey, altKey: e.altKey, isComposing: composing || e.isComposing, bubbles: true, cancelable: true });
        if (!field.dispatchEvent(forwarded)) { e.preventDefault(); return; }
        if (type !== "keydown" || composing || e.isComposing || e.keyCode === 229) return;
        var sel = offsets(), a = sel[0], b = sel[1];
        if (e.key === "Backspace" || e.key === "Delete") {
          var target = ranges.filter(function (r) { return a === b && (e.key === "Backspace" ? r.end === a : r.start === a) || a < r.end && b > r.start; });
          if (target.length) {
            e.preventDefault(); target.forEach(function (r) { a = Math.min(a, r.start); b = Math.max(b, r.end); });
            select(a, b);
            var before = valid(text, ranges);
            if (!doc.execCommand("delete", false)) { replace("", a, b); notify(before, "remove"); }
          }
        } else if (e.key === "Enter") {
          e.preventDefault(); insertPlain("\n");
        }
      });
    });
    ["focus", "blur", "click", "mouseup", "scroll"].forEach(function (type) { editor.addEventListener(type, function () { if (type === "focus") seenCaret = true; if (type === "blur") { rawPasteArmed = false; rawPasteActive = false; } offsets(); field.dispatchEvent(new Event(type)); }); });
    editor.addEventListener("paste", function (e) {
      e.stopPropagation();
      var bypass = rawPasteArmed;
      rawPasteArmed = false;
      rawPasteActive = bypass;
      try {
        var forward = new Event("paste", { bubbles: true, cancelable: true });
        Object.defineProperty(forward, "clipboardData", { value: e.clipboardData }); field.dispatchEvent(forward);
        e.preventDefault();
        if (forward.defaultPrevented || !e.clipboardData) return;
        var rich = null;
        if (!bypass) {
          try { rich = JSON.parse(e.clipboardData.getData("application/x-wsc-capsules+json")); } catch (_) { rich = null; }
        }
        if (rich && typeof rich.text === "string" && Array.isArray(rich.ranges) && valid(rich.text, rich.ranges).length === rich.ranges.length) {
          var sel = expanded.apply(null, offsets()), before = valid(text, ranges), start = sel[0];
          replace(rich.text, sel[0], sel[1]);
          ranges = valid(text, ranges.concat(rich.ranges.map(function (r) { return Object.assign({}, r, { start: r.start + start, end: r.end + start }); })).sort(function (a, b) { return a.start - b.start; }));
          render(); notify(before, "paste");
        } else {
          var plain = e.clipboardData.getData("text/plain");
          var found = !bypass && plain && plain.indexOf("<mailbox ") >= 0 ? scanMailboxTags(plain) : [];
          if (found.length) {
            var mSel = expanded.apply(null, offsets()), mBefore = valid(text, ranges), mStart = mSel[0];
            var ctx = viewingContext(doc);
            var added = found.map(function (m) {
              var mKey;
              do { mKey = "mailbox:" + (++nextKey); } while (ranges.some(function (r) { return r.kind === "mailbox" && r.key === mKey; }));
              return {
                start: mStart + m.start,
                end: mStart + m.end,
                kind: "mailbox",
                id: mKey,
                key: mKey,
                label: formatMailboxLabel(m.tag, ctx),
                payload: { anchor: m.raw, title: formatMailboxTooltip(m.tag), mailbox: m.tag }
              };
            });
            spliceText(plain, mSel[0], mSel[1]);
            ranges = valid(text, ranges.concat(added).sort(function (a, b) { return a.start - b.start; }));
            render(); notify(mBefore, "paste");
          } else {
            insertPlain(plain);
          }
        }
      } finally {
        rawPasteActive = false;
      }
    });
    // Rich HTML drops must not manufacture capsule identity or execute markup.
    editor.addEventListener("drop", function (e) {
      if (e.dataTransfer && Array.prototype.indexOf.call(e.dataTransfer.types || [], "Files") >= 0) return;
      e.preventDefault(); if (e.dataTransfer) insertPlain(e.dataTransfer.getData("text/plain"));
    });
    editor.addEventListener("copy", copy); editor.addEventListener("cut", copy);
    function copy(e) {
      var sel = offsets(); if (!e.clipboardData || sel[0] === sel[1]) return;
      var a = sel[0], b = sel[1], selected = ranges.filter(function (r) { return a < r.end && b > r.start; });
      selected.forEach(function (r) { a = Math.min(a, r.start); b = Math.max(b, r.end); });
      var raw = text.slice(a, b), local = selected.map(function (r) { return Object.assign({}, r, { start: r.start - a, end: r.end - a }); });
      var plain = raw;
      selected.slice().reverse().forEach(function (r) {
        if (r.payload && typeof r.payload.plain === "string") plain = plain.slice(0, r.start - a) + r.payload.plain + plain.slice(r.end - a);
      });
      e.preventDefault(); e.clipboardData.setData("text/plain", plain);
      if (selected.length) e.clipboardData.setData("application/x-wsc-capsules+json", JSON.stringify({ text: raw, ranges: local }));
      if (e.type === "cut") { var before = valid(text, ranges); replace("", a, b); notify(before, "remove"); }
    }
    function insertGeneric(kind, payload, options) {
      options = options || {};
      if (kind === "skill" || kind === "topic" || !Object.prototype.hasOwnProperty.call(kinds, kind) || !payload || typeof payload.anchor !== "string" || !payload.anchor) return null;
      var key = options.key || payload.key;
      if (!key) do { key = kind + ":" + (++nextKey); } while (ranges.some(function (r) { return r.kind === kind && r.key === key; }));
      key = String(key);
      var label = typeof payload.label === "string" ? payload.label : payload.anchor;
      var saved = options.at, at = saved && typeof saved.start === "number" ? [saved.start, saved.end] : typeof saved === "number" ? [saved, saved] : focused(field) ? offsets() : seenCaret ? selection.slice() : [text.length, text.length];
      at = [Math.max(0, Math.min(text.length, at[0])), Math.max(0, Math.min(text.length, at[1]))];
      at = expanded(at[0], at[1]);
      var before = valid(text, ranges), prefix = at[0] && !/\s/.test(text.charAt(at[0] - 1)) ? " " : "";
      var suffix = !/\s/.test(text.charAt(at[1])) ? " " : "";
      var mark = { start: at[0] + prefix.length, end: at[0] + prefix.length + payload.anchor.length, kind: kind, id: key, key: key, label: label, payload: Object.assign({}, payload, { anchor: payload.anchor }) };
      var kept = options.preserveCaret ? (focused(field) ? offsets().slice() : selection.slice()) : null;
      if (!options.preserveCaret) field.focus();
      replace(prefix + payload.anchor + suffix, at[0], at[1]);
      ranges = valid(text, ranges.concat(mark).sort(function (a, b) { return a.start - b.start; }));
      render(); if (kept) { var moved = movedSelection(kept, at[0], at[1], prefix.length + payload.anchor.length + suffix.length); select(moved[0], moved[1]); }
      notify(before, "insert");
      return mark;
    }
    var api = field.wsc = {
      editor: editor, snapshot: function () { return valid(text, ranges); },
      rawPaste: function () { return rawPasteActive || rawPasteArmed; },
      rememberCaret: offsets,
      ordered: function () { return valid(text, ranges); },
      restore: function (saved) { var before = valid(text, ranges); ranges = valid(text, saved); render(); changed(before, "restore"); },
      insertCapsule: insertGeneric,
      upsert: function (kind, key, payload) {
        key = String(key); var old = ranges.find(function (r) { return r.kind === kind && r.key === key; });
        if (!old) return insertGeneric(kind, payload, { key: key, at: text.length, preserveCaret: true });
        if (!payload || typeof payload.anchor !== "string" || !payload.anchor) return null;
        var before = valid(text, ranges), label = typeof payload.label === "string" ? payload.label : payload.anchor, mark;
        if (marker(old) === payload.anchor && old.label === label && JSON.stringify(old.payload) === JSON.stringify(payload)) return old;
        if (marker(old) === payload.anchor) {
          mark = { start: old.start, end: old.end, kind: kind, id: key, key: key, label: label, payload: Object.assign({}, payload) };
          ranges = valid(text, ranges.map(function (r) { return r === old ? mark : r; }));
          var matches = Array.prototype.filter.call(editor.querySelectorAll(".wsc-capsule"), function (node) {
            return node.getAttribute("data-wsc-kind") === kind && node.getAttribute("data-wsc-key") === key;
          });
          if (matches.length === 1) {
            var chip = matches[0];
            chip.setAttribute("data-wsc-payload", JSON.stringify(mark.payload));
            if (mark.payload && typeof mark.payload.title === "string" && mark.payload.title) chip.setAttribute("title", mark.payload.title);
            else chip.removeAttribute("title");
            if (old.label !== label || chip.getAttribute("data-wsc-label") !== label) {
              chip.setAttribute("data-wsc-label", label);
              chip.textContent = (kind === "skill" ? "/" : kind === "topic" ? "#" : "") + label;
            }
            changed(before, "update");
            return mark;
          }
          selection = focused(field) ? offsets().slice() : selection.slice();
          render();
          changed(before, "update");
          return mark;
        }
        var kept = focused(field) ? offsets().slice() : selection.slice();
        spliceText(payload.anchor, old.start, old.end);
        mark = { start: old.start, end: old.start + payload.anchor.length, kind: kind, id: key, key: key, label: label, payload: Object.assign({}, payload) };
        ranges = valid(text, ranges.concat(mark).sort(function (a, b) { return a.start - b.start; }));
        selection = movedSelection(kept, old.start, old.end, payload.anchor.length);
        render();
        notify(before, "update"); return mark;
      },
      remove: function (kind, key) {
        var before = valid(text, ranges), removed = before.filter(function (r) { return r.kind === kind && r.key === String(key); });
        removed.slice().reverse().forEach(function (r) { replace("", r.start, r.end + (text.charAt(r.end) === " " ? 1 : 0)); });
        if (removed.length) notify(before, "remove");
        return removed;
      },
      insert: function (kind, id, label, at) {
        var before = valid(text, ranges), value = kind === "skill" ? id : "[" + id + "]", tail = /\s/.test(text.charAt(at.end)) ? "" : " ";
        field.focus(); select(at.start, at.end);
        // A browser editing transaction keeps capsule insertion/deletion undoable.
        if (doc.execCommand("insertHTML", false, capsule(kind, id, label).outerHTML + tail)) return;
        replace(value + tail, at.start, at.end);
        ranges.push({ start: at.start, end: at.start + value.length, kind: kind, id: id, label: label });
        ranges.sort(function (a, b) { return a.start - b.start; }); render(); notify(before, "insert");
      },
      anchor: function (at) { var p = point(at), r = doc.createRange(); r.setStart(p[0], p[1]); r.collapse(true); var box = r.getBoundingClientRect(); return box.height ? { x: box.left, top: box.top, bottom: box.bottom } : { x: editor.getBoundingClientRect().left, top: editor.getBoundingClientRect().top, bottom: editor.getBoundingClientRect().bottom }; }
    };
    render(); return api;
  }
  function topicPicker(field, source) {
    if (!field || !window.wsMention) return null;
    attach(field);
    var popup = window.wsMention.attach(field, {
      label: wsT("js.capsule.topics"), token: function (v, c) { return token(v, c, "#"); },
      source: function (q, done) { source(function (rows) { done(rank(rows, q, function (r) { return r.label; })); }); },
      render: function (li, row) {
        var name = doc.createElement("span"), stem = doc.createElement("span");
        name.className = "wsm-name"; name.textContent = row.label; stem.className = "wsm-desc"; stem.textContent = row.id;
        li.appendChild(name); li.appendChild(stem);
      }, choose: function (row, at) { field.wsc.insert("topic", row.id, row.label, at); }
    });
    topicPickers.push({ field: field, popup: popup });
    field.addEventListener("keydown", function (e) { if (!e.defaultPrevented && !e.isComposing && e.keyCode !== 229 && popup.key(e)) e.stopImmediatePropagation(); });
    return popup;
  }
  function rawPaste(field) { return field ? !!(field.wsc && rawPasteBypass()) : rawPasteBypass(); }
  window.WsCapsules = {
    attach: attach, topicPicker: topicPicker, sweep: sweep, token: token, rank: rank,
    valid: valid, shifted: shifted, stripMarkers: stripMarkers, command: command,
    focused: focused, fieldFor: fieldFor, rawPasteBypass: rawPasteBypass, rawPaste: rawPaste,
    parseMailboxTag: parseMailboxTag, scanMailboxTags: scanMailboxTags, skipTextref: skipTextref,
    formatMailboxLabel: formatMailboxLabel, formatMailboxTooltip: formatMailboxTooltip, viewingContext: viewingContext
  };
})();
