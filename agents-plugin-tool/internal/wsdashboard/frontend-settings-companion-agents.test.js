const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

// Minimal DOM for the Companion Agents card (dashboard-settings-companion-agents.js).
function element(tag) {
  const el = {
    tagName: String(tag).toUpperCase(), className: "", textContent: "", title: "", value: "",
    disabled: false, children: [], dataset: {}, attrs: {}, listeners: {},
    setAttribute(k, v) { this.attrs[k] = String(v); },
    addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); },
    append(...items) { this.children.push(...items.filter(Boolean)); },
    add(opt, index) { index === undefined ? this.children.push(opt) : this.children.splice(index, 0, opt); },
    all() { return this.children.flatMap(c => typeof c === "string" ? [] : [c, ...(c.all ? c.all() : [])]); }
  };
  return el;
}
const tick = () => new Promise(resolve => setImmediate(resolve));
const byData = (root, prop, val) => root.all().filter(c => c.dataset && (val === undefined ? Object.hasOwn(c.dataset, prop) : c.dataset[prop] === val));
const catalog = JSON.parse(fs.readFileSync(path.join(__dirname, "assets", "dashboard-settings-catalog.json"), "utf8"));
const specs = new Map(catalog.map(s => [s.key, s]));
const BODY = "dashboard.companion_body_tier";
const WORK = "dashboard.companion_work_tier";

const readback = (overrides = {}) => ({
  body: { configured: "", harness: "claude", tier: "small", model: "claude-small", effort: "low", available: true },
  work: { configured: "", harness: "claude", tier: "medium", model: "claude-medium", effort: "", available: true },
  options: [
    { value: "auto", kind: "auto" },
    { value: "claude.small", kind: "harness", harness: "claude", tier: "small", model: "claude-small", effort: "low" },
    { value: "codex.medium", kind: "harness", harness: "codex", tier: "medium", model: "", effort: "" },
    { value: "brain", kind: "alias", harness: "codex", tier: "large", model: "codex-large", effort: "high", target: "codex.large" }
  ],
  ...overrides
});

function setup({ values = {}, layers = {}, scope = "global", editable = true, matches = () => true, respond } = {}) {
  const fetches = [], writes = [];
  let answer = respond || (() => ({ ok: true, status: 200, json: async () => ({ resolved_agents: readback() }) }));
  const win = {};
  const context = vm.createContext({
    window: win,
    fetch: async url => { fetches.push(String(url)); return answer(); },
    Option: class { constructor(text, value, _d, selected) { this.tagName = "OPTION"; this.textContent = text; this.value = value; this.selected = !!selected; } }
  });
  vm.runInContext(fs.readFileSync(path.join(__dirname, "assets", "dashboard-settings-companion-agents.js"), "utf8"), context);
  const mod = win.WsSettingsCompanionAgents;
  const el = (tag, cls, text) => { const n = element(tag); if (cls) n.className = cls; if (text !== undefined && text !== "") n.textContent = String(text); return n; };
  let root = null;
  const ctx = {
    scope, filter: "all", searchTerm: "",
    tr: (k, ...a) => a.length ? k + ":" + a.join("|") : k,
    el, append: (p, ...kids) => { p.append(...kids); return p; },
    byKey: new Map([BODY, WORK].map(key => [key, { key, value: values[key] ?? "", default: "", layers: layers[key] || [] }])),
    specFor: k => specs.get(k),
    matches,
    editable: () => editable,
    selected: e => e.value,
    renderKeyProvenance: () => ({ node: el("span", "prov"), drawer: null }),
    submitConfig: async (entry, action, value) => { writes.push({ key: entry.key, action, value }); },
    rerender: () => { root = mod.render(ctx); }
  };
  ctx.rerender();
  return { mod, ctx, fetches, writes, get root() { return root; }, setAnswer(fn) { answer = fn; } };
}

const selectFor = (root, role) => byData(root, "companionAgentRole", role)[0].all().find(c => c.tagName === "SELECT");
const pillFor = (root, role) => byData(root, "companionAgentReadback", role)[0];

test("companion agents card lists the readback options and shows each role's launch", async () => {
  const h = setup();
  assert.equal(h.root.dataset.companionAgents, "1");
  assert.equal(pillFor(h.root, "body").textContent, "js.settings.companion.agents.loading");
  await tick();
  assert.deepEqual(h.fetches, ["/api/companion/agents"]);
  const body = selectFor(h.root, "body");
  assert.deepEqual(body.children.map(o => o.value), ["", "claude.small", "codex.medium", "brain"]);
  assert.equal(body.children[0].textContent, "js.settings.companion.agents.auto");
  assert.ok(body.children[0].selected, "an unset value selects auto");
  assert.equal(body.children[1].textContent, "claude.small — claude-small · low");
  assert.equal(body.children[2].textContent, "codex.medium — js.settings.companion.agents.default_model");
  assert.equal(body.children[3].textContent, "brain → codex.large — codex-large · high");
  assert.equal(body.attrs["aria-label"], specs.get(BODY).label);
  const bodyPill = pillFor(h.root, "body");
  assert.equal(bodyPill.textContent, "claude · claude-small · low");
  assert.match(bodyPill.className, /\bok\b/);
  assert.equal(pillFor(h.root, "work").textContent, "claude · claude-medium");
  assert.equal(byData(h.root, "companionAgentFallback").length, 0);
  h.ctx.rerender();
  await tick();
  assert.equal(h.fetches.length, 1, "a rerender reuses the loaded readback");
});

