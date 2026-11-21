const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const pw = require(process.env.WS_PLAYWRIGHT_MODULE);
const [base] = process.argv.slice(2);
const shotsDir = process.env.WS_DIALOG_SHOTS || '';
const shotPrefix = process.env.WS_DIALOG_SHOT_PREFIX || '';
const captureOnly = process.env.WS_DIALOG_CAPTURE_ONLY === '1';

const VIEWPORTS = [
  { name: '1280x800', width: 1280, height: 800 },
  { name: '700x1300', width: 700, height: 1300 },
];

function webkitAvailable() {
  if (!pw.webkit || typeof pw.webkit.executablePath !== 'function') return false;
  try {
    return fs.existsSync(pw.webkit.executablePath());
  } catch {
    return false;
  }
}

async function openVariant(page, variant) {
  if (variant === 'confirm') {
    await page.evaluate(() => {
      window.__dlgPromise = window.wsConfirm(
        '이 세션을 중지할까요? 실행 중인 백그라운드 작업 2개가 프로세스와 함께 종료됩니다.'
      );
    });
    const dlg = page.locator('dialog.ws-confirm[open]');
    await dlg.waitFor({ state: 'visible' });
    return {
      dlg,
      close: async () => {
        await dlg.locator('.pbtn:not(.danger)').click();
        await dlg.waitFor({ state: 'detached' });
        await page.evaluate(() => window.__dlgPromise);
      },
    };
  }

  if (variant === 'destructive') {
    await page.evaluate(() => {
      window.__dlgPromise = window.wsConfirm(window.wsT('js.draft.leave'), {
        accept: window.wsT('js.draft.leave.accept'),
        cancel: window.wsT('js.draft.leave.stay'),
      });
    });
    const dlg = page.locator('dialog.ws-confirm[open]');
    await dlg.waitFor({ state: 'visible' });
    return {
      dlg,
      close: async () => {
        await dlg.locator('.pbtn:not(.danger)').click();
        await dlg.waitFor({ state: 'detached' });
        await page.evaluate(() => window.__dlgPromise);
      },
    };
  }

  if (variant === 'input-confirm') {
    await page.evaluate(() => {
      const dl = document.createElement('dialog');
      dl.className = 'rdeldlg';
      dl.id = 'ws-test-rdeldlg';
      dl.innerHTML =
        '<h2 class="rdhd"><span>워크트리 삭제</span></h2>' +
        '<code class="rdpath">/Users/dev/worktrees/feature-branch</code>' +
        '<div class="rdfacts"><div class="rdfact warn"><span>커밋되지 않은 변경 2개</span></div>' +
        '<ul class="rdpaths"><li>assets/dashboard-rail.css</li><li>assets/confirm.js</li></ul></div>' +
        '<label class="rdconf"><span class="rdconft">계속하려면 <code>feature-branch</code>를 입력하세요.</span>' +
        '<input class="rdq" type="text" placeholder="feature-branch"></label>' +
        '<div class="rdrow"><button type="button" class="pbtn">취소</button>' +
        '<button type="button" class="pbtn danger" disabled>삭제</button></div>';
      document.body.appendChild(dl);
      dl.showModal();
    });
    const dlg = page.locator('#ws-test-rdeldlg[open]');
    await dlg.waitFor({ state: 'visible' });
    return {
      dlg,
      close: async () => {
        await page.evaluate(() => {
          const dl = document.getElementById('ws-test-rdeldlg');
          if (dl) { if (dl.open) dl.close(); dl.remove(); }
        });
      },
    };
  }

  if (variant === 'permission') {
    await page.evaluate(() => {
      const dl = document.createElement('dialog');
      dl.className = 'rdeldlg pmdlg';
      dl.id = 'ws-test-pmdlg';
      dl.innerHTML =
        '<h2 class="rdhd"><span>전체 권한으로 실행할까요?</span></h2>' +
        '<div class="rdpath">/Users/dev/worktrees/repo</div>' +
        '<div class="rdfacts"><div class="rdfact bad"><span>확인 없이 명령을 실행합니다.</span></div></div>' +
        '<label class="pmack"><input type="checkbox"><span>위험을 이해했으며 계속합니다.</span></label>' +
        '<div class="rdrow"><button type="button" class="pbtn">취소</button>' +
        '<button type="button" class="pbtn danger" disabled>계속</button></div>';
      document.body.appendChild(dl);
      dl.showModal();
    });
    const dlg = page.locator('#ws-test-pmdlg[open]');
    await dlg.waitFor({ state: 'visible' });
    return {
      dlg,
      close: async () => {
        await page.evaluate(() => {
          const dl = document.getElementById('ws-test-pmdlg');
          if (dl) { if (dl.open) dl.close(); dl.remove(); }
        });
      },
    };
  }

  throw new Error('unknown variant: ' + variant);
}

