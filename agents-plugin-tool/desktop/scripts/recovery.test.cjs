'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { test } = require('node:test');
const vm = require('node:vm');

const uiDir = path.resolve(__dirname, '../ui');
const indexHtml = fs.readFileSync(path.join(uiDir, 'index.html'), 'utf8');
const recoveryJs = fs.readFileSync(path.join(uiDir, 'recovery.js'), 'utf8');

class FakeElement {
  constructor(tag, { k, act, notice = false, hidden = false } = {}) {
    this.tagName = tag.toUpperCase();
    this.dataset = {};
    if (k !== undefined) this.dataset.k = k;
    if (act !== undefined) this.dataset.act = act;
    this.isNotice = notice;
    this.hidden = hidden;
    this.textContent = '';
    this.className = '';
    this.href = undefined;
    this.parentNode = null;
    this.children = [];
    this.listeners = new Map();
  }

  appendChild(child) {
    return this.insertBefore(child, null);
  }

  insertBefore(child, ref) {
    if (child.parentNode) {
      const siblings = child.parentNode.children;
      const idx = siblings.indexOf(child);
      if (idx !== -1) siblings.splice(idx, 1);
    }
    child.parentNode = this;
    if (ref) {
      const idx = this.children.indexOf(ref);
      if (idx === -1) throw new Error('Reference node is not a child');
      this.children.splice(idx, 0, child);
    } else {
      this.children.push(child);
    }
    return child;
  }

  get nextElementSibling() {
    if (!this.parentNode) return null;
    const siblings = this.parentNode.children;
    const idx = siblings.indexOf(this);
    return idx >= 0 && idx + 1 < siblings.length ? siblings[idx + 1] : null;
  }

  removeAttribute(name) {
    if (name === 'href') delete this.href;
  }

  closest(selector) {
    if (selector !== '[data-act]') return null;
    for (let cur = this; cur; cur = cur.parentNode) {
      if (cur.dataset && cur.dataset.act !== undefined) return cur;
    }
    return null;
  }

  addEventListener(type, fn) {
    if (!this.listeners.has(type)) this.listeners.set(type, []);
    this.listeners.get(type).push(fn);
  }

  click() {
    for (const fn of this.listeners.get('click') || []) fn({ target: this });
  }
}

function buildDom() {
  const keyed = {};
  const notices = [];
  const actions = [];

  const node = (tag, opts = {}) => {
    const el = new FakeElement(tag, opts);
    if (opts.k) keyed[opts.k] = el;
    if (opts.notice) notices.push(el);
    if (opts.act) actions.push(el);
    return el;
  };

  const main = node('main');

  const idle = node('section', { k: 'idle' });
  idle.appendChild(node('button', { act: 'retry' }));
  idle.appendChild(node('p', { notice: true, hidden: true }));
  main.appendChild(idle);

  const failure = node('section', { k: 'failure', hidden: true });
  for (const k of [
    'fTag', 'fBundle', 'fReason', 'fCopy', 'fVariant',
    'fPathRow', 'fPath', 'fBundledRow', 'fBundled', 'fDetail', 'fLkg', 'fLog',
  ]) {
    failure.appendChild(node('span', { k }));
  }
  const fRetry = node('button', { act: 'retry' });
  const fRetryLabel = node('span');
  fRetry.appendChild(fRetryLabel);
  failure.appendChild(fRetry);
  failure.appendChild(node('button', { act: 'copy-diagnostics' }));
  failure.appendChild(node('button', { act: 'open-logs' }));
  failure.appendChild(node('p', { notice: true, hidden: true }));
  main.appendChild(failure);

  const blocked = node('section', { k: 'blocked', hidden: true });
  for (const k of [
    'bFloorPill', 'bTitle', 'bCopy', 'bBundledRow', 'bBundled',
    'bFloorRow', 'bFloor', 'bLkgRow', 'bLkg',
  ]) {
    blocked.appendChild(node('span', { k }));
  }
  const bActions = node('div', { k: 'bActions' });
  bActions.appendChild(node('button', { act: 'retry' }));
  bActions.appendChild(node('button', { act: 'open-logs' }));
  bActions.appendChild(node('button', { act: 'copy-diagnostics' }));
  blocked.appendChild(bActions);
  blocked.appendChild(node('p', { notice: true, hidden: true }));
  main.appendChild(blocked);

  const update = node('section', { k: 'update', hidden: true });
  for (const k of ['uPill', 'uTag', 'uTitle', 'uCopy', 'uProgress', 'uProgressLabel', 'uActions']) {
    update.appendChild(node('div', { k }));
  }
  update.appendChild(node('button', { k: 'uDownload' }));
  update.appendChild(node('button', { k: 'uRestart' }));
  update.appendChild(node('button', { k: 'uCheck' }));
  update.appendChild(node('a', { k: 'uLink', hidden: true }));
  main.appendChild(update);

  const diag = node('details', { k: 'diag', hidden: true });
  for (const k of ['dCode', 'dExit', 'dLogRow', 'dLog', 'dStage', 'dTail']) {
    diag.appendChild(node('span', { k }));
  }
  main.appendChild(diag);

  return { main, el: keyed, notices, actions, fRetryLabel };
}

