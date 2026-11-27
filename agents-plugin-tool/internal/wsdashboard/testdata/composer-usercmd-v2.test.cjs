const { test } = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const { readAssetSource } = require('./asset-source.cjs');
const { EN, jsonRes, makeUcmdHarness } = require('./usercmd-harness.cjs');

test('trigger button mounts right after #sbadge, stays visible at 0 commands, hides on 403', async () => {
  const calls = [];
  const h = makeUcmdHarness((url, opts) => {
    calls.push({ url, method: opts.method || 'GET' });
    return jsonRes({ rev: 'e1.1.r1', token: 'tok1', commands: [] });
  });
  await h.flushMicrotasks();

  const badge = h.document.getElementById('ucmdbadge');
  assert(badge, '#ucmdbadge created');
  assert.equal(h.sbadge.nextSibling, badge, 'inserted right after #sbadge');
  assert.equal(badge.hidden, false, 'always visible even at 0 commands');
  assert.equal(badge.classList.contains('is-empty'), true);
  assert.equal(h.observed.includes(badge), true, 'registered with xhRo');
  assert.ok(h.hudLayouts >= 1, 'triggers xhLayout');

  // Opening popover at 0 commands shows empty state.
  h.fire(badge, 'click');
  const pop = h.composer.querySelector('.ucmd-pop');
  assert.equal(pop.hidden, false);
  assert.equal(h.composer.querySelector('.ucmd-empty').hidden, false);
  assert.equal(h.composer.querySelector('.ucmd-list').hidden, true);

  // Denied fetch (403) hides trigger and closes popover.
  h.setFetch(() => Promise.resolve({ ok: false, status: 403, json: () => Promise.resolve({}), text: () => Promise.resolve('forbidden') }));
  h.ctx.ucmdFetch();
  await h.flushMicrotasks();
  assert.equal(badge.hidden, true);
  assert.equal(pop.hidden, true);
});

