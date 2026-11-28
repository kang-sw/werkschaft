"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

const ASSETS = path.join(__dirname, "assets");
const readAsset = (name) => fs.readFileSync(path.join(ASSETS, name), "utf8");

function makeRoot(initialAttrs = {}) {
  const attrs = new Map(Object.entries(initialAttrs));
  return {
    hasAttribute(k) { return attrs.has(k); },
    getAttribute(k) { return attrs.has(k) ? attrs.get(k) : null; },
    setAttribute(k, v) { attrs.set(k, String(v)); },
    removeAttribute(k) { attrs.delete(k); },
    toggleAttribute(k, force) {
      const next = force === undefined ? !attrs.has(k) : Boolean(force);
      if (next) attrs.set(k, "");
      else attrs.delete(k);
      return next;
    }
  };
}

function makeButton() {
  const attrs = new Map();
  return {
    setAttribute(k, v) { attrs.set(k, String(v)); },
    getAttribute(k) { return attrs.has(k) ? attrs.get(k) : null; }
  };
}

function loadShellModule() {
  const ctx = { window: {} };
  vm.runInNewContext(readAsset("shell.js"), ctx);
  return ctx.window.WsShell;
}

function extractFunction(src, name) {
  const start = src.indexOf(`function ${name}(`);
  assert.notEqual(start, -1, `missing function ${name}`);
  let depth = 0;
  let inBrace = false;
  for (let i = start; i < src.length; i++) {
    const ch = src[i];
    if (ch === "{") { depth++; inBrace = true; }
    else if (ch === "}") {
      depth--;
      if (inBrace && depth === 0) return src.slice(start, i + 1);
    }
  }
  throw new Error(`unterminated function ${name}`);
}