test("an unlisted value stays selected as custom and the fallback reason is shown", async () => {
  const data = readback({
    body: { configured: "agy.small", harness: "claude", tier: "small", model: "claude-small", available: true, fallback_reason: "\"agy.small\": not registered; using auto" },
    work: { configured: "", available: false, fallback_reason: "no harness can host a companion session now" }
  });
  const h = setup({ values: { [BODY]: "Agy.Small", [WORK]: "brain" }, respond: () => ({ ok: true, status: 200, json: async () => ({ resolved_agents: data }) }) });
  await tick();
  const body = selectFor(h.root, "body");
  assert.equal(body.children[1].value, "agy.small");
  assert.equal(body.children[1].textContent, "js.settings.companion.agents.custom:agy.small");
  assert.ok(body.children[1].selected);
  assert.match(pillFor(h.root, "body").className, /\bwarn\b/);
  const reason = byData(h.root, "companionAgentFallback", "body")[0];
  assert.equal(reason.textContent, "js.settings.companion.agents.fallback:\"agy.small\": not registered; using auto");
  const work = selectFor(h.root, "work");
  assert.ok(work.children.find(o => o.value === "brain").selected, "a listed alias is selected as itself");
  assert.equal(pillFor(h.root, "work").textContent, "js.settings.companion.agents.unavailable");
  assert.match(pillFor(h.root, "work").className, /\bfail\b/);
});

test("choosing a value saves it at once and refreshes the readback", async () => {
  const h = setup();
  await tick();
  const work = selectFor(h.root, "work");
  work.value = "codex.medium";
  work.listeners.change[0]();
  await tick();
  await tick();
  assert.deepEqual(h.writes, [{ key: WORK, action: "set", value: "codex.medium" }]);
  assert.equal(h.fetches.length, 2, "the readback reloads after the save");
  const body = selectFor(h.root, "body");
  body.value = "";
  body.listeners.change[0]();
  await tick();
  assert.deepEqual(h.writes[1], { key: BODY, action: "reset", value: "" }, "auto at global scope resets");
});

test("auto under a project scope pins auto only over an explicit global value", async () => {
  const globalLayer = [{ scope: "global", present: true, value: "codex.medium" }];
  const h = setup({ scope: "project", layers: { [BODY]: globalLayer, [WORK]: [{ scope: "global", present: true, value: "auto" }] } });
  await tick();
  for (const role of ["body", "work"]) {
    const select = selectFor(h.root, role);
    select.value = "";
    select.listeners.change[0]();
    await tick();
  }
  assert.deepEqual(h.writes, [
    { key: BODY, action: "set", value: "auto" },
    { key: WORK, action: "reset", value: "" }
  ]);
});

test("a failed readback says so, and a read-only scope disables the selectors", async () => {
  const h = setup({ editable: false, respond: () => ({ ok: false, status: 404, json: async () => ({}) }) });
  await tick();
  const pill = pillFor(h.root, "body");
  assert.equal(pill.textContent, "js.settings.companion.agents.load_failed");
  assert.match(pill.className, /\bwarn\b/);
  const select = selectFor(h.root, "body");
  assert.ok(select.disabled);
  assert.deepEqual(select.children.map(o => o.value), [""], "auto stays offered without a readback");
  select.listeners.change[0]();
  await tick();
  assert.deepEqual(h.writes, []);
  h.setAnswer(() => ({ ok: true, status: 200, json: async () => ({ resolved_agents: readback() }) }));
  h.mod.refresh(h.ctx.rerender);
  await tick();
  assert.equal(pillFor(h.root, "body").textContent, "claude · claude-small · low");
});

