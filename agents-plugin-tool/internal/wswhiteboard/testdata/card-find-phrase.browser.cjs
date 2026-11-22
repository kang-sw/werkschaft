// Phrase search across inline formatting, host (card-find.js) and doc frame
// (doc-frame.js runFind block). argv: card-find.js path, frame find block path.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);

const html = '<section class="topic"><p>Then restart the <strong>service</strong> and <em>restart</em> <a href="#x">the</a> service again.</p>' +
  '<p>restart the</p><p>service</p></section>';

(async () => {
  const host = fs.readFileSync(process.argv[2], 'utf8');
  const frame = fs.readFileSync(process.argv[3], 'utf8');
  const browser = await chromium.launch({ headless: true });
  try {
    const page = await browser.newPage();
    await page.setContent('<!DOCTYPE html><body>' + html + '</body>');
    await page.addScriptTag({ content: host });
    const got = await page.evaluate(() => {
      const root = document.querySelector('section.topic');
      const marks = window.wbCardFind.highlightNode(root, 'Restart the service');
      const out = marks.map(m => (m.wbFindGroup || [m]).map(h => h.textContent).join('|'));
      window.wbCardFind.clearHighlights(root);
      return { out, marks: root.querySelectorAll('mark').length, html: document.body.innerHTML };
    });
    // Two matches inside the first paragraph; none across the two <p> blocks.
    assert.deepEqual(got.out, ['restart the |service', 'restart| |the| service']);
    assert.equal(got.marks, 0);
    assert.equal(got.html, html);

    const page2 = await browser.newPage();
    await page2.setContent('<!DOCTYPE html><body>' + html + '</body>');
    const framed = await page2.evaluate(src => {
      const fn = new Function('d', 'body', src + '\nreturn { runFind: runFind, clearFind: clearFind, marks: function () { return findMarks; } };');
      const api = fn(document, document.body);
      const count = api.runFind('restart the service');
      const out = api.marks().map(m => (m.wbFindGroup || [m]).map(h => h.textContent).join('|'));
      api.clearFind();
      return { count, out, left: document.querySelectorAll('mark').length, html: document.body.innerHTML };
    }, frame);
    assert.equal(framed.count, 2);
    assert.deepEqual(framed.out, ['restart the |service', 'restart| |the| service']);
    assert.equal(framed.left, 0);
    assert.equal(framed.html, html);

    // Empty, hidden and skipped block elements are boundaries, not bridges.
    const blocks = '<section class="topic"><div>one<div></div>two</div><div>one<div hidden>x</div>two</div><div>one<br>two</div></section>';
    // A paragraph of N inline nodes with N one-letter matches: the match to
    // node mapping must stay linear (counted as Node.nodeValue reads).
    const n = 2000;
    const wide = '<section class="topic"><p>' + '<span>a</span>'.repeat(n) + '</p></section>';
    const probe = (page, src) => page.evaluate(({ src, blocks, wide }) => {
      const desc = Object.getOwnPropertyDescriptor(Node.prototype, 'nodeValue');
      let reads = 0;
      Object.defineProperty(Node.prototype, 'nodeValue', { configurable: true, set: desc.set, get() { reads++; return desc.get.call(this); } });
      const find = src ? (() => {
        const api = new Function('d', 'body', src + '\nreturn { runFind: runFind, clearFind: clearFind };')(document, document.body);
        return q => { const c = api.runFind(q); api.clearFind(); return c; };
      })() : q => { const root = document.querySelector('section.topic'); const c = window.wbCardFind.highlightNode(root, q).length; window.wbCardFind.clearHighlights(root); return c; };
      document.body.innerHTML = blocks;
      const bridged = find('onetwo');
      document.body.innerHTML = wide;
      reads = 0;
      const count = find('a');
      return { bridged, count, reads };
    }, { src, blocks, wide });
    for (const [label, tab, src] of [['host', page, null], ['frame', page2, frame]]) {
      const got = await probe(tab, src);
      assert.equal(got.bridged, 0, label + ' bridges a block boundary');
      assert.equal(got.count, n, label + ' match count');
      assert.ok(got.reads < 20 * n, label + ' match mapping is quadratic: ' + got.reads + ' nodeValue reads for ' + n + ' nodes');
    }
    console.log('card-find phrase ok');
  } finally {
    await browser.close();
  }
})().catch(err => { console.error(err); process.exit(1); });
