;(function () {
  // Runs inside a served page's topic-body frame, a sandboxed srcdoc document in an
  // opaque origin (wswhiteboard framedDoc), after the body's own markup. The frame
  // cannot touch the host page, so it tells the host what the host acts on
  // through postMessage: the content height, so the host sizes the frame
  // (again whenever the host asks with {wbDoc: "size"}); a
  // marked link (data-wl) click, so the host opens its link menu; and an
  // item/panel-reference click (href="#item-nn" or "#pN"), so the host
  // reveals and scrolls to the existing board target; and an
  // image or diagram click, so the host opens its viewer over the whole page
  // ({wbDoc: "view"}: the image's source, or the diagram flattened by
  // wbFlattenSvg, which svg-view.js defines ahead of this script); and, for
  // an image outside the session root, a request for its bytes
  // ({wbDoc: "img"}, see relay below); and a text selection, which the
  // dashboard's live board offers to add to the message as a mention
  // ({wbDoc: "sel"}, see below); and the host's rail key, Ctrl+`
  // ({wbDoc: "rail"}, see the end); and the palette shortcut, Ctrl+K or
  // Ctrl+Space ({wbDoc: "search"}, see the end); and Ctrl+Q toggles the
  // host board's Overview ({wbDoc: "overview"}).
  // A plain link needs no help: <base target="_blank"> opens it in a new tab.
  var d = document, body = d.querySelector(".doc-body");

  // The frame's mermaid blocks draw through this once the frame is laid
  // out: inside a collapsed body the frame has no width, and mermaid would
  // measure every label as empty. Until then mermaid's own draw on page
  // load is turned off. A theme switch draws through it again.
  var redraw = null;
  window.wbDocMermaid = function (draw) {
    redraw = draw;
    if (window.innerWidth) { draw(); return; }
    if (window.mermaid) window.mermaid.initialize({ startOnLoad: false });
    window.addEventListener("resize", function once() {
      if (!window.innerWidth) return;
      window.removeEventListener("resize", once);
      draw();
    });
  };

  if (body) wbWatchChanges(body);
  if (!body || window.parent === window) return;
  var keyMeta = d.querySelector('meta[name="wb-doc-key"]');
  var docKey = keyMeta && keyMeta.getAttribute("content");
  if (!docKey) return;
  // The host compares this key with the source iframe's own identity. It
  // never uses a claimed entry/topic locator to attribute a selection.
  function post(m) { m.docKey = docKey; window.parent.postMessage(m, "*"); }

  var last = -1;
  function size() {
    var h = Math.ceil(body.getBoundingClientRect().height);
    if (h === last) return;
    last = h;
    post({ wbDoc: "height", height: h });
  }
  if (window.ResizeObserver) new ResizeObserver(size).observe(body);
  window.addEventListener("load", size);
  // The host page may register its listener after this frame first posts;
  // it then asks for the height again. It also posts its pinned theme
  // ({wbDoc: "theme"}: "light", "dark", or "" for the OS) on a switch and in
  // answer to each height, since this document follows only its own root:
  // the server pins the theme current when it rendered the frame, and a
  // switch since then reaches the frame only this way. The frame's drawn
  // diagrams redraw in the new theme.
  function theme(v) {
    var root = d.documentElement;
    if (v !== "light" && v !== "dark") v = "";
    if ((root.getAttribute("data-theme") || "") === v) return;
    if (v) root.setAttribute("data-theme", v);
    else root.removeAttribute("data-theme");
    if (redraw && d.querySelector("pre.mermaid[data-processed]")) window.wbDocMermaid(redraw);
  }
  // An image outside the session root: the server leaves its path text
  // marked data-relay with the image route URL (wswhiteboard relayImage),
  // which this opaque origin cannot load (the route refuses it a cross-site
  // request). The frame asks the host page for it by index, and the host,
  // which fetches only a URL listed on this frame's own element, answers
  // with the image as a Blob. The image replaces the path text only once it
  // decodes; with no answer (a LAN viewer, a missing file, a refused fetch)
  // or a broken image the path text stays. A request lost to a host that was
  // not yet listening is asked again when the host asks for the height.
  // A raw <img> the author typed with such a path comes with no src and the
  // same data-relay (wswhiteboard rawImageTag); it keeps its own attributes
  // and takes the image as its src.
  var relays = Array.prototype.slice.call(d.querySelectorAll("span.img-text[data-relay], img[data-relay]"));
  function askRelays() {
    relays.forEach(function (s, i) {
      if (!s.wbRelayed) post({ wbDoc: "img", id: i, href: s.getAttribute("data-relay") });
    });
  }
  function relayed(m) {
    var s = typeof m.id === "number" ? relays[m.id] : null, b = m.blob;
    if (!s || s.wbRelayed || !s.parentNode || !window.Blob || !(b instanceof Blob) || !/^image\//.test(b.type)) return;
    s.wbRelayed = true;
    if (s.tagName === "IMG") {
      var ir = new FileReader();
      ir.onload = function () { s.src = ir.result; };
      ir.readAsDataURL(b);
      return;
    }
    var alt = s.cloneNode(true);
    Array.prototype.forEach.call(alt.querySelectorAll("a, code"), function (c) { c.parentNode.removeChild(c); });
    var r = new FileReader();
    r.onload = function () {
      var img = d.createElement("img"), code = s.querySelector("code");
      img.alt = alt.textContent.trim();
      if (code) img.title = code.textContent;
      function show() { if (s.parentNode) s.parentNode.replaceChild(img, s); }
      img.src = r.result;
      if (img.decode) img.decode().then(show, function () {});
      else { img.onload = show; }
    };
    r.readAsDataURL(b);
  }
  // findMarks holds each match's first mark; a match crossing inline tags
  // carries all of its marks as wbFindGroup (card-find.js keeps the same rules).
  var findMarks = [];
  var FIND_INLINE = { A: 1, ABBR: 1, B: 1, BDI: 1, BDO: 1, CITE: 1, CODE: 1, DATA: 1, DEL: 1, DFN: 1, EM: 1, FONT: 1, I: 1, INS: 1, KBD: 1, LABEL: 1, MARK: 1, Q: 1, S: 1, SAMP: 1, SMALL: 1, SPAN: 1, STRONG: 1, SUB: 1, SUP: 1, TIME: 1, U: 1, VAR: 1 };
  function findGroup(m) { return m.wbFindGroup || [m]; }
  function clearFind() {
    var parents = new Set();
    findMarks.forEach(function (first) {
      findGroup(first).forEach(function (m) {
        var p = m.parentNode;
        if (!p) return;
        parents.add(p);
        while (m.firstChild) p.insertBefore(m.firstChild, m);
        p.removeChild(m);
      });
    });
    parents.forEach(function (p) { if (p.normalize) p.normalize(); });
    findMarks = [];
  }
  function findBlock(node) {
    var p = node.parentElement;
    while (p && p !== body && FIND_INLINE[p.tagName]) p = p.parentElement;
    return p;
  }
  function runFind(query) {
    clearFind();
    if (!query) return 0;
    var needle = String(query).toLowerCase(), len = needle.length;
    if (!len || !d.createTreeWalker) return 0;
    // Elements are walked too: a non-inline one (even empty, hidden or
    // skipped) becomes a null boundary so text on either side never joins.
    var nodes = [], walker = d.createTreeWalker(body, 5, {
      acceptNode: function (node) {
        if (node.nodeType === 1) return FIND_INLINE[node.tagName] ? 3 : 1;
        var p = node.parentElement;
        // Whitespace-only nodes stay: they join words across inline tags.
        if (!p || !node.nodeValue) return 2;
        if (p.closest && p.closest("script, style, template, textarea, input, select, svg, [hidden]")) return 2;
        for (var a = p; a && a !== body; a = a.parentElement) {
          if (a.parentElement && a.parentElement.tagName === "DETAILS" && !a.parentElement.open && a.tagName !== "SUMMARY") return 2;
        }
        return 1;
      }
    });
    var n;
    while ((n = walker.nextNode())) {
      if (n.nodeType !== 1) nodes.push(n);
      else if (nodes.length && nodes[nodes.length - 1]) nodes.push(null);
    }
    // A block's consecutive text nodes are searched as one string, then each
    // match is cut back into per-node marks.
    var i = 0, k;
    while (i < nodes.length) {
      if (!nodes[i]) { i++; continue; } // block boundary
      var block = findBlock(nodes[i]), run = [], starts = [], ends = [], text = "";
      while (i < nodes.length && nodes[i] && findBlock(nodes[i]) === block) {
        var val = nodes[i].nodeValue, low = val.toLowerCase();
        if (low.length !== val.length) low = val; // keep offsets aligned
        run.push(nodes[i]);
        starts.push(text.length);
        text += low;
        ends.push(text.length);
        i++;
      }
      // Matches arrive in order and never overlap: one forward walk.
      var cuts = run.map(function () { return []; }), groups = [], k0 = 0;
      for (var idx = text.indexOf(needle); idx >= 0; idx = text.indexOf(needle, idx + len)) {
        var end = idx + len, g = [];
        while (k0 < run.length && ends[k0] <= idx) k0++;
        for (k = k0; k < run.length && starts[k] < end; k++) {
          cuts[k].push([Math.max(idx, starts[k]) - starts[k], Math.min(end, ends[k]) - starts[k], g]);
        }
        groups.push(g);
      }
      for (k = 0; k < run.length; k++) {
        if (!cuts[k].length) continue;
        var node = run[k], v = node.nodeValue, frag = d.createDocumentFragment(), pos = 0;
        cuts[k].forEach(function (c) {
          if (c[0] > pos) frag.appendChild(d.createTextNode(v.slice(pos, c[0])));
          var mark = d.createElement("mark");
          mark.className = "wb-find-hit";
          mark.textContent = v.slice(c[0], c[1]);
          frag.appendChild(mark);
          c[2].push(mark);
          pos = c[1];
        });
        if (pos < v.length) frag.appendChild(d.createTextNode(v.slice(pos)));
        if (node.parentNode) node.parentNode.replaceChild(frag, node);
      }
      groups.forEach(function (g) {
        if (!g.length) return;
        g[0].wbFindGroup = g;
        findMarks.push(g[0]);
      });
    }
    return findMarks.length;
  }
  function focusFind(index, seq, scroll) {
    findMarks.forEach(function (m) { findGroup(m).forEach(function (h) { h.classList.remove("wb-find-cur"); }); });
    var cur = index >= 0 && index < findMarks.length ? findMarks[index] : null;
    if (!cur) return;
    findGroup(cur).forEach(function (h) { h.classList.add("wb-find-cur"); });
    if (scroll) {
      var r = cur.getBoundingClientRect();
      post({ wbDoc: "find-scroll", seq: seq, top: r.top, height: r.height });
    }
  }
  window.addEventListener("message", function (e) {
    if (e.source !== window.parent || !e.data || e.data.docKey !== docKey) return;
    if (e.data.wbDoc === "size") { last = -1; size(); askRelays(); }
    else if (e.data.wbDoc === "theme") theme(e.data.theme);
    else if (e.data.wbDoc === "img") relayed(e.data);
    else if (e.data.wbDoc === "unsel" && window.getSelection) window.getSelection().removeAllRanges();
    else if (e.data.wbDoc === "find") post({ wbDoc: "find-res", seq: e.data.seq, count: runFind(e.data.query) });
    else if (e.data.wbDoc === "find-focus") focusFind(e.data.index, e.data.seq, e.data.scroll);
    else if (e.data.wbDoc === "find-clear") clearFind();
  });

  // The selection: its text and where its end stands in this frame, "" once
  // it collapses (each change, but a collapsed one only once). The host
  // clears it after adding it ({wbDoc: "unsel"}).
  var selQueued = false, selLast = "";
  d.addEventListener("selectionchange", function () {
    if (selQueued || !window.getSelection) return;
    selQueued = true;
    requestAnimationFrame(function () {
      selQueued = false;
      var s = window.getSelection(), inside = s && body.contains(s.anchorNode) && body.contains(s.focusNode);
      var text = inside && !s.isCollapsed && s.rangeCount ? s.toString() : "";
      if (!text && !selLast) return;
      selLast = text;
      var r = null;
      if (text) {
        var rs = s.getRangeAt(0).getClientRects();
        r = rs.length ? rs[rs.length - 1] : s.getRangeAt(0).getBoundingClientRect();
      }
      post({ wbDoc: "sel", text: text, x: r ? r.right : 0, y: r ? r.bottom : 0 });
    });
  });
  size();
  askRelays();

  // The Werkschaft desktop app names itself in its user agent. Its webview
  // has no tabs, so there a modified click on a marked file link is handed
  // to the host too, as a pointer Cmd/Ctrl+click (mod), which the host
  // previews; a browser keeps its new tab.
  var desktop = /\bWerkschaft\//.test(navigator.userAgent || "");
  function link(a, e) {
    var r = a.getBoundingClientRect(), key = e.type === "keydown" || e.detail === 0;
    post({
      wbDoc: "link",
      kind: a.getAttribute("data-wl"),
      href: a.getAttribute("href") || "",
      rel: a.getAttribute("data-rel") || "",
      abs: a.getAttribute("data-abs") || "",
      img: a.hasAttribute("data-img"),
      mod: !key && !!(e.metaKey || e.ctrlKey),
      x: key ? r.left : e.clientX,
      y: key ? r.bottom : e.clientY
    });
  }
  // Ahead of the link handling below. An image in a marked link or an
  // in-page anchor keeps that link's behavior; any other link around an image
  // opens the viewer, whose original button then reaches the link.
  d.addEventListener("click", function (e) {
    if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    var t = e.target, a = t && t.closest && t.closest("a"), img = t && t.closest && t.closest("img");
    if (!t || !t.closest || (a && (a.hasAttribute("data-wl") || (a.getAttribute("href") || "").charAt(0) === "#"))) return;
    if (img) {
      if (!img.complete || !img.naturalWidth) return;
      e.preventDefault();
      post({ wbDoc: "view", kind: "img", src: img.currentSrc || img.src, alt: img.alt, href: a ? a.href : "" });
      return;
    }
    var s = wbSvgTarget(t), flat = s && wbFlattenSvg(s);
    if (!flat) return;
    e.preventDefault();
    post({ wbDoc: "view", kind: "svg", svg: flat });
  }, true);
  d.addEventListener("click", function (e) {
    var t = e.target, a = t && t.closest && t.closest("a");
    if (!a || e.button !== 0) return;
    var href = a.getAttribute("href") || "";
    if (href.charAt(0) === "#") {
      // The frame never scrolls, so an in-page anchor only means something
      // on the host's board; a footnote's anchor names an element of this
      // frame and has nowhere to go.
      e.preventDefault();
      var id = href.slice(1);
      if (!/^(?:(?:item|check)-[0-9]+|p[1-9][0-9]*)$/.test(id)) return;
      post({ wbDoc: "jump", id: id });
      return;
    }
    if (!a.hasAttribute("data-wl")) return;
    // As on the host: a modified click on a link with an href is the
    // browser's new tab, except on a file link in the desktop app.
    if (href && !(desktop && a.getAttribute("data-wl") === "file") && (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey)) return;
    e.preventDefault();
    link(a, e);
  }, true);
  d.addEventListener("keydown", function (e) {
    var a = e.target && e.target.closest && e.target.closest("a[data-wl]:not([href])");
    if (a && (e.key === "Enter" || e.key === " ")) { e.preventDefault(); link(a, e); }
  });
  // Ctrl+` (the host's rail key, same test): a key pressed in this frame
  // reaches only this document, so the frame hands it to the host
  // ({wbDoc: "rail"}), which decides whether the rail toggles.
  d.addEventListener("keydown", function (e) {
    if (e.code !== "Backquote" || !e.ctrlKey || e.altKey || e.metaKey || e.shiftKey || e.isComposing) return;
    e.preventDefault();
    post({ wbDoc: "rail" });
  });
  // Ctrl+K, Ctrl+Space and Cmd+K (the host palette's keys, by tracker.js
  // qShortcut: only a terminal keeps them, and a body has none) open the
  // host's search palette ({wbDoc: "search"}).
  d.addEventListener("keydown", function (e) {
    if (!(e.ctrlKey || e.metaKey) || e.altKey || e.shiftKey || e.isComposing || e.keyCode === 229) return;
    var k = (e.key || "").toLowerCase() === "k";
    if (e.metaKey ? e.ctrlKey || !k : !k && e.code !== "Space" && e.key !== " ") return;
    e.preventDefault();
    post({ wbDoc: "search" });
  });
  // Like the session document, leave editing, terminal and IME input native.
  d.addEventListener("keydown", function (e) {
    if (e.defaultPrevented || e.isComposing || e.keyCode === 229 || !e.ctrlKey || e.metaKey || e.altKey || e.shiftKey || (e.key || "").toLowerCase() !== "q") return;
    var t = e.target;
    if (t && (t.isContentEditable || (t.closest && t.closest("input, textarea, select, [contenteditable]:not([contenteditable=\"false\"]), .xterm, .tm-host")))) return;
    e.preventDefault();
    post({ wbDoc: "overview" });
  });
  // Ctrl/Cmd+F inside a framed card opens the host's in-card find bar.
  d.addEventListener("keydown", function (e) {
    if (e.defaultPrevented || e.isComposing || e.keyCode === 229 || !(e.ctrlKey || e.metaKey) || e.altKey || e.shiftKey || (e.key || "").toLowerCase() !== "f") return;
    e.preventDefault();
    post({ wbDoc: "find-open" });
  });
  // Report pointer hover so host-focused Ctrl/Cmd+F knows when the pointer
  // is over this framed card.
  var hovered = false;
  function setHover(on) {
    if (hovered === on) return;
    hovered = on;
    post({ wbDoc: "hover", on: on });
  }
  d.addEventListener("pointermove", function () { setHover(true); }, { passive: true });
  d.addEventListener("pointerleave", function () { setHover(false); });
  window.addEventListener("blur", function () { setHover(false); });
  // Forward wheel deltas passively so the host's snap-deck wheel controller
  // sees boundary gestures while native 120Hz inner card scroll stays unblocked.
  // Skip forwarding when any scrollable ancestor inside this frame can still
  // scroll in the gesture direction.
  function canScrollInFrame(target, dy) {
    var root = d.scrollingElement || d.documentElement;
    for (var n = target && target.nodeType === 1 ? target : root; n && n.nodeType === 1; n = n.parentNode) {
      if (n.scrollHeight > n.clientHeight + 2) {
        var isRoot = n === root || n === d.documentElement || n === d.body;
        var cs = isRoot ? null : window.getComputedStyle && window.getComputedStyle(n);
        if (isRoot || (cs && /auto|scroll/.test(cs.overflowY))) {
          var canMove = dy > 0 ? n.scrollTop + n.clientHeight < n.scrollHeight - 1 : n.scrollTop > 1;
          if (canMove) return true;
        }
      }
      if (n === root) break;
    }
    return false;
  }
  d.addEventListener("wheel", function (e) {
    if (e.defaultPrevented || e.ctrlKey) return;
    if (e.deltaY && canScrollInFrame(e.target, e.deltaY)) return;
    post({ wbDoc: "wheel", dy: e.deltaY, dx: e.deltaX, mode: e.deltaMode });
  }, { passive: true });
})();
