// Run by TestBoardPanelFullscreenBrowser against the dashboard session frame.
const assert = require('node:assert/strict');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const [base] = process.argv.slice(2);

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    for (const exit of ['Escape', 'button']) {
      const tab = await browser.newPage({
        viewport: { width: 1440, height: 950 }, reducedMotion: 'reduce',
      });
      try {
        tab.setDefaultTimeout(5000);
        await tab.goto(base + '/s/livesession');
        const active = tab.locator('iframe.sframe.on');
        await active.waitFor({ state: 'visible' });
        const page = await (await active.elementHandle()).contentFrame();
        await page.waitForFunction(() => window.WsBoardAnchor?.free());
        const topic = page.locator('section.topic[data-topic="Topic 10"]');
        const panel = topic.locator('#p2');
        const body = topic.frameLocator('.tdoc iframe.doc-frame').getByText('Topic body text', { exact: true });
        const panelBody = panel.locator('.entry-body iframe.doc-frame');
        const panelText = panelBody.contentFrame().getByText('Second panel document', { exact: true });
        const restore = panel.locator('summary > .d-min');
        const select = card => card.evaluate(t => { window.WsBoardAnchor.select(t, { smooth: false }); });
        assert.equal(await topic.getAttribute('data-wb-offwindow'), 'hidden', 'the initial topic is virtualized');
        await select(topic);
        await body.waitFor({ state: 'visible' });
        await panelText.waitFor({ state: 'visible' });
        for (const moveBoard of [false, true]) {
          await panel.locator('summary > .d-max').click();
          await restore.waitFor({ state: 'visible' });
          if (moveBoard) {
            // Move the background window far enough that the topic would be
            // parked if only its ordinary off-window exemption applied.
            await select(page.locator('section.topic[data-topic="Topic 20"]'));
            await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
          }
          const restoreVisible = await restore.isVisible();
          const fullscreenFrameVisible = await panelText.isVisible();
          if (exit === 'Escape') await tab.keyboard.press('Escape');
          else await restore.click();
          await page.waitForFunction(() => !document.documentElement.hasAttribute('data-board-max'));
          assert.equal(await topic.getAttribute('data-wb-offwindow'), null, `${exit} restores the originating topic to the window`);
          assert(restoreVisible, 'the fullscreen restore control stays visible while the board moves');
          assert(fullscreenFrameVisible, 'the fullscreen panel document stays mounted while the board moves');
          await body.waitFor({ state: 'visible' });
          await panelText.waitFor({ state: 'visible' });
          assert(await panelBody.evaluate(el => {
            const r = el.getBoundingClientRect();
            return el.checkVisibility({ contentVisibilityAuto: true, visibilityProperty: true })
              && r.top < innerHeight && r.bottom > 0;
          }), 'the restored panel document is rendered within the viewport');
          console.log(`${exit} restored the topic with the board ${moveBoard ? 'moved' : 'in place'}`);
        }
      } finally {
        await tab.close();
      }
    }
  } finally {
    await browser.close();
  }
})().catch(err => { console.error(err); process.exitCode = 1; });
