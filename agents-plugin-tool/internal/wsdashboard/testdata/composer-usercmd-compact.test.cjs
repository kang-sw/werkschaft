// Compact two-zone user command tiles (mockup 261126 ucmd-panel-compact,
// Option C): one-line $ collapse, ~2x expand into the bash-highlighted overlay
// editor, Esc / outside-click / Collapse folding with drafts kept, inline
// rename, the create form's editor, and the theme-token CSS contract.
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { EN, jsonRes, makeUcmdHarness } = require('./usercmd-harness.cjs');

const ASSETS = path.resolve(__dirname, '../assets');
const css = name => fs.readFileSync(path.join(ASSETS, name), 'utf8');

const MULTI = [
  'set -e',
  'ROOT="$(pwd -W 2>/dev/null || pwd)"',
  'if [ ! -f "$ROOT/cfg.json" ]; then echo "missing" >&2; exit 1; fi',
  '"$ROOT/build/demo.exe" --config "$ROOT/cfg.json" --wait-debugger'
].join('\n');

async function open(commands, onWrite) {
  const reqs = [];
  const h = makeUcmdHarness((url, opts) => {
    const method = opts.method || 'GET';
    const body = opts.body ? JSON.parse(opts.body) : null;
    reqs.push({ url, method, body });
    if (method === 'GET') return jsonRes({ rev: 'e1.1.r1', token: 'tok1', commands });
    if (onWrite) return onWrite(url, method, body);
    return jsonRes({}, 404);
  });
  await h.flushMicrotasks();
  h.fire(h.document.getElementById('ucmdbadge'), 'click');
  const card = key => h.composer.querySelector('.ucmd-card[data-ucmd-key="' + key + '"]');
  return { h, reqs, card };
}

function parts(card) {
  return {
    box: card.querySelector('.ucmd-cmd-box'),
    row: card.querySelector('.ucmd-cmd-row'),
    preview: card.querySelector('.ucmd-cmd-preview'),
    pill: card.querySelector('.ucmd-lines-pill'),
    ta: card.querySelector('.ucmd-inline-input.is-cmd'),
    code: card.querySelector('.ucmd-code-hl code'),
    meta: card.querySelector('.ucmd-ed-meta'),
    fold: card.querySelector('.ucmd-collapse-btn'),
    revs: card.querySelectorAll('.ucmd-rev-btn'),
    title: card.querySelector('.ucmd-title'),
    prompt: card.querySelector('.ucmd-inline-input.is-prompt')
  };
}

function tokenClasses(el) {
  const out = [];
  for (const c of el.children) {
    const cls = String(c.className || '').split(/\s+/).find(x => x.startsWith('ucmd-hl-'));
    out.push([cls ? cls.slice(8) : '', c.textContent]);
  }
  return out;
}

function rule(src, selector) {
  // The rule whose selector list is exactly `selector` (not a member of a
  // comma-separated list ending in it).
  let at = -1;
  for (let i = src.indexOf('\n' + selector + ' {'); i >= 0; i = src.indexOf('\n' + selector + ' {', i + 1)) {
    if (src.charAt(i - 1) !== ',') { at = i; break; }
  }
  assert.ok(at >= 0, 'rule ' + selector + ' exists');
  return src.slice(at, src.indexOf('}', at));
}

const CMDS = [
  { id: 'demo', source: 'agent', label: 'strata demo', line: MULTI, prompt: '', cwd: '', revision: 1 },
  { id: 'test', source: 'user', label: 'w5090 tests', line: 'go test ./...', prompt: 'Summarize failures', cwd: 'pkg', revision: 2 }
];

test('bash tokenizer round-trips and classifies keywords, strings, variables, operators, flags, numbers, comments', async () => {
  const { h } = await open([]);
  const tok = h.ctx.ucmdBashTokens;
  const samples = [
    MULTI,
    "MSYS_NO_PATHCONV=1 MSYS2_ARG_CONV_EXCL='*' powershell.exe -NoProfile -File \"$ROOT/x.ps1\" -Config Release",
    'for f in a b; do echo ${f} `date` $1 $?; done # loop',
    'go test ./... 2>&1 | tee out.txt && echo ok >> log || exit 3',
    "echo $'a\\tb' \"unterminated $(echo (nested)) tail",
    'x=1\\\n  y',
    ''
  ];
  for (const s of samples) {
    assert.equal(tok(s).map(t => t[1]).join(''), s, 'segments rejoin to the input: ' + JSON.stringify(s));
  }
  const has = (s, cls, text) => assert.ok(tok(s).some(t => t[0] === cls && t[1] === text), cls + ' ' + text + ' in ' + s);
  has('set -e', 'k', 'set');
  has('set -e', 'f', '-e');
  has('ROOT="$(pwd -W 2>/dev/null || pwd)"', 'v', 'ROOT');
  has('ROOT="$(pwd -W 2>/dev/null || pwd)"', 'v', '$(pwd -W 2>/dev/null || pwd)');
  has('if [ ! -f "$X" ]; then echo "a" >&2; exit 1; fi', 'k', 'if');
  has('if [ ! -f "$X" ]; then echo "a" >&2; exit 1; fi', 'k', 'then');
  has('if [ ! -f "$X" ]; then echo "a" >&2; exit 1; fi', 'k', 'fi');
  has('if [ ! -f "$X" ]; then echo "a" >&2; exit 1; fi', 'x', 'echo');
  has('if [ ! -f "$X" ]; then echo "a" >&2; exit 1; fi', 's', '"a"');
  has('if [ ! -f "$X" ]; then echo "a" >&2; exit 1; fi', 'n', '1');
  has('for f in a b; do echo $f; done', 'k', 'in');
  has('for f in a b; do echo $f; done', 'k', 'done');
  has('for f in a b; do echo $f; done', 'v', '$f');
  has('go test 2>&1 | tee x && echo ok', 'o', '2>&1');
  has('go test 2>&1 | tee x && echo ok', 'o', '&&');
  has('go test 2>&1 | tee x && echo ok', 'x', 'tee');
  has('echo a >> log', 'o', '>>');
  has("echo 'single $NOT' # trailing", 's', "'single $NOT'");
  has("echo 'single $NOT' # trailing", 'c', '# trailing');
  has('cmd --flag=val --other', 'f', '--flag');
  has('cmd --flag=val --other', 'f', '--other');
  has('export STRATA=1 local x', 'k', 'export');
  assert.ok(!tok('echo a#b').some(t => t[0] === 'c'), '# inside a word is not a comment');
  assert.ok(!tok('echo done').some(t => t[0] === 'k'), 'keywords only colour in command position');
});

