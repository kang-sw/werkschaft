// Pure Node: lazy-history batching, the top-shell history cache, the
// loading state, and the shell-held board geometry, on the whiteboard's own
// lazy-history DOM fixture (wswhiteboard/stream_lazy_target_test.go).
const { test } = require('node:test');
const fs = require('node:fs');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const { readAssetSource } = require('./testdata/asset-source.cjs');

const wb = path.join(__dirname, '../wswhiteboard');
const prelude = fs.readFileSync(path.join(wb, 'check_form_test.go'), 'utf8').split('const formPrelude = `')[1].split('`')[0];
const region = fs.readFileSync(path.join(wb, 'stream_lazy_target_test.go'), 'utf8').split('script := `')[1].split('` + cases')[0];
const [head, rest] = region.split('` + formPrelude + `');
const [mid, tail] = rest.split('` + string(encoded) + `');
const asset = readAssetSource('wswhiteboard', 'stream.js').replace('var kit = viewKit(window);', 'var kit = window.historyKit;');
const harness = head + prelude + mid + JSON.stringify(asset) + tail + `
function tsec(name, th, n, lazy, rows, body){return '<section class="topic" data-topic="'+name+'"'+(th?' data-th="'+th+'"':'')+'><div class="wb-history" data-history="'+name+'"><div class="history-live">'+gap(lazy,rows||'')+row(n)+'</div></div>'+(body?'<details class="entry-panel"><div class="entry-body" data-lazy></div></details>':'')+'</section>';}
function full(name, n){return tsec(name,'full',n,false,row(n-3)+row(n-2)+row(n-1));}
function marker(s){return '<template data-history-images="'+s+'"></template>';}
function topicOf(name){return root.querySelectorAll('section.topic').find(s=>s.getAttribute('data-topic')===name);}
function near(){return watches.filter(w=>w.opt.margin==='800px');}
`;

function runCase(name, body) {
  const r = spawnSync(process.execPath, ['-'], { input: harness + body, encoding: 'utf8', timeout: 60000 });
  assert.equal(r.status, 0, name + '\n' + r.stdout + r.stderr);
}

test('fillLater coalesces near-view topics into one request, joins in-flight topics, and clears loading', () => {
  runCase('batch', `(async function(){
  root=templates(parse(tsec('a','ha',10,true)+tsec('b','hb',20,true,'',true)+tsec('c','hc',30,true)));
  window.wbHistory.init(root);
  check('three near-view watches',near().length,3);
  near().forEach(w=>w.opt.onChange({visible:true}));
  var gapA=topicOf('a').querySelector('.history-gap'), bodyB=topicOf('b').querySelector('.entry-body');
  check('gap shows loading while in flight',gapA.getAttribute('data-loading'),'1');
  check('earlier panel body shows loading while in flight',bodyB.getAttribute('data-loading'),'1');
  check('no request before the batch closes',requests.length,0);
  await drain();
  check('one batched request for every near topic',requests.map(r=>r.query),['name=a&name=b&name=c']);
  var joined=window.wbHistory.hydrateAll(root);await drain();
  check('in-flight topics join, no second request',requests.length,1);
  requests[0].resolve([{name:'a',html:full('a',10)},{name:'b',html:full('b',20)},{name:'c',html:full('c',30)}]);
  await drain();await joined;
  check('loading cleared on arrival',[gapA,bodyB].map(n=>n.hasAttribute('data-loading')),[false,false]);
  check('every gap hydrated',['a','b','c'].map(n=>!!topicOf(n).querySelector('.history-gap[data-lazy]')),[false,false,false]);
  check('history rows admitted',!!root.querySelector('[id="e9"]')&&!!root.querySelector('[id="e29"]'),true);
  scriptDone=true;if(bad.length)throw Error(bad.join('\\n'));
})().catch(e=>{console.error(e);process.exitCode=1;});`);
});

test('a failed history request clears loading and leaves the topic lazy for a retry', () => {
  runCase('failure', `(async function(){
  window.wbHistoryFetch=function(){return Promise.reject(Error('offline'));};
  root=templates(parse(tsec('a','ha',10,true)));
  window.wbHistory.init(root);
  near()[0].opt.onChange({visible:true});
  var gapA=topicOf('a').querySelector('.history-gap');
  check('loading while in flight',gapA.getAttribute('data-loading'),'1');
  await drain();
  check('loading cleared on failure',gapA.hasAttribute('data-loading'),false);
  check('still lazy',gapA.hasAttribute('data-lazy'),true);
  scriptDone=true;if(bad.length)throw Error(bad.join('\\n'));
})().catch(e=>{console.error(e);process.exitCode=1;});`);
});

