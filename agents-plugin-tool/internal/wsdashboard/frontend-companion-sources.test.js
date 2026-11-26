const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

// A DOM just rich enough for the Sources section: elements keep their text and
// children, and the document records every tag it was asked to create so a test
// can prove that pack text never became markup.
function dom() {
  const doc = { created: [], activeElement: null };
  function element(tag) {
    doc.created.push(tag.toLowerCase());
    return {
      tagName: tag.toUpperCase(), className: "", textContent: "", value: "", disabled: false, checked: false,
      children: [], attrs: {}, listeners: {},
      setAttribute(k, v) { this.attrs[k] = String(v); },
      addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); },
      append(...items) { this.children.push(...items); },
      replaceChildren(...items) { this.children = [...items]; },
      focus() { doc.activeElement = this; },
      all() { return this.children.flatMap(child => typeof child === "string" ? [] : [child, ...child.all()]); },
      text() { return this.textContent + this.children.map(c => typeof c === "string" ? c : c.text()).join(""); }
    };
  }
  doc.createElement = element;
  return doc;
}
const fire = (target, type, event = {}) => { for (const fn of target.listeners[type] || []) fn({ preventDefault() {}, ...event }); };
const tick = () => new Promise(resolve => setImmediate(resolve));
const cls = (root, name) => root.all().filter(c => c.className.split(" ").includes(name));
const tagged = (root, tag) => root.all().filter(c => c.tagName === tag);
const buttonOf = (root, label) => root.all().find(c => c.tagName === "BUTTON" && c.textContent === label);

function setup({ raw = "", write = true, handler, catalog, verdicts } = {}) {
  const doc = dom();
  const calls = [], changed = [], notices = [], selectedPacks = [];
  const state = { raw, write, verdicts };
  const fetch = async (url, init = {}) => {
    calls.push({ url, init, body: typeof init.body === "string" ? JSON.parse(init.body) : init.body });
    const out = await (handler?.(url, init, calls.at(-1).body) ?? { status: 200, body: {} });
    return { ok: out.status >= 200 && out.status < 300, status: out.status, json: async () => out.body };
  };
  const context = vm.createContext({ window: {}, URLSearchParams, AbortController, fetch, wsT: (k, ...a) => a.length ? k + ":" + a.join("|") : k });
  for (const name of ["packs", "sources"]) {
    vm.runInContext(fs.readFileSync(path.join(__dirname, "assets", `dashboard-companion-${name}.js`), "utf8"), context);
  }
  const C = context.window.WsCompanion;
  const signal = new AbortController().signal;
  const section = C.sourcesSection({
    doc, signal, options: { signal }, canWrite: () => state.write, raw: () => state.raw,
    verdicts: () => state.verdicts,
    notice: key => notices.push(key),
    selectPack: id => selectedPacks.push(id),
    catalogChanged: next => changed.push(next)
  });
  if (catalog) section.setCatalog(catalog);
  section.redraw();
  return { C, doc, section, calls, changed, notices, selectedPacks, state, root: section.node };
}

const listing = (over = {}) => ({
  source: "https://example.invalid/packs.git", kind: "git", official: false, commit: "0123456789abcdef0123", name: "Example Packs",
  owner: { name: "Example Author", url: "https://example.invalid/" }, packs: [], errors: [], ...over
});
const pack = (id, status, over = {}) => ({ id, name: "Pack " + id, version: "1.0.0", author: "Author", license: "CC-BY-4.0", description: "About " + id, status, ...over });

test("source entries are judged one by one and an invalid entry never uses up a slot", () => {
  const { C } = setup();
  const urls = Array.from({ length: 18 }, (_, i) => `https://example.invalid/s${i}.git`);
  const entries = C.parseCompanionSources(["http://example.invalid/x", "https://user:pw@example.invalid/", urls[0], urls[0], "https:///nohost", "https://example.invalid/" + "a".repeat(2048), ...urls.slice(1)].join("\n "));
  assert.deepEqual(Array.from(entries.slice(0, 6), e => e.error), [
    "js.companion.sources.err_url", "js.companion.sources.err_creds", "", "js.companion.sources.err_dup",
    "js.companion.sources.err_url", "js.companion.sources.err_long"]);
  const ok = entries.filter(e => !e.error), over = entries.filter(e => e.error === "js.companion.sources.err_limit");
  assert.equal(ok.length, 16, "six invalid entries left all 16 slots to the valid ones");
  assert.equal(over.length, 2, "only entries past the 16th valid one are refused");
  assert.equal(C.parseCompanionSources("").length, 0);
});

