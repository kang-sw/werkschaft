// Harness-free: node internal/wsdashboard/testdata/draft-merge.test.cjs
// The per-session draft block of dashboard-lifecycle.js (saveDraft and its
// IndexedDB merge, startDraftRecovery and restoreDraft) in VMs, one per
// renderer, over one fake shared recovery row and one shared legacy
// (localStorage) store. An older dashboard page is the test writing that
// row directly.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const lifecycle = fs.readFileSync(path.join(__dirname, '../assets/dashboard-lifecycle.js'), 'utf8');
const start = lifecycle.indexOf('  // --- per-session drafts ---');
const end = lifecycle.indexOf('  if (draftStore) {\n    ["input"');
assert.ok(start > 0 && end > start, 'the draft block is found');
const block = lifecycle.slice(start, end);
const railWindow = {};
vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../assets/rail.js'), 'utf8'), { window: railWindow });
const WsRail = railWindow.WsRail;

// IndexedDB hands each read its own copy, with its own Blob objects.
function clone(v) {
  if (v instanceof Blob) return new Blob([v], { type: v.type });
  if (Array.isArray(v)) return v.map(clone);
  if (v && typeof v === 'object') {
    const o = {};
    for (const k of Object.keys(v)) o[k] = clone(v[k]);
    return o;
  }
  return v;
}

// fakeDB is one shared "drafts" store holding the session's row. failNext
// aborts that many writing transactions (a quota error) before they commit.
function fakeDB() {
  const db = { row: undefined, failNext: 0 };
  db.transaction = () => {
    let aborted = false, pending = null;
    const tx = { error: null, abort() { aborted = true; } };
    tx.objectStore = () => ({
      get() {
        const req = {};
        setImmediate(() => {
          req.result = clone(db.row);
          if (req.onsuccess) req.onsuccess();
          setImmediate(() => {
            if (!aborted && pending && db.failNext > 0) {
              db.failNext--;
              aborted = true;
              tx.error = new Error('QuotaExceededError');
            }
            if (aborted) { if (tx.onabort) tx.onabort(); return; }
            if (pending) db.row = pending.del ? undefined : clone(pending.value);
            if (tx.oncomplete) tx.oncomplete();
          });
        });
        return req;
      },
      put(value) { pending = { value }; },
      delete() { pending = { del: true }; },
    });
    return tx;
  };
  return db;
}

