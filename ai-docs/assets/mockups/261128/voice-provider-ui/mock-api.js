/* mock-api.js — 음성 API 제공자 목업의 백엔드 어댑터.
 * UI(app.js)는 이 파일의 VoiceApi 계약(요청/응답 봉투/지연/오류)만 알고 있으며,
 * 픽스처 상태는 전부 여기에만 존재한다. 실제 포팅 시 이 파일만 fetch 어댑터로 교체한다.
 *
 * 계약 요약
 *   VoiceApi.getVoiceConfig()                      -> {ok, data: Config}
 *   VoiceApi.putBackendOrder(order[])              -> {ok, data: Config}        (dashboard.voice_backend_order)
 *   VoiceApi.putOpenRouterVoice({shared, apiKey})  -> {ok, data: Config} | {ok:false, error}
 *                                                      (dashboard.voice_openrouter_shared_key + 키 슬롯 openrouter_voice)
 *   VoiceApi.setProviderKey(id, on)                -> {ok, data: Config}        (openai / groq / local 데모 구성)
 *   VoiceApi.startDictation(onEvent)               -> controller {stop(): Promise<{raw, refined, provider}>}
 *   VoiceApi.getSim()/setSim(patch)                -> 시뮬레이터 전용(환경·권한·상단 LLM 키)
 *   VoiceApi.subscribe(fn)                         -> unsubscribe, 상태 변경 시 Config 전달
 */