function createHarness({
  transport = 'mac',
  transportThrows = false,
  userActivation = { isActive: true },
  initialUpdateBridge = false,
} = {}) {
  const dom = buildDom();
  const posted = [];
  const timers = [];
  const docListeners = new Map();
  const winListeners = new Map();

  const postMessage = (msg) => {
    if (transportThrows) throw new Error('transport closed');
    posted.push(msg);
  };

  const apiCalls = [];
  let statusHandler = null;
  const makeUpdateBridge = () => ({
    onStatus(cb) { statusHandler = cb; },
    download() { apiCalls.push('download'); },
    restart() { apiCalls.push('restart'); },
    check() { apiCalls.push('check'); },
  });

  const ctx = {
    Element: FakeElement,
    navigator: userActivation === undefined ? {} : { userActivation },
    document: {
      querySelectorAll(selector) {
        if (selector === '[data-k]') return Object.values(dom.el);
        if (selector === '[data-notice]') return dom.notices;
        return [];
      },
      addEventListener(type, fn) {
        if (!docListeners.has(type)) docListeners.set(type, []);
        docListeners.get(type).push(fn);
      },
    },
    addEventListener(type, fn) {
      if (!winListeners.has(type)) winListeners.set(type, []);
      winListeners.get(type).push(fn);
    },
    setTimeout(fn, ms) {
      timers.push({ fn, ms });
      return timers.length;
    },
  };

  if (transport === 'mac') {
    ctx.webkit = { messageHandlers: { werkschaftRecovery: { postMessage } } };
  } else if (transport === 'windows') {
    ctx.chrome = { webview: { postMessage } };
  }
  if (initialUpdateBridge) {
    ctx.werkschaftDesktop = { update: makeUpdateBridge() };
  }

  ctx.window = ctx;
  vm.createContext(ctx);
  vm.runInContext(recoveryJs, ctx, { filename: 'recovery.js' });

  const dispatchDocClick = (target) => {
    for (const fn of docListeners.get('click') || []) fn({ target });
  };
  const attachUpdateBridge = () => {
    ctx.werkschaftDesktop = { update: makeUpdateBridge() };
    for (const fn of winListeners.get('werkschaft-update-ready') || []) fn();
  };
  const emitUpdateStatus = (next) => {
    assert.equal(typeof statusHandler, 'function', 'onStatus callback should be registered');
    statusHandler(next);
  };

  return {
    ctx,
    ...dom,
    posted,
    timers,
    apiCalls,
    dispatchDocClick,
    attachUpdateBridge,
    emitUpdateStatus,
  };
}

test('harness covers every data-k key declared in ui/index.html', () => {
  const htmlKeys = Array.from(indexHtml.matchAll(/data-k="([^"]+)"/g), (m) => m[1]).sort();
  const { el } = createHarness();
  assert.deepEqual(Object.keys(el).sort(), htmlKeys);
});

test('initial state shows idle and hides failure, blocked, diag, and update before WAIT_MS', () => {
  const h = createHarness();
  assert.equal(h.el.idle.hidden, false);
  assert.equal(h.el.failure.hidden, true);
  assert.equal(h.el.blocked.hidden, true);
  assert.equal(h.el.diag.hidden, true);
  assert.equal(h.el.update.hidden, true);
  assert.equal(h.el.update.parentNode, h.main);
  assert.equal(h.el.update.nextElementSibling, h.el.diag);
  assert.equal(h.timers.length, 1);
  assert.equal(h.timers[0].ms, 1500);
});

