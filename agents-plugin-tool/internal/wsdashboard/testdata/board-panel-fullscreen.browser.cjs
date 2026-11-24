// Run by TestBoardPanelFullscreenBrowser against the dashboard session frame.
const assert = require('node:assert/strict');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const [base] = process.argv.slice(2);

async function checkJumpPin(browser, release) {
  const tab = await browser.newPage({
    viewport: { width: 1440, height: 950 }, reducedMotion: 'reduce',
  });
  try {
    tab.setDefaultTimeout(5000);
    await tab.goto(base + '/s/livesession');
    const page = tab.frameLocator('iframe.sframe.on');
    const board = page.locator('#bcol');
    const topic = board.locator('section.topic[data-topic="Topic 10"]');
    const target = topic.locator('#p2');
    const above = topic.locator('.tdoc iframe.doc-frame');
    const link = page.locator('#convo a[data-wb-jump="p2"]');
    await topic.evaluate(el => {
      window.WsBoardAnchor.select(el);
      const card = document.createElement('section');
      card.className = 'wbupdate-topic';
      card.dataset.wbTopic = el.id;
      card.innerHTML = '<a href="#p2" data-wb-jump="p2">#p2</a>';
      document.querySelector('#r-entries .centries').prepend(card);
    });
    await above.contentFrame().getByText('Topic body text', { exact: true }).waitFor();
    const measure = () => topic.locator('#p2, #removed-panel-space').evaluate(el => {
      const col = el.closest('#bcol');
      return { offset: el.getBoundingClientRect().top - col.getBoundingClientRect().top, top: col.scrollTop };
    });
    const grow = async height => {
      const before = await above.evaluate(el => el.offsetHeight);
      // Release a height report from the sandbox after the panel has landed.
      await above.contentFrame().locator('body').evaluate((el, height) => {
        const docKey = document.querySelector('meta[name="wb-doc-key"]').content;
        parent.postMessage({ wbDoc: 'height', docKey, height }, '*');
      }, before + height);
      await expectFrameHeight(above, before + height);
      await board.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    };
    await link.click();
    assert.equal(await target.evaluate(el => document.activeElement === el), true, 'the conversation link lands on the panel');
    const landed = await measure();
    // The height report lands after the old history follow has gone quiet.
    await tab.waitForTimeout(release === 'timeout' ? 950 : 450);
    await grow(140);
    const held = await measure();
    assert.ok(Math.abs(held.offset - landed.offset) <= 3, 'late height keeps the panel in place: ' + JSON.stringify({ landed, held }));
    assert.ok(held.top > landed.top + 130, 'the board compensated for the actual frame report');
    if (release === 'wheel') {
      const bounds = await board.boundingBox();
      await tab.mouse.move(bounds.x + bounds.width - 2, bounds.y + 240);
      await tab.mouse.wheel(0, 85);
      await board.evaluate((el, top) => new Promise(resolve => {
        if (el.scrollTop > top + 80) resolve();
        else el.addEventListener('scrollend', resolve, { once: true });
      }), held.top);
      assert.ok((await measure()).top > held.top + 80, 'native wheel moved the board during the pin');
    } else if (release === 'timeout') {
      await tab.waitForTimeout(600);
    } else if (release === 'native-scroll') {
      await board.evaluate(el => el.scrollBy({ top: 85, behavior: 'instant' }));
      assert.ok((await measure()).top > held.top + 80, 'native programmatic scroll moved the board without wheel input');
    } else if (release === 'removal') {
      await target.evaluate(el => {
        const space = document.createElement('div');
        space.id = 'removed-panel-space';
        space.style.height = el.offsetHeight + 'px';
        el.replaceWith(space);
      });
    } else if (release === 'replacement') {
      await tab.emulateMedia({ reducedMotion: 'no-preference' });
      const replacedAt = await target.evaluate(el => new Promise(resolve => {
        const col = el.closest('#bcol');
        col.addEventListener('scroll', () => {
          const at = col.scrollTop;
          window.WsBoardAnchor.land(el.closest('.topic').querySelector('.tdoc iframe'), at, false);
          resolve(at);
        }, { once: true });
        window.WsBoardAnchor.land(el, col.scrollTop + 520, true);
      }));
      await tab.waitForTimeout(380);
      assert.ok(Math.abs((await measure()).top - replacedAt) <= 3, 'a jump to the current offset cancels old native smooth motion');
    }
    const released = await measure();
    await grow(110);
    const after = await measure();
    assert.ok(Math.abs(after.top - released.top) <= 3, release + ' releases scrollTop: ' + JSON.stringify({ released, after }));
    assert.ok(after.offset > released.offset + 100, 'growth after release moves the panel');
    console.log('conversation panel jump holds through late heights and releases on ' + release);
  } finally {
    await tab.close();
  }
}

