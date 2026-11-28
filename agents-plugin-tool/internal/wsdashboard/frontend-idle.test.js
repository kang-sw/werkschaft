const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const dashboard = path.join(__dirname, 'assets');
const whiteboard = path.join(__dirname, '../wswhiteboard/assets');
function read(dir, file) { return fs.readFileSync(path.join(dir, file), 'utf8'); }

test('whiteboard clock skips hidden pages and catches up once shown', () => {
  const code = read(whiteboard, 'ago.js');
  let now = 60000, paused = false, queries = 0, timer, starts = 0, stops = 0;
  const handlers = {};
  const clock = { textContent: '', getAttribute() { return '0'; } };
  const document = {
    hidden: false,
    documentElement: { hasAttribute(name) { return name === 'data-animation-paused' && paused; } },
    querySelectorAll(selector) { queries++; return selector === 'time.tago[data-at]' ? [clock] : []; },
    addEventListener(type, fn) { (handlers[type] ||= []).push(fn); }
  };
  const window = { wsT: true, wbBodyAgeHover: true };
  const context = { window, document, wsT: (key, n) => key + (n === undefined ? '' : ':' + n), Date: { now: () => now }, setInterval(fn) { starts++; timer = fn; return starts; }, clearInterval() { stops++; timer = null; } };
  vm.createContext(context);
  vm.runInContext(code, context);
  assert.equal(clock.textContent, 'js.wb.ago.min:1');
  document.hidden = true;
  now = 180000;
  const before = queries;
  handlers.visibilitychange[0]();
  assert.equal(timer, null);
  assert.equal(stops, 1);
  window.wbAgo(document);
  assert.equal(queries, before);
  assert.equal(clock.textContent, 'js.wb.ago.min:1');
  document.hidden = false; paused = true;
  handlers.visibilitychange[0]();
  assert.equal(timer, null);
  assert.equal(queries, before);
  paused = false;
  handlers['ws-page-visibility'][0]();
  assert.equal(starts, 2);
  assert.equal(clock.textContent, 'js.wb.ago.min:3');
  vm.runInContext(code, context);
  assert.equal(handlers.visibilitychange.length, 1);
  assert.equal(handlers['ws-page-visibility'].length, 1);
  assert.equal(starts, 2, 're-executing the asset does not add a clock');
  paused = true; handlers['ws-page-visibility'][0]();
  assert.equal(timer, null);
});

test('dashboard tick interval stops and restarts across visibility changes', () => {
  const code = read(dashboard, 'dashboard.js');
  const block = code.match(/  var tickTimer = null;[\s\S]*?  syncTickTimer\(\);/)[0];
  const handlers = {}, active = new Map(); let id = 0, ticks = 0;
  const doc = { hidden: false, addEventListener(type, fn) { handlers[type] = fn; } };
  const ctx = { doc, shHidden: false, docHidden() { return doc.hidden; }, tickTimes() { ticks++; },
    setInterval(fn) { active.set(++id, fn); return id; }, clearInterval(n) { active.delete(n); } };
  vm.runInNewContext(block, ctx);
  assert.equal(active.size, 1); assert.equal(ticks, 1);
  ctx.shHidden = true; handlers['ws-page-visibility']();
  assert.equal(active.size, 0);
  doc.hidden = true; ctx.shHidden = false; handlers['ws-page-visibility']();
  assert.equal(active.size, 0);
  doc.hidden = false; handlers['ws-page-visibility'](); handlers['ws-page-visibility']();
  assert.equal(active.size, 1); assert.equal(ticks, 3);
});