test('commands rest as a one-line ellipsis preview with a +N lines cue; click expands into the highlighted editor', async () => {
  const { h, card } = await open(CMDS);
  const d = parts(card('agent:demo'));
  const s = parts(card('user:test'));

  // Two-zone tile: header band + body well, no inline capsule bars.
  const tile = card('agent:demo');
  assert.ok(tile.querySelector('.ucmd-band .ucmd-line1 .ucmd-acts'), 'actions live in the header band');
  assert.ok(tile.querySelector('.ucmd-well .ucmd-cmd-box'), 'command lives in the body well');
  const well = tile.querySelector('.ucmd-well');
  assert.deepEqual(well.children.map(c => c.className), ['ucmd-cmd-box', 'ucmd-divider', 'ucmd-note'], 'command and prompt note share one well behind a hairline');
  assert.equal(tile.querySelectorAll('.ucmd-inline-bar').length, 0, 'no nested capsule bars');

  // Collapsed resting state.
  assert.equal(d.box.classList.contains('is-expanded'), false);
  assert.equal(d.row.getAttribute('aria-expanded'), 'false');
  assert.equal(d.preview.textContent, MULTI.split('\n').join(' ↵ '), 'lines joined with ↵ on one line');
  assert.ok(tokenClasses(d.preview).some(([cls, t]) => cls === 'k' && t === 'set'), 'preview is lightly highlighted');
  assert.ok(tokenClasses(d.preview).some(([cls]) => cls === 'sep'), 'line separators are dimmed');
  assert.equal(d.pill.textContent, '+3 lines');
  assert.equal(d.pill.classList.contains('is-single'), false);
  assert.equal(s.pill.textContent, '', 'single-line command shows only the chevron cue');
  assert.equal(s.pill.classList.contains('is-single'), true);
  assert.equal(d.ta.value, MULTI, 'the full command stays in the editor while collapsed');

  // Click expands and focuses the overlay editor.
  h.fire(d.row, 'click');
  assert.equal(d.box.classList.contains('is-expanded'), true);
  assert.equal(d.row.getAttribute('aria-expanded'), 'true');
  assert.equal(h.document.activeElement, d.ta);
  assert.equal(d.code.textContent, MULTI, 'backdrop paints the exact text');
  const cls = tokenClasses(d.code);
  for (const want of ['k', 'v', 's', 'o', 'f', 'x', 'n']) assert.ok(cls.some(([c]) => c === want), 'token class ' + want + ' painted');
  assert.equal(d.ta.style.getPropertyValue('--ucmd-rows'), '4');
  assert.equal(d.meta.textContent, '4 lines');

  // Keyboard: Enter / Space on the focused preview row also expands.
  const ev = h.fire(s.row, 'keydown', { key: 'Enter' });
  assert.equal(ev.defaultPrevented, true);
  assert.equal(s.box.classList.contains('is-expanded'), true);
  assert.equal(d.box.classList.contains('is-expanded'), false, 'only one editor is open at a time');
  assert.equal(s.ta.style.getPropertyValue('--ucmd-rows'), '2', 'minimum two rows');
});

