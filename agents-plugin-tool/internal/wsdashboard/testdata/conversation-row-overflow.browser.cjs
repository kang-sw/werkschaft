const assert = require('node:assert/strict');
const path = require('node:path');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const [base, shots] = process.argv.slice(2);

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    for (const width of [440, 1440]) {
      const page = await browser.newPage({ viewport: { width, height: 850 }, colorScheme: 'dark' });
      await page.goto(base + '/s/livesession');
      const convo = page.frameLocator('iframe.sframe.on').locator('#convo');
      const turnSummary = convo.locator('details.d-turn > summary.d-turn-sum').first();
      await turnSummary.waitFor({ timeout: 5000 });
      await turnSummary.click();
      await convo.locator('.mailline.sent .mpeer').filter({ hasText: 'id:recipientmailgeometry' }).waitFor({ timeout: 5000 });
      if (shots) await convo.screenshot({ path: path.join(shots, width + '.png') });

      const metrics = await convo.evaluate(el => {
        const bounds = node => {
          const { left, right, width } = node.getBoundingClientRect();
          return { left, right, width };
        };
        const rowMetrics = row => ({
          row: bounds(row),
          trailing: bounds(row.querySelector('.row-time')),
          icon: bounds(row.querySelector('.row-icon > .i') || row.querySelector('.row-icon')),
        });
        const turnMetrics = row => {
          const main = row.querySelector('.d-turn-main');
          const meta = row.querySelector('.d-turn-meta');
          const tok = row.querySelector('[data-conv-turn-tokens]');
          const dot = tok ? tok.previousElementSibling : null;
          return {
            ...rowMetrics(row),
            main: bounds(main),
            meta: meta ? bounds(meta) : null,
            metaDisplay: meta ? getComputedStyle(meta).display : '',
            metaEllipsized: !!meta && getComputedStyle(meta).textOverflow === 'ellipsis' && meta.scrollWidth > meta.clientWidth,
            tok: tok ? bounds(tok) : null,
            dot: dot ? bounds(dot) : null,
            tokText: tok ? tok.textContent.trim() : '',
            tokShrink: tok ? getComputedStyle(tok).flexShrink : '',
            tokClipped: !!tok && (tok.scrollWidth > tok.clientWidth || getComputedStyle(tok).overflow === 'hidden'),
          };
        };
        const mail = [...el.querySelectorAll('.mailline > .conv-row')]
          .filter(row => row.querySelector('.mpeer')?.textContent.endsWith('mailgeometry'))
          .map(row => {
            const preview = row.querySelector('.mfirst');
            return {
              ...rowMetrics(row),
              arrow: bounds(row.querySelector('.arr')),
              address: bounds(row.querySelector('.mpeer')),
              count: row.querySelector('.au-tok').textContent,
              ellipsized: getComputedStyle(preview).textOverflow === 'ellipsis' && preview.scrollWidth > preview.clientWidth,
            };
          });
        const tool = el.querySelector('.conv-row[data-call="overflow-call"]');
        const turns = [...el.querySelectorAll('details.d-turn > summary.d-turn-sum')];
        // Populate async audit-counts & whiteboard-fold badges on turn 2 to verify middle overflow without .d-turn-reason.
        const secondTurn = turns[1];
        if (secondTurn) {
          const fold = secondTurn.querySelector('[data-conv-turn-folded]');
          if (fold) fold.hidden = false;
          const counts = secondTurn.querySelector('.turn-audit-counts');
          if (counts) {
            counts.textContent = 'system 10 · think 2 · mcp 4 · read 12 · write 8 · extra-long-audit-segment';
            counts.hidden = false;
          }
          void secondTurn.offsetWidth;
        }
        return {
          scrollWidth: el.scrollWidth, clientWidth: el.clientWidth,
          mail, tool: rowMetrics(tool), turn: turnMetrics(turns[0]),
          turnReasonless: secondTurn ? turnMetrics(secondTurn) : null,
          toolCount: tool.querySelector('.au-tok').textContent,
          toolDuration: tool.querySelector('.au-dur').textContent,
        };
      });
      console.log(JSON.stringify({ width, ...metrics }));
      assert(metrics.scrollWidth <= metrics.clientWidth, `${width}px viewport overflows the conversation scroller`);
      assert.equal(metrics.mail.length, 2, 'incoming and outgoing mail rows');
      assert.equal(metrics.toolCount, '+115');
      assert.equal(metrics.toolDuration, '<0.1s');
      for (const { row, trailing } of [...metrics.mail, metrics.tool, metrics.turn, ...(metrics.turnReasonless ? [metrics.turnReasonless] : [])]) {
        assert(trailing.width > 0 && trailing.right <= row.right, 'trailing metadata fits the row');
      }
      for (const t of [metrics.turn, metrics.turnReasonless].filter(Boolean)) {
        assert.equal(t.tokShrink, '0', 'turn token count must never shrink');
        assert(!t.tokClipped, 'turn token count must never clip');
        assert(t.tok.width > 0 && t.tok.right <= t.main.right + 0.5 && t.tok.right <= t.trailing.left,
          'turn token count fits unclipped before row-time');
        assert(t.dot && t.dot.right <= t.tok.left, 'separator dot before token count stays intact');
        assert(t.tokText.startsWith('+') && t.tokText.endsWith(' tok'), 'expected formatted token count');
      }
      if (width <= 440) {
        assert(metrics.turn.metaEllipsized, '440px viewport ellipsizes long turn middle metadata');
        assert(metrics.turnReasonless && metrics.turnReasonless.metaEllipsized,
          '440px viewport ellipsizes reasonless middle metadata before token count');
      }
      for (const mail of metrics.mail) {
        assert.equal(mail.count, '+188');
        assert(mail.ellipsized, 'long mail preview ellipsizes');
        assert(mail.arrow.right <= mail.icon.left && mail.icon.right <= mail.address.left,
          'direction arrow, envelope, and address have separate space');
        assert(Math.abs(mail.icon.left - metrics.mail[0].icon.left) <= 1, 'folded mail icons share a gutter');
      }
      await page.close();
    }
  } finally {
    await browser.close();
  }
})().catch(err => {
  console.error(err);
  process.exitCode = 1;
});
