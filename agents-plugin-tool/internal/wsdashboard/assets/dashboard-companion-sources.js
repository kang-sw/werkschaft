(function (C) {
  "use strict";
  // The Sources section of the companion modal: pack sources (the built-in
  // official one plus the user's dashboard.companion_sources list), a listing
  // panel with install/update/uninstall, a local zip import with a validate
  // preview, and the authoring links. The browser never fetches a source URL;
  // it only calls the daemon's /api/companion routes. Every marketplace, pack
  // and server string is rendered with textContent, never as markup, and only
  // https: URLs become links.
  const api = "/api/companion", official = "official", maxSources = 16, maxURL = 2048;
  const sourcesKey = "dashboard.companion_sources";
  const authoring = [
    [api + "/authoring/guide.md", "js.companion.sources.guide"],
    [api + "/authoring/pack.schema.json", "js.companion.sources.pack_schema"],
    [api + "/authoring/marketplace.schema.json", "js.companion.sources.marketplace_schema"]
  ];
  const importable = new Set(["available", "installed", "update", "other"]);
  const statusBadges = {
    installed: "js.companion.sources.badge_installed", update: "js.companion.sources.badge_update",
    other: "js.companion.sources.badge_other", manual: "js.companion.sources.badge_manual",
    builtin: "js.companion.sources.badge_builtin"
  };

  function t(key, ...args) { return wsT(key, ...args); }
  function text(value) { return typeof value === "string" ? value : ""; }
  function record(value) { return value !== null && typeof value === "object" && !Array.isArray(value); }

  function utf8Length(value) {
    let n = 0;
    for (const ch of value) {
      const c = ch.codePointAt(0);
      n += c < 0x80 ? 1 : c < 0x800 ? 2 : c < 0x10000 ? 3 : 4;
    }
    return n;
  }

  // urlError is a quick local check, used before an entry is written and for
  // a translated message: an absolute https URL of at most 2048 bytes with no
  // userinfo. The daemon's source rules are stricter; resolveSources lets its
  // verdicts decide. It returns an i18n key.
  function urlError(url) {
    if (utf8Length(url) > maxURL) return "js.companion.sources.err_long";
    if (/[\u0000-\u001f\u007f-\u009f\s]/.test(url)) return "js.companion.sources.err_url";
    const match = /^https:\/\/([^/?#]*)/i.exec(url);
    if (!match) return "js.companion.sources.err_url";
    const at = match[1].lastIndexOf("@");
    if (!match[1].slice(at + 1)) return "js.companion.sources.err_url";
    return at >= 0 ? "js.companion.sources.err_creds" : "";
  }

  // parseSources splits the raw value with the local check. An invalid entry
  // keeps its error key and never uses up one of the 16 slots.
  function parseSources(raw) {
    const entries = [], seen = new Set();
    let valid = 0;
    for (const url of text(raw).split(/\s+/).filter(Boolean)) {
      let error = "";
      if (seen.has(url)) error = "js.companion.sources.err_dup";
      else if (valid >= maxSources) error = "js.companion.sources.err_limit";
      else error = urlError(url);
      seen.add(url);
      if (!error) valid++;
      entries.push({ url, error });
    }
    return entries;
  }

  // resolveSources judges each entry of raw by the daemon's verdicts (the
  // config payload's sourceEntries, from the parser the source routes use),
  // so an entry the routes refuse never looks usable and the 16 slots count
  // as the daemon counts them. The local check only supplies a translated
  // message where it agrees; until verdicts for this exact list arrive (just
  // after a write) it stands in. Each returned error is display text.
  function resolveSources(raw, verdicts) {
    const local = parseSources(raw), limit = "js.companion.sources.err_limit";
    const known = Array.isArray(verdicts) && verdicts.length === local.length &&
      verdicts.every((verdict, i) => record(verdict) && verdict.url === local[i].url);
    if (!known) return local.map(entry => ({ url: entry.url, error: entry.error ? t(entry.error) : "" }));
    let valid = 0;
    return local.map((entry, i) => {
      const refused = text(verdicts[i].error);
      let error = "";
      if (!refused) valid++;
      else if (entry.error && entry.error !== limit) error = t(entry.error);
      else if (valid >= maxSources) error = t(limit);
      else error = refused;
      return { url: entry.url, error };
    });
  }

  function isHTTPS(url) { return typeof url === "string" && /^https:\/\/[^\s\u0000-\u001f\u007f-\u009f]+$/i.test(url); }

  function listingOf(value) {
    if (!record(value)) return null;
    return {
      source: text(value.source), kind: text(value.kind), official: value.official === true,
      commit: text(value.commit), name: text(value.name),
      owner: record(value.owner) ? { name: text(value.owner.name), url: text(value.owner.url) } : null,
      packs: (Array.isArray(value.packs) ? value.packs : []).filter(pack => record(pack) && text(pack.id)),
      errors: (Array.isArray(value.errors) ? value.errors : []).filter(record)
    };
  }

  function sourcesSection(deps) {
    const { doc, signal, options } = deps;
    const state = { catalog: null, panel: null, busy: false, message: "", failed: false, addError: "",
      optimistic: null, lastConfig: undefined, signature: "" };
    const details = doc.createElement("details");
    details.className = "ws-companion-sources";
    const summary = doc.createElement("summary");
    summary.textContent = t("js.companion.sources.title");
    details.append(summary);

    let keyed = new Map();

    function canWrite() { return deps.canWrite(); }
    function raw() { return state.optimistic ?? text(deps.raw()); }
    function el(tag, className, content) {
      const item = doc.createElement(tag);
      if (className) item.className = className;
      if (content !== undefined) item.textContent = content;
      return item;
    }
    function button(label, key, onClick, extra = {}) {
      const item = el("button", "ws-companion-settings-action ws-companion-sources-button", label);
      item.type = "button";
      item.focusKey = key;
      item.disabled = state.busy || !canWrite() || extra.disabled === true;
      if (extra.title) item.title = extra.title;
      item.addEventListener("click", onClick, options);
      keyed.set(key, item);
      return item;
    }
    // link shows the full URL; only https: URLs are links.
    function link(url) {
      if (!isHTTPS(url)) return el("span", "ws-companion-sources-url", url);
      const item = el("a", "ws-companion-sources-url", url);
      item.href = url;
      item.target = "_blank";
      item.rel = "noopener noreferrer";
      return item;
    }
    function warning() { return el("p", "ws-companion-sources-warn", t("js.companion.sources.warning")); }
    function badge(key) { return el("span", "ws-companion-sources-badge", t(key)); }

    async function send(url, init = {}) {
      try {
        const response = await fetch(url, { method: "POST", credentials: "same-origin", signal, ...init });
        let body = null;
        try { body = await response.json(); } catch (error) { body = null; }
        return { ok: response.ok, status: response.status, body: record(body) ? body : {} };
      } catch (error) {
        return signal.aborted ? null : { ok: false, status: 0, body: {} };
      }
    }
    function json(body) {
      return { headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) };
    }
    function failure(res) {
      return text(res.body.error) || t("js.companion.sources.request_failed", res.status);
    }

    // guard runs one write action: a single action at a time, only where the
    // page may write, keeping keyboard focus on the same control afterwards.
    async function guard(action) {
      if (state.busy || !canWrite()) return;
      const focusKey = doc.activeElement?.focusKey;
      state.busy = true;
      state.message = "";
      state.failed = false;
      render();
      try {
        await action();
      } finally {
        state.busy = false;
        render();
        const again = keyed.get(focusKey);
        if (again && !again.disabled) again.focus({ preventScroll: true });
      }
    }
    // packFailure is why one pack of an HTTP 200 result was not written.
    function packFailure(pack, res) {
      return text(pack?.error?.message) || t("js.companion.sources.request_failed", res.status);
    }

    function done(message) { state.message = message; state.failed = false; }
    function fail(message) { state.message = message; state.failed = true; }

    // Before a write lands, raw() is the optimistic list and the verdicts
    // (for the daemon's list) do not match it, so the local check stands in.
    function entries() { return resolveSources(raw(), deps.verdicts?.()); }
    function listed(url) { return entries().some(entry => !entry.error && entry.url === url); }

    async function writeSources(urls) {
      const value = urls.join(" ");
      const res = await send("/api/config/set", json({ scope: "global", key: sourcesKey, value }));
      if (!res?.ok) {
        if (res) deps.notice("settings_failed");
        return false;
      }
      state.optimistic = value;
      return true;
    }

    function addProblem(url) {
      const list = entries();
      const error = urlError(url);
      if (error) return error;
      if (list.some(entry => entry.url === url)) return "js.companion.sources.err_dup";
      if (list.filter(entry => !entry.error).length >= maxSources) return "js.companion.sources.err_limit";
      return "";
    }

    const addInput = el("input");
    addInput.type = "text";
    addInput.placeholder = "https://example.invalid/packs.git";
    addInput.setAttribute("aria-label", t("js.companion.sources.add_label"));
    const addOrigin = el("p", "ws-companion-sources-origin");
    addInput.addEventListener("input", () => {
      const url = addInput.value.trim();
      addOrigin.textContent = url ? t("js.companion.sources.origin", url) : "";
    }, options);
    addInput.addEventListener("keydown", event => {
      if (event.key === "Enter") { event.preventDefault(); add(); }
    }, options);

    function add() {
      const url = addInput.value.trim();
      if (!url || state.busy || !canWrite()) return;
      const problem = addProblem(url);
      state.addError = problem;
      if (problem) { render(); return; }
      guard(async () => {
        if (await writeSources([...entries().map(entry => entry.url), url])) {
          addInput.value = "";
          addOrigin.textContent = "";
        }
      });
    }

    function remove(index) {
      guard(async () => {
        const urls = entries().map(entry => entry.url);
        const [gone] = urls.splice(index, 1);
        if (await writeSources(urls) && state.panel?.source === gone && !urls.includes(gone)) state.panel = null;
      });
    }

    function applyCatalog(catalog) {
      if (Array.isArray(catalog?.packs)) state.catalog = catalog;
      deps.catalogChanged(Array.isArray(catalog?.packs) ? catalog : null);
    }

    function fetchSource(source) {
      guard(async () => {
        const res = await send(api + "/sources/fetch", json({ source }));
        if (!res) return;
        const listing = listingOf(res.body.listing);
        if (listing) state.panel = { mode: "listing", source, listing };
        if (!res.ok && !listing) fail(failure(res));
        else if (!res.ok) fail(t("js.companion.sources.fetch_failed"));
      });
    }

    function install(panel, pack) {
      guard(async () => {
        const res = await send(api + "/sources/install", json({ source: panel.source, ids: [pack.id] }));
        if (!res) return;
        const listing = listingOf(res.body.listing);
        if (!res.ok) { fail(failure(res)); return; }
        if (listing) state.panel = { mode: "listing", source: panel.source, listing };
        // HTTP 200 only means the request ran; the pack's own result says whether it was written.
        const outcome = outcomeFor(listing?.packs, panel.listing.packs, pack);
        if (outcome?.installed === true && !record(outcome.error)) done(t("js.companion.sources.done_install", text(pack.name) || pack.id));
        else fail(packFailure(outcome, res));
        applyCatalog(res.body.catalog);
      });
    }

    // A marketplace may carry an invalid and a valid row under one ID. The result lists rows in the
    // order of the listing that was acted on, so the selected row's place among its same-ID rows
    // finds its own outcome, success or failure; a duplicate's error is never reported for it.
    function outcomeFor(packs, before, selected) {
      const same = list => (list || []).filter(item => record(item) && item.id === selected.id);
      const rows = same(packs), index = same(before).indexOf(selected);
      if (index >= 0 && rows.length === same(before).length) return rows[index];
      return rows.find(item => item.installed === true && !record(item.error)) || rows[0];
    }

    function uninstall(id, name) {
      guard(async () => {
        const res = await send(api + "/packs/" + encodeURIComponent(id) + "/uninstall");
        if (!res) return;
        if (!res.ok) { fail(failure(res)); return; }
        // The shown listing predates this click: its cards for the pack go back to available.
        for (const pack of state.panel?.listing.packs || []) {
          if (pack.id === id && (pack.status === "installed" || pack.status === "update")) pack.status = "available";
        }
        done(t("js.companion.sources.done_uninstall", name || id));
        applyCatalog(res.body);
      });
    }

    const fileInput = el("input");
    fileInput.type = "file";
    fileInput.accept = ".zip,application/zip";
    fileInput.setAttribute("aria-label", t("js.companion.sources.import_label"));
    fileInput.addEventListener("change", () => {
      const file = fileInput.files?.[0];
      if (!file) return;
      validate(file);
    }, options);

    function importURL(action, file, ids = []) {
      return api + "/packs/" + action + "?name=" + encodeURIComponent(file.name) +
        ids.map(id => "&id=" + encodeURIComponent(id)).join("");
    }

    function validate(file) {
      guard(async () => {
        const res = await send(importURL("validate", file), { headers: { "Content-Type": "application/zip" }, body: file });
        fileInput.value = "";
        if (!res) return;
        const listing = listingOf(res.body.listing);
        if (!listing) { fail(failure(res)); return; }
        const selected = new Set(listing.packs.filter(pack => canPick(pack) && pack.status !== "installed").map(pack => pack.id));
        state.panel = { mode: "preview", source: "", name: file.name, file, listing, selected };
        if (!res.ok) fail(t("js.companion.sources.validate_failed"));
      });
    }

    function canPick(pack) { return !record(pack.error) && importable.has(pack.status); }

    function runImport(panel) {
      // One row per selected ID: the first pickable one, which is the row the user ticked.
      const rows = [];
      for (const pack of panel.listing.packs) {
        if (panel.selected.has(pack.id) && !rows.some(row => row.id === pack.id) && canPick(pack)) rows.push(pack);
      }
      for (const pack of panel.listing.packs) {
        if (panel.selected.has(pack.id) && !rows.some(row => row.id === pack.id)) rows.push(pack);
      }
      const ids = rows.map(pack => pack.id);
      if (!ids.length) return;
      guard(async () => {
        const res = await send(importURL("import", panel.file, ids), { headers: { "Content-Type": "application/zip" }, body: panel.file });
        if (!res) return;
        if (!res.ok) { fail(failure(res)); return; }
        const listing = listingOf(res.body.listing);
        if (listing) state.panel = { mode: "result", source: "", name: panel.name, listing };
        else state.panel = null;
        // Count what was written, not what was asked for; failed packs keep their error on the result cards.
        const outcomes = rows.map(row => outcomeFor(listing?.packs, panel.listing.packs, row)).filter(Boolean);
        const written = outcomes.filter(item => item.installed === true && !record(item.error));
        const failed = outcomes.filter(item => !written.includes(item));
        if (!listing) done(t("js.companion.sources.done_import", ids.length));
        else if (!failed.length && written.length) done(t("js.companion.sources.done_import", written.length));
        else if (!written.length) fail(packFailure(failed[0], res));
        else fail(t("js.companion.sources.done_import", written.length) + " " + failed.map(item => item.id + ": " + packFailure(item, res)).join(" "));
        applyCatalog(res.body.catalog);
      });
    }

    function originText(listing, panel) {
      if (listing.official || panel?.source === official) return t("js.companion.sources.official_name");
      if (listing.kind === "local") return t("js.companion.sources.origin_local", panel?.name || listing.source);
      return listing.source;
    }

    function installedOrigin(origin) {
      if (origin.official) return t("js.companion.sources.official_name");
      if (origin.kind === "local") return t("js.companion.sources.origin_local", text(origin.source));
      return text(origin.source);
    }

    function catalogPack(id) {
      return (state.catalog?.packs || []).find(pack => record(pack) && pack.id === id) || null;
    }

    function sourceRow(label, source, error) {
      const isOfficial = source === official;
      const row = el("li", "ws-companion-sources-source");
      const name = el("div", "ws-companion-sources-source-name");
      if (isOfficial) name.append(el("span", "", label), badge("js.companion.sources.badge_official"));
      else name.append(link(label));
      row.append(name);
      if (error) row.append(el("p", "ws-companion-sources-error", error));
      else if (!isOfficial) row.append(warning());
      const actions = el("div", "ws-companion-sources-actions");
      if (!error) {
        const active = state.panel?.source === source;
        actions.append(button(t(active ? "js.companion.sources.check" : "js.companion.sources.show"),
          "fetch:" + source, () => fetchSource(source)));
      }
      return { row, actions };
    }

    function sourceList() {
      const group = el("section", "ws-companion-sources-group");
      group.append(el("h3", "", t("js.companion.sources.sources")));
      const list = el("ul", "ws-companion-sources-list");
      const first = sourceRow(t("js.companion.sources.official_name"), official, "");
      first.row.append(first.actions);
      list.append(first.row);
      entries().forEach((entry, index) => {
        const { row, actions } = sourceRow(entry.url, entry.url, entry.error);
        actions.append(button(t("js.companion.sources.remove"), "remove:" + index, () => remove(index)));
        row.append(actions);
        list.append(row);
      });
      group.append(list);
      return group;
    }

    function addForm() {
      const form = el("section", "ws-companion-sources-group");
      form.append(el("h3", "", t("js.companion.sources.add")));
      addInput.disabled = state.busy || !canWrite();
      const line = el("div", "ws-companion-sources-add");
      line.append(addInput, button(t("js.companion.sources.add_button"), "add", add));
      form.append(line, addOrigin, warning());
      if (state.addError) form.append(el("p", "ws-companion-sources-error", t(state.addError)));
      return form;
    }

    function packCard(panel, pack) {
      const card = el("li", "ws-companion-sources-card");
      const head = el("div", "ws-companion-sources-card-head");
      const name = text(pack.name) || pack.id;
      if (panel.mode === "preview") {
        const label = el("label", "ws-companion-sources-pick");
        const box = el("input");
        box.type = "checkbox";
        box.checked = panel.selected.has(pack.id);
        box.disabled = state.busy || !canWrite() || !canPick(pack);
        box.addEventListener("change", () => {
          if (box.checked) panel.selected.add(pack.id); else panel.selected.delete(pack.id);
          render();
        }, options);
        label.append(box, el("strong", "", name));
        head.append(label);
      } else {
        head.append(el("strong", "", name));
      }
      if (text(pack.version)) head.append(el("span", "ws-companion-settings-muted", "v" + text(pack.version)));
      if (statusBadges[pack.status]) head.append(badge(statusBadges[pack.status]));
      card.append(head);
      const meta = [text(pack.author), text(pack.license)].filter(Boolean).join(" · ");
      card.append(el("p", "ws-companion-settings-muted", pack.id + (meta ? " · " + meta : "")));
      if (text(pack.description)) card.append(el("p", "ws-companion-sources-description", text(pack.description)));
      if (text(pack.url)) card.append(link(text(pack.url)));
      const installed = catalogPack(pack.id);
      if (record(installed?.origin)) {
        card.append(el("p", "ws-companion-sources-origin", t("js.companion.sources.installed_from", installedOrigin(installed.origin))));
      }
      if (record(pack.error)) {
        card.append(el("p", "ws-companion-sources-error", text(pack.error.message)));
        return card;
      }
      if (panel.mode !== "listing") return card;
      const actions = el("div", "ws-companion-sources-actions");
      const key = id => id + ":" + pack.id;
      const status = pack.status;
      if (status === "available") actions.append(button(t("js.companion.sources.install"), key("install"), () => install(panel, pack)));
      if (status === "other") actions.append(button(t("js.companion.sources.replace"), key("install"), () => install(panel, pack)));
      if (status === "update") actions.append(button(t("js.companion.sources.update"), key("install"), () => install(panel, pack)));
      const removable = status === "update" || status === "installed" && record(installed?.origin);
      if (removable) actions.append(button(t("js.companion.sources.uninstall"), key("uninstall"), () => uninstall(pack.id, name)));
      if (actions.children.length) card.append(actions);
      return card;
    }

    function panelBlock() {
      const panel = state.panel;
      if (!panel) return null;
      const { listing } = panel;
      const block = el("section", "ws-companion-sources-group ws-companion-sources-panel");
      const heading = panel.mode === "preview" ? "js.companion.sources.preview" : "js.companion.sources.packs";
      block.append(el("h3", "", t(heading)));
      if (listing.name) block.append(el("p", "ws-companion-sources-name", listing.name));
      if (listing.owner?.name || listing.owner?.url) {
        const owner = el("p", "ws-companion-settings-muted", listing.owner.name ? t("js.companion.sources.owner", listing.owner.name) : "");
        if (listing.owner.url) owner.append(" ", link(listing.owner.url));
        block.append(owner);
      }
      const origin = el("p", "ws-companion-sources-origin", t("js.companion.sources.origin", originText(listing, panel)));
      if (listing.commit) origin.append(" @ " + listing.commit.slice(0, 12));
      block.append(origin);
      if (!(listing.official || panel.source === official)) block.append(warning());
      if (listing.errors.length) {
        const errors = el("ul", "ws-companion-sources-errors");
        for (const error of listing.errors) {
          errors.append(el("li", "ws-companion-sources-error",
            [text(error.id), text(error.path), text(error.message)].filter(Boolean).join(": ")));
        }
        block.append(errors);
      }
      if (listing.packs.length) {
        const cards = el("ul", "ws-companion-sources-cards");
        cards.append(...listing.packs.map(pack => packCard(panel, pack)));
        block.append(cards);
      } else if (!listing.errors.length) block.append(el("p", "ws-companion-settings-muted", t("js.companion.sources.empty")));
      const actions = el("div", "ws-companion-sources-actions");
      if (panel.mode === "preview") {
        actions.append(button(t("js.companion.sources.import"), "import", () => runImport(panel),
          { disabled: panel.selected.size === 0 }));
      }
      actions.append(button(t("js.companion.sources.close_listing"), "close", () => { state.panel = null; render(); }, { disabled: false }));
      block.append(actions);
      return block;
    }

    function installedBlock() {
      const installed = (state.catalog?.packs || []).filter(pack => record(pack) && record(pack.origin) && text(pack.id));
      const group = el("section", "ws-companion-sources-group");
      group.append(el("h3", "", t("js.companion.sources.installed")));
      if (!installed.length) {
        group.append(el("p", "ws-companion-settings-muted", t("js.companion.sources.installed_none")));
        return group;
      }
      const list = el("ul", "ws-companion-sources-cards");
      for (const pack of installed) {
        const { origin } = pack, name = text(pack.name) || pack.id;
        const card = el("li", "ws-companion-sources-card");
        const head = el("div", "ws-companion-sources-card-head");
        head.append(el("strong", "", name));
        if (text(origin.version)) head.append(el("span", "ws-companion-settings-muted", "v" + text(origin.version)));
        if (origin.official === true) head.append(badge("js.companion.sources.badge_official"));
        card.append(head, el("p", "ws-companion-sources-origin", t("js.companion.sources.installed_from", installedOrigin(origin))));
        const actions = el("div", "ws-companion-sources-actions");
        const source = origin.official === true ? official : text(origin.source);
        if (origin.kind !== "local" && (source === official || listed(source))) {
          actions.append(button(t("js.companion.sources.check"), "check:" + pack.id, () => fetchSource(source)));
        }
        actions.append(button(t("js.companion.sources.uninstall"), "uninstall:" + pack.id, () => uninstall(pack.id, name)));
        card.append(actions);
        list.append(card);
      }
      group.append(list);
      return group;
    }

    function importBlock() {
      const group = el("section", "ws-companion-sources-group");
      group.append(el("h3", "", t("js.companion.sources.import_title")), el("p", "ws-companion-settings-muted", t("js.companion.sources.import_hint")));
      fileInput.disabled = state.busy || !canWrite();
      group.append(fileInput);
      return group;
    }

    function authoringBlock() {
      const group = el("section", "ws-companion-sources-group");
      group.append(el("h3", "", t("js.companion.sources.authoring")));
      const list = el("p", "ws-companion-sources-links");
      authoring.forEach(([href, key], index) => {
        const item = el("a", "", t(key));
        item.href = href;
        item.target = "_blank";
        item.rel = "noopener noreferrer";
        list.append(...(index ? [" · "] : []), item);
      });
      group.append(list);
      return group;
    }

    function render() {
      keyed = new Map();
      const body = el("div", "ws-companion-sources-body");
      if (state.message) {
        const line = el("p", state.failed ? "ws-companion-sources-error" : "ws-companion-sources-ok", state.message);
        line.setAttribute("role", state.failed ? "alert" : "status");
        body.append(line);
      }
      body.append(sourceList(), addForm());
      const panel = panelBlock();
      if (panel) body.append(panel);
      body.append(installedBlock(), importBlock(), authoringBlock());
      details.replaceChildren(summary, body);
    }

    // refresh follows the live config: it redraws only when the source list,
    // the daemon's verdicts on it, or the write permission changed, so typing
    // in the add field is undisturbed.
    function signatureOf() {
      return raw() + "\n" + canWrite() + "\n" + JSON.stringify(deps.verdicts?.() ?? null);
    }
    function refresh() {
      const config = deps.raw();
      if (config !== state.lastConfig) { state.lastConfig = config; state.optimistic = null; }
      const signature = signatureOf();
      if (signature === state.signature) return;
      state.signature = signature;
      render();
    }

    function setCatalog(catalog) {
      if (!Array.isArray(catalog?.packs)) return;
      state.catalog = catalog;
      render();
    }

    // Nothing is drawn until the modal builds: redraw runs on every build.
    return {
      node: details, setCatalog, refresh,
      redraw() { state.signature = signatureOf(); render(); }
    };
  }

  Object.assign(C, { sourcesSection, parseCompanionSources: parseSources, resolveCompanionSources: resolveSources });
})(window.WsCompanion);
