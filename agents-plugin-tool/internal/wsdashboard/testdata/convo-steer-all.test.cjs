"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const assetsDir = path.join(__dirname, "..", "assets");
const statusSrc = fs.readFileSync(path.join(assetsDir, "dashboard-composer-status.js"), "utf8");
const inputSrc = fs.readFileSync(path.join(assetsDir, "dashboard-composer-input.js"), "utf8");
const actionsSrc = fs.readFileSync(path.join(assetsDir, "dashboard-actions.js"), "utf8");
const convoCss = fs.readFileSync(path.join(assetsDir, "dashboard-conversation.css"), "utf8");
const tmplSrc = fs.readFileSync(path.join(assetsDir, "dashboard.tmpl"), "utf8");

function extractSteerAllSection(src) {
  const start = src.indexOf("  function steerHeld(btn) {");
  const end = src.indexOf("\n  // applySent marks each board item", start);
  assert.ok(start >= 0 && end > start, "dashboard-composer-status.js steer section not found");
  return src.slice(start, end);
}

function createHarness(opts) {
  const options = opts || {};
  const posts = [];
  const okNotes = [];
  const warnNotes = [];
  let mutationCb = null;
  let heldNodes = [];
  let layoutReads = 0;

  const lbl = {
    textContent: "전부 즉시 보내기",
  };
  const steerAllBtn = {
    id: "convo-steer-all-btn",
    hidden: true,
    disabled: false,
    querySelector(sel) { return sel === ".d-convo-steer-all-lbl" ? lbl : null; },
    get offsetHeight() { layoutReads++; return 28; },
    getBoundingClientRect() { layoutReads++; return { width: 120, height: 28 }; },
  };
  const convlive = { id: "r-convlive" };

  const i18n = {
    "js.steer_all": "전부 즉시 보내기",
    "js.steer_all.count": "전부 즉시 보내기 ({0})",
    "js.steer_all.ok": "전부 전환됨: 세션이 다음 도구 호출 시점에 모두 받습니다.",
    "js.steer_all.fail": "전부 전환하지 못함: {0}",
  };
  function wsT(key) {
    const s = Object.prototype.hasOwnProperty.call(i18n, key) ? i18n[key] : key;
    const a = arguments;
    return s.replace(/\{(\d+)\}/g, function (m, i) {
      return Number(i) + 1 < a.length ? String(a[Number(i) + 1]) : m;
    });
  }

  function FakeMutationObserver(cb) {
    mutationCb = cb;
    return { observe() {} };
  }

  const sandbox = {
    liveMode: options.liveMode !== false,
    $: (id) => (id === "convo-steer-all-btn" ? steerAllBtn : id === "r-convlive" ? convlive : null),
    doc: {
      querySelectorAll(sel) {
        if (sel === "#r-convlive [data-steer]") return heldNodes.slice();
        return [];
      },
    },
    MutationObserver: FakeMutationObserver,
    wsT,
    post(route, body, done) {
      posts.push({ route, body, done });
    },
    noteOk(msg) { okNotes.push(msg); },
    noteWarn(msg) { warnNotes.push(msg); },
    Array,
    String,
  };
  sandbox.globalThis = sandbox;

  const section = extractSteerAllSection(statusSrc);
  const trailer = ";globalThis.__api = { syncSteerAll, steerAllHeld };";
  vm.runInNewContext(section + trailer, sandbox, { filename: "dashboard-steer-all.js" });

  return {
    api: sandbox.__api,
    btn: steerAllBtn,
    lbl,
    posts,
    okNotes,
    warnNotes,
    setHeldButtons(arr) { heldNodes = arr; },
    triggerMutation() { if (mutationCb) mutationCb([]); },
    get layoutReads() { return layoutReads; },
  };
}