test("the daemon's verdicts decide which entries are usable and how the slots count", async () => {
  // A git URL with a query passes the local prefix check, but the daemon's
  // source rules refuse it; it must not look usable or take a slot.
  const query = "https://example.invalid/team/repo?tab=readme";
  const urls = Array.from({ length: 16 }, (_, i) => `https://example.invalid/s${i}.git`);
  const raw = [query, ...urls].join(" ");
  const daemonQuery = "a git repository URL must not carry a query; name a branch or tag after # instead";
  const verdicts = [{ url: query, error: daemonQuery }, ...urls.map(url => ({ url }))];
  const rows = t => cls(t.root, "ws-companion-sources-source").slice(1);
  const show = row => buttonOf(row, "js.companion.sources.show");

  const t = setup({ raw, verdicts });
  assert.match(rows(t)[0].text(), /must not carry a query/, "the refused entry shows the daemon's reason");
  assert.equal(show(rows(t)[0]), undefined, "and cannot be fetched");
  assert.ok(!rows(t)[0].text().includes("js.companion.sources.warning"), "an error replaces the warning");
  assert.ok(rows(t).slice(1).every(row => show(row)), "all 16 valid entries are usable: the refused one took no slot");
  assert.ok(!t.root.text().includes("js.companion.sources.err_limit"));

  // Without verdicts for this exact list (none yet, or a stale list), the
  // local check stands in and counts the query URL as a slot.
  for (const stale of [undefined, verdicts.slice(1), [{ url: "https://example.invalid/other.git" }, ...verdicts.slice(1)]]) {
    const local = setup({ raw, verdicts: stale });
    assert.ok(show(rows(local)[0]), "the local check passes the query URL");
    assert.match(rows(local).at(-1).text(), /js\.companion\.sources\.err_limit/);
  }

  // Where the local check agrees, its translated message is shown; a limit
  // refusal is recounted from the daemon's verdicts.
  const extra = "https://example.invalid/extra.git";
  const agree = setup({
    raw: ["http://example.invalid/x", ...urls, extra].join(" "),
    verdicts: [{ url: "http://example.invalid/x", error: "URL must be an absolute https URL" }, ...urls.map(url => ({ url })),
      { url: extra, error: "at most 16 sources are used" }]
  });
  assert.match(rows(agree)[0].text(), /js\.companion\.sources\.err_url/);
  assert.ok(!rows(agree)[0].text().includes("absolute https"));
  assert.match(rows(agree).at(-1).text(), /js\.companion\.sources\.err_limit/);

  // A fetch acts only on an entry the daemon accepts.
  const installed = setup({
    raw, verdicts,
    catalog: { packs: [{ id: "p", name: "P", origin: { source: query, kind: "git", version: "1" } }] }
  });
  assert.equal(buttonOf(installed.root, "js.companion.sources.check"), undefined, "an installed pack's source the daemon refuses offers no check");

  const resolved = t.C.resolveCompanionSources(raw, verdicts);
  assert.equal(resolved.filter(entry => !entry.error).length, 16);
});

test("add and remove rewrite the whole value and keep every other entry as it was", async () => {
  const bad = "http://example.invalid/old";
  const a = "https://example.invalid/a.git", b = "https://example.invalid/b.zip";
  const t = setup({ raw: `${a} ${bad} ${b}` });
  const sourceRows = () => cls(t.root, "ws-companion-sources-source");
  assert.equal(sourceRows().length, 4, "the official source plus three entries");
  const invalid = sourceRows()[2];
  assert.match(invalid.text(), /http:\/\/example\.invalid\/old/);
  assert.match(invalid.text(), /js\.companion\.sources\.err_url/, "an invalid entry shows its error");
  assert.ok(buttonOf(invalid, "js.companion.sources.remove"), "and a Remove button");
  assert.equal(buttonOf(invalid, "js.companion.sources.show"), undefined, "but cannot be fetched");

  fire(buttonOf(sourceRows()[1], "js.companion.sources.remove"), "click");
  await tick();
  assert.deepEqual(t.calls.at(-1).body, { scope: "global", key: "dashboard.companion_sources", value: `${bad} ${b}` });
  assert.equal(t.calls.at(-1).url, "/api/config/set");
  assert.equal(sourceRows().length, 3, "the list shows the written value before the config stream confirms");

  const input = t.root.all().find(c => c.tagName === "INPUT" && c.type === undefined || c.placeholder);
  input.value = "  https://example.invalid/c.git ";
  fire(input, "input");
  assert.match(cls(t.root, "ws-companion-sources-origin")[0].text(), /^js\.companion\.sources\.origin:https:\/\/example\.invalid\/c\.git$/, "the add form shows the full source origin");
  fire(buttonOf(t.root, "js.companion.sources.add_button"), "click");
  await tick();
  assert.deepEqual(t.calls.at(-1).body.value, `${bad} ${b} https://example.invalid/c.git`);

  input.value = b;
  fire(buttonOf(t.root, "js.companion.sources.add_button"), "click");
  assert.equal(t.calls.length, 2, "an invalid or duplicate entry is not written");
  assert.match(t.root.text(), /js\.companion\.sources\.err_dup/);
  input.value = "http://example.invalid/new";
  fire(buttonOf(t.root, "js.companion.sources.add_button"), "click");
  assert.match(t.root.text(), /js\.companion\.sources\.err_url/);
  assert.equal(t.calls.length, 2);
});

test("a full list refuses another entry but a list of invalid entries does not", async () => {
  const full = Array.from({ length: 16 }, (_, i) => `https://example.invalid/s${i}`).join(" ");
  const t = setup({ raw: full });
  const input = t.root.all().find(c => c.placeholder);
  input.value = "https://example.invalid/extra";
  fire(buttonOf(t.root, "js.companion.sources.add_button"), "click");
  assert.equal(t.calls.length, 0);
  assert.match(t.root.text(), /js\.companion\.sources\.err_limit/);
  const t2 = setup({ raw: Array.from({ length: 20 }, (_, i) => `http://example.invalid/s${i}`).join(" ") });
  const input2 = t2.root.all().find(c => c.placeholder);
  input2.value = "https://example.invalid/extra";
  fire(buttonOf(t2.root, "js.companion.sources.add_button"), "click");
  await tick();
  assert.equal(t2.calls.length, 1, "invalid entries leave every slot free");
});

