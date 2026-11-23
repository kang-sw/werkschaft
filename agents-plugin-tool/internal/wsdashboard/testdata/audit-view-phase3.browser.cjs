"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");

const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE || "playwright");
const BASE = process.env.AUDIT_BASE;
const SHOTS = process.env.AUDIT_P3_SHOTS || "";

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

    const page = await context.newPage();
    page.setDefaultTimeout(8000);
    page.on("pageerror", (e) => { console.error("PAGEERROR:", e.message); errors.push(e.message); });
    page.on("console", (msg) => { if (msg.type() === "error") console.error("CONSOLE_ERR:", msg.text()); });

    await page.goto(BASE + "/s/livesession");
    const frameEl = await page.locator("iframe.sframe.on").elementHandle({ timeout: 10000 });
    const frame = await frameEl.contentFrame();
    await frame.waitForLoadState("load").catch(() => {});
    await frame.waitForSelector('.au-row[data-call="p3-c-multi"]');
    // Let call hydration settle before looking.
    await frame.waitForFunction(() => document.querySelector('.au-row[data-call="p3-c-multi"]').getAttribute("data-au-hydrated") === "1");

    const shot = async (name) => {
      if (!SHOTS) return;
      fs.mkdirSync(SHOTS, { recursive: true });
      await page.screenshot({ animations: "disabled", path: path.join(SHOTS, name) });
    };

    // 1. The conversation draws no audit search bar, no kind filter chips, and the
    // tab line carries no harness chip.
    console.log("STEP 1: no search bar, filter chips, or header harness chip");
    assert.equal(await frame.locator(".au-swrap, .au-bar, .au-fchip, .au-sq").count(), 0);
    assert.equal(await frame.locator("#chead .hb, #r-entries .thead .hb").count(), 0);
    await shot("audit-p3-wide-dark.png");

    // 2. A call row still opens its popup.
    console.log("STEP 2: a call row opens the popup");
    await frame.locator('.au-row[data-call="p3-c-multi"]').click();
    assert.equal(await frame.locator('.au-win').count(), 0, 'summary expands without opening the inspector');
    await frame.locator('.au-row[data-call="p3-c-multi"]').locator('xpath=..').locator('.au-open-call').click();
    await frame.waitForSelector(".au-win");
    assert.equal(await frame.locator(".au-win .au-sinp").inputValue(), "");
    await frame.locator(".au-win .au-close").first().click();
    await frame.waitForFunction(() => document.querySelectorAll(".au-win").length === 0);

    // 3. The inspector draws the server's spans (the event route's matches), so each
    // server-accepted RE2 query is marked exactly as the server matched it.
    console.log("STEP 3: inspector marks are the server's spans");
    const openHit = async (query, opts, eventId) => {
      await frame.evaluate(([id, o]) => window.WsAudit.openEvent(id, o), [eventId, { search: query, regex: !!opts.regex, caseSensitive: !!opts.caseSensitive }]);
      await frame.waitForSelector(".au-win .au-hit");
    };
    const marks = async (lineNo) => frame.locator(".au-win " + (lineNo ? `.au-line[data-line="${lineNo}"] ` : "") + ".au-hit").allTextContents();
    await openHit("(?i)alpha_needle", { regex: true, caseSensitive: true }, "p3-ev-multi-out");
    assert.ok((await marks()).length > 0, "inspector marks a (?i) RE2 query the server matched");
    await shot("inspector-re2-inline-flag.png");

    await openHit("(?i:foo)", { regex: true, caseSensitive: true }, "p3-ev-uni");
    assert.deepEqual(await marks(1), ["FOO"]);
    await shot("inspector-scoped-flag.png");

    await openHit("\\p{Greek}+", { regex: true, caseSensitive: true }, "p3-ev-uni");
    assert.deepEqual(await marks(2), ["τα", "ελληνικά", "αβγ"]);
    await shot("inspector-greek-script.png");

    await openHit("^.$", { regex: true, caseSensitive: true }, "p3-ev-uni");
    assert.deepEqual(await marks(3), ["😀"], "a lone emoji is one rune for ^.$");
    assert.deepEqual(await marks(4), []);
    await shot("inspector-emoji-line.png");

    // The inspector's own find box asks the server too: an empty-able Unicode class over an astral
    // character followed by a letter completes (it froze the local loop), and marks the letter.
    await frame.locator(".au-win .au-sinp").fill("\\p{L}*");
    await frame.waitForFunction(() => {
      const l = document.querySelector('.au-win .au-line[data-line="4"] .au-hit');
      return l && l.textContent === "a";
    });
    assert.deepEqual(await marks(4), ["a"]);
    assert.deepEqual(await marks(3), []);
    assert.deepEqual(await marks(1), ["plain", "FOO", "line"]);
    await shot("inspector-empty-unicode-class.png");

    // A refused query shows the server's text and zero marks.
    await frame.locator(".au-win .au-sinp").fill("(");
    await frame.waitForSelector(".au-win .au-scnt.au-scnt-err");
    assert.match(await frame.locator(".au-win .au-scnt").textContent(), /valid regular expression/);
    assert.equal(await frame.locator(".au-win .au-hit").count(), 0);
    await shot("inspector-invalid-query.png");

    // Seam lines: the same-start alternative marks the retained head; both sides of a seam are marked.
    await openHit("ab|a", { regex: true, caseSensitive: true }, "p3-ev-seam1");
    assert.deepEqual(await marks(1), ["a"], "ab|a with head a and tail b marks a on the head");
    await shot("inspector-seam-same-start.png");
    await openHit("alpha|beta", { regex: true, caseSensitive: true }, "p3-ev-seam2");
    assert.deepEqual(await marks(1), ["alpha", "beta"]);
    await shot("inspector-seam-both-sides.png");

    // 4. Narrow viewport (480x840): no horizontal overflow, light and dark captures.
    console.log("STEP 4: narrow overflow + captures");
    const closeBtn = frame.locator(".au-win .au-close");
    if ((await closeBtn.count()) > 0) await closeBtn.first().click();
    await page.setViewportSize({ width: 480, height: 840 });
    const overflow = await frame.evaluate(() => document.documentElement.scrollWidth > window.innerWidth);
    assert.equal(overflow, false, "narrow viewport document must not overflow horizontally");

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
