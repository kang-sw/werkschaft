/* Interactive controller for Werkschaft Gallery Workpanel (Graybox B) mockup.
   Vanilla JS, zero external dependencies, deterministic DOM updates. */
(function () {
  "use strict";

  var FX = window.WP_FIXTURES || { todos: [], agenda: {}, notes: [], manuals: [] };
  var LAYERS = ["worktree", "repo", "clone", "machine"];

  var state = {
    view: "wide",          // "wide" | "narrow" | "empty"
    theme: "dark",         // "dark" | "light"
    activePage: "workpanel",
    activeMemTab: "todo",  // used in narrow container (< 540px)
    activeRefTab: "notes", // "notes" | "manuals"
    openTodos: new Set(),
    agendaAllOpen: false,
    notesLayer: "all",
    notesQuery: "",
    showMuted: true,
    openNotes: new Set(),
    mutedIds: new Set(FX.notes.filter(function (n) { return n.muted; }).map(function (n) { return n.id; })),
    manualsQuery: "",
    refreshCount: 0,
    previewManual: null
  };

  var els = {
    root: document.documentElement,
    bgal: document.getElementById("bgal"),
    bpEdge: document.getElementById("bp-edge"),
    wpRoot: document.getElementById("wp-root"),
    widthReadout: document.getElementById("m-width-readout"),
    wpMemory: document.getElementById("wp-memory"),
    todoHost: document.getElementById("todo-list-host"),
    todoCountBadge: document.getElementById("todo-count-badge"),
    todoProgFill: document.getElementById("todo-prog-fill"),
    segTodoCount: document.getElementById("seg-todo-count"),
    segAgendaCount: document.getElementById("seg-agenda-count"),
    agendaHost: document.getElementById("agenda-tree-host"),
    agendaCountBadge: document.getElementById("agenda-count-badge"),
    agendaToggleAll: document.getElementById("agenda-toggle-all"),
    tabNotesCnt: document.getElementById("tab-notes-cnt"),
    tabManualsCnt: document.getElementById("tab-manuals-cnt"),
    panelNotes: document.getElementById("panel-notes"),
    panelManuals: document.getElementById("panel-manuals"),
    refNotesActs: document.getElementById("ref-notes-acts"),
    refManualsActs: document.getElementById("ref-manuals-acts"),
    notesSyncTs: document.getElementById("notes-sync-ts"),
    notesRefreshBtn: document.getElementById("notes-refresh-btn"),
    notesSearch: document.getElementById("notes-search"),
    notesSearchClr: document.getElementById("notes-search-clr"),
    notesMutedToggle: document.getElementById("notes-muted-toggle"),
    notesLayerChips: document.getElementById("notes-layer-chips"),
    notesHost: document.getElementById("notes-list-host"),
    manualsSearch: document.getElementById("manuals-search"),
    manualsSearchClr: document.getElementById("manuals-search-clr"),
    manualsHost: document.getElementById("manuals-list-host"),
    pvBackdrop: document.getElementById("wspv-backdrop"),
    pvSheet: document.getElementById("wspv-sheet"),
    pvTitle: document.getElementById("wspv-title"),
    pvPath: document.getElementById("wspv-path"),
    pvSummary: document.getElementById("wspv-summary"),
    pvBody: document.getElementById("wspv-body"),
    pvCopy: document.getElementById("wspv-copy"),
    pvClose: document.getElementById("wspv-close"),
    toast: document.getElementById("wp-toast")
  };

  var toastTimer = null;
  function showToast(msg) {
    els.toast.textContent = msg;
    els.toast.hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () { els.toast.hidden = true; }, 2200);
  }

  function escHtml(s) {
    return String(s)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }

  function formatInlineCode(text) {
    return escHtml(text).replace(/`([^`]+)`/g, "<code>$1</code>");
  }

  function isQuiet() {
    return state.view === "empty";
  }

  /* ------------------------------------------------------------------------
     1. Todo Renderer
     ------------------------------------------------------------------------ */
  function todoStatusIcon(st) {
    if (st === "done") {
      return '<svg class="ic-sm" viewBox="0 0 16 16" aria-hidden="true"><circle cx="8" cy="8" r="5.6"/><path d="M5.4 8.2 7.2 10 10.7 6.3"/></svg>';
    }
    if (st === "wip") {
      return '<svg class="ic-sm" viewBox="0 0 16 16" aria-hidden="true"><circle cx="8" cy="8" r="5.6" stroke-dasharray="22 10"/><circle cx="8" cy="8" r="2" fill="currentColor" stroke="none"/></svg>';
    }
    if (st === "defer") {
      return '<svg class="ic-sm" viewBox="0 0 16 16" aria-hidden="true"><circle cx="8" cy="8" r="5.6"/><path d="M6.4 6v4M9.6 6v4"/></svg>';
    }
    return '<svg class="ic-sm" viewBox="0 0 16 16" aria-hidden="true"><circle cx="8" cy="8" r="5.6"/></svg>';
  }

  function renderTodos() {
    var items = isQuiet() ? [] : FX.todos;
    var doneCount = items.filter(function (t) { return t.status === "done"; }).length;
    var total = items.length;
    var pct = total ? Math.round((doneCount / total) * 100) : 0;

    els.todoCountBadge.textContent = doneCount + "/" + total;
    els.segTodoCount.textContent = doneCount + "/" + total;
    els.todoProgFill.style.width = pct + "%";

    if (!total) {
      els.todoHost.innerHTML =
        '<div class="wp-empty">' +
          '<svg class="wp-empty-ic ic" viewBox="0 0 16 16" aria-hidden="true"><path d="M3 4.2 4.6 5.8 7.4 3"/><line x1="9.2" y1="4.4" x2="13.5" y2="4.4"/><circle cx="4.5" cy="9.2" r="1.2"/><line x1="9.2" y1="9.2" x2="13.5" y2="9.2"/><circle cx="4.5" cy="12.8" r="1.2"/><line x1="9.2" y1="12.8" x2="13.5" y2="12.8"/></svg>' +
          '<div class="wp-empty-title">No active checklist steps</div>' +
          '<div class="wp-empty-sub">Live SSE <code>#r-panels</code> will stream steps once <code>ws/todo.add</code> runs.</div>' +
        '</div>';
      return;
    }

    var html = '<ul class="todo-list">';
    items.forEach(function (item) {
      var open = state.openTodos.has(item.key);
      html +=
        '<li class="todo-item is-' + item.status + (open ? " is-open" : "") + '">' +
          '<button type="button" class="todo-row" data-todo-key="' + escHtml(item.key) + '" aria-expanded="' + (open ? "true" : "false") + '">' +
            '<span class="todo-mk">' + todoStatusIcon(item.status) + '</span>' +
            '<span class="todo-main">' +
              '<span class="todo-line1">' +
                '<span class="todo-title">' + escHtml(item.title) + '</span>' +
              '</span>' +
              '<span class="todo-meta">' +
                '<span class="todo-status st-' + item.status + '">' + escHtml(item.status) + '</span>' +
                '<span class="todo-key">{' + escHtml(item.key) + '}</span>' +
              '</span>' +
            '</span>' +
            '<svg class="todo-chev ic-xs" viewBox="0 0 12 12" aria-hidden="true"><path d="M4.2 2.8 7.8 6l-3.6 3.2"/></svg>' +
          '</button>' +
          (open ? '<div class="todo-inst">' + formatInlineCode(item.instruction) + '</div>' : "") +
        '</li>';
    });
    html += "</ul>";
    els.todoHost.innerHTML = html;
  }

  /* ------------------------------------------------------------------------
     2. Agenda Tree Renderer
     ------------------------------------------------------------------------ */
  function summarizeValue(val) {
    if (Array.isArray(val)) return "[" + val.length + " items]";
    if (val && typeof val === "object") {
      var ks = Object.keys(val);
      return "{ " + ks.slice(0, 3).join(", ") + (ks.length > 3 ? ", …" : "") + " }";
    }
    return String(val);
  }

  function renderAgendaNode(key, val, depth) {
    if (Array.isArray(val)) {
      var allPrimitives = val.every(function (x) { return typeof x !== "object" || x === null; });
      if (allPrimitives && val.length <= 4 && val.join("").length < 56) {
        return (
          '<li class="ag-leaf">' +
            '<span class="ag-key">' + escHtml(key) + '</span>' +
            '<span class="ag-val arr-inline">' +
              val.map(function (v) { return '<span class="ag-pill">' + escHtml(v) + '</span>'; }).join("") +
            '</span>' +
          '</li>'
        );
      }
      var arrInner = val.map(function (v, idx) {
        return renderAgendaNode("[" + idx + "]", v, depth + 1);
      }).join("");
      return (
        '<li>' +
          '<details' + (depth < 1 ? " open" : "") + '>' +
            '<summary class="ag-sum">' +
              '<span class="ag-key">' + escHtml(key) + '</span>' +
              '<span class="ag-fold-preview">' + escHtml(summarizeValue(val)) + '</span>' +
            '</summary>' +
            '<ul>' + arrInner + '</ul>' +
          '</details>' +
        '</li>'
      );
    }

    if (val && typeof val === "object") {
      var entries = Object.keys(val).map(function (k) {
        return renderAgendaNode(k, val[k], depth + 1);
      }).join("");
      var openAttr = depth === 0
        ? (key !== "peer_coordination" ? " open" : "")
        : (depth === 1 && Object.keys(val).length <= 3 ? " open" : "");
      return (
        '<li>' +
          '<details class="' + (depth === 0 ? "ag-blob" : "") + '"' + openAttr + '>' +
            '<summary class="ag-sum">' +
              '<span class="ag-key">' + escHtml(key) + '</span>' +
              '<span class="ag-fold-preview">' + escHtml(summarizeValue(val)) + '</span>' +
            '</summary>' +
            '<ul>' + entries + '</ul>' +
          '</details>' +
        '</li>'
      );
    }

    var cls = typeof val === "number" ? "num" : (typeof val === "boolean" ? "bool" : "str");
    return (
      '<li class="ag-leaf">' +
        '<span class="ag-key">' + escHtml(key) + '</span>' +
        '<span class="ag-val ' + cls + '">' + escHtml(String(val)) + '</span>' +
      '</li>'
    );
  }

  function renderAgenda() {
    var blobKeys = isQuiet() ? [] : Object.keys(FX.agenda);
    els.agendaCountBadge.textContent = String(blobKeys.length);
    els.segAgendaCount.textContent = String(blobKeys.length);

    if (!blobKeys.length) {
      els.agendaHost.innerHTML =
        '<div class="wp-empty">' +
          '<svg class="wp-empty-ic ic" viewBox="0 0 16 16" aria-hidden="true"><path d="M3 3h4.5v3.5H3zM9.5 3H13v3.5H9.5zM3 9.5h10V13H3z"/></svg>' +
          '<div class="wp-empty-title">Working memory is clear</div>' +
          '<div class="wp-empty-sub">Session blobs written via <code>ws/agenda.set</code> appear here live.</div>' +
        '</div>';
      return;
    }

    var html = '<ul class="ag-tree">';
    blobKeys.forEach(function (k) {
      html += renderAgendaNode(k, FX.agenda[k], 0);
    });
    html += "</ul>";
    els.agendaHost.innerHTML = html;
  }

  /* ------------------------------------------------------------------------
     3. Notes Renderer (4 Layers + Search + Muted + Expand)
     ------------------------------------------------------------------------ */
  function renderNotes() {
    var source = isQuiet() ? [] : FX.notes;
    var q = state.notesQuery.trim().toLowerCase();

    // Update layer chip counts against current search + muted visibility
    var baseMatches = source.filter(function (n) {
      var muted = state.mutedIds.has(n.id);
      if (!state.showMuted && muted) return false;
      if (q && n.key.toLowerCase().indexOf(q) === -1 && n.body.toLowerCase().indexOf(q) === -1 && n.layer.indexOf(q) === -1) {
        return false;
      }
      return true;
    });

    var counts = { all: baseMatches.length, worktree: 0, repo: 0, clone: 0, machine: 0 };
    baseMatches.forEach(function (n) { counts[n.layer] = (counts[n.layer] || 0) + 1; });

    Object.keys(counts).forEach(function (ly) {
      var el = els.notesLayerChips.querySelector('[data-lcnt="' + ly + '"]');
      if (el) el.textContent = String(counts[ly]);
    });

    var visible = baseMatches.filter(function (n) {
      return state.notesLayer === "all" || n.layer === state.notesLayer;
    });

    els.tabNotesCnt.textContent = isQuiet()
      ? "0"
      : (q || state.notesLayer !== "all" || !state.showMuted
          ? visible.length + "/" + source.length
          : String(source.length));

    els.notesSearchClr.hidden = !state.notesQuery.length;

    if (!visible.length) {
      var title = isQuiet() ? "No notes recorded yet" : "No matching notes";
      var sub = isQuiet()
        ? "Notes from <code>worktree</code>, <code>repo</code>, <code>clone</code>, and <code>machine</code> layers will appear after refresh."
        : "Try clearing the search query or switching back to the <code>All</code> layer filter.";
      els.notesHost.innerHTML =
        '<div class="wp-empty">' +
          '<svg class="wp-empty-ic ic" viewBox="0 0 16 16" aria-hidden="true"><path d="M3.2 2.5h9.6v8.2L9.8 13.5H3.2z"/><path d="M9.8 10.7v2.8M9.8 10.7h3"/></svg>' +
          '<div class="wp-empty-title">' + title + '</div>' +
          '<div class="wp-empty-sub">' + sub + '</div>' +
        '</div>';
      return;
    }

    var layersToShow = state.notesLayer === "all" ? LAYERS : [state.notesLayer];
    var html = '<div class="notes-groups">';

    layersToShow.forEach(function (ly) {
      var groupItems = visible.filter(function (n) { return n.layer === ly; });
      if (!groupItems.length) return;
      groupItems.sort(function (a, b) { return b.priority - a.priority; });

      html += '<div class="notes-group">';
      if (state.notesLayer === "all") {
        html +=
          '<div class="notes-group-hd">' +
            '<span class="notes-group-dot" data-ly="' + ly + '" aria-hidden="true"></span>' +
            '<span>' + ly + '</span>' +
            '<span class="tabular">(' + groupItems.length + ')</span>' +
          '</div>';
      }

      groupItems.forEach(function (n) {
        var muted = state.mutedIds.has(n.id);
        var open = state.openNotes.has(n.id);
        var prioCls = n.priority === 0 ? " is-zero" : "";
        html +=
          '<article class="note-row' + (muted ? " is-muted" : "") + (open ? " is-open" : "") + '" data-note-id="' + escHtml(n.id) + '">' +
            '<div class="note-top">' +
              '<span class="note-prio tabular' + prioCls + '" title="Note priority">P' + n.priority + '</span>' +
              '<span class="note-ly-tag" data-ly="' + ly + '">' + ly + '</span>' +
              '<span class="note-key" title="' + escHtml(n.key) + '">' + escHtml(n.key) + '</span>' +
              (muted ? '<span class="note-muted-pill">muted</span>' : "") +
              '<span class="note-age tabular">' + escHtml(n.age) + '</span>' +
              '<button type="button" class="note-mute-btn" data-mute-id="' + escHtml(n.id) + '" title="' + (muted ? "Unmute note" : "Mute note") + '" aria-label="' + (muted ? "Unmute note" : "Mute note") + '">' +
                (muted
                  ? '<svg class="ic-xs" viewBox="0 0 16 16" aria-hidden="true"><path d="M2.5 2.5 13.5 13.5M6.2 4.4A5.8 5.8 0 0 1 8 4.1c3.7 0 6 3.9 6 3.9a9 9 0 0 1-1.9 2.3M9.8 11.6A5.8 5.8 0 0 1 8 11.9C4.3 11.9 2 8 2 8a9.2 9.2 0 0 1 2.2-2.6"/></svg>'
                  : '<svg class="ic-xs" viewBox="0 0 16 16" aria-hidden="true"><path d="M2 8s2.3-3.8 6-3.8S14 8 14 8s-2.3 3.8-6 3.8S2 8 2 8z"/><circle cx="8" cy="8" r="1.5"/></svg>') +
              '</button>' +
            '</div>' +
            '<button type="button" class="note-body-btn" data-expand-note="' + escHtml(n.id) + '" aria-expanded="' + (open ? "true" : "false") + '">' +
              escHtml(n.body) +
            '</button>' +
          '</article>';
      });

      html += "</div>";
    });

    html += "</div>";
    els.notesHost.innerHTML = html;
  }

  /* ------------------------------------------------------------------------
     4. Manuals Renderer & Simulated WsPreview
     ------------------------------------------------------------------------ */
  function renderManuals() {
    var q = state.manualsQuery.trim().toLowerCase();
    var all = FX.manuals;
    var visible = all.filter(function (m) {
      if (!q) return true;
      return m.stem.toLowerCase().indexOf(q) !== -1 ||
        m.file.toLowerCase().indexOf(q) !== -1 ||
        m.summary.toLowerCase().indexOf(q) !== -1;
    });

    els.tabManualsCnt.textContent = q ? visible.length + "/" + all.length : String(all.length);
    els.manualsSearchClr.hidden = !state.manualsQuery.length;

    if (!visible.length) {
      els.manualsHost.innerHTML =
        '<div class="wp-empty">' +
          '<svg class="wp-empty-ic ic" viewBox="0 0 16 16" aria-hidden="true"><path d="M3 2.8h6.2L13 6.5v6.7H3z"/><path d="M9.2 2.8v3.7H13"/></svg>' +
          '<div class="wp-empty-title">No manuals match "' + escHtml(state.manualsQuery) + '"</div>' +
          '<div class="wp-empty-sub">Clear the filter to browse all 13 docs in <code>ai-docs/manuals/</code>.</div>' +
        '</div>';
      return;
    }

    var html = '<ul class="manual-list">';
    visible.forEach(function (m) {
      html +=
        '<li>' +
          '<button type="button" class="manual-btn" data-manual-stem="' + escHtml(m.stem) + '">' +
            '<svg class="ic-sm manual-ic" viewBox="0 0 16 16" aria-hidden="true"><path d="M3 2.5h6.4L13 6.1v7.4H3z"/><path d="M9.4 2.5v3.6H13M5.4 8.5h5.2M5.4 10.9h3.6"/></svg>' +
            '<span class="manual-main">' +
              '<span class="manual-head">' +
                '<span class="manual-stem">' + escHtml(m.stem) + '</span>' +
                '<span class="manual-ext">.md</span>' +
              '</span>' +
              '<span class="manual-sum">' + escHtml(m.summary) + '</span>' +
            '</span>' +
            '<svg class="ic-xs manual-open-ic" viewBox="0 0 12 12" aria-hidden="true"><path d="M4.2 2.8 7.8 6l-3.6 3.2"/></svg>' +
          '</button>' +
        '</li>';
    });
    html += "</ul>";
    els.manualsHost.innerHTML = html;
  }

  function openManualPreview(stem) {
    var m = FX.manuals.find(function (x) { return x.stem === stem; });
    if (!m) return;
    state.previewManual = m;
    els.pvTitle.textContent = m.file;
    els.pvPath.textContent = "ai-docs/manuals/" + m.file;
    els.pvSummary.innerHTML = "<strong>summary:</strong>" + formatInlineCode(m.summary);

    var bodyHtml = "";
    (m.sections || []).forEach(function (sec) {
      if (sec.h) bodyHtml += "<h3>" + escHtml(sec.h) + "</h3>";
      if (sec.p) bodyHtml += "<p>" + formatInlineCode(sec.p) + "</p>";
      if (sec.code) bodyHtml += "<pre>" + escHtml(sec.code) + "</pre>";
      if (sec.list) {
        bodyHtml += "<ul>" + sec.list.map(function (li) {
          return "<li>" + formatInlineCode(li) + "</li>";
        }).join("") + "</ul>";
      }
    });
    els.pvBody.innerHTML = bodyHtml;
    els.pvBackdrop.hidden = false;
    els.pvSheet.focus();
  }

  function closeManualPreview() {
    state.previewManual = null;
    els.pvBackdrop.hidden = true;
  }

  /* ------------------------------------------------------------------------
     5. View, Theme, Gallery Rail & Resize Controllers
     ------------------------------------------------------------------------ */
  function setView(view) {
    state.view = view;
    els.root.dataset.view = view;
    els.root.dataset.state = view === "empty" ? "empty" : "populated";
    els.bgal.style.removeProperty("--gal-w");

    document.querySelectorAll("[data-view-btn]").forEach(function (btn) {
      var on = btn.dataset.viewBtn === view;
      btn.classList.toggle("is-on", on);
      btn.setAttribute("aria-selected", on ? "true" : "false");
    });

    if (state.activePage !== "workpanel") setGalleryPage("workpanel");
    renderTodos();
    renderAgenda();
    renderNotes();
    renderManuals();
    requestAnimationFrame(updateWidthReadout);
  }

  function setTheme(theme) {
    state.theme = theme;
    els.root.dataset.theme = theme;
    document.querySelectorAll("[data-theme-btn]").forEach(function (btn) {
      var on = btn.dataset.themeBtn === theme;
      btn.classList.toggle("is-on", on);
      btn.setAttribute("aria-pressed", on ? "true" : "false");
    });
  }

  function setGalleryPage(page) {
    state.activePage = page;
    document.querySelectorAll(".gal-page[data-page]").forEach(function (p) {
      var on = p.dataset.page === page;
      p.hidden = !on;
      if (on) p.setAttribute("data-active", "");
      else p.removeAttribute("data-active");
    });
    document.querySelectorAll(".gal-dot[data-dot-page]").forEach(function (d) {
      var on = d.dataset.dotPage === page;
      d.classList.toggle("active", on);
      d.setAttribute("aria-selected", on ? "true" : "false");
    });
  }

  function setMemTab(tab) {
    state.activeMemTab = tab;
    els.wpMemory.dataset.activeMem = tab;
    document.querySelectorAll("[data-mem-tab]").forEach(function (btn) {
      var on = btn.dataset.memTab === tab;
      btn.classList.toggle("is-on", on);
      btn.setAttribute("aria-selected", on ? "true" : "false");
    });
  }

  function setRefTab(tab) {
    state.activeRefTab = tab;
    document.querySelectorAll("[data-ref-tab]").forEach(function (btn) {
      var on = btn.dataset.refTab === tab;
      btn.classList.toggle("is-on", on);
      btn.setAttribute("aria-selected", on ? "true" : "false");
    });
    els.panelNotes.hidden = tab !== "notes";
    els.panelManuals.hidden = tab !== "manuals";
    els.refNotesActs.hidden = tab !== "notes";
    els.refManualsActs.hidden = tab !== "manuals";
  }

  function updateWidthReadout() {
    if (!els.wpRoot || !els.widthReadout) return;
    var w = Math.round(els.wpRoot.getBoundingClientRect().width);
    var mode = w >= 540 ? "2-col" : "narrow-seg";
    els.widthReadout.textContent = w + "px · " + mode;
  }

  /* ------------------------------------------------------------------------
     6. Event Bindings
     ------------------------------------------------------------------------ */
  function bindEvents() {
    // Mockup bar view + theme
    document.querySelectorAll("[data-view-btn]").forEach(function (btn) {
      btn.addEventListener("click", function () { setView(btn.dataset.viewBtn); });
    });
    document.querySelectorAll("[data-theme-btn]").forEach(function (btn) {
      btn.addEventListener("click", function () { setTheme(btn.dataset.themeBtn); });
    });

    // Gallery rail dots & prev/next (Home -> User pins -> System panels)
    var pageOrder = ["home", "p1", "workpanel", "ticketmap", "git"];
    document.querySelectorAll(".gal-dot[data-dot-page]").forEach(function (dot) {
      dot.addEventListener("click", function () { setGalleryPage(dot.dataset.dotPage); });
    });
    document.querySelectorAll("[data-goto-page]").forEach(function (btn) {
      btn.addEventListener("click", function () { setGalleryPage(btn.dataset.gotoPage); });
    });
    document.getElementById("gal-prev").addEventListener("click", function () {
      var idx = pageOrder.indexOf(state.activePage);
      setGalleryPage(pageOrder[(idx - 1 + pageOrder.length) % pageOrder.length]);
    });
    document.getElementById("gal-next").addEventListener("click", function () {
      var idx = pageOrder.indexOf(state.activePage);
      setGalleryPage(pageOrder[(idx + 1) % pageOrder.length]);
    });
    document.getElementById("gal-plus-btn").addEventListener("click", function () {
      showToast("New gallery page drop slot (+)");
    });

    // Narrow working-memory tab switch
    document.querySelectorAll("[data-mem-tab]").forEach(function (btn) {
      btn.addEventListener("click", function () { setMemTab(btn.dataset.memTab); });
    });

    // Todo expand/collapse instructions
    els.todoHost.addEventListener("click", function (ev) {
      var btn = ev.target.closest("[data-todo-key]");
      if (!btn) return;
      var k = btn.dataset.todoKey;
      if (state.openTodos.has(k)) state.openTodos.delete(k);
      else state.openTodos.add(k);
      renderTodos();
    });

    // Agenda expand/collapse all
    els.agendaToggleAll.addEventListener("click", function () {
      var details = els.agendaHost.querySelectorAll("details");
      if (!details.length) return;
      var anyClosed = Array.prototype.some.call(details, function (d) { return !d.open; });
      details.forEach(function (d) { d.open = anyClosed; });
    });

    // Lower reference tabs
    document.querySelectorAll("[data-ref-tab]").forEach(function (btn) {
      btn.addEventListener("click", function () { setRefTab(btn.dataset.refTab); });
    });

    // Notes layer chips
    els.notesLayerChips.addEventListener("click", function (ev) {
      var chip = ev.target.closest("[data-layer]");
      if (!chip) return;
      state.notesLayer = chip.dataset.layer;
      els.notesLayerChips.querySelectorAll("[data-layer]").forEach(function (c) {
        var on = c.dataset.layer === state.notesLayer;
        c.classList.toggle("is-on", on);
        c.setAttribute("aria-pressed", on ? "true" : "false");
      });
      renderNotes();
    });

    // Notes search & clear
    els.notesSearch.addEventListener("input", function () {
      state.notesQuery = els.notesSearch.value;
      renderNotes();
    });
    els.notesSearchClr.addEventListener("click", function () {
      state.notesQuery = "";
      els.notesSearch.value = "";
      renderNotes();
      els.notesSearch.focus();
    });

    // Notes show/hide muted toggle
    els.notesMutedToggle.addEventListener("click", function () {
      state.showMuted = !state.showMuted;
      els.notesMutedToggle.setAttribute("aria-pressed", state.showMuted ? "true" : "false");
      renderNotes();
    });

    // Notes explicit Refresh
    els.notesRefreshBtn.addEventListener("click", function () {
      els.notesRefreshBtn.classList.add("is-busy");
      els.notesRefreshBtn.disabled = true;
      setTimeout(function () {
        state.refreshCount += 1;
        els.notesRefreshBtn.classList.remove("is-busy");
        els.notesRefreshBtn.disabled = false;
        var now = new Date();
        var hh = String(now.getHours()).padStart(2, "0");
        var mm = String(now.getMinutes()).padStart(2, "0");
        var ss = String(now.getSeconds()).padStart(2, "0");
        els.notesSyncTs.textContent = "synced " + hh + ":" + mm + ":" + ss;
        showToast("GET /s/42/notes · " + (isQuiet() ? "0" : FX.notes.length) + " notes synced");
      }, 360);
    });

    // Notes expand body & mute individual note
    els.notesHost.addEventListener("click", function (ev) {
      var muteBtn = ev.target.closest("[data-mute-id]");
      if (muteBtn) {
        var mid = muteBtn.dataset.muteId;
        if (state.mutedIds.has(mid)) {
          state.mutedIds.delete(mid);
          showToast("Note unmuted");
        } else {
          state.mutedIds.add(mid);
          showToast("Note muted");
        }
        renderNotes();
        return;
      }

      var expBtn = ev.target.closest("[data-expand-note]");
      if (expBtn) {
        var nid = expBtn.dataset.expandNote;
        if (state.openNotes.has(nid)) state.openNotes.delete(nid);
        else state.openNotes.add(nid);
        renderNotes();
      }
    });

    // Manuals search & click to preview
    els.manualsSearch.addEventListener("input", function () {
      state.manualsQuery = els.manualsSearch.value;
      renderManuals();
    });
    els.manualsSearchClr.addEventListener("click", function () {
      state.manualsQuery = "";
      els.manualsSearch.value = "";
      renderManuals();
      els.manualsSearch.focus();
    });
    els.manualsHost.addEventListener("click", function (ev) {
      var btn = ev.target.closest("[data-manual-stem]");
      if (!btn) return;
      openManualPreview(btn.dataset.manualStem);
    });

    // WsPreview close / copy / Escape
    els.pvClose.addEventListener("click", closeManualPreview);
    els.pvBackdrop.addEventListener("click", function (ev) {
      if (ev.target === els.pvBackdrop) closeManualPreview();
    });
    els.pvCopy.addEventListener("click", function () {
      if (!state.previewManual) return;
      var rel = "ai-docs/manuals/" + state.previewManual.file;
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(rel).catch(function () {});
      }
      showToast("Copied: " + rel);
    });
    document.addEventListener("keydown", function (ev) {
      if (ev.key === "Escape" && !els.pvBackdrop.hidden) {
        closeManualPreview();
      }
    });

    // Interactive left-edge resize drag (.bp-edge) to test @container breakpoint smoothly
    var dragging = false;
    var startX = 0;
    var startW = 780;
    els.bpEdge.addEventListener("pointerdown", function (ev) {
      dragging = true;
      startX = ev.clientX;
      startW = els.bgal.getBoundingClientRect().width;
      els.bgal.classList.add("is-dragging");
      els.bpEdge.classList.add("drag");
      els.bpEdge.setPointerCapture(ev.pointerId);
    });
    els.bpEdge.addEventListener("pointermove", function (ev) {
      if (!dragging) return;
      var delta = startX - ev.clientX;
      var nextW = Math.max(320, Math.min(920, Math.round(startW + delta)));
      els.bgal.style.setProperty("--gal-w", nextW + "px");
      updateWidthReadout();
    });
    function endDrag() {
      if (!dragging) return;
      dragging = false;
      els.bgal.classList.remove("is-dragging");
      els.bpEdge.classList.remove("drag");
    }
    els.bpEdge.addEventListener("pointerup", endDrag);
    els.bpEdge.addEventListener("pointercancel", endDrag);

    if (typeof ResizeObserver !== "undefined") {
      new ResizeObserver(updateWidthReadout).observe(els.wpRoot);
    }
  }

  // Initial render
  bindEvents();
  setTheme("dark");
  setView("wide");
  setRefTab("notes");
})();