test('history cache: hit by section hash and image stamp skips the request; a new data-th or stamp misses', () => {
  runCase('cache', `(async function(){
  var store=new Map(), puts=[];
  window.wbHistoryCache={get:function(n,th,st){return store.get(n+'|'+th+'|'+st);},put:function(n,th,st,html){puts.push([n,th,st]);store.set(n+'|'+th+'|'+st,html);}};
  function load(th,stamp){root=templates(parse(tsec('a',th,10,true)+marker(stamp)));watches=[];requests=[];window.wbHistory.init(root);near()[0].opt.onChange({visible:true});return topicOf('a').querySelector('.history-gap');}
  load('h1','s1');await drain();
  check('first view fetches',requests.map(r=>r.query),['name=a']);
  requests[0].resolve([{name:'a',html:full('a',10)}]);await drain();
  check('answer cached under section hash and stamp',puts,[['a','h1','s1']]);
  // A reloaded frame showing the same revision hydrates from memory.
  var gap=load('h1','s1');
  check('cache hit shows no loading state',gap.hasAttribute('data-loading'),false);
  await drain();
  check('cache hit sends no request',requests.length,0);
  check('cache hit hydrates',!!topicOf('a').querySelector('[data-lazy]'),false);
  // The topic changed: its section hash moved, so the cache misses.
  gap=load('h2','s1');
  check('changed topic shows loading',gap.getAttribute('data-loading'),'1');
  await drain();
  check('changed topic fetches',requests.map(r=>r.query),['name=a']);
  requests[0].resolve([{name:'a',html:full('a',10)}]);await drain();
  check('changed topic cached anew',puts.at(-1),['a','h2','s1']);
  // An image edit moves the board's history stamp: miss as well.
  load('h2','s2');await drain();
  check('new image stamp fetches',requests.map(r=>r.query),['name=a']);
  requests[0].resolve([]);await drain();
  // A section without a hash (a deferred topic) is never cached.
  root=templates(parse(tsec('d','',40,true)));watches=[];requests=[];window.wbHistory.init(root);near()[0].opt.onChange({visible:true});await drain();
  requests[0].resolve([{name:'d',html:full('d',40)}]);await drain();
  check('no hash, no cache entry',puts.some(p=>p[0]==='d'),false);
  scriptDone=true;if(bad.length)throw Error(bad.join('\\n'));
})().catch(e=>{console.error(e);process.exitCode=1;});`);
});

test('the 512-name route cap: a large batch splits into 256-name requests', () => {
  runCase('chunk', `(async function(){
  var html='';for(var i=0;i<300;i++)html+=tsec('t'+i,'',1000+i,true);
  root=templates(parse(html));window.wbHistory.init(root);
  window.wbHistory.hydrateAll(root);await drain();
  check('two requests',requests.length,2);
  check('chunk sizes',requests.map(r=>r.query.split('&').length),[256,44]);
  requests.forEach(r=>r.resolve([]));await drain();
  scriptDone=true;if(bad.length)throw Error(bad.join('\\n'));
})().catch(e=>{console.error(e);process.exitCode=1;});`);
});

// shell.js memo and the session-view helpers, across two frame realms that
// share one top shell.
const shellSrc = readAssetSource('wsdashboard', 'shell.js');
const viewSrc = readAssetSource('wsdashboard', 'dashboard-session-view.js');
function helper(name) {
  const m = viewSrc.match(new RegExp('\\n  function ' + name + '\\([\\s\\S]*?\\n  \\}\\n'));
  assert.ok(m, 'missing ' + name);
  return m[0];
}
function topShell() {
  const top = { console };
  top.window = top;
  vm.createContext(top);
  vm.runInContext(shellSrc, top);
  return top;
}
function frame(top, pathname, extra) {
  const ctx = Object.assign({ location: { pathname }, Map, Number, isFinite, Math }, extra || {});
  ctx.window = { top: top || null, WsShell: undefined };
  if (!top) ctx.window.top = ctx.window;
  vm.createContext(ctx);
  vm.runInContext(['shellMemo', 'boardHeightCache', 'boardOffsetSave', 'boardOffsetTake'].map(helper).join(''), ctx);
  return ctx;
}

test('shell memo is a bounded LRU by entries and size, with realm-owned tables', () => {
  const top = topShell();
  const m = top.WsShell.memo('t', 2, 0);
  assert.equal(top.WsShell.memo('t', 2, 0), m, 'one memo per name');
  m.set('a', 'x'); m.set('b', 'y'); m.get('a'); m.set('c', 'z');
  assert.equal(m.get('b'), undefined, 'least recently used entry evicted');
  assert.equal(m.get('a'), 'x'); assert.equal(m.get('c'), 'z');
  const s = top.WsShell.memo('s', 10, 5);
  s.set('k1', 'abc'); s.set('k2', 'de'); s.set('k3', 'f');
  assert.equal(s.get('k1'), undefined, 'size cap evicts the oldest');
  assert.equal(s.get('k2'), 'de');
  s.set('huge', 'abcdefgh');
  assert.equal(s.get('huge'), undefined, 'an entry over the cap is not kept');
  const t = top.WsShell.memo('tables', 4, 0);
  const tbl = t.table('/s/a');
  assert.equal(t.table('/s/a'), tbl);
  assert.ok(tbl instanceof vm.runInContext('Map', top), 'table is made in the shell realm');
});

test('board heights and reading offset survive a frame reload through the top shell', () => {
  const top = topShell();
  const node = { id: 'card' };
  const bcol = { scrollTop: 50380 };
  const first = frame(top, '/s/abc', {
    $: id => (id === 'bcol' ? bcol : null),
    anchorKey: 't:A', anchorLast: node, freeActive: true, freePos: new Map([[node, 50500]]),
  });
  const heights = first.boardHeightCache();
  heights.set('t:A', 480);
  first.boardOffsetSave();
  // The evicted frame's realm is gone; a new one reads the same shell memory.
  const second = frame(top, '/s/abc');
  assert.equal(second.boardHeightCache().get('t:A'), 480, 'heights restored');
  assert.equal(second.boardOffsetTake('t:B'), null, 'another card ignores the offset');
  first.boardOffsetSave();
  assert.equal(second.boardOffsetTake('t:A'), 120, 'offset restored for its card');
  assert.equal(second.boardOffsetTake('t:A'), null, 'taken once');
  assert.equal(frame(top, '/s/other').boardHeightCache().size, 0, 'per session page');
  // Unframed, with no shell script: a page-local table.
  const alone = frame(null, '/s/abc');
  assert.notEqual(alone.boardHeightCache(), alone.boardHeightCache());
  assert.equal(alone.boardOffsetTake('t:A'), null);
});