test('__werkschaftRecovery renders package error, start failure, LKG variants, and diagnostics', () => {
  const h = createHarness();

  // Package error with full metadata and LKG failed.
  h.ctx.__werkschaftRecovery({
    variant: 'DigestMismatch',
    bundledVersion: '0.52.2',
    reason: 'Bundled daemon failed digest verification',
    detail: 'sha256 mismatch',
    path: '/app/ws-mcp',
    logPath: '/tmp/shell.log',
    lkg: 'failed',
    lkgVersion: '0.52.1',
    exitCode: 42,
    stderrTail: 'fatal: bad digest\n',
  });

  assert.equal(h.el.idle.hidden, true);
  assert.equal(h.el.failure.hidden, false);
  assert.equal(h.el.blocked.hidden, true);
  assert.equal(h.el.diag.hidden, false);

  assert.equal(h.el.fTag.textContent, 'Daemon Package Error');
  assert.equal(h.el.fBundle.textContent, 'Bundled v0.52.2 · LKG v0.52.1 failed');
  assert.equal(h.el.fReason.textContent, 'Bundled daemon failed digest verification');
  assert.match(h.el.fCopy.textContent, /Package integrity validation rejected/);
  assert.match(h.el.fCopy.textContent, /Last-Known-Good \(LKG\) stage v0\.52\.1 did not become healthy/);
  assert.equal(h.el.fVariant.textContent, 'DaemonPackageError::DigestMismatch');
  assert.equal(h.el.fVariant.className, 'code code-fail');
  assert.equal(h.el.fPathRow.hidden, false);
  assert.equal(h.el.fPath.textContent, '/app/ws-mcp');
  assert.equal(h.el.fBundledRow.hidden, false);
  assert.equal(h.el.fBundled.textContent, 'v0.52.2');
  assert.equal(h.el.fDetail.textContent, '(sha256 mismatch)');
  assert.equal(h.el.fLkg.textContent, 'v0.52.1 did not become healthy');
  assert.equal(h.el.fLog.hidden, false);
  assert.equal(h.el.fLog.textContent, '/tmp/shell.log');

  // Diagnostics preview
  assert.equal(h.el.dCode.hidden, false);
  assert.equal(h.el.dExit.textContent, '42');
  assert.equal(h.el.dLogRow.hidden, false);
  assert.equal(h.el.dLog.textContent, '/tmp/shell.log');
  assert.equal(
    h.el.dStage.textContent,
    'daemon-start (exit_code=42, bundled=0.52.2, lkg=failed:0.52.1)'
  );
  assert.equal(h.el.dTail.textContent, 'fatal: bad digest\n');

  // Start failure with minimal fields and no exitCode/stderrTail.
  h.ctx.__werkschaftRecovery({ lkg: 'none' });
  assert.equal(h.el.fTag.textContent, 'Start Failed');
  assert.equal(h.el.fBundle.textContent, 'LKG None');
  assert.equal(h.el.fReason.textContent, 'Werkschaft could not start the bundled daemon');
  assert.match(h.el.fCopy.textContent, /could not bring the dashboard up, and no previous Last-Known-Good/);
  assert.equal(h.el.fVariant.textContent, '—');
  assert.equal(h.el.fVariant.className, '');
  assert.equal(h.el.fPathRow.hidden, true);
  assert.equal(h.el.fBundledRow.hidden, true);
  assert.equal(h.el.fLog.hidden, true);
  assert.equal(h.el.dCode.hidden, true);
  assert.equal(h.el.dLogRow.hidden, true);
  assert.equal(h.el.dStage.textContent, 'startup (lkg=none)');
  assert.equal(h.el.dTail.textContent, 'No daemon-start output was captured.');

  // Remaining LKG states: invalid, running, blocked, default.
  h.ctx.__werkschaftRecovery({ lkg: 'invalid' });
  assert.equal(h.el.fBundle.textContent, 'LKG Invalid');
  assert.equal(h.el.fLkg.textContent, 'Recorded stage failed revalidation');
  assert.match(h.el.fCopy.textContent, /failed revalidation\./);

  h.ctx.__werkschaftRecovery({ lkg: 'running', lkgVersion: 'v0.51.0' });
  assert.equal(h.el.fBundle.textContent, 'LKG v0.51.0 (running)');
  assert.equal(h.el.fLkg.textContent, 'Running on v0.51.0');
  assert.match(h.el.fCopy.textContent, /running the Last-Known-Good \(LKG\) stage v0\.51\.0\./);

  h.ctx.__werkschaftRecovery({ lkg: 'blocked', lkgVersion: '0.50.9' });
  assert.equal(h.el.fBundle.textContent, 'LKG v0.50.9 (blocked)');
  assert.equal(h.el.fLkg.textContent, 'v0.50.9 blocked by the minor floor');

  h.ctx.__werkschaftRecovery({});
  assert.equal(h.el.fBundle.textContent, 'LKG Not tried');
  assert.equal(h.el.fLkg.textContent, 'Not attempted');
  assert.equal(h.el.fCopy.textContent, 'Werkschaft could not bring the dashboard up.');

  // Reset to idle with null.
  h.ctx.__werkschaftRecovery(null);
  assert.equal(h.el.idle.hidden, false);
  assert.equal(h.el.failure.hidden, true);
  assert.equal(h.el.diag.hidden, true);
});

