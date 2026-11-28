// Harness-free: node internal/wsdashboard/testdata/rail-attention.test.cjs
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const window = {};
vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../assets/rail.js'), 'utf8'), { window });

function row(id, asks) {
  const attrs = { 'data-rid': id };
  if (asks !== undefined) attrs['data-asks'] = asks;
  const classes = new Set(['rs']);
  const styles = Object.create(null);
  const link = {
    setAttribute: (k, v) => { link[k] = v; },
    removeAttribute: k => { delete link[k]; }
  };
  const listeners = new Set();
  return {
    listeners,
    styles,
    style: {
      setProperty: (k, v) => { styles[k] = String(v); },
      removeProperty: k => { delete styles[k]; },
      getPropertyValue: k => styles[k] || ''
    },
    addEventListener: (t, f) => { if (t === 'animationend') listeners.add(f); },
    removeEventListener: (t, f) => { if (t === 'animationend') listeners.delete(f); },
    fire: name => [...listeners].forEach(f => f({ animationName: name })),
    getAttribute: k => attrs[k] ?? null,
    setAttribute: (k, v) => { attrs[k] = v; },
    querySelector: () => link,
    classList: {
      contains: k => classes.has(k),
      add: k => classes.add(k),
      remove: k => classes.delete(k),
      toggle: (k, on) => on ? classes.add(k) : classes.delete(k)
    }
  };
}
const flagged = r => r.classList.contains('attn');
const flashing = r => r.classList.contains('attn-flash');

// --- Core baseline, ID deltas, current/show, replacement phase, reconnect ----
let now = 1000;
const state = window.WsRail.attention({ now: () => now });
let a = row('away', '12'), current = row('shown', ''), old = row('old');
const sync = () => state.sync([a, current, old], 'shown', 'New question');

sync();
assert.equal(flagged(a), false, 'first observation silently baselines');
a.setAttribute('data-asks', '12 15');
sync();
assert.equal(flagged(a), true, 'new ID away flags');
assert.equal(flashing(a), true, 'new flag flashes');
assert.equal(a.style.getPropertyValue('--attn-t'), '', 'fresh flash starts at default 0s without inline offset');
assert.equal(a.style.getPropertyValue('--attn-breathe-delay'), '', 'fresh flash has no breathe delay override');
assert.equal(a.querySelector().title, 'New question', 'flag has accessible hint');

// Same live unmoved node keeps running animation without style churn.
now += 200;
sync();
assert.equal(flashing(a), true, 'same node keeps its running animation');
assert.equal(a.style.getPropertyValue('--attn-t'), '', 'unmoved live node inline style is untouched');

// Replacement mid-flash (t = 400ms < 1140ms): preserves remaining flash with -0.4s offset.
now += 200; // elapsed = 400ms
a = row('away', '12 15');
sync();
assert.equal(flagged(a), true, 'mid-flash row replacement keeps flag');
assert.equal(flashing(a), true, 'mid-flash row replacement continues remaining flash');
assert.equal(a.style.getPropertyValue('--attn-t'), '0.4s', 'mid-flash replacement shifts flash & breathe by -0.4s via --attn-t');
assert.equal(a.style.getPropertyValue('--attn-breathe-delay'), '', 'mid-flash replacement keeps breathe delay unset');
assert.equal(a.listeners.size, 1, 'mid-flash replacement attaches animationend listener');

// Already-flagged unread session receiving a 2nd ask must NOT re-flash or reset startedAt.
now += 200; // elapsed = 600ms
a.setAttribute('data-asks', '12 15 18');
sync();
assert.equal(flagged(a), true, '2nd ask keeps existing flag');
assert.equal(a.style.getPropertyValue('--attn-t'), '0.4s', '2nd ask on unmoved flagged node does not reset animation');

