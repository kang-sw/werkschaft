/* mock-api.js — /api/account/* 계약 시뮬레이터. UI(app.js)는 MockApi.call()만 사용한다.
 * 응답 규약: 성공 → {ok:true, ...payload}, 실패 → reject({status, error}). 모든 호출에 지연(latency)을 둔다. */
(function (global) {
  'use strict';
  var LATENCY = 260;
  var seq = 100;
  var state = null;
  var flows = {}; // flow_id -> {kind, harness, ...}

  function nid(p) { return p + '-' + (++seq).toString(36); }
  function clone(o) { return JSON.parse(JSON.stringify(o)); }
  function sleep(ms) { return new Promise(function (r) { setTimeout(r, ms); }); }
  function fail(status, error) { return Promise.reject({ status: status, error: error }); }

  function seed(name) {
    var multi = {
      harness: {
        claude: { active: 'default', slots: [
          { id: 'default', label: 'Default', kind: 'default', config_dir: '~/.claude', signed_in: true, account: 'kang@werk.dev' },
          { id: 's-work', label: 'Work', kind: 'managed', config_dir: '~/.werk/accounts/claude/work', signed_in: true, account: 'kang@corp.example' },
          { id: 's-lab', label: 'Lab (실험)', kind: 'custom', config_dir: '/Volumes/data/claude-lab', signed_in: false, account: '' }
        ] },
        codex: { active: 's-pro', slots: [
          { id: 'default', label: 'Default', kind: 'default', config_dir: '~/.codex', signed_in: true, account: 'kang@werk.dev' },
          { id: 's-pro', label: 'Pro', kind: 'managed', config_dir: '~/.werk/accounts/codex/pro', signed_in: true, account: 'kang-pro@werk.dev' }
        ] },
        agy: { signed_in: true, verified: true, account: 'kang@werk.dev' }
      },
      openai: { consent: true, accounts: [
        { id: 'oa-1', email: 'kang@werk.dev', plan: 'Plus', active: true, needs_reauth: false },
        { id: 'oa-2', email: 'team@corp.example', plan: 'Pro', active: false, needs_reauth: true }
      ] },
      openrouter: { masked: 'sk-or-v1-••••••••a3f9', state: 'verified', voice_shared: true },
      endpoints: [
        { id: 'ep-1', name: 'Ollama (로컬)', kind: 'ollama', base_url: 'http://127.0.0.1:11434/v1', model: 'qwen3:14b' },
        { id: 'ep-2', name: 'LM Studio', kind: 'lmstudio', base_url: 'http://127.0.0.1:1234/v1', model: 'gpt-oss-20b' },
        { id: 'ep-3', name: '연구실 vLLM', kind: 'vllm', base_url: 'http://10.0.4.21:8000/v1', model: 'llama-3.3-70b' }
      ]
    };
    var empty = {
      harness: {
        claude: { active: 'default', slots: [{ id: 'default', label: 'Default', kind: 'default', config_dir: '~/.claude', signed_in: false, account: '' }] },
        codex: { active: 'default', slots: [{ id: 'default', label: 'Default', kind: 'default', config_dir: '~/.codex', signed_in: false, account: '' }] },
        agy: { signed_in: false, verified: false, account: '' }
      },
      openai: { consent: false, accounts: [] },
      openrouter: { masked: '', state: 'none', voice_shared: false },
      endpoints: []
    };
    return clone(name === 'empty' ? empty : multi);
  }

  function slotsOf(h) {
    var s = state.harness[h];
    if (!s || !s.slots) throw { status: 400, error: '알 수 없는 하네스: ' + h };
    return s;
  }
  function findSlot(h, id) {
    var s = slotsOf(h);
    for (var i = 0; i < s.slots.length; i++) if (s.slots[i].id === id) return s.slots[i];
    throw { status: 404, error: '슬롯을 찾을 수 없습니다.' };
  }

  var routes = {
    'GET /api/account/status': function () { return { status: clone(state) }; },

    'POST /api/account/slots/switch': function (b) { findSlot(b.harness, b.id); slotsOf(b.harness).active = b.id; return {}; },
    'POST /api/account/slots/add': function (b) {
      var dir = (b.config_dir || '').trim();
      if (!dir) throw { status: 400, error: '폴더 경로를 입력하세요.' };
      if (!/^(~|\/)/.test(dir)) throw { status: 400, error: '절대 경로(또는 ~/)로 입력하세요.' };
      if (/missing|없는/.test(dir)) throw { status: 404, error: '폴더를 찾을 수 없습니다: ' + dir };
      var s = slotsOf(b.harness);
      var label = (b.label || '').trim() || dir.split('/').filter(Boolean).pop();
      var slot = { id: nid('s'), label: label, kind: 'custom', config_dir: dir, signed_in: true, account: 'detected@local' };
      s.slots.push(slot);
      return { slot: slot };
    },
    'POST /api/account/slots/rename': function (b) {
      var l = (b.label || '').trim();
      if (!l) throw { status: 400, error: '이름을 입력하세요.' };
      findSlot(b.harness, b.id).label = l; return {};
    },
    'POST /api/account/slots/remove': function (b) {
      var s = slotsOf(b.harness);
      if (b.id === 'default') throw { status: 400, error: 'Default 슬롯은 삭제할 수 없습니다.' };
      findSlot(b.harness, b.id);
      s.slots = s.slots.filter(function (x) { return x.id !== b.id; });
      if (s.active === b.id) s.active = 'default';
      return {};
    },
    'POST /api/account/slots/sign-in/start': function (b) {
      slotsOf(b.harness);
      var id = nid('flow');
      if (b.method === 'device') {
        flows[id] = { kind: 'device', harness: b.harness, slot_id: b.slot_id || null };
        return { flow_id: id, method: 'device', verify_url: 'https://auth.openai.com/codex/device', user_code: 'WK4R-9PXT', expires_in: 900 };
      }
      flows[id] = { kind: 'browser', harness: b.harness, slot_id: b.slot_id || null };
      return { flow_id: id, method: 'browser', url: b.harness === 'claude'
        ? 'https://claude.ai/oauth/authorize?code=true&client_id=9d1c…&state=' + id
        : 'https://auth.openai.com/oauth/authorize?client_id=app_EMo…&state=' + id };
    },
    'POST /api/account/slots/sign-in/complete': function (b) {
      var f = flows[b.flow_id];
      if (!f) throw { status: 410, error: '로그인 세션이 만료되었습니다. 다시 시도하세요.' };
      if (f.kind === 'browser') {
        var code = (b.code || '').trim();
        if (!code) throw { status: 400, error: '브라우저에 표시된 코드를 붙여넣으세요.' };
        if (/bad|wrong/i.test(code)) throw { status: 401, error: '코드가 올바르지 않습니다.' };
      }
      var s = slotsOf(f.harness), slot;
      if (f.slot_id) { slot = findSlot(f.harness, f.slot_id); slot.signed_in = true; slot.account = 'reauth@werk.dev'; }
      else {
        var n = s.slots.length;
        slot = { id: nid('s'), label: (b.label || '').trim() || ('Account ' + n), kind: 'managed',
          config_dir: '~/.werk/accounts/' + f.harness + '/acct-' + n, signed_in: true, account: 'new' + n + '@werk.dev' };
        s.slots.push(slot);
      }
      delete flows[b.flow_id];
      return { slot: slot };
    },
    'POST /api/account/slots/sign-in/cancel': function (b) { delete flows[b.flow_id]; return {}; },

    'POST /api/account/openai/switch': function (b) {
      var found = false;
      state.openai.accounts.forEach(function (a) { a.active = a.id === b.id; if (a.active) found = true; });
      if (!found) throw { status: 404, error: '계정을 찾을 수 없습니다.' };
      return {};
    },
    'POST /api/account/openai/sign-out': function (b) {
      var was = state.openai.accounts.filter(function (a) { return a.id === b.id; })[0];
      state.openai.accounts = state.openai.accounts.filter(function (a) { return a.id !== b.id; });
      if (was && was.active && state.openai.accounts.length) state.openai.accounts[0].active = true;
      return {};
    },
    'POST /api/account/sign-in/start': function (b) {
      var id = nid('flow');
      flows[id] = { kind: 'openai', account_id: b && b.account_id || null };
      return { flow_id: id, url: 'https://auth.openai.com/oauth/authorize?client_id=app_EMo…&originator=werk&state=' + id };
    },
    'POST /api/account/sign-in/complete': function (b) {
      var f = flows[b.flow_id];
      if (!f) throw { status: 410, error: '로그인 세션이 만료되었습니다. 다시 시도하세요.' };
      var code = (b.code || '').trim();
      if (!code) throw { status: 400, error: '리디렉션된 주소 또는 코드를 붙여넣으세요.' };
      if (/bad|wrong/i.test(code)) throw { status: 401, error: '코드를 확인할 수 없습니다.' };
      var acc;
      if (f.account_id) { acc = state.openai.accounts.filter(function (a) { return a.id === f.account_id; })[0]; if (acc) acc.needs_reauth = false; }
      else {
        var first = !state.openai.accounts.length;
        acc = { id: nid('oa'), email: 'new' + state.openai.accounts.length + '@werk.dev', plan: 'Plus', active: first, needs_reauth: false };
        state.openai.accounts.push(acc);
      }
      delete flows[b.flow_id];
      return { account: acc };
    },
    'POST /api/account/sign-in/cancel': function (b) { delete flows[b.flow_id]; return {}; },
    'POST /api/account/consent': function (b) { state.openai.consent = !!b.enabled; return { consent: state.openai.consent }; },

    'PUT /api/account/key': function (b) {
      var k = (b.key || '').trim();
      if (!k) throw { status: 400, error: 'API 키를 입력하세요.' };
      if (!/^sk-or-/.test(k)) throw { status: 400, error: "OpenRouter 키는 'sk-or-' 로 시작합니다." };
      state.openrouter.masked = k.slice(0, 9) + '••••••••' + k.slice(-4);
      state.openrouter.state = /bad|rej/i.test(k) ? 'rejected' : 'verified';
      state.openrouter.voice_shared = true;
      return { state: state.openrouter.state };
    },
    'POST /api/account/key/recheck': function () {
      if (!state.openrouter.masked) throw { status: 400, error: '저장된 키가 없습니다.' };
      return { state: state.openrouter.state === 'rejected' ? 'rejected' : 'verified' };
    },
    'DELETE /api/account/key': function () { state.openrouter = { masked: '', state: 'none', voice_shared: false }; return {}; },

    'POST /api/account/harness-auth/start': function (b) {
      var id = nid('flow'); flows[id] = { kind: 'agy', harness: b.harness };
      return { flow_id: id, verify_url: 'https://accounts.google.com/o/oauth2/v2/auth?client_id=agy-cli…&state=' + id };
    },
    'POST /api/account/harness-auth/complete': function (b) {
      if (!flows[b.flow_id]) throw { status: 410, error: '로그인 세션이 만료되었습니다.' };
      state.harness.agy = { signed_in: true, verified: true, account: 'kang@werk.dev' };
      delete flows[b.flow_id]; return {};
    },
    'POST /api/account/harness-auth/reverify': function () {
      var a = state.harness.agy;
      if (!a.signed_in) throw { status: 401, error: '로그인되어 있지 않습니다.' };
      a.verified = true; return { verified: true };
    },

    'GET /api/account/werk-endpoints': function () { return { endpoints: clone(state.endpoints) }; },
    'POST /api/account/werk-endpoints': function (b) { var e = validEp(b); e.id = nid('ep'); state.endpoints.push(e); return { endpoint: e }; },
    'PUT /api/account/werk-endpoints': function (b) {
      var e = validEp(b), i = state.endpoints.findIndex(function (x) { return x.id === b.id; });
      if (i < 0) throw { status: 404, error: '엔드포인트를 찾을 수 없습니다.' };
      e.id = b.id; state.endpoints[i] = e; return { endpoint: e };
    },
    'DELETE /api/account/werk-endpoints': function (b) {
      state.endpoints = state.endpoints.filter(function (x) { return x.id !== b.id; }); return {};
    }
  };

  function validEp(b) {
    var name = (b.name || '').trim(), url = (b.base_url || '').trim();
    if (!name) throw { status: 400, error: '이름을 입력하세요.' };
    if (!/^https?:\/\/[^\s/]+/.test(url)) throw { status: 400, error: 'base_url 은 http(s):// 로 시작해야 합니다.' };
    return { name: name, kind: b.kind || 'ollama', base_url: url, model: (b.model || '').trim() };
  }

  global.MockApi = {
    scenarios: ['multi', 'empty', 'flows'],
    reset: function (name) { flows = {}; state = seed(name === 'empty' ? 'empty' : 'multi'); },
    call: function (method, path, body) {
      var fn = routes[method + ' ' + path];
      return sleep(LATENCY).then(function () {
        if (!fn) return fail(404, '알 수 없는 엔드포인트: ' + method + ' ' + path);
        try { var r = fn(body || {}); r.ok = true; return r; } catch (e) { return e && e.status ? fail(e.status, e.error) : fail(500, String(e)); }
      });
    }
  };
  global.MockApi.reset('multi');
})(window);
