// Compact horizontal icon org-chart HUD browser acceptance against roster-hud-v2.
// env: HUD_BASE, HUD_MOCKUP (path to roster-hud-v2.html), HUD_SHOTS (optional dir).
const assert = require('node:assert/strict');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const base = process.env.HUD_BASE;
const switcher = process.env.HUD_SWITCH;
const mockup = process.env.HUD_MOCKUP;
const shots = process.env.HUD_SHOTS;

async function openSession(b, scenario, { width = 1280, height = 840, scheme = 'dark' } = {}) {
  if (scenario) await fetch(`${switcher}?s=${scenario}`);
  const ctx = await b.newContext({ viewport: { width, height }, deviceScaleFactor: 2, colorScheme: scheme, locale: 'ko-KR' });
  await ctx.addCookies([{ name: 'ws_dashboard_lang', value: 'ko', url: base }]);
  const tab = await ctx.newPage();
  const errors = [];
  tab.on('pageerror', e => errors.push(e.message));
  await tab.goto(`${base}/s/livesession`);
  const frame = await (await tab.locator('iframe.sframe.on').elementHandle()).contentFrame();
  await frame.locator('#agisland[data-mode="compact"]:not([hidden])').waitFor();
  await frame.waitForTimeout(350);
  return { ctx, tab, frame, errors };
}

async function chooseTheme(tab, frame, theme) {
  const openedRail = !await tab.locator('#gear').isVisible();
  if (openedRail) await tab.locator('#railbtn').click();
  await tab.locator('#gear').click();
  await tab.locator(`#gmenu [data-theme-set="${theme}"]`).click();
  await tab.locator(`html[data-theme="${theme}"]`).waitFor();
  await frame.locator(`html[data-theme="${theme}"]`).waitFor();
  await tab.keyboard.press('Escape');
  if (openedRail) {
    await tab.keyboard.press('Escape');
    await tab.locator('html:not(.rail-open)').waitFor();
  }
}

async function checkKeyedRebuild(frame) {
  const result = await frame.evaluate(async () => {
    const tree = document.getElementById('r-tree');
    const carrier = tree.querySelector('.ag-hud-data');
    const compact = document.getElementById('agcompact');
    const idle = document.getElementById('agidle');
    const pop = document.getElementById('agpop');
    const original = carrier.textContent;
    const source = JSON.parse(original);
    const node = id => compact.querySelector(`.hud-node[data-id="${id}"]`);
    const project = data => {
      carrier.textContent = JSON.stringify(data);
      window.wsHud.project(tree, compact, idle, pop, () => {});
    };
    const h3 = node('h3'), h4 = node('h4'), h5 = node('h5');
    const ring = h3.querySelector('.hud-ring');
    const animation = ring.getAnimations()[0];
    const startTime = animation?.currentTime;
    const delay = id => parseFloat(getComputedStyle(node(id).querySelector('.hud-ring')).animationDelay) * 1000;
    const stagger = [delay('h3'), delay('h5')];
    await new Promise(resolve => setTimeout(resolve, 65));
    project(source);
    const same = node('h3') === h3 && node('h4') === h4 && node('h5') === h5 &&
      node('h3').querySelector('.hud-ring') === ring && ring.getAnimations()[0] === animation;
    const advanced = animation?.currentTime - startTime;
    const changed = structuredClone(source);
    const h2 = changed.roots.find(r => r.id === 'h1').children.find(c => c.id === 'h2');
    h2.children.reverse();
    h2.children.find(c => c.id === 'h3').state = 'thinking';
    h2.children.find(c => c.id === 'h3').label = 'sym-extract-updated';
    project(changed);
    const reordered = [...node('h2').closest('.tree-node').querySelector('.tree-kids').children]
      .map(w => w.querySelector('.hud-node').getAttribute('data-id'));
    const updated = node('h3') === h3 && node('h3').querySelector('.hud-ring') === ring &&
      node('h3').getAttribute('data-state') === 'thinking' &&
      node('h3').getAttribute('aria-label').includes('sym-extract-updated') &&
      node('h4') === h4 && node('h5') === h5;
    const thinkingDelay = delay('h3');
    const oldH4Animation = h4.querySelector('.hud-ring').getAnimations()[0];
    const oldH4Time = oldH4Animation?.currentTime;
    const oldClock = document.timeline.currentTime;
    h2.children = h2.children.filter(c => c.id !== 'h4');
    project(changed);
    const removed = !node('h4') && node('h3') === h3;
    project(source);
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    const newH4Time = node('h4').querySelector('.hud-ring').getAnimations()[0]?.currentTime;
    const elapsed = document.timeline.currentTime - oldClock;
    const period = 1150;
    const drift = Number.isFinite(oldH4Time) && Number.isFinite(newH4Time)
      ? Math.abs((((newH4Time - oldH4Time - elapsed) % period) + period * 1.5) % period - period / 2)
      : null;
    const remounted = node('h4') !== h4 && node('h3') === h3 && node('h5') === h5;
    carrier.textContent = original;
    return { same, advanced, stagger, reordered, updated, thinkingDelay, removed, remounted, drift,
      animationName: getComputedStyle(ring).animationName };
  });
  assert.equal(result.same, true, 'stable keys preserve buttons, rings and running CSS animation');
  assert(result.advanced > 30 && result.advanced < 300, `animation advances across unchanged rebuild: ${result.advanced}`);
  assert(result.stagger.every(x => Number.isFinite(x) && x <= 0 && x > -900), `tool delays are bounded: ${result.stagger}`);
  assert.notEqual(result.stagger[0], result.stagger[1], 'different agent keys stagger equal-period rings');
  assert.deepEqual(result.reordered, ['h4', 'h3'], 'sibling order follows new source order');
  assert.equal(result.updated, true, 'state and label update in place while siblings keep identity');
  assert(result.thinkingDelay <= 0 && result.thinkingDelay > -1150, 'state change restamps within thinking period');
  assert.equal(result.removed, true, 'removed keyed node disappears');
  assert.equal(result.remounted, true, 'reappearing node is new while surviving nodes stay mounted');
  assert(Number.isFinite(result.drift) && result.drift < 80, `remounted ring resumes phase within 80ms: ${result.drift}`);
  assert.match(result.animationName, /hud-spin-tool/, 'restored state uses tool animation');
}