test("stageFill: CSS enforces Band 3 visibility, no :has(), and preserves --stagew", () => {
  const boardCSS = readAsset("dashboard-board.css");
  const railCSS = readAsset("dashboard-rail.css");

  assert.match(boardCSS, /\.board-acts \.board-stage-fill\s*\{\s*display:\s*none;\s*\}/);
  assert.match(boardCSS, /\.board-acts \.board-action\[aria-pressed="true"\]\s*\{/);

  const band3Block = railCSS.slice(
    railCSS.indexOf("@media (min-width: 1600px)"),
    railCSS.indexOf("@media (max-width: 1599.98px)")
  );
  assert.match(
    band3Block,
    /:root\[data-stage-fill\]:not\(\[data-mode="index"\]\):not\(\[data-pincol\]\):not\(\[data-gal="empty"\]\):not\(\[data-gal="drag"\]\):not\(\[data-overview-preview\]\) \.dbody > \.dgrid\s*\{\s*max-width:\s*none;\s*\}/
  );
  assert.match(
    band3Block,
    /:root\[data-stage-fill\]:not\(\[data-shell\]\):not\(\[data-pincol\]\):not\(\[data-gal="empty"\]\):not\(\[data-gal="drag"\]\):not\(\[data-overview-preview\]\) \.dash:not\(\.index\):not\(\.tk-paged\) > \.dwrap\s*\{\s*--stage-side:\s*0px;\s*\}/
  );
  assert.match(
    band3Block,
    /:root:not\(\[data-pincol\]\):not\(\[data-gal="empty"\]\):not\(\[data-gal="drag"\]\):not\(\[data-overview-preview\]\) \.board-acts \.board-stage-fill\s*\{\s*display:\s*inline-flex;\s*\}/
  );

  for (const line of `${boardCSS}\n${railCSS}`.split("\n")) {
    if (line.includes("stage-fill") || line.includes("stageFill")) {
      assert.equal(line.includes(":has("), false, `unexpected :has() in ${line}`);
    }
  }
});

test("stageFill: WsShell.message keeps T0 compatibility for settings with or without stageFill", () => {
  const WsShell = loadShellModule();
  const legacy = WsShell.message({ wsShell: "settings", layout: "auto", input: "enter" });
  assert.deepEqual(legacy, { wsShell: "settings", layout: "auto", input: "enter" });

  const withFill = WsShell.message({ wsShell: "settings", layout: "split", input: "mod", stageFill: true });
  assert.deepEqual(withFill, { wsShell: "settings", layout: "split", input: "mod", stageFill: true });
});

test("stageFill: hydration, toggle persistence, storage sync, and frame/shell head.side updates", () => {
  const dashJS = readAsset("dashboard.js");
  const lifeJS = readAsset("dashboard-lifecycle.js");

  const parseFn = extractFunction(dashJS, "parseStageFill");
  const setFn = extractFunction(dashJS, "setStageFill");
  const toggleFn = extractFunction(dashJS, "toggleStageFill");
  const shSettingsFn = extractFunction(lifeJS, "shSettings");
  const shHeadFn = extractFunction(lifeJS, "shHead");

  const store = new Map([["ws.dashboard.stageFill", "1"]]);
  const root = makeRoot();
  const btn = makeButton();
  const events = [];
  const posted = [];
  const sentToFrames = [];
  const topProps = new Map();
  let galleryQueued = 0;

  const shTop = {
    classList: { toggle() {} },
    style: {
      paddingRight: "",
      minHeight: "",
      setProperty(k, v) { topProps.set(k, v); }
    }
  };

  const sandbox = {
    STAGE_FILL_KEY: "ws.dashboard.stageFill",
    layoutMode: "auto",
    inputMode: "enter",
    extraMotion: false,
    framed: true,
    galNarrow: true,
    root,
    doc: {
      dispatchEvent(ev) { events.push(ev); }
    },
    CustomEvent: function CustomEvent(type, init) {
      this.type = type;
      this.detail = init && init.detail;
    },
    load(k) { return store.has(k) ? store.get(k) : null; },
    save(k, v) {
      if (v === null) store.delete(k);
      else store.set(k, v);
      return true;
    },
    q(sel) { return sel === ".board-stage-fill" ? [btn] : []; },
    queueGallery() { galleryQueued++; },
    shPost(kind, body) { posted.push({ kind, body }); },
    shSend(ent, kind, body) { sentToFrames.push({ ent, kind, body }); },
    shTop,
    shHeadHtml: "",
    shSeq: 0,
    $(id) { return id === "qsslot" ? { innerHTML: "" } : null; },
    swap() {},
    railRenameNew() {}
  };

  vm.runInNewContext(`
    ${parseFn}
    var stageFill = parseStageFill(load(STAGE_FILL_KEY));
    if (stageFill) root.setAttribute("data-stage-fill", "");
    ${setFn}
    ${toggleFn}
    ${shSettingsFn}
    ${shHeadFn}
  `, sandbox);

  // Hydrated from localStorage before mount.
  assert.equal(sandbox.stageFill, true);
  assert.equal(root.hasAttribute("data-stage-fill"), true);

  // User toggle off: clears storage, attribute, aria-pressed, dispatches ws-stage-fill, posts settings.
  sandbox.toggleStageFill();
  assert.equal(sandbox.stageFill, false);
  assert.equal(root.hasAttribute("data-stage-fill"), false);
  assert.equal(btn.getAttribute("aria-pressed"), "false");
  assert.equal(store.has("ws.dashboard.stageFill"), false);
  assert.equal(events.length, 1);
  assert.equal(events[0].type, "ws-stage-fill");
  assert.equal(galleryQueued, 1);
  assert.deepEqual(JSON.parse(JSON.stringify(posted[0])), {
    kind: "settings",
    body: { layout: "auto", input: "enter", stageFill: false, extraMotion: false }
  });

  // Shell mirrors head.side = 120 when stageFill is off.
  sandbox.shHead({ head: { html: "", qs: "", pad: 0, top: 44, side: 120 } });
  assert.equal(topProps.get("--sh-stage-side"), "120px");

  // Cross-tab storage / shell settings sync turns stageFill back on without rewriting storage.
  sandbox.setStageFill(true);
  assert.equal(sandbox.stageFill, true);
  assert.equal(root.hasAttribute("data-stage-fill"), true);
  assert.equal(btn.getAttribute("aria-pressed"), "true");
  assert.equal(store.has("ws.dashboard.stageFill"), false);
  assert.equal(events.length, 2);

  // Shell forwards stageFill in shSettings and clears --sh-stage-side when head.side becomes 0.
  const frameA = { id: "A" };
  sandbox.shSettings(frameA);
  assert.deepEqual(JSON.parse(JSON.stringify(sentToFrames[0])), {
    ent: frameA,
    kind: "settings",
    body: { layout: "auto", input: "enter", stageFill: true, extraMotion: false }
  });
  sandbox.shHead({ head: { html: "", qs: "", pad: 0, top: 44, side: 0 } });
  assert.equal(topProps.get("--sh-stage-side"), "");

  // Lifecycle wiring checks: ws-stage-fill triggers queueHead, and both shell & frame handle settings.stageFill.
  assert.match(lifeJS, /doc\.addEventListener\("ws-stage-fill", queueHead\);/);
  assert.match(lifeJS, /case "settings":[\s\S]*?setStageFill\(m\.stageFill\)[\s\S]*?shFrames\.forEach/);
  assert.match(lifeJS, /m\.wsShell === "settings"[\s\S]*?typeof m\.stageFill === "boolean"[\s\S]*?setStageFill\(m\.stageFill\)/);
  assert.match(dashJS, /e\.key === STAGE_FILL_KEY \|\| e\.key === null/);
});
