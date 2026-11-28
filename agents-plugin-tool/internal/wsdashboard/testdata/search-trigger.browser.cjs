// Run by TestSearchTriggerBrowser against a disposable page fixture.
const assert = require('node:assert/strict');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const [base] = process.argv.slice(2);

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: 'en-US' });
    const page = await context.newPage(), errors = [];
    page.on('pageerror', e => errors.push(e.message));
    async function activeFrame() {
      await page.locator('iframe.sframe.on').waitFor();
      const f = await (await page.locator('iframe.sframe.on').elementHandle()).contentFrame();
      await f.waitForLoadState('domcontentloaded');
      return f;
    }

    // 1. Index page: #tkqs is mounted in #qsslot and mirrored in the shell header; #shandle is absent.
    await page.goto(base + '/');
    let frame = await activeFrame();
    await frame.locator('#guide').waitFor();
    await page.locator('#qsslot #tkqs').waitFor({ state: 'visible' });
    assert.equal(await frame.locator('#shandle').count(), 0, 'index page has no #shandle');
    assert.equal(await frame.locator('#tkqs').count(), 1, 'index frame mounts #tkqs');

    await page.locator('#qsslot #tkqs').click();
    await frame.locator('#tkqpop').waitFor({ state: 'visible' });
    await frame.locator('#tkqin').press('Escape');
    await frame.locator('#tkqpop').waitFor({ state: 'hidden' });

    await frame.locator('#guide').click();
    await page.keyboard.press('Control+k');
    await frame.locator('#tkqpop').waitFor({ state: 'visible' });
    await frame.locator('#tkqin').press('Escape');
    await frame.locator('#tkqpop').waitFor({ state: 'hidden' });

    // 2. Live session page (shell-framed): shell #qsslot #tkqs owns the centered
    //    search trigger pill, framed #shandle is hidden, and shortcuts open/close #tkqpop.
    await page.goto(base + '/s/livesession');
    frame = await activeFrame();
    await page.locator('#qsslot #tkqs').waitFor({ state: 'visible' });
    assert.equal(await frame.locator('#tkqs').count(), 0, 'live session frame omits duplicate #tkqs');
    assert.equal(await frame.locator('#shandle').isVisible(), false, 'framed live session hides duplicate right-end #shandle');

    await page.locator('#qsslot #tkqs').click();
    await frame.locator('#tkqpop').waitFor({ state: 'visible' });
    await frame.locator('#tkqin').press('Escape');
    await frame.locator('#tkqpop').waitFor({ state: 'hidden' });

    await frame.locator('#convo').click();
    await page.keyboard.press('Control+k');
    await frame.locator('#tkqpop').waitFor({ state: 'visible' });
    await frame.locator('#tkqin').press('Escape');
    await frame.locator('#tkqpop').waitFor({ state: 'hidden' });

    await frame.locator('#convo').click();
    await page.keyboard.press('/');
    await frame.locator('#tkqpop').waitFor({ state: 'visible' });
    await frame.locator('#tkqin').press('Escape');
    await frame.locator('#tkqpop').waitFor({ state: 'hidden' });

    await page.locator('#railbtn').focus();
    await page.keyboard.press('Control+k');
    await frame.locator('#tkqpop').waitFor({ state: 'visible' });
    await frame.locator('#tkqin').press('Escape');
    await frame.locator('#tkqpop').waitFor({ state: 'hidden' });

    // 3. Non-live session page (boardsession): shell #qsslot #tkqs owns centered search; framed #shandle stays hidden.
    await page.goto(base + '/s/boardsession');
    frame = await activeFrame();
    await page.locator('#qsslot #tkqs').waitFor({ state: 'visible' });
    assert.equal(await frame.locator('#tkqs').count(), 0, 'board session frame omits duplicate #tkqs');
    assert.equal(await frame.locator('#shandle').isVisible(), false, 'framed board session hides duplicate right-end #shandle');
    await page.locator('#qsslot #tkqs').click();
    await frame.locator('#tkqpop').waitFor({ state: 'visible' });
    await frame.locator('#tkqin').press('Escape');
    await frame.locator('#tkqpop').waitFor({ state: 'hidden' });

    assert.deepEqual(errors, [], 'no page errors: ' + JSON.stringify(errors));
    console.log('PASS: shell #qsslot #tkqs owns centered search on index and session pages while framed #shandle stays hidden and shortcuts open/close search.');
    await context.close();
  } finally {
    await browser.close();
  }
})().catch(e => { console.error(e); process.exitCode = 1; });