test("a refresh keeps the last readback on screen and drops stale answers", async () => {
  const pending = [];
  const h = setup({ respond: () => new Promise(resolve => pending.push(resolve)) });
  const answer = (i, data) => pending[i]({ ok: true, status: 200, json: async () => ({ resolved_agents: data }) });
  answer(0, readback());
  await tick();
  h.mod.refresh(h.ctx.rerender);
  h.ctx.rerender();
  assert.equal(pillFor(h.root, "body").textContent, "claude · claude-small · low", "the old readback stays while reloading");
  assert.equal(selectFor(h.root, "body").children.length, 4, "the options stay while reloading");
  h.mod.refresh(h.ctx.rerender);
  answer(2, readback({ body: { harness: "codex", model: "codex-small", available: true } }));
  await tick();
  answer(1, readback({ body: { harness: "stale", available: true } }));
  await tick();
  assert.equal(pillFor(h.root, "body").textContent, "codex · codex-small", "a superseded answer is ignored");
  h.mod.resetState();
  h.ctx.rerender();
  h.mod.resetState();
  answer(3, readback({ body: { harness: "stale", available: true } }));
  await tick();
  h.ctx.rerender();
  assert.equal(pillFor(h.root, "body").textContent, "js.settings.companion.agents.loading", "a reset drops the in-flight answer");
});

test("a rejected save still refreshes the readback", async () => {
  const h = setup();
  await tick();
  h.ctx.submitConfig = async () => { throw new Error("denied"); };
  const select = selectFor(h.root, "body");
  select.value = "claude.small";
  select.listeners.change[0]();
  await tick();
  await tick();
  assert.equal(h.fetches.length, 2);
});

test("the agy consumer-terms notice shows once while either role launches on agy", async () => {
  const agyOptions = readback().options.concat([
    { value: "agy.small", kind: "harness", harness: "agy", tier: "small", model: "agy-small", effort: "" },
    { value: "far", kind: "alias", harness: "agy", tier: "medium", model: "agy-medium", effort: "", target: "agy.medium" }
  ]);
  const respond = () => ({ ok: true, status: 200, json: async () => ({ resolved_agents: readback({ options: agyOptions }) }) });
  const notices = h => byData(h.root, "companionAgentTerms");
  const none = setup({ values: { [BODY]: "claude.small", [WORK]: "brain" }, respond });
  await tick();
  assert.equal(notices(none).length, 0, "no notice without agy");
  assert.equal(notices(setup({ respond })).length, 0, "auto never shows it");

  for (const values of [{ [BODY]: "agy.small" }, { [WORK]: "Far" }, { [BODY]: "agy.small", [WORK]: "agy.small" }]) {
    const h = setup({ values, respond });
    await tick();
    const found = notices(h);
    assert.equal(found.length, 1, JSON.stringify(values));
    assert.equal(found[0].textContent, "js.lead.agy.terms", "the lead modal's own key");
    assert.equal(found[0].attrs.role, "note");
    assert.equal(h.root.children[h.root.children.length - 1], found[0], "it closes the card");
  }

  // Before the readback loads, a dotted value is read by its prefix; an
  // alias cannot be resolved yet.
  const pending = setup({ values: { [BODY]: "agy.small", [WORK]: "far" }, respond: () => new Promise(() => {}) });
  assert.equal(notices(pending).length, 1);
  assert.equal(notices(setup({ values: { [WORK]: "far" }, respond: () => new Promise(() => {}) })).length, 0);

  // A project scope shows its own value, not the effective one.
  const project = setup({ scope: "project", values: { [BODY]: "agy.small" }, respond });
  project.ctx.selected = () => "claude.small";
  await tick();
  project.ctx.rerender();
  assert.equal(notices(project).length, 0);
  project.ctx.selected = () => "agy.small";
  project.ctx.rerender();
  assert.equal(notices(project).length, 1);

  // Choosing agy and then leaving it moves the notice with the saved value.
  const live = setup({ respond });
  await tick();
  live.ctx.byKey.get(WORK).value = "agy.small";
  live.ctx.rerender();
  assert.equal(notices(live).length, 1);
  live.ctx.byKey.get(WORK).value = "";
  live.ctx.rerender();
  assert.equal(notices(live).length, 0);
});

test("the card hides with its rows and never fetches while hidden", async () => {
  const h = setup({ matches: entry => entry.key === WORK });
  assert.equal(byData(h.root, "companionAgentRole").length, 1);
  const hidden = setup({ matches: () => false });
  assert.equal(hidden.root, null);
  await tick();
  assert.deepEqual(hidden.fetches, []);
  assert.deepEqual([...hidden.mod.keys], [BODY, WORK]);
});

test("the card is wired into the Companion core group and the settings bundle", () => {
  const companion = fs.readFileSync(path.join(__dirname, "assets", "dashboard-settings-companion.js"), "utf8");
  assert.match(companion, /WsSettingsCompanionAgents\.render\(ctx\)/);
  assert.match(companion, /WsSettingsCompanionAgents\.resetState\(\)/);
  const pages = fs.readFileSync(path.join(__dirname, "pages.go"), "utf8");
  const agentsAt = pages.indexOf("\"dashboard-settings-companion-agents.js\"");
  assert.ok(agentsAt > 0 && agentsAt < pages.indexOf("\"dashboard-settings-companion.js\""), "the agents module loads before the companion panel");
  for (const key of [BODY, WORK]) {
    assert.equal(specs.get(key).section, "companion");
  }
});