test('dual-scope same-ID coexistence, inline editing with zero focus loss, Enter guard, revert, and save PUT', async () => {
  const reqs = [];
  const baseCommands = [
    { id: 'build', source: 'agent', label: 'Agent Build', line: 'go build ./...', prompt: 'Check build', cwd: 'backend', revision: 'r1' },
    { id: 'build', source: 'user', label: 'My Build', line: 'make all\nmake check', prompt: '', cwd: '', revision: 'r2' }
  ];
  const h = makeUcmdHarness((url, opts) => {
    const method = opts.method || 'GET';
    const body = opts.body ? JSON.parse(opts.body) : null;
    reqs.push({ url, method, body });
    if (method === 'GET') return jsonRes({ rev: 'e1.1.r1', token: 'tok1', commands: baseCommands });
    if (method === 'PUT') {
      const updated = baseCommands.map(c =>
        c.id === 'build' && c.source === body.source
          ? Object.assign({}, c, { line: body.line, prompt: body.prompt, revision: 'r3' })
          : c
      );
      return jsonRes({ rev: 'e1.2.r1', token: 'tok1', commands: updated });
    }
    return jsonRes({}, 404);
  });
  await h.flushMicrotasks();

  const badge = h.document.getElementById('ucmdbadge');
  assert.equal(badge.classList.contains('is-empty'), false);
  assert.equal(badge.querySelector('.ucmd-trigger-cnt').textContent, '2');
  h.fire(badge, 'click');

  const cards = h.composer.querySelectorAll('.ucmd-card');
  assert.equal(cards.length, 2, 'both agent:build and user:build coexist');
  assert.equal(cards[0].getAttribute('data-ucmd-key'), 'agent:build');
  assert.equal(cards[1].getAttribute('data-ucmd-key'), 'user:build');

  // Multiline user command uses textarea and preserves newline.
  const userCmdIn = cards[1].querySelector('.ucmd-inline-input.is-cmd');
  assert.equal(userCmdIn.tagName, 'TEXTAREA');
  assert.equal(userCmdIn.value, 'make all\nmake check');

  // Inline edit on agent:build keeps focus and toggles dirty in place without DOM rebuild.
  const agentCard = cards[0];
  const cmdIn = agentCard.querySelector('.ucmd-inline-input.is-cmd');
  const prIn = agentCard.querySelector('.ucmd-inline-input.is-prompt');
  cmdIn.focus();
  cmdIn.value = 'go build -race ./...';
  h.fire(cmdIn, 'input');
  assert.equal(h.document.activeElement, cmdIn, 'focus preserved on input');
  assert.equal(agentCard.classList.contains('is-dirty'), true);
  assert.equal(agentCard.querySelector('.ucmd-cmd-box').classList.contains('is-dirty'), true);
  assert.equal(agentCard.querySelector('.ucmd-rev-btn').disabled, false);

  // Incoming SSE refresh preserves active input focus and unsaved draft!
  h.ctx.ucmdApply({ rev: 'e1.1.r1', token: 'tok1', commands: baseCommands }, false);
  assert.equal(h.composer.querySelectorAll('.ucmd-card')[0], agentCard, 'same DOM card node reused');
  assert.equal(h.document.activeElement, cmdIn, 'focus preserved across SSE update');
  assert.equal(cmdIn.value, 'go build -race ./...', 'unsaved draft preserved across SSE update');

  // Enter on Line 2 never runs; focuses ▶ button and shows reminder toast.
  const enterEv = h.fire(cmdIn, 'keydown', { key: 'Enter' });
  assert.equal(enterEv.defaultPrevented, true);
  assert.equal(h.document.activeElement, agentCard.querySelector('.ucmd-ibtn-run'));
  assert.equal(h.warns.at(-1), EN['js.ucmd.enter.hint']);
  assert.equal(reqs.filter(r => r.url.endsWith('/run')).length, 0);

  // Enter on Line 3 never asks; focuses ↗ button and shows reminder toast.
  prIn.focus();
  const enterPr = h.fire(prIn, 'keydown', { key: 'Enter' });
  assert.equal(enterPr.defaultPrevented, true);
  assert.equal(h.document.activeElement, agentCard.querySelector('.ucmd-ibtn-ask'));
  assert.equal(h.posts.length, 0);

  // Single-click ↺ reverts command and clears dirty.
  h.fire(agentCard.querySelector('.ucmd-rev-btn'), 'click');
  assert.equal(cmdIn.value, 'go build ./...');
  assert.equal(agentCard.classList.contains('is-dirty'), false);

  // Edit again and click 💾 -> PUT with source, revision, line, prompt.
  cmdIn.value = 'go build -v ./...';
  h.fire(cmdIn, 'input');
  prIn.value = 'Check verbose build';
  h.fire(prIn, 'input');
  h.fire(agentCard.querySelector('.ucmd-ibtn-save'), 'click');
  await h.flushMicrotasks();

  const put = reqs.find(r => r.method === 'PUT');
  assert(put, 'PUT request sent');
  assert.equal(put.url, '/s/sess1/user-commands/build');
  assert.deepEqual(put.body, {
    source: 'agent',
    revision: 'r1',
    line: 'go build -v ./...',
    prompt: 'Check verbose build'
  });
  assert.equal(agentCard.classList.contains('is-dirty'), false, 'dirty cleared after save');
});