// Replacement post-flash (t = 1540ms >= 1140ms): seamless breathe phase, never replays flash.
now += 940; // elapsed = 1540ms => breathe elapsed = 400ms
a = row('away', '12 15 18');
sync();
assert.equal(flagged(a), true, 'post-flash row patch keeps flag');
assert.equal(flashing(a), false, 'post-flash row patch never replays flash');
assert.equal(a.style.getPropertyValue('--attn-t'), '', 'post-flash replacement clears --attn-t');
assert.equal(a.style.getPropertyValue('--attn-breathe-delay'), '-0.4s', 'post-flash replacement inherits exact breathe phase');

// Later replacement wraps cleanly modulo 1000ms (t = 2740ms => phase 600ms).
now += 1200; // elapsed = 2740ms => breathe elapsed = 1600ms => phase 600ms
a = row('away', '12 15 18');
sync();
assert.equal(flashing(a), false, 'later replacement stays in breathe');
assert.equal(a.style.getPropertyValue('--attn-breathe-delay'), '-0.6s', 'breathe phase wraps modulo 1s');

// Entering session clears flag, hint, and inline CSS custom properties.
state.sync([a, current, old], 'away', 'New question');
assert.equal(flagged(a), false, 'entering session clears flag');
assert.equal(a.querySelector().title, undefined, 'entering removes hint');
assert.equal(a.style.getPropertyValue('--attn-t'), '', 'entering clears --attn-t');
assert.equal(a.style.getPropertyValue('--attn-breathe-delay'), '', 'entering clears --attn-breathe-delay');

// Equal-count replacement, close, reopen, already-open, current, old server, new row.
a.setAttribute('data-asks', '12 40');
sync();
assert.equal(flagged(a), true, 'equal-count replacement is new');
state.sync([a, current, old], 'away', 'New question');
a.setAttribute('data-asks', '12');
sync();
assert.equal(flagged(a), false, 'closing does not flag');
a.setAttribute('data-asks', '12 40');
sync();
assert.equal(flagged(a), true, 'settled ID reopening is new');
state.sync([a, current, old], 'away', 'New question');
sync();
assert.equal(flagged(a), false, 're-asking an already-open ID is not new');
current.setAttribute('data-asks', '100');
sync();
assert.equal(flagged(current), false, 'current session never flags');
assert.equal(flagged(old), false, 'old server without attribute does nothing');
const added = row('new-row', '1 2');
state.sync([a, current, old, added], 'shown', 'New question');
assert.equal(flagged(added), false, 'new rows silently baseline');

// Long disconnect (baseline = true) silently baselines offline arrivals.
a.setAttribute('data-asks', '12 40 99');
state.observe([a, current, old], 'shown', true);
sync();
assert.equal(flagged(a), false, 'long-disconnect baseline silently absorbs offline arrivals');

// Short reconnect (baseline = false) flags genuine new ask, ignores pre-existing asks and new rows.
const brandNew = row('brand-new', '777');
a.setAttribute('data-asks', '12 40 99 101');
state.observe([a, current, old, brandNew], 'shown', false);
state.mark([a, current, old, brandNew], 'shown', 'New question');
assert.equal(flagged(a), true, 'short reconnect flags genuine new ask');
assert.equal(flashing(a), true, 'short reconnect flashes genuine new ask');
assert.equal(flagged(brandNew), false, 'short reconnect silently baselines newly appearing session');
// Existing flag retained across a later long-disconnect baseline; post-flash replacement stays settled.
now += 1500;
state.observe([a, current, old], 'shown', true);
a = row('away', '12 40 99 101');
sync();
assert.equal(flagged(a), true, 'reconnect retains an existing flag');
assert.equal(flashing(a), false, 'post-flash reconnect replacement is settled');
assert.equal(a.style.getPropertyValue('--attn-breathe-delay'), '-0.36s', 'reconnect replacement preserves breathe phase');

// Reload resets memory-only state.
const reset = window.WsRail.attention();
reset.sync([a], 'shown', 'New question');
assert.equal(flagged(a), false, 'reload resets memory-only state');

