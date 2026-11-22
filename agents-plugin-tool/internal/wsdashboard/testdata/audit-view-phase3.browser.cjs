"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");

const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE || "playwright");
const BASE = process.env.AUDIT_BASE;
const SHOTS = process.env.AUDIT_P3_SHOTS || "";

async function runSearch(frame, query, opts = {}) {
  const rxBtn = frame.locator('[data-au-sopt="regex"]');
  const csBtn = frame.locator('[data-au-sopt="case"]');
  const curRx = (await rxBtn.getAttribute("aria-pressed")) === "true";
  if (!!opts.regex !== curRx) await rxBtn.click();
  const curCs = (await csBtn.getAttribute("aria-pressed")) === "true";
  if (!!opts.caseSensitive !== curCs) await csBtn.click();
  await frame.locator(".au-skind").selectOption(opts.kind || "");
  await frame.locator(".au-sq").fill(query);
  await frame.locator(".au-sbtn").click();
}

(async () => {
  if (!BASE) throw new Error("AUDIT_BASE required");
  const server = await chromium.launchServer({ headless: true });
  const browser = await chromium.connect(server.wsEndpoint());
  const errors = [];
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: "en-US", colorScheme: "dark" });
    context.setDefaultTimeout(8000);
    await context.route("**/*", (route) => {
      const origin = new URL(route.request().url()).origin;
      if (origin === BASE) return route.continue();
      return route.abort();
    });

    // Simulate a truncated envelope split at line 40 for p3-ev-trunc so client auto-paging runs.
    const headLines = Array.from({ length: 40 }, (_, i) => `padding line ${i + 1}`).join("\n") + "\n";
    const tailLines = Array.from({ length: 415 }, (_, idx) => {
      const no = idx + 41;
      return no === 450 ? "line 450 TRUNC_NEEDLE_450 reached" : `padding line ${no}`;
    }).join("\n") + "\n";
    await context.route("**/s/livesession/audit/event/p3-ev-trunc*", async (route) => {
      const reqUrl = new URL(route.request().url());
      if (reqUrl.searchParams.get("part") === "out") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ data: tailLines, next: headLines.length + tailLines.length, end: true })
        });
        return;
      }
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          v: 1, id: "p3-ev-trunc", seq: 7, session: "livesession", at: new Date().toISOString(),
          harness: "claude", kind: "tool_result", subkind: "Bash", call_id: "p3-c-trunc", model_seen: true,
          in: "", out: headLines,
          truncated: true,
          range: { out: { offset: headLines.length, total: headLines.length + tailLines.length } }
        })
      });
    });

    const page = await context.newPage();
    page.setDefaultTimeout(8000);
    page.on("pageerror", (e) => { console.error("PAGEERROR:", e.message); errors.push(e.message); });
    page.on("console", (msg) => { if (msg.type() === "error") console.error("CONSOLE_ERR:", msg.text()); });

    await page.goto(BASE + "/s/livesession");
    const frameEl = await page.locator("iframe.sframe.on").elementHandle({ timeout: 10000 });
    const frame = await frameEl.contentFrame();
    await frame.waitForLoadState("load").catch(() => {});

    await frame.waitForSelector(".au-swrap .au-sq");
    await frame.waitForSelector('.au-row[data-call="p3-c-multi"]');

    // 1. Search ALPHA_NEEDLE -> hits on multi (In 1, Out 2), parent subagent (Out 1), and unrendered/thinking.
    console.log("STEP 1: basic search + badges");
    await runSearch(frame, "ALPHA_NEEDLE");
    await frame.waitForSelector(".au-shit");
    await frame.waitForSelector('.au-row[data-call="p3-c-multi"] .au-sbadge-in');

    const multiInText = await frame.locator('.au-row[data-call="p3-c-multi"] .au-sbadge-in').textContent();
    const multiOutText = await frame.locator('.au-row[data-call="p3-c-multi"] .au-sbadge-out').textContent();
    assert.match(multiInText, /\b1\b/);
    assert.match(multiOutText, /\b2\b/);

    // Verify subagent event badged parent call row p3-c-parent via parent_id routing.
    const parentBadge = frame.locator('.au-row[data-call="p3-c-parent"] .au-sbadge-out');
    await parentBadge.waitFor();
    assert.match(await parentBadge.textContent(), /\b1\b/);

    // Toggle thinking filter chip on -> hidden event row gets badged via data-event-id.
    const thinkChip = frame.locator('.au-fchip[data-au-kind="thinking"]');
    if ((await thinkChip.count()) > 0 && (await thinkChip.getAttribute("aria-pressed")) !== "true") {
      await thinkChip.click();
    }
    const thinkBadge = frame.locator('[data-event-id="p3-ev-think"] .au-sbadge-out');
    await thinkBadge.waitFor();
    assert.match(await thinkBadge.textContent(), /\b1\b/);

    // Click badged row p3-c-multi -> opens audit window on first matching hit (Out line 2) with .au-sinp pre-filled.
    console.log("STEP 2: open badged row & subagent parent routing");
    await frame.locator('.au-row[data-call="p3-c-multi"]').click();
    await frame.waitForSelector('.au-win .au-line.au-ln-target[data-line="2"]');
    assert.equal(await frame.locator('.au-win .au-sinp').inputValue(), "ALPHA_NEEDLE");
    const activeTabMulti = await frame.locator('.au-win .au-tab[aria-selected="true"]').getAttribute("data-au-tab");
    assert.equal(activeTabMulti, "out");

    // Trigger Enter on badged parent row p3-c-parent -> opens subagent event on Out L3 with breadcrumb back to parent call.
    await frame.evaluate(() => {
      const row = document.querySelector('.au-row[data-call="p3-c-parent"]');
      row.setAttribute("tabindex", "0");
      row.focus();
      row.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));
    });
    await frame.waitForSelector('.au-win .au-line.au-ln-target[data-line="3"]');
    await frame.waitForSelector('.au-win .au-crumb:not([hidden]) .au-crumb-back');
    const subTab = await frame.locator('.au-win .au-tab[aria-selected="true"]').getAttribute("data-au-tab");
    assert.equal(subTab, "out");
    await frame.locator('.au-win .au-crumb-back').click();
    await frame.waitForFunction(() => {
      const c = document.querySelector('.au-win .au-crumb');
      return c && c.hidden;
    });

    // Click unrendered hit in hit list (no row in #convo) -> still opens by event_id on Out L2.
    console.log("STEP 3: unrendered hit + truncated line 450 jump");
    const hits = frame.locator(".au-shit");
    const hitCount = await hits.count();
    let clickedUnrendered = false;
    for (let i = 0; i < hitCount; i++) {
      const txt = await hits.nth(i).textContent();
      if (txt.includes("UNRENDERED_NEEDLE")) {
        await hits.nth(i).click();
        clickedUnrendered = true;
        break;
      }
    }
    assert.equal(clickedUnrendered, true, "unrendered hit should appear in hit list");
    await frame.waitForSelector('.au-win .au-line.au-ln-target[data-line="2"]');

    // 2. Truncated body jump to line 450 (auto-paging range chunks!).
    await runSearch(frame, "TRUNC_NEEDLE_450");
    await frame.waitForSelector(".au-shit");
    await frame.locator(".au-shit").first().click();
    await frame.waitForSelector('.au-win .au-line.au-ln-target[data-line="450"]');

    if (SHOTS) {
      fs.mkdirSync(SHOTS, { recursive: true });
      await runSearch(frame, "ALPHA_NEEDLE");
      await frame.waitForSelector(".au-shit");
      await frame.locator('.au-row[data-call="p3-c-multi"]').click();
      await frame.waitForSelector('.au-win .au-line.au-ln-target[data-line="2"]');
      await page.screenshot({ animations: "disabled", path: path.join(SHOTS, "audit-p3-wide-dark.png") });

      await page.emulateMedia({ colorScheme: "light" });
      await page.evaluate(() => document.documentElement.setAttribute("data-theme", "light"));
      await frame.evaluate(() => document.documentElement.setAttribute("data-theme", "light"));
      await page.screenshot({ animations: "disabled", path: path.join(SHOTS, "audit-p3-wide-light.png") });
      await page.emulateMedia({ colorScheme: "dark" });
      await page.evaluate(() => document.documentElement.setAttribute("data-theme", "dark"));
      await frame.evaluate(() => document.documentElement.setAttribute("data-theme", "dark"));
    }

    // 3. Pagination with next cursor (>100 hits).
    console.log("STEP 4: pagination + regex/case/kind + errors");
    await runSearch(frame, "PAGINATED_TOKEN");
    await frame.waitForSelector(".au-smore");
    assert.match(await frame.locator(".au-slist-stat").textContent(), /100\+/);
    await frame.locator(".au-smore").click();
    await frame.waitForFunction(() => {
      const f = document.querySelector(".au-sfoot");
      return f && f.hidden;
    });
    assert.equal(await frame.locator(".au-shit").count(), 105);

    // 4. Regex + Case-sensitive + Kind filter.
    await runSearch(frame, "CaseCheck_[0-9]+", { regex: true, caseSensitive: true, kind: "tool_result" });
    await frame.waitForSelector(".au-shit");
    assert.equal(await frame.locator(".au-shit").count(), 1);
    await frame.locator(".au-shit").first().click();
    await frame.waitForSelector('.au-win .au-hit');
    const markedText = await frame.locator('.au-win .au-hit').textContent();
    assert.equal(markedText, "CaseCheck_77");

    // 5. Invalid regex 400 -> calm inline error.
    await runSearch(frame, "[invalid", { regex: true });
    await frame.waitForSelector(".au-slist-stat.au-sstat-err");

    // 6. Empty search result -> calm empty message.
    await runSearch(frame, "NO_SUCH_MATCH_ZZZ", { regex: false, caseSensitive: false });
    await frame.waitForFunction(() => {
      const s = document.querySelector(".au-slist-stat");
      return s && !s.classList.contains("au-sstat-err") && document.querySelectorAll(".au-shit").length === 0;
    });

    // 7. Clear removes hit list and all row badges.
    await runSearch(frame, "ALPHA_NEEDLE");
    await frame.waitForSelector(".au-sbadge");
    await frame.locator(".au-sclear").click();
    await frame.waitForFunction(() => {
      const list = document.querySelector(".au-slist");
      return list && list.hidden && document.querySelectorAll(".au-sbadge").length === 0;
    });

    // 8. Narrow viewport (480x840) zero horizontal overflow + light/dark captures.
    console.log("STEP 5: narrow overflow + captures");
    const closeBtn = frame.locator(".au-win .au-close");
    if ((await closeBtn.count()) > 0) await closeBtn.first().click();
    await page.setViewportSize({ width: 480, height: 840 });
    await runSearch(frame, "ALPHA_NEEDLE");
    await frame.waitForSelector(".au-shit");
    const overflow = await frame.evaluate(() => {
      const bar = document.querySelector(".au-bar");
      return {
        docOverflow: document.documentElement.scrollWidth > window.innerWidth,
        barOverflow: bar ? bar.scrollWidth > bar.clientWidth + 1 : false
      };
    });
    assert.equal(overflow.docOverflow, false, "narrow viewport document must not overflow horizontally");
    assert.equal(overflow.barOverflow, false, "narrow viewport audit bar must not overflow horizontally");

    if (SHOTS) {
      await page.emulateMedia({ colorScheme: "light" });
      await page.evaluate(() => document.documentElement.setAttribute("data-theme", "light"));
      await frame.evaluate(() => document.documentElement.setAttribute("data-theme", "light"));
      await page.screenshot({ animations: "disabled", path: path.join(SHOTS, "audit-p3-narrow-light.png") });
      await page.emulateMedia({ colorScheme: "dark" });
      await page.evaluate(() => document.documentElement.setAttribute("data-theme", "dark"));
      await frame.evaluate(() => document.documentElement.setAttribute("data-theme", "dark"));
      await page.screenshot({ animations: "disabled", path: path.join(SHOTS, "audit-p3-narrow-dark.png") });
    }
    assert.equal(errors.length, 0, "unexpected pageerror(s): " + errors.join("; "));
    console.log("AUDIT_VIEW_PHASE3_BROWSER_OK");
    await context.unrouteAll({ behavior: "ignoreErrors" }).catch(() => {});
    await context.close().catch(() => {});
  } finally {
    await browser.close().catch(() => {});
    await server.close().catch(() => {});
  }
})().catch((err) => {
  console.error(err);
  process.exit(1);
});