test('1s hold confirmation on Run, Ask Agent (preserving #composer draft), Delete, and Escape/early-release safety', async () => {
  const reqs = [];
  const cmds = [
    { id: 'test', source: 'agent', label: 'Run Tests', line: 'go test ./...', prompt: 'Check failures', cwd: 'pkg', revision: 'r1' }
  ];
  const h = makeUcmdHarness((url, opts) => {
    const method = opts.method || 'GET';
    const body = opts.body ? JSON.parse(opts.body) : null;
    reqs.push({ url, method, body });
    if (method === 'GET') return jsonRes({ rev: 'e1.1.r1', token: 'tok1', commands: cmds });
    if (url.endsWith('/run')) return jsonRes({ id: body.submission_id, command: body.line || 'go test ./...', exec_id: 'ex1', token: 'tok1' });
    if (url.endsWith('/remove')) return jsonRes({ rev: 'e1.2.r1', token: 'tok1', commands: [] });
    return jsonRes({}, 404);
  });
  await h.flushMicrotasks();

  const badge = h.document.getElementById('ucmdbadge');
  const pop = h.composer.querySelector('.ucmd-pop');
  h.fire(badge, 'click');
  const card = h.composer.querySelector('.ucmd-card');
  const runBtn = card.querySelector('.ucmd-ibtn-run');
  const askBtn = card.querySelector('.ucmd-ibtn-ask');
  const delBtn = card.querySelector('.ucmd-ibtn-del');

  // 1) Early release (<1000ms) on ▶ cancels and emits quick-click reminder toast; nothing runs.
  h.holdConfirm(runBtn, 400);
  assert.equal(reqs.filter(r => r.url.endsWith('/run')).length, 0);
  assert.equal(h.warns.at(-1), EN['js.ucmd.hold.early.run']);

  // 2) Escape during active hold cancels ONLY the hold, keeping popover open!
  h.holdConfirm(runBtn, 600, { escape: true });
  assert.equal(pop.hidden, false, 'popover stays open when Escape cancels a hold');
  assert.equal(reqs.filter(r => r.url.endsWith('/run')).length, 0);

  // 3) Full 1000ms hold on ↗ sends immediate structured POST /message WITHOUT touching #composer draft.
  const cmdIn = card.querySelector('.ucmd-inline-input.is-cmd');
  cmdIn.value = 'go test -race ./...';
  h.fire(cmdIn, 'input');
  h.holdConfirm(askBtn, 1000);
  await h.flushMicrotasks();
  assert.equal(h.posts.length, 1);
  assert.equal(h.posts[0].route, 'message');
  assert.equal(h.posts[0].body.text, [
    '<ws:ucmd label="Run Tests" edited="true">',
    'User requested execution of the following command.',
    '<cmd cwd="pkg">go test -race ./...</cmd>',
    '<prompt>Check failures</prompt>',
    '</ws:ucmd>'
  ].join('\n'));
  assert.equal(h.ctext.value, 'unsent composer draft', '#composer draft untouched');
  assert.equal(pop.hidden, true, 'popover closes after ask send');

  // 4) Reopen and full 1000ms hold on ▶ sends edited line override + source to /run and tracks ucmd origin.
  h.fire(badge, 'click');
  h.holdConfirm(runBtn, 1000);
  await h.flushMicrotasks();
  const runReq = reqs.find(r => r.url.endsWith('/run'));
  assert(runReq, 'run POST sent');
  assert.equal(runReq.body.source, 'agent');
  assert.equal(runReq.body.line, 'go test -race ./...');
  assert.equal(h.tracked.length, 1);
  assert.equal(h.tracked[0].origin, 'ucmd');
  assert.equal(pop.hidden, true, 'popover closes after run');

  // 5) After guard expires, reopen and full 1000ms hold on × deletes command.
  h.advance(700);
  h.fire(badge, 'click');
  h.holdConfirm(delBtn, 1000);
  await h.flushMicrotasks();
  const delReq = reqs.find(r => r.url.endsWith('/remove'));
  assert(delReq, 'remove POST sent');
  assert.deepEqual(delReq.body, { revision: 'r1', source: 'agent' });
  assert.equal(h.composer.querySelectorAll('.ucmd-card').length, 0);
  assert.equal(h.composer.querySelector('.ucmd-empty').hidden, false);
});

