"use strict";

// Real-layout check of the live call-details graft. A reader scrolled away from
// the bottom has a tool-call preview open in a running turn above the viewport;
// turn-rev updates must not move the scroll geometry, and the preview's height
// must never dip. Disposable page, no network. GRAFT_OFF=1 removes the fix from
// the loaded source as a negative control (the run must then fail).
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);

const assets = path.join(__dirname, "..", "assets");
let conv = fs.readFileSync(path.join(assets, "dashboard-conversation.js"), "utf8");
const audit = fs.readFileSync(path.join(assets, "dashboard-audit-rows.js"), "utf8");
function between(src, a, b) {
  const s = src.indexOf(a), e = src.indexOf(b, s);
  assert.ok(s >= 0 && e > s, "markers: " + a);
  return src.slice(s, e);
}
if (process.env.GRAFT_OFF) {
  conv = conv.replace(/graftLiveCalls\((?:old|null|el)[^)]*\)(?!\s*\{)/g, "({ grafted: 0, anyOpen: false })")
    .replace("newTurn.parentNode.replaceChild(oldTurn, newTurn);", "void 0;");
}
const source =
  between(audit, "    function auDirectItems(el) {", "\n    function auRenderedOwners(") + "\n" +
  between(audit, "    function auInlineJSON(url) {", "\n    doc.addEventListener(\"ws-approval-rows-changed\"") + "\n" +
  between(conv, "  function entryKeys(box) {", "\n  // gapped reports") +
  "\n;return { mergeEntries: mergeEntries, swapConvLive: swapConvLive };";