(async () => {
  const b = await chromium.launch();
  try {
    // 1. Dark & Light themes + 12-agent 3-level geometry & computed-style checks
    for (const scheme of ['dark', 'light']) {
      const { ctx, tab, frame, errors } = await openSession(b, 'heavy', { width: 1280, scheme: scheme === 'dark' ? 'light' : 'dark' });
      await chooseTheme(tab, frame, scheme);
      const geom = await frame.evaluate(() => {
        const n = document.querySelector('#agcompact .hud-node');
        const e = n.querySelector('.hud-emblem');
        const toolRing = document.querySelector('#agcompact .hud-node[data-state="tool"] .hud-ring');
        const perm = document.querySelector('#agcompact .hud-badge.perm');
        const fail = document.querySelector('#agcompact .hud-badge.fail');
        const roots = document.querySelector('#agcompact .hud-roots');
        const kids = document.querySelector('#agcompact .tree-kids');
        const path = document.querySelector('#agcompact .hud-lines path');
        const nr = n.getBoundingClientRect(), er = e.getBoundingClientRect();
        const pr = perm.getBoundingClientRect(), fr = fail.getBoundingClientRect();
        const ns = getComputedStyle(n), rs = getComputedStyle(toolRing);
        const rts = getComputedStyle(roots), ks = getComputedStyle(kids), ps = getComputedStyle(path);
        const idleNode = document.querySelector('#agcompact .hud-node[data-state="idle"]');
        return {
          nodeW: Math.round(nr.width), nodeH: Math.round(nr.height), nodeRadius: ns.borderRadius,
          emblemW: Math.round(er.width), emblemH: Math.round(er.height),
          ringWidth: rs.paddingTop,
          permW: Math.round(pr.width), permH: Math.round(pr.height), permText: perm.textContent,
          failW: Math.round(fr.width), failH: Math.round(fr.height), failText: fail.textContent,
          rootsGap: rts.gap, kidsGap: ks.gap, kidsTop: ks.paddingTop,
          strokeWidth: ps.strokeWidth, pathD: path.getAttribute('d'),
          idleOpacity: Number(getComputedStyle(idleNode).opacity).toFixed(2),
          nodeCount: document.querySelectorAll('#agcompact .hud-node').length,
          harnesses: [...new Set([...document.querySelectorAll('#agcompact .hud-emblem')].map(x => x.getAttribute('data-h')))].sort(),
          states: [...new Set([...document.querySelectorAll('#agcompact .hud-node')].map(x => x.getAttribute('data-state')))].sort()
        };
      });
      assert.equal(geom.nodeW, 24); assert.equal(geom.nodeH, 24);
      assert.equal(geom.emblemW, 18); assert.equal(geom.emblemH, 18);
      assert.equal(geom.ringWidth, '1.5px');
      assert.equal(geom.permW, 11); assert.equal(geom.permH, 11); assert.equal(geom.permText, '!');
      assert.equal(geom.failW, 11); assert.equal(geom.failH, 11); assert.equal(geom.failText, '×');
      assert.equal(geom.rootsGap, '10px'); assert.equal(geom.kidsGap, '6px'); assert.equal(geom.kidsTop, '10px');
      assert.equal(geom.strokeWidth, '1.15px');
      assert(geom.pathD.includes('M') && geom.pathD.includes('V') && geom.pathD.includes('H'), 'orthogonal SVG connector');
      assert.equal(geom.idleOpacity, '0.42');
      assert.equal(geom.nodeCount, 12);
      assert.deepEqual(geom.harnesses, ['agy', 'claude', 'codex', 'pi']);
      assert.deepEqual(geom.states, ['failed', 'holding', 'idle', 'permission', 'thinking', 'tool']);
      const flat = await frame.evaluate(() => {
        const panel = document.getElementById('agisland'), compact = document.getElementById('agcompact');
        const tip = document.getElementById('hud-tooltip'), drawer = document.getElementById('drawer');
        return { shadow: getComputedStyle(panel).boxShadow, border: getComputedStyle(panel).borderWidth,
          background: getComputedStyle(panel).backgroundColor, footer: !!compact.querySelector('.hud-footer'),
          expand: !!compact.querySelector('.hud-expand-btn'), text: compact.textContent,
          islandZ: parseInt(getComputedStyle(panel).zIndex, 10),
          tipZ: parseInt(getComputedStyle(tip).zIndex, 10),
          drawerZ: parseInt(getComputedStyle(drawer).zIndex, 10) };
      });
      assert.equal(flat.shadow, 'none'); assert.equal(flat.border, '0px');
      assert.match(flat.background, /^color\(|^rgba\(/);
      assert.equal(flat.footer, false); assert.equal(flat.expand, false); assert(!flat.text.includes('완료'));
      assert(flat.islandZ > 4 && flat.islandZ < flat.drawerZ, `agisland z-index (${flat.islandZ}) > 4 and < drawer (${flat.drawerZ})`);
      assert(flat.tipZ > 4 && flat.tipZ < flat.drawerZ, `hud-tooltip z-index (${flat.tipZ}) > 4 and < drawer (${flat.drawerZ})`);
      if (shots) await frame.locator('#agisland').screenshot({ path: `${shots}/hud-compact-${scheme}.png` });
      await frame.click('#agcompact', { position: { x: 2, y: 2 } });
      await frame.locator('#agisland[data-mode="full"]').waitFor();
      assert.equal(await frame.getAttribute('#agcompact', 'aria-expanded'), 'true');
      if (shots && scheme === 'dark') await tab.screenshot({ path: `${shots}/hud-expanded-dark.png` });
      await frame.click('#agclose');
      await frame.locator('#agisland[data-mode="compact"]').waitFor();
      await frame.locator('#agcompact').focus();
      await frame.locator('#agcompact').press('Enter');
      await frame.locator('#agisland[data-mode="full"]').waitFor();
      await frame.click('#agclose');
      await frame.locator('#agisland[data-mode="compact"]').waitFor();
      if (shots) await tab.screenshot({ path: `${shots}/compact-${scheme}.png` });
      if (shots) await frame.locator('#agcompact').screenshot({ path: `${shots}/hud-${scheme}.png` });

      // Hover-card context row: A normal bar (linked h2), B past the ws soft
      // threshold (the same linked session after a refresh, updated in place
      // while hovered), D an unlinked hosted child with the ws thresholds on its row (h5),
      // C no scale at all (unlinked h10, tokens only).
      {
        const tip = frame.locator('#hud-tooltip');
        const row = tip.locator('.tt-ctx');
        await frame.locator('#agcompact .hud-node[data-id="h2"]').hover();
        await row.locator('.bar').waitFor();
        assert.equal(await row.getAttribute('class'), 'tt-ctx', 'A: normal colour');
        assert.equal(await row.locator('.pct').textContent(), '38%');
        assert.equal(await row.locator('.tok').textContent(), '104k / 272k');
        assert.equal(await row.locator('.m-soft').count(), 0);
        const meta = await tip.locator('.tt-meta').boundingBox(), ctxBox = await row.boundingBox();
        assert(ctxBox.y >= meta.y + meta.height - 1 && ctxBox.y < meta.y + meta.height + 12, 'row sits right under the meta row');
        assert(ctxBox.x >= (await tip.boundingBox()).x && ctxBox.x + ctxBox.width <= (await tip.boundingBox()).x + (await tip.boundingBox()).width, 'row stays inside the card');
        if (shots) await tip.screenshot({ path: `${shots}/hud-ctx-A-${scheme}.png` });
        await fetch(`${switcher}?s=heavy&usage=soft`);
        await row.locator('.warn, .tok:text("156k / 200k")').first().waitFor();
        await frame.waitForFunction(() => document.querySelector('#hud-tooltip .tt-ctx.warn'));
        assert.equal(await row.locator('.pct').textContent(), '78%');
        assert.equal(await row.locator('.m-soft').count(), 1);
        if (shots) await tip.screenshot({ path: `${shots}/hud-ctx-B-${scheme}.png` });
        // D: an unlinked hosted child, scaled by the ws thresholds on its own row.
        await frame.locator('#agcompact .hud-node[data-id="h5"]').hover();
        await frame.waitForFunction(() => document.querySelector('#hud-tooltip .tt-ctx.warn'));
        assert.equal(await row.locator('.pct').textContent(), '78%');
        assert.equal(await row.locator('.tok').textContent(), '156k / 200k');
        assert.equal(await row.locator('.m-soft').count(), 1);
        if (shots) await tip.screenshot({ path: `${shots}/hud-ctx-D-unlinked-${scheme}.png` });
        // Compaction line: D compacting now (h5, hovered above), the chip then the
        // count and origin, directly under the context row; the card updates in
        // place when the compaction ends (count-only, manual); E count only (h2).
        {
          const cmp = tip.locator('.tt-cmp');
          await cmp.waitFor();
          assert.equal(await cmp.locator('.d-now').textContent(), '압축 중', 'D: compacting chip');
          assert.equal(await cmp.locator('.cnt').textContent(), '압축 2회 · 마지막 ws 자동', 'D: count and origin');
          const ctxBox = await row.boundingBox(), cmpBox = await cmp.boundingBox(), tipBox = await tip.boundingBox();
          assert(cmpBox.y >= ctxBox.y + ctxBox.height - 1 && cmpBox.y < ctxBox.y + ctxBox.height + 14, 'line sits right under the context row');
          assert(cmpBox.x >= tipBox.x && cmpBox.x + cmpBox.width <= tipBox.x + tipBox.width + 1, 'line stays inside the card');
          assert.notEqual(await cmp.locator('.d-now').evaluate(el => getComputedStyle(el, '::before').animationName), 'none', 'the dot pulses');
          if (shots) await tip.screenshot({ path: `${shots}/hud-cmp-D-${scheme}.png` });
          await fetch(`${switcher}?s=heavy&cmp=done`);
          await frame.waitForFunction(() => !document.querySelector('#hud-tooltip .tt-cmp .d-now'));
          assert.equal(await cmp.locator('.cnt').textContent(), '압축 3회 · 마지막 수동', 'the hovered card updates in place');
          await fetch(`${switcher}?s=heavy`);
          await frame.waitForFunction(() => document.querySelector('#hud-tooltip .tt-cmp .d-now'));
          await frame.locator('#agcompact .hud-node[data-id="h2"]').hover();
          await frame.waitForFunction(() => { const c = document.querySelector('#hud-tooltip .tt-cmp'); return c && !c.querySelector('.d-now'); });
          assert.equal(await tip.locator('.tt-cmp .cnt').textContent(), '압축 3회 · 마지막 하네스', 'E: count only');
          assert.equal(await tip.locator('.tt-cmp .d-now').count(), 0);
          if (shots) await tip.screenshot({ path: `${shots}/hud-cmp-E-${scheme}.png` });
        }
        await frame.locator('#agcompact .hud-node[data-id="h10"]').hover();
        await frame.waitForFunction(() => document.querySelector('#hud-tooltip .tt-ctx.d-unknown'));
        assert.equal(await tip.locator('.tt-cmp').count(), 0, 'F: never compacted, no line');
        if (shots) await tip.screenshot({ path: `${shots}/hud-cmp-F-${scheme}.png` });
        assert.equal(await row.locator('.bar').count(), 0, 'C: no bar');
        assert.equal(await row.locator('.tok').textContent(), '컨텍스트 41.0k');
        if (shots) await tip.screenshot({ path: `${shots}/hud-ctx-C-${scheme}.png` });
        await frame.locator('#agcompact .hud-node[data-id="h7"]').hover();
        assert.equal(await tip.locator('.tt-ctx').count(), 0, 'unknown tokens: no row');
        await fetch(`${switcher}?s=heavy`);
        await frame.waitForTimeout(350);
        await tab.mouse.move(2, 400);
      }

      // Node and close controls keep their own actions in each theme.
      await frame.click('#agcompact .hud-node[data-id="h3"]');
      await frame.locator('#agisland[data-mode="full"]').waitFor();
      assert.equal(await frame.locator('#agpop details[data-key="agent-h3"]').evaluate(el => el.open), true);
      await frame.click('#agclose');
      await frame.locator('#agisland[data-mode="compact"]').waitFor();
      await frame.locator('#agcompact .hud-node[data-id="h2"]').hover();
      await frame.locator('#agcompact .node-slot:has(.hud-node[data-id="h2"]) .hud-close').click();
      const closeDialog = frame.locator('dialog.rdeldlg[open]');
      await closeDialog.waitFor();
      assert.equal(await frame.getAttribute('#agisland', 'data-mode'), 'compact');
      await closeDialog.locator('.pbtn:not(.danger)').click();

      if (process.env.WS_SUBAGENT_SHOTS) {
        const dir = process.env.WS_SUBAGENT_SHOTS;
        let closes = 0;
        await tab.route('**/agents/boardsession/close', async route => {
          closes++;
          await route.fulfill({ status: 200, body: '{}' });
        });
        const node = frame.locator('#agcompact .hud-node[data-id="h2"]');
        await node.hover();
        const close = frame.locator('#agcompact .node-slot:has(.hud-node[data-id="h2"]) .hud-close');
        await close.waitFor({ state: 'visible' });
        await frame.waitForTimeout(250);
        await tab.screenshot({ path: `${dir}/hud-hover-${scheme}.png` });
        for (const [id, state] of [['h11', 'failed'], ['h8', 'idle']]) {
          const target = frame.locator(`#agcompact .hud-node[data-id="${id}"]`);
          assert.equal(await target.getAttribute('data-state'), state);
          await target.hover();
          await frame.locator(`#agcompact .node-slot:has(.hud-node[data-id="${id}"]) .hud-close`).waitFor({ state: 'visible' });
          await frame.waitForTimeout(250);
          await tab.screenshot({ path: `${dir}/hud-${state}-hover-${scheme}.png` });
        }
        await node.hover();
        await close.click();
        const dialog = frame.locator('dialog.rdeldlg[open]');
        await dialog.waitFor();
        assert.equal(closes, 0, 'active child waits for confirmation');
        await tab.screenshot({ path: `${dir}/close-modal-${scheme}.png` });
        await dialog.locator('.pbtn:not(.danger)').click();
        assert.equal(closes, 0, 'cancel sends no request');
        await frame.click('#agcompact', { position: { x: 2, y: 2 } });
        await frame.locator('#agpop details[data-key="agent-h1"] > summary').click();
        await frame.waitForTimeout(500);
        const row = frame.locator('#agpop .agent:has(> details[data-key="agent-h2"])');
        await row.locator('> details > summary').hover();
        const rowClose = row.locator('> details > summary > .aclose');
        await rowClose.waitFor({ state: 'visible' });
        await frame.waitForTimeout(250);
        await tab.screenshot({ path: `${dir}/agpop-hover-${scheme}.png` });
        await rowClose.click();
        await dialog.waitFor();
        await dialog.locator('.pbtn.danger').click();
        await frame.waitForTimeout(150);
        assert.equal(closes, 1, 'confirmed close sends one request');
        await tab.unroute('**/agents/boardsession/close');
        if (await frame.locator('#agclose').isVisible()) await frame.locator('#agclose').click();
      }

      if (scheme === 'dark') {
        await checkKeyedRebuild(frame);
        // Tooltip clamp + own-row task from #r-tree
        await frame.locator('#agcompact .hud-node[data-id="h1"]').hover();
        await frame.locator('#hud-tooltip:not([hidden])').waitFor();
        const tt = await frame.evaluate(() => {
          const tip = document.getElementById('hud-tooltip').getBoundingClientRect();
          const convo = document.getElementById('convo').getBoundingClientRect();
          return {
            left: tip.left, right: tip.right, top: tip.top, bottom: tip.bottom,
            convoLeft: convo.left, convoRight: convo.right, convoTop: convo.top, convoBottom: convo.bottom,
            task: document.querySelector('#hud-tooltip .tt-task')?.textContent || '',
            hint: document.querySelector('#hud-tooltip .tt-hint')?.textContent || '',
            chain: document.querySelector('#hud-tooltip .tt-chain')?.textContent || ''
          };
        });
        assert(tt.left >= tt.convoLeft - 1 && tt.right <= tt.convoRight + 1, 'tooltip horizontally clamped inside #convo');
        assert(tt.top >= tt.convoTop - 1 && tt.bottom <= tt.convoBottom + 1, 'tooltip vertically clamped inside #convo');
        assert.equal(tt.task, 'Waiting on ast-scanner', 'own-row task read from #r-tree');
        assert.match(tt.hint, /트리 상세 열기/);
        if (shots) await tab.screenshot({ path: `${shots}/tooltip-dark.png` });

        // Unlinked node click -> opens #agpop & focuses details summary
        await frame.click('#agcompact .hud-node[data-id="h3"]');
        await frame.waitForTimeout(250);
        const popFocus = await frame.evaluate(() => ({
          mode: document.getElementById('agisland').getAttribute('data-mode'),
          open: document.querySelector('#agpop details[data-key="agent-h3"]')?.open,
          activeKey: document.activeElement?.closest('details')?.getAttribute('data-key')
        }));
        assert.deepEqual(popFocus, { mode: 'full', open: true, activeKey: 'agent-h3' });
        if (shots) await tab.screenshot({ path: `${shots}/full-dark.png` });
        await tab.keyboard.press('Escape');
        await frame.waitForTimeout(250);

        // Linked node click -> navigates to /s/<replyId>
        await frame.locator('#agcompact .hud-node[data-id="h2"]').hover();
        const linkedHint = await frame.textContent('#hud-tooltip .tt-hint');
        assert.match(linkedHint, /\/s\/boardsession/);
        await frame.click('#agcompact .hud-node[data-id="h2"]');
        await tab.waitForFunction(() => location.pathname.endsWith('/s/boardsession'));
      }
      assert.deepEqual(errors, [], `no pageerrors in ${scheme}`);
      await ctx.close();
    }

    // 2. Depth-4 cap +2 overflow chip & narrow 420px spacing
    {
      const { ctx, tab, frame, errors } = await openSession(b, 'depth', { width: 420, scheme: 'dark' });
      const narrow = await frame.evaluate(() => {
        const roots = document.querySelector('#agcompact .hud-roots');
        const kids = document.querySelector('#agcompact .tree-kids');
        const ov = document.querySelector('#agcompact .hud-overflow');
        const orect = ov.getBoundingClientRect();
        return {
          rootsGap: getComputedStyle(roots).gap,
          kidsGap: getComputedStyle(kids).gap,
          kidsTop: getComputedStyle(kids).paddingTop,
          overflowText: ov.textContent,
          overflowH: Math.round(orect.height),
          nodeCount: document.querySelectorAll('#agcompact .hud-node').length
        };
      });
      assert.deepEqual(narrow, { rootsGap: '7px', kidsGap: '5px', kidsTop: '9px', overflowText: '+2', overflowH: 12, nodeCount: 4 });
      if (shots) await tab.screenshot({ path: `${shots}/narrow-depth4-dark.png` });
      assert.deepEqual(errors, []);
      await ctx.close();
    }

    // 3. Wide fan-out (14 direct) horizontal scroll inside #agisland
    {
      const { ctx, tab, frame, errors } = await openSession(b, 'fanout', { width: 420, scheme: 'dark' });
      const fan = await frame.evaluate(() => {
        const island = document.getElementById('agisland').getBoundingClientRect();
        const convo = document.getElementById('convo').getBoundingClientRect();
        const sc = document.querySelector('#agcompact .hud-tree-scroll');
        return {
          nodes: document.querySelectorAll('#agcompact .hud-node').length,
          fitsConvo: island.width <= convo.width - 15,
          scrolls: sc.scrollWidth > sc.clientWidth
        };
      });
      assert.deepEqual(fan, { nodes: 14, fitsConvo: true, scrolls: true });
      if (shots) await tab.screenshot({ path: `${shots}/fanout-dark.png` });
      assert.deepEqual(errors, []);
      await ctx.close();
    }

    // 4. Idle pill when all agents are asleep/closed
    for (const scheme of ['dark', 'light']) {
      await fetch(`${switcher}?s=idle`);
      const ctx = await b.newContext({ viewport: { width: 1280, height: 840 }, colorScheme: 'dark', locale: 'ko-KR' });
      await ctx.addCookies([{ name: 'ws_dashboard_lang', value: 'ko', url: base }]);
      const tab = await ctx.newPage();
      await tab.goto(`${base}/s/livesession`);
      const frame = await (await tab.locator('iframe.sframe.on').elementHandle()).contentFrame();
      await frame.locator('#agisland[data-mode="idle"]:not([hidden])').waitFor();
      await frame.waitForTimeout(250);
      await chooseTheme(tab, frame, scheme);
      const idleStyle = await frame.evaluate(() => ({ shadow: getComputedStyle(document.getElementById('agisland')).boxShadow,
        border: getComputedStyle(document.getElementById('agisland')).borderWidth, text: document.getElementById('agidle').textContent }));
      assert.equal(idleStyle.shadow, 'none'); assert.equal(idleStyle.border, '0px'); assert(!idleStyle.text.includes('완료'));
      if (shots) await frame.locator('#agisland').screenshot({ path: `${shots}/hud-idle-${scheme}.png` });
      await frame.click('#agidle');
      await frame.locator('#agisland[data-mode="full"]').waitFor();
      if (shots) await tab.screenshot({ path: `${shots}/idle-dark.png` });
      await ctx.close();
    }

    // 5. Single-agent compact HUD left-side hover expansion, pixel-locked icon, SSE jitter regression, & left click
    for (const scheme of ['dark', 'light']) {
      const { ctx, tab, frame, errors } = await openSession(b, 'single', { width: 1280, scheme: scheme === 'dark' ? 'light' : 'dark' });
      await chooseTheme(tab, frame, scheme);
      await tab.mouse.move(1, 1);
      await frame.waitForTimeout(250);
      const rest = await frame.evaluate(() => {
        const panel = document.getElementById('agisland'), compact = document.getElementById('agcompact');
        const slot = compact.querySelector('.node-slot'), node = compact.querySelector('.hud-node'), convo = document.getElementById('convo');
        const pr = panel.getBoundingClientRect(), cr = compact.getBoundingClientRect(), sr = slot.getBoundingClientRect(), nr = node.getBoundingClientRect(), vr = convo.getBoundingClientRect();
        return {
          nodes: compact.querySelectorAll('.hud-node').length,
          mode: panel.getAttribute('data-mode'),
          shadow: getComputedStyle(panel).boxShadow,
          border: getComputedStyle(panel).borderWidth,
          panelL: pr.left, panelW: pr.width, panelH: pr.height, panelR: pr.right,
          compactL: cr.left, compactW: cr.width, compactH: cr.height, compactR: cr.right,
          slotX: sr.x, slotY: sr.y, slotW: sr.width, slotH: sr.height,
          nodeX: nr.x, nodeY: nr.y, nodeW: nr.width, nodeH: nr.height, nodeL: nr.left, nodeR: nr.right,
          convoR: vr.right
        };
      });
      assert.equal(rest.nodes, 1);
      assert.equal(rest.mode, 'compact');
      assert.equal(rest.shadow, 'none');
      assert.equal(rest.border, '0px');
      const convoBox = await frame.locator('#convo').boundingBox();
      const shotClip = {
        x: Math.max(0, Math.round(convoBox.x + convoBox.width - 320)),
        y: Math.max(0, Math.round(convoBox.y - 8)),
        width: 320,
        height: 160
      };
      if (shots) await tab.screenshot({ path: `${shots}/hud-1agent-rest-${scheme}.png`, clip: shotClip });

      // Hover the compact HUD on its left edge so #agcompact expands leftward without hovering .hud-node directly
      await frame.locator('#agcompact').hover({ position: { x: 4, y: Math.round(rest.compactH / 2) } });
      await frame.waitForTimeout(250);
      const hovered = await frame.evaluate(() => {
        const panel = document.getElementById('agisland'), compact = document.getElementById('agcompact');
        const slot = compact.querySelector('.node-slot'), node = compact.querySelector('.hud-node'), convo = document.getElementById('convo');
        const pr = panel.getBoundingClientRect(), cr = compact.getBoundingClientRect(), sr = slot.getBoundingClientRect(), nr = node.getBoundingClientRect(), vr = convo.getBoundingClientRect();
        const before = getComputedStyle(compact, '::before');
        return {
          isHovered: compact.matches(':hover'),
          panelL: pr.left, panelW: pr.width, panelH: pr.height, panelR: pr.right,
          compactL: cr.left, compactW: cr.width, compactH: cr.height, compactR: cr.right,
          nodeX: nr.x, nodeY: nr.y, nodeW: nr.width, nodeH: nr.height, nodeL: nr.left, nodeR: nr.right,
          slotX: sr.x, slotY: sr.y, slotW: sr.width, slotH: sr.height,
          convoR: vr.right,
          beforeOpacity: parseFloat(before.opacity),
          beforeLeft: parseFloat(before.left)
        };
      });
      assert.equal(hovered.isHovered, true, `single-agent HUD stays hovered in ${scheme}`);
      assert(hovered.compactW >= rest.compactW + 24, `compact width grows on hover (${rest.compactW} -> ${hovered.compactW})`);
      assert(hovered.panelW >= rest.panelW + 24, `island width grows on hover (${rest.panelW} -> ${hovered.panelW})`);
      assert(hovered.panelL <= rest.panelL - 24, `island expands leftward (${rest.panelL} -> ${hovered.panelL})`);
      assert(Math.abs(hovered.compactR - rest.compactR) <= 1, 'right edge stays anchored');
      assert(Math.abs(hovered.compactH - rest.compactH) <= 0.5, `compact height unchanged (${rest.compactH} -> ${hovered.compactH})`);
      assert(hovered.panelR <= hovered.convoR + 1, 'no horizontal overflow past #convo');
      assert(hovered.nodeL - hovered.compactL >= 28, `generous empty left click-zone created (${hovered.nodeL - hovered.compactL}px)`);
      assert(hovered.beforeOpacity >= 0.4, `subtle left chevron cue fades in (opacity=${hovered.beforeOpacity})`);
      assert(hovered.beforeLeft >= 0 && hovered.beforeLeft < 24, `chevron cue sits on the left (${hovered.beforeLeft}px)`);
      assert(Math.abs(hovered.nodeX - rest.nodeX) <= 0.5, `node X locked on hover (${rest.nodeX} -> ${hovered.nodeX})`);
      assert(Math.abs(hovered.nodeY - rest.nodeY) <= 0.5, `node Y locked on hover (${rest.nodeY} -> ${hovered.nodeY})`);
      assert(Math.abs(hovered.nodeW - rest.nodeW) <= 0.5, `node width unchanged (${rest.nodeW} -> ${hovered.nodeW})`);
      assert(Math.abs(hovered.nodeH - rest.nodeH) <= 0.5, `node height unchanged (${rest.nodeH} -> ${hovered.nodeH})`);
      if (shots) await tab.screenshot({ path: `${shots}/hud-1agent-hover-${scheme}.png`, clip: shotClip });

      // Hovering directly on .hud-node also keeps node X/W/H and .node-slot pixel-locked
      await frame.locator('#agcompact .hud-node').hover();
      await frame.waitForTimeout(250);
      const nodeHovered = await frame.evaluate(() => {
        const panel = document.getElementById('agisland'), slot = document.querySelector('#agcompact .node-slot'), node = document.querySelector('#agcompact .hud-node');
        const pr = panel.getBoundingClientRect(), sr = slot.getBoundingClientRect(), nr = node.getBoundingClientRect();
        return { panelW: pr.width, slotX: sr.x, slotY: sr.y, nodeX: nr.x, nodeW: nr.width, nodeH: nr.height };
      });
      assert(nodeHovered.panelW >= rest.panelW + 24, 'hovering .hud-node keeps HUD expanded');
      assert(Math.abs(nodeHovered.nodeX - rest.nodeX) <= 0.5, `node X locked when directly hovered (${rest.nodeX} -> ${nodeHovered.nodeX})`);
      assert(Math.abs(nodeHovered.slotX - rest.slotX) <= 0.5 && Math.abs(nodeHovered.slotY - rest.slotY) <= 0.5, 'node-slot locked when directly hovered');

      // Critical SSE jitter regression test: keep hovering while 5 live SSE tree updates arrive over ~300ms
      await frame.locator('#agcompact').hover({ position: { x: 4, y: Math.round(rest.compactH / 2) } });
      await frame.waitForTimeout(50);
      await frame.evaluate(() => {
        window.__hudJitterSamples = [];
        window.__hudJitterStop = false;
        const panel = document.getElementById('agisland'), node = document.querySelector('#agcompact .hud-node');
        function sample() {
          const pr = panel.getBoundingClientRect(), nr = node.getBoundingClientRect();
          window.__hudJitterSamples.push({
            w: pr.width, l: pr.left, nx: nr.x, ny: nr.y,
            sizing: panel.hasAttribute('data-sizing')
          });
          if (!window.__hudJitterStop) requestAnimationFrame(sample);
        }
        requestAnimationFrame(sample);
      });
      for (let i = 1; i <= 5; i++) {
        const cap = `sse-jitter-${scheme}-${i}`;
        await fetch(`${switcher}?s=single&cap=${cap}`);
        await frame.waitForFunction(c => document.getElementById('r-tree').textContent.includes(c), cap);
        await frame.waitForTimeout(60);
      }
      const jitter = await frame.evaluate(() => {
        window.__hudJitterStop = true;
        return window.__hudJitterSamples;
      });
      assert(jitter.length >= 8, `captured enough animation frames during SSE updates (${jitter.length})`);
      assert(jitter.every(s => !s.sizing), 'data-sizing attribute is never set during live SSE updates');
      const widths = jitter.map(s => s.w), nodeXs = jitter.map(s => s.nx), nodeYs = jitter.map(s => s.ny);
      const minW = Math.min(...widths), maxW = Math.max(...widths);
      const maxNodeDX = Math.max(...nodeXs.map(x => Math.abs(x - rest.nodeX)));
      const maxNodeDY = Math.max(...nodeYs.map(y => Math.abs(y - rest.nodeY)));
      assert(minW >= rest.panelW + 24, `width never snaps back toward rest during SSE updates (minW=${minW}, restW=${rest.panelW})`);
      assert(maxW - minW <= 1, `hover width remains stable across SSE updates (minW=${minW}, maxW=${maxW})`);
      assert(maxNodeDX <= 0.5 && maxNodeDY <= 0.5, `node stays pixel-locked across SSE updates (dx=${maxNodeDX}, dy=${maxNodeDY})`);

      // Click inside the newly expanded LEFT empty space (outside .hud-node) -> opens #agpop
      const compactBox = await frame.locator('#agcompact').boundingBox();
      const nodeBox = await frame.locator('#agcompact .hud-node').boundingBox();
      const clickX = (compactBox.x + nodeBox.x) / 2;
      const clickY = compactBox.y + compactBox.height / 2;
      assert(clickX < nodeBox.x - 4, `click target (${clickX}) is well left of .hud-node (${nodeBox.x})`);
      await tab.mouse.click(clickX, clickY);
      await frame.locator('#agisland[data-mode="full"]').waitFor();
      assert.equal(await frame.getAttribute('#agcompact', 'aria-expanded'), 'true');
      assert.deepEqual(errors, [], `no pageerrors in single-agent ${scheme}`);
      await ctx.close();
    }

    // 5b. Folded alive-idle direct children: active nodes + single +8 badge, hover tooltip, click -> #agpop
    const foldShots = process.env.WS_HUD_FOLD_SHOTS;
    if (foldShots) require('node:fs').mkdirSync(foldShots, { recursive: true });
    for (const scheme of ['dark', 'light']) {
      const { ctx, tab, frame, errors } = await openSession(b, 'folded', { width: 1280, scheme: scheme === 'dark' ? 'light' : 'dark' });
      await chooseTheme(tab, frame, scheme);
      await tab.mouse.move(1, 1);
      await frame.waitForTimeout(250);
      const foldState = await frame.evaluate(() => {
        const nodes = [...document.querySelectorAll('#agcompact .hud-node')];
        const idleDirect = nodes.filter(n => n.getAttribute('data-state') === 'idle');
        const folded = document.querySelectorAll('#agcompact .hud-folded');
        const btn = folded[0];
        const nr = nodes[0].getBoundingClientRect();
        const fr = btn.getBoundingClientRect();
        return {
          mode: document.getElementById('agisland').getAttribute('data-mode'),
          nodeCount: nodes.length,
          idleDirectCount: idleDirect.length,
          foldedCount: folded.length,
          foldedText: btn.textContent,
          foldedHasNodeClass: btn.classList.contains('hud-node'),
          foldedH: Math.round(fr.height),
          topDelta: Math.abs(fr.top - nr.top),
          popAgents: document.querySelectorAll('#agpop .agent:not(.root)').length
        };
      });
      assert.equal(foldState.mode, 'compact');
      assert.equal(foldState.nodeCount, 2, 'only 2 active direct .hud-node elements');
      assert.equal(foldState.idleDirectCount, 0, '0 stray idle direct .hud-node elements');
      assert.equal(foldState.foldedCount, 1, 'single .hud-folded badge');
      assert.equal(foldState.foldedText, '+8');
      assert.equal(foldState.foldedHasNodeClass, false);
      assert.equal(foldState.foldedH, 24);
      assert(foldState.topDelta <= 1, `folded badge aligned with 24px nodes (topDelta=${foldState.topDelta})`);
      assert.equal(foldState.popAgents, 10, '#agpop still lists all 10 agents');

      const convoBox = await frame.locator('#convo').boundingBox();
      const foldClip = {
        x: Math.max(0, Math.round(convoBox.x + convoBox.width - 360)),
        y: Math.max(0, Math.round(convoBox.y)),
        width: 360,
        height: 200
      };
      if (foldShots) await tab.screenshot({ path: `${foldShots}/hud-folded-${scheme}.png`, clip: foldClip });

      await frame.locator('#agcompact .hud-folded').hover();
      await frame.locator('#hud-tooltip:not([hidden])').waitFor();
      await frame.waitForTimeout(200);
      const foldTip = await frame.evaluate(() => {
        const tip = document.getElementById('hud-tooltip').getBoundingClientRect();
        const convo = document.getElementById('convo').getBoundingClientRect();
        return {
          pill: document.querySelector('#hud-tooltip .tt-pill')?.textContent || '',
          title: document.querySelector('#hud-tooltip .tt-title')?.textContent || '',
          hint: document.querySelector('#hud-tooltip .tt-hint')?.textContent || '',
          insideConvo: tip.left >= convo.left - 1 && tip.right <= convo.right + 1 && tip.top >= convo.top - 1 && tip.bottom <= convo.bottom + 1
        };
      });
      assert.equal(foldTip.pill, '+8');
      assert(foldTip.title.includes('8'), `tooltip title includes count: ${foldTip.title}`);
      assert.match(foldTip.hint, /트리 상세 열기/);
      assert.equal(foldTip.insideConvo, true, 'folded tooltip clamped inside #convo');
      if (foldShots) await tab.screenshot({ path: `${foldShots}/hud-folded-hover-${scheme}.png`, clip: foldClip });

      await frame.click('#agcompact .hud-folded');
      await frame.locator('#agisland[data-mode="full"]').waitFor();
      assert.equal(await frame.locator('#hud-tooltip').isHidden(), true, 'clicking .hud-folded hides tooltip');
      assert.deepEqual(errors, [], `no pageerrors in folded ${scheme}`);
      await ctx.close();
    }

    // 6. Optional side-by-side mockup capture
    if (shots && mockup) {
      const ctx = await b.newContext({ viewport: { width: 1280, height: 840 }, colorScheme: 'dark' });
      const tab = await ctx.newPage();
      await tab.goto(`file://${mockup}?theme=dark&scenario=heavy&hover=s-101`);
      await tab.waitForTimeout(300);
      await tab.screenshot({ path: `${shots}/mockup-v2-dark.png` });
      await ctx.close();
    }
    console.log('hud browser acceptance ok');
  } finally {
    await b.close();
  }
})().catch(e => { console.error(e); process.exit(1); });