// renderer is one dashboard page of the session, its composer holding imgs
// and its text in ctext. The composer's marker helpers are stand-ins: a
// marker is "[Image #n]", inserted at the text's end.
function renderer(db, legacy = new Map()) {
  const marker = n => '[Image #' + n + ']';
  const markerNums = text => {
    const out = {};
    for (const m of String(text).matchAll(/\[Image #([1-9][0-9]{0,5})\]/g)) out[+m[1]] = true;
    return out;
  };
  const ctx = {
    Blob, URL, crypto: globalThis.crypto, setTimeout, clearTimeout, console,
    WsRail, railStore: { load: k => (legacy.has(k) ? legacy.get(k) : null), save: (k, v) => { if (v === null) legacy.delete(k); else legacy.set(k, v); return true; } },
    rid: 'session', shell: false, mode: 'live', live: false, grid: { inert: false }, doc: {},
    imgs: [], imgSeq: 0, outbox: [], mentions: [], bselections: [], openState: {}, warnings: [],
    ctext: { value: '', selectionStart: 0, selectionEnd: 0, setSelectionRange(a, b) { this.selectionStart = a; this.selectionEnd = b; } },
    cchips: null, ccmd: null, cmdSel: null, boardIncarnation: null, IMG_MAX: 8,
    marker, markerNums, inlineEditor: () => null, region: () => null, attachSrc: ref => ref,
    splitSelectionText: text => ({ text, cuts: [], groups: [] }), selectionRanges: r => r,
    liveMention: () => true, grow() {}, paintMentions() {}, renderImgs() {}, setTab() {}, restoreOpen() {},
    restoreDeliveries() {}, wsT: k => k, noteWarn(m) { ctx.warnings.push(m); },
  };
  ctx.window = ctx;
  ctx.nextNum = () => {
    for (const k of Object.keys(markerNums(ctx.ctext.value))) ctx.imgSeq = Math.max(ctx.imgSeq, +k);
    return ++ctx.imgSeq;
  };
  ctx.insertMarkers = nums => { ctx.ctext.value += (ctx.ctext.value ? ' ' : '') + nums.map(marker).join(' '); };
  vm.createContext(ctx);
  vm.runInContext(block, ctx);
  // As the dashboard's readDraft: the legacy projection lists every pasted
  // image's number as lost (its bytes live only in the recovery row).
  ctx.readDraft = () => ({ text: ctx.ctext.value, lost: ctx.imgs.filter(p => !p.ref).map(p => p.n), refs: [], open: {}, tab: '' });
  ctx.draftDB = db;
  ctx.draftLoaded = true;
  ctx.save = () => ctx.saveDraft();
  ctx.paste = (name, bytes) => {
    const n = ctx.nextNum();
    ctx.imgs.push({ n, name, ref: '', blob: new Blob([bytes], { type: 'image/png' }), data: null });
    ctx.ctext.value += (ctx.ctext.value ? ' ' : '') + marker(n);
    return n;
  };
  ctx.drop = name => {
    const p = ctx.imgs.find(q => q.name === name);
    ctx.imgs.splice(ctx.imgs.indexOf(p), 1);
    ctx.ctext.value = ctx.ctext.value.replace(marker(p.n), '').trim();
  };
  return ctx;
}
// open is a page opened on the session: it restores the shared draft.
async function open(db, legacy) {
  const ctx = renderer(db, legacy);
  ctx.draftLoaded = false;
  ctx.draftDatabase = () => Promise.resolve(db);
  ctx.startDraftRecovery();
  await ctx.draftReady;
  return ctx;
}
const image = (n, name, bytes) => ({ n, name, ref: '', blob: new Blob([bytes], { type: 'image/png' }) });
const stored = db => (db.row && db.row.active && db.row.active.images || []);
const bytesOf = db => Promise.all(stored(db).map(p => p.blob.text()));

(async () => {
  // 1. Same slot, name, size and type, different bytes: an older page's
  // ID-less image is another image, never a copy of this window's.
  {
    const db = fakeDB(), a = renderer(db);
    a.imgs.push(image(1, 'image.png', 'abc'));
    assert.equal(await a.save(), true);
    db.row.active.images = [{ n: 1, name: 'image.png', ref: '', blob: new Blob(['xyz'], { type: 'image/png' }) }];
    a.ctext.value = 'draft edited after the older page';
    assert.equal(await a.save(), true);
    assert.deepEqual((await bytesOf(db)).sort(), ['abc', 'xyz'], 'the older page\'s different bytes are kept');
    assert.ok(stored(db).every(p => p.id), 'the kept ID-less image gets its legacy ID');

    // The older page writes back this window's own bytes without the ID:
    // that is a copy, stored once.
    db.row.active.images = db.row.active.images.map(({ id, ...p }) => p);
    a.ctext.value = 'draft edited again';
    assert.equal(await a.save(), true);
    assert.deepEqual((await bytesOf(db)).sort(), ['abc', 'xyz'], 'an ID-stripped copy of held bytes is stored once');
  }

  // 2. A merge past IMG_MAX keeps every saved image and reports failure.
  {
    const db = fakeDB(), a = renderer(db), b = renderer(db);
    a.imgs.push(image(1, 'a.png', 'first window'));
    assert.equal(await a.save(), true);
    for (let i = 0; i < 8; i++) b.imgs.push(image(i + 1, `b${i}.png`, `second window ${i}`));
    assert.equal(await b.save(), false, 'the over-cap save is refused');
    assert.equal(b.draftUnsaved, true, 'the refusing window holds its draft as unsaved');
    assert.ok((await bytesOf(db)).includes('first window'), 'the other window\'s saved image is kept');
    assert.equal(await a.save(), true);
    b.imgs.pop();
    b.ctext.value = 'draft with one image less';
    assert.equal(await b.save(), true, 'with room again the merge saves');
    assert.equal(stored(db).length, 8);
    assert.ok((await bytesOf(db)).includes('first window'));
  }

  // 3. A removal whose first write aborts still tombstones on the retry.
  {
    const db = fakeDB(), a = renderer(db), b = renderer(db);
    a.imgs.push(image(1, 'gone.png', 'gone'));
    assert.equal(await a.save(), true);
    const id = stored(db)[0].id;
    // The stale window restored the same image under its ID.
    b.imgs.push(Object.assign(image(1, 'gone.png', 'gone'), { id }));
    assert.equal(await b.save(), true);
    a.imgs.length = 0;
    db.failNext = 1;
    assert.equal(await a.save(), false, 'the aborted write reports failure');
    assert.equal(await a.save(), true, 'the retry commits');
    assert.ok(db.row && db.row.removed.includes(id), 'the retry carries the tombstone');
    b.ctext.value = 'stale edit';
    assert.equal(await b.save(), true);
    assert.deepEqual(stored(db), [], 'the stale window cannot resurrect the removed image');
  }
  // 4. The reviewer's A/B/C case: A removes original.png (#1) while a stale
  // B still holds it; a fresh C pastes replacement.png, also #1; B edits
  // its text, still naming #1, and saves. Reopened, B's #1 never binds to
  // C's bytes: it is reported lost and C's image comes back past it.
  {
    const db = fakeDB(), legacy = new Map(), a = await open(db, legacy);
    a.ctext.value = 'Shared draft';
    a.paste('original.png', 'original');
    assert.equal(await a.save(), true);
    const b = await open(db, legacy);
    assert.deepEqual(b.imgs.map(p => [p.name, p.n]), [['original.png', 1]]);
    a.drop('original.png');
    assert.equal(await a.save(), true);
    const c = renderer(db, legacy);
    assert.equal(c.paste('replacement.png', 'replacement'), 1);
    assert.equal(await c.save(), true);
    b.ctext.value += ' edited by the stale window';
    assert.match(b.ctext.value, /\[Image #1\]/);
    assert.equal(await b.save(), true);
    const d = await open(db, legacy);
    const replacement = d.imgs.find(p => p.name === 'replacement.png');
    assert.ok(replacement, 'C\'s bytes are kept');
    assert.ok(replacement.n > 1, 'C\'s image never takes the number B\'s text names: ' + replacement.n);
    assert.doesNotMatch(d.ctext.value, /\[Image #1\]/, 'B\'s #1 stays unresolved: ' + d.ctext.value);
    assert.match(d.ctext.value, new RegExp('edited by the stale window.*\\[Image #' + replacement.n + '\\]'));
    assert.deepEqual(d.warnings, ['js.draft.imgslost'], 'the missing image is reported');
  }

  // 5. A refused (over-cap) save leaves the committed legacy projection:
  // the reopened draft is A's text with A's image under its marker.
  {
    const db = fakeDB(), legacy = new Map(), a = await open(db, legacy), b = await open(db, legacy);
    a.ctext.value = 'Text of the first window';
    a.paste('a.png', 'first window');
    assert.equal(await a.save(), true);
    b.ctext.value = 'Conflicting text of the second window';
    for (let i = 0; i < 8; i++) b.paste(`b${i}.png`, `second window ${i}`);
    assert.equal(await b.save(), false, 'the over-cap save is refused');
    const d = await open(db, legacy);
    assert.equal(d.ctext.value, 'Text of the first window [Image #1]');
    assert.deepEqual(d.imgs.map(p => [p.name, p.n]), [['a.png', 1]]);
    assert.deepEqual(d.warnings, []);
  }
  console.log('draft-merge: ok');
})().catch(e => { console.error(e); process.exit(1); });