// Held updates (hidden/gated): open then close still flags and flashes fresh on first mount.
let heldNow = 5000;
const held = window.WsRail.attention({ now: () => heldNow });
let heldRow = row('held', '');
held.sync([heldRow], 'shown', 'New question');
held.observe([row('held', '7')], 'shown');
heldNow += 4000; // Time passing before first mount must NOT consume the flash.
held.observe([row('held', '')], 'shown');
heldRow = row('held', '');
held.mark([heldRow], 'shown', 'New question');
assert.equal(flagged(heldRow), true, 'hidden/gated open then close still flags');
assert.equal(flashing(heldRow), true, 'held flag flashes fresh when first mounted');
assert.equal(heldRow.style.getPropertyValue('--attn-t'), '', 'held flag first mount starts at 0s');
heldNow += 1200;
heldRow = row('held', '');
held.mark([heldRow], 'shown', 'New question');
assert.equal(flashing(heldRow), false, 'post-flash patch does not replay held flash');
held.observe([row('held', '8')], 'shown');
held.mark([heldRow], 'held', 'New question');
assert.equal(flagged(heldRow), false, 'successful show clears a held flag');

// --- Pause/resume (data-animation-paused): paused time does not advance elapsed ---
{
  let t = 10000;
  const pausedAttn = window.WsRail.attention({ now: () => t, paused: false });
  let p = row('p1', '');
  pausedAttn.sync([p], 'shown', 'New question');
  p.setAttribute('data-asks', '1');
  pausedAttn.sync([p], 'shown', 'New question');
  assert.equal(flashing(p), true, 'pause test starts flashing');

  // 400ms active, then 5000ms paused, then 200ms active => 600ms active elapsed.
  t += 400;
  pausedAttn.setPaused(true);
  t += 5000;
  pausedAttn.setPaused(false);
  t += 200;
  p = row('p1', '1');
  pausedAttn.sync([p], 'shown', 'New question');
  assert.equal(flashing(p), true, 'paused interval does not expire the 1.14s flash window');
  assert.equal(p.style.getPropertyValue('--attn-t'), '0.6s', 'active elapsed equals 400ms + 200ms');

  // 800ms more active => 1400ms total active => breathe elapsed 260ms.
  t += 800;
  p = row('p1', '1');
  pausedAttn.sync([p], 'shown', 'New question');
  assert.equal(flashing(p), false, 'flash finishes once active time reaches 1.14s');
  assert.equal(p.style.getPropertyValue('--attn-breathe-delay'), '-0.26s', 'breathe phase excludes paused time');

  // Initial paused state freezes clock until resumed.
  let t2 = 20000;
  const initPaused = window.WsRail.attention({ now: () => t2, paused: true });
  let p2 = row('p2', '');
  initPaused.sync([p2], 'shown', 'New question');
  p2.setAttribute('data-asks', '2');
  initPaused.sync([p2], 'shown', 'New question');
  t2 += 3000; // Still paused
  p2 = row('p2', '2');
  initPaused.sync([p2], 'shown', 'New question');
  assert.equal(flashing(p2), true, 'initially paused flag does not expire while paused');
  assert.equal(p2.style.getPropertyValue('--attn-t'), '0s', 'zero active elapsed while initially paused');
}