test('editor sizing, trailing newline, and scroll sync keep the overlay aligned', async () => {
  const { h, card } = await open(CMDS);
  const d = parts(card('agent:demo'));
  h.fire(d.row, 'click');

  const long = Array.from({ length: 20 }, (_, i) => 'echo line ' + i).join('\n');
  d.ta.value = long;
  h.fire(d.ta, 'input');
  assert.equal(d.ta.style.getPropertyValue('--ucmd-rows'), '20', 'rows track the line count; CSS caps at 140px then scrolls');
  assert.equal(d.meta.textContent, '20 lines');
  assert.equal(d.code.textContent, long);

  d.ta.value = 'echo a\n';
  h.fire(d.ta, 'input');
  assert.equal(d.code.textContent, 'echo a\n ', 'a trailing newline keeps its empty last row in the backdrop');
  assert.equal(d.ta.style.getPropertyValue('--ucmd-rows'), '2');

  d.ta.scrollTop = 48;
  d.ta.scrollLeft = 12;
  h.fire(d.ta, 'scroll');
  assert.equal(d.code.style.transform, 'translate(-12px,-48px)', 'backdrop mirrors both scroll axes');
  d.ta.scrollTop = 0;
  d.ta.scrollLeft = 0;
  h.fire(d.ta, 'scroll');
  assert.equal(d.code.style.transform, '');

  // Repainting is keyed on text: an unchanged value does not rebuild spans.
  const first = d.code.children[0];
  h.ctx.ucmdCodePaint(h.ctx.ucmdNodes['agent:demo'].code);
  assert.equal(d.code.children[0], first);
});

test('Esc, outside press and Collapse fold back to the preview while keeping the draft and dirty/revert state', async () => {
  const { h, card } = await open(CMDS);
  const d = parts(card('agent:demo'));
  const s = parts(card('user:test'));
  const pop = h.composer.querySelector('.ucmd-pop');

  // Edit then Esc: collapses, keeps the popover open, refocuses the row.
  h.fire(d.row, 'click');
  d.ta.value = 'set -e\necho edited';
  h.fire(d.ta, 'input');
  const esc = h.fire(d.ta, 'keydown', { key: 'Escape' });
  assert.equal(esc.defaultPrevented, true);
  assert.equal(d.box.classList.contains('is-expanded'), false);
  assert.equal(pop.hidden, false, 'Esc in the editor does not close the popover');
  assert.equal(h.document.activeElement, d.row);
  assert.equal(d.preview.textContent, 'set -e ↵ echo edited', 'preview reflects the draft');
  assert.equal(d.pill.textContent, '+1 line');
  assert.equal(card('agent:demo').classList.contains('is-dirty'), true);
  assert.equal(d.box.classList.contains('is-dirty'), true);
  assert.equal(d.revs[0].classList.contains('is-dirty'), true, 'collapsed ↺ stays armed');
  assert.equal(d.revs[0].disabled, false);

  // IME composition Esc is left to the IME.
  h.fire(d.row, 'click');
  h.fire(d.ta, 'keydown', { key: 'Escape', isComposing: true });
  assert.equal(d.box.classList.contains('is-expanded'), true);

  // Pressing an action button keeps it open (no layout shift under a hold).
  h.fire(card('user:test').querySelector('.ucmd-ibtn-run'), 'mousedown');
  assert.equal(d.box.classList.contains('is-expanded'), true);
  h.fire(d.ta, 'focusout', { relatedTarget: card('agent:demo').querySelector('.ucmd-ibtn-run') });
  assert.equal(d.box.classList.contains('is-expanded'), true);

  // A press elsewhere in the popover folds it.
  h.fire(s.prompt, 'mousedown');
  assert.equal(d.box.classList.contains('is-expanded'), false);

  // Tabbing out folds it too.
  h.fire(d.row, 'click');
  h.fire(d.ta, 'focusout', { relatedTarget: s.prompt });
  assert.equal(d.box.classList.contains('is-expanded'), false);

  // Collapse button.
  h.fire(d.row, 'click');
  h.fire(d.fold, 'click');
  assert.equal(d.box.classList.contains('is-expanded'), false);
  assert.equal(d.fold.textContent, EN['js.ucmd.collapse']);

  // Document Esc with focus on an action button folds first, then closes.
  h.fire(d.row, 'click');
  card('agent:demo').querySelector('.ucmd-ibtn-run').focus();
  h.fire(h.document, 'keydown', { key: 'Escape' });
  assert.equal(d.box.classList.contains('is-expanded'), false);
  assert.equal(pop.hidden, false);
  h.fire(h.document, 'keydown', { key: 'Escape' });
  assert.equal(pop.hidden, true);

  // Closing folds; reopening shows the preview with the draft still pending.
  h.fire(h.document.getElementById('ucmdbadge'), 'click');
  assert.equal(d.box.classList.contains('is-expanded'), false);
  assert.equal(d.ta.value, 'set -e\necho edited');

  // ↺ from the collapsed row restores the template without expanding.
  h.fire(d.revs[0], 'click');
  assert.equal(d.box.classList.contains('is-expanded'), false);
  assert.equal(d.ta.value, MULTI);
  assert.equal(d.preview.textContent, MULTI.split('\n').join(' ↵ '));
  assert.equal(card('agent:demo').classList.contains('is-dirty'), false);
});

