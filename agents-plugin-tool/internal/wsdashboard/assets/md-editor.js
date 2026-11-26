/* Element-scoped live hybrid WYSIWYG Markdown renderer for contenteditable editors.
 * Exposes window.WsMdEditor.attach(editorEl, opts) -> { reconcile(), schedule(), detach() }.
 * Uses strictly <span> wrappers (never DIV/P) to preserve WsCapsules' canonical text/offset
 * contract, guards IME composition, suppresses formatting in bang mode, and owns a per-instance
 * managed undo/redo stack covering both text edits and atomic capsules. */
(function () {
  "use strict";

  var HISTORY_LIMIT = 150;
  var COALESCE_WINDOW_MS = 600;

  function capsuleMarker(r) {
    if (!r) return "";
    if (r.kind === "skill") return r.id || "";
    if (r.kind === "topic") return "[" + (r.id || "") + "]";
    return r.payload && typeof r.payload.anchor === "string" ? r.payload.anchor : "";
  }

  function cloneCapsules(list) {
    if (!Array.isArray(list) || !list.length) return [];
    return list.map(function (r) {
      var copy = { start: r.start, end: r.end, kind: r.kind, id: r.id, label: r.label };
      if (r.kind !== "skill" && r.kind !== "topic") {
        copy.key = r.key;
        copy.payload = r.payload ? Object.assign({}, r.payload) : null;
      }
      return copy;
    });
  }

  function serializeCapsules(list) {
    if (!Array.isArray(list) || !list.length) return "";
    var out = [];
    for (var i = 0; i < list.length; i++) {
      var r = list[i];
      out.push(r.start + ":" + r.end + ":" + r.kind + ":" + (r.key || r.id) + ":" + (r.label || ""));
    }
    return out.join("|");
  }

  function serializeMentions(list) {
    if (!Array.isArray(list) || !list.length) return "";
    var out = [];
    for (var i = 0; i < list.length; i++) {
      var m = list[i];
      if (m && Number.isInteger(m.start) && Number.isInteger(m.end)) {
        out.push(m.start + ":" + m.end + ":" + (m.text || ""));
      }
    }
    return out.join("|");
  }

  function isMeaningful(node) {
    return node.nodeType === 1 || (node.nodeType === 3 && node.nodeValue.length > 0);
  }

  function prevMeaningful(node) {
    for (var s = node.previousSibling; s; s = s.previousSibling) if (isMeaningful(s)) return s;
    return null;
  }

  function nextMeaningful(node) {
    for (var s = node.nextSibling; s; s = s.nextSibling) if (isMeaningful(s)) return s;
    return null;
  }

  // Canonical DOM reader matching capsules.js read() rules, also collecting existing capsule nodes.
  function readEditorDom(root, endpoint) {
    var out = "", capsules = [], capsuleNodes = [], units = [], position = 0;
    function walk(n) {
      var start = out.length;
      if (n.nodeType === 3) {
        var v = n.nodeValue || "";
        out += v;
        if (endpoint && endpoint.node === n) position = start + Math.min(endpoint.offset, v.length);
        units.push({ node: n, start: start, end: out.length });
        return;
      }
      if (n.nodeType !== 1 && n !== root) return;
      if (n.nodeType === 1 && n.hasAttribute && n.hasAttribute("data-wsc-kind")) {
        var kind = n.getAttribute("data-wsc-kind");
        var id = n.getAttribute("data-wsc-id") || "";
        var label = n.getAttribute("data-wsc-label") || "";
        var mark = { start: start, kind: kind, id: id, label: label };
        if (kind !== "skill" && kind !== "topic") {
          mark.key = n.getAttribute("data-wsc-key") || "";
          try { mark.payload = JSON.parse(n.getAttribute("data-wsc-payload") || "null"); } catch (_) { mark.payload = null; }
        }
        var mstr = capsuleMarker(mark);
        out += mstr;
        mark.end = out.length;
        capsules.push(mark);
        capsuleNodes.push({ range: mark, node: n });
        if (endpoint && n.contains && n.contains(endpoint.node)) position = endpoint.offset ? out.length : start;
        units.push({ node: n, start: start, end: out.length, atomic: true });
        return;
      }
      if (n.nodeName === "BR") {
        if (!nextMeaningful(n) && (n.parentNode === root || /^(DIV|P)$/.test(n.parentNode.nodeName))) return;
        out += "\n";
        units.push({ node: n, start: start, end: out.length, atomic: true });
        return;
      }
      var block = n !== root && /^(DIV|P)$/.test(n.nodeName);
      var prev = block ? prevMeaningful(n) : null;
      if (block && prev && (/^(DIV|P)$/.test(prev.nodeName) || (out && out.slice(-1) !== "\n"))) {
        var boundary = out.length;
        out += "\n";
        units.push({ node: n, start: boundary, end: out.length, atomic: true, boundary: true });
      }
      var kids = n.childNodes || [];
      for (var i = 0; i < kids.length; i++) {
        if (endpoint && endpoint.node === n && endpoint.offset === i) position = out.length;
        walk(kids[i]);
      }
      if (endpoint && endpoint.node === n && endpoint.offset === kids.length) position = out.length;
      var next = block ? nextMeaningful(n) : null;
      if (block && next && !/^(DIV|P)$/.test(next.nodeName) && out.slice(-1) !== "\n") out += "\n";
    }
    if (root) walk(root);
    return { text: out, capsules: capsules, capsuleNodes: capsuleNodes, units: units, position: position };
  }

  function pointInDom(root, at) {
    var units = readEditorDom(root).units;
    for (var i = 0; i < units.length; i++) {
      var u = units[i];
      if (at > u.end) continue;
      if (!u.atomic) return [u.node, Math.max(0, at - u.start)];
      if (u.boundary && at > u.start) return [u.node, 0];
      var parent = u.node.parentNode;
      if (!parent) break;
      var idx = Array.prototype.indexOf.call(parent.childNodes, u.node);
      return [parent, idx + (at > u.start ? 1 : 0)];
    }
    var count = root.childNodes ? root.childNodes.length : 0;
    // If the last child is our trailing caret-filler <br>, place caret right before it.
    if (count > 0 && root.childNodes[count - 1].nodeName === "BR") return [root, count - 1];
    return [root, count];
  }

  function collectCapsuleNodes(root) {
    var list = [];
    if (!root || !root.querySelectorAll) return list;
    var els = root.querySelectorAll("[data-wsc-kind]");
    for (var i = 0; i < els.length; i++) list.push(els[i]);
    return list;
  }

  function createCapsuleNode(doc, r) {
    var chip = doc.createElement("span");
    chip.className = "wsc-capsule wsc-" + r.kind;
    chip.setAttribute("contenteditable", "false");
    chip.setAttribute("data-wsc-kind", r.kind);
    chip.setAttribute("data-wsc-id", r.id);
    chip.setAttribute("data-wsc-label", r.label);
    if (r.kind !== "skill" && r.kind !== "topic") {
      chip.setAttribute("data-wsc-key", r.key || r.id);
      chip.setAttribute("data-wsc-payload", JSON.stringify(r.payload || null));
      if (r.payload && typeof r.payload.title === "string" && r.payload.title) chip.setAttribute("title", r.payload.title);
    }
    chip.textContent = (r.kind === "skill" ? "/" : r.kind === "topic" ? "#" : "") + r.label;
    return chip;
  }

  function hasMarkdownMarkup(root) {
    if (!root || !root.querySelector) return false;
    return !!root.querySelector(".wsmd-line");
  }

  function attach(editorEl, opts) {
    if (!editorEl) return { reconcile: function () {}, schedule: function () {}, detach: function () {} };
    opts = opts || {};
    var doc = editorEl.ownerDocument || document;
    var win = doc.defaultView || window;
    var field = editorEl.wscField || null;

    var detached = false;
    var localComposing = false;
    var rafId = 0;
    var lastSig = null;
    var domDirty = true;
    var lastWasBang = false;
    var applyingHistory = false;
    var reconciling = false;

    // Managed per-instance undo/redo stack.
    var undoStack = [];
    var redoStack = [];
    var lastEditTime = 0;
    var lastEditKind = "";

    function getWsc() {
      if (!field && editorEl.wscField) field = editorEl.wscField;
      return field && field.wsc ? field.wsc : null;
    }

    function getMentions() {
      if (typeof opts.getMentions === "function") {
        var m = opts.getMentions();
        return Array.isArray(m) ? m : [];
      }
      return [];
    }

    function isBang() {
      return typeof opts.isBang === "function" ? !!opts.isBang() : false;
    }

    function isComposing() {
      return localComposing || (typeof opts.isComposing === "function" ? !!opts.isComposing() : false);
    }

    function isEditorFocused() {
      if (doc.activeElement === editorEl) return true;
      var sel = win.getSelection && win.getSelection();
      return !!(sel && sel.rangeCount && editorEl.contains && editorEl.contains(sel.anchorNode));
    }

    function readSelectionOffsets() {
      var wsc = getWsc();
      var sel = win.getSelection && win.getSelection();
      if (sel && sel.rangeCount && editorEl.contains && editorEl.contains(sel.anchorNode) && editorEl.contains(sel.focusNode)) {
        var a = readEditorDom(editorEl, { node: sel.anchorNode, offset: sel.anchorOffset }).position;
        var f = readEditorDom(editorEl, { node: sel.focusNode, offset: sel.focusOffset }).position;
        return [a, f];
      }
      if (wsc && field) return [field.selectionStart || 0, field.selectionEnd || 0];
      return [0, 0];
    }

    function restoreSelectionOffsets(offsets) {
      if (!offsets || !isEditorFocused()) return;
      var sel = win.getSelection && win.getSelection();
      if (!sel) return;
      var a = offsets[0], f = offsets[1];
      var startPt = pointInDom(editorEl, a);
      var endPt = a === f ? startPt : pointInDom(editorEl, f);
      if (typeof sel.setBaseAndExtent === "function") {
        try {
          sel.setBaseAndExtent(startPt[0], startPt[1], endPt[0], endPt[1]);
          return;
        } catch (_) { /* fall through to Range */ }
      }
      if (doc.createRange) {
        var lo = Math.min(a, f), hi = Math.max(a, f);
        var p1 = pointInDom(editorEl, lo), p2 = lo === hi ? p1 : pointInDom(editorEl, hi);
        var r = doc.createRange();
        r.setStart(p1[0], p1[1]);
        r.setEnd(p2[0], p2[1]);
        sel.removeAllRanges();
        sel.addRange(r);
      }
    }

    function readState() {
      var wsc = getWsc();
      if (wsc && field) {
        var text = typeof field.value === "string" ? field.value : "";
        var caps = wsc.snapshot ? wsc.snapshot() : [];
        return { text: text, capsules: cloneCapsules(caps) };
      }
      var domState = readEditorDom(editorEl);
      var meaningful = Array.prototype.filter.call(editorEl.childNodes || [], isMeaningful);
      var domText = meaningful.length === 1 && meaningful[0].nodeName === "BR" ? "" : domState.text;
      return { text: domText, capsules: cloneCapsules(domState.capsules) };
    }

    function captureSnapshot() {
      var st = readState();
      var sel = readSelectionOffsets();
      return { text: st.text, capsules: st.capsules, sel: [sel[0], sel[1]] };
    }

    function recordHistory(kindHint) {
      if (applyingHistory || detached) return;
      var snap = captureSnapshot();
      var now = Date.now();
      if (!undoStack.length) {
        undoStack.push(snap);
        lastEditTime = now;
        lastEditKind = "";
        return;
      }
      var top = undoStack[undoStack.length - 1];
      var sameCap = serializeCapsules(snap.capsules) === serializeCapsules(top.capsules);
      if (snap.text === top.text && sameCap) {
        top.sel = snap.sel;
        return;
      }
      var delta = snap.text.length - top.text.length;
      var kind = kindHint || (delta > 0 ? "insert" : delta < 0 ? "delete" : "other");
      var canCoalesce = false;
      if (
        undoStack.length >= 2 &&
        sameCap &&
        (kind === "insert" || kind === "delete") &&
        kind === lastEditKind &&
        now - lastEditTime < COALESCE_WINDOW_MS &&
        Math.abs(delta) <= 4
      ) {
        // Split bursts on newlines or word boundaries (space typed after non-space).
        var addedSlice = delta > 0 ? snap.text.slice(Math.max(0, snap.sel[1] - delta), snap.sel[1]) : "";
        var removedSlice = delta < 0 ? top.text.slice(Math.max(0, snap.sel[1]), Math.min(top.text.length, snap.sel[1] - delta)) : "";
        var crossedNewline = addedSlice.indexOf("\n") >= 0 || removedSlice.indexOf("\n") >= 0;
        var crossedSpace = delta > 0 && addedSlice === " " && top.text.length > 0 && top.text.charAt(Math.max(0, snap.sel[1] - 2)) !== " ";
        var prevEnd = top.sel[1];
        var contiguous = snap.sel[0] === snap.sel[1] && snap.sel[1] === prevEnd + delta;
        if (!crossedNewline && !crossedSpace && contiguous) canCoalesce = true;
      }
      if (canCoalesce) {
        undoStack[undoStack.length - 1] = snap;
      } else {
        undoStack.push(snap);
        if (undoStack.length > HISTORY_LIMIT) undoStack.shift();
      }
      redoStack = [];
      lastEditTime = now;
      lastEditKind = kind;
    }

    function applySnapshot(snap, action) {
      applyingHistory = true;
      try {
        var wsc = getWsc();
        var lo = Math.max(0, Math.min(snap.text.length, snap.sel ? snap.sel[0] : snap.text.length));
        var hi = Math.max(0, Math.min(snap.text.length, snap.sel ? snap.sel[1] : snap.text.length));
        if (wsc && field) {
          if (typeof wsc.applyState === "function") {
            wsc.applyState(snap.text, cloneCapsules(snap.capsules), [lo, hi], action || "history");
          } else {
            field.value = snap.text;
            if (typeof wsc.restore === "function") wsc.restore(cloneCapsules(snap.capsules));
            if (typeof field.setSelectionRange === "function") field.setSelectionRange(lo, hi);
            field.dispatchEvent(new Event("input", { bubbles: true }));
          }
        } else {
          // Standalone editor without WsCapsules
          var frag = doc.createDocumentFragment();
          var at = 0;
          for (var i = 0; i < snap.capsules.length; i++) {
            var r = snap.capsules[i];
            if (r.start > at) frag.appendChild(doc.createTextNode(snap.text.slice(at, r.start)));
            frag.appendChild(createCapsuleNode(doc, r));
            at = r.end;
          }
          var tail = snap.text.slice(at);
          if (tail || snap.capsules.length) frag.appendChild(doc.createTextNode(tail));
          editorEl.textContent = "";
          editorEl.appendChild(frag);
          restoreSelectionOffsets([lo, hi]);
          editorEl.dispatchEvent(new Event("input", { bubbles: true }));
        }
        domDirty = true;
        lastEditKind = "";
        lastEditTime = 0;
      } finally {
        applyingHistory = false;
      }
      reconcile(true);
    }

    function performUndo() {
      // Ensure latest state is captured if any unrecorded edit occurred.
      recordHistory("other");
      if (undoStack.length <= 1) return false;
      var current = undoStack.pop();
      redoStack.push(current);
      if (redoStack.length > HISTORY_LIMIT) redoStack.shift();
      var prev = undoStack[undoStack.length - 1];
      applySnapshot(prev, "undo");
      return true;
    }

    function performRedo() {
      if (!redoStack.length) return false;
      var next = redoStack.pop();
      undoStack.push(next);
      if (undoStack.length > HISTORY_LIMIT) undoStack.shift();
      applySnapshot(next, "redo");
      return true;
    }

    function takeCapsuleNode(pool, range) {
      for (var i = 0; i < pool.length; i++) {
        var el = pool[i];
        if (el.getAttribute("data-wsc-kind") !== range.kind) continue;
        var keyMatch = range.kind === "skill" || range.kind === "topic"
          ? el.getAttribute("data-wsc-id") === range.id
          : el.getAttribute("data-wsc-key") === range.key;
        if (keyMatch && el.getAttribute("data-wsc-label") === range.label) {
          pool.splice(i, 1);
          return el;
        }
      }
      return createCapsuleNode(doc, range);
    }

    function renderFlat(text, capsules, existingNodes) {
      var frag = doc.createDocumentFragment();
      var at = 0;
      for (var i = 0; i < capsules.length; i++) {
        var r = capsules[i];
        if (r.start > at) frag.appendChild(doc.createTextNode(text.slice(at, r.start)));
        frag.appendChild(takeCapsuleNode(existingNodes, r));
        at = r.end;
      }
      var tail = text.slice(at);
      if (tail || capsules.length) frag.appendChild(doc.createTextNode(tail));
      editorEl.textContent = "";
      editorEl.appendChild(frag);
    }

    function appendRangeWithCapsules(parent, text, segStart, segEnd, cls, capsules, existingNodes) {
      var cur = segStart;
      for (var i = 0; i < capsules.length; i++) {
        var c = capsules[i];
        if (c.end <= segStart) continue;
        if (c.start >= segEnd) break;
        if (c.start > cur) {
          var before = text.slice(cur, c.start);
          if (cls) {
            var sp = doc.createElement("span");
            sp.className = cls;
            sp.appendChild(doc.createTextNode(before));
            parent.appendChild(sp);
          } else {
            parent.appendChild(doc.createTextNode(before));
          }
        }
        if (c.start >= segStart && c.end <= segEnd) {
          parent.appendChild(takeCapsuleNode(existingNodes, c));
        }
        cur = Math.min(segEnd, c.end);
      }
      if (cur < segEnd) {
        var slice = text.slice(cur, segEnd);
        if (cls) {
          var el = doc.createElement("span");
          el.className = cls;
          el.appendChild(doc.createTextNode(slice));
          parent.appendChild(el);
        } else {
          parent.appendChild(doc.createTextNode(slice));
        }
      }
    }

    function renderMarkdown(text, capsules, mentions, existingNodes) {
      if (!text.length) {
        if (editorEl.childNodes.length) editorEl.textContent = "";
        return;
      }
      var parser = win.WsMdParser || window.WsMdParser;
      if (!parser || typeof parser.parse !== "function") {
        renderFlat(text, capsules, existingNodes);
        return;
      }
      var parsed = parser.parse(text, mentions, capsules);
      var lines = parsed.lines;
      var frag = doc.createDocumentFragment();
      for (var i = 0; i < lines.length; i++) {
        var ln = lines[i];
        var lineHasContent = ln.end > ln.start;
        if (lineHasContent || ln.blockClass) {
          var lineSpan = doc.createElement("span");
          lineSpan.className = ln.blockClass ? "wsmd-line " + ln.blockClass : "wsmd-line";
          if (ln.qDepth > 0) lineSpan.setAttribute("data-wsmd-qdepth", String(Math.min(ln.qDepth, 4)));
          if (ln.indent > 0) lineSpan.setAttribute("data-wsmd-indent", String(ln.indent));
          var tokens = ln.tokens;
          if (!tokens.length && lineHasContent) {
            appendRangeWithCapsules(lineSpan, text, ln.start, ln.end, "", capsules, existingNodes);
          } else {
            for (var t = 0; t < tokens.length; t++) {
              var tok = tokens[t];
              appendRangeWithCapsules(lineSpan, text, tok.start, tok.end, tok.cls, capsules, existingNodes);
            }
          }
          frag.appendChild(lineSpan);
        }
        if (ln.hasNewline) {
          frag.appendChild(doc.createTextNode("\n"));
        }
      }
      // Trailing newline needs a caret filler <br> so Chromium renders and targets the empty final line.
      if (text.charAt(text.length - 1) === "\n") {
        frag.appendChild(doc.createElement("br"));
      } else if (capsules.length && capsules[capsules.length - 1].end === text.length) {
        // Keep an empty trailing text node after an ending capsule so the caret can sit after it.
        frag.appendChild(doc.createTextNode(""));
      }
      editorEl.textContent = "";
      editorEl.appendChild(frag);
    }

    function reconcile(force) {
      if (detached || reconciling || isComposing()) return;
      reconciling = true;
      try {
        var st = readState();
        var bang = isBang();
        var mentions = bang ? [] : getMentions();
        var sig = (bang ? "B:" : "M:") + st.text + "\0" + serializeCapsules(st.capsules) + "\0" + serializeMentions(mentions);

        // Fast no-op when bang mode is already active and DOM has no markdown spans.
        if (bang && lastWasBang && !hasMarkdownMarkup(editorEl)) {
          lastSig = sig;
          domDirty = false;
          return;
        }
        // Fast no-op when content + metadata + DOM are already up to date.
        if (!force && !domDirty && sig === lastSig) return;

        var wasFocused = isEditorFocused();
        var savedSel = wasFocused ? readSelectionOffsets() : null;
        var existingNodes = collectCapsuleNodes(editorEl);

        if (bang) {
          renderFlat(st.text, st.capsules, existingNodes);
        } else {
          renderMarkdown(st.text, st.capsules, mentions, existingNodes);
        }

        lastSig = sig;
        lastWasBang = bang;
        domDirty = false;

        if (wasFocused && savedSel) {
          restoreSelectionOffsets(savedSel);
          var wsc = getWsc();
          if (wsc && typeof wsc.rememberCaret === "function") wsc.rememberCaret();
        }
        if (typeof opts.onReconciled === "function") opts.onReconciled();
      } finally {
        reconciling = false;
      }
    }

    function schedule() {
      if (detached || isComposing() || rafId) return;
      var raf = typeof win.requestAnimationFrame === "function"
        ? win.requestAnimationFrame
        : function (fn) { return setTimeout(fn, 0); };
      rafId = raf(function () {
        rafId = 0;
        if (!detached && !isComposing()) reconcile(false);
      });
    }

    function cancelPending() {
      if (!rafId) return;
      if (typeof win.cancelAnimationFrame === "function") win.cancelAnimationFrame(rafId);
      else clearTimeout(rafId);
      rafId = 0;
    }

    // --- Event Handlers ---
    function onCompositionStart() {
      localComposing = true;
      cancelPending();
      recordHistory("other");
    }

    function onCompositionEnd() {
      localComposing = false;
      domDirty = true;
      recordHistory("other");
      reconcile(true);
    }

    function onInput() {
      if (detached) return;
      domDirty = true;
      if (isComposing()) return;
      recordHistory();
      schedule();
    }

    function onWscChange(e) {
      if (detached || applyingHistory) return;
      domDirty = true;
      if (isComposing()) return;
      recordHistory("other");
      var action = e && e.detail && e.detail.action;
      if (action === "insert" || action === "remove" || action === "restore") {
        reconcile(true);
      } else {
        schedule();
      }
    }

    function onBeforeInput(e) {
      if (detached || isComposing() || e.isComposing) return;
      if (e.inputType === "historyUndo") {
        e.preventDefault();
        e.stopPropagation();
        performUndo();
      } else if (e.inputType === "historyRedo") {
        e.preventDefault();
        e.stopPropagation();
        performRedo();
      } else if (e.inputType === "insertFromPaste" || e.inputType === "insertFromDrop" || e.inputType === "deleteByCut") {
        recordHistory("other");
      }
    }

    function onKeydown(e) {
      if (detached || isComposing() || e.isComposing || e.keyCode === 229) return;
      if (e.altKey) return;
      var mod = e.ctrlKey || e.metaKey;
      if (!mod) return;
      var key = (e.key || "").toLowerCase();
      if (key === "z") {
        e.preventDefault();
        e.stopPropagation();
        if (e.shiftKey) performRedo();
        else performUndo();
      } else if (key === "y" && e.ctrlKey && !e.metaKey && !e.shiftKey) {
        e.preventDefault();
        e.stopPropagation();
        performRedo();
      }
    }

    function onRenderHook() {
      if (detached || reconciling) return;
      domDirty = true;
      if (isComposing()) return;
      if (!applyingHistory) recordHistory("other");
      reconcile(true);
    }

    editorEl.addEventListener("compositionstart", onCompositionStart);
    editorEl.addEventListener("compositionend", onCompositionEnd);
    editorEl.addEventListener("beforeinput", onBeforeInput, true);
    editorEl.addEventListener("keydown", onKeydown, true);

    if (field) {
      field.addEventListener("input", onInput);
      field.addEventListener("wsc:change", onWscChange);
    } else {
      editorEl.addEventListener("input", onInput);
    }

    editorEl._wsmdRenderHook = onRenderHook;

    // Seed initial history baseline and perform initial reconcile if non-empty.
    undoStack.push(captureSnapshot());
    if (readState().text.length) reconcile(true);

    function detach() {
      if (detached) return;
      detached = true;
      cancelPending();
      editorEl.removeEventListener("compositionstart", onCompositionStart);
      editorEl.removeEventListener("compositionend", onCompositionEnd);
      editorEl.removeEventListener("beforeinput", onBeforeInput, true);
      editorEl.removeEventListener("keydown", onKeydown, true);
      if (field) {
        field.removeEventListener("input", onInput);
        field.removeEventListener("wsc:change", onWscChange);
      } else {
        editorEl.removeEventListener("input", onInput);
      }
      if (editorEl._wsmdRenderHook === onRenderHook) delete editorEl._wsmdRenderHook;
    }

    return {
      reconcile: function () { reconcile(false); },
      schedule: schedule,
      detach: detach
    };
  }

  window.WsMdEditor = {
    attach: attach
  };
})();