test('__werkschaftRecovery renders readable and unreadable floor-blocked states and relocates #update', () => {
  const h = createHarness();

  // Readable minor floor -> #update moves inside #blocked before #bActions.
  h.ctx.__werkschaftRecovery({
    floorBlocked: true,
    minorFloor: '0.52',
    bundledVersion: '0.52.2',
    lkg: 'blocked',
    lkgVersion: '0.51.9',
  });

  assert.equal(h.el.idle.hidden, true);
  assert.equal(h.el.failure.hidden, true);
  assert.equal(h.el.blocked.hidden, false);
  assert.equal(h.el.diag.hidden, false);
  assert.equal(h.el.bFloorPill.textContent, 'daemon-minor-floor: 0.52');
  assert.equal(h.el.bTitle.textContent, 'Cannot fall back to v0.51.9 to protect data compatibility');
  assert.match(h.el.bCopy.textContent, /advanced to minor floor 0\.52/);
  assert.match(h.el.bCopy.textContent, /Rolling back to LKG v0\.51\.9 is blocked/);
  assert.equal(h.el.bBundledRow.hidden, false);
  assert.equal(h.el.bBundled.textContent, 'v0.52.2');
  assert.equal(h.el.bFloorRow.hidden, false);
  assert.equal(h.el.bFloor.textContent, '0.52');
  assert.equal(h.el.bLkgRow.hidden, false);
  assert.equal(h.el.bLkg.textContent, 'v0.51.9 (blocked · cross-minor)');
  assert.match(h.el.dStage.textContent, /minor_floor=0\.52, floor_blocked=true/);
  assert.equal(h.el.update.parentNode, h.el.blocked);
  assert.equal(h.el.update.nextElementSibling, h.el.bActions);

  // Unreadable minor floor -> distinct title/copy/LKG label, #update stays inside #blocked.
  h.ctx.__werkschaftRecovery({
    floorBlocked: true,
    minorFloor: 'unreadable',
    bundledVersion: '0.52.2',
    lkg: 'blocked',
    lkgVersion: '0.51.9',
  });
  assert.equal(h.el.bFloorPill.textContent, 'daemon-minor-floor: unreadable');
  assert.equal(h.el.bTitle.textContent, 'Cannot fall back: daemon-minor-floor is unreadable');
  assert.match(h.el.bCopy.textContent, /daemon-minor-floor record could not be read/);
  assert.equal(h.el.bLkg.textContent, 'v0.51.9 (blocked · floor unreadable)');
  assert.equal(h.el.update.parentNode, h.el.blocked);
  assert.equal(h.el.update.nextElementSibling, h.el.bActions);

  // Leaving floor-blocked relocates #update back above #diag.
  h.ctx.__werkschaftRecovery({ reason: 'start failed' });
  assert.equal(h.el.blocked.hidden, true);
  assert.equal(h.el.failure.hidden, false);
  assert.equal(h.el.update.parentNode, h.main);
  assert.equal(h.el.update.nextElementSibling, h.el.diag);
});

test('__werkschaftRecoveryNotice updates and hides all notice elements', () => {
  const h = createHarness();
  assert.equal(h.notices.length, 3);
  for (const n of h.notices) assert.equal(n.hidden, true);

  h.ctx.__werkschaftRecoveryNotice('Diagnostics copied to clipboard.');
  for (const n of h.notices) {
    assert.equal(n.hidden, false);
    assert.equal(n.textContent, 'Diagnostics copied to clipboard.');
  }

  h.ctx.__werkschaftRecoveryNotice('   ');
  for (const n of h.notices) {
    assert.equal(n.hidden, true);
    assert.equal(n.textContent, '');
  }
});

