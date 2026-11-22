// The Windows title strip's page. It owns no state of its own: the shell
// pushes window state through werkschaftStrip.set, and every action goes back
// to the shell through the few fixed commands below. The shell accepts them
// from this page's webview only.
(() => {
  'use strict';
  const root = document.documentElement;
  const internals = window.__TAURI_INTERNALS__;
  const call = (cmd, args) => {
    try {
      return Promise.resolve(internals.invoke(cmd, args)).catch(() => {});
    } catch (e) {
      return Promise.resolve();
    }
  };

  const title = document.getElementById('title');
  const max = document.querySelector('.cap.max');
  const labels = Array.from(document.querySelectorAll('.menu'));
  let open = null;

  const popup = (label) => {
    if (open || !label) return;
    open = label;
    label.classList.add('is-open');
    const strip = document.getElementById('strip').getBoundingClientRect();
    const box = label.getBoundingClientRect();
    // The menu hangs from the label's left edge, at the strip's bottom edge.
    call('strip_popup', { menu: label.dataset.menu, x: box.left, y: strip.bottom })
      .then(() => { open = null; label.classList.remove('is-open'); });
  };

  const bool = (v) => v === true;
  const set = (state) => {
    if (!state || typeof state !== 'object') return;
    if ('maximized' in state) {
      root.classList.toggle('maximized', bool(state.maximized));
      max.setAttribute('aria-label', bool(state.maximized) ? 'Restore' : 'Maximize');
    }
    if ('focused' in state) root.classList.toggle('unfocused', !bool(state.focused));
    if ('title' in state && typeof state.title === 'string') {
      title.textContent = state.title;
      title.title = state.title;
    }
    if ('theme' in state) {
      if (state.theme === 'light' || state.theme === 'dark') root.dataset.theme = state.theme;
      else delete root.dataset.theme;
    }
    // The maximize button is hit-tested by the shell (for the Windows 11 snap
    // layouts flyout), so the page never sees the pointer over it.
    if ('maxHover' in state) max.classList.toggle('is-hover', bool(state.maxHover));
    if ('maxPressed' in state) max.classList.toggle('is-pressed', bool(state.maxPressed));
  };
  const openMenu = (name) => popup(labels.find((l) => l.dataset.menu === name));
  Object.defineProperty(window, 'werkschaftStrip', { value: Object.freeze({ set, openMenu }) });

  labels.forEach((label) => label.addEventListener('mousedown', (e) => {
    if (e.button === 0) { e.preventDefault(); popup(label); }
  }));
  document.querySelectorAll('.cap').forEach((cap) => cap.addEventListener('click', (e) => {
    if (e.button === 0) call(cap.dataset.cap);
  }));
  // The rest of the strip drags the window; a double click maximizes or restores.
  document.getElementById('strip').addEventListener('mousedown', (e) => {
    if (e.button !== 0 || e.target.closest('.menu, .cap')) return;
    e.preventDefault();
    call(e.detail === 2 ? 'strip_toggle_maximize' : 'strip_drag');
  });
  document.addEventListener('contextmenu', (e) => e.preventDefault());
})();