(function (global) {
  'use strict';

  var delay = function (ms) { return new Promise(function (r) { setTimeout(r, ms); }); };
  var lat = function () { return delay(160 + Math.random() * 180); };

  var LLM_KEY_MASK = 'sk-or-v1-…9f2a';

  var state = {
    sim: { env: 'mac', nativeStt: 'available', llmKey: true },
    dashboard: {
      voice_backend_order: ['native', 'openrouter', 'groq', 'openai', 'local'],
      voice_openrouter_shared_key: true
    },
    keys: {
      openrouter_voice: null,
      openai: null,
      groq: 'gsk_…c7d1',
      local: null
    }
  };

  var listeners = [];

  function nativeEval() {
    var s = state.sim;
    if (s.env === 'web') return { configured: true, available: false, code: 'desktop_only' };
    if (s.nativeStt === 'denied') return { configured: true, available: false, code: 'permission_denied' };
    if (s.nativeStt === 'offline') return { configured: true, available: false, code: 'offline_pack_missing' };
    return { configured: true, available: true, code: null };
  }

  function openrouterEval() {
    var shared = state.dashboard.voice_openrouter_shared_key && state.sim.llmKey;
    var mode = shared ? 'shared' : (state.keys.openrouter_voice ? 'dedicated' : 'none');
    return {
      configured: mode !== 'none',
      available: mode !== 'none',
      mode: mode,
      sharedFlag: state.dashboard.voice_openrouter_shared_key,
      maskedKey: state.keys.openrouter_voice
    };
  }

  function keyEval(id) {
    var k = state.keys[id];
    return { configured: !!k, available: !!k, maskedKey: k };
  }

  function build() {
    var providers = {
      native: nativeEval(),
      openrouter: openrouterEval(),
      openai: keyEval('openai'),
      groq: keyEval('groq'),
      local: keyEval('local')
    };
    var order = state.dashboard.voice_backend_order.slice();
    var chain = [];
    var active = null;
    order.forEach(function (id) {
      var p = providers[id];
      if (!p.configured) return;
      var st = p.available ? (active ? 'standby' : 'active') : 'unavailable';
      if (st === 'active') active = id;
      chain.push({ id: id, state: st, code: p.code || null });
    });
    return {
      voice_backend_order: order,
      voice_openrouter_shared_key: state.dashboard.voice_openrouter_shared_key,
      llm: { configured: state.sim.llmKey, masked: state.sim.llmKey ? LLM_KEY_MASK : null },
      env: state.sim.env,
      nativeStt: state.sim.nativeStt,
      providers: providers,
      chain: chain,
      active: active
    };
  }

  function emit() {
    var c = build();
    listeners.slice().forEach(function (fn) { fn(c); });
    return c;
  }

  /* ---- 받아쓰기 시뮬레이션 ---- */
  var SCRIPTS = [
    { raw: '내일 오전에 배포 전에 로그인 테스트 한번 돌려줘', refined: '내일 오전 배포 전에 로그인 테스트를 한 번 돌려 주세요.' },
    { raw: '그리고 결과는 요약해서 알려줘', refined: '그리고 결과는 요약해서 알려 주세요.' },
    { raw: '실패한 케이스가 있으면 로그도 같이 붙여줘', refined: '실패한 케이스가 있으면 로그도 같이 붙여 주세요.' }
  ];
  var cursor = 0;

  function startDictation(onEvent) {
    var provider = build().active;
    if (!provider) throw new Error('no_voice_provider');
    var speed = provider === 'native' ? 170 : 250;
    var base = cursor % SCRIPTS.length;
    cursor += 1;
    var sIdx = 0, wIdx = 0, doneRaw = [], doneRef = [], partial = [], timer = null, stopped = false;

    function tick() {
      if (stopped) return;
      var sc = SCRIPTS[(base + sIdx) % SCRIPTS.length];
      var words = sc.raw.split(' ');
      wIdx += 1;
      partial = words.slice(0, wIdx);
      onEvent({ type: 'partial', provider: provider, text: doneRaw.concat(partial).join(' ') });
      var wait = speed + Math.random() * 90;
      if (wIdx >= words.length) {
        doneRaw.push(sc.raw); doneRef.push(sc.refined);
        sIdx += 1; wIdx = 0; partial = [];
        wait = speed * 3.2;
      }
      timer = setTimeout(tick, wait);
    }
    timer = setTimeout(tick, 320);

    return {
      provider: provider,
      stop: function () {
        stopped = true;
        clearTimeout(timer);
        var raw = doneRaw.concat(partial).join(' ');
        var refined = doneRef.concat(partial.length ? [partial.join(' ') + '.'] : []).join(' ');
        return delay(950 + Math.random() * 350).then(function () {
          return { raw: raw, refined: refined, provider: provider };
        });
      }
    };
  }

  /* ---- 공개 API ---- */
  var ok = function (d) { return { ok: true, data: d }; };

  global.VoiceApi = {
    snapshot: build,
    subscribe: function (fn) {
      listeners.push(fn);
      return function () { listeners = listeners.filter(function (x) { return x !== fn; }); };
    },
    getSim: function () { return Object.assign({}, state.sim); },
    setSim: function (patch) { Object.assign(state.sim, patch); return emit(); },

    getVoiceConfig: function () { return lat().then(function () { return ok(build()); }); },

    putBackendOrder: function (order) {
      return lat().then(function () {
        var known = ['native', 'openrouter', 'openai', 'groq', 'local'];
        var valid = order.length === known.length && known.every(function (k) { return order.indexOf(k) >= 0; });
        if (!valid) return { ok: false, error: { code: 'invalid_order', message: '우선순위 목록이 올바르지 않습니다.' } };
        state.dashboard.voice_backend_order = order.slice();
        return ok(emit());
      });
    },

    putOpenRouterVoice: function (req) {
      return lat().then(function () {
        var shared = !!req.shared;
        var key = (req.apiKey || '').trim();
        if (shared && !state.sim.llmKey) {
          return { ok: false, error: { code: 'llm_key_missing', message: '상단 LLM OpenRouter 키가 없어 공유할 수 없습니다.' } };
        }
        if (!shared && key && key.indexOf('sk-or-') !== 0) {
          return { ok: false, error: { code: 'invalid_key', message: '키 형식이 올바르지 않습니다. sk-or- 로 시작해야 합니다.' } };
        }
        state.dashboard.voice_openrouter_shared_key = shared;
        if (!shared) state.keys.openrouter_voice = key ? 'sk-or-v1-…' + (key.slice(-4) || 'xxxx') : null;
        return ok(emit());
      });
    },

    setProviderKey: function (id, on) {
      return lat().then(function () {
        var demo = { openai: 'sk-proj-…41be', groq: 'gsk_…c7d1', local: 'http://127.0.0.1:8178' };
        state.keys[id] = on ? demo[id] : null;
        return ok(emit());
      });
    },

    startDictation: startDictation
  };
})(window);
