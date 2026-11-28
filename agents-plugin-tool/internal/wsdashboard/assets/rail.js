(function () {
  // The session rail's browser-side state, window.WsRail, for the session
  // page's script (dashboard.js) and the iframe shell's first paint:
  // per-session drafts, the collapsed workspaces, parents, and ended folds
  // left open, and the rail's client-side marks (the viewed session's row,
  // the revive entry in flight), with the drag's refusal, re-find, and
  // swap-hold rules, the tree's drop zones (which the keyboard path's moves
  // and the context menu's "move under" read too), and the rail layout
  // route's answers. The daemon keeps the rows' order and tree, the stars,
  // and the workspace order (its rail layout, POST /rail/layout) and renders
  // the rail in that order; the page only reads a row's star and place from
  // it. The drafts and the collapsed and open sets are in localStorage
  // under ws.dashboard., through the store the caller passes ({ load(key),
  // save(key, value or null) }, which must not throw), so the page works
  // the same with storage absent; the retired local order and stars are
  // read there once, for the layout's import. The DOM helpers touch only
  // the elements they are given; the revive route and hand-off notice, the
  // popover and delete modal helpers at the end, and the approval card's
  // input preview after them, are pure.
  var DRAFT = "ws.dashboard.draft.", DRAFTS = "ws.dashboard.drafts";
  var STARS = "ws.dashboard.stars", COLLAPSED = "ws.dashboard.railcol";
  var ORDER = "ws.dashboard.railorder", WSORDER = "ws.dashboard.wsorder";
  var ENDED = "ws.dashboard.railended";
  // DRAFT_CAP bounds the sessions whose drafts are kept; the oldest saved
  // goes first.
  var DRAFT_CAP = 50;

  function parse(raw, def) {
    if (raw === null || raw === undefined) return def;
    try { var v = JSON.parse(raw); return v === null || v === undefined ? def : v; } catch (e) { return def; }
  }
  function strings(v) { return Array.isArray(v) ? v.filter(function (x) { return typeof x === "string"; }) : []; }
  function keys(o) { return o && typeof o === "object" ? Object.keys(o) : []; }

  // Admission drafts keep the original observation revision until the user
  // explicitly clears/reselects. A reload or server render is not fresh consent.
  function admissionDrafts(store) {
    var key = "ws.dashboard.worktree-admission.drafts";
    return {
      load: function () {
        var v = parse(store.load(key), []);
        return Array.isArray(v) ? v.filter(function (s) {
          return s && typeof s.repository === "string" && typeof s.path === "string" && typeof s.revision === "string" &&
            (s.action === "approve" || s.action === "reject" || s.action === "undo");
        }) : [];
      },
      save: function (v) { store.save(key, v.length ? JSON.stringify(v) : null); }
    };
  }

  // A draft is one session's unsent input and view state: text and
  // mentions ({start, end, text}) of the composer, sel its selection
  // ([start, end]), cmd its slash command selection ({name, args}: args a
  // string when the capsule holds them, else null), answers the board answers by item (readAnswers' shape,
  // topic notes among them), chips the answer chips, bmentions the board
  // mentions ({topic, id?, text}), wpicks and wconsoles the web card's
  // picked elements ({url, selector, text}) and console blocks ({url,
  // lines}), refs the images that
  // reference written attachment files ({ref, name, n}), lost the numbers
  // of the pasted images it could not keep, open the open details by key, and tab
  // the tab shown. emptyDraft reports one that holds nothing to restore.
  function emptyDraft(d) {
    if (!d || typeof d !== "object") return true;
    return !d.text && !d.cmd && !(d.chips && d.chips.length) && !(d.bmentions && d.bmentions.length) && !(d.closeRequests && d.closeRequests.length) &&
      !(d.wpicks && d.wpicks.length) && !(d.wconsoles && d.wconsoles.length) && !keys(d.answers).length && !(d.refs && d.refs.length) &&
      !(d.textRefs && d.textRefs.length) && !(d.lost && d.lost.length) && !keys(d.open).length && (!d.tab || d.tab === "convo");
  }

  // drafts is the draft store: load(rid), save(rid, draft) (an empty draft
  // clears), and clear(rid). Saves return the storage adapter's write result
  // (false means failure). Beyond cap sessions the oldest saved is dropped.
  function drafts(store, cap) {
    cap = cap || DRAFT_CAP;
    function index() { return strings(parse(store.load(DRAFTS), [])); }
    function clear(rid) {
      var saved = store.save(DRAFT + rid, null);
      var ids = index(), rest = ids.filter(function (x) { return x !== rid; });
      if (rest.length !== ids.length) store.save(DRAFTS, rest.length ? JSON.stringify(rest) : null);
      return saved;
    }
    return {
      load: function (rid) {
        var d = parse(store.load(DRAFT + rid), null);
        return d && typeof d === "object" && !Array.isArray(d) ? d : null;
      },
      save: function (rid, d) {
        if (emptyDraft(d)) return clear(rid);
        var ids = index().filter(function (x) { return x !== rid; });
        ids.push(rid);
        while (ids.length > cap) store.save(DRAFT + ids.shift(), null);
        store.save(DRAFTS, JSON.stringify(ids));
        return store.save(DRAFT + rid, JSON.stringify(d));
      },
      clear: clear
    };
  }

  // stripMarkers removes each image marker ("[Image #n]" and the space after
  // it) naming one of nums from text, moving the mentions after a removed
  // marker with the text and dropping one a removal cuts into.
  function stripMarkers(text, mentions, nums) {
    var want = {};
    (nums || []).forEach(function (n) { want[n] = true; });
    var re = /\[Image #([1-9][0-9]{0,5})\] ?/g, m, cuts = [];
    while ((m = re.exec(text))) if (want[+m[1]]) cuts.push([m.index, m.index + m[0].length]);
    if (!cuts.length) return { text: text, mentions: (mentions || []).slice() };
    var out = "", at = 0;
    cuts.forEach(function (c) { out += text.slice(at, c[0]); at = c[1]; });
    out += text.slice(at);
    var kept = [];
    (mentions || []).forEach(function (mn) {
      var shift = 0, cut = false;
      cuts.forEach(function (c) {
        if (c[1] <= mn.start) shift += c[1] - c[0];
        else if (c[0] < mn.end) cut = true;
      });
      if (!cut) kept.push({ start: mn.start - shift, end: mn.end - shift, text: mn.text });
    });
    return { text: out, mentions: kept };
  }

  // A set of strings kept as one JSON array: has(x), toggle(x) returning
  // the new state, put(x, on), list().
  function stringSet(store, key) {
    function list() { return strings(parse(store.load(key), [])); }
    function put(x, on) {
      var l = list(), i = l.indexOf(x);
      if ((i >= 0) === !!on) return;
      if (i >= 0) l.splice(i, 1); else l.push(x);
      store.save(key, l.length ? JSON.stringify(l) : null);
    }
    return {
      list: list,
      has: function (x) { return list().indexOf(x) >= 0; },
      toggle: function (x) {
        var on = list().indexOf(x) < 0;
        put(x, on);
        return on;
      },
      put: put
    };
  }
  // collapsed is the collapsed workspaces' paths; endedOpen the fold rows'
  // open lists, by FOLD_KEYS (marks).
  function collapsed(store) { return stringSet(store, COLLAPSED); }
  function endedOpen(store) { return stringSet(store, ENDED); }

  // LEGACY are the retired local layout's keys (the starred sessions, the
  // sessions' drag order by reply-id, the workspaces' by path), and what
  // the import op calls each.
  var LEGACY = [[ORDER, "order"], [STARS, "stars"], [WSORDER, "wsorder"]];
  function legacyClear(store) { LEGACY.forEach(function (k) { store.save(k[0], null); }); }
  // legacyImport is the rail layout's import of the retired local layout
  // held in store, for a page whose rail reports the server layout empty
  // (empty); null when there is nothing to import. A server layout that
  // holds an entry wins: the local keys are cleared, never imported.
  function legacyImport(store, empty) {
    var body = { op: "import" }, any = false;
    LEGACY.forEach(function (k) {
      var v = strings(parse(store.load(k[0]), []));
      body[k[1]] = v;
      if (store.load(k[0]) !== null) any = true;
    });
    if (!any) return null;
    if (!empty) { legacyClear(store); return null; }
    return body;
  }
  // legacyDone takes the import's answer: an import applied, or one the
  // server did not apply (another browser's came first), clears the local
  // keys; any other answer (retry: the daemon has not read its registry
  // yet; an unreachable daemon) keeps them for a later load.
  function legacyDone(store, answer) {
    var a = answer || {};
    if (a.applied === true || a.reason === "not_applied") legacyClear(store);
  }

  // LAYOUT_REFUSALS maps the rail layout route's refusal reasons (the
  // layout* codes in rail_layout.go) to their strings; the drag's own
  // refusals (dropRefusal) and later the tree's and the keyboard's name
  // the same codes. layoutRefusal is a reason's string key, an unknown or
  // missing reason's the generic one.
  var LAYOUT_REFUSALS = {
    unknown: "js.rail.layout.unknown", ended: "js.rail.layout.ended", self: "js.rail.layout.self",
    depth: "js.rail.layout.depth", workspace: "js.rail.layout.workspace", group: "js.rail.layout.group",
    level: "js.rail.layout.level", stars: "js.rail.layout.stars", child: "js.rail.layout.child",
    toplevel: "js.rail.layout.toplevel", invalid: "js.rail.layout.invalid", retry: "js.rail.layout.retry",
    store: "js.rail.layout.store", hiring: "js.rail.layout.hiring",
    control: "js.rail.drag.control", child_life: "js.rail.drag.child",
    hosted: "js.rail.drag.hosted", blocked: "js.rail.drag.blocked"
  };
  function layoutRefusal(reason) {
    return LAYOUT_REFUSALS.hasOwnProperty(reason) ? LAYOUT_REFUSALS[reason] : LAYOUT_REFUSALS.invalid;
  }
  // staleRail reports a rail render (its HTML) older than the layout
  // generation min: its list's data-lgen is below it. A render without one
  // is never stale.
  function staleRail(html, min) {
    var m = /\bdata-lgen="([0-9]+)"/.exec(typeof html === "string" ? html : html && html.shell ? html.shell.join("") : "");
    return !!m && +m[1] < min;
  }

  function hasClass(el, c) { return (" " + (el.className || "") + " ").indexOf(" " + c + " ") >= 0; }
  // starred reads the favorite state rendered on the session row.
  function starred(el) {
    return !!el && el.getAttribute("data-star") === "1";
  }

  // markAttnResync flags a moved session row (or rows inside a moved container)
  // so attention.mark() re-syncs its CSS animation phase after DOM reinsertion.
  function markAttnResync(n) {
    if (!n) return;
    if (hasClass(n, "rs")) { n.wsAttnResync = true; return; }
    if (n.querySelectorAll) {
      Array.prototype.forEach.call(n.querySelectorAll(".rs"), function (r) { r.wsAttnResync = true; });
    }
  }

  // drop moves item (a live session row, worktree group or workspace section) next to
  // ref, a sibling of its kind, before it or (after) after it, and reports
  // whether it moved. It refuses a ref in another list (a session never
  // leaves its own group), a ref of another kind or on the other side of
  // the stars, an ended row, and the item itself.
  function drop(item, ref, after) {
    if (!item || !ref || item === ref || !item.parentNode || ref.parentNode !== item.parentNode) return false;
    var kind = hasClass(item, "rws") ? "rws" : hasClass(item, "rwt") ? "rwt" : "rs";
    if (!hasClass(ref, kind) || hasClass(item, "ended") || hasClass(ref, "ended") || (kind === "rs" && starred(item) !== starred(ref))) return false;
    item.parentNode.insertBefore(item, after ? ref.nextSibling : ref);
    markAttnResync(item);
    return true;
  }

  // up is el or its nearest ancestor of class c, or null.
  function up(el, c) {
    for (var n = el; n; n = n.parentNode) if (hasClass(n, c)) return n;
    return null;
  }
  // dropRefusal names why a dragged session row cannot land where the
  // pointer is (over, the element under it), as a layout refusal reason
  // (LAYOUT_REFUSALS): "workspace" over another workspace, "group" over
  // another worktree than its own group's (a child's own is where it would
  // go top-level, not its parent's), "stars" over a live row of its own
  // list on the other side of the stars; "" anywhere else (its own group,
  // a header, the ended fold, or outside the list, where the drop takes the
  // nearest place). A workspace section is never refused.
  function dropRefusal(item, over) {
    if (!item || !over || hasClass(item, "rws")) return "";
    var ws = up(over, "rws");
    if (!ws) return "";
    if (ws !== up(item, "rws")) return "workspace";
    if (hasClass(item, "rwt")) return "";
    var wt = up(over, "rwt");
    if (wt && wt !== homeWt(item)) return "group";
    var row = up(over, "rs");
    if (row && row !== item && row.parentNode === item.parentNode && !hasClass(row, "ended") && starred(row) !== starred(item)) return "stars";
    return "";
  }

  // The tree: a child row sits in its parent's children box (.rkids, the
  // parent row's next sibling, data-parent the parent's reply-id) and
  // carries data-g, the path of its own worktree group; a top-level row's
  // group is its worktree's (.rwt data-g), or its plain-folder workspace's
  // path. data-kids marks a stored parent (of a live or an ended child),
  // which never becomes a child; kcol marks a parent the page shows
  // collapsed (kidsCollapsed, the parents' reply-ids, per browser).
  var KIDS_COLLAPSED = "ws.dashboard.railkids";
  function kidsCollapsed(store) { return stringSet(store, KIDS_COLLAPSED); }
  function attr(el, k) { return el && el.getAttribute ? el.getAttribute(k) : null; }
  function kidsOf(row) {
    var k = row && row.nextSibling;
    return k && hasClass(k, "rkids") ? k : null;
  }
  function rowGroup(row) {
    var g = attr(row, "data-g");
    if (g !== null) return g;
    var wt = up(row, "rwt"), ws = up(row, "rws");
    return wt ? attr(wt, "data-g") || "" : attr(ws, "data-ws") || "";
  }
  // rowFacts is what a drop reads of a session row: its reply-id, its
  // workspace and group, live, its parent (a child's), kids (a stored
  // parent), star (its rendered star), and first, its first child while
  // its children show.
  function rowFacts(row) {
    var box = row.parentNode, kids = kidsOf(row), ws = up(row, "rws");
    return {
      rid: attr(row, "data-rid") || "", ws: attr(ws, "data-ws") || "", group: rowGroup(row),
      live: !hasClass(row, "ended"), parent: box && hasClass(box, "rkids") ? attr(box, "data-parent") || "" : "",
      kids: attr(row, "data-kids") === "1", star: starred(row),
      first: kids && !hasClass(row, "kcol") && kids.children.length ? attr(kids.children[0], "data-rid") || "" : ""
    };
  }
  // dropZone is where the dragged session row d lands over the row t (both
  // rowFacts) with the pointer at frac of t's height (0 its top, 1 its
  // bottom): { zone, ref, op }, op the layout op the drop posts and ref the
  // reply-id the zone is drawn against: the line before or after that row,
  // or (zone "child") the parent outlined. A place the route would refuse
  // is { refusal }, the route's reason code (LAYOUT_REFUSALS); d itself
  // and its own children are no target (null). The middle half of a valid
  // parent (top-level, live, d's workspace, d no parent itself) attaches d
  // as its last child, its quarters are before and after; any other row
  // splits in halves. A line among a parent's children attaches d there
  // (among its own siblings it moves); a line at the top level moves a
  // top-level row (its own group, its side of the stars) or detaches a
  // child (its own group only; the route checks no stars for a detach).
  // The line after a parent whose children show is the line before its
  // first child. An orphan shows top-level, so it is a target as one.
  // With halves every row splits in halves (no child zone): the pointer
  // drag's line, whose child zone arms only after a dwell.
  function dropZone(d, t, frac, halves) {
    if (!d || !t || !d.rid || t.rid === d.rid || t.parent === d.rid) return null;
    var parentOK = !t.parent && t.live && t.ws === d.ws && !d.kids;
    var zone = parentOK && !halves ? (frac < 0.25 ? "before" : frac > 0.75 ? "after" : "child") : (frac < 0.5 ? "before" : "after");
    if (zone === "child") return { zone: zone, ref: t.rid, op: { op: "attach", rid: d.rid, parent: t.rid } };
    if (zone === "after" && parentOK && t.first) {
      return dropZone(d, { rid: t.first, ws: t.ws, group: "", live: true, parent: t.rid, kids: false, star: false, first: "" }, 0);
    }
    if (!t.live) return { refusal: "ended" };
    if (t.ws !== d.ws) return { refusal: "workspace" };
    var op;
    if (t.parent) {
      if (d.kids) return { refusal: "depth" };
      op = d.parent === t.parent ? { op: "move", rid: d.rid } : { op: "attach", rid: d.rid, parent: t.parent };
    } else {
      if (t.group !== d.group) return { refusal: "group" };
      if (!d.parent && t.star !== d.star) return { refusal: "stars" };
      op = { op: d.parent ? "detach" : "move", rid: d.rid };
    }
    op[zone] = t.rid;
    return { zone: zone, ref: t.rid, op: op };
  }
  function rowsIn(box) {
    return box ? Array.prototype.filter.call(box.children, function (c) { return hasClass(c, "rs"); }) : [];
  }
  // dropNoop reports a drop (z, from dropZone) that leaves the dragged row
  // item where it stands: a move right next to it, on the side it is on, or
  // an attach to its own parent while it is already the last child.
  function dropNoop(item, z) {
    var o = z && z.op;
    if (!o) return true;
    var rows = rowsIn(item.parentNode), i = rows.indexOf(item);
    if (o.op === "attach" && !o.before && !o.after) {
      return hasClass(item.parentNode, "rkids") && attr(item.parentNode, "data-parent") === o.parent && i === rows.length - 1;
    }
    if (o.op !== "move") return false;
    var j = -1;
    rows.forEach(function (r, k) { if (attr(r, "data-rid") === (o.before || o.after)) j = k; });
    return j >= 0 && (o.before ? j === i + 1 : j === i - 1);
  }
  // moveRow shows a tree drop at once: item (with its children's box) goes
  // before ref, after ref and its children (zone "after"), or last into
  // ref's children's box (zone "child"), which make() makes when ref has
  // none; a child keeps its own group (data-g), and a children's box it
  // leaves empty goes. It returns the undo, which puts every node back.
  function moveRow(item, ref, zone, make) {
    var undo = [], from = item.parentNode, kids = kidsOf(item), group = rowGroup(item), had = attr(item, "data-g");
    function put(n, box, next) {
      var p = n.parentNode, s = n.nextSibling;
      undo.push(function () { if (p) { p.insertBefore(n, s); markAttnResync(n); } else if (n.parentNode) n.parentNode.removeChild(n); });
      box.insertBefore(n, next);
      markAttnResync(n);
    }
    var box = ref.parentNode, next = ref;
    if (zone === "after") next = (kidsOf(ref) || ref).nextSibling;
    else if (zone === "child") {
      box = kidsOf(ref);
      if (!box) {
        box = make();
        box.setAttribute("data-parent", attr(ref, "data-rid") || "");
        put(box, ref.parentNode, ref.nextSibling);
      }
      next = null;
    }
    if (next === item) next = (kids || item).nextSibling;
    put(item, box, next);
    if (kids) put(kids, box, next);
    if (hasClass(box, "rkids")) {
      undo.push(function () { if (had === null) item.removeAttribute("data-g"); else item.setAttribute("data-g", had); });
      item.setAttribute("data-g", group);
    }
    if (from && from !== box && hasClass(from, "rkids") && !from.children.length) {
      var fp = from.parentNode, fs = from.nextSibling;
      undo.push(function () { fp.insertBefore(from, fs); });
      fp.removeChild(from);
    }
    return function () { for (var i = undo.length - 1; i >= 0; i--) undo[i](); };
  }
  // homeWt is the worktree a session row's own group renders in: a
  // top-level row's own, a child's by its data-g; null in a plain-folder
  // workspace.
  function homeWt(item) {
    var box = item.parentNode;
    if (!box || !hasClass(box, "rkids")) return up(item, "rwt");
    var g = attr(item, "data-g") || "";
    return find(up(item, "rws"), function (n) { return hasClass(n, "rwt") && attr(n, "data-g") === g; });
  }
  // homeRows is the list a dragged row's top-level peers stand in, for a
  // drop over no row: a top-level row's own, a child's own group's (null
  // when that group lists no row).
  function homeRows(item) {
    var box = item.parentNode;
    if (!box || !hasClass(box, "rkids")) return box;
    var home = homeWt(item) || find(up(item, "rws"), function (n) { return hasClass(n, "wsbody"); });
    return home ? find(home, function (n) { return hasClass(n, "rsessions"); }, true) : null;
  }

  function gripEl(row) {
    return row ? find(row, function (n) { return hasClass(n, "drgrip"); }) : null;
  }
  function homeGroup(row) {
    var wt = homeWt(row);
    if (wt) return wt;
    var ws = up(row, "rws");
    return (ws && find(ws, function (n) { return hasClass(n, "wsbody"); })) || ws;
  }
  function nodeGroup(node) {
    if (!node) return null;
    if (hasClass(node, "rwt")) return node;
    var wt = up(node, "rwt");
    if (wt) return wt;
    if (hasClass(node, "rs")) return homeGroup(node);
    if (hasClass(node, "wsbody")) return node;
    var ws = hasClass(node, "rws") ? node : up(node, "rws");
    return (ws && find(ws, function (n) { return hasClass(n, "wsbody"); })) || ws;
  }
  function activeRowsBox(node) {
    var g = nodeGroup(node);
    if (!g) return null;
    return find(g, function (n) {
      return hasClass(n, "rsessions") && !up(n, "rended") && (hasClass(g, "rwt") || !up(n, "rwt"));
    }, true);
  }
  function groupKey(node) {
    var g = nodeGroup(node);
    if (!g) return "";
    var rws = hasClass(g, "rws") ? g : up(g, "rws");
    var ws = attr(rws, "data-ws") || "";
    return hasClass(g, "rwt") ? ws + "\0wt:" + (attr(g, "data-g") || "") : ws + "\0ws";
  }
  function rowSection(row) {
    if (!row) return "";
    var ed = up(row, "rended");
    if (ed) return attr(ed, "data-k") === "arch" ? "arch" : "del";
    if (hasClass(row, "archived")) return "arch";
    if (hasClass(row, "ended")) return "del";
    return "active";
  }
  function rowMeta(row) {
    if (!row) return null;
    var sec = rowSection(row), grip = gripEl(row), box = activeRowsBox(row);
    var starRaw = attr(row, "data-star"), rws = up(row, "rws");
    var starHidden = sec === "del" && starRaw === null;
    return {
      rid: attr(row, "data-rid") || "",
      section: sec,
      groupKey: groupKey(row),
      ws: attr(rws, "data-ws") || "",
      box: box,
      child: !!(row.parentNode && hasClass(row.parentNode, "rkids")) || attr(row, "data-g") !== null,
      hosted: !!(grip && attr(grip, "data-host") === "1"),
      hasProcess: !!(grip && attr(grip, "data-proc") === "1"),
      hasKids: attr(row, "data-kids") === "1",
      hasBg: !!(grip && attr(grip, "data-bg") === "1"),
      revive: grip ? (attr(grip, "data-revive") || "") : "",
      reviveBlocked: grip ? (attr(grip, "data-revive-blocked") || "") : "",
      star: starHidden ? false : (starRaw === "1" || !!(grip && attr(grip, "data-star-was") === "1")),
      starHidden: starHidden
    };
  }
  function rowRisks(dm) {
    var out = [];
    if (!dm) return out;
    if (dm.hasProcess) out.push("proc");
    if (dm.hasKids) out.push("kids");
    if (dm.hasBg) out.push("bg");
    return out;
  }
  function lifecycleEligibility(dm, canControl) {
    if (!dm) return { eligible: false, refusal: "unknown" };
    if (dm.child) return { eligible: false, refusal: "child_life" };
    if (!canControl) return { eligible: false, refusal: "control" };
    if (!dm.hosted) return { eligible: false, refusal: "hosted" };
    if (dm.section === "del") {
      if (dm.reviveBlocked) return { eligible: false, refusal: "blocked" };
      if (dm.revive !== "reopen") return { eligible: false, refusal: "hosted" };
    }
    return { eligible: true, refusal: "" };
  }
  function activeTopRows(box) {
    var out = [], kids = (box && box.children) || [];
    for (var i = 0; i < kids.length; i++) {
      var c = kids[i];
      if (hasClass(c, "rs") && !hasClass(c, "ended") && !hasClass(c, "archived")) out.push(c);
    }
    return out;
  }
  function restoreOp(dm, targetRow, frac, activeRows) {
    var topRows = activeRows || [], tgt = targetRow || null;
    var f = typeof frac === "number" ? frac : 1;
    if (tgt && tgt.parentNode && hasClass(tgt.parentNode, "rkids")) {
      var pid = attr(tgt.parentNode, "data-parent") || "";
      tgt = null;
      for (var i = 0; i < topRows.length; i++) {
        if ((attr(topRows[i], "data-rid") || "") === pid) { tgt = topRows[i]; f = 1; break; }
      }
    }
    var wantStar = !!dm.star;
    var peers = topRows.filter(function (r) { return (attr(r, "data-star") === "1") === wantStar; });
    if (!peers.length) {
      var fallbackOp = dm.starHidden ? { op: "detach", rid: dm.rid } : null;
      if (!topRows.length) return { op: fallbackOp, ref: null, after: false, boxTop: true };
      return wantStar
        ? { op: fallbackOp, ref: topRows[0], after: false, boxTop: false }
        : { op: fallbackOp, ref: topRows[topRows.length - 1], after: true, boxTop: false };
    }
    var ref = null, after = false;
    for (var j = 0; j < peers.length; j++) {
      if (peers[j] === tgt) { ref = tgt; after = f >= 0.5; break; }
    }
    if (!ref) {
      if (tgt) {
        if (wantStar) { ref = peers[peers.length - 1]; after = true; }
        else { ref = peers[0]; after = false; }
      } else {
        if (f <= 0) { ref = peers[0]; after = false; }
        else { ref = peers[peers.length - 1]; after = true; }
      }
    }
    var refId = attr(ref, "data-rid") || "";
    var op = { op: dm.starHidden ? "detach" : "move", rid: dm.rid };
    if (after) op.after = refId; else op.before = refId;
    return { op: op, ref: ref, after: after, boxTop: false };
  }
  function lifecycleDrop(dm, target, opts) {
    opts = opts || {};
    var canControl = !!opts.canControl;
    var elig = lifecycleEligibility(dm, canControl);
    if (!target) {
      if (dm && dm.section !== "active" && !elig.eligible) return { refusal: elig.refusal };
      return null;
    }
    if (dm.section === "active" && target.section === "active") return null;
    if (dm.section !== "active" && !elig.eligible) {
      if (target.groupKey && dm.groupKey !== target.groupKey) {
        var tWs0 = target.groupKey.split("\0")[0];
        return { refusal: dm.ws !== tWs0 ? "workspace" : "group" };
      }
      return { refusal: elig.refusal };
    }
    if (target.groupKey && dm.groupKey !== target.groupKey) {
      var tWs = target.groupKey.split("\0")[0];
      return { refusal: dm.ws !== tWs ? "workspace" : "group" };
    }
    if (dm.section === target.section) return { noop: true, refusal: "" };
    if (!elig.eligible) return { refusal: elig.refusal };
    var enc = encodeURIComponent(dm.rid);
    if (target.section === "arch") {
      if (dm.section === "del") {
        return { action: "reopen-archive", rid: dm.rid, url: "/agents/" + enc + "/reopen", body: { hidden: true }, targetSection: "arch", risks: [] };
      }
      return { action: "archive", rid: dm.rid, url: "/agents/" + enc + "/hide", body: {}, targetSection: "arch", risks: rowRisks(dm) };
    }
    if (target.section === "del") {
      return { action: "delete", rid: dm.rid, url: "/agents/" + enc + "/close", body: {}, targetSection: "del", risks: rowRisks(dm) };
    }
    if (target.section === "active") {
      var rp = restoreOp(dm, target.targetRow, target.frac, opts.activeRows);
      if (dm.section === "arch") {
        return { action: "unarchive", rid: dm.rid, url: "/agents/" + enc + "/unhide", body: {}, targetSection: "active", risks: [], restore: rp };
      }
      return { action: "reopen-active", rid: dm.rid, url: "/agents/" + enc + "/reopen", body: { hidden: false }, targetSection: "active", risks: [], restore: rp };
    }
    return { refusal: "unknown" };
  }
  // find is the first node under root that test passes, depth first; with
  // top, only root's own children are tried.
  function find(root, test, top) {
    var kids = (root && root.children) || [];
    for (var i = 0; i < kids.length; i++) {
      if (test(kids[i])) return kids[i];
      var deep = top ? null : find(kids[i], test);
      if (deep) return deep;
    }
    return null;
  }
  function liveRows(box) { return rowsIn(box).filter(function (r) { return !hasClass(r, "ended"); }); }
  function rowOf(root, rid) { return root && rid ? refind(root, { cls: "rs", attr: "data-rid", val: rid }) : null; }

  // keyMove is the keyboard path's op for the session row item (Alt and an
  // arrow): "up" and "down" reorder it past the next live row of its level
  // (its group's top-level rows, or its parent's children), "in" attaches
  // it to the previous top-level row as its last child, and "out" detaches
  // a child, which the route lands right after its old parent when that is
  // in the child's own group, else at the head of its own group (the page
  // shows it before that group's first unstarred row). It answers
  // dropZone's { zone, ref, op }, read by dropZone's rules where a place is
  // involved (zone and ref empty when the page has no row to show the
  // detach against: the route still places it), or { refusal } with the
  // route's reason for an op it would refuse (an ended row moves no way,
  // a child or a parent never goes one level deeper, a top-level row has
  // no parent to leave); null when there is no row to move past.
  function keyMove(item, dir) {
    var d = rowFacts(item);
    if (!d.rid) return null;
    if (!d.live) return { refusal: "ended" };
    var rows = liveRows(item.parentNode), i = rows.indexOf(item), t;
    if (dir === "up" || dir === "down") {
      t = rows[dir === "up" ? i - 1 : i + 1];
      if (!t) return null;
      var tf = rowFacts(t);
      tf.first = "";
      return dropZone(d, tf, dir === "up" ? 0 : 1);
    }
    if (dir === "in") {
      if (d.parent || d.kids) return { refusal: "depth" };
      t = rows[i - 1];
      return t ? dropZone(d, rowFacts(t), 0.5) : null;
    }
    if (dir !== "out") return null;
    if (!d.parent) return { refusal: "toplevel" };
    var op = { op: "detach", rid: d.rid }, p = rowOf(up(item, "rws"), d.parent);
    if (p && rowGroup(p) === d.group) return { zone: "after", ref: d.parent, op: op };
    var home = liveRows(homeRows(item)), first = home.filter(function (r) { return !starred(r); })[0];
    if (first) return { zone: "before", ref: attr(first, "data-rid") || "", op: op };
    if (home.length) return { zone: "after", ref: attr(home[home.length - 1], "data-rid") || "", op: op };
    return { zone: "", ref: "", op: op };
  }
  // underRows is where the context menu's "move under" can put the session
  // row item: { rows, refusal }, rows every row of its workspace whose
  // middle would take it as a child (dropZone; its own parent aside) in
  // the rail's order, refusal the route's reason when none can (an ended
  // row, or a parent, which never becomes a child), else "".
  function underRows(item) {
    var d = rowFacts(item), out = [];
    if (!d.live) return { rows: out, refusal: "ended" };
    if (d.kids) return { rows: out, refusal: "depth" };
    (function walk(n) {
      var kids = (n && n.children) || [];
      for (var i = 0; i < kids.length; i++) {
        var k = kids[i];
        if (hasClass(k, "rs")) {
          var z = k !== item ? dropZone(d, rowFacts(k), 0.5) : null;
          if (z && z.zone === "child" && z.ref !== d.parent) out.push(k);
        } else walk(k);
      }
    })(up(item, "rws"));
    return { rows: out, refusal: "" };
  }

  // itemKey names a dragged item, a session row by reply-id or a workspace
  // section by path, so refind can find the node a swap put in its place.
  function itemKey(el) {
    var ws = hasClass(el, "rws"), wt = hasClass(el, "rwt"), attr = ws ? "data-ws" : wt ? "data-g" : "data-rid";
    var key = { cls: ws ? "rws" : wt ? "rwt" : "rs", attr: attr, val: el.getAttribute(attr) || "" };
    if (wt) key.ws = (up(el, "rws") || el).getAttribute("data-ws") || "";
    return key;
  }
  // refind is the element under box that key names, or null.
  function refind(box, key) {
    var kids = box.children || [];
    for (var i = 0; i < kids.length; i++) {
      var k = kids[i];
      if (hasClass(k, key.cls) && k.getAttribute(key.attr) === key.val &&
          (key.ws === undefined || attr(up(k, "rws"), "data-ws") === key.ws)) return k;
      var deep = refind(k, key);
      if (deep) return deep;
    }
    return null;
  }

  // swapGate holds the rail's swaps while busy() reports a press, a drag,
  // or an open rename on it, since a swap replaces every row node: offer(s)
  // reports whether it held s (the latest offer replaces an earlier one),
  // release() hands the held swap back once busy() clears (null while busy
  // or with none held), and forget() drops it (its hash turned current).
  function swapGate(busy) {
    var held = null;
    return {
      offer: function (s) {
        if (!busy()) return false;
        held = s;
        return true;
      },
      release: function () {
        if (busy() || !held) return null;
        var s = held;
        held = null;
        return s;
      },
      forget: function () { held = null; }
    };
  }

  // markCurrent marks the row of the viewed session rid among rows: the
  // .cur class and aria-current on its link; any other row loses both.
  function markCurrent(rows, rid) {
    rows.forEach(function (r) {
      var cur = r.getAttribute("data-rid") === rid, a = r.querySelector(".rl");
      if (cur) r.classList.add("cur"); else r.classList.remove("cur");
      if (a) { if (cur) a.setAttribute("aria-current", "page"); else a.removeAttribute("aria-current"); }
    });
  }

  // rowAsks is a row's open board question IDs (data-asks), or null from an
  // older server that does not render the attribute.
  function rowAsks(row) {
    var raw = row.getAttribute("data-asks");
    return raw === null ? null : new Set(raw.trim().split(/\s+/).filter(Boolean));
  }

  // attention is tab-memory only, independent of the persistent question
  // dot. First observations (and long-disconnect full rails) baseline
  // silently. Replaced or reordered rows preserve their pause-aware animation
  // phase via negative CSS animation-delay (--attn-t during the 1.14s flash
  // intro, --attn-breathe-delay after) and never replay a finished flash.
  // Absent rows stay in seen for ATTN_ABSENT_GRACE_MS (30s) so a transient
  // drop across a daemon restart neither false-triggers nor drops new asks.
  // flashDone drops .attn-flash once its three flashes end. Left on, the
  // delayed breathe overlaps the flash on the same opacity, the browser
  // refuses to composite it, and the long-lived pulse repaints the gradient on
  // the main thread each frame. Without the class the breathe starts alone
  // from the opacity the flash ended on, so the hand-over stays seamless.
  var ATTN_FLASH_MS = 1140;
  var ATTN_BREATHE_MS = 1000;
  var ATTN_ABSENT_GRACE_MS = 30000;
  function attnSec(ms) {
    var s = Math.round(ms) / 1000;
    return (s === 0 ? 0 : s) + "s";
  }
  function setAttnProp(row, name, value) {
    if (!row || !row.style) return;
    if (value === null || value === "") {
      if (row.style.removeProperty) row.style.removeProperty(name);
      else delete row.style[name];
    } else if (row.style.setProperty) {
      row.style.setProperty(name, value);
    } else {
      row.style[name] = value;
    }
  }
  function clearFlashListener(row) {
    if (!row || !row.wsAttnFlashEnd) return;
    if (row.removeEventListener) row.removeEventListener("animationend", row.wsAttnFlashEnd);
    row.wsAttnFlashEnd = null;
  }
  function flashDone(row, entry) {
    clearFlashListener(row);
    if (!row.addEventListener) return;
    function done(e) {
      if (e.animationName !== "attn-flash" && e.animationName !== "attn-flash-ring") return;
      clearFlashListener(row);
      row.classList.remove("attn-flash");
      setAttnProp(row, "--attn-t", null);
      if (entry) entry.flashDone = true;
    }
    row.wsAttnFlashEnd = done;
    row.addEventListener("animationend", done);
  }
  function attention(opts) {
    var nowFn = typeof opts === "function" ? opts : (opts && typeof opts.now === "function" ? opts.now : Date.now);
    var paused = !!(opts && typeof opts === "object" && opts.paused);
    var pausedAt = paused ? nowFn() : null;
    var pausedTotal = 0;
    var seen = Object.create(null);
    var asks = rowAsks;
    function activeNow() {
      var t = nowFn();
      return t - pausedTotal - (paused ? Math.max(0, t - pausedAt) : 0);
    }
    function setPaused(next) {
      next = !!next;
      if (next === paused) return;
      var t = nowFn();
      if (next) {
        paused = true;
        pausedAt = t;
      } else {
        pausedTotal += Math.max(0, t - pausedAt);
        paused = false;
        pausedAt = null;
      }
    }
    function observe(rows, rid, baseline) {
      var now = nowFn();
      var present = Object.create(null);
      rows.forEach(function (row) {
        var id = row.getAttribute("data-rid"), ids = asks(row), prev = seen[id];
        if (prev && prev.missingSince !== null && prev.missingSince !== undefined && now - prev.missingSince > ATTN_ABSENT_GRACE_MS) {
          delete seen[id];
          prev = null;
        }
        if (!ids) { delete seen[id]; return; } // Old server: neither baseline nor flag.
        present[id] = true;
        var fresh = !baseline && !!prev && !prev.flagged && id !== rid && Array.from(ids).some(function (n) { return !prev.asks.has(n); });
        var flagged = id !== rid && (fresh || !!(prev && prev.flagged));
        var flash = flagged && (fresh || !!(prev && prev.flash));
        var startedAt = flagged ? (fresh ? null : (prev && prev.startedAt !== undefined ? prev.startedAt : null)) : null;
        var flashFinished = flagged && !fresh && !!(prev && prev.flashDone);
        seen[id] = {
          asks: ids,
          flagged: flagged,
          flash: flash,
          startedAt: startedAt,
          flashDone: flashFinished,
          missingSince: null
        };
      });
      Object.keys(seen).forEach(function (id) {
        if (present[id]) return;
        var entry = seen[id];
        if (entry.missingSince === null || entry.missingSince === undefined) entry.missingSince = now;
        else if (now - entry.missingSince > ATTN_ABSENT_GRACE_MS) delete seen[id];
      });
    }
    function mark(rows, rid, hint) {
      var now = nowFn(), actNow = activeNow();
      rows.forEach(function (row) {
        var id = row.getAttribute("data-rid"), ids = asks(row), prev = seen[id];
        if (prev && prev.missingSince !== null && prev.missingSince !== undefined && now - prev.missingSince > ATTN_ABSENT_GRACE_MS) {
          delete seen[id];
          prev = null;
        }
        if (!ids) { delete seen[id]; prev = null; }
        else if (!prev) prev = seen[id] = { asks: ids, flagged: false, flash: false, startedAt: null, flashDone: false, missingSince: null };
        else prev.missingSince = null;
        if (prev && id === rid) { prev.flagged = false; prev.flash = false; prev.startedAt = null; prev.flashDone = false; }
        var on = !!prev && prev.flagged, link = row.querySelector(".rl");
        var hadAttn = row.classList.contains("attn");
        var resync = !!row.wsAttnResync;
        if (resync) row.wsAttnResync = false;
        if (!on) {
          clearFlashListener(row);
          row.classList.remove("attn-flash");
          row.classList.remove("attn");
          setAttnProp(row, "--attn-t", null);
          setAttnProp(row, "--attn-breathe-delay", null);
        } else if (prev.flash) {
          prev.flash = false;
          prev.flashDone = false;
          prev.startedAt = actNow;
          setAttnProp(row, "--attn-t", null);
          setAttnProp(row, "--attn-breathe-delay", null);
          row.classList.add("attn");
          row.classList.add("attn-flash");
          flashDone(row, prev);
        } else if (!hadAttn || resync) {
          if (prev.startedAt === null || prev.startedAt === undefined) prev.startedAt = actNow;
          var elapsed = Math.max(0, actNow - prev.startedAt);
          row.classList.add("attn");
          if (!prev.flashDone && elapsed < ATTN_FLASH_MS) {
            setAttnProp(row, "--attn-breathe-delay", null);
            setAttnProp(row, "--attn-t", attnSec(elapsed));
            row.classList.add("attn-flash");
            flashDone(row, prev);
          } else {
            prev.flashDone = true;
            clearFlashListener(row);
            row.classList.remove("attn-flash");
            setAttnProp(row, "--attn-t", null);
            var phase = Math.max(0, elapsed - ATTN_FLASH_MS) % ATTN_BREATHE_MS;
            setAttnProp(row, "--attn-breathe-delay", attnSec(-phase));
          }
        }
        if (link) { if (on) link.setAttribute("title", hint); else link.removeAttribute("title"); }
      });
    }
    return {
      observe: observe,
      mark: mark,
      setPaused: setPaused,
      sync: function (rows, rid, hint) { observe(rows, rid); mark(rows, rid, hint); }
    };
  }

  // notices picks, from each full read of the rail's rows, what the page
  // that owns the rail notifies of; tab memory only, one pass per read.
  // "idle": a top-level live row (no data-g, neither ended nor stopped)
  // whose data-busy went away, which the server keeps while the session,
  // any child row under it, any background work it knows of, or any exec
  // job under its worktree still runs, so a child finishing under a busy
  // parent is no notice and the parent's own finish is one. A worktree's
  // exec jobs keep every session in it busy, so a finish counts only when
  // its busy span (the reads in a row with data-busy, a first read
  // included) had one with data-active, the row's own work (its turn, a
  // wait, a subagent, a background task, or an active child): a session
  // that sat idle while another's job ran is no notice, and one whose turn
  // ended while a job ran is one when the job ends. Its tag holds the
  // row's last-tool time, with a count added when a finish repeats that
  // time (a turn without a tool), so a browser notification of a later
  // finish never replaces an earlier one under the same tag. "ask": a row
  // of any depth whose data-asks gained IDs the read before did not have,
  // as attention reads them: a closed ID that reopens is new; ids are
  // those IDs, sorted. A row's first read, and every row of a read passed
  // baseline (a reconnect's first full rail), only records; a row gone or
  // ended is forgotten or recorded idle without a notice.
  function notices() {
    var seen = Object.create(null);
    function read(rows, baseline) {
      var kept = Object.create(null), out = [];
      rows.forEach(function (row) {
        var id = row.getAttribute("data-rid");
        if (!id || kept[id]) return;
        var prev = seen[id], ids = rowAsks(row), top = row.getAttribute("data-g") === null;
        var live = !row.classList.contains("ended") && !row.classList.contains("stopped");
        var busy = live && row.getAttribute("data-busy") === "1";
        var active = busy && (row.getAttribute("data-active") === "1" || !!(prev && prev.busy && prev.active));
        var when = row.querySelector("time.when.t"), ms = (when && when.getAttribute("data-ms")) || "0";
        var cur = kept[id] = { busy: busy, active: active, top: top, asks: ids, ms: prev ? prev.ms : "", n: prev ? prev.n : 0 };
        if (!prev || baseline) return;
        if (top && prev.top && live && prev.busy && prev.active && !busy) {
          cur.n = cur.ms === ms ? cur.n + 1 : 0;
          cur.ms = ms;
          out.push({ kind: "idle", rid: id, row: row, tag: "ws-idle:" + id + ":" + ms + (cur.n ? "." + cur.n : "") });
        }
        if (ids && prev.asks) {
          var fresh = Array.from(ids).filter(function (n) { return !prev.asks.has(n); }).sort();
          if (fresh.length) out.push({ kind: "ask", rid: id, row: row, ids: fresh, tag: "ws-ask:" + id + ":" + fresh.join(",") });
        }
      });
      seen = kept;
      return out;
    }
    return { read: read };
  }

  // state is the rail's stored sets over store, as marks reads them: the
  // collapsed workspaces and parents and the ended folds left open. The
  // rows' order and stars come rendered from the daemon's layout.
  function state(store) {
    return { collapsed: collapsed(store), kids: kidsCollapsed(store), ended: endedOpen(store) };
  }
  // marks applies the stored marks (s, state) to the rendered rail region
  // r: marks the viewed session's row (rid), collapses the collapsed
  // workspaces and parents. The page script runs it after every rail swap;
  // the iframe shell also runs it as soon as the rail is parsed, before the
  // first paint. (The archived and ended lists open as a popup, not here.)
  function marks(r, s, rid) {
    function all(sel) { return Array.prototype.slice.call(r.querySelectorAll(sel)); }
    markCurrent(all(".rs"), rid);
    all(".rws").forEach(function (ws) {
      var col = s.collapsed.has(ws.getAttribute("data-ws") || ""), tog = ws.querySelector(".wstog");
      if (col) ws.classList.add("col"); else ws.classList.remove("col");
      if (tog) tog.setAttribute("aria-expanded", String(!col));
    });
    all(".kidtog").forEach(function (tog) {
      var row = tog.closest(".rs"), col = !!row && s.kids.has(row.getAttribute("data-rid") || "");
      if (row) { if (col) row.classList.add("kcol"); else row.classList.remove("kcol"); }
      tog.setAttribute("aria-expanded", String(!col));
    });
  }
  // A group's fold row (.rfold, data-key its path) has two halves, archived
  // ("arch") and ended ("del"). Each opens a floating popup (dashboard-rail-
  // popovers.js, rpFoldMake) that lists the half's rows, copied from the
  // server-rendered .rended source beside the row; the source itself never
  // shows. foldMatch is the popup's filter: every blank-separated word of
  // query, lowercased, must occur in hay (a row's reply-id, harness, and
  // text); an empty query matches all.
  function foldMatch(hay, query) {
    var h = String(hay || "").toLowerCase(), words = String(query || "").toLowerCase().split(/\s+/);
    for (var i = 0; i < words.length; i++) if (words[i] && h.indexOf(words[i]) < 0) return false;
    return true;
  }
  // foldSource is the hidden .rended list of kind k ("arch" or "del") that
  // fold row f's group rendered beside it, or null when it has none.
  function foldSource(f, k) {
    var sibs = f && f.parentNode ? Array.prototype.slice.call(f.parentNode.children) : [];
    for (var i = 0; i < sibs.length; i++) if (sibs[i].classList.contains("rended") && sibs[i].getAttribute("data-k") === k) return sibs[i];
    return null;
  }

  // REVIVE_MOVES are the ways an ended row brings its session back (its
  // revive entry's data-revive); each is the session's route of that name.
  var REVIVE_MOVES = { takeover: true, reopen: true };
  // reviveRoute is the route the revive entry for move posts to for the
  // session rid; empty for a move the rail does not know or no session.
  function reviveRoute(move, rid) {
    if (!REVIVE_MOVES.hasOwnProperty(move) || !rid) return "";
    return "/agents/" + encodeURIComponent(rid) + "/" + move;
  }
  // markRevive marks the revive entry of the row of session rid among rows
  // as posting (the .busy class and aria-busy); every other entry loses
  // both. An empty rid marks none.
  function markRevive(rows, rid) {
    rows.forEach(function (r) {
      var b = r.querySelector(".rrev");
      if (!b) return;
      var on = !!rid && r.getAttribute("data-rid") === rid;
      if (on) { b.classList.add("busy"); b.setAttribute("aria-busy", "true"); }
      else { b.classList.remove("busy"); b.removeAttribute("aria-busy"); }
    });
  }
  // handedNotice is the hand-off notice a session state st calls for: null
  // unless the session was handed to a terminal, else { command }, the shell
  // command that resumes it there (empty when the state carries none, as
  // for a viewer who does not hold the controls).
  function handedNotice(st) {
    if (!st || typeof st !== "object" || st.status !== "handed_off") return null;
    return { command: typeof st.resume_command === "string" ? st.resume_command.trim() : "" };
  }

  // leaveGuard is a beforeunload listener that asks the browser to confirm
  // leaving while holds() reports input the draft store cannot keep (pasted
  // images or a failed draft write), and does nothing otherwise.
  function leaveGuard(holds) {
    return function (e) {
      if (!holds()) return undefined;
      e.preventDefault();
      e.returnValue = "";
      return "";
    };
  }

  // popPlace is where a rail popover of size w x h goes in a viewport of vw x
  // vh, against the anchor rect a ({top, bottom, left, right}, viewport
  // coordinates): below the anchor, its right edge on the anchor's, else
  // above it when only that fits, kept 8px inside the viewport.
  function popPlace(a, w, h, vw, vh) {
    var m = 8, gap = 4;
    var left = Math.max(m, Math.min(a.right - w, vw - w - m));
    var top = a.bottom + gap;
    if (top + h > vh - m) top = a.top - gap - h >= m ? a.top - gap - h : Math.max(m, vh - h - m);
    return { top: Math.round(top), left: Math.round(left) };
  }

  // matchBranches is the branches of list whose name holds query, ignoring
  // case and surrounding space; an empty query matches every branch.
  function matchBranches(list, query) {
    var qv = String(query || "").trim().toLowerCase();
    return strings(list).filter(function (b) { return !qv || b.toLowerCase().indexOf(qv) >= 0; });
  }

  // worktreeBody is the add-worktree request for the repository workspace
  // ws from the form f: mode "new" (name, from base; an empty base is the
  // root's current HEAD), "old" (pick, an existing branch), or "detached"
  // (from base without a branch), and the folder:
  // the custom one when custom is set, else the default the form shows
  // (shown). The default names the current minute, so the daemon's own
  // default at add time can differ from the one the form showed.
  function worktreeBody(ws, f) {
    var isNew = f.mode === "new", detached = f.mode === "detached";
    var body = {
      workspace: ws,
      branch: detached ? "" : isNew ? String(f.name || "").trim() : String(f.pick || ""),
      "new": isNew,
      base: isNew || detached ? String(f.base || "") : "",
      folder: String((f.custom ? f.folder : f.shown) || "").trim()
    };
    if (detached) body.detached = true;
    return body;
  }

  // leadBody is the spawn request for a hosted lead session in root from
  // the form f: the harness, the plugin isolation (always sent), and a model, effort, and permission mode only
  // when one was picked (none leaves the harness's own). The form supplies
  // admitted permission choices.
  // A common permission level (f.level, for POST /agents/leads/permission)
  // replaces the native mode, never joins it, and carries
  // permission_full_ack only for "full" with the warning's grant (f.ack).
  function leadBody(root, f) {
    var body = { root: root, harness: String(f.harness || ""), plugin_isolation: f.isolation === "isolated" ? "isolated" : "default" };
    if (f.provider) body.provider = String(f.provider);
    if (f.model) body.model = String(f.model);
    if (f.effort) body.effort = String(f.effort);
    if (f.level) {
      body.permission_level = String(f.level);
      if (body.permission_level === "full" && f.ack === true) body.permission_full_ack = true;
    } else if (f.mode) body.permission_mode = String(f.mode);
    if (f.dashboardControl === true) body.dashboard_control_access = true;
    if (typeof f.convHistoryAccess === "boolean") body.conv_history_access = f.convHistoryAccess;
    return body;
  }

  // deleteReady reports whether the worktree delete modal's Delete is on:
  // shown is the check the modal displays ({key, check}: key the request it
  // answered), key the modal's current request, typed its input. Only the
  // current request's check counts (an answer to an earlier open is stale),
  // the typed text must be the check's folder name exactly, and the check
  // must hold no refusal, except the submit's own two (force: the delete
  // needs force the submit did not carry; changed: what force covers moved
  // since the check): their check is fresh, with a fresh token, and asks
  // only for the name to be typed again. A forced delete needs the token.
  function deleteReady(shown, typed, key) {
    var c = shown && shown.check;
    if (!c || typeof c !== "object" || shown.key !== key) return false;
    var r = c.refusal ? String(c.refusal) : "";
    if (r && r !== "force" && r !== "changed") return false;
    var folder = typeof c.folder === "string" ? c.folder : "";
    if (!folder || typed !== folder) return false;
    return !c.force || !!c.token;
  }
  // deleteBody is the delete submit for the worktree at path of the
  // repository workspace ws, confirmed against check: force and the check's
  // token only when the check needs force.
  function deleteBody(ws, path, check) {
    var force = !!(check && check.force);
    return { workspace: ws, path: path, force: force, token: force ? String(check.token || "") : "" };
  }

  // APPROVAL_CAP bounds an approval card's input preview.
  var APPROVAL_CAP = 4000;
  // approvalText is a tool call's input as an approval card shows it: a
  // shell call's command; a file call's path, then what it writes (an
  // edit's old lines under "-" and new ones under "+", a write's content);
  // a fetch's address; any other input as indented JSON. Capped at
  // APPROVAL_CAP characters.
  function approvalText(input) {
    var text = "";
    function lines(mark, s) { return String(s).split("\n").map(function (l) { return mark + l; }).join("\n"); }
    if (input === null || input === undefined) text = "";
    else if (typeof input !== "object" || Array.isArray(input)) text = typeof input === "string" ? input : JSON.stringify(input, null, 2);
    else if (typeof input.command === "string") text = input.command;
    else if (typeof input.file_path === "string") {
      text = input.file_path;
      if (typeof input.new_string === "string") text += "\n" + (input.old_string ? lines("- ", input.old_string) + "\n" : "") + lines("+ ", input.new_string);
      else if (typeof input.content === "string") text += "\n" + input.content;
    } else if (typeof input.url === "string") text = input.url;
    else if (Object.keys(input).length) text = JSON.stringify(input, null, 2);
    return text.length > APPROVAL_CAP ? text.slice(0, APPROVAL_CAP) + "\u2026" : text;
  }

  // Canonical server inputs are independent of the live DOM (client marks,
  // optimistic layout and time formatting never become the delta baseline).
  // The server sends a full region whenever these inter-row slots change.
  var ROW_SLOT = "<!--ws-rail-row-->";
  function rowInputFull(html, doc) {
    if (typeof html !== "string" || html.indexOf(ROW_SLOT) >= 0) return null;
    var box = doc.createElement("div"), rows = Object.create(null), order = [];
    box.innerHTML = html;
    var nodes = Array.prototype.slice.call(box.querySelectorAll(".rs[data-rid]"));
    if (nodes.some(function (n) {
      var id = n.getAttribute("data-rid");
      if (!id || rows[id] || n.parentElement.closest(".rs")) return true;
      rows[id] = { html: n.outerHTML, asks: n.getAttribute("data-asks") }; order.push(id);
      return false;
    })) return null;
    nodes.forEach(function (n) { n.replaceWith(doc.createComment("ws-rail-row")); });
    return { shell: box.innerHTML.split(ROW_SLOT), order: order, rows: rows };
  }
  function rowNode(html, id, doc) {
    if (typeof html !== "string") return null;
    var box = doc.createElement("div"); box.innerHTML = html;
    var n = box.firstElementChild;
    return box.childNodes.length === 1 && n && n.matches("div.rs[data-rid]") && n.getAttribute("data-rid") === id && !n.querySelector(".rs") ? n : null;
  }
  function rowInputPatch(base, ev, doc) {
    if (!base || !ev || typeof ev.hash !== "string" || !Array.isArray(ev.order) || !Array.isArray(ev.rows) || ev.order.length + 1 !== base.shell.length) return null;
    var rows = Object.create(null), changed = Object.create(null), seen = Object.create(null);
    Object.keys(base.rows).forEach(function (id) { rows[id] = base.rows[id]; });
    if (ev.rows.some(function (r) {
      if (!r || typeof r.key !== "string" || !r.key || typeof r.hash !== "string" || changed[r.key]) return true;
      var n = rowNode(r.html, r.key, doc);
      if (!n) return true;
      changed[r.key] = true; rows[r.key] = { html: n.outerHTML, asks: n.getAttribute("data-asks") };
      return false;
    })) return null;
    if (ev.order.some(function (id) {
      if (typeof id !== "string" || !id || seen[id] || !rows[id]) return true;
      seen[id] = true; return false;
    }) || Object.keys(changed).some(function (id) { return !seen[id]; })) return null;
    var kept = Object.create(null);
    ev.order.forEach(function (id) { kept[id] = rows[id]; });
    return { shell: base.shell, order: ev.order.slice(), rows: kept };
  }
  function rowInputAttentionRows(input) {
    if (!input || typeof input === "string" || !Array.isArray(input.order) || !input.rows) return null;
    return input.order.map(function (id) {
      var row = input.rows[id];
      var asks = row && Object.prototype.hasOwnProperty.call(row, "asks") ? row.asks : null;
      return {
        getAttribute: function (name) {
          if (name === "data-rid") return id;
          if (name === "data-asks") return asks;
          return null;
        }
      };
    });
  }
  function rowInputHTML(input) {
    if (typeof input === "string") return input;
    return input.order.map(function (id, i) { return input.shell[i] + input.rows[id].html; }).join("") + input.shell[input.order.length];
  }
  // Validate everything before mutating. Only changed rows are parsed; an
  // unchanged row and a hover-only row keep their nodes and running spinners.
  // Return null for the existing full swap, false for hover-only/no-op, true
  // when visible markup changed and client marks must run again.
  function mergeRows(root, previous, next) {
    if (!previous || !next || previous.shell.length !== next.shell.length || previous.shell.some(function (s, i) { return s !== next.shell[i]; })) return null;
    var live = Object.create(null), updates = [], doc = root.ownerDocument;
    Array.prototype.forEach.call(root.querySelectorAll(".rs[data-rid]"), function (n) { live[n.getAttribute("data-rid")] = n; });
    if (Object.keys(live).length !== previous.order.length || previous.order.some(function (id) { return !live[id]; })) return null;
    var reordered = previous.order.some(function (id, i) { return id !== next.order[i]; }), visible = reordered;
    function withoutHover(n) { n = n.cloneNode(true); var src = n.querySelector(":scope > template.shp-source"); if (src) src.remove(); return n.outerHTML; }
    if (next.order.some(function (id) {
      var old = previous.rows[id], value = next.rows[id];
      if (!old || !live[id]) return true;
      if (old.html === value.html) return false;
      var a = rowNode(old.html, id, doc), b = rowNode(value.html, id, doc);
      if (!a || !b) return true;
      var source = b.querySelector(":scope > template.shp-source"), oldSource = live[id].querySelector(":scope > template.shp-source");
      if (source && oldSource && withoutHover(a) === withoutHover(b)) updates.push({ old: oldSource, node: source });
      else { updates.push({ old: live[id], node: b, id: id }); visible = true; }
      return false;
    })) return null;
    updates.forEach(function (u) { u.old.replaceWith(u.node); if (u.id) live[u.id] = u.node; });
    if (reordered) {
      var slots = previous.order.map(function (id, i) {
        if (id === next.order[i]) return null;
        var slot = doc.createComment("row");
        live[id].replaceWith(slot);
        return slot;
      });
      slots.forEach(function (slot, i) {
        if (!slot) return;
        var node = live[next.order[i]];
        node.wsAttnResync = true;
        slot.replaceWith(node);
      });
    }
    return visible;
  }

  // Remember server markup before client marks/time formatting touch it.
  // A hover-only render replaces inert sources, not rows or their running
  // animations. Any other change uses the existing full rail swap.
  function hoverUpdates(root) {
    function read(box) {
      var sources = [];
      Array.prototype.forEach.call(box.querySelectorAll(".rs > template.shp-source"), function (src) {
        sources.push({ rid: src.parentNode.getAttribute("data-rid"), node: src });
        src.parentNode.removeChild(src);
      });
      return { markup: box.innerHTML, sources: sources };
    }
    var seen = read(root.cloneNode(true)).markup;
    return function (html) {
      var box = root.ownerDocument.createElement("div");
      box.innerHTML = html;
      var next = read(box), same = next.markup === seen;
      seen = next.markup;
      if (!same) return false;
      var sources = Object.create(null);
      Array.prototype.forEach.call(root.querySelectorAll(".rs > template.shp-source"), function (src) {
        sources[src.parentNode.getAttribute("data-rid")] = src;
      });
      // Validate all keys before changing anything; a locally missing source
      // falls back to the normal swap rather than leaving stale evidence.
      if (Object.keys(sources).length !== next.sources.length || next.sources.some(function (s) { return !sources[s.rid]; })) return false;
      next.sources.forEach(function (s) {
        var old = sources[s.rid];
        if (old.outerHTML !== s.node.outerHTML) old.parentNode.replaceChild(s.node, old);
      });
      return true;
    };
  }

  // Keep a read-only hover outside replaced rail/tree regions. The source
  // belongs directly to its row/agent: a parent's model is never a fallback.
  function hoverPlace(anchor, edge, width, height, viewport) {
    var left = edge + 8;
    if (left + width > viewport.width - 8) left = anchor.left - width - 8;
    return { left: Math.max(8, Math.min(left, viewport.width - width - 8)),
      top: Math.max(8, Math.min(anchor.top, viewport.height - height - 8)) };
  }
  function hover(doc, win) {
    var pop = null, active = null, x = 0, y = 0, keyboard = false, html = null, popCls = "", popW = 0, popH = 0;
    function target(n) {
      return n && n.closest ? n.closest(".rs, .amodel, .agent .aname") : null;
    }
    function source(n) {
      var owner = hasClass(n, "rs") ? n : n.closest(".agent");
      if (!owner) return null;
      for (var i = 0; i < owner.children.length; i++) {
        var c = owner.children[i];
        if (c.tagName === "TEMPLATE" && hasClass(c, "shp-source")) return c;
      }
      return null;
    }
    function hide() {
      active = null; html = null; popCls = ""; popW = 0; popH = 0;
      if (pop) pop.hidden = true;
    }
    function show(n) {
      var src = n && source(n);
      // A rail drag (rdragging) shows no hover: the rows it passes are targets.
      if (!src || !n.isConnected || hasClass(doc.documentElement, "rdragging")) { hide(); return; }
      if (!n.getClientRects().length) { hide(); return; }
      // Read anchor and rail geometry before mutating pop so DOM writes do not force a second layout.
      var a = n.getBoundingClientRect(), rail = hasClass(n, "rs") && n.closest(".rail");
      var edge = rail ? rail.getBoundingClientRect().right : a.right;
      var vw = win.innerWidth, vh = win.innerHeight;
      var nextCls = "rpop shp" + (src.getAttribute("data-stale") ? " stale" : "");
      var nextHtml = src.innerHTML;
      var created = false;
      if (!pop) {
        pop = doc.createElement("div"); pop.className = nextCls;
        pop.setAttribute("role", "tooltip");
        doc.body.appendChild(pop);
        created = true;
      }
      active = n;
      var wasHidden = created || pop.hidden;
      var contentChanged = wasHidden || popCls !== nextCls || html !== nextHtml;
      if (popCls !== nextCls) { popCls = nextCls; pop.className = nextCls; }
      if (html !== nextHtml) { html = nextHtml; pop.innerHTML = html; }
      if (contentChanged && typeof win.wsFmtTimes === "function") win.wsFmtTimes(pop);
      if (pop.hidden) pop.hidden = false;
      if (contentChanged || !popW || !popH) {
        popW = pop.offsetWidth;
        popH = pop.offsetHeight;
      }
      var p = hoverPlace(a, edge, popW, popH, { width: vw, height: vh });
      var leftPx = p.left + "px", topPx = p.top + "px";
      if (pop.style.left !== leftPx) pop.style.left = leftPx;
      if (pop.style.top !== topPx) pop.style.top = topPx;
    }
    doc.addEventListener("mouseover", function (e) {
      keyboard = false; x = e.clientX; y = e.clientY;
      var n = target(e.target); if (n) show(n); else hide();
    });
    doc.addEventListener("mousemove", function (e) { x = e.clientX; y = e.clientY; });
    doc.addEventListener("mouseout", function (e) {
      if (active && target(e.relatedTarget) !== active) hide();
    });
    doc.addEventListener("focusin", function (e) {
      keyboard = true; var n = target(e.target); if (n) show(n); else hide();
    });
    doc.addEventListener("focusout", hide);
    doc.addEventListener("pointerdown", hide);
    doc.addEventListener("keydown", function (e) { if (e.key === "Escape") hide(); });
    doc.addEventListener("scroll", hide, true);
    win.addEventListener("resize", hide); win.addEventListener("blur", hide);
    // SSE can replace the hovered node without a pointer event. Re-hit-test
    // at the last pointer (or focus) rather than retaining detached evidence.
    var observer = new win.MutationObserver(function (changes) {
      if (!active || !changes.some(function (c) { return c.target !== pop && !pop.contains(c.target); })) return;
      show(target(keyboard ? doc.activeElement : doc.elementFromPoint(x, y)));
    });
    observer.observe(doc.body, { childList: true, subtree: true });
    return { hide: hide, refresh: function () { if (active) show(target(keyboard ? doc.activeElement : doc.elementFromPoint(x, y))); } };
  }

  // rail.js is shared by the shell (which owns the rail) and framed pages
  // (which own the agent tree). Each document installs exactly one hover.
  if (typeof document !== "undefined") {
    if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", function () { hover(document, window); });
    else hover(document, window);
  }

  // topOver tells whether the header strip's items run past its content
  // box (its right padding is the clearance of the drawer's and tracker's
  // handles). The head region's wrapper lays out as its items (display:
  // contents), so they are measured in its place.
  function topOver(top, win) {
    var left = top.querySelector(":scope > .top-left");
    if (left) {
      if (left.scrollWidth > left.clientWidth + 0.5) return true;
      var lbox = left.getBoundingClientRect(), lend = lbox.right, lover = false;
      Array.prototype.forEach.call(left.querySelectorAll(":scope > :not(.r-head), :scope > .r-head > *"), function (el) {
        var r = el.getBoundingClientRect();
        if (r.width > 0 && r.right > lend + 0.5) lover = true;
      });
      return lover;
    }
    var box = top.getBoundingClientRect(), end = box.right - (parseFloat(win.getComputedStyle(top).paddingRight) || 0), over = false;
    Array.prototype.forEach.call(top.querySelectorAll(":scope > :not(.r-head), :scope > .r-head > *"), function (el) {
      var r = el.getBoundingClientRect();
      if (r.width > 0 && r.right > end + 0.5) over = true;
    });
    return over;
  }
  // topFit steps the header strip (dashboard.css "top bar") down as it runs
  // short, the center first. Measured with the identity at its full width
  // (tmeas), an overflowing strip compacts plan usage to its one trigger
  // (tu); one that still overflows hides the low-priority chips (tch), and
  // the identity then ellipsizes to fit. The strip is one row of fixed
  // height, so no step moves anything under it. Returns the level, 0-2.
  function topFit(top, win) {
    win = win || window;
    if (!top || !top.classList) return 0;
    var cl = top.classList, level = 0;
    cl.add("tmeas"); cl.remove("tu", "tch");
    if (topOver(top, win)) {
      level = 1; cl.add("tu");
      if (topOver(top, win)) { level = 2; cl.add("tch"); }
    }
    cl.remove("tmeas");
    return level;
  }
  // topWatch keeps topFit current: on a resize of the strip (the window,
  // the drawer's and tracker's room it pads clear of) and on a change of its
  // items (a head swap, a usage redraw, a rename). Its own class changes
  // neither resize the strip nor touch its items. The search slot (qsslot)
  // holds the tracker's own fixed-position UI, whose renders are no change
  // of the strip's items.
  function topWatch(top, win) {
    win = win || window;
    if (!top) return;
    var fit = function () { topFit(top, win); };
    var inSlot = function (rec) { var n = rec.target; n = n && n.nodeType !== 1 ? n.parentNode : n; return !!(n && n.closest && n.closest(".qsslot")); };
    if (win.ResizeObserver) {
      var ro = new win.ResizeObserver(fit), slot = top.querySelector(".qsslot"), left = top.querySelector(":scope > .top-left");
      ro.observe(top);
      if (left) ro.observe(left);
      if (slot) ro.observe(slot); // its search handle is a strip item
    }
    if (win.MutationObserver) new win.MutationObserver(function (recs) { if (!recs.every(inSlot)) fit(); }).observe(top, { childList: true, subtree: true, characterData: true, attributes: true, attributeFilter: ["hidden"] });
    if (win.document.fonts && win.document.fonts.ready) win.document.fonts.ready.then(fit);
    fit();
  }
  // botOver / botFit / botWatch compact the relocated bottom-right usage handle
  // (.shell-bottom.bu) when it overflows #slot-bot-right, and restore the full
  // pill as soon as the right cell widens again.
  function botOver(bar) {
    var right = bar && bar.querySelector("#slot-bot-right, .bot-right");
    if (!right) return false;
    var rbox = right.getBoundingClientRect();
    if (rbox.width <= 0) return false;
    // 1px slack: scrollWidth/clientWidth are independently rounded, so a
    // fractional-width cell that exactly fits can differ by 1 (false collapse).
    if (right.scrollWidth > right.clientWidth + 1) return true;
    var btn = right.querySelector(".usage-toggle");
    if (!btn) return false;
    var bbox = btn.getBoundingClientRect();
    return bbox.width > 0 && (bbox.left < rbox.left - 0.5 || bbox.right > rbox.right + 0.5);
  }
  function botFit(bar, win) {
    win = win || window;
    if (!bar || !bar.classList) return 0;
    var cl = bar.classList;
    cl.remove("bu");
    if (botOver(bar)) {
      cl.add("bu");
      return 1;
    }
    return 0;
  }
  function botWatch(bar, win) {
    win = win || window;
    if (!bar) return;
    var fit = function () { botFit(bar, win); };
    if (win.ResizeObserver) {
      var ro = new win.ResizeObserver(fit);
      ro.observe(bar);
      var right = bar.querySelector("#slot-bot-right, .bot-right");
      if (right) ro.observe(right);
      var center = bar.querySelector("#slot-bot-center, .bot-center");
      if (center) ro.observe(center);
    }
    if (win.MutationObserver) {
      new win.MutationObserver(fit).observe(bar, { childList: true, subtree: true, characterData: true, attributes: true, attributeFilter: ["hidden"] });
    }
    if (win.document && win.document.fonts && win.document.fonts.ready) win.document.fonts.ready.then(fit);
    fit();
  }
  window.WsRail = {
    homeGroup: homeGroup,
    groupKey: groupKey,
    activeRowsBox: activeRowsBox,
    activeTopRows: activeTopRows,
    rowSection: rowSection,
    rowMeta: rowMeta,
    rowRisks: rowRisks,
    lifecycleEligibility: lifecycleEligibility,
    restoreOp: restoreOp,
    lifecycleDrop: lifecycleDrop,
    admissionDrafts: admissionDrafts,
    hoverUpdates: hoverUpdates,
    rowInputFull: rowInputFull,
    rowInputPatch: rowInputPatch,
    rowInputHTML: rowInputHTML,
    rowInputAttentionRows: rowInputAttentionRows,
    mergeRows: mergeRows,
    hover: hover,
    hoverPlace: hoverPlace,
    DRAFT_CAP: DRAFT_CAP,
    drafts: drafts,
    emptyDraft: emptyDraft,
    stripMarkers: stripMarkers,
    collapsed: collapsed,
    legacyImport: legacyImport,
    legacyDone: legacyDone,
    layoutRefusal: layoutRefusal,
    staleRail: staleRail,
    starred: starred,
    endedOpen: endedOpen,
    state: state,
    marks: marks,
    foldMatch: foldMatch,
    foldSource: foldSource,
    drop: drop,
    dropRefusal: dropRefusal,
    kidsCollapsed: kidsCollapsed,
    rowFacts: rowFacts,
    dropZone: dropZone,
    dropNoop: dropNoop,
    moveRow: moveRow,
    homeRows: homeRows,
    keyMove: keyMove,
    underRows: underRows,
    itemKey: itemKey,
    refind: refind,
    swapGate: swapGate,
    markCurrent: markCurrent,
    attention: attention,
    notices: notices,
    rowAsks: rowAsks,
    reviveRoute: reviveRoute,
    markRevive: markRevive,
    handedNotice: handedNotice,
    leaveGuard: leaveGuard,
    popPlace: popPlace,
    topFit: topFit,
    topWatch: topWatch,
    botFit: botFit,
    botWatch: botWatch,
    matchBranches: matchBranches,
    worktreeBody: worktreeBody,
    deleteReady: deleteReady,
    deleteBody: deleteBody,
    leadBody: leadBody,
    approvalText: approvalText
  };
})();
