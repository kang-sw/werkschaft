// Harness-free: node internal/wsdashboard/testdata/draft-merge.test.cjs
// The per-session draft block of dashboard-lifecycle.js (saveDraft and its
// IndexedDB merge) in VMs, one per renderer, over one fake shared recovery
// row. An older dashboard page is the test writing that row directly.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const lifecycle = fs.readFileSync(path.join(__dirname, '../assets/dashboard-lifecycle.js'), 'utf8');
const start = lifecycle.indexOf('  // --- per-session drafts ---');
const end = lifecycle.indexOf('  function queueDraft() {');
assert.ok(start > 0 && end > start, 'the draft block is found');
const block = lifecycle.slice(start, end);

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

// renderer is one dashboard page of the session, its composer holding imgs.
function renderer(db) {
  const ctx = {
    Blob, URL, crypto: globalThis.crypto, setTimeout, clearTimeout, console,
    WsRail: { drafts: () => ({ save: () => true, load: () => null }), emptyDraft: d => !d || !d.text },
    railStore: {}, rid: 'session', shell: false, mode: 'live',
    imgs: [], outbox: [], ctext: { value: 'draft' }, IMG_MAX: 8,
  };
  ctx.window = ctx;
  vm.createContext(ctx);
  vm.runInContext(block, ctx);
  ctx.readDraft = () => ({ text: ctx.ctext.value, lost: [], open: {}, tab: '' });
  ctx.draftDB = db;
  ctx.draftLoaded = true;
  ctx.save = () => ctx.saveDraft();
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
  console.log('draft-merge: ok');
})().catch(e => { console.error(e); process.exit(1); });
