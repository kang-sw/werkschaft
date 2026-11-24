// Run by TestTierRoutingUIBrowser against the production settings backend:
// the graph view, previews, and saves are the daemon's own. The new-session
// popover's /support/choices and /agents routes are answered here (no runner
// is part of the fixture); its named tiers are the bundles the daemon's
// graph view lists after the edits, resolved as the daemon reports them.
// argv: <login link> <shots dir|"">
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const [login, shots] = process.argv.slice(2);
const shot = name => shots ? path.join(shots, name) : undefined;

(async () => {
  if (shots) fs.mkdirSync(shots, { recursive: true });
  const browser = await chromium.launch({ headless: true });
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1800 }, locale: 'en-US', colorScheme: 'light', serviceWorkers: 'block' });
    const page = await context.newPage();
    page.setDefaultTimeout(8000);
    const errors = [];
    page.on('pageerror', e => { if (!/serviceWorker/.test(e.message)) errors.push(e.stack || e.message); });
    await page.goto(login);
    await page.waitForURL(url => url.pathname === '/settings' && !url.searchParams.has('login'));
    const origin = new URL(page.url()).origin;
    const frame = page.frameLocator('#workflow-settings[open] > iframe');
    const card = frame.locator('[data-tier-routing]');
    const view = async harness => (await page.request.get(origin + '/api/config/tier-routing?harness=' + (harness || 'claude'))).json();
    const nodeOf = (v, name) => v.nodes.find(n => n.name === name);
    const routeOf = (v, name, size) => nodeOf(v, name).routes.find(r => r.size === size);
    const responded = suffix => page.waitForResponse(r => new URL(r.url()).pathname === suffix);
    async function openSettings() {
      await frame.locator('[data-settings-section=agents]').click();
      await card.locator('.tr-node').first().waitFor();
      await page.waitForTimeout(300);
    }
    async function openNode(name) {
      const shown = responded('/api/config/tier-routing/preview');
      await card.locator(`[data-tier-node="${name}"]`).click();
      const modal = frame.locator('dialog[data-tier-modal=node]');
      await modal.waitFor();
      await shown;
      await page.waitForTimeout(80);
      return modal;
    }
    // edit runs fn and waits for the daemon's preview of the draft it makes.
    async function edit(fn) {
      const shown = responded('/api/config/tier-routing/preview');
      await fn();
      await shown;
      await page.waitForTimeout(80);
    }
    // save clicks the modal's Save and waits for the write and the reload.
    async function save(modal, button = '.tr-save') {
      const wrote = page.waitForResponse(r => /\/api\/config\/(set|reset)$/.test(new URL(r.url()).pathname));
      const reread = page.waitForResponse(r => new URL(r.url()).pathname === '/api/config/tier-routing' && r.request().method() === 'GET');
      await modal.locator(button).click();
      assert.equal((await wrote).status(), 200);
      await reread;
      await modal.waitFor({ state: 'detached' });
      await page.waitForTimeout(150);
    }
    const traceRow = (modal, size) => modal.locator('.tr-trace-row').nth(['small', 'medium', 'large', 'xlarge'].indexOf(size));
    const chips = loc => loc.locator('code').allTextContents();

    await openSettings();

    // 0. Constrained viewport (1280x800, modal height ~768px): .tr-card must not
    // collapse or clip vertically, and .scontent must scroll cleanly.
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.waitForTimeout(100);
    const modalMetrics = await card.evaluate(el => {
      const scontent = el.closest('.scontent');
      const trFrame = el.querySelector('.tr-frame');
      return {
        cardScrollHeight: el.scrollHeight,
        cardClientHeight: el.clientHeight,
        frameHeight: trFrame ? trFrame.getBoundingClientRect().height : 0,
        scontentScrollHeight: scontent ? scontent.scrollHeight : 0,
        scontentClientHeight: scontent ? scontent.clientHeight : 0
      };
    });
    assert.ok(modalMetrics.cardScrollHeight <= modalMetrics.cardClientHeight + 2, `tr-card clipped at 1280x800: ${JSON.stringify(modalMetrics)}`);
    assert.ok(modalMetrics.frameHeight >= 120, `tr-frame collapsed at 1280x800: ${JSON.stringify(modalMetrics)}`);
    assert.ok(modalMetrics.scontentScrollHeight > modalMetrics.scontentClientHeight, `scontent not scrollable at 1280x800: ${JSON.stringify(modalMetrics)}`);
    await page.setViewportSize({ width: 1440, height: 1800 });
    await page.waitForTimeout(100);

    // 1. The graph is the daemon's, global, and shows the caller fallthrough.
    let v = await view();
    assert.match(await card.locator('[data-tier-scope=global]').textContent(), /Global/);
    assert.equal(v.harness, 'claude');
    assert.equal(await card.locator('.tr-node').count(), v.nodes.length, 'one card per daemon node');
    const edgeCount = v.nodes.reduce((n, node) => n + node.edges.length, 0);
    assert.equal(await card.locator('path.tr-edge').count(), edgeCount, 'one path per daemon edge');
    assert.equal(await card.locator('path.tr-edge.fall').count(), v.nodes.filter(n => n.edges.some(e => e.kind === 'caller')).length);
    await card.locator('[data-tier-caller-terminal=claude]').waitFor();
    assert.match(await card.locator('[data-tier-node=implement] [data-tier-fallthrough=claude]').textContent(), /falls through/);
    assert.equal(routeOf(v, 'implement', 'large').trace, 'implement:large -> large -> claude.large');
    assert.equal(routeOf(v, 'implement', 'large').caller_fallthrough, true);
    await card.locator('[data-tier-node=ticket-worker]').hover();
    const workerRoute = routeOf(v, 'ticket-worker', 'medium');
    const traceChips = await chips(card.locator('.tr-trace'));
    assert.deepEqual(traceChips.map(c => c.split(' · ')[0]), [workerRoute.token, ...workerRoute.hops.map(h => h.to)]);
    assert.match(traceChips.at(-1), /claude\.medium · caller/);
    assert.match(traceChips.at(-2), /medium · bare size/);
    // agy cannot spawn in this daemon: the bundle routed to it, the node
    // whose route ends there, and its trace say so, and carry no binding.
    assert.equal(v.unavailable.agy, 'harness "agy" is not installed');
    const triage = routeOf(v, 'ticket-selector', 'small');
    assert.equal(triage.unavailable, v.unavailable.agy);
    assert.equal(triage.model, undefined);
    assert.match(await card.locator('[data-id="b:fast-triage"] [data-tier-unavailable=agy]').textContent(), /"agy" is not installed/);
    assert.equal(await card.locator('[data-tier-node=ticket-selector]').getAttribute('data-tier-unavailable'), 'agy');
    assert.equal(await card.locator('[data-tier-node=ticket-worker]').getAttribute('data-tier-unavailable'), null);
    await card.locator('[data-tier-node=ticket-selector]').hover();
    assert.match(await card.locator('.tr-trace [data-tier-unavailable=agy]').textContent(), /"agy" is not installed/);
    await card.screenshot({ path: shot('graph.png') });
    console.log('graph: daemon nodes and edges drawn; implement falls through to claude.large via the caller');

    // 2. The caller harness picks the fallthrough terminal.
    let reread = responded('/api/config/tier-routing');
    await card.locator('[data-tier-caller=codex]').click();
    await reread;
    await card.locator('[data-tier-caller-terminal=codex]').waitFor();
    assert.match(await card.locator('[data-tier-node=implement] [data-tier-fallthrough]').textContent(), /codex/);
    await card.locator('[data-tier-node=ticket-worker]').hover();
    assert.match((await chips(card.locator('.tr-trace'))).at(-1), /codex\.medium · caller/);
    await card.screenshot({ path: shot('graph-caller-codex.png') });
    reread = responded('/api/config/tier-routing');
    await card.locator('[data-tier-caller=claude]').click();
    await reread;
    await card.locator('[data-tier-caller-terminal=claude]').waitFor();
    console.log('caller: codex moves the fallthrough terminal and the trace');

    // 3. Pointer edits: passthrough, pinning, and an exception's precedence.
    let modal = await openNode('ticket-reviewer-design');
    await modal.screenshot({ path: shot('modal.png') });
    await page.screenshot({ path: shot('modal-page.png') });
    const primary = modal.locator('.tr-prow').first();
    await edit(() => primary.locator('.tr-target').selectOption('harness:codex'));
    assert.match((await chips(traceRow(modal, 'large'))).at(-1), /^codex\.large$/, 'a * size passes the requested size');
    await edit(() => primary.locator('.tr-sizemode').selectOption('small'));
    assert.match((await chips(traceRow(modal, 'large'))).at(-1), /^codex\.small$/, 'a pinned size replaces it');
    await edit(() => modal.locator('.tr-exc-add').click());
    const exc = modal.locator('.tr-exc-row').first();
    await edit(() => exc.locator('.tr-exc-size').selectOption('large'));
    await edit(() => exc.locator('.tr-target').selectOption('bundle:deep-design'));
    assert.match((await chips(traceRow(modal, 'large'))).at(-1), /^deep-design$/, 'the exact-size exception wins');
    assert.match((await chips(traceRow(modal, 'medium'))).at(-1), /^codex\.small$/);
    await modal.screenshot({ path: shot('modal-edit.png') });
    await save(modal);
    v = await view();
    assert.deepEqual(nodeOf(v, 'ticket-reviewer-design').pointers, { '*': 'codex.small', large: 'deep-design' });
    assert.equal(routeOf(v, 'ticket-reviewer-design', 'large').trace, 'ticket-reviewer-design:large -> deep-design');
    assert.equal(routeOf(v, 'ticket-reviewer-design', 'medium').trace, 'ticket-reviewer-design:medium -> codex.small');
    console.log('edit: passthrough, pinning, and exception precedence saved and resolved by the daemon');

    // 4. Removing the exception drops its precedence; reset returns the node
    // to its packaged default.
    modal = await openNode('ticket-reviewer-design');
    await edit(() => modal.locator('.tr-exc-row').first().locator('.tr-exc-remove').click());
    assert.equal(await modal.locator('.tr-exc-row').count(), 0);
    assert.match((await chips(traceRow(modal, 'large'))).at(-1), /^codex\.small$/, 'without the exception large follows *');
    await save(modal, '.tr-reset-node');
    v = await view();
    assert.equal(nodeOf(v, 'ticket-reviewer-design').overrides, undefined);
    assert.deepEqual(nodeOf(v, 'ticket-reviewer-design').pointers, { '*': 'review:*' });
    console.log('reset: ticket-reviewer-design points to review:* again');

    // 5. A cycle shows inline and cannot be saved; the stored graph stays.
    modal = await openNode('review');
    await edit(() => modal.locator('.tr-prow').first().locator('.tr-target').selectOption('node:ticket-reviewer-design'));
    assert.match(await modal.locator('.tr-modal-err').textContent(), /pointer cycle/);
    assert.equal(await modal.locator('.tr-save').isDisabled(), true);
    await modal.screenshot({ path: shot('modal-cycle.png') });
    await modal.locator('.tr-modal-x').click();
    v = await view();
    assert.equal(nodeOf(v, 'review').pointers['*'], 'codex.*', 'the last saved graph stays in force');
    console.log('cycle: refused inline, review still points to codex.*');

    // 6. A ninth hop shows inline and cannot be saved.
    const chain = JSON.parse(JSON.stringify(v.routing));
    for (let i = 1; i <= 6; i++) chain.nodes['h' + i] = { '*': 'h' + (i + 1) + ':*' };
    chain.nodes.h7 = {};
    const seeded = await page.request.post(origin + '/api/config/set', { headers: { Origin: origin }, data: { scope: 'global', key: 'agent.tier_routing', value: chain, revision: v.revision } });
    assert.equal(seeded.status(), 200, await seeded.text());
    await page.reload();
    await openSettings();
    let shown;
    await card.locator('.tr-add-node').click();
    modal = frame.locator('dialog[data-tier-modal=node]');
    await modal.waitFor();
    await edit(() => modal.locator('.tr-name-input').fill('h0'));
    await edit(() => modal.locator('.tr-prow').first().locator('.tr-target').selectOption('node:h1'));
    assert.match(await modal.locator('.tr-modal-err').textContent(), /more than 8 hops/);
    assert.equal(await modal.locator('.tr-save').isDisabled(), true);
    await modal.screenshot({ path: shot('modal-hop-limit.png') });
    await modal.locator('.tr-modal-x').click();
    v = await view();
    assert.equal(nodeOf(v, 'h0'), undefined);
    console.log('hops: a ninth hop is refused inline and nothing is stored');

    // 7. A referenced bundle cannot be deleted until its references move.
    shown = responded('/api/config/tier-routing/preview');
    await card.locator('[data-id="b:fast-triage"]').click();
    modal = frame.locator('dialog[data-tier-modal=bundle]');
    await modal.waitFor();
    await shown;
    await modal.locator('.tr-delete').click();
    assert.match(await modal.locator('.tr-modal-err').textContent(), /Still referenced by ticket-selector · \*/);
    await modal.screenshot({ path: shot('modal-delete-refused.png') });
    await modal.locator('.tr-modal-x').click();
    assert.ok((await view()).bundles.some(b => b.name === 'fast-triage'));
    modal = await openNode('ticket-selector');
    await save(modal, '.tr-reset-node');
    shown = responded('/api/config/tier-routing/preview');
    await card.locator('[data-id="b:fast-triage"]').click();
    modal = frame.locator('dialog[data-tier-modal=bundle]');
    await modal.waitFor();
    await shown;
    await save(modal, '.tr-delete');
    v = await view();
    assert.equal(v.bundles.some(b => b.name === 'fast-triage'), false);
    // A referenced user node is refused the same way.
    modal = await openNode('h7');
    await modal.locator('.tr-delete').click();
    assert.match(await modal.locator('.tr-modal-err').textContent(), /Still referenced by h6 · \*/);
    await modal.locator('.tr-modal-x').click();
    console.log('delete: refused while referenced, done once the reference moved');

    // 8. Both kinds of user node: a bundle and a pointer node.
    await card.locator('.tr-add-bundle').click();
    modal = frame.locator('dialog[data-tier-modal=bundle]');
    await modal.waitFor();
    await edit(() => modal.locator('.tr-name-input').fill('discussion'));
    await edit(() => modal.locator('.tr-bundle-harness').selectOption('codex'));
    await edit(() => modal.locator('.tr-bundle-model').fill('gpt-5.5'));
    await edit(() => modal.locator('.tr-bundle-effort').fill('high'));
    await modal.screenshot({ path: shot('modal-new-bundle.png') });
    await save(modal);
    await card.locator('.tr-add-node').click();
    modal = frame.locator('dialog[data-tier-modal=node]');
    await modal.waitFor();
    await edit(() => modal.locator('.tr-name-input').fill('lead-review'));
    await edit(() => modal.locator('.tr-prow').first().locator('.tr-target').selectOption('bundle:discussion'));
    await save(modal);
    v = await view();
    assert.deepEqual(v.bundles.find(b => b.name === 'discussion'), { name: 'discussion', harness: 'codex', model: 'gpt-5.5', effort: 'high', inbound: ['lead-review *'],
      route: v.bundles.find(b => b.name === 'discussion').route });
    assert.equal(nodeOf(v, 'lead-review').kind, 'user');
    assert.equal(routeOf(v, 'lead-review', 'small').trace, 'lead-review:small -> discussion');
    console.log('create: bundle discussion and node lead-review saved');

    // 8b. An unreferenced user node can be deleted.
    await card.locator('.tr-add-node').click();
    modal = frame.locator('dialog[data-tier-modal=node]');
    await modal.waitFor();
    await edit(() => modal.locator('.tr-name-input').fill('scratch'));
    await edit(() => modal.locator('.tr-prow').first().locator('.tr-target').selectOption('harness:codex'));
    await save(modal);
    assert.ok(nodeOf(await view(), 'scratch'));
    modal = await openNode('scratch');
    await save(modal, '.tr-delete');
    assert.equal(nodeOf(await view(), 'scratch'), undefined);
    console.log('delete: an unreferenced user node is removed');

    // 8c. A late preview of an older draft never revives Save for a newer
    // draft the editor refuses before asking the daemon.
    const previewPath = '**/api/config/tier-routing/preview';
    let release;
    const held = new Promise(resolve => { release = resolve; });
    let holding = true;
    await page.route(previewPath, async route => {
      if (holding) { holding = false; await held; }
      await route.continue();
    });
    await card.locator('.tr-add-bundle').click();
    modal = frame.locator('dialog[data-tier-modal=bundle]');
    await modal.waitFor();
    await modal.locator('.tr-bundle-harness').selectOption('codex');
    await modal.locator('.tr-bundle-model').fill('gpt-5.5');
    await modal.locator('.tr-bundle-effort').fill('high');
    const sent = page.waitForRequest(r => new URL(r.url()).pathname === '/api/config/tier-routing/preview');
    await modal.locator('.tr-name-input').fill('late');
    await sent;
    await modal.locator('.tr-name-input').fill('');
    await page.waitForTimeout(300);
    assert.match(await modal.locator('.tr-modal-err').textContent(), /Enter a name/);
    const late = responded('/api/config/tier-routing/preview');
    release();
    await late;
    await page.waitForTimeout(150);
    assert.match(await modal.locator('.tr-modal-err').textContent(), /Enter a name/, 'the late response does not clear the refusal');
    assert.equal(await modal.locator('.tr-save').isDisabled(), true, 'the late response does not enable Save');
    await page.unroute(previewPath);
    await modal.locator('.tr-modal-x').click();
    assert.equal((await view()).bundles.some(b => b.name === 'late'), false);
    console.log('stale: a late preview of an older draft is dropped');

    // 8d. A save built from a graph another tab has since changed is refused
    // and nothing is written: this tab opens the delete of an unreferenced
    // bundle, another tab points a node at it, this tab confirms.
    const otherTab = async mutate => {
      const current = await view();
      const routing = JSON.parse(JSON.stringify(current.routing));
      mutate(routing);
      const wrote = await page.request.post(origin + '/api/config/set', { headers: { Origin: origin }, data: { scope: 'global', key: 'agent.tier_routing', value: routing, revision: current.revision } });
      assert.equal(wrote.status(), 200, await wrote.text());
    };
    await otherTab(r => { r.bundles.spare = { harness: 'codex', model: 'gpt-5.5-mini' }; });
    reread = responded('/api/config/tier-routing');
    await card.locator('[data-tier-caller=claude]').click();
    await reread;
    shown = responded('/api/config/tier-routing/preview');
    await card.locator('[data-id="b:spare"]').click();
    modal = frame.locator('dialog[data-tier-modal=bundle]');
    await modal.waitFor();
    await shown;
    await otherTab(r => { r.nodes['uses-spare'] = { '*': 'spare' }; });
    const refused = page.waitForResponse(r => new URL(r.url()).pathname === '/api/config/set');
    reread = responded('/api/config/tier-routing');
    await modal.locator('.tr-delete').click();
    assert.equal((await refused).status(), 409);
    await reread;
    await page.waitForTimeout(150);
    assert.match(await modal.locator('.tr-modal-err').textContent(), /changed since it was loaded/);
    assert.equal(await modal.locator('.tr-delete').isDisabled(), true, 'the stale modal cannot write again');
    v = await view();
    assert.ok(v.bundles.some(b => b.name === 'spare'), 'the bundle survives the stale delete');
    assert.deepEqual(nodeOf(v, 'uses-spare').pointers, { '*': 'spare' }, "the other tab's pointer survives");
    await modal.locator('.tr-modal-x').click();
    assert.match(await card.locator('.tr-notice').textContent(), /changed since it was loaded/);
    shown = responded('/api/config/tier-routing/preview');
    await card.locator('[data-id="b:spare"]').click();
    modal = frame.locator('dialog[data-tier-modal=bundle]');
    await modal.waitFor();
    await shown;
    await modal.locator('.tr-delete').click();
    assert.match(await modal.locator('.tr-modal-err').textContent(), /Still referenced by uses-spare · \*/, 'the reloaded graph knows the new reference');
    await modal.locator('.tr-modal-x').click();
    await otherTab(r => { delete r.nodes['uses-spare']; delete r.bundles.spare; });
    console.log('concurrent: a save from a stale graph is refused, reloads, and loses nothing');

    // 8e. Graph reads answer out of order: a slow read for an earlier caller
    // harness does not undo a later switch.
    let releaseRead;
    const heldRead = new Promise(resolve => { releaseRead = resolve; });
    const claudeRead = url => url.pathname === '/api/config/tier-routing' && url.searchParams.get('harness') === 'claude';
    await page.route(claudeRead, async route => { await heldRead; await route.continue(); });
    const slow = page.waitForRequest(r => claudeRead(new URL(r.url())));
    await card.locator('[data-tier-caller=claude]').click();
    await slow;
    reread = page.waitForResponse(r => new URL(r.url()).searchParams.get('harness') === 'codex');
    await card.locator('[data-tier-caller=codex]').click();
    await reread;
    await card.locator('[data-tier-caller-terminal=codex]').waitFor();
    const lateRead = page.waitForResponse(r => claudeRead(new URL(r.url())));
    releaseRead();
    await lateRead;
    await page.waitForTimeout(200);
    await page.unroute(claudeRead);
    assert.equal(await card.locator('[data-tier-caller-terminal=codex]').count(), 1, 'the late claude read is dropped');
    assert.equal(await card.locator('[data-tier-caller=codex]').getAttribute('aria-pressed'), 'true');
    await card.locator('[data-tier-node=ticket-worker]').hover();
    assert.match((await chips(card.locator('.tr-trace'))).at(-1), /codex\.medium · caller/);
    reread = responded('/api/config/tier-routing');
    await card.locator('[data-tier-caller=claude]').click();
    await reread;
    await card.locator('[data-tier-caller-terminal=claude]').waitFor();
    console.log('order: a late graph read for an earlier caller is dropped');

    // 9. After a reload the drawn graph and traces match the daemon's.
    await page.reload();
    await openSettings();
    v = await view();
    assert.equal(await card.locator('.tr-node').count(), v.nodes.length);
    assert.equal(await card.locator('path.tr-edge').count(), v.nodes.reduce((n, node) => n + node.edges.length, 0));
    for (const name of ['lead-review', 'ticket-selector', 'ticket-worker-elevated', 'h1']) {
      await card.locator(`[data-tier-node="${name}"]`).hover();
      const node = nodeOf(v, name), route = node.routes.find(r => r.size === (node.size || 'large'));
      assert.deepEqual((await chips(card.locator('.tr-trace'))).map(c => c.split(' · ')[0]), [route.token, ...route.hops.map(h => h.to)], name);
    }
    await card.screenshot({ path: shot('graph-after-edits.png') });
    console.log('reload: graph and traces match the daemon resolution');

    // 10. The new-session picker offers the saved bundles.
    const MODES = ['auto', 'default', 'acceptEdits', 'plan'];
    const choices = {
      harnesses: [
        { harness: 'claude', lead: true, available: true, models: [{ id: 'claude-opus-4-6' }, { id: 'sonnet-4.6' }], efforts: ['low', 'medium', 'high', 'max'], permission_modes: MODES, permission_levels: ['read-only', 'workdir-write', 'full'] },
        { harness: 'codex', lead: true, available: true, models: [{ id: 'gpt-5.5' }, { id: 'gpt-5.5-mini' }], efforts: ['low', 'medium', 'high'], permission_modes: ['read-only', 'workspace-write'], permission_levels: ['read-only', 'workdir-write', 'full'] }
      ],
      last: {},
      tiers: v.bundles.map(b => ({ name: b.name, trace: b.route.trace, harness: b.route.harness, model: b.route.model, effort: b.route.effort }))
    };
    const posts = [];
    await page.route('**/support/choices', route => route.fulfill({ json: choices, headers: { 'Cache-Control': 'no-store' } }));
    await page.route('**/agents/leads**', route => {
      posts.push({ path: new URL(route.request().url()).pathname, body: route.request().postDataJSON() });
      return route.fulfill({ status: 500, body: 'fixture: not starting a session' });
    });
    await page.goto(origin + '/');
    if (await page.locator('#railbtn').isVisible() && !await page.locator('html').evaluate(el => el.classList.contains('rail-open'))) {
      await page.locator('#railbtn').click();
    }
    await page.locator('#rail .rnew').first().click();
    const pop = page.locator('.rpop:not([hidden])').filter({ has: page.locator('.pmsec') });
    await pop.waitFor();
    const picker = pop.locator('[data-tier-picker]');
    await picker.waitFor();
    await page.waitForTimeout(200);
    assert.deepEqual(await picker.locator('option').evaluateAll(os => os.map(o => o.value)), ['', 'deep-design', 'discussion']);
    await pop.screenshot({ path: shot('picker.png') });
    const rows = pop.locator('.spopts select');
    await picker.selectOption('discussion');
    assert.equal(await pop.locator('[data-harness=codex]').getAttribute('aria-checked'), 'true');
    assert.equal(await rows.nth(0).inputValue(), 'gpt-5.5');
    assert.equal(await rows.last().inputValue(), 'high');
    await pop.locator('.pmseg [data-level=read-only]').click();
    await picker.selectOption('deep-design');
    assert.equal(await pop.locator('[data-harness=claude]').getAttribute('aria-checked'), 'true');
    assert.equal(await rows.nth(0).inputValue(), 'claude-opus-4-6');
    assert.equal(await pop.locator('.pmseg [data-level=read-only]').getAttribute('aria-checked'), 'true', 'the permission level stays the user\'s');
    await pop.screenshot({ path: shot('picker-filled.png') });
    await rows.nth(0).selectOption('sonnet-4.6');
    assert.equal(await picker.inputValue(), '', 'a manual model edit returns the picker to manual');
    await pop.screenshot({ path: shot('picker-manual.png') });
    await picker.selectOption('deep-design');
    await pop.locator('.pbtn.go').click();
    await page.waitForTimeout(600);
    assert.equal(posts.length, 1, JSON.stringify(posts));
    assert.equal(posts[0].path, '/agents/leads/permission');
    assert.equal(posts[0].body.harness, 'claude');
    assert.equal(posts[0].body.model, 'claude-opus-4-6');
    assert.equal(posts[0].body.effort, 'high');
    assert.equal(posts[0].body.permission_level, 'read-only');
    assert.equal(JSON.stringify(posts[0].body).includes('discussion') || JSON.stringify(posts[0].body).includes('deep-design'), false, 'the launch carries concrete fields, no tier name');
    console.log('picker: saved bundles fill harness/model/effort; manual edit returns to manual; launch stays concrete');

    assert.deepEqual(errors, []);
    console.log('tier routing UI cases passed');
  } finally {
    await browser.close();
  }
})().catch(e => { console.error(e); process.exit(1); });