test("the third-party warning shows for user sources and listings and never for the official source", async () => {
  const warn = "js.companion.sources.warning";
  const user = "https://example.invalid/packs.git";
  const t = setup({
    raw: user,
    handler: (url, init, body) => ({ status: 200, body: { listing: body.source === "official"
      ? listing({ source: "official", kind: "zip", official: true, name: "Official", packs: [pack("default", "builtin")] })
      : listing({ packs: [pack("a", "available")] }) } })
  });
  const rows = cls(t.root, "ws-companion-sources-source");
  assert.ok(!rows[0].text().includes(warn), "the official source row carries no warning");
  assert.ok(rows[1].text().includes(warn), "a user source row carries it");
  assert.ok(cls(t.root, "ws-companion-sources-group").some(g => g.text().includes(warn) && g.text().includes("add_button")), "and so does the add form");

  fire(buttonOf(rows[0], "js.companion.sources.show"), "click");
  await tick(); await tick();
  assert.deepEqual(t.calls.at(-1).body, { source: "official" });
  const panel = () => cls(t.root, "ws-companion-sources-panel")[0];
  assert.ok(!panel().text().includes(warn), "the official listing carries no warning");
  assert.match(panel().text(), /js\.companion\.sources\.official_name/);

  fire(buttonOf(cls(t.root, "ws-companion-sources-source")[1], "js.companion.sources.show"), "click");
  await tick(); await tick();
  assert.deepEqual(t.calls.at(-1), { url: "/api/companion/sources/fetch", init: t.calls.at(-1).init, body: { source: user } });
  assert.ok(panel().text().includes(warn), "a user listing carries it");
  assert.match(panel().text(), /js\.companion\.sources\.origin:https:\/\/example\.invalid\/packs\.git/, "with the full source URL");
  assert.ok(buttonOf(cls(t.root, "ws-companion-sources-source")[1], "js.companion.sources.check"), "a shown source offers Check for updates");
});