test('inline + New command form validates required fields, shows 400 error inline, and adds user command on 201', async () => {
  let postAttempt = 0;
  const h = makeUcmdHarness((url, opts) => {
    const method = opts.method || 'GET';
    const body = opts.body ? JSON.parse(opts.body) : null;
    if (method === 'GET') return jsonRes({ rev: 'e1.1.r1', token: 'tok1', commands: [] });
    if (method === 'POST' && url.endsWith('/user-commands')) {
      postAttempt++;
      if (postAttempt === 1) return jsonRes({ error: 'cwd escapes worktree root' }, 400);
      return jsonRes({
        rev: 'e1.2.r1',
        token: 'tok1',
        commands: [{ id: 'deploy', source: 'user', label: body.label, line: body.line, cwd: body.cwd, prompt: body.prompt, revision: 'r1' }]
      }, 201);
    }
    return jsonRes({}, 404);
  });
  await h.flushMicrotasks();

  const badge = h.document.getElementById('ucmdbadge');
  h.fire(badge, 'click');
  h.fire(h.composer.querySelector('.ucmd-add-btn'), 'click');
  h.fire(h.composer.querySelector('.ucmd-agent-foot .ucmd-link-btn'), 'click');

  const form = h.composer.querySelector('.ucmd-add-form');
  const errEl = form.querySelector('.ucmd-form-err');
  assert.equal(form.hidden, false);
  assert.equal(h.composer.querySelector('.ucmd-empty').hidden, true, 'empty card hidden while adding');

  // Empty submit shows client validation error.
  h.fire(form, 'submit');
  assert.equal(errEl.hidden, false);
  assert.equal(errEl.textContent, EN['js.ucmd.form.req']);

  // Fill fields with bad cwd -> server 400 renders inline, form stays open with values preserved.
  form.querySelector('[data-f="label"]').value = 'Deploy';
  form.querySelector('[data-f="cwd"]').value = '../outside';
  form.querySelector('[data-f="line"]').value = './deploy.sh';
  form.querySelector('[data-f="prompt"]').value = 'Verify rollout';
  h.fire(form, 'submit');
  await h.flushMicrotasks();
  assert.equal(form.hidden, false);
  assert.equal(errEl.hidden, false);
  assert.equal(errEl.textContent, 'cwd escapes worktree root');
  assert.equal(form.querySelector('[data-f="label"]').value, 'Deploy');

  // Fix cwd -> 201 closes form, clears fields, and renders the new user card.
  form.querySelector('[data-f="cwd"]').value = 'scripts';
  h.fire(form, 'submit');
  await h.flushMicrotasks();
  assert.equal(form.hidden, true);
  assert.equal(h.composer.querySelectorAll('.ucmd-card').length, 1);
  assert.equal(h.composer.querySelector('.ucmd-card').getAttribute('data-source'), 'user');
  assert.equal(h.composer.querySelector('.ucmd-cwd').textContent, 'scripts');
});

test('keyboard Space/Enter hold-to-confirm, empty-line ask guard, and 409 stale/withdrawn on delete preserve drafts and warn', async () => {
  const reqs = [];
  let delAttempt = 0;
  const cmds = [
    { id: 'build', source: 'agent', label: 'Build', line: 'go build ./...', prompt: 'Check errors', cwd: '', revision: 'r1' }
  ];
  const h = makeUcmdHarness((url, opts) => {
    const method = opts.method || 'GET';
    const body = opts.body ? JSON.parse(opts.body) : null;
    reqs.push({ url, method, body });
    if (method === 'GET') return jsonRes({ rev: 'e1.1.r1', token: 'tok1', commands: cmds });
    if (url.endsWith('/run')) return jsonRes({ id: body.submission_id, command: body.line || 'go build ./...', exec_id: 'ex1', token: 'tok1' });
    if (url.endsWith('/remove')) {
      delAttempt++;
      if (delAttempt === 1) {
        return jsonRes({
          error: 'stale',
          rev: 'e1.2.r1',
          token: 'tok1',
          commands: [{ id: 'build', source: 'agent', label: 'Build', line: 'go build ./...', prompt: 'Updated by agent', cwd: '', revision: 'r2' }]
        }, 409);
      }
      return jsonRes({ error: 'withdrawn', rev: 'e1.3.r1', token: 'tok1', commands: [] }, 409);
    }
    return jsonRes({}, 404);
  });
  await h.flushMicrotasks();

  const badge = h.document.getElementById('ucmdbadge');
  h.fire(badge, 'click');
  const card = h.composer.querySelector('.ucmd-card');
  const cmdIn = card.querySelector('.ucmd-inline-input.is-cmd');
  const runBtn = card.querySelector('.ucmd-ibtn-run');
  const askBtn = card.querySelector('.ucmd-ibtn-ask');
  const delBtn = card.querySelector('.ucmd-ibtn-del');

  // 1) Keyboard Space early release (<1000ms) cancels and emits quick-click toast.
  runBtn.focus();
  h.fire(runBtn, 'keydown', { key: ' ', repeat: false });
  h.advance(300);
  h.fire(runBtn, 'keyup', { key: ' ' });
  assert.equal(reqs.filter(r => r.url.endsWith('/run')).length, 0);
  assert.equal(h.warns.at(-1), EN['js.ucmd.hold.early.run']);

  // 2) Keyboard Enter full 1000ms hold + release confirms run.
  h.fire(runBtn, 'keydown', { key: 'Enter', repeat: false });
  h.advance(1000);
  h.fire(runBtn, 'keyup', { key: 'Enter' });
  await h.flushMicrotasks();
  assert.equal(reqs.filter(r => r.url.endsWith('/run')).length, 1);

  // 3) Empty Line 2 prevents Ask Agent from dispatching POST /message.
  h.advance(700);
  h.fire(badge, 'click');
  cmdIn.value = '   ';
  h.fire(cmdIn, 'input');
  h.holdConfirm(askBtn, 1000);
  assert.equal(h.posts.length, 0, 'empty command line does not send ask message');
  assert.equal(h.warns.at(-1), EN['js.ucmd.save.empty']);

  // 4) 409 stale on delete keeps unsaved inline command draft and shows stale warning.
  cmdIn.value = 'go build -race ./...';
  h.fire(cmdIn, 'input');
  h.holdConfirm(delBtn, 1000);
  await h.flushMicrotasks();
  assert.equal(h.warns.at(-1), EN['js.ucmd.stale']);
  assert.equal(h.oks.length, 0);
  const refreshedCard = h.composer.querySelector('.ucmd-card');
  assert.equal(refreshedCard.querySelector('.ucmd-inline-input.is-cmd').value, 'go build -race ./...', 'stale 409 preserves unsaved inline draft');
  assert.equal(refreshedCard.classList.contains('is-dirty'), true);

  // 5) 409 withdrawn on delete warns withdrawn and clears card.
  h.holdConfirm(refreshedCard.querySelector('.ucmd-ibtn-del'), 1000);
  await h.flushMicrotasks();
  assert.equal(h.warns.at(-1), EN['js.ucmd.withdrawn']);
  assert.equal(h.composer.querySelectorAll('.ucmd-card').length, 0);
});