test('idle CSS bounds animation rates and contains without paint or size containment', () => {
  const base = read(dashboard, 'dashboard.css');
  const rail = read(dashboard, 'dashboard-rail.css');
  const conversation = read(dashboard, 'dashboard-conversation.css');
  const layout = read(dashboard, 'dashboard-layout.css');
  const clocks = read(whiteboard, 'page-items.css');
  assert.match(base, /dash-spin \.8s steps\(12\) infinite/);
  assert.match(base, /dash-pulse 1\.6s steps\(4\) infinite/);
  assert.match(base, /\.hb \.hbr::before\s*\{[^}]*animation: hbspin 1\.1s linear infinite/);
  assert.match(rail, /rail-blink 1\.4s steps\(4\) infinite/);
  assert.match(conversation, /dash-spin \.8s steps\(12\) infinite/);
  assert.match(base, /:root\[data-animation-paused\][^{]+\{\s*animation-play-state: paused/);
  assert.match(conversation, /\.composer \{ contain: layout style;/);
  // The board column takes no containment: the descendant :has() that
  // released it re-matched the whole board on every DOM change. Uncontained,
  // fixed sheets and popovers use the viewport, and Files, portaled to body,
  // keeps its toolbar controls above the body-level scrim.
  assert.doesNotMatch(layout, /#bcol[^{]*\{[^}]*contain/);
  assert.doesNotMatch(layout, /#bcol:has\(/);
  const board = read(dashboard, 'dashboard-board.js');
  assert.match(board, /sheet\.id = "toolbar-files"/);
  assert.match(board, /doc\.body\.appendChild\(sheet\)/);
  assert.match(clocks, /time\.tago, time\.entry-at\[data-at\] \{[^}]*contain: layout style;/);
});

test('conic spinners rotate a pseudo-element by transform and every pseudo animation pauses and honours reduced motion', () => {
  const base = read(dashboard, 'dashboard.css');
  const conversation = read(dashboard, 'dashboard-conversation.css');
  const exec = read(dashboard, 'dashboard-exec-hud.css');
  const rail = read(dashboard, 'dashboard-rail.css');
  const all = base + conversation + exec + rail;
  // No angle custom property animates inside a gradient (main-thread repaint every frame).
  assert.doesNotMatch(all, /@property --(hba|hud-spin-[ab])/);
  assert.doesNotMatch(all, /@property[^{]*\{[^}]*syntax:\s*["']<angle>["']/);
  assert.doesNotMatch(all, /@keyframes[^{]+\{\s*to\s*\{\s*--/);
  // Typed color properties allow 0.5s color transitions without animating the gradient angle.
  assert.match(conversation, /@property --d-hud-ring-arc\s*\{\s*syntax:\s*"<color>";\s*inherits:\s*true;/);
  assert.match(conversation, /@property --d-hud-ring-tip\s*\{\s*syntax:\s*"<color>";\s*inherits:\s*true;/);
  assert.match(conversation, /\.hud-ring\s*\{[^}]*transition:\s*[^;}]*background-color 0\.5s ease[^;}]*--d-hud-ring-arc 0\.5s ease[^;}]*--d-hud-ring-tip 0\.5s ease/);
  assert.match(conversation, /\.hud-node:is\(\[data-state="thinking"\], \[data-state="tool"\]\) \.hud-ring\s*\{[^}]*contain:\s*paint;/);
  assert.doesNotMatch(conversation, /\.hud-node\[data-state="(?:holding|permission)"\] \.hud-ring\s*\{[^}]*contain:\s*paint/);
  // Box-shadow never animates in a perpetual keyframe set; only opacity or transform.
  for (const name of ['hbspin', 'hud-spin', 'xh-spin', 'hud-hold-breathe', 'hud-perm-pulse', 'attn-flash-ring', 'attn-breathe-ring']) {
    const m = all.match(new RegExp(String.raw`^@keyframes ${name}\b[^\n]*\n(?:[ \t][^\n]*\n)*`, 'm'));
    assert(m, `@keyframes ${name}`);
    assert.doesNotMatch(m[0], /box-shadow|--[a-z]/, `${name} animates only transform or opacity`);
  }
  for (const [css, sel] of [[base, '.hb .hbr::before'], [conversation, '.hud-node:is([data-state="thinking"], [data-state="tool"]) .hud-ring::before'], [exec, '[data-st="running"] .xring::before']]) {
    const i = css.indexOf(sel + ' {'); assert(i >= 0, sel);
    assert.match(css.slice(i, css.indexOf('}', i)), /conic-gradient\([^)]*\)?[^}]*animation: [a-z-]+ [^;]+;/, `${sel} carries the rotating gradient`);
  }
  assert.match(base, /:root\[data-animation-paused\] :is\(\.hb \.hbr, \.hud-node\[data-state\] \.hud-ring\)::before,\s*:root\[data-animation-paused\] \.hud-node\[data-state\] \.hud-ring::after\s*\{\s*animation-play-state: paused;/);
  assert.match(exec, /:root\[data-animation-paused\] \.xring::before/);
  assert.match(rail, /\.hb \.hbr::before \{ animation: none; \}/);
  assert.match(base, /\.hud-node\[data-state\] \.hud-ring::before, \.hud-node\[data-state\] \.hud-ring::after \{ animation: none; \}/);
});

test('continuous motion stays off the main thread: hidden subtrees pause and no bar animates width', () => {
  const rail = read(dashboard, 'dashboard-rail.css');
  const drawer = read(dashboard, 'dashboard-drawer.css');
  const tracker = read(dashboard, 'tracker.css');
  const conversation = read(dashboard, 'dashboard-conversation.css');
  const diff = read(whiteboard, 'page-panel-diff.css');
  // An animation under a visibility: hidden target cannot be composited and ticks every frame.
  const pause = ' { animation-play-state: paused !important; }';
  assert(rail.includes(':root:not(.rail-open) .rail *::after' + pause), 'closed rail drawer pauses');
  assert(drawer.includes('.drawer:not(.open) .dr-clip *::after' + pause), 'closed drawer pauses');
  assert(tracker.includes('.tk.tk-drawer:not(.open) .tk-dr-clip *::after' + pause), 'closed tracker drawer pauses');
  assert(conversation.includes('.agisland:not([data-mode="compact"]) > .agcompact *::after,'), 'hidden compact HUD pauses');
  assert(conversation.includes('.agisland:not([data-mode="full"]) > .agpop *::after' + pause), 'hidden full popover pauses');
  const media = rail.indexOf('@media (max-width: 1599.98px) {');
  assert(media >= 0 && rail.indexOf(':root:not(.rail-open) .rail *,', media) > media, 'the rail pauses only where it is a hidden drawer');
  // Bars and fills grow by transform, never by an animated width or left.
  assert.match(rail, /\.gal-pullfill \{[^}]*width: 100%;[^}]*transform: scaleX\(0\);[^}]*transition: transform \.06s linear;/);
  assert.match(diff, /\.pdg-bar \{[^}]*transition: transform \.1s;/);
  assert.match(diff, /\.pdg-hit:hover \.pdg-bar[^{]*\{ transform: scaleX\(1\.6667\); \}/);
  assert.doesNotMatch(diff + rail + conversation, /transition: [^;}]*\b(width|left) \.(06|1|12)s/);
  assert.doesNotMatch(conversation.match(/\.sb-bar \.fill \{[^}]*\}/)[0], /transition/);
  assert.match(read(dashboard, 'cards.js'), /fill\.style\.transform = f/);
});

test('HUD node animations pause when hidden and disable under reduced motion', () => {
  const base = read(dashboard, 'dashboard.css');
  const conversation = read(dashboard, 'dashboard-conversation.css');
  for (const name of ['hud-spin', 'hud-hold-breathe', 'hud-perm-pulse']) {
    assert.match(conversation, new RegExp(`@keyframes ${name}\\b`), `missing @keyframes ${name}`);
  }
  assert.match(base, /:root\[data-animation-paused\]\s+\.hud-node\[data-state\]\s+\.hud-ring\s*\{\s*animation-play-state:\s*paused;\s*\}/);
  assert.match(base, /@media \(prefers-reduced-motion: reduce\)\s*\{\s*\.hud-node\[data-state\]\s+\.hud-ring\s*\{\s*animation:\s*none;\s*\}\s*\}/);
  assert.match(conversation, /@media \(prefers-reduced-motion: reduce\)\s*\{[^@]*\.hud-ring,\s*\.hud-ring::before[^}]*transition:\s*none !important;/);
  assert.match(conversation, /@media \(prefers-reduced-motion: reduce\)\s*\{[^@]*\.hud-node\[data-state\]\s+\.hud-ring\s*\{\s*animation:\s*none;\s*\}/);

  const voice = read(dashboard, 'dashboard-voice.css');
  const pulse = voice.match(/^@keyframes cvox-pulse\b[^\n]*\n(?:[ \t][^\n]*\n)*/m);
  assert(pulse, 'missing @keyframes cvox-pulse');
  assert.doesNotMatch(pulse[0], /box-shadow|width|height|left|top|--[a-z]/, 'cvox-pulse animates only opacity/transform');
  assert.match(voice, /:root\[data-animation-paused\]\s+\.cvox-cap\[data-state\]\s+\.cvox-dot\s*\{\s*animation-play-state:\s*paused !important;\s*\}/);
  assert.match(voice, /@media \(prefers-reduced-motion: reduce\)\s*\{[^@]*\.cvox-cap\[data-state\]\s+\.cvox-dot\s*\{\s*animation:\s*none !important;\s*\}/);
});

function findForbiddenHas(css, file) {
  const src = css.replace(/\/\*[\s\S]*?\*\//g, '');
  const violations = [];
  const highAncestorRe = /(?:^|(?<![a-zA-Z0-9_-]))(:root|html|body|\.dash|\.dbody|\.dwrap|\.dgrid)(?![a-zA-Z0-9_-])/;
  const stripBrackets = s => {
    let out = '', depth = 0, open = '';
    for (let i = 0; i < s.length; i++) {
      const ch = s[i];
      if (!depth && (ch === '[' || ch === '(')) { depth = 1; open = ch; continue; }
      if (depth) {
        if (ch === open) depth++;
        else if ((open === '[' && ch === ']') || (open === '(' && ch === ')')) depth--;
        continue;
      }
      out += ch;
    }
    return out;
  };
  const topBranches = body => {
    const out = []; let depth = 0, start = 0;
    for (let i = 0; i < body.length; i++) {
      const ch = body[i];
      if (ch === '(' || ch === '[') depth++;
      else if (ch === ')' || ch === ']') depth--;
      else if (depth === 0 && ch === ',') { out.push(body.slice(start, i)); start = i + 1; }
    }
    out.push(body.slice(start));
    return out;
  };
  const compoundBefore = pos => {
    let i = pos - 1;
    const parts = [];
    while (i >= 0) {
      const ch = src[i];
      if (ch === ')' || ch === ']') {
        const close = ch, open = ch === ')' ? '(' : '[';
        let depth = 1, end = i;
        i--;
        while (i >= 0 && depth > 0) {
          if (src[i] === close) depth++;
          else if (src[i] === open) depth--;
          i--;
        }
        parts.unshift(src.slice(i + 1, end + 1));
        continue;
      }
      if (ch === '(') {
        i--;
        while (i >= 0 && /[a-zA-Z0-9_-]/.test(src[i])) i--;
        if (i >= 0 && src[i] === ':') i--;
        continue;
      }
      if (/[\s,>+~{};]/.test(ch)) {
        if (ch === ',' || /\s/.test(ch)) {
          let j = i, depth = 0, foundOpen = -1;
          while (j >= 0) {
            const c = src[j];
            if (c === ')' || c === ']') depth++;
            else if (c === '(' || c === '[') {
              if (depth === 0) { foundOpen = j; break; }
              depth--;
            } else if (depth === 0 && (c === '{' || c === '}' || c === ';')) break;
            j--;
          }
          if (foundOpen >= 0 && src[foundOpen] === '(') {
            const branch = src.slice(foundOpen + 1, pos);
            const cur = topBranches(branch).at(-1).trim();
            if (cur === '' || !/[\s>+~]/.test(cur)) { i = foundOpen; continue; }
          }
        }
        break;
      }
      parts.unshift(ch);
      i--;
    }
    return parts.join('');
  };
  const re = /:has\(/g;
  let m;
  while ((m = re.exec(src))) {
    const start = m.index, bodyStart = start + 5;
    let depth = 1, end = bodyStart;
    while (end < src.length && depth > 0) {
      if (src[end] === '(') depth++;
      else if (src[end] === ')') depth--;
      end++;
    }
    const body = src.slice(bodyStart, end - 1);
    const expr = src.slice(start, end);
    if (topBranches(body).some(b => /^\s*[+~]/.test(b))) {
      violations.push(`${file}: sibling ${expr}`);
    }
    if (/:empty\b/.test(body)) {
      violations.push(`${file}: :empty inside ${expr}`);
    }
    const compound = stripBrackets(compoundBefore(start));
    if (highAncestorRe.test(compound)) {
      violations.push(`${file}: high-ancestor ${compound}${expr}`);
    }
  }
  return violations;
}

test('CSS avoids high-cost :has() selectors across dashboard and whiteboard assets', () => {
  // Verify the detector catches each forbidden class while permitting local component :has().
  assert.equal(findForbiddenHas('.a:has(+ .b) {}', 'x.css').length, 1);
  assert.equal(findForbiddenHas('.a:has(~ .b) {}', 'x.css').length, 1);
  assert.equal(findForbiddenHas('.a:has(> .b:empty) {}', 'x.css').length, 1);
  assert.equal(findForbiddenHas(':root[data-x]:has(.b) {}', 'x.css').length, 1);
  assert.equal(findForbiddenHas('body:has(.b) {}', 'x.css').length, 1);
  assert.equal(findForbiddenHas('.dgrid:is(.live, :has(> #c)) {}', 'x.css').length, 1);
  assert.equal(findForbiddenHas('.dgrid.live > .bcol:has(> .r-board > .empty) {}', 'x.css').length, 0);
  assert.equal(findForbiddenHas('.dgrid.live > .agisland[data-mode="compact"]:is(:hover, :has(> .agcompact:focus-visible)) {}', 'x.css').length, 0);

  const violations = [];
  for (const [label, dir] of [['wsdashboard', dashboard], ['wswhiteboard', whiteboard]]) {
    for (const file of fs.readdirSync(dir).filter(f => f.endsWith('.css')).sort()) {
      violations.push(...findForbiddenHas(read(dir, file), `${label}/${file}`));
    }
  }
  assert.deepEqual(
    violations,
    [],
    'Forbidden :has() selectors violate ai-docs/manuals/performance.md (replace with explicit data-* / hidden / :empty):\n' + violations.join('\n')
  );
});

test('exec HUD 1-second tick skips layout when elapsed digit length is unchanged', () => {
  const css = read(dashboard, 'dashboard-exec-hud.css');
  assert.match(css, /^\.xel\s*\{[^}]*font-variant-numeric:\s*tabular-nums;/m);

  const js = read(dashboard, 'dashboard-exec-hud.js');
  const fmtFn = js.match(/\n  function xhFmtElapsed\([\s\S]*?\n  \}/)[0];
  const renderFn = js.match(/\n  function xhRender\([\s\S]*?\n  \}/)[0];
  const tickFn = js.match(/\n  function xhTickText\([\s\S]*?\n  \}/)[0];

  let now = 5000, layouts = 0, textWrites = 0, textVal = '';
  const elapsedNode = {
    getAttribute(k) { return k === 'data-xsince' ? '0' : null; },
    get textContent() { return textVal; },
    set textContent(v) { textWrites++; textVal = v; }
  };
  const group = {
    contains() { return false; },
    appendChild() {},
    set textContent(_) {}
  };
  const job = { id: 'j1', started_at: 0 };
  const ctx = {
    Date: { now: () => now },
    window: {},
    doc: { activeElement: null },
    xhEnabled: true, xhAbsent: false, xhSig: '', xhHidden: false,
    xhVis: [], xhSlots: [], xhJobs: [job], xhGroup: group, xhMore: {}, xhAgg: {},
    xhTip: null, xhPeekEl: null, XH_MAX_CHIPS: 3,
    xhVisible: () => [job],
    xhGroupEl: () => group,
    xhSt: () => 'running',
    xhPeekOpen: () => false,
    xhPeekId: () => '',
    xhBuildChip: () => { textVal = '0:05'; return {}; },
    xhLayout() { layouts++; },
    xhSchedule() {},
    xhPeekSync() {},
    xhButtons: () => [],
    q(sel, root) { return root === group && sel === '[data-xsince]' ? [elapsedNode] : []; }
  };
  vm.createContext(ctx);
  vm.runInContext(fmtFn + '\n' + renderFn + '\n' + tickFn, ctx);

  ctx.xhRender(false);
  assert.equal(layouts, 1, 'initial build lays out once');
  assert.equal(textVal, '0:05');

  now = 6000;
  ctx.xhRender(false);
  assert.equal(textVal, '0:06');
  assert.equal(textWrites, 1);
  assert.equal(layouts, 1, 'same-length tick skips xhLayout');

  ctx.xhRender(false);
  assert.equal(textWrites, 1, 'unchanged elapsed string skips DOM textContent write');
  assert.equal(layouts, 1);

  now = 600000;
  ctx.xhRender(false);
  assert.equal(textVal, '10:00');
  assert.equal(layouts, 2, 'digit-length change triggers xhLayout');
});

test('explicit state attributes sync across consent banner, settings popover, audit badges, and whiteboard gaps', () => {
  const makeAttrs = () => {
    const map = new Map();
    return {
      setAttribute(k, v) { map.set(k, String(v)); },
      removeAttribute(k) { map.delete(k); },
      hasAttribute(k) { return map.has(k); },
      getAttribute(k) { return map.has(k) ? map.get(k) : null; }
    };
  };

  // 1. Account consent banner -> :root[data-account-banner]
  const streamJs = read(dashboard, 'dashboard-stream.js');
  const consentFn = streamJs.match(/\n  function setConsentBannerHidden\([\s\S]*?\n  \}/)[0];
  const root = Object.assign(makeAttrs(), { style: { setProperty() {}, removeProperty() {} } });
  const consentBanner = { hidden: true };
  const consentCtx = { root, consentBanner, consentResize: null };
  vm.createContext(consentCtx);
  vm.runInContext(consentFn, consentCtx);
  consentCtx.setConsentBannerHidden(false);
  assert.equal(root.hasAttribute('data-account-banner'), true);
  consentCtx.setConsentBannerHidden(true);
  assert.equal(root.hasAttribute('data-account-banner'), false);

  // 2. Settings popover -> :root[data-settings-open]
  const popJs = read(dashboard, 'dashboard-rail-popovers.js');
  const popBlock = popJs.match(/\n  function railPopClose\([\s\S]*?\n  function railPopOpen\([\s\S]*?\n  \}/)[0];
  const gmenu = { hidden: true, parentNode: {}, focus() {} };
  const btn = Object.assign(makeAttrs(), { closest() { return null; }, focus() {} });
  const popCtx = {
    window: {}, root, doc: { body: { appendChild() {} } }, gmenu,
    rp: { P: null, kind: '', ws: '', btn: null },
    layoutMode: 'auto', inputMode: 'enter',
    rpKey: () => 'settings',
    railPopBtn: () => btn,
    railPopPlace() {},
    railPopScrim() {},
    setLayout() {}, setInput() {}, markTheme() {}, markLang() {}
  };
  vm.createContext(popCtx);
  vm.runInContext(popBlock, popCtx);
  popCtx.railPopOpen('settings', btn);
  assert.equal(root.hasAttribute('data-settings-open'), true);
  popCtx.railPopClose(false);
  assert.equal(root.hasAttribute('data-settings-open'), false);

  // 3. Audit boundary badge -> .boundary-badge[data-has-audit]
  const auditJs = read(dashboard, 'dashboard-audit-rows.js');
  assert.match(auditJs, /badge\.setAttribute\("data-has-audit", ""\)/);
  assert.match(auditJs, /badge\.removeAttribute\("data-has-audit"\)/);

  // 4. Whiteboard history gap -> data-gap-dir and .gap-sep[hidden]
  const wbStreamJs = read(whiteboard, 'stream.js');
  const gapFn = wbStreamJs.match(/\n  function syncGapAttrs\([\s\S]*?\n  \}/)[0];
  const gapCtx = {};
  vm.createContext(gapCtx);
  vm.runInContext(gapFn, gapCtx);
  const up = { hidden: false }, down = { hidden: true }, sep = { hidden: false }, range = { textContent: '1–5' };
  const gap = Object.assign(makeAttrs(), {
    querySelector(sel) {
      if (sel === '[data-load="up"]') return up;
      if (sel === '[data-load="down"]') return down;
      if (sel === '.gap-sep') return sep;
      if (sel === '.gap-range') return range;
      return null;
    }
  });
  gapCtx.syncGapAttrs(gap);
  assert.equal(gap.getAttribute('data-gap-dir'), 'up');
  assert.equal(sep.hidden, false);
  down.hidden = false;
  gapCtx.syncGapAttrs(gap);
  assert.equal(gap.getAttribute('data-gap-dir'), 'both');
  up.hidden = true; range.textContent = '';
  gapCtx.syncGapAttrs(gap);
  assert.equal(gap.getAttribute('data-gap-dir'), 'down');
  assert.equal(sep.hidden, true);
});