test('WAIT_MS fallback reveals unavailable update card when no bridge binds', () => {
  const h = createHarness();
  assert.equal(h.el.update.hidden, true);
  assert.equal(h.timers.length, 1);
  assert.equal(h.timers[0].ms, 1500);

  h.timers[0].fn();
  assert.equal(h.el.update.hidden, false);
  assert.equal(h.el.uPill.className, 'pill');
  assert.equal(h.el.uPill.textContent, 'Unavailable');
  assert.equal(h.el.uTitle.textContent, 'Self-Update');
  assert.match(h.el.uCopy.textContent, /Self-update is unavailable in this build/);
  assert.equal(h.el.uProgress.hidden, true);
  assert.equal(h.el.uDownload.hidden, true);
  assert.equal(h.el.uRestart.hidden, true);
  assert.equal(h.el.uCheck.hidden, true);
  assert.equal(h.el.uLink.hidden, true);
  assert.equal(h.el.uActions.hidden, true);
});

test('self-update card state machine covers all 8 states and invokes bridge actions', () => {
  const h = createHarness();
  h.attachUpdateBridge();

  // 1. unchecked (initial null status after binding)
  assert.equal(h.el.update.hidden, false);
  assert.equal(h.el.uPill.className, 'pill');
  assert.equal(h.el.uPill.textContent, 'Not Checked');
  assert.equal(h.el.uTag.textContent, 'Self-Update Recovery');
  assert.equal(h.el.uTitle.textContent, 'Check for a Werkschaft update');
  assert.equal(h.el.uCheck.hidden, false);
  assert.equal(h.el.uDownload.hidden, true);
  assert.equal(h.el.uRestart.hidden, true);
  assert.equal(h.el.uActions.hidden, false);

  // 2. available (nested phase form)
  h.emitUpdateStatus({ state: 'phase', phase: { phase: 'available', version: '0.53.0' } });
  assert.equal(h.el.uPill.className, 'pill pill-ok');
  assert.equal(h.el.uPill.textContent, 'Update Available · v0.53.0');
  assert.equal(h.el.uTag.textContent, 'Self-Update Recovery');
  assert.equal(h.el.uTitle.textContent, 'Update to Werkschaft v0.53.0');
  assert.equal(h.el.uDownload.hidden, false);
  assert.equal(h.el.uRestart.hidden, true);
  assert.equal(h.el.uCheck.hidden, true);
  assert.equal(h.el.uProgress.hidden, true);

  // Blocked mode changes tag and title for available/ready.
  h.ctx.__werkschaftRecovery({ floorBlocked: true, minorFloor: '0.52' });
  assert.equal(h.el.uTag.textContent, 'Recommended Recovery');
  assert.equal(h.el.uTitle.textContent, 'Install a compatible hotfix');
  h.ctx.__werkschaftRecovery(null);

  // 3. downloading
  h.emitUpdateStatus({ state: 'downloading', version: 'v0.53.0' });
  assert.equal(h.el.uPill.className, 'pill pill-primary');
  assert.equal(h.el.uPill.textContent, 'Downloading · v0.53.0');
  assert.equal(h.el.uProgress.hidden, false);
  assert.equal(h.el.uProgressLabel.textContent, 'Downloading v0.53.0…');
  assert.equal(h.el.uActions.hidden, true);

  // 4. ready
  h.emitUpdateStatus({ state: 'ready', version: '0.53.0' });
  assert.equal(h.el.uPill.className, 'pill pill-ok');
  assert.equal(h.el.uPill.textContent, 'Ready to Install · v0.53.0');
  assert.equal(h.el.uRestart.hidden, false);
  assert.equal(h.el.uRestart.textContent, 'Restart to Update (v0.53.0)');
  assert.equal(h.el.uProgress.hidden, true);
  assert.equal(h.el.uActions.hidden, false);

  // 5. error (with custom message + downloadPage link)
  h.emitUpdateStatus({
    state: 'error',
    message: 'Network offline',
    downloadPage: 'https://example.invalid/releases',
  });
  assert.equal(h.el.uPill.className, 'pill pill-fail');
  assert.equal(h.el.uPill.textContent, 'Check Failed');
  assert.equal(h.el.uTitle.textContent, 'Could not check for updates');
  assert.equal(h.el.uCopy.textContent, 'Network offline');
  assert.equal(h.el.uCheck.hidden, false);
  assert.equal(h.el.uLink.hidden, false);
  assert.equal(h.el.uLink.href, 'https://example.invalid/releases');

  // 6. download-error (fallback message, no downloadPage removes href)
  h.emitUpdateStatus({ state: 'download-error', version: '0.53.0' });
  assert.equal(h.el.uPill.className, 'pill pill-fail');
  assert.equal(h.el.uPill.textContent, 'Download Failed');
  assert.equal(h.el.uTitle.textContent, 'Could not download Werkschaft v0.53.0');
  assert.equal(h.el.uCopy.textContent, 'The update download failed. Try again in a moment.');
  assert.equal(h.el.uLink.hidden, true);
  assert.equal(h.el.uLink.href, undefined);

  // 7. apply-error
  h.emitUpdateStatus({ state: 'apply-error', version: '0.53.0' });
  assert.equal(h.el.uPill.className, 'pill pill-fail');
  assert.equal(h.el.uPill.textContent, 'Install Failed');
  assert.equal(h.el.uTitle.textContent, 'Could not install Werkschaft v0.53.0');
  assert.equal(h.el.uCopy.textContent, 'The update could not be applied. Try again in a moment.');
  assert.equal(h.el.uCheck.hidden, false);

  // 8. default (e.g. up-to-date)
  h.emitUpdateStatus({ state: 'up-to-date' });
  assert.equal(h.el.uPill.className, 'pill');
  assert.equal(h.el.uPill.textContent, 'No Update Found');
  assert.equal(h.el.uTitle.textContent, 'No update is available right now');
  assert.equal(h.el.uCheck.hidden, false);

  // Blocked mode changes unchecked/default titles.
  h.ctx.__werkschaftRecovery({ floorBlocked: true, minorFloor: '0.52' });
  assert.equal(h.el.uTitle.textContent, 'No compatible hotfix yet');
  h.emitUpdateStatus({ state: 'unchecked' });
  assert.equal(h.el.uTitle.textContent, 'Look for a compatible hotfix');

  // Button click handlers call the bridge API.
  h.el.uDownload.click();
  h.el.uRestart.click();
  h.el.uCheck.click();
  assert.deepEqual(h.apiCalls, ['download', 'restart', 'check']);
});