test('Ask Agent serializes <ws:ucmd> XML with proper escaping and omits optional edited/cwd when unset', async () => {
  const h = makeUcmdHarness((url, opts) => {
    const method = opts.method || 'GET';
    if (method === 'GET') {
      return jsonRes({
        rev: 'e1.1.r1',
        token: 'tok1',
        commands: [
          {
            id: 'esc',
            source: 'user',
            label: 'Build & "Test" <v1> \'ok\'',
            line: 'echo "a < b" && cat [Image #1] > out.txt',
            cwd: '',
            prompt: '',
            revision: 'r1'
          }
        ]
      });
    }
    return jsonRes({}, 404);
  });
  await h.flushMicrotasks();
  h.fire(h.document.getElementById('ucmdbadge'), 'click');
  const askBtn = h.composer.querySelector('.ucmd-card .ucmd-ibtn-ask');
  h.holdConfirm(askBtn, 1000);
  await h.flushMicrotasks();
  assert.equal(h.posts.length, 1);
  assert.equal(h.posts[0].body.text, [
    '<ws:ucmd label="Build &amp; &quot;Test&quot; &lt;v1&gt; &#39;ok&#39;">',
    'User requested execution of the following command.',
    '<cmd>echo "a &lt; b" &amp;&amp; cat &#91;Image #1] &gt; out.txt</cmd>',
    '<prompt>' + EN['js.ucmd.ask.default'] + '</prompt>',
    '</ws:ucmd>'
  ].join('\n'));
  assert.equal(h.posts[0].body.text.includes('edited='), false);
  assert.equal(h.posts[0].body.text.includes('cwd='), false);
  assert.equal(h.posts[0].body.text.includes('[Image #1]'), false);
});

