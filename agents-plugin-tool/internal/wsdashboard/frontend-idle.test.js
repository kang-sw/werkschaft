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
  const ctx = { doc, shHidden: false, tickTimes() { ticks++; },
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
  assert.match(rail, /rail-orbit 1\.3s steps\(12\) infinite/);
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
  assert.doesNotMatch(all, /@keyframes[^{]+\{\s*to\s*\{\s*--/);
  // Box-shadow never animates in a perpetual keyframe set; only opacity or transform.
  for (const name of ['hbspin', 'hud-spin-think', 'hud-spin-tool', 'xh-spin', 'hud-hold-breathe', 'hud-perm-pulse', 'attn-flash-ring', 'attn-breathe-ring']) {
    const m = all.match(new RegExp(String.raw`^@keyframes ${name}\b[^\n]*\n(?:[ \t][^\n]*\n)*`, 'm'));
    assert(m, `@keyframes ${name}`);
    assert.doesNotMatch(m[0], /box-shadow|--[a-z]/, `${name} animates only transform or opacity`);
  }
  for (const [css, sel] of [[base, '.hb .hbr::before'], [conversation, '.hud-node[data-state="thinking"] .hud-ring::before'], [conversation, '.hud-node[data-state="tool"] .hud-ring::before'], [exec, '[data-st="running"] .xring::before']]) {
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
  for (const name of ['hud-spin-think', 'hud-spin-tool', 'hud-hold-breathe', 'hud-perm-pulse']) {
    assert.match(conversation, new RegExp(`@keyframes ${name}\\b`), `missing @keyframes ${name}`);
  }
  assert.match(base, /:root\[data-animation-paused\]\s+\.hud-node\[data-state\]\s+\.hud-ring\s*\{\s*animation-play-state:\s*paused;\s*\}/);
  assert.match(base, /@media \(prefers-reduced-motion: reduce\)\s*\{\s*\.hud-node\[data-state\]\s+\.hud-ring\s*\{\s*animation:\s*none;\s*\}\s*\}/);
  assert.match(conversation, /@media \(prefers-reduced-motion: reduce\)\s*\{[^@]*\.hud-node\[data-state\]\s+\.hud-ring\s*\{\s*animation:\s*none;\s*\}/);
});