test("marketplace and pack text is rendered as text, never markup, and only https URLs link", async () => {
  const hostile = "<img src=x onerror=alert(1)>";
  const t = setup({
    raw: "https://example.invalid/packs.git",
    handler: () => ({ status: 200, body: { listing: listing({
      name: hostile, owner: { name: hostile, url: "javascript:alert(1)" },
      errors: [{ id: hostile, path: "<svg onload=1>", message: "<script>x</script>" }],
      packs: [
        pack("evil", "available", { name: hostile, author: hostile, license: hostile, description: hostile, url: "javascript:alert(1)", version: hostile }),
        pack("web", "available", { url: "https://example.invalid/web.zip" }),
        pack("plain", "available", { url: "http://example.invalid/plain.zip" }),
        pack("oops", "available", { error: { id: "oops", path: "pack.json", message: hostile } })
      ] }) } })
  });
  const before = t.doc.created.length;
  fire(buttonOf(cls(t.root, "ws-companion-sources-source")[1], "js.companion.sources.show"), "click");
  await tick(); await tick();
  const created = t.doc.created.slice(before);
  assert.ok(!created.some(tag => ["img", "svg", "script"].includes(tag)), "no element came from pack text: " + created);
  assert.ok(cls(t.root, "ws-companion-sources-panel")[0].text().includes(hostile), "the text itself is shown");
  const anchors = tagged(cls(t.root, "ws-companion-sources-panel")[0], "A").filter(a => a.href);
  assert.ok(anchors.every(a => /^https:\/\//.test(a.href)), "every link is https");
  assert.ok(anchors.some(a => a.href === "https://example.invalid/web.zip" && a.textContent === a.href), "an https pack URL is a link showing the full URL");
  assert.ok(anchors.every(a => a.rel === "noopener noreferrer" && (a.target === "_blank")), "links open safely");
  assert.ok(!anchors.some(a => a.href.startsWith("javascript:") || a.href.startsWith("http:")), "javascript: and http: URLs are plain text");
  assert.ok(t.root.text().includes("javascript:alert(1)") && t.root.text().includes("http://example.invalid/plain.zip"));
  const cards = cls(t.root, "ws-companion-sources-card");
  assert.deepEqual(cards[3].all().filter(c => c.tagName === "BUTTON"), [], "a pack with an error has no action");
});

test("each pack status maps to its badge and buttons", async () => {
  const origin = { source: "https://example.invalid/packs.git", kind: "git", version: "1.0.0", installedAt: "t", official: false };
  const catalog = { packs: [
    { id: "installed-own", name: "x", origin }, { id: "installed-hand", name: "x" }, { id: "upd", name: "x", origin }
  ] };
  const statuses = ["available", "installed-own", "installed-hand", "upd", "other", "manual", "builtin"];
  const t = setup({
    raw: "https://example.invalid/packs.git", catalog,
    handler: () => ({ status: 200, body: { listing: listing({ packs: [
      pack("available", "available"), pack("installed-own", "installed"), pack("installed-hand", "installed"),
      pack("upd", "update"), pack("other", "other"), pack("manual", "manual"), pack("builtin", "builtin")] }) } })
  });
  fire(buttonOf(cls(t.root, "ws-companion-sources-source")[1], "js.companion.sources.show"), "click");
  await tick(); await tick();
  const cards = cls(cls(t.root, "ws-companion-sources-panel")[0], "ws-companion-sources-card");
  const view = card => ({
    badges: cls(card, "ws-companion-sources-badge").map(b => b.textContent),
    buttons: tagged(card, "BUTTON").map(b => b.textContent)
  });
  const [available, own, hand, upd, other, manual, builtin] = cards.map(view);
  const k = s => "js.companion.sources." + s;
  assert.deepEqual(available, { badges: [], buttons: [k("install")] });
  assert.deepEqual(own, { badges: [k("badge_installed")], buttons: [k("uninstall")] }, "installed with an origin offers Uninstall");
  assert.deepEqual(hand, { badges: [k("badge_installed")], buttons: [] }, "installed without a catalog origin has no action");
  assert.deepEqual(upd, { badges: [k("badge_update")], buttons: [k("update"), k("uninstall")] });
  assert.deepEqual(other, { badges: [k("badge_other")], buttons: [k("replace")] });
  assert.deepEqual(manual, { badges: [k("badge_manual")], buttons: [] });
  assert.deepEqual(builtin, { badges: [k("badge_builtin")], buttons: [] });
  assert.equal(statuses.length, cards.length);
});

test("install, update and uninstall call the daemon, take the returned catalog and reload the pack", async () => {
  const origin = { source: "https://example.invalid/packs.git", kind: "git", version: "1.0.0", installedAt: "t", official: false };
  const fresh = { packs: [{ id: "default" }, { id: "a", name: "Pack a", origin }] };
  const t = setup({
    raw: "https://example.invalid/packs.git",
    handler: (url, init, body) => {
      if (url.endsWith("/sources/fetch")) return { status: 200, body: { listing: listing({ packs: [pack("a", "available")] }) } };
      if (url.endsWith("/sources/install")) return { status: 200, body: { listing: listing({ packs: [pack("a", "installed", { installed: true })] }), catalog: fresh } };
      if (url.endsWith("/uninstall")) return { status: 200, body: { packs: [{ id: "default" }] } };
      return { status: 500, body: {} };
    }
  });
  fire(buttonOf(cls(t.root, "ws-companion-sources-source")[1], "js.companion.sources.show"), "click");
  await tick(); await tick();
  fire(buttonOf(t.root, "js.companion.sources.install"), "click");
  await tick(); await tick();
  assert.deepEqual(t.calls.at(-1).body, { source: "https://example.invalid/packs.git", ids: ["a"] });
  assert.equal(t.changed.length, 1);
  assert.equal(t.changed[0], fresh, "the returned catalog reaches the modal for its pack ids");
  assert.match(t.root.text(), /js\.companion\.sources\.done_install:Pack a/);
  assert.ok(cls(t.root, "ws-companion-sources-card").some(c => c.text().includes("badge_installed")), "the listing shows the new status");
  const installed = cls(t.root, "ws-companion-sources-group").find(g => g.text().includes("js.companion.sources.installed_from"));
  assert.ok(installed, "the Installed packs list shows the origin");

  const uninstall = tagged(t.root, "BUTTON").find(b => b.focusKey === "uninstall:a");
  fire(uninstall, "click");
  await tick(); await tick();
  assert.equal(t.calls.at(-1).url, "/api/companion/packs/a/uninstall");
  assert.equal(t.changed.length, 2);
  assert.deepEqual(t.changed[1], { packs: [{ id: "default" }] });
  assert.ok(cls(t.root, "ws-companion-sources-card").some(c => tagged(c, "BUTTON").some(b => b.textContent === "js.companion.sources.install")), "the shown listing card returns to available");
});

test("installed packs offer Check for updates only for a non-local origin from a listed or official source", () => {
  const listed = "https://example.invalid/listed.git";
  const mk = (id, over) => ({ id, name: id, origin: { source: listed, kind: "git", version: "1", installedAt: "t", official: false, ...over } });
  const t = setup({
    raw: listed,
    catalog: { packs: [mk("listed", {}), mk("gone", { source: "https://example.invalid/removed.git" }), mk("local", { kind: "local", source: "pack.zip" }),
      mk("off", { official: true, source: "official", kind: "zip" })] }
  });
  const card = id => cls(t.root, "ws-companion-sources-card").find(c => c.text().includes(id + ""));
  const checks = id => tagged(cls(t.root, "ws-companion-sources-card").find(c => tagged(c, "STRONG")[0].textContent === id), "BUTTON").map(b => b.textContent);
  assert.deepEqual(checks("listed"), ["js.companion.sources.check", "js.companion.sources.uninstall"]);
  assert.deepEqual(checks("gone"), ["js.companion.sources.uninstall"], "a source that is no longer listed cannot be checked");
  assert.deepEqual(checks("local"), ["js.companion.sources.uninstall"], "a local install has nothing to re-fetch");
  assert.deepEqual(checks("off"), ["js.companion.sources.check", "js.companion.sources.uninstall"]);
  assert.ok(card("local").text().includes("origin_local:pack.zip"), "a local origin shows the file name");
});

test("the import preview validates the file, lists passed packs as checkboxes, and imports the same bytes with the chosen ids", async () => {
  const file = { name: "Fan Pack.zip", size: 3 };
  let sent = [];
  const t = setup({
    handler: (url, init) => {
      sent.push(init.body);
      if (url.startsWith("/api/companion/packs/validate")) {
        return { status: 200, body: { listing: listing({ source: "Fan Pack.zip", kind: "local", name: "Fan", packs: [
          pack("a", "available"), pack("b", "available"), pack("c", "installed"), pack("bad", "available", { error: { id: "bad", path: "pack.json", message: "no poses.stand" } }), pack("hand", "manual")] }) } };
      }
      return { status: 200, body: { listing: listing({ kind: "local", name: "Fan", packs: [pack("a", "installed", { installed: true }), pack("b", "installed", { installed: true }), pack("c", "installed", { installed: true })] }), catalog: { packs: [{ id: "default" }, { id: "a" }, { id: "b" }] } } };
    }
  });
  const input = tagged(t.root, "INPUT").find(c => c.type === "file");
  assert.equal(input.accept, ".zip,application/zip");
  input.files = [file];
  fire(input, "change");
  await tick(); await tick();
  assert.equal(t.calls[0].url, "/api/companion/packs/validate?name=Fan%20Pack.zip");
  const panel = () => cls(t.root, "ws-companion-sources-panel")[0];
  const boxes = () => tagged(panel(), "INPUT").filter(c => c.type === "checkbox");
  assert.deepEqual(boxes().map(b => [b.checked, b.disabled]), [[true, false], [true, false], [false, false], [false, true], [false, true]],
    "passed packs are pickable; an erroring or hand-made one is not; an identical installed one starts unchecked");
  assert.match(panel().text(), /origin:js\.companion\.sources\.origin_local:Fan Pack\.zip|origin_local:Fan Pack\.zip/);
  assert.ok(panel().text().includes("js.companion.sources.warning"), "a local import carries the warning");
  assert.match(panel().text(), /no poses\.stand/);

  fire(boxes()[1], "change", {});
  boxes()[1].checked = false;
  fire(boxes()[1], "change");
  fire(boxes()[2], "change");
  boxes()[2].checked = true;
  fire(boxes()[2], "change");
  fire(buttonOf(t.root, "js.companion.sources.import"), "click");
  await tick(); await tick();
  const call = t.calls.at(-1);
  assert.equal(call.url, "/api/companion/packs/import?name=Fan%20Pack.zip&id=a&id=c");
  assert.equal(call.init.body, file, "the same File is sent again");
  assert.equal(sent[0], sent.at(-1));
  assert.equal(t.changed.length, 1);
  assert.deepEqual(t.changed[0], { packs: [{ id: "default" }, { id: "a" }, { id: "b" }] });
  assert.match(t.root.text(), /done_import:2/);
  assert.deepEqual(tagged(cls(t.root, "ws-companion-sources-panel")[0], "BUTTON").map(b => b.textContent), ["js.companion.sources.close_listing"], "the result has no install buttons for a local file");
});

test("a 200 install or import reports each pack's own result, not the request's", async () => {
  const bad = { error: { id: "a", path: "pack.json", message: "cannot move the staged pack into place" } };
  let installBody;
  const t = setup({
    raw: "https://example.invalid/packs.git",
    handler: (url) => {
      if (url.endsWith("/sources/fetch")) return { status: 200, body: { listing: listing({ packs: [pack("a", "available")] }) } };
      if (url.endsWith("/sources/install")) return { status: 200, body: installBody };
      if (url.startsWith("/api/companion/packs/validate")) return { status: 200, body: { listing: listing({ kind: "local", packs: [pack("a", "available"), pack("b", "available")] }) } };
      return { status: 200, body: { listing: listing({ kind: "local", packs: [pack("a", "available", { ...bad }), pack("b", "installed", { installed: true })] }) } };
    }
  });
  fire(buttonOf(cls(t.root, "ws-companion-sources-source")[1], "js.companion.sources.show"), "click");
  await tick(); await tick();

  installBody = { listing: listing({ packs: [pack("a", "available", { installed: false })] }) };
  fire(buttonOf(t.root, "js.companion.sources.install"), "click");
  await tick(); await tick();
  assert.ok(!t.root.text().includes("done_install"), "installed:false is not announced as success");

  installBody = { listing: listing({ packs: [pack("a", "available", { ...bad })] }) };
  fire(buttonOf(t.root, "js.companion.sources.install"), "click");
  await tick(); await tick();
  assert.ok(!t.root.text().includes("done_install"), "a per-pack error is not announced as success");
  assert.match(t.root.text(), /cannot move the staged pack into place/);

  const input = tagged(t.root, "INPUT").find(c => c.type === "file");
  input.files = [{ name: "Fan.zip", size: 3 }];
  fire(input, "change");
  await tick(); await tick();
  fire(buttonOf(t.root, "js.companion.sources.import"), "click");
  await tick(); await tick();
  const text = t.root.text();
  assert.match(text, /done_import:1/, "one of the two requested packs was written");
  assert.ok(!/done_import:2/.test(text), "the requested count is not reported");
  assert.match(text, /a: cannot move the staged pack into place/);
});

test("a duplicate-ID invalid row does not mask the pack that installed", async () => {
  const bad = { error: { id: "a", path: "pack.json", message: "duplicate row is invalid" } };
  const both = () => listing({ packs: [pack("a", "available", { ...bad }), pack("a", "installed", { installed: true })] });
  const t = setup({
    raw: "https://example.invalid/packs.git",
    handler: (url) => {
      if (url.endsWith("/sources/fetch")) return { status: 200, body: { listing: listing({ packs: [pack("a", "available")] }) } };
      if (url.endsWith("/sources/install")) return { status: 200, body: { listing: both() } };
      if (url.startsWith("/api/companion/packs/validate")) return { status: 200, body: { listing: listing({ kind: "local", packs: [pack("a", "available")] }) } };
      return { status: 200, body: { listing: listing({ kind: "local", packs: [pack("a", "available", { ...bad }), pack("a", "installed", { installed: true })] }) } };
    }
  });
  fire(buttonOf(cls(t.root, "ws-companion-sources-source")[1], "js.companion.sources.show"), "click");
  await tick(); await tick();
  fire(buttonOf(t.root, "js.companion.sources.install"), "click");
  await tick(); await tick();
  assert.match(t.root.text(), /done_install/, "the valid duplicate's install is announced");
  assert.ok(!t.root.text().includes("duplicate row is invalid") || !/js\.companion\.sources\.install_failed/.test(t.root.text()), "no failure notice for the invalid duplicate");

  const input = tagged(t.root, "INPUT").find(c => c.type === "file");
  input.files = [{ name: "Fan.zip", size: 3 }];
  fire(input, "change");
  await tick(); await tick();
  fire(buttonOf(t.root, "js.companion.sources.import"), "click");
  await tick(); await tick();
  assert.match(t.root.text(), /done_import:1/);
  assert.ok(!/a: duplicate row is invalid/.test(t.root.text()), "the import summary counts no false failure");
});

test("a rejected import file shows the daemon's reason and offers no import", async () => {
  const file = { name: "big.zip" };
  const t = setup({ handler: () => ({ status: 413, body: { error: "file is over 64 MiB" } }) });
  const input = tagged(t.root, "INPUT").find(c => c.type === "file");
  input.files = [file];
  fire(input, "change");
  await tick(); await tick();
  assert.match(t.root.text(), /file is over 64 MiB/);
  assert.equal(cls(t.root, "ws-companion-sources-panel").length, 0);
  const bad = setup({ handler: () => ({ status: 422, body: { listing: listing({ kind: "local", packs: [], errors: [{ id: "", path: "marketplace.json", message: "schema is not supported" }] }) } }) });
  const field = tagged(bad.root, "INPUT").find(c => c.type === "file");
  field.files = [file];
  fire(field, "change");
  await tick(); await tick();
  assert.match(bad.root.text(), /marketplace\.json: schema is not supported/);
  assert.equal(buttonOf(bad.root, "js.companion.sources.import").disabled, true, "nothing selected, nothing to import");
});

test("a LAN viewer sees the Sources section with every write control disabled and sends nothing", async () => {
  const t = setup({ raw: "https://example.invalid/packs.git http://example.invalid/bad", write: false,
    catalog: { packs: [{ id: "a", name: "a", origin: { source: "official", kind: "zip", version: "1", installedAt: "t", official: true } }] } });
  const controls = t.root.all().filter(c => ["BUTTON", "INPUT"].includes(c.tagName));
  assert.ok(controls.length > 6);
  assert.ok(controls.every(c => c.disabled), "buttons, the add field and the file input are all disabled");
  const click = buttonOf(cls(t.root, "ws-companion-sources-source")[1], "js.companion.sources.show");
  fire(click, "click");
  await tick();
  assert.equal(t.calls.length, 0);
  assert.deepEqual(tagged(t.root, "A").map(a => a.href).filter(href => href.startsWith("/api/")), [
    "/api/companion/authoring/guide.md", "/api/companion/authoring/pack.schema.json", "/api/companion/authoring/marketplace.schema.json"]);
});

test("refresh redraws only when the source list or write permission changes", () => {
  const t = setup({ raw: "https://example.invalid/a" });
  const settled = t.root.children[1];
  t.section.refresh();
  assert.equal(t.root.children[1], settled, "an unrelated config push leaves the add field alone");
  t.state.raw = "https://example.invalid/a https://example.invalid/b";
  t.section.refresh();
  assert.equal(cls(t.root, "ws-companion-sources-source").length, 3);
  t.state.write = false;
  t.section.refresh();
  assert.ok(tagged(t.root, "BUTTON").every(b => b.disabled));
});

test("duplicate-ID rows in the initial preview: one id is sent and the selected valid row's own result is reported", async () => {
  const bad = { error: { id: "a", path: "pack.json", message: "duplicate row is invalid" } };
  const wrote = { error: { id: "a", path: "pack.json", message: "valid row cannot be moved into place" } };
  for (const failure of [false, true]) {
    const initial = () => listing({ kind: "local", packs: [pack("a", "available", { ...bad }), pack("a", "available")] });
    const result = () => listing({ kind: "local", packs: [pack("a", "available", { ...bad }),
      failure ? pack("a", "available", { ...wrote }) : pack("a", "installed", { installed: true })] });
    const t = setup({
      raw: "https://example.invalid/packs.git",
      handler: (url) => {
        if (url.endsWith("/sources/fetch")) return { status: 200, body: { listing: initial() } };
        if (url.endsWith("/sources/install")) return { status: 200, body: { listing: result() } };
        if (url.startsWith("/api/companion/packs/validate")) return { status: 200, body: { listing: initial() } };
        return { status: 200, body: { listing: result() } };
      }
    });
    const label = failure ? "failure" : "success";
    const shown = () => t.root.text();
    const alertText = () => t.root.all().find(c => c.attrs.role === "alert")?.text();
    fire(buttonOf(cls(t.root, "ws-companion-sources-source")[1], "js.companion.sources.show"), "click");
    await tick(); await tick();
    fire(buttonOf(t.root, "js.companion.sources.install"), "click");
    await tick(); await tick();
    if (failure) {
      assert.equal(alertText(), "valid row cannot be moved into place", "install " + label + ": the alert is the valid row's error");
    } else assert.match(shown(), /done_install/, "install " + label);

    const input = tagged(t.root, "INPUT").find(c => c.type === "file");
    input.files = [{ name: "Fan.zip", size: 3 }];
    fire(input, "change");
    await tick(); await tick();
    fire(buttonOf(t.root, "js.companion.sources.import"), "click");
    await tick(); await tick();
    assert.equal(t.calls.at(-1).url, "/api/companion/packs/import?name=Fan.zip&id=a", "import sends the ID once");
    if (failure) assert.equal(alertText(), "valid row cannot be moved into place", "import " + label);
    else assert.match(shown(), /done_import:1/, "import " + label + " counts one");
    assert.ok(!/done_import:2/.test(shown()));
  }
});

test("local .vrm file picker and drag-and-drop call import-vrm and select the new pack", async () => {
  const picked = { name: "My Avatar.vrm", size: 128 };
  const dropped = { name: "Second.vrm", size: 256 };
  const t = setup({
    handler: (url) => {
      if (url.startsWith("/api/companion/packs/import-vrm")) {
        const isSecond = url.includes("Second");
        return { status: 200, body: { id: isSecond ? "second" : "my-avatar", name: isSecond ? "Second" : "My Avatar", schema: "ws-companion-vrm/v1", renderer: "vrm", rev: "abc" } };
      }
      return { status: 404, body: {} };
    }
  });
  assert.equal(t.calls.length, 0, "no network fetch on initial render");

  const fileInputs = tagged(t.root, "INPUT").filter(c => c.type === "file");
  assert.equal(fileInputs.length, 2, "zip input and vrm input are both present");
  assert.equal(fileInputs[0].accept, ".zip,application/zip");
  assert.equal(fileInputs[1].accept, ".vrm,model/gltf-binary");

  fileInputs[1].files = [picked];
  fire(fileInputs[1], "change");
  await tick(); await tick();
  assert.equal(t.calls.length, 1);
  assert.equal(t.calls[0].url, "/api/companion/packs/import-vrm?name=My+Avatar&filename=My+Avatar.vrm");
  assert.equal(t.calls[0].init.headers["Content-Type"], "model/gltf-binary");
  assert.equal(t.calls[0].init.body, picked);
  assert.deepEqual(t.selectedPacks, ["my-avatar"], "newly imported VRM pack is selected");
  assert.deepEqual(t.changed, [null], "catalog refresh is triggered");
  assert.match(t.root.text(), /js\.companion\.sources\.done_import_vrm:My Avatar/);

  // Drag-and-drop of a non-.vrm file is rejected locally without network calls.
  const dropzone = cls(t.root, "ws-companion-sources-dropzone")[0];
  fire(dropzone, "dragover", {});
  assert.equal(dropzone.attrs["data-drag"], "over");
  fire(dropzone, "drop", { dataTransfer: { files: [{ name: "notes.txt" }] } });
  await tick();
  assert.equal(t.calls.length, 1, "non-.vrm drop sends no request");
  assert.match(t.root.text(), /js\.companion\.sources\.vrm_ext_error/);

  // Drag-and-drop of a .vrm file uploads and selects it.
  fire(dropzone, "drop", { dataTransfer: { files: [dropped] } });
  await tick(); await tick();
  assert.equal(t.calls.length, 2);
  assert.equal(t.calls[1].url, "/api/companion/packs/import-vrm?name=Second&filename=Second.vrm");
  assert.equal(t.calls[1].init.body, dropped);
  assert.deepEqual(t.selectedPacks, ["my-avatar", "second"]);
});

test("official VRM CTA shows not-yet-published state when absent and install action when listed", async () => {
  // Case 1: official marketplace lists no format:"vrm" entry yet.
  const emptyOfficial = setup({
    handler: () => ({ status: 200, body: { listing: listing({ source: "official", kind: "zip", official: true, name: "Official", packs: [pack("default", "builtin")] }) } })
  });
  assert.equal(emptyOfficial.calls.length, 0, "opt-in CTA fetches nothing until clicked");
  fire(buttonOf(emptyOfficial.root, "js.companion.sources.official_vrm_button"), "click");
  await tick(); await tick();
  assert.deepEqual(emptyOfficial.calls[0].body, { source: "official" });
  assert.match(emptyOfficial.root.text(), /js\.companion\.sources\.badge_coming_soon/);
  assert.match(emptyOfficial.root.text(), /js\.companion\.sources\.official_vrm_unavailable/);
  assert.equal(emptyOfficial.root.all().find(c => c.attrs.role === "alert"), undefined, "not-yet-published is informative, not an error");

  // Case 2: official marketplace lists a format:"vrm" entry with vroidSourceUrl.
  const vrmEntry = pack("official-vrm", "available", {
    name: "Official Avatar", format: "vrm", vroidSourceUrl: "https://example.invalid/official-vrm"
  });
  const freshCatalog = { packs: [
    { id: "default", renderer: "2d", schema: "ws-companion/v1" },
    { id: "official-vrm", name: "Official Avatar", renderer: "vrm", schema: "ws-companion-vrm/v1",
      origin: { source: "official", kind: "zip", version: "1.0.0", installedAt: "t", official: true } }
  ] };
  const liveOfficial = setup({
    handler: (url) => {
      if (url.endsWith("/sources/fetch")) {
        return { status: 200, body: { listing: listing({ source: "official", kind: "zip", official: true, name: "Official", packs: [pack("default", "builtin"), vrmEntry] }) } };
      }
      return { status: 200, body: { listing: listing({ source: "official", kind: "zip", official: true, name: "Official", packs: [pack("default", "builtin"), { ...vrmEntry, status: "installed", installed: true }] }), catalog: freshCatalog } };
    }
  });
  fire(buttonOf(liveOfficial.root, "js.companion.sources.official_vrm_button"), "click");
  await tick(); await tick();
  const downloadBtn = buttonOf(liveOfficial.root, "js.companion.sources.official_vrm_install:Official Avatar");
  assert.ok(downloadBtn, "CTA switches to install action when an official VRM entry is listed");
  // Listing card renders VRM badge and safe https vroidSourceUrl link.
  const panel = cls(liveOfficial.root, "ws-companion-sources-panel")[0];
  assert.ok(cls(panel, "ws-companion-vrm-badge").some(b => b.textContent === "js.companion.sources.badge_vrm"), "listing card shows VRM badge");
  assert.ok(tagged(panel, "A").some(a => a.href === "https://example.invalid/official-vrm" && a.rel === "noopener noreferrer"), "vroidSourceUrl renders as safe https link");

  fire(downloadBtn, "click");
  await tick(); await tick();
  assert.deepEqual(liveOfficial.calls.at(-1).body, { source: "official", ids: ["official-vrm"] });
  assert.deepEqual(liveOfficial.selectedPacks, ["official-vrm"], "installing official VRM selects it");
  assert.equal(cls(liveOfficial.root, "ws-companion-sources-vrm-cta").length, 0, "CTA hides once a VRM pack is installed");
  assert.ok(cls(liveOfficial.root, "ws-companion-vrm-badge").length >= 1, "installed VRM card shows VRM badge");
});

test("companion disabled runs zero WebGL contexts and zero extra fetches", () => {
  let webglCalls = 0;
  let fetches = 0;
  const listeners = {};
  const stageDoc = {
    documentElement: { hasAttribute: () => true },
    body: { append() {}, querySelector: () => null },
    createElement(tag) {
      return {
        tagName: tag.toUpperCase(), className: "", style: {}, dataset: {},
        getContext(kind) { if (String(kind).includes("webgl")) webglCalls++; return null; },
        setAttribute() {}, addEventListener() {}, append() {}, remove() {},
        querySelector: () => null, querySelectorAll: () => []
      };
    },
    getElementById: () => null,
    querySelector: () => null,
    addEventListener(type, fn) { (listeners[type] ||= []).push(fn); }
  };
  const win = {
    innerWidth: 1200, innerHeight: 800, devicePixelRatio: 1,
    matchMedia: () => ({ matches: false, addEventListener() {} }),
    addEventListener() {}, removeEventListener() {},
    wsCompanionModalRows: []
  };
  const context = vm.createContext({
    window: win, document: stageDoc, URLSearchParams, AbortController,
    ResizeObserver: class { observe() {} disconnect() {} },
    requestAnimationFrame: () => 0,
    cancelAnimationFrame() {},
    fetch: async () => { fetches++; return { ok: true, json: async () => ({}) }; },
    wsT: k => k
  });
  for (const name of ["packs", "sources", "settings"]) {
    vm.runInContext(fs.readFileSync(path.join(__dirname, "assets", `dashboard-companion-${name}.js`), "utf8"), context);
  }
  const C = context.window.WsCompanion;
  const node = stageDoc.createElement("div"), gear = stageDoc.createElement("button");
  // Construct modal with companion_enabled=false (never opened) and verify zero fetches and zero WebGL calls.
  C.settingsModal(node, gear, { signal: new AbortController().signal }, {
    rows: () => [], config: () => ({ enabled: false, pack: "default" }),
    persona: () => null, presets: () => ({ available: [] }),
    anchor: () => ({ left: 0, right: 0, top: 0, bottom: 0 }),
    resetSize() {}, applyPreset() {}, notice() {}, packsChanged() {}
  });
  assert.equal(fetches, 0, "zero fetches when companion_enabled is false");
  assert.equal(webglCalls, 0, "zero WebGL contexts created");
});