async function measureDialog(dlg) {
  return dlg.evaluate(el => {
    const cs = getComputedStyle(el);
    const rect = el.getBoundingClientRect();
    const padY = parseFloat(cs.paddingTop) + parseFloat(cs.paddingBottom);
    const borderY = parseFloat(cs.borderTopWidth) + parseFloat(cs.borderBottomWidth);
    const gap = parseFloat(cs.rowGap) || 0;
    const kids = [...el.children].filter(c => getComputedStyle(c).display !== 'none');
    let contentH = 0;
    for (let i = 0; i < kids.length; i++) {
      const kcs = getComputedStyle(kids[i]);
      const kr = kids[i].getBoundingClientRect();
      contentH += kr.height + parseFloat(kcs.marginTop) + parseFloat(kcs.marginBottom);
      if (i > 0) contentH += gap;
    }
    const row = el.querySelector('.rdrow');
    const rowCS = row ? getComputedStyle(row) : null;
    const btns = [...el.querySelectorAll('.rdrow button')].map(b => {
      const br = b.getBoundingClientRect();
      return { text: b.textContent, width: br.width, height: br.height };
    });
    return {
      vw: window.innerWidth,
      vh: window.innerHeight,
      rect: { x: rect.x, y: rect.y, width: rect.width, height: rect.height, left: rect.left, right: rect.right, top: rect.top, bottom: rect.bottom },
      expectedMaxH: contentH + padY + borderY,
      display: cs.display,
      flexDirection: cs.flexDirection,
      rowAlignItems: rowCS ? rowCS.alignItems : null,
      btns,
    };
  });
}

function assertMetrics(engine, vp, variant, m) {
  const tag = `${engine} ${vp.name} ${variant}`;
  assert.ok(m.btns.length >= 2, `${tag}: expected >= 2 action buttons`);
  for (const b of m.btns) {
    assert.ok(b.height >= 16 && b.height <= 48, `${tag}: button "${b.text}" height ${b.height}px must be natural single-line (<= 48px)`);
  }
  assert.ok(
    m.rect.height <= m.expectedMaxH + 4,
    `${tag}: dialog height ${m.rect.height}px exceeds content + padding (${m.expectedMaxH}px)`
  );
  assert.ok(
    m.rect.height < m.vh * 0.6,
    `${tag}: dialog height ${m.rect.height}px stretched toward viewport (${m.vh}px)`
  );
  const cx = (m.rect.left + m.rect.right) / 2;
  const cy = (m.rect.top + m.rect.bottom) / 2;
  assert.ok(Math.abs(cx - m.vw / 2) <= 2, `${tag}: dialog not horizontally centered (cx=${cx}, vw/2=${m.vw / 2})`);
  assert.ok(Math.abs(cy - m.vh / 2) <= 2, `${tag}: dialog not vertically centered (cy=${cy}, vh/2=${m.vh / 2})`);
  // Guard against macOS WKWebView's top-layer display:grid stretching bug and button row stretching.
  assert.equal(m.display, 'flex', `${tag}: dialog must use flex column rather than grid`);
  assert.equal(m.flexDirection, 'column', `${tag}: dialog must lay out vertically`);
  assert.equal(m.rowAlignItems, 'center', `${tag}: .rdrow must align buttons at center, not stretch`);
}

async function runEngine(name, browserType) {
  const browser = await browserType.launch({ headless: true });
  try {
    for (const vp of VIEWPORTS) {
      const ctx = await browser.newContext({
        viewport: { width: vp.width, height: vp.height },
        locale: 'ko-KR',
      });
      await ctx.addCookies([
        { name: 'ws_dashboard_theme', value: 'dark', url: base },
        { name: 'ws_dashboard_lang', value: 'ko', url: base },
      ]);
      const tab = await ctx.newPage();
      const errors = [];
      tab.on('pageerror', e => errors.push(e.message));
      await tab.goto(`${base}/s/livesession`);
      await tab.locator('iframe.sframe.on').waitFor({ state: 'visible' });
      const frame = await (await tab.locator('iframe.sframe.on').elementHandle()).contentFrame();
      await frame.locator('#convo').waitFor({ state: 'visible' });

      // Test both top-level shell document and framed session document.
      const targets = [
        { scope: 'shell', page: tab, variants: ['confirm', 'destructive', 'input-confirm', 'permission'] },
        { scope: 'frame', page: frame, variants: ['confirm'] },
      ];

      for (const target of targets) {
        for (const variant of target.variants) {
          const handle = await openVariant(target.page, variant);
          const m = await measureDialog(handle.dlg);
          if (shotsDir && target.scope === 'shell' && (variant === 'confirm' || variant === 'input-confirm')) {
            fs.mkdirSync(shotsDir, { recursive: true });
            const fname = `${shotPrefix ? shotPrefix + '-' : ''}${name}-${vp.name}-${variant}.png`;
            await tab.screenshot({ path: path.join(shotsDir, fname) });
          }
          if (!captureOnly) {
            assertMetrics(`${name}/${target.scope}`, vp, variant, m);
          }
          await handle.close();
        }
      }

      // Also verify that even if a UA lacks dialog { height: fit-content }, our CSS
      // keeps dialog height fitted to content and buttons <= 48px.
      await tab.addStyleTag({ content: 'dialog { height: auto; }' });
      const handle = await openVariant(tab, 'confirm');
      const mFallback = await measureDialog(handle.dlg);
      if (!captureOnly) {
        assertMetrics(`${name}/ua-height-auto`, vp, 'confirm', mFallback);
      }
      await handle.close();

      assert.deepEqual(errors, [], `${name} ${vp.name} pageerrors`);
      await ctx.close();
    }
    console.log(`dialog layout ${name}: PASS`);
  } finally {
    await browser.close();
  }
}

(async () => {
  await runEngine('chromium', pw.chromium);
  if (webkitAvailable()) {
    await runEngine('webkit', pw.webkit);
  } else {
    console.log('dialog layout webkit: SKIP (WebKit browser binary not installed)');
  }
})().catch(e => {
  console.error(e);
  process.exitCode = 1;
});