test('single-line commands gain lines with Shift+Enter, survive SSE refresh in place, and save the multi-line draft', async () => {
  let saved = null;
  const { h, reqs, card } = await open(CMDS, (url, method, body) => {
    if (method === 'PUT') {
      saved = body;
      return jsonRes({ rev: 'e1.2.r1', token: 'tok1', commands: CMDS.map(c => c.id === 'test' ? Object.assign({}, c, { line: body.line, revision: 3 }) : c) });
    }
    return jsonRes({}, 404);
  });
  const tile = card('user:test');
  const s = parts(tile);
  h.fire(s.row, 'click');
  s.ta.value = 'go test ./...\ngo vet ./...';
  h.fire(s.ta, 'input');
  const enter = h.fire(s.ta, 'keydown', { key: 'Enter', shiftKey: true });
  assert.equal(enter.defaultPrevented, false, 'Shift+Enter is a newline');

  h.ctx.ucmdApply({ rev: 'e1.1.r1', token: 'tok1', commands: CMDS }, false);
  assert.equal(card('user:test'), tile, 'tile node reused');
  assert.equal(s.ta.value, 'go test ./...\ngo vet ./...', 'newline draft is not stripped');
  assert.equal(s.box.classList.contains('is-expanded'), true, 'expansion survives refresh');
  assert.equal(h.document.activeElement, s.ta);

  h.fire(tile.querySelector('.ucmd-ibtn-save'), 'mousedown');
  h.fire(tile.querySelector('.ucmd-ibtn-save'), 'click');
  await h.flushMicrotasks();
  assert.deepEqual(saved, { source: 'user', revision: 2, line: 'go test ./...\ngo vet ./...' });
  assert.equal(card('user:test'), tile, 'multi-line answer patches the same tile');
  assert.equal(tile.classList.contains('is-dirty'), false);
  assert.equal(s.box.classList.contains('is-expanded'), true);
  assert.ok(reqs.some(r => r.method === 'PUT'));
});

test('inline-editable title: rename marks dirty, Esc reverts, Save sends only the label, empty name is refused', async () => {
  let saved = null;
  const { h, card } = await open(CMDS, (url, method, body) => {
    if (method === 'PUT') {
      saved = body;
      return jsonRes({ rev: 'e1.2.r1', token: 'tok1', commands: CMDS.map(c => c.id === 'demo' ? Object.assign({}, c, { label: body.label, revision: 4 }) : c) });
    }
    return jsonRes({}, 404);
  });
  const tile = card('agent:demo');
  const t = parts(tile).title;
  assert.equal(t.tagName, 'INPUT');
  assert.equal(t.value, 'strata demo');
  assert.ok(t.title.includes(EN['js.ucmd.title.edit']));

  t.focus();
  t.value = 'strata demo (VS)';
  h.fire(t, 'input');
  assert.equal(tile.classList.contains('is-dirty'), true);
  assert.equal(t.classList.contains('is-dirty'), true);

  const esc = h.fire(t, 'keydown', { key: 'Escape' });
  assert.equal(esc.defaultPrevented, true);
  assert.equal(t.value, 'strata demo');
  assert.equal(tile.classList.contains('is-dirty'), false);
  assert.equal(h.composer.querySelector('.ucmd-pop').hidden, false, 'reverting Esc does not close the popover');

  t.value = '   ';
  h.fire(t, 'input');
  h.fire(tile.querySelector('.ucmd-ibtn-save'), 'click');
  assert.equal(saved, null);
  assert.equal(h.warns.at(-1), EN['js.ucmd.label.empty']);

  t.value = 'strata demo (VS) ';
  h.fire(t, 'input');
  const enter = h.fire(t, 'keydown', { key: 'Enter' });
  assert.equal(enter.defaultPrevented, true);
  assert.equal(h.document.activeElement, tile.querySelector('.ucmd-ibtn-save'), 'Enter moves to Save');
  h.fire(tile.querySelector('.ucmd-ibtn-save'), 'click');
  await h.flushMicrotasks();
  assert.deepEqual(saved, { source: 'agent', revision: 1, label: 'strata demo (VS)' });
  assert.equal(t.value, 'strata demo (VS)');
  assert.equal(tile.classList.contains('is-dirty'), false);
});

test('create form uses the highlighted editor, Enter submits, and the new tile opens expanded and focused', async () => {
  const created = [];
  const { h, card } = await open([], (url, method, body) => {
    if (method === 'POST' && url.endsWith('/user-commands')) {
      created.push(body);
      return jsonRes({ rev: 'e1.2.r1', token: 'tok1', commands: [{ id: 'deploy', source: 'user', label: body.label, line: body.line, cwd: '', prompt: '', revision: 1 }] }, 201);
    }
    return jsonRes({}, 404);
  });
  h.fire(h.composer.querySelector('.ucmd-add-btn'), 'click');
  const form = h.composer.querySelector('.ucmd-add-form');
  const line = form.querySelector('[data-f="line"]');
  const code = form.querySelector('.ucmd-code-hl code');
  assert.equal(line.tagName, 'TEXTAREA');
  assert.ok(line.classList.contains('ucmd-code-ta'));
  form.querySelector('[data-f="label"]').value = 'Deploy';
  line.value = 'set -e\n./deploy.sh --env=staging';
  h.fire(line, 'input');
  assert.equal(code.textContent, line.value);
  assert.ok(tokenClasses(code).some(([c, t]) => c === 'f' && t === '--env'));
  assert.equal(h.fire(line, 'keydown', { key: 'Enter', shiftKey: true }).defaultPrevented, false);
  h.fire(line, 'keydown', { key: 'Enter' });
  await h.flushMicrotasks();
  assert.equal(created.length, 1);
  assert.equal(created[0].line, 'set -e\n./deploy.sh --env=staging');
  assert.equal(code.textContent, '', 'form editor repainted empty after create');
  const p = parts(card('user:deploy'));
  assert.equal(p.box.classList.contains('is-expanded'), true);
  assert.equal(h.document.activeElement, p.ta);
});

