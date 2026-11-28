  // --- workpanel: the gallery's Workpanel system page (Graybox B) ---------
  // The server renders the Workpanel (dashboard.tmpl "workpanel", #wproot)
  // hidden, holding the one panels region; wpMount adopts it into the
  // gallery's Workpanel page host (cards.js #wpdock) and shows it, and a
  // page without the gallery keeps it hidden. Nothing here writes inside
  // the panels region: the region swap (dashboard-board.js) and the keyed
  // panel-rows merge (dashboard-stream.js) own its rows, and wpSync, after
  // each swap, only reads the rows' count chips into the chrome outside it.
  // The reference card's Notes tab reads the notes route (notes.go) each
  // time the page comes into view on it, and its Manuals tab reads the
  // manuals route (manuals.go) the first time it opens; nothing polls.
  var wproot = $("wproot"), wpmemory = $("wpmemory"), wpMounted = false, wpVis = false;
  var wpRef = "notes", wpLayer = "all", wpMuted = true, wpOpenNotes = {};
  var wpNotes = null, wpNotesSeq = 0, wpNotesAt = 0;
  var wpManuals = null, wpManualsSeq = 0, wpManualsBusy = false;
  var WP_LAYERS = ["worktree", "repo", "clone", "machine"];
  var WP_DOC_ICON = ["M3 2.5h6.4L13 6.1v7.4H3z", "M9.4 2.5v3.6H13M5.4 8.5h5.2M5.4 10.9h3.6"];

  function wpEl(sel) { return wproot ? wproot.querySelector(sel) : null; }
  function wpPanel(id) { var r = region("panels"); return r ? r.querySelector('[id="p-' + id + '"]') : null; }
  function wpMk(tag, cls, text) {
    var el = doc.createElement(tag);
    if (cls) el.className = cls;
    if (text != null) el.textContent = text;
    return el;
  }
  // wpIcon draws one of the Workpanel's stroke icons (the template's set).
  function wpIcon(paths, cls) {
    var svg = doc.createElementNS("http://www.w3.org/2000/svg", "svg");
    svg.setAttribute("class", "wp-ic" + (cls ? " " + cls : ""));
    svg.setAttribute("viewBox", "0 0 16 16");
    svg.setAttribute("aria-hidden", "true");
    paths.forEach(function (d) {
      var p = doc.createElementNS("http://www.w3.org/2000/svg", "path");
      p.setAttribute("d", d);
      svg.appendChild(p);
    });
    return svg;
  }

  // wpMount moves the root into the gallery's Workpanel page once the
  // gallery has built it.
  function wpMount() {
    if (wpMounted || !wproot) return wpMounted;
    var host = $("wpdock");
    if (!host) return false;
    host.appendChild(wproot);
    wproot.hidden = false;
    wpMounted = true;
    wpSync();
    return true;
  }
  // wpShown tells whether the gallery shows the Workpanel page now.
  function wpShown() {
    var C = window.WsCards, g = C && C.gallery && C.gallery();
    return wpMounted && !!g && !g.hidden && !!C.activeSys && C.activeSys() === "workpanel";
  }
  // drawerWait keeps the approval-wait signal current on its host: the
  // gallery rail's Workpanel slot (the retired drawer cards' direct
  // replacement). The tree region's hidden compact roster (.ag-source)
  // does not count: it repeats this tree's running rows and adds linked
  // delegate sessions' agents, whose approvals are not this session's to
  // wait on. Called after each tree or conversation swap
  // (dashboard-board.js) and each gallery change, since a repaint can
  // rebuild the slot.
  function drawerWait() {
    var wait = q("#r-convlive .permwait, #r-tree .phase.permission, #r-tree .brow.perm").some(function (el) { return !el.closest(".ag-source"); });
    var slot = doc.querySelector('#bgal .gal-dot.sys[data-sys="workpanel"]');
    if (slot && slot.classList.contains("wait") !== wait) {
      slot.classList.toggle("wait", wait);
      var sfx = "; " + wsT("js.drawer.waiting"), label = slot.getAttribute("aria-label") || "";
      if (label.slice(-sfx.length) === sfx) label = label.slice(0, -sfx.length);
      if (wait) label += sfx;
      slot.setAttribute("aria-label", label);
      if (slot.hasAttribute("title")) slot.title = label;
    }
  }

  // wpGallery follows the gallery: each coming into view reads the shown
  // reference tab again.
  function wpGallery() {
    wpMount();
    drawerWait();
    var vis = wpShown();
    if (vis === wpVis) return;
    wpVis = vis;
    if (vis) wpRefLoad();
  }

  // wpSync reads the panels region after each swap: the cards kept open,
  // the empty placeholders, the counts, and Todo's progress ("done/total").
  function wpSync() {
    if (!wproot) return;
    ["todo", "agenda"].forEach(function (id) {
      var p = wpPanel(id), c = p && p.querySelector(":scope > summary > .chip"), count = c ? c.textContent.trim() : "";
      if (p && p.tagName === "DETAILS" && !p.open) p.open = true;
      q('.wp-mem-empty[data-for="' + id + '"]', wproot).forEach(function (el) { el.hidden = !!p; });
      q('.wp-mem-tools[data-for="' + id + '"]', wproot).forEach(function (el) { el.hidden = !p; });
      q('[data-wp-count="' + id + '"], [data-wp-badge="' + id + '"]', wproot).forEach(function (el) { el.textContent = count; });
      if (id !== "todo") return;
      var m = /^(\d+)\/(\d+)$/.exec(count), pct = m && +m[2] > 0 ? Math.round(+m[1] * 100 / +m[2]) : 0;
      var fill = $("wpprog"), bar = fill && fill.closest("[role=progressbar]");
      if (fill) fill.style.transform = "scaleX(" + pct / 100 + ")"; // a scale, not a width, stays on the compositor
      if (bar) bar.setAttribute("aria-valuenow", String(pct));
    });
    // The head's session chip follows a rename (the head region's name).
    var chip = $("wpsession"), snm = doc.querySelector(".dwrap > .top .sname .snm");
    if (chip && snm && snm.textContent.trim()) chip.textContent = snm.textContent.trim();
  }

  // wpStreamSync: the head's live indicator follows the stream
  // (dashboard-stream.js connected).
  function wpStreamSync(on) {
    var el = $("wpsse");
    if (el) el.classList.toggle("off", !on);
  }

  // The narrow layout's [Todo | Agenda] switch.
  function wpMem(id) {
    if (!wpmemory) return;
    wpmemory.setAttribute("data-active-mem", id);
    q(".wp-seg-btn", wproot).forEach(function (b) {
      var on = b.getAttribute("data-mem-tab") === id;
      b.classList.toggle("is-on", on);
      b.setAttribute("aria-selected", String(on));
    });
  }
  // wpAgendaAll expands every agenda blob, or folds them all when every one
  // is open.
  function wpAgendaAll() {
    var p = wpPanel("agenda"), ds = p ? q(".body details", p) : [];
    if (!ds.length) return;
    var open = !ds.every(function (d) { return d.open; });
    ds.forEach(function (d) { d.open = open; });
  }

  // --- the reference card ---------------------------------------------------
  function wpRefTab(id) {
    wpRef = id;
    q(".wp-tab", wproot).forEach(function (b) {
      var on = b.getAttribute("data-ref-tab") === id;
      b.classList.toggle("is-on", on);
      b.setAttribute("aria-selected", String(on));
    });
    q(".wp-ref-acts", wproot).forEach(function (el) { el.hidden = el.getAttribute("data-acts") !== id; });
    var n = $("wppanel-notes"), m = $("wppanel-manuals");
    if (n) n.hidden = id !== "notes";
    if (m) m.hidden = id !== "manuals";
    wpRefLoad();
  }
  // wpRefLoad reads the shown tab: notes each time, manuals once (again
  // after a failure).
  function wpRefLoad() {
    if (wpRef === "notes") wpNotesLoad(false);
    else if (!wpManuals && !wpManualsBusy) wpManualsLoad();
  }

  // Notes come from the notes route's cached read; a wait joins the
  // server's running worker, and Refresh posts for a fresh read.
  function wpNotesLoad(refresh) {
    var list = $("wpnoteslist"), button = $("wpnotesrefresh");
    if (!list) return;
    var seq = ++wpNotesSeq, url = "/s/" + rid + "/notes";
    if (button) button.disabled = true;
    function request(wait) {
      return fetch(url + (wait ? "?wait=1" : ""), { method: refresh && !wait ? "POST" : "GET", credentials: "same-origin", cache: "no-store" }).then(function (r) {
        if (!r.ok) throw new Error("notes");
        return r.json();
      }).then(function (data) {
        if (seq !== wpNotesSeq) return;
        wpNotes = data;
        if (!data.loading) wpNotesAt = Date.now();
        wpNotesRender();
        if (data.loading) return request(true);
        if (button) button.disabled = false;
      });
    }
    request(false).catch(function () {
      if (seq !== wpNotesSeq) return;
      if (button) button.disabled = false;
      wpNotes = { layers: [], error: true };
      wpNotesRender();
    });
  }
  function wpLayerName(ly) { return WP_LAYERS.indexOf(ly) >= 0 ? wsT("js.notes." + ly) : ly; }
  // wpNotesRender draws the notes through the search, the muted toggle, and
  // the layer chip; the chips count what the search and the toggle keep.
  function wpNotesRender() {
    var list = $("wpnoteslist"), data = wpNotes || { layers: [] };
    if (!list) return;
    var input = $("wpnotesq"), query = input ? input.value.trim().toLowerCase() : "";
    var all = [], present = {};
    (data.layers || []).forEach(function (l) {
      present[l.layer] = true;
      (l.notes || []).forEach(function (n) { all.push({ layer: l.layer, note: n }); });
    });
    if (wpLayer !== "all" && !present[wpLayer]) wpLayer = "all";
    var kept = all.filter(function (r) {
      if (!wpMuted && r.note.visible === false) return false;
      if (!query) return true;
      return [r.note.key, r.note.value, r.layer, wpLayerName(r.layer)].some(function (s) { return String(s || "").toLowerCase().indexOf(query) >= 0; });
    });
    var counts = { all: kept.length };
    WP_LAYERS.forEach(function (ly) { counts[ly] = 0; });
    kept.forEach(function (r) { counts[r.layer] = (counts[r.layer] || 0) + 1; });
    q(".wp-lchip", wproot).forEach(function (b) {
      var ly = b.getAttribute("data-layer"), em = b.querySelector("em"), on = ly === wpLayer;
      if (ly !== "all") b.hidden = !present[ly];
      if (em) em.textContent = String(counts[ly] || 0);
      b.classList.toggle("is-on", on);
      b.setAttribute("aria-pressed", String(on));
    });
    var shown = kept.filter(function (r) { return wpLayer === "all" || r.layer === wpLayer; });
    var cnt = wpEl('[data-wp-count="notes"]');
    if (cnt) cnt.textContent = shown.length === all.length ? String(all.length) : shown.length + "/" + all.length;
    var ts = $("wpnotests");
    if (ts) {
      ts.textContent = wpNotesAt ? relAgo(Date.now() - wpNotesAt) : "";
      ts.title = wpNotesAt ? new Date(wpNotesAt).toLocaleString(wsI18n.lang) : "";
    }
    var clr = wpEl('[data-clear="wpnotesq"]');
    if (clr) clr.hidden = !query;

    list.textContent = "";
    if (data.local_only) list.appendChild(wpMk("div", "wp-hint", wsT("js.notes.local")));
    if (data.error) list.appendChild(wpMk("div", "wp-hint", wsT("js.notes.error")));
    if (data.loading && !all.length) { list.appendChild(wpMk("div", "wp-hint", wsT("js.notes.loading"))); return; }
    (data.layers || []).forEach(function (l) {
      if (l.error && (wpLayer === "all" || wpLayer === l.layer)) list.appendChild(wpMk("div", "wp-hint", wpLayerName(l.layer) + ": " + wsT("js.notes.error")));
    });
    if (!shown.length) {
      if (data.error) return;
      var empty = wpMk("div", "wp-empty");
      empty.appendChild(wpIcon(WP_DOC_ICON, "wp-empty-ic"));
      empty.appendChild(wpMk("span", "wp-empty-title", wsT(all.length ? "js.wp.nomatch" : "js.notes.empty")));
      list.appendChild(empty);
      return;
    }
    (wpLayer === "all" ? WP_LAYERS.concat(Object.keys(present).filter(function (ly) { return WP_LAYERS.indexOf(ly) < 0; })) : [wpLayer]).forEach(function (ly) {
      var rows = shown.filter(function (r) { return r.layer === ly; });
      if (!rows.length) return;
      rows.sort(function (a, b) { return (b.note.priority || 0) - (a.note.priority || 0); });
      var group = wpMk("section", "wp-notes-group");
      if (wpLayer === "all") {
        var hd = wpMk("div", "wp-notes-group-hd");
        var dot = wpMk("span", "wp-ly-dot"); dot.setAttribute("data-ly", ly); dot.setAttribute("aria-hidden", "true");
        hd.appendChild(dot);
        hd.appendChild(wpMk("span", "", wpLayerName(ly)));
        hd.appendChild(wpMk("span", "", "(" + rows.length + ")"));
        group.appendChild(hd);
      }
      rows.forEach(function (r) { group.appendChild(wpNoteRow(r.layer, r.note)); });
      list.appendChild(group);
    });
  }
  function wpNoteRow(ly, note) {
    var id = ly + "\u0000" + note.key, open = !!wpOpenNotes[id], muted = note.visible === false;
    var row = wpMk("article", "wp-note-row" + (muted ? " is-muted" : "") + (open ? " is-open" : ""));
    var top = wpMk("div", "wp-note-top");
    var prio = note.priority || 0;
    top.appendChild(wpMk("span", "wp-note-prio" + (prio === 0 ? " is-zero" : ""), "P" + prio));
    var tag = wpMk("span", "wp-ly-tag", wpLayerName(ly)); tag.setAttribute("data-ly", ly);
    top.appendChild(tag);
    var key = wpMk("span", "wp-note-key", note.key); key.title = note.key;
    top.appendChild(key);
    if (muted) top.appendChild(wpMk("span", "wp-note-muted", wsT("js.notes.muted")));
    var at = Date.parse(note.written_at);
    if (isFinite(at)) { var age = wpMk("span", "wp-note-age", glRel(at)); age.title = new Date(at).toLocaleString(wsI18n.lang); top.appendChild(age); }
    row.appendChild(top);
    var body = wpMk("button", "wp-note-body", note.value);
    body.type = "button";
    body.setAttribute("aria-expanded", String(open));
    body.addEventListener("click", function () {
      var on = body.getAttribute("aria-expanded") !== "true";
      if (on) wpOpenNotes[id] = true; else delete wpOpenNotes[id];
      body.setAttribute("aria-expanded", String(on));
      row.classList.toggle("is-open", on);
    });
    row.appendChild(body);
    return row;
  }

  // Manuals: the project's ai-docs/manuals list, read when the tab opens.
  function wpManualsLoad() {
    var list = $("wpmanualslist");
    if (!list) return;
    var seq = ++wpManualsSeq;
    wpManualsBusy = true;
    list.textContent = "";
    list.appendChild(wpMk("div", "wp-hint", wsT("js.wp.manuals.loading")));
    fetch("/s/" + rid + "/manuals", { credentials: "same-origin", cache: "no-store" }).then(function (r) {
      if (!r.ok) throw new Error("manuals");
      return r.json();
    }).then(function (data) {
      if (seq !== wpManualsSeq) return;
      wpManualsBusy = false;
      wpManuals = Array.isArray(data && data.manuals) ? data.manuals : [];
      wpManualsRender();
    }).catch(function () {
      if (seq !== wpManualsSeq) return;
      wpManualsBusy = false;
      wpManuals = null;
      list.textContent = "";
      list.appendChild(wpMk("div", "wp-hint", wsT("js.wp.manuals.error")));
    });
  }
  function wpManualsRender() {
    var list = $("wpmanualslist");
    if (!list || !wpManuals) return;
    var input = $("wpmanualsq"), query = input ? input.value.trim().toLowerCase() : "";
    var shown = wpManuals.filter(function (m) {
      return !query || [m.stem, m.path, m.summary].some(function (s) { return String(s || "").toLowerCase().indexOf(query) >= 0; });
    });
    var cnt = wpEl('[data-wp-count="manuals"]');
    if (cnt) cnt.textContent = shown.length === wpManuals.length ? String(wpManuals.length) : shown.length + "/" + wpManuals.length;
    var clr = wpEl('[data-clear="wpmanualsq"]');
    if (clr) clr.hidden = !query;
    list.textContent = "";
    if (!shown.length) {
      var empty = wpMk("div", "wp-empty");
      empty.appendChild(wpIcon(WP_DOC_ICON, "wp-empty-ic"));
      empty.appendChild(wpMk("span", "wp-empty-title", wsT(wpManuals.length ? "js.wp.nomatch" : "js.wp.manuals.empty")));
      list.appendChild(empty);
      return;
    }
    var ul = wpMk("ul", "wp-manual-list");
    shown.forEach(function (m) {
      var li = wpMk("li"), b = wpMk("button", "wp-manual-btn");
      b.type = "button";
      b.setAttribute("data-path", m.path);
      b.title = m.path;
      b.appendChild(wpIcon(WP_DOC_ICON, "wp-manual-ic"));
      var main = wpMk("span", "wp-manual-main"), head = wpMk("span", "wp-manual-head");
      head.appendChild(wpMk("span", "wp-manual-stem", m.stem));
      head.appendChild(wpMk("span", "wp-manual-ext", ".md"));
      main.appendChild(head);
      main.appendChild(wpMk("span", "wp-manual-sum", m.summary || ""));
      b.appendChild(main);
      b.appendChild(wpIcon(["M6 4.2 9.8 8 6 11.8"], "wp-manual-open"));
      b.addEventListener("click", function () {
        if (window.WsPreview && window.WsPreview.open) window.WsPreview.open(m.path, { keep: true });
      });
      li.appendChild(b);
      ul.appendChild(li);
    });
    list.appendChild(ul);
  }

  if (wproot) {
    // The working memory's cards stay open: a header click is no toggle.
    if (wpmemory) wpmemory.addEventListener("click", function (e) {
      if (e.target.closest && e.target.closest("#r-panels > details.panel > summary")) e.preventDefault();
    });
    q(".wp-seg-btn", wproot).forEach(function (b) {
      b.addEventListener("click", function () { wpMem(b.getAttribute("data-mem-tab")); });
    });
    var wpAgAll = $("wpagall");
    if (wpAgAll) wpAgAll.addEventListener("click", wpAgendaAll);
    q(".wp-tab", wproot).forEach(function (b) {
      b.addEventListener("click", function () { wpRefTab(b.getAttribute("data-ref-tab")); });
    });
    var wpRefresh = $("wpnotesrefresh");
    if (wpRefresh) wpRefresh.addEventListener("click", function (e) { e.preventDefault(); wpNotesLoad(true); });
    var wpMutedBtn = $("wpnotesmuted");
    if (wpMutedBtn) wpMutedBtn.addEventListener("click", function () {
      wpMuted = !wpMuted;
      wpMutedBtn.setAttribute("aria-pressed", String(wpMuted));
      wpNotesRender();
    });
    q(".wp-lchip", wproot).forEach(function (b) {
      b.addEventListener("click", function () { wpLayer = b.getAttribute("data-layer"); wpNotesRender(); });
    });
    var wpNotesQ = $("wpnotesq"), wpManualsQ = $("wpmanualsq");
    if (wpNotesQ) wpNotesQ.addEventListener("input", wpNotesRender);
    if (wpManualsQ) wpManualsQ.addEventListener("input", wpManualsRender);
    q(".wp-search-clr", wproot).forEach(function (b) {
      b.addEventListener("click", function () {
        var input = $(b.getAttribute("data-clear"));
        if (!input) return;
        input.value = "";
        input.dispatchEvent(new Event("input"));
        input.focus();
      });
    });
  }
  // The wait signal reads the page as rendered; the gallery's rail slot
  // joins from the microtask below.
  drawerWait();
  if (wproot) {
    // The gallery builds its pages when the board mounts, after this file
    // runs: mount and follow it from a microtask, as the ticket map does.
    if (typeof Promise === "function") Promise.resolve().then(function () {
      var C = window.WsCards;
      if (C && C.onGalleryChange) C.onGalleryChange(wpGallery);
      wpGallery();
    });
  }