// --- Reorder resync (wsAttnResync from mergeRows / moveRow) ---
{
  let t = 30000;
  const reorderAttn = window.WsRail.attention({ now: () => t });
  const r = row('mv', '');
  reorderAttn.sync([r], 'shown', 'New question');
  r.setAttribute('data-asks', '9');
  reorderAttn.sync([r], 'shown', 'New question');
  assert.equal(flashing(r), true);

  // Mid-flash reorder at 500ms: keeps flash with --attn-t: 0.5s, clears wsAttnResync.
  t += 500;
  r.wsAttnResync = true;
  reorderAttn.mark([r], 'shown', 'New question');
  assert.equal(r.wsAttnResync, false, 'resync marker is consumed');
  assert.equal(flashing(r), true, 'mid-flash reorder preserves remaining flash');
  assert.equal(r.style.getPropertyValue('--attn-t'), '0.5s', 'mid-flash reorder offsets flash instead of replaying from 0');

  // Post-flash reorder at 1650ms (even if animationend never fired): strips .attn-flash and syncs breathe.
  t += 1150; // elapsed = 1650ms => breathe phase = 510ms
  r.wsAttnResync = true;
  reorderAttn.mark([r], 'shown', 'New question');
  assert.equal(flashing(r), false, 'post-flash reorder never replays .attn-flash');
  assert.equal(flagged(r), true, 'post-flash reorder keeps .attn');
  assert.equal(r.style.getPropertyValue('--attn-t'), '', 'post-flash reorder clears --attn-t');
  assert.equal(r.style.getPropertyValue('--attn-breathe-delay'), '-0.51s', 'post-flash reorder sets exact breathe phase');
}

// --- Absent-row 30s grace period ---
{
  let t = 40000;
  const absentAttn = window.WsRail.attention({ now: () => t });
  const r1 = row('r1', '10'), r2 = row('r2', '20'), keep = row('keep', '');
  absentAttn.sync([r1, r2, keep], 'shown', 'New question');

  // Snapshot drops r1 and r2 transiently at t = 41000.
  t = 41000;
  absentAttn.sync([keep], 'shown', 'New question');

  // r1 returns at +30000ms (at grace boundary) with same ask: no false trigger.
  t = 71000;
  absentAttn.sync([r1, keep], 'shown', 'New question');
  assert.equal(flagged(r1), false, 'row returning within 30s with old ask does not false-trigger');

  // r1 drops again at t = 72000, returns at +15000ms with genuine NEW ask: DOES trigger!
  t = 72000;
  absentAttn.sync([keep], 'shown', 'New question');
  t = 87000;
  r1.setAttribute('data-asks', '10 11');
  absentAttn.sync([r1, keep], 'shown', 'New question');
  assert.equal(flagged(r1), true, 'row returning within 30s with genuinely new ask flags');
  assert.equal(flashing(r1), true, 'row returning within 30s with genuinely new ask flashes');

  // r2 was absent since t = 41000 (> 30s ago): pruned and silently re-baselined on return.
  r2.setAttribute('data-asks', '20 99');
  absentAttn.sync([r1, r2, keep], 'shown', 'New question');
  assert.equal(flagged(r2), false, 'row absent > 30s is pruned and silently baselines on return');

  // Direct return after > 30s with NO intermediate snapshot still prunes & baselines silently.
  const r3 = row('r3', '30');
  absentAttn.sync([r3, keep], 'shown', 'New question');
  t += 1000;
  absentAttn.sync([keep], 'shown', 'New question'); // missingSince set
  t += 30001; // > 30000ms with no intermediate snapshot
  r3.setAttribute('data-asks', '30 31');
  absentAttn.sync([r3, keep], 'shown', 'New question');
  assert.equal(flagged(r3), false, 'row returning directly after > 30s without intermediate tick still baselines silently');
}

// --- animationend cleanup and early flashDone ---
{
  let t = 90000;
  const flashRow = row('flash', '');
  const flashState = window.WsRail.attention({ now: () => t });
  flashState.sync([flashRow], 'shown', 'New question');
  flashRow.setAttribute('data-asks', '5');
  flashState.sync([flashRow], 'shown', 'New question');
  assert.equal(flashing(flashRow), true, 'a fresh flag flashes');
  flashRow.fire('attn-breathe');
  flashRow.fire('some-other-animation');
  assert.equal(flashing(flashRow), true, 'unrelated animations do not end the flash');
  flashRow.fire('attn-flash');
  assert.equal(flashing(flashRow), false, 'the flash class drops when the flashes end');
  assert.equal(flagged(flashRow), true, 'the flag stays, now breathing alone');
  assert.equal(flashRow.listeners.size, 0, 'the end listener removes itself');
  // Replacement even shortly after animationend never replays flash.
  t += 50;
  const replaced = row('flash', '5');
  flashState.sync([replaced], 'shown', 'New question');
  assert.equal(flagged(replaced), true, 'replacement after animationend keeps flag');
  assert.equal(flashing(replaced), false, 'replacement after animationend never replays flash');
}