test('optimistic held outboxBubble hydrates <ws:ucmd> cards adjacent to <wb:selection> blocks', () => {
  const deliverySrc = readAssetSource('wsdashboard', 'dashboard-composer-delivery.js');
  const bundlesSrc = readAssetSource('wsdashboard', 'dashboard-bundles.js');

  function extractFunc(src, name) {
    const marker = '  function ' + name + '(';
    const start = src.indexOf(marker);
    assert(start >= 0, 'missing ' + marker);
    const end = src.indexOf('\n  }\n', start);
    assert(end >= 0, 'unterminated ' + name);
    return src.slice(start, end + 4);
  }

  function makeEl(tag) {
    return {
      tagName: tag.toUpperCase(),
      className: '',
      attrs: {},
      children: [],
      _text: '',
      get textContent() {
        return this.children.length ? this.children.map(c => c.textContent).join('') : this._text;
      },
      set textContent(v) { this._text = String(v); this.children = []; },
      get firstChild() { return this.children[0] || null; },
      setAttribute(k, v) { this.attrs[k] = String(v); },
      getAttribute(k) { return this.attrs[k]; },
      appendChild(c) { this.children.push(c); return c; },
      cloneNode() { return makeEl(tag); },
      addEventListener() {},
      querySelectorAll(sel) {
        const out = [];
        for (const c of this.children) {
          if (sel.startsWith('.') && c.className && c.className.split(/\s+/).includes(sel.slice(1))) out.push(c);
          if (c.querySelectorAll) out.push(...c.querySelectorAll(sel));
        }
        return out;
      },
      querySelector(sel) { return this.querySelectorAll(sel)[0] || null; }
    };
  }

  const doc = {
    createElement: makeEl,
    createElementNS: (_ns, tag) => makeEl(tag),
    createTextNode: t => ({ nodeType: 3, textContent: String(t) }),
    querySelector: () => null
  };
  const ctx = vm.createContext({
    window: {},
    doc,
    TextEncoder,
    Object,
    Array,
    String,
    RegExp,
    heldAct: () => null,
    wsT: k => EN[k] || k,
    marker: n => '[Image #' + n + ']'
  });
  vm.runInContext(
    bundlesSrc + '\n' +
    extractFunc(deliverySrc, 'ucmdUnescapeXml') + '\n' +
    extractFunc(deliverySrc, 'parseUcmdBlock') + '\n' +
    extractFunc(deliverySrc, 'splitUcmdParts') + '\n' +
    extractFunc(deliverySrc, 'outboxBubble'),
    ctx
  );

  const sel = [
    '<wb:selection>',
    '<wb:mention topic="Plan">check this line</wb:mention>',
    '<wb:comment>looks good</wb:comment>',
    '</wb:selection>'
  ].join('\n');
  const ucmd = [
    '<ws:ucmd label="Run Tests">',
    'User requested execution of the following command.',
    '<cmd cwd="pkg">go test ./...</cmd>',
    '</ws:ucmd>'
  ].join('\n');

  const cases = [
    { name: 'selection before ucmd', text: sel + '\n\n' + ucmd, wantSels: 1, wantCards: 1 },
    { name: 'ucmd before selection', text: ucmd + '\n\n' + sel, wantSels: 1, wantCards: 1 },
    { name: 'ucmd between selections', text: sel + '\n\n' + ucmd + '\n\n' + sel, wantSels: 2, wantCards: 1 },
    { name: 'selection, prose, and ucmd', text: sel + '\n\nPlease run:\n\n' + ucmd, wantSels: 1, wantCards: 1 }
  ];

  for (const tc of cases) {
    const bub = ctx.outboxBubble({ id: 'h-' + tc.name, back: { text: tc.text }, pics: [] });
    assert.equal(bub.querySelectorAll('.icapsule-selection').length, tc.wantSels, tc.name + ': selection count');
    assert.equal(bub.querySelectorAll('.ucmd-req').length, tc.wantCards, tc.name + ': ucmd card count');
    assert.equal(bub.querySelector('.ucmd-req-cmd').textContent, 'go test ./...', tc.name + ': cmd text');
    assert.equal(bub.textContent.includes('<ws:ucmd'), false, tc.name + ': raw <ws:ucmd leaked');
    assert.equal(bub.textContent.includes('<wb:selection'), false, tc.name + ': raw <wb:selection leaked');
  }
});