(async () => {
  const browser = await chromium.launch();
  try {
    const page = await (await browser.newContext({ viewport: { width: 900, height: 500 } })).newPage();
    const errors = [];
    page.on("pageerror", (e) => errors.push(e.message));
    await page.setContent(`<!doctype html><style>
      body{margin:0} #convo{height:400px;overflow:auto;overflow-anchor:none}
      .r-entries,.r-convlive{display:contents}
      .filler{height:60px} pre{margin:0;white-space:pre-wrap} details{display:block}
    </style><div id="convo"><div id="r-entries" class="r-entries"><div class="centries"></div></div><div id="r-convlive" class="r-convlive"></div></div>`);
    const result = await page.evaluate(async ({ source }) => {
      const convo = document.getElementById("convo"), box = convo.querySelector(".centries"), convlive = document.getElementById("r-convlive");
      const fetches = [];
      window.fetch = (url) => {
        fetches.push(url);
        const call = /call=([^&]+)/.exec(url), part = /\/event\/([^?]+)\?part=(in|out)/.exec(url);
        const data = call ? { call: { id: "ev-" + call[1], in_bytes: 5 }, events: [{ kind: "tool_result", call_id: call[1], id: "r-" + call[1], out_bytes: 9 }] }
          : { data: (part[2] === "in" ? "INPUT\n" : "OUTPUT\n").repeat(12) + part[1], end: true };
        return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(data) });
      };
      const openState = {};
      document.addEventListener("toggle", (e) => { if (e.target.dataset.key) openState[e.target.dataset.key] = e.target.open; }, true);
      const restoreOpen = (el) => {
        const all = [el].concat(Array.from(el.querySelectorAll("details")));
        all.forEach((d) => { if (d.tagName === "DETAILS" && d.dataset.key in openState) d.open = openState[d.dataset.key]; });
      };
      const api = new Function("doc", "auRowsRid", "stick", "pinConvo", "wsT", "auIsResultSummary", "restoreOpen", "fmtTimes", "region", "frag", "openState", "openKey", "syncTurnAria", source)(
        document, "sess", false, () => {}, (k) => k, (ev, id) => ev.call_id === id && ev.kind === "tool_result", restoreOpen, () => {},
        (n) => n === "convlive" ? document.getElementById("r-convlive") : n === "entries" ? document.getElementById("r-entries") : null,
        (h) => { const t = document.createElement("template"); t.innerHTML = h; return t.content; },
        openState, (d) => d.hasAttribute("data-key") ? "k:" + d.getAttribute("data-key") : null,
        (d) => { const s = d.firstElementChild; if (s && s.hasAttribute("data-conv-turn-summary")) s.setAttribute("aria-expanded", d.open ? "true" : "false"); });

      const filler = (i) => `<div class="filler" data-entry="${String(i).padStart(3, "0")}">row ${i}</div>`;
      const li = (id, dur) => `<li><details class="au-call-details" data-key="call-${id}"><summary class="conv-row au-row au-done" data-call="${id}"><span class="row-icon au-mk">ok</span><span class="row-content trace-content"><span class="au-tool">Bash</span></span><span class="row-time au-right"><span class="au-att" hidden></span><span class="au-dur">${dur}</span><span class="au-tok">+1k</span></span></summary><pre class="au-call-input">ls</pre><div class="conv-audit-items"></div></details></li>`;
      const turn = (rev, calls) => `<details class="toolsline d-turn" data-conv-turn="1" data-entry="010" data-key="turn-live" data-turn-rev="${rev}"><summary class="conv-row d-turn-sum" data-conv-turn-summary>${rev}</summary><div class="d-turn-drawer"><ol class="au-calls">${calls.join("")}</ol></div></details>`;
      const src = (html) => { const d = document.createElement("div"); d.innerHTML = html; return d; };
      const frames = () => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
      const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

      let rows = "";
      for (let i = 1; i <= 9; i++) rows += filler(i);
      rows += turn("r0", [li("c1", "1s")]);
      for (let i = 11; i <= 60; i++) rows += filler(i);
      api.mergeEntries(box, src(rows));
      const turnEl = () => box.querySelector('[data-entry="010"]');
      turnEl().open = true; // the reader opened the turn drawer
      const details = () => box.querySelector('details[data-key="call-c1"]');
      const original = details();
      original.open = true;
      await sleep(100); // toggle, then the preview fetches
      await frames();
      if (!original.querySelector(".au-call-output")) throw new Error("preview did not load");
      const sys = document.createElement("div");
      sys.setAttribute("data-au-row-key", "event:e1");
      original.querySelector(".conv-audit-items").appendChild(sys);

      convo.scrollTop = 1500; // reading below the turn, away from the bottom
      await frames();
      const probe = box.querySelector('[data-entry="040"]');
      const geometry = () => [convo.scrollHeight, convo.scrollTop, Math.round(probe.getBoundingClientRect().top), Math.round(original.getBoundingClientRect().height)];
      const base = geometry(), samples = [], identity = [];
      const fetchesBefore = fetches.length;
      for (let i = 1; i <= 8; i++) {
        api.mergeEntries(box, src(turn("r" + i, [li("c1", i + "s")])));
        samples.push(geometry()); // same task as the merge: nothing laid out in between hides a dip
        await frames();
        samples.push(geometry());
        identity.push(details() === original && original.open && original.querySelector(".conv-audit-items").firstChild === sys && original.hasAttribute("data-au-preview"));
      }
      const beforeSibling = geometry();
      api.mergeEntries(box, src(turn("r9", [li("c1", "9s"), li("c2", "")])));
      await frames();
      const afterSibling = geometry();
      await sleep(100);
      const fetchDelta = fetches.length - fetchesBefore;

      // Direct #r-convlive streaming + r-convlive -> r-entries live-to-completed handoff.
      const liveHtml = (rev, calls) => `<div class="conv clive"><details class="toolsline d-turn live d-turn-live" data-conv-turn="1" data-key="turn-live" data-turn-rev="${rev}"><summary class="conv-row d-turn-sum" data-conv-turn-summary>${rev}</summary><div class="d-turn-drawer"><ol class="au-calls">${calls.join("")}</ol></div></details><div class="filler" style="height:600px">streaming tail</div></div>`;
      api.mergeEntries(box, src(`<span hidden data-entry="061" data-turn-live-absorbed="1"></span>`));
      api.swapConvLive(convlive, liveHtml("lr0", [li("clive1", "1s")]));
      const liveTurnEl = () => convlive.querySelector("details.d-turn-live");
      liveTurnEl().open = true;
      const liveCallEl = () => convlive.querySelector('details[data-key="call-clive1"]');
      const liveOrig = liveCallEl();
      liveOrig.open = true;
      await sleep(100);
      await frames();
      if (!liveOrig.querySelector(".au-call-output")) throw new Error("convlive preview did not load");
      convo.scrollTop = Math.max(0, convo.scrollHeight - 450);
      await frames();
      const liveGeo = () => [convo.scrollHeight, convo.scrollTop, Math.round(liveOrig.getBoundingClientRect().height)];
      const liveBase = liveGeo(), liveSamples = [], liveIdentity = [];
      const liveFetchesBefore = fetches.length;
      for (let i = 1; i <= 6; i++) {
        api.swapConvLive(convlive, liveHtml("lr" + i, [li("clive1", i + "s")]));
        liveSamples.push(liveGeo());
        await frames();
        liveSamples.push(liveGeo());
        liveIdentity.push(liveCallEl() === liveOrig && liveOrig.open && liveTurnEl().open);
      }
      // Live -> completed handoff into #r-entries.
      const doneTurnHtml = `<details class="toolsline d-turn" data-conv-turn="1" data-entry="061" data-key="turn-061" data-turn-rev="done"><summary class="conv-row d-turn-sum" data-conv-turn-summary>done</summary><div class="d-turn-drawer"><ol class="au-calls">${li("clive1", "7s")}</ol></div><div class="conv-audit-items"></div></details>`;
      api.mergeEntries(box, src(doneTurnHtml));
      api.swapConvLive(convlive, `<div class="conv clive"></div>`);
      await frames();
      const handedTurn = box.querySelector('details[data-entry="061"]');
      const handedCall = handedTurn && handedTurn.querySelector('details[data-key="call-clive1"]');
      return {
        base, samples, identity, beforeSibling, afterSibling,
        detailsHeight: [beforeSibling[3], afterSibling[3]],
        fetchDelta,
        callFetches: fetches.filter((u) => u.includes("/events?call=c1")).length,
        sameNode: details() === original, dur: original.querySelector(".au-dur").textContent,
        liveBase, liveSamples, liveIdentity,
        liveCallFetches: fetches.filter((u) => u.includes("/events?call=clive1")).length,
        liveFetchDelta: fetches.length - liveFetchesBefore,
        handoffSameNode: handedCall === liveOrig,
        handoffTurnOpen: !!(handedTurn && handedTurn.open),
        handoffCallOpen: !!(handedCall && handedCall.open),
        handoffHeight: handedCall ? Math.round(handedCall.getBoundingClientRect().height) : -1,
      };
    }, { source });

    assert.deepEqual(errors, [], "page errors");
    for (const s of result.samples) assert.deepEqual(s, result.base, "scrollHeight/scrollTop/probe/details height constant across updates");
    assert.ok(result.identity.every(Boolean), "node, open state, audit row and preview survive: " + JSON.stringify(result.identity));
    assert.equal(result.afterSibling[1], result.beforeSibling[1], "scrollTop untouched by the merge");
    assert.equal(result.detailsHeight[0], result.detailsHeight[1], "open preview height unchanged by a new sibling call");
    assert.equal(result.sameNode, true);
    assert.equal(result.dur, "9s", "summary follows the fresh markup");
    assert.equal(result.fetchDelta, 0, "no refetch after the first preview load");
    assert.equal(result.callFetches, 1, "/audit/events?call= fetched once");
    for (const s of result.liveSamples) assert.deepEqual(s, result.liveBase, "convlive streaming keeps scrollHeight/scrollTop/details height constant");
    assert.ok(result.liveIdentity.every(Boolean), "convlive node and open state survive streaming ticks");
    assert.equal(result.liveFetchDelta, 0, "convlive streaming performs no refetch");
    assert.equal(result.liveCallFetches, 1, "convlive call fetched once");
    assert.equal(result.handoffSameNode, true, "open call node grafted from r-convlive to r-entries");
    assert.equal(result.handoffTurnOpen, true, "outer turn drawer stays open across handoff");
    assert.equal(result.handoffCallOpen, true, "open call details stays open across handoff");
    assert.equal(result.handoffHeight, result.liveBase[2], "call preview height unchanged across handoff");
    console.log("ok " + JSON.stringify({ base: result.base, samples: result.samples.length, fetchDelta: result.fetchDelta, callFetches: result.callFetches, liveSamples: result.liveSamples.length }));
  } finally {
    await browser.close();
  }
})().catch((e) => { console.error(e); process.exitCode = 1; });