// --- CSS phase variables and unchanged steady-state visibility ---
const railCss = fs.readFileSync(path.join(__dirname, '../assets/dashboard-rail.css'), 'utf8');
assert.match(railCss, /\.rail \.rs\.attn::before\s*\{[^}]*linear-gradient\(90deg,[^}]*opacity:\s*\.04;[^}]*animation:\s*attn-breathe 1s ease-in-out var\(--attn-breathe-delay, 0s\) infinite;/s, 'settled unread row pulses at 1Hz with amber gradient and inherits --attn-breathe-delay');
assert.match(railCss, /\.rail \.rs\.attn \.hb::after\s*\{[^}]*opacity:\s*\.25;[^}]*animation:\s*attn-breathe-ring 1s ease-in-out var\(--attn-breathe-delay, 0s\) infinite;/s, 'settled unread badge ring pulses at 1Hz and inherits --attn-breathe-delay');
assert.match(railCss, /\.rail \.rs\.attn\.attn-flash::before\s*\{\s*animation:\s*attn-flash \.38s ease-in-out calc\(-1 \* var\(--attn-t, 0s\)\) 3,\s*attn-breathe 1s ease-in-out calc\(1\.14s - var\(--attn-t, 0s\)\) infinite;\s*\}/, 'fresh arrival flashes 3x then chains into 1Hz pulse shifted by --attn-t');
assert.match(railCss, /\.rail \.rs\.attn\.attn-flash \.hb::after\s*\{\s*animation:\s*attn-flash-ring \.38s ease-in-out calc\(-1 \* var\(--attn-t, 0s\)\) 3,\s*attn-breathe-ring 1s ease-in-out calc\(1\.14s - var\(--attn-t, 0s\)\) infinite;\s*\}/, 'fresh arrival badge ring flashes 3x then chains into 1Hz pulse shifted by --attn-t');
assert.match(railCss, /@keyframes attn-flash\s*\{\s*0%,\s*100%\s*\{\s*opacity:\s*\.04;\s*\}\s*50%\s*\{\s*opacity:\s*\.44;\s*\}\s*\}/, 'attn-flash opacity range unchanged');
assert.match(railCss, /@keyframes attn-breathe\s*\{\s*0%,\s*100%\s*\{\s*opacity:\s*\.04;\s*\}\s*50%\s*\{\s*opacity:\s*\.28;\s*\}\s*\}/, 'attn-breathe opacity range unchanged');
assert.match(railCss, /@keyframes attn-breathe-ring\s*\{\s*0%,\s*100%\s*\{\s*opacity:\s*\.25;\s*\}\s*50%\s*\{\s*opacity:\s*\.85;\s*\}\s*\}/, 'attn-breathe-ring opacity range unchanged');
assert.match(railCss, /:root\[data-animation-paused\] \.rail \.rs\.attn::before,\s*:root\[data-animation-paused\] \.rail \.rs\.attn \.hb::after\s*\{\s*animation-play-state:\s*paused;\s*\}/, 'data-animation-paused pauses both overlays');
assert.match(railCss, /@media\s*\(prefers-reduced-motion:\s*reduce\)\s*\{\s*\.rail \.rs\.attn::before\s*\{\s*animation:\s*none !important;\s*opacity:\s*\.16;\s*\}\s*\.rail \.rs\.attn \.hb::after\s*\{\s*animation:\s*none !important;\s*box-shadow:\s*0 0 0 2px color-mix\(/s, 'reduced motion disables unread pulse and keeps static amber tint and 2px ring');

console.log('PASS: rail attention phase preservation, pause/resume, reorder resync, 30s absent grace, reconnect baseline, CSS rules');