test('self-update binds immediately when bridge is already present at script load', () => {
  const h = createHarness({ initialUpdateBridge: true });
  assert.equal(h.el.update.hidden, false);
  assert.equal(h.el.uPill.textContent, 'Not Checked');
});

test('[data-act] click dispatch is gated on userActivation and posts over macOS and Windows transports', () => {
  for (const transport of ['mac', 'windows']) {
    const h = createHarness({ transport, userActivation: { isActive: true } });
    // Direct button click and nested child click both resolve via closest('[data-act]').
    const copyBtn = h.actions.find((a) => a.dataset.act === 'copy-diagnostics');
    const logsBtn = h.actions.find((a) => a.dataset.act === 'open-logs');
    h.dispatchDocClick(h.fRetryLabel);
    h.dispatchDocClick(copyBtn);
    h.dispatchDocClick(logsBtn);
    // Non-Element target and Element without [data-act] ancestor are ignored.
    h.dispatchDocClick({ notAnElement: true });
    h.dispatchDocClick(h.el.fReason);
    assert.deepEqual(
      h.posted,
      [
        'werkschaft:recovery:retry',
        'werkschaft:recovery:copy-diagnostics',
        'werkschaft:recovery:open-logs',
      ],
      transport
    );
  }

  // Inactive userActivation blocks dispatch.
  const blocked = createHarness({ transport: 'mac', userActivation: { isActive: false } });
  blocked.dispatchDocClick(blocked.actions[0]);
  assert.deepEqual(blocked.posted, []);

  // Missing navigator.userActivation (older WebView) still allows dispatch.
  const legacy = createHarness({ transport: 'windows', userActivation: undefined });
  legacy.dispatchDocClick(legacy.actions[0]);
  assert.deepEqual(legacy.posted, ['werkschaft:recovery:retry']);

  // Throwing transport does not surface an uncaught error.
  const broken = createHarness({ transport: 'mac', transportThrows: true });
  assert.doesNotThrow(() => broken.dispatchDocClick(broken.actions[0]));
  assert.deepEqual(broken.posted, []);
});
