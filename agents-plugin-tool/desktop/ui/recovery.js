(() => {
  'use strict';

  // The DOM is built once in index.html; every update patches text or `hidden`
  // on the nodes found here by key.
  const el = {};
  document.querySelectorAll('[data-k]').forEach((node) => { el[node.dataset.k] = node; });
  const notices = Array.from(document.querySelectorAll('[data-notice]'));

  const text = (key, value) => {
    if (el[key].textContent !== value) el[key].textContent = value;
  };
  const show = (key, visible) => { el[key].hidden = !visible; };
  const str = (value) => (typeof value === 'string' && value.trim() ? value : null);
  const ver = (value) => {
    const v = str(value);
    return v ? (/^v/i.test(v) ? v : 'v' + v) : null;
  };

  // ---- Native actions -----------------------------------------------------

  const post = (message) => {
    try {
      const windows = window.chrome?.webview;
      if (windows && typeof windows.postMessage === 'function') {
        windows.postMessage(message);
        return;
      }
      const mac = window.webkit?.messageHandlers?.werkschaftRecovery;
      if (mac && typeof mac.postMessage === 'function') mac.postMessage(message);
    } catch (_) { /* no transport: the buttons do nothing */ }
  };

  document.addEventListener('click', (event) => {
    const button = event.target instanceof Element ? event.target.closest('[data-act]') : null;
    if (!button || navigator.userActivation?.isActive === false) return;
    post('werkschaft:recovery:' + button.dataset.act);
  });

  window.__werkschaftRecoveryNotice = (message) => {
    const line = str(message) || '';
    for (const node of notices) {
      if (node.textContent !== line) node.textContent = line;
      node.hidden = !line;
    }
  };

  // ---- Failure payload ----------------------------------------------------

  let payload = null;

  const lkgLabel = (p) => {
    const v = ver(p.lkgVersion);
    switch (p.lkg) {
      case 'none': return 'None';
      case 'blocked': return v ? v + ' (blocked)' : 'Blocked';
      case 'invalid': return 'Invalid';
      case 'failed': return v ? v + ' failed' : 'Failed';
      case 'running': return v ? v + ' (running)' : 'Running';
      default: return 'Not tried';
    }
  };

  const lkgDetail = (p) => {
    const v = ver(p.lkgVersion) || 'The recorded stage';
    switch (p.lkg) {
      case 'none': return 'None available';
      case 'blocked': return v + ' blocked by the minor floor';
      case 'invalid': return 'Recorded stage failed revalidation';
      case 'failed': return v + ' did not become healthy';
      case 'running': return 'Running on ' + v;
      default: return 'Not attempted';
    }
  };

  const lkgClause = (p) => {
    const v = ver(p.lkgVersion);
    switch (p.lkg) {
      case 'none': return 'and no previous Last-Known-Good (LKG) stage is available on this machine.';
      case 'invalid': return 'and the recorded Last-Known-Good (LKG) stage failed revalidation.';
      case 'failed': return v
        ? 'and the Last-Known-Good (LKG) stage ' + v + ' did not become healthy.'
        : 'and the Last-Known-Good (LKG) stage did not become healthy.';
      case 'running': return v
        ? 'so Werkschaft is running the Last-Known-Good (LKG) stage ' + v + '.'
        : 'so Werkschaft is running the Last-Known-Good (LKG) stage.';
      default: return null;
    }
  };

  const renderFailure = (p) => {
    const variant = str(p.variant);
    const bundled = ver(p.bundledVersion);
    const detail = str(p.detail);
    const path = str(p.path);
    const log = str(p.logPath);

    text('fTag', variant ? 'Daemon Package Error' : 'Start Failed');
    text('fBundle', (bundled ? 'Bundled ' + bundled + ' · ' : '') + 'LKG ' + lkgLabel(p));
    text('fReason', str(p.reason) || 'Werkschaft could not start the bundled daemon');
    const lead = variant
      ? 'Package integrity validation rejected the bundled runtime before launch'
      : 'Werkschaft could not bring the dashboard up';
    const clause = lkgClause(p);
    text('fCopy', clause ? lead + ', ' + clause : lead + '.');

    text('fVariant', variant ? 'DaemonPackageError::' + variant : '—');
    el.fVariant.className = variant ? 'code code-fail' : '';
    show('fPathRow', !!path);
    text('fPath', path || '');
    show('fBundledRow', !!(bundled || detail));
    text('fBundled', bundled || '');
    text('fDetail', detail ? '(' + detail + ')' : '');
    text('fLkg', lkgDetail(p));
    show('fLog', !!log);
    text('fLog', log || '');
  };

  const renderBlocked = (p) => {
    const floor = str(p.minorFloor);
    const unreadable = floor === 'unreadable';
    const bundled = ver(p.bundledVersion);
    const lkg = ver(p.lkgVersion);

    text('bFloorPill', 'daemon-minor-floor: ' + (floor || 'unknown'));
    text('bTitle', unreadable
      ? 'Cannot fall back: daemon-minor-floor is unreadable'
      : 'Cannot fall back to ' + (lkg || 'an earlier version') + ' to protect data compatibility');
    let copy = bundled ? 'Bundled ' + bundled + ' failed to start' : 'The bundled daemon failed to start';
    if (unreadable) {
      copy += ', and the daemon-minor-floor record could not be read, so Werkschaft cannot confirm that an earlier version is safe to run. Automatic fallback is blocked to protect your data.';
    } else {
      copy += floor
        ? ', and this profile has already advanced to minor floor ' + floor + '.'
        : ', and this profile has already advanced past the earlier version.';
      copy += lkg
        ? ' Rolling back to LKG ' + lkg + ' is blocked to prevent state corruption.'
        : ' Rolling back is blocked to prevent state corruption.';
    }
    text('bCopy', copy);
    show('bBundledRow', !!bundled);
    text('bBundled', bundled || '');
    show('bFloorRow', !!floor);
    text('bFloor', floor || '');
    show('bLkgRow', !!lkg);
    text('bLkg', lkg ? lkg + (unreadable ? ' (blocked · floor unreadable)' : ' (blocked · cross-minor)') : '');
  };

  const renderDiagnostics = (p) => {
    const exit = Number.isInteger(p.exitCode) ? String(p.exitCode) : null;
    const bundled = str(p.bundledVersion);
    const lkgV = str(p.lkgVersion);
    const floor = str(p.minorFloor);
    const log = str(p.logPath);
    const parts = [];
    if (exit !== null) parts.push('exit_code=' + exit);
    if (bundled) parts.push('bundled=' + bundled.replace(/^v/i, ''));
    parts.push('lkg=' + (str(p.lkg) || 'not-tried') + (lkgV ? ':' + lkgV.replace(/^v/i, '') : ''));
    if (floor) parts.push('minor_floor=' + floor);
    if (p.floorBlocked) parts.push('floor_blocked=true');

    show('dCode', exit !== null);
    text('dExit', exit || '');
    show('dLogRow', !!log);
    text('dLog', log || '');
    text('dStage', (exit !== null ? 'daemon-start' : 'startup') + ' (' + parts.join(', ') + ')');
    text('dTail', str(p.stderrTail) || 'No daemon-start output was captured.');
  };

  const renderMain = () => {
    const p = payload;
    const blocked = !!(p && p.floorBlocked);
    show('idle', !p);
    show('failure', !!p && !blocked);
    show('blocked', blocked);
    show('diag', !!p);
    if (p) {
      if (blocked) renderBlocked(p); else renderFailure(p);
      renderDiagnostics(p);
    }
    // One update card: inside the blocked card, else below the main card.
    if (blocked && el.update.parentNode !== el.blocked) {
      el.blocked.insertBefore(el.update, el.bActions);
    } else if (!blocked && el.update.nextElementSibling !== el.diag) {
      el.diag.parentNode.insertBefore(el.update, el.diag);
    }
  };

  window.__werkschaftRecovery = (next) => {
    payload = next && typeof next === 'object' ? next : null;
    renderMain();
    renderUpdate();
  };

  // ---- Self-update card ---------------------------------------------------

  const UNAVAILABLE = 'Self-update is unavailable in this build. Install a release build of Werkschaft to update.';
  const WAIT_MS = 1500;
  let status = null;
  let bound = false;
  let waited = false;

  const updateApi = () => window.werkschaftDesktop?.update || null;

  const kindOf = (s) => {
    if (!s || typeof s !== 'object') return 'unchecked';
    if (s.state === 'phase') return str(s.phase?.phase) || 'unchecked';
    return str(s.state) || 'unchecked';
  };

  const view = (blocked) => {
    if (!bound) {
      return { tone: '', pill: 'Unavailable', title: 'Self-Update', copy: UNAVAILABLE };
    }
    const kind = kindOf(status);
    const v = ver(status?.version) || ver(status?.phase?.version);
    const named = v ? 'Werkschaft ' + v : 'the new version';
    const generic = blocked
      ? 'Install this update directly from this screen, or open shell.log for full startup diagnostics.'
      : 'A signed update is ready on the update channel. Installing it replaces the bundled package without losing workspace state.';
    const title = blocked ? 'Install a compatible hotfix' : 'Update to ' + named;
    switch (kind) {
      case 'available':
        return { tone: 'ok', pill: 'Update Available' + (v ? ' · ' + v : ''), title, copy: generic, download: true };
      case 'downloading':
        return {
          tone: 'primary', pill: 'Downloading' + (v ? ' · ' + v : ''), title, copy: generic,
          progress: 'Downloading ' + (v || 'update') + '…',
        };
      case 'ready':
        return {
          tone: 'ok', pill: 'Ready to Install' + (v ? ' · ' + v : ''), title, copy: generic,
          restart: v ? 'Restart to Update (' + v + ')' : 'Restart to Update',
        };
      case 'error':
        return failureView('Check Failed', 'Could not check for updates', 'The update check failed. Try again in a moment.');
      case 'download-error':
        return failureView('Download Failed', 'Could not download ' + named, 'The update download failed. Try again in a moment.');
      case 'apply-error':
        return failureView('Install Failed', 'Could not install ' + named, 'The update could not be applied. Try again in a moment.');
      case 'unchecked':
        return {
          tone: '', pill: 'Not Checked',
          title: blocked ? 'Look for a compatible hotfix' : 'Check for a Werkschaft update',
          copy: 'A signed update can replace the bundled package without losing workspace state.', check: true,
        };
      default:
        return {
          tone: '', pill: 'No Update Found',
          title: blocked ? 'No compatible hotfix yet' : 'No update is available right now',
          copy: 'No newer release is available on the update channel yet. Check again later.', check: true,
        };
    }
  };

  const failureView = (pill, title, fallback) => ({
    tone: 'fail', pill, title,
    copy: str(status?.message) || fallback,
    check: true,
    link: str(status?.downloadPage),
  });

  const TONE = { ok: 'pill pill-ok', fail: 'pill pill-fail', primary: 'pill pill-primary', '': 'pill' };

  function renderUpdate() {
    // Until the bridge shows up the card stays out, so a release build never
    // flashes the development notice.
    const known = bound || waited;
    show('update', known);
    if (!known) return;
    const blocked = !!(payload && payload.floorBlocked);
    const s = view(blocked);
    const pill = TONE[s.tone] || 'pill';
    if (el.uPill.className !== pill) el.uPill.className = pill;
    text('uPill', s.pill);
    text('uTag', blocked && (s.download || s.restart) ? 'Recommended Recovery' : 'Self-Update Recovery');
    text('uTitle', s.title);
    text('uCopy', s.copy);
    show('uProgress', !!s.progress);
    text('uProgressLabel', s.progress || '');
    show('uDownload', !!s.download);
    show('uRestart', !!s.restart);
    text('uRestart', s.restart || '');
    show('uCheck', !!s.check);
    show('uLink', !!s.link);
    if (s.link) el.uLink.href = s.link; else el.uLink.removeAttribute('href');
    show('uActions', !!(s.download || s.restart || s.check));
  }

  const bind = () => {
    const api = updateApi();
    if (!api || bound) return;
    bound = true;
    if (typeof api.onStatus === 'function') {
      api.onStatus((next) => {
        status = next;
        renderUpdate();
      });
    }
    renderUpdate();
  };

  const call = (name) => () => {
    const api = updateApi();
    if (api && typeof api[name] === 'function') api[name]();
  };
  el.uDownload.addEventListener('click', call('download'));
  el.uRestart.addEventListener('click', call('restart'));
  el.uCheck.addEventListener('click', call('check'));

  window.addEventListener('werkschaft-update-ready', bind);
  bind();
  setTimeout(() => {
    waited = true;
    renderUpdate();
  }, WAIT_MS);

  renderMain();
  renderUpdate();
})();
