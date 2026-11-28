const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

function loadPresence() {
  const listeners = {};
  const win = {
    WsCompanion: {},
    addEventListener(type, fn) { (listeners[type] ||= []).push(fn); }
  };
  win.window = win;
  const context = vm.createContext({ window: win, setInterval, clearInterval, Date, Math, JSON });
  vm.runInContext(fs.readFileSync(path.join(__dirname, "assets/dashboard-companion-messenger-presence.js"), "utf8"), context);
  return { C: win.WsCompanion, listeners };
}

function harness(extra = {}) {
  const { C } = loadPresence();
  const posts = [];
  const beacons = [];
  const intervals = [];
  const winListeners = {};
  const win = { addEventListener(type, fn) { (winListeners[type] ||= []).push(fn); } };
  const fire = (type, event = {}) => (winListeners[type] || []).forEach(fn => fn(event));
  const presence = C.messengerPresenceReporter({
    window: win,
    client: "tab-a",
    fetch(url, init) {
      posts.push({ url, init, body: JSON.parse(init.body) });
      return Promise.resolve({ ok: true });
    },
    navigator: extra.navigator || {
      sendBeacon(url, body) { beacons.push({ url, body: JSON.parse(body) }); return true; }
    },
    setInterval(fn, ms) { intervals.push({ fn, ms, cleared: false }); return intervals.length; },
    clearInterval(id) { if (intervals[id - 1]) intervals[id - 1].cleared = true; },
    isOpen: extra.isOpen
  });
  return { presence, posts, beacons, intervals, fire };
}

test("messenger presence posts open/close once per change with a heartbeat while open", () => {
  const h = harness();
  h.presence.set(true);
  h.presence.set(true);
  assert.equal(h.posts.length, 1);
  assert.equal(h.posts[0].url, "/api/companion/messenger/presence");
  assert.deepEqual(h.posts[0].body, { open: true, client: "tab-a" });
  assert.equal(h.posts[0].init.method, "POST");
  assert.equal(h.intervals.length, 1);
  assert.equal(h.intervals[0].ms, 60000);
  h.intervals[0].fn();
  assert.deepEqual(h.posts[1].body, { open: true, client: "tab-a" });

  h.presence.set(false);
  assert.deepEqual(h.posts[2].body, { open: false, client: "tab-a" });
  assert.equal(h.intervals[0].cleared, true);
  assert.equal(h.beacons.length, 0);
});

test("messenger presence closes through sendBeacon on pagehide and only once", () => {
  const h = harness();
  h.fire("pagehide", { persisted: false });
  assert.equal(h.beacons.length, 0, "a closed messenger sends nothing on teardown");

  h.presence.set(true);
  h.fire("pagehide", { persisted: false });
  h.fire("beforeunload", {});
  assert.equal(h.beacons.length, 1);
  assert.deepEqual(h.beacons[0].body, { open: false, client: "tab-a" });
  assert.equal(h.presence.isOpen(), false);
  assert.equal(h.intervals[0].cleared, true);
});

test("messenger presence falls back to a keepalive fetch when sendBeacon refuses", () => {
  const h = harness({ navigator: { sendBeacon() { return false; } } });
  h.presence.set(true);
  h.fire("pagehide", { persisted: false });
  const last = h.posts[h.posts.length - 1];
  assert.deepEqual(last.body, { open: false, client: "tab-a" });
  assert.equal(last.init.keepalive, true);
});

test("messenger presence re-reports an open messenger restored from the bfcache", () => {
  let open = true;
  const h = harness({ isOpen: () => open });
  h.presence.set(true);
  h.fire("pagehide", { persisted: true });
  h.fire("pageshow", { persisted: true });
  assert.equal(h.presence.isOpen(), true);
  assert.deepEqual(h.posts[h.posts.length - 1].body, { open: true, client: "tab-a" });
  open = false;
});

test("phoneSnapNewer drops stale snapshots within one daemon epoch", () => {
  const src = fs.readFileSync(path.join(__dirname, "assets/dashboard-companion-stage.js"), "utf8");
  const start = src.indexOf("function phoneSnapNewer");
  const end = src.indexOf("\n  }\n", start) + 4;
  const fn = new vm.Script("(" + src.slice(start, end) + ")").runInNewContext({ Number });
  assert.equal(fn(null, { seq: 1 }), true);
  assert.equal(fn({ epoch: 5, seq: 9 }, { epoch: 5, seq: 8 }), false);
  assert.equal(fn({ epoch: 5, seq: 9 }, { epoch: 5, seq: 9 }), true);
  assert.equal(fn({ epoch: 5, seq: 9 }, { epoch: 6, seq: 1 }), true, "a restarted daemon resets seq");
  assert.equal(fn({ seq: 0 }, { epoch: 6, seq: 1 }), true);
});