test("syncSteerAll toggles visibility, count label, and ignores exec notices / already-steered", () => {
  const h = createHarness();

  // 0 un-steered user held messages (e.g. only exec-notice or already-steered .steertag).
  h.setHeldButtons([]);
  h.api.syncSteerAll();
  assert.equal(h.btn.hidden, true);
  assert.equal(h.btn.disabled, false);

  // 1 held message: shown without count suffix.
  const b1 = { disabled: false, getAttribute: () => "e1" };
  h.setHeldButtons([b1]);
  h.triggerMutation();
  assert.equal(h.btn.hidden, false);
  assert.equal(h.lbl.textContent, "전부 즉시 보내기");

  // 3 held messages (including optimistic bubbles inserted into #r-convlive): shows count.
  const b2 = { disabled: false, getAttribute: () => "e2" };
  const b3 = { disabled: false, getAttribute: () => "e3" };
  h.setHeldButtons([b1, b2, b3]);
  h.triggerMutation();
  assert.equal(h.btn.hidden, false);
  assert.equal(h.lbl.textContent, "전부 즉시 보내기 (3)");

  // Back to 0: hidden again.
  h.setHeldButtons([]);
  h.triggerMutation();
  assert.equal(h.btn.hidden, true);
  assert.equal(h.layoutReads, 0, "syncSteerAll must never force layout reads");
});

test("steerAllHeld disables buttons, posts steer-all, handles success, idempotent 0, and failure", () => {
  const h = createHarness();
  const b1 = { disabled: false };
  const b2 = { disabled: false };
  h.setHeldButtons([b1, b2]);
  h.api.syncSteerAll();

  // Click -> disables steer-all and all per-message [data-steer] buttons.
  h.api.steerAllHeld(h.btn);
  assert.equal(h.btn.disabled, true);
  assert.equal(b1.disabled, true);
  assert.equal(b2.disabled, true);
  assert.equal(h.posts.length, 1);
  assert.equal(h.posts[0].route, "steer-all");

  // Failure -> re-enables all buttons and warns.
  h.posts[0].done(false, "board-replaced");
  assert.equal(h.btn.disabled, false);
  assert.equal(b1.disabled, false);
  assert.equal(b2.disabled, false);
  assert.deepEqual(h.warnNotes, ["전부 전환하지 못함: board-replaced"]);

  // Success with count > 0 -> ok toast.
  h.api.steerAllHeld(h.btn);
  assert.equal(h.posts.length, 2);
  h.setHeldButtons([]);
  h.posts[1].done(true, { ok: true, count: 2 });
  assert.deepEqual(h.okNotes, ["전부 전환됨: 세션이 다음 도구 호출 시점에 모두 받습니다."]);
  assert.equal(h.btn.hidden, true);
  assert.equal(h.btn.disabled, false);

  // Success with count === 0 (idempotent race) -> no duplicate ok toast.
  h.setHeldButtons([b1]);
  h.api.syncSteerAll();
  h.api.steerAllHeld(h.btn);
  h.setHeldButtons([]);
  h.posts[2].done(true, { ok: true, count: 0 });
  assert.equal(h.okNotes.length, 1);
  assert.equal(h.btn.hidden, true);
});

test("CSS, template, input, and delegated action contracts for #convo-steer-all-btn", () => {
  assert.match(tmplSrc, /id="convo-bottom-btn"[\s\S]*?<button type="button" id="convo-steer-all-btn" class="d-convo-steer-all"[^>]*hidden>/);
  assert.match(convoCss, /\.dgrid\.live\s*>\s*\.d-convo-steer-all\s*\{[^}]*grid-area:\s*convo;[^}]*align-self:\s*end;[^}]*justify-self:\s*start;[^}]*margin:\s*0 0 18px 10px;/);
  assert.match(convoCss, /\.dgrid\.live\s*>\s*\.d-convo-steer-all\[hidden\]\s*\{\s*display:\s*none\s*!important;\s*\}/);
  assert.match(convoCss, /\.dgrid\.live\[data-tab="board"\]\s*>\s*\.d-convo-steer-all/);
  assert.match(convoCss, /@media\s*\(prefers-reduced-motion:\s*reduce\)\s*\{[^}]*\.d-convo-steer-all/);
  assert.match(inputSrc, /route === "steer" \|\| route === "steer-all"/);
  assert.match(actionsSrc, /t\.closest\("#convo-steer-all-btn"\)[\s\S]*steerAllHeld\(u\)/);
});