test('CSS contract: Option C tray/band/well tokens per theme, identical overlay metrics, 140px cap, --hl-* token colours', () => {
  const main = css('dashboard-composer-ucmd.css');
  const ed = css('dashboard-composer-ucmd-editor.css');
  for (const v of ['--ucmd-tray:', '--ucmd-band:', '--ucmd-band-hover:', '--ucmd-body:', '--ucmd-ed-bg:', '--ucmd-hair:']) {
    assert.equal(main.split(v).length - 1, 3, v + ' defined for light, prefers-dark and data-theme=dark');
  }
  const base = rule(main, '.composer');
  assert.ok(/--ucmd-tray: color-mix\(in srgb, var\(--wb-fg\) [\d.]+%, var\(--wb-bg\)\)/.test(base), 'light tray derives from --wb-* tokens');
  assert.ok(base.includes('--ucmd-band: var(--wb-card)'));
  assert.ok(rule(main, '.ucmd-pop-body').includes('gap: 10px'), 'clear inter-tile gap');
  assert.ok(rule(main, '.ucmd-pop-body').includes('background: var(--ucmd-tray)'));
  assert.ok(rule(main, '.ucmd-band').includes('background: var(--ucmd-band)'));
  assert.ok(rule(main, '.ucmd-well').includes('background: var(--ucmd-body)'));

  // Scoped under .composer so `.composer textarea` (0,1,1) cannot restyle it.
  const shared = rule(ed, '.composer .ucmd-code-hl,\n.composer .ucmd-code-ta');
  for (const p of ['box-sizing: border-box', 'font-family: var(--wb-mono)', 'font-size: 11.5px', 'line-height: 16px', 'letter-spacing: 0', 'tab-size: 4', 'padding: 1px 2px 1px 0', 'border: 0', 'white-space: pre', 'overflow-wrap: normal', 'word-break: normal', 'font-variant-ligatures: none']) {
    assert.ok(shared.includes(p), 'shared overlay metric: ' + p);
  }
  const ta = rule(ed, '.composer .ucmd-code-ta');
  assert.ok(ta.includes('min-height: 42px') && ta.includes('border-radius: 0'), 'outranks .composer textarea sizing');
  assert.ok(rule(ed, '.composer .ucmd-code-ta:focus').includes('outline: none'));
  const note = rule(main, '.composer .ucmd-inline-input.is-prompt');
  assert.ok(note.includes('min-height: 0') && note.includes('background: transparent'), 'prompt note is not a 38px composer field');
  assert.ok(ta.includes('max-height: 140px'), '~2x the old 68px field');
  assert.ok(ta.includes('height: calc(var(--ucmd-rows, 2) * 16px'));
  assert.ok(ta.includes('color: transparent') && ta.includes('-webkit-text-fill-color: transparent'));
  assert.ok(ta.includes('caret-color: var(--wb-fg)'));
  assert.ok(ta.includes('background: transparent'));
  assert.ok(rule(ed, '.ucmd-code-ta::selection').includes('color: transparent'));
  const pre = rule(ed, '.ucmd-code-hl');
  assert.ok(pre.includes('position: absolute') && pre.includes('overflow: hidden') && pre.includes('pointer-events: none'));
  const preview = rule(ed, '.ucmd-cmd-preview');
  assert.ok(preview.includes('text-overflow: ellipsis') && preview.includes('overflow: hidden') && preview.includes('font-family: var(--wb-mono)'));
  assert.ok(rule(ed, '.ucmd-cmd-row').includes('min-height: 26px'));
  for (const t of ['k', 'x', 's', 'v', 'c', 'o', 'f', 'n']) {
    assert.ok(/var\(--hl-[a-z]+\)/.test(rule(ed, '.ucmd-hl-' + t)), 'token ' + t + ' uses a theme --hl-* colour');
    assert.ok(!/font-(weight|style)/.test(rule(ed, '.ucmd-hl-' + t)), 'token ' + t + ' keeps glyph advances');
  }
  assert.ok(!/#[0-9a-f]{3,8}\b/i.test(ed), 'editor sheet has no hardcoded colours');
  const git = css('dashboard-git.css');
  assert.equal(git.split('--hl-k:').length - 1, 3, '--hl-* exist for light and both dark selectors');
});

// --- Chromium: real layout, overlay alignment and the LOOK screenshots ------
// Runs the production ucmd fragments and CSS in a static composer fixture
// with a stubbed fetch (no daemon). Needs WS_PLAYWRIGHT_MODULE (and
// WS_PLAYWRIGHT_EXECUTABLE when the module's Chromium build is not installed).
// WS_UCMD_LOOK_DIR, when set, receives popover screenshots per theme.
const PW = process.env.WS_PLAYWRIGHT_MODULE;
const pwSkip = PW ? false : 'set WS_PLAYWRIGHT_MODULE to run Chromium acceptance';
const LOOK_CMDS = [
  { id: 'strata-vs', source: 'agent', label: 'strata demo (Release + VS 디버거)', revision: 1, prompt: '', cwd: '', line: [
    'set -e',
    'ROOT="$(pwd -W 2>/dev/null || pwd)"',
    "MSYS_NO_PATHCONV=1 MSYS2_ARG_CONV_EXCL='*' powershell.exe -NoProfile -ExecutionPolicy Bypass -File \"$ROOT/scripts/launch-vs-demo.ps1\" -Config Release",
    'CFG=configs/demo.station.json',
    'export STRATA_LOG_LEVEL=debug',
    'export STRATA_STATION_CFG="$ROOT/$CFG"',
    'if [ ! -f "$STRATA_STATION_CFG" ]; then echo "missing config: $STRATA_STATION_CFG" >&2; exit 1; fi',
    'echo "[strata] launching Release binary with VS debugger attached..."',
    '"$ROOT/build/Release/strata-demo.exe" --config "$STRATA_STATION_CFG" --wait-debugger',
    'echo "[strata] session exited ($?)"'
  ].join('\n') },
  { id: 'strata-lock', source: 'agent', label: 'strata demo: Release 빌드(sys_lock) + 실행 (디버그 심볼 포함)', revision: 1, prompt: '', cwd: '', line: [
    'set -e',
    'export ROOT="$(pwd -W 2>/dev/null || pwd)"',
    "bash D:/sys_lock.sh run strata-release-build 75 -- bash -c 'bash scripts/build-release.sh --with-pdb'",
    'echo "[strata] demo finished"'
  ].join('\n') },
  { id: 'w5090-test', source: 'user', label: 'w5090 원격 전체 테스트', revision: 1, cwd: '', prompt: '실패한 패키지만 요약하고 원인 파일:라인을 보고해줘', line: [
    'set -e',
    "ssh w5090 'cd ~/src/strata && go test -race -count=1 ./internal/... ./cmd/...'",
    'echo "[w5090] remote test suite done"'
  ].join('\n') }
];

async function openChromium(t, { colorScheme = 'dark', lang = 'en' } = {}) {
  const { chromium } = require(PW);
  const exe = process.env.WS_PLAYWRIGHT_EXECUTABLE;
  const browser = await chromium.launch({ headless: true, ...(exe ? { executablePath: exe } : {}) });
  t.after(() => browser.close());
  const ctx = await browser.newContext({ viewport: { width: 900, height: 1000 }, reducedMotion: 'reduce', colorScheme, deviceScaleFactor: 2 });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', e => errs.push(e.message));
  const table = JSON.parse(fs.readFileSync(path.resolve(__dirname, '../../wsi18n/tables/' + lang + '.json'), 'utf8'));
  const WB = fs.readFileSync(path.resolve(__dirname, '../../wswhiteboard/assets/page.css'), 'utf8');
  const sheet = [WB, css('dashboard.css'), css('dashboard-conversation.css'), css('dashboard-drawer.css'), css('dashboard-composer-ucmd.css'), css('dashboard-composer-ucmd-editor.css')].join('\n');
  const html = '<!doctype html><html><head><meta charset="utf-8"><style>' + sheet + '</style></head>' +
    '<body style="margin:0;padding:560px 24px 24px;width:780px;background:var(--wb-bg)">' +
    '<div id="composer" class="composer" data-cexec="1"><div class="cbadges"><span class="sbwrap"><button type="button" class="sbadge" id="sbadge" hidden></button></span></div>' +
    '<textarea id="ctext" rows="1"></textarea></div><button id="after" type="button">after</button></body></html>';
  await page.route('http://fx.test/', r => r.fulfill({ contentType: 'text/html', body: html }));
  await page.goto('http://fx.test/');
  const shim = 'var doc = document, $ = function (id) { return document.getElementById(id); }, rid = "fx";\n' +
    'var T = ' + JSON.stringify(table) + ';\n' +
    'function wsT(k) { var s = T[k] || k; for (var i = 1; i < arguments.length; i++) s = s.replace("{" + (i - 1) + "}", arguments[i]); return s; }\n' +
    'function wsTn(k, n) { return wsT(k + (n === 1 ? ".one" : ".other"), n); }\n' +
    'function noteWarn(m) { window.__warn = m; } function noteOk(m) { window.__ok = m; } function post(r, b, d) { d(true, {}); }\n' +
    'var composer = $("composer"), ctext = $("ctext"), cexecEnabled = true, cexecToken = "tok";\n' +
    'function cexecNewID() { return "sub-1"; } function cexecTrackSubmission() {} function cexecUnreachable(w) { return w; } function cexecList(cb) { cb([], ""); }\n' +
    'window.fetch = function () { return Promise.resolve({ ok: true, status: 200, json: function () { return Promise.resolve(window.__slots); }, text: function () { return Promise.resolve(JSON.stringify(window.__slots)); } }); };\n' +
    'window.__slots = ' + JSON.stringify({ rev: '1.1.x', token: 'tok', commands: LOOK_CMDS }) + ';\n';
  await page.addScriptTag({ content: fs.readFileSync(path.join(ASSETS, 'hold-confirm.js'), 'utf8') });
  const files = ['dashboard-composer-ucmd-editor.js', 'dashboard-composer-ucmd-card.js', 'dashboard-composer-ucmd.js'].map(f => fs.readFileSync(path.join(ASSETS, f), 'utf8')).join('\n');
  await page.addScriptTag({ content: '(function(){' + shim + files + '\n})();' });
  await page.click('#ucmdbadge');
  await page.waitForSelector('.ucmd-pop:not([hidden]) .ucmd-card');
  const look = async name => {
    const dir = process.env.WS_UCMD_LOOK_DIR;
    if (dir) await page.locator('.ucmd-pop').screenshot({ path: path.join(dir, 'live-' + lang + '-' + colorScheme + '-' + name + '.png') });
  };
  return { page, errs, look };
}

// align measures, for the first, middle and last glyph of every visible line,
// how far the painted backdrop glyph sits from where the textarea's own text
// grid puts it (same font advance and line height, same origin and scroll).
// dy is the glyph box offset inside a 16px line (half-leading), calibrated
// once at scroll 0 so a scroll desync cannot hide inside it.
function alignProbe({ s, dy }) {
  const ta = document.querySelector(s), code = ta.parentNode.querySelector('code');
  const cs = getComputedStyle(ta), cv = document.createElement('canvas').getContext('2d');
  cv.font = cs.fontWeight + ' ' + cs.fontSize + ' ' + cs.fontFamily;
  const adv = cv.measureText('0').width, lh = parseFloat(cs.lineHeight);
  const r = ta.getBoundingClientRect(), lines = ta.value.split('\n');
  const want = new Map();
  let at = 0;
  lines.forEach((l, li) => {
    [0, Math.floor(l.length / 2), l.length - 1].forEach(ci => { if (ci >= 0 && ci < l.length) want.set(at + ci, [li, ci]); });
    at += l.length + 1;
  });
  const walker = document.createTreeWalker(code, NodeFilter.SHOW_TEXT);
  let off = 0, n, worst = 0, checked = 0, firstDy = null;
  while ((n = walker.nextNode())) {
    for (let i = 0; i < n.data.length; i++) {
      const pos = want.get(off + i);
      if (!pos) continue;
      const rg = document.createRange();
      rg.setStart(n, i);
      rg.setEnd(n, i + 1);
      const g = rg.getBoundingClientRect();
      if (g.bottom < r.top || g.top > r.bottom || g.right < r.left || g.left > r.right) continue;
      const x = r.left + parseFloat(cs.paddingLeft) + pos[1] * adv - ta.scrollLeft;
      const y = r.top + parseFloat(cs.paddingTop) + pos[0] * lh - ta.scrollTop;
      if (firstDy === null) firstDy = g.top - y;
      worst = Math.max(worst, Math.abs(g.left - x), Math.abs(g.top - y - (dy || 0)));
      checked++;
    }
    off += n.data.length;
  }
  return { worst, checked, firstDy, st: ta.scrollTop, sl: ta.scrollLeft, sh: ta.scrollHeight, ch: ta.clientHeight };
}

for (const colorScheme of ['dark', 'light']) {
  test('Chromium ' + colorScheme + ': two-zone tiles, 26px collapsed rows, glyph-exact overlay at scroll extremes, 140px cap', { skip: pwSkip }, async t => {
    const { page, errs, look } = await openChromium(t, { colorScheme, lang: 'ko' });
    const tile = '.ucmd-card[data-ucmd-key="agent:strata-vs"]';
    const geo = await page.evaluate(sel => {
      const r = el => el.getBoundingClientRect();
      const bg = el => getComputedStyle(el).backgroundColor;
      const cards = [...document.querySelectorAll('.ucmd-card')];
      const t = document.querySelector(sel);
      const p = t.querySelector('.ucmd-cmd-preview');
      return {
        row: r(t.querySelector('.ucmd-cmd-row')).height,
        gap: r(cards[1]).top - r(cards[0]).bottom,
        tray: bg(document.querySelector('.ucmd-pop-body')),
        band: bg(t.querySelector('.ucmd-band')),
        well: bg(t.querySelector('.ucmd-well')),
        nested: [...t.querySelectorAll('.ucmd-well *')].filter(e => getComputedStyle(e).backgroundColor !== 'rgba(0, 0, 0, 0)' && !e.closest('.ucmd-lines-pill, .ucmd-rev-btn') && !e.matches('.ucmd-divider, .ucmd-collapse-btn')).map(e => e.className),
        clipped: p.scrollWidth > p.clientWidth,
        overflow: getComputedStyle(p).textOverflow,
        ph: r(p).height
      };
    }, tile);
    assert.ok(geo.row >= 25.5 && geo.row <= 27, 'collapsed $ row is ~26px: ' + geo.row);
    assert.ok(Math.abs(geo.gap - 10) < 0.6, 'tiles are 10px apart: ' + geo.gap);
    assert.notEqual(geo.band, geo.well, 'header band and body well are distinct tonal zones');
    assert.notEqual(geo.tray, geo.well, 'tray is distinct from the tile');
    assert.notEqual(geo.tray, geo.band);
    assert.deepEqual(geo.nested, [], 'no nested capsules inside the well at rest');
    assert.equal(geo.clipped, true, 'long multi-line preview is clipped to one line');
    assert.equal(geo.overflow, 'ellipsis');
    assert.equal(geo.ph, 16);
    await look('collapsed');

    await page.click(tile + ' .ucmd-cmd-row');
    const sel = tile + ' .ucmd-code-ta';
    assert.equal(await page.evaluate(s => document.activeElement === document.querySelector(s), sel), true);
    const metrics = await page.evaluate(s => {
      const ta = document.querySelector(s), pre = ta.parentNode.querySelector('.ucmd-code-hl');
      const props = ['fontFamily', 'fontSize', 'fontWeight', 'fontStyle', 'lineHeight', 'letterSpacing', 'wordSpacing', 'tabSize', 'paddingTop', 'paddingRight', 'paddingBottom', 'paddingLeft', 'borderTopWidth', 'borderLeftWidth', 'whiteSpace', 'wordBreak', 'overflowWrap', 'boxSizing', 'fontVariantLigatures', 'fontKerning', 'textIndent'];
      const a = getComputedStyle(ta), b = getComputedStyle(pre);
      const ra = ta.getBoundingClientRect(), rb = pre.getBoundingClientRect();
      return { diff: props.filter(p => a[p] !== b[p]).map(p => p + ':' + a[p] + '|' + b[p]), rect: [ra.left - rb.left, ra.top - rb.top, ra.width - rb.width, ra.height - rb.height], h: ra.height, ink: a.webkitTextFillColor, caret: a.caretColor };
    }, sel);
    assert.deepEqual(metrics.diff, [], 'pre and textarea share every text metric');
    assert.ok(metrics.rect.every(v => Math.abs(v) < 0.01), 'pre exactly covers the textarea: ' + metrics.rect);
    assert.ok(Math.abs(metrics.h - 140) < 0.6, '10-line command caps at 140px: ' + metrics.h);
    assert.equal(metrics.ink, 'rgba(0, 0, 0, 0)', 'native text ink is transparent');
    assert.notEqual(metrics.caret, 'rgba(0, 0, 0, 0)', 'caret stays visible');
    await look('expanded');

    let a = await page.evaluate(alignProbe, { s: sel, dy: 0 });
    assert.equal(a.st, 0, 'first open starts at the top like the preview');
    const dy = a.firstDy;
    assert.ok(dy >= 0 && dy < 3, 'glyph box sits inside its 16px line: ' + dy);
    a = await page.evaluate(alignProbe, { s: sel, dy });
    assert.ok(a.checked > 5 && a.worst < 0.75, 'aligned at rest: ' + JSON.stringify(a));
    await page.evaluate(s => { const ta = document.querySelector(s); ta.scrollTop = ta.scrollHeight; ta.scrollLeft = ta.scrollWidth; }, sel);
    await page.waitForFunction(s => document.querySelector(s).parentNode.querySelector('code').style.transform !== '', sel);
    a = await page.evaluate(alignProbe, { s: sel, dy });
    assert.ok(a.sh > a.ch && a.st > 0 && a.sl > 0, 'scrolled to both extremes: ' + JSON.stringify(a));
    assert.ok(a.checked > 3 && a.worst < 0.75, 'aligned at the bottom-right scroll extreme: ' + JSON.stringify(a));
    await look('scrolled');

    // Native typing at the end, including a Shift+Enter newline.
    await page.keyboard.press('Control+End');
    await page.keyboard.press('Shift+Enter');
    await page.keyboard.type('echo "done" && exit 0');
    const typed = await page.evaluate(s => { const ta = document.querySelector(s); return { v: ta.value, c: ta.parentNode.querySelector('code').textContent }; }, sel);
    assert.equal(typed.c, typed.v, 'backdrop text tracks native typing');
    await page.waitForTimeout(50);
    a = await page.evaluate(alignProbe, { s: sel, dy });
    assert.ok(a.checked > 3 && a.worst < 0.75, 'aligned after typing at the end: ' + JSON.stringify(a));

    // Esc folds back to the preview showing the draft; the popover stays.
    await page.keyboard.press('Escape');
    const folded = await page.evaluate(s => ({
      open: document.querySelector(s + ' .ucmd-cmd-box').classList.contains('is-expanded'),
      pop: !document.querySelector('.ucmd-pop').hidden,
      dirty: document.querySelector(s).classList.contains('is-dirty'),
      pill: document.querySelector(s + ' .ucmd-lines-pill').textContent
    }), tile);
    assert.deepEqual(folded, { open: false, pop: true, dirty: true, pill: '+10줄' });

    // A 3-line command opens at 3 x 16 + 10px; a press outside folds it.
    const short = '.ucmd-card[data-ucmd-key="user:w5090-test"]';
    await page.click(short + ' .ucmd-cmd-row');
    const sh = await page.evaluate(s => document.querySelector(s + ' .ucmd-code-ta').getBoundingClientRect().height, short);
    assert.ok(Math.abs(sh - 58) < 0.6, '3 lines = 58px: ' + sh);
    await page.click(short + ' .ucmd-inline-input.is-prompt');
    assert.equal(await page.evaluate(s => document.querySelector(s + ' .ucmd-cmd-box').classList.contains('is-expanded'), short), false, 'outside press folds');
    await look('dirty');
    assert.deepEqual(errs, []);
  });
}
