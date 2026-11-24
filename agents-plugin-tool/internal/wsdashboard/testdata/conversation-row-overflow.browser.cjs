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
          icon: bounds(row.querySelector('.row-icon > .i')),
        });
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
        const turn = el.querySelector('.turnline > .conv-row');
        return {
          scrollWidth: el.scrollWidth, clientWidth: el.clientWidth,
          mail, tool: rowMetrics(tool), turn: rowMetrics(turn),
          toolCount: tool.querySelector('.au-tok').textContent,
          toolDuration: tool.querySelector('.au-dur').textContent,
        };
      });
      console.log(JSON.stringify({ width, ...metrics }));
      assert(metrics.scrollWidth <= metrics.clientWidth, `${width}px viewport overflows the conversation scroller`);
      assert.equal(metrics.mail.length, 2, 'incoming and outgoing mail rows');
      assert.equal(metrics.toolCount, '+115');
      assert.equal(metrics.toolDuration, '<0.1s');
      for (const { row, trailing } of [...metrics.mail, metrics.tool, metrics.turn]) {
        assert(trailing.width > 0 && trailing.right <= row.right, 'trailing metadata fits the row');
      }
      for (const mail of metrics.mail) {
        assert.equal(mail.count, '+188');
        assert(mail.ellipsized, 'long mail preview ellipsizes');
        assert(mail.arrow.right <= mail.icon.left && mail.icon.right <= mail.address.left,
          'direction arrow, envelope, and address have separate space');
        assert(Math.abs(mail.icon.left - metrics.turn.icon.left) <= 1, 'mail and turn icons share a gutter');
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