async function checkDestinationClearance(browser) {
  const tab = await browser.newPage({ viewport: { width: 1440, height: 950 }, reducedMotion: 'reduce' });
  try {
    await tab.goto(base + '/s/livesession');
    const page = tab.frameLocator('iframe.sframe.on');
    const board = page.locator('#bcol');
    const target = board.locator('section.topic[data-topic="Topic 10"] #p2');
    await board.evaluate(col => {
      const first = window.WsBoardAnchor.windowInfo().list[0];
      window.WsBoardAnchor.select(first, { smooth: false });
      const card = document.createElement('section');
      card.className = 'wbupdate-topic';
      card.dataset.wbTopic = col.querySelector('section.topic[data-topic="Topic 10"]').id;
      card.innerHTML = '<a href="#p2" data-wb-jump="p2">#p2</a>';
      document.querySelector('#r-entries .centries').prepend(card);
    });
    const rail = () => board.evaluate(col => {
      const head = col.querySelector('.tophead.wb-stuck');
      const pill = col.querySelector('.oq-wrap.wb-oq-top');
      return {
        stack: col.querySelectorAll('.wb-rail-wrap.up .wb-cap:not([hidden])').length,
        headBottom: head?.getBoundingClientRect().bottom,
        pillBottom: pill?.querySelector('.oq-pill').getBoundingClientRect().bottom,
      };
    });
    const departure = await rail();
    assert.equal(departure.stack, 0, 'departure card has no upper rail capsules');
    // Give the incoming history panel a tall payload before the real click.
    await board.evaluate(col => {
      const topic = col.querySelector('section.topic[data-topic="Topic 10"]');
      const panel = topic.querySelector('#p2') || window.WsBoardVirtual.searchRoots(topic)
        .map(root => root.querySelector('#p2')).find(Boolean);
      panel.style.minHeight = innerHeight + 'px';
    });
    await page.locator('#convo a[data-wb-jump="p2"]').click();
    await board.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(() => requestAnimationFrame(resolve)))));
    const destination = await rail();
    const landed = await target.evaluate(el => ({
      top: el.getBoundingClientRect().top,
      pin: window.WsBoardAnchor.pinning(),
      focused: document.activeElement === el,
    }));
    assert.equal(destination.stack, 2, 'destination card has two upper rail capsules');
    assert.ok(landed.focused && landed.pin, 'conversation navigation focuses and pins the destination');
    assert.ok(landed.top >= Math.max(destination.headBottom, destination.pillBottom) - 2,
      'tall panel lands below the destination controls: ' + JSON.stringify({ departure, destination, landed }));
  } finally {
    await tab.close();
  }
}

async function checkMentionRelease(browser) {
  const tab = await browser.newPage({ viewport: { width: 1440, height: 950 }, reducedMotion: 'reduce' });
  try {
    await tab.goto(base + '/s/livesession');
    const page = tab.frameLocator('iframe.sframe.on');
    const source = page.locator('section.topic[data-topic="Topic 10"]');
    const far = page.locator('section.topic[data-topic="Topic 20"]');
    const board = page.locator('#bcol');
    const select = card => card.evaluate(el => window.WsBoardAnchor.select(el, { smooth: false }));
    const frame = source.frameLocator('.tdoc iframe.doc-frame');
    await select(source);
    await frame.getByText('Topic body text').evaluate(() => {
      const docKey = document.querySelector('meta[name="wb-doc-key"]').content;
      parent.postMessage({ wbDoc: 'sel', docKey, text: 'Topic body text', x: 30, y: 30 }, '*');
    });
    const popover = page.locator('[data-wbmention-pop]');
    const note = popover.getByRole('textbox');
    await note.focus();
    assert.equal(await note.evaluate(el => el === document.activeElement), true, 'the visible composer editor takes focus');
    await select(far);
    assert.equal(await source.getAttribute('data-wb-offwindow'), 'parked', 'the mention holds the far frame');
    await note.press('Escape');
    await popover.waitFor({ state: 'hidden', timeout: 2500 });
    const moved = await board.evaluate(el => {
      const start = el.scrollTop;
      el.scrollBy({ top: start > 40 ? -30 : 30, behavior: 'instant' });
      return Math.abs(el.scrollTop - start);
    });
    assert.ok(moved > 1, 'the board scrolls after Escape even at its home edge');
    await source.locator('.tdoc iframe.doc-frame').waitFor({ state: 'detached', timeout: 2500 });
    assert.equal(await source.getAttribute('data-wb-offwindow'), 'hidden', 'scroll reconciliation releases the former mention frame');
  } finally {
    await tab.close();
  }
}

async function expectFrameHeight(frame, min) {
  await frame.evaluate((el, height) => new Promise((resolve, reject) => {
    const timeout = setTimeout(() => {
      observer.disconnect();
      reject(new Error('Expected frame height ' + height + ', got ' + el.offsetHeight + ' (' + el.style.height + ')'));
    }, 2500);
    const observer = new ResizeObserver(() => {
      if (el.offsetHeight < height) return;
      observer.disconnect();
      clearTimeout(timeout);
      resolve();
    });
    observer.observe(el);
  }), min);
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    for (const release of ['wheel', 'timeout', 'native-scroll', 'removal', 'replacement']) await checkJumpPin(browser, release);
    await checkDestinationClearance(browser);
    await checkMentionRelease(browser);
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