test('+ New command opens the agent request by default; the link switches to the manual form and back', async () => {
  const h = makeUcmdHarness(() => jsonRes({ rev: 'e1.1.r1', token: 'tok1', commands: [] }));
  await h.flushMicrotasks();
  h.fire(h.document.getElementById('ucmdbadge'), 'click');
  h.fire(h.composer.querySelector('.ucmd-add-btn'), 'click');
  const box = h.composer.querySelector('.ucmd-agent-box');
  const form = h.composer.querySelector('.ucmd-add-form');
  assert.equal(box.hidden, false, 'agent box is the default surface');
  assert.equal(form.hidden, true);
  assert.equal(box.querySelectorAll('.ucmd-qchip').length, 3);
  assert.equal(box.querySelector('.ucmd-mcp-pill').textContent, 'session.register_user_command');
  assert.equal(box.querySelector('.ucmd-link-btn').textContent, EN['js.ucmd.agent.manual']);
  h.fire(box.querySelector('.ucmd-link-btn'), 'click');
  assert.equal(box.hidden, true);
  assert.equal(form.hidden, false, 'manual four-field form after the switch');
  h.fire(form.querySelector('.ucmd-link-btn'), 'click');
  assert.equal(box.hidden, false, 'the manual form has a way back');
  assert.equal(form.hidden, true);
  // Reopening after a close starts at the agent request again.
  h.fire(box.querySelector('.ucmd-link-btn'), 'click');
  h.fire(h.composer.querySelector('.ucmd-add-btn'), 'click');
  h.fire(h.composer.querySelector('.ucmd-add-btn'), 'click');
  assert.equal(box.hidden, false);
  assert.equal(form.hidden, true);
});

test('agent request sends <ws:ucmd-add> with the mcp-guidance, guards empty input, and closes the popover', async () => {
  const h = makeUcmdHarness(() => jsonRes({ rev: 'e1.1.r1', token: 'tok1', commands: [] }));
  await h.flushMicrotasks();
  h.fire(h.document.getElementById('ucmdbadge'), 'click');
  h.fire(h.composer.querySelector('.ucmd-add-btn'), 'click');
  const ta = h.composer.querySelector('.ucmd-agent-ta');
  const err = h.composer.querySelector('.ucmd-agent-box .ucmd-form-err');
  h.fire(h.composer.querySelector('.ucmd-btn-primary[data-act="send"]'), 'click');
  assert.equal(h.posts.length, 0, 'empty request is not sent');
  assert.equal(err.hidden, false);
  assert.equal(err.textContent, EN['js.ucmd.agent.err']);

  h.fire(h.composer.querySelector('.ucmd-qchip'), 'click');
  assert.equal(ta.value, EN['js.ucmd.agent.chip.1.req'], 'chip fills the request');
  ta.value = 'run <go test> & report';
  const ev = h.fire(ta, 'keydown', { key: 'Enter', shiftKey: false });
  assert.equal(ev.defaultPrevented, true);
  assert.equal(h.posts.length, 1);
  assert.equal(h.posts[0].route, 'message');
  const text = h.posts[0].body.text;
  assert.ok(text.startsWith('<ws:ucmd-add scope="worktree" tool="session.register_user_command">\n  <request>run &lt;go test&gt; &amp; report</request>\n  <mcp-guidance>\n'), text);
  assert.ok(text.endsWith('\n  </mcp-guidance>\n</ws:ucmd-add>'), text);
  for (const needle of ['session.register_user_command', 'session_key', 'id:', 'label:', 'line:', 'description:', 'prompt:', 'cwd:', 'agy', 'call(tool: "session.register_user_command"', 'per worktree']) {
    assert.ok(text.includes(needle), 'guidance mentions ' + needle);
  }
  assert.deepEqual(h.oks, [EN['js.ucmd.agent.sent']]);
  assert.equal(h.composer.querySelector('.ucmd-pop').hidden, true, 'popover closes after sending');
});

test('Shift+Enter keeps a newline in the agent request and does not send', async () => {
  const h = makeUcmdHarness(() => jsonRes({ rev: 'e1.1.r1', token: 'tok1', commands: [] }));
  await h.flushMicrotasks();
  h.fire(h.document.getElementById('ucmdbadge'), 'click');
  h.fire(h.composer.querySelector('.ucmd-add-btn'), 'click');
  const ta = h.composer.querySelector('.ucmd-agent-ta');
  ta.value = 'x';
  const ev = h.fire(ta, 'keydown', { key: 'Enter', shiftKey: true });
  assert.equal(ev.defaultPrevented, false);
  assert.equal(h.posts.length, 0);
});
