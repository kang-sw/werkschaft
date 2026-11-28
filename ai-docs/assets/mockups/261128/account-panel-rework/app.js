/* app.js — 설정 → 계정/음성 UI. 데이터는 전부 MockApi.call() 경유. */
(function () {
  'use strict';
  var $ = function (s, r) { return (r || document).querySelector(s); };
  var pane = $('#pane');
  var S = null;                       // /api/account/status 의 status
  var ui = { section: 'account', drawer: {}, rename: null, busy: {} };
  var HNAME = { claude: 'Claude Code', codex: 'Codex CLI' };
  var KIND = { ollama: 'Ollama', lmstudio: 'LM Studio', vllm: 'vLLM' };

  function esc(s) { return String(s == null ? '' : s).replace(/[&<>"']/g, function (c) { return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]; }); }
  function api(m, p, b) { return MockApi.call(m, p, b); }
  function toast(msg) {
    var t = $('#toast'); t.textContent = msg; t.hidden = false;
    clearTimeout(toast.t); toast.t = setTimeout(function () { t.hidden = true; }, 2200);
  }
  function refresh() { return api('GET', '/api/account/status').then(function (r) { S = r.status; render(); }); }
  function setDrawer(p, d) { if (d) ui.drawer[p] = d; else delete ui.drawer[p]; render(); }
  function drawerErr(p, e) { if (ui.drawer[p]) { ui.drawer[p].err = e.error || String(e); ui.drawer[p].busy = false; render(); } else toast(e.error || String(e)); }
  function run(p, fn) { return fn().catch(function (e) { drawerErr(p, e); }); }

  /* ---------- 공통 조각 ---------- */
  function btn(act, label, o) {
    o = o || {};
    var a = ' data-act="' + act + '"' + (o.p ? ' data-p="' + o.p + '"' : '') + (o.id ? ' data-id="' + esc(o.id) + '"' : '');
    return '<button class="ab ' + (o.cls || '') + (o.hold ? ' hold' : '') + '"' + a + (o.title ? ' title="' + esc(o.title) + '"' : '') + (o.dis ? ' disabled' : '') + '><span>' + label + '</span></button>';
  }
  function prov(ic, mono, name, sub, acts, kids, drawer) {
    return '<div class="prov"><div class="prov-h"><div class="mono-ic ' + ic + '" aria-hidden="true">' + mono + '</div>' +
      '<div class="prov-id"><b>' + name + '</b><span>' + sub + '</span></div><div class="prov-act">' + acts + '</div></div>' +
      '<div class="kids">' + kids + (drawer || '') + '</div></div>';
  }
  function grp(n, title, en, desc, body) {
    return '<section class="grp"><div class="grp-h"><div class="grp-n">' + n + '</div><div class="grp-t"><h2>' + title + '<small>' + en + '</small></h2><p>' + desc + '</p></div></div>' + body + '</section>';
  }
  function drawerShell(title, body, err, foot) {
    return '<div class="drawer"><div class="dr-t"><span>' + title + '</span></div>' + body +
      '<div class="dr-foot">' + (err ? '<span class="dr-err">' + esc(err) + '</span>' : '') + foot + '</div></div>';
  }
  function linkLine(url) {
    return '<div class="dr-line"><span class="dr-link" title="' + esc(url) + '">' + esc(url) + '</span>' + btn('copy', '링크 복사', { cls: 'pri', id: url }) + '</div>';
  }

  /* ---------- 하네스 (Claude / Codex) ---------- */
  function harnessBlock(h) {
    var H = S.harness[h], act = H.slots.filter(function (s) { return s.id === H.active; })[0] || H.slots[0];
    var any = H.slots.some(function (s) { return s.signed_in; });
    var sub = any ? '활성: ' + esc(act.label) + ' · 슬롯 ' + H.slots.length + '개' : '로그인 필요 · 슬롯 ' + H.slots.length + '개';
    var acts = btn('browser', '+ 브라우저 로그인', { p: h, cls: 'pri' }) + (h === 'codex' ? btn('device', '디바이스 코드', { p: h, cls: 'pri' }) : '') + btn('addopen', '기존 폴더', { p: h });
    var rows = H.slots.map(function (s) { return slotRow(h, s, s.id === H.active); }).join('');
    return prov(h === 'claude' ? 'ic-claude' : 'ic-codex', h === 'claude' ? 'C' : 'X', HNAME[h], sub, acts, rows, harnessDrawer(h));
  }
  function slotRow(h, s, active) {
    var renaming = ui.rename && ui.rename.h === h && ui.rename.id === s.id;
    var tag = s.kind === 'default' ? '기본' : s.kind;
    var name = renaming
      ? '<span class="row-edit"><input class="inp" data-rn value="' + esc(s.label) + '" maxlength="32">' + btn('rn-save', '저장', { p: h, id: s.id, cls: 'pri' }) + btn('rn-cancel', '취소') + '</span>'
      : '<span class="row-name">' + esc(s.label) + '</span>';
    var state = s.signed_in ? '<span class="pill ok dot">로그인됨</span>' : '<span class="pill warn dot">재인증 필요</span>';
    var acts = '';
    if (!renaming && s.kind !== 'default') acts += btn('rn-open', '이름 변경', { p: h, id: s.id });
    if (!s.signed_in) acts += btn('reauth', '재인증', { p: h, id: s.id, cls: 'warn' });
    if (s.kind !== 'default') acts += btn('slot-del', '삭제', { p: h, id: s.id, hold: 1, cls: 'danger', title: '1.5초 길게 눌러 삭제' });
    return '<div class="row' + (active ? ' is-active' : '') + '"><div class="lead">' +
      (active ? '<span class="pill on dot">사용 중</span>' : btn('slot-use', '사용', { p: h, id: s.id, cls: 'pri' })) + '</div>' +
      '<div class="row-main">' + name + '<span class="tag">' + tag + '</span>' + state +
      '<span class="row-sub" title="' + esc(s.config_dir) + '">' + esc(s.config_dir) + (s.account ? ' · ' + esc(s.account) : '') + '</span></div>' +
      '<div class="row-act">' + acts + '</div></div>';
  }
  function harnessDrawer(h) {
    var d = ui.drawer[h]; if (!d) return '';
    var cancel = btn('dr-cancel', '취소', { p: h });
    if (d.type === 'add') {
      return drawerShell('기존 폴더 추가', '<p class="dr-hint">이미 로그인된 ' + HNAME[h] + ' 설정 폴더(config_dir)를 슬롯으로 등록합니다.</p>' +
        '<div class="fields"><div class="fld"><label>config_dir</label><input class="inp m" data-f="dir" placeholder="~/.' + h + '-alt"></div>' +
        '<div class="fld"><label>라벨 (선택)</label><input class="inp" data-f="label" placeholder="예: Personal"></div></div>',
        d.err, cancel + btn('add-submit', d.busy ? '추가 중…' : '추가', { p: h, cls: 'solid', dis: d.busy }));
    }
    if (d.type === 'browser') {
      return drawerShell((d.slot_id ? '재인증' : '새 슬롯') + ' · 브라우저 로그인',
        '<p class="dr-hint">① 아래 링크를 브라우저에서 열어 로그인 → ② 표시된 코드를 붙여넣기.</p>' + linkLine(d.flow.url) +
        '<div class="fields">' + (d.slot_id ? '' : '<div class="fld"><label>슬롯 라벨 (선택)</label><input class="inp" data-f="label" placeholder="예: Work"></div>') +
        '<div class="fld"><label>인증 코드</label><input class="inp m" data-f="code" placeholder="붙여넣기"></div></div>',
        d.err, cancel + btn('flow-done', d.busy ? '확인 중…' : '완료', { p: h, cls: 'solid', dis: d.busy }));
    }
    if (d.type === 'device') {
      return drawerShell('디바이스 코드 로그인', '<p class="dr-hint">다른 기기 브라우저에서 주소를 열고 코드를 입력한 뒤 승인하세요.</p>' + linkLine(d.flow.verify_url) +
        '<div class="dr-line"><span class="dr-code">' + esc(d.flow.user_code) + '</span>' + btn('copy', '코드 복사', { cls: 'pri', id: d.flow.user_code }) + '</div>' +
        '<div class="dr-wait"><span class="spin"></span>승인을 기다리는 중 · 만료까지 15분</div>',
        d.err, cancel + btn('flow-done', d.busy ? '확인 중…' : '승인 완료 확인', { p: h, cls: 'solid', dis: d.busy }));
    }
    return '';
  }

  /* ---------- AgY ---------- */
  function agyBlock() {
    var a = S.harness.agy, d = ui.drawer.agy;
    var st = !a.signed_in ? '<span class="pill warn dot">로그인 필요</span>' : a.verified ? '<span class="pill ok dot">로그인됨 · 검증됨</span>' : '<span class="pill dot">미확인</span>';
    var acts = a.signed_in ? btn('agy-verify', '재확인', { cls: 'pri', dis: ui.busy.agy }) + btn('agy-login', '다시 로그인') : btn('agy-login', '+ 로그인', { cls: 'pri' });
    var row = '<div class="row"><div class="lead"><span class="pill on dot">단일</span></div><div class="row-main"><span class="row-name">Antigravity 계정</span>' + st +
      '<span class="row-sub">' + (a.account ? esc(a.account) + ' · ' : '') + '슬롯 없음 — 하나의 계정만 사용</span></div></div>';
    var dw = '';
    if (d) dw = drawerShell('Antigravity 로그인', '<p class="dr-hint">브라우저에서 Google 로그인을 마친 뒤 완료를 누르세요.</p>' + linkLine(d.flow.verify_url),
      d.err, btn('dr-cancel', '취소', { p: 'agy' }) + btn('agy-done', d.busy ? '확인 중…' : '완료', { cls: 'solid', dis: d.busy }));
    return prov('ic-agy', 'A', 'Antigravity <span class="tag">AgY</span>', a.signed_in ? '단일 계정 · 로그인됨' : '단일 계정 · 로그인 필요', acts, row, dw);
  }

  /* ---------- OpenAI ---------- */
  function openaiBlock() {
    var O = S.openai, act = O.accounts.filter(function (a) { return a.active; })[0], d = ui.drawer.openai;
    var sub = O.accounts.length ? '계정 ' + O.accounts.length + '개 · 활성 ' + esc(act ? act.email : '—') : '로그인된 계정 없음';
    var rows = O.accounts.length ? O.accounts.map(function (a) {
      var acts = (a.needs_reauth ? btn('oa-reauth', '재인증', { id: a.id, cls: 'warn' }) : '') + btn('oa-out', '로그아웃', { id: a.id, hold: 1, cls: 'danger', title: '1.5초 길게 눌러 로그아웃' });
      return '<div class="row' + (a.active ? ' is-active' : '') + '"><div class="lead">' + (a.active ? '<span class="pill on dot">사용 중</span>' : btn('oa-use', '사용', { id: a.id, cls: 'pri' })) + '</div>' +
        '<div class="row-main"><span class="row-name">' + esc(a.email) + '</span><span class="tag">' + esc(a.plan) + '</span>' +
        (a.needs_reauth ? '<span class="pill warn dot">재인증 필요</span>' : '<span class="pill ok dot">연결됨</span>') + '</div><div class="row-act">' + acts + '</div></div>';
    }).join('') : '<div class="empty-row">ChatGPT 플랜 계정을 연결하면 내장 Werk가 구독 한도로 동작합니다.</div>';
    var sub2 = '<div class="subset"><span class="sl">하위 설정</span><div class="st"><b>백그라운드 사용량 수집 동의</b><span>' +
      (O.accounts.length ? '남은 한도를 주기적으로 조회해 HUD에 표시합니다. 대화 내용은 읽지 않습니다.' : '계정을 연결하면 설정할 수 있습니다.') + '</span></div>' +
      '<button class="tgl" role="switch" aria-label="백그라운드 사용량 수집 동의" aria-checked="' + (O.consent ? 'true' : 'false') + '" data-act="consent"' + (O.accounts.length ? '' : ' disabled') + '></button></div>';
    var dw = '';
    if (d) dw = drawerShell(d.account_id ? 'OpenAI 재인증' : 'OpenAI 계정 추가', '<p class="dr-hint">① 링크를 브라우저에서 열어 로그인 → ② 이동된 주소(또는 코드)를 붙여넣기.</p>' + linkLine(d.flow.url) +
      '<div class="fld"><label>리디렉션 주소 / 코드</label><input class="inp m" data-f="code" placeholder="http://localhost:1455/auth/callback?code=…"></div>',
      d.err, btn('dr-cancel', '취소', { p: 'openai' }) + btn('oa-done', d.busy ? '확인 중…' : '완료', { cls: 'solid', dis: d.busy }));
    return prov('ic-openai', 'O', 'OpenAI <span class="tag">ChatGPT 플랜 · SIWC</span>', sub, btn('oa-add', '+ 계정 추가', { cls: 'pri' }), rows + sub2, dw);
  }

  /* ---------- OpenRouter ---------- */
  function orBlock() {
    var R = S.openrouter, d = ui.drawer.or, has = !!R.masked;
    var st = { verified: '<span class="pill ok dot">검증됨</span>', rejected: '<span class="pill fail dot">거부됨</span>', unverified: '<span class="pill dot">미검증</span>', none: '<span class="pill dot">키 없음</span>' }[R.state];
    var acts = btn('or-edit', has ? '키 편집' : '+ 키 입력', { cls: 'pri' }) + (has ? btn('or-recheck', ui.busy.or ? '확인 중…' : '재확인', { dis: ui.busy.or }) + btn('or-del', '삭제', { hold: 1, cls: 'danger', title: '1.5초 길게 눌러 삭제' }) : '');
    var row = has
      ? '<div class="row"><div class="lead"><span class="pill dot on">공용</span></div><div class="row-main"><span class="keypill">' + esc(R.masked) + '</span>' + st +
        (R.voice_shared ? '<button class="chip-link" data-act="goto" data-id="voice" title="음성 섹션으로 이동">음성(Voice) 섹션에서 공용 키 참조 중 →</button>' : '') + '</div></div>'
      : '<div class="empty-row">키가 없으면 OpenRouter 모델과 음성 인식(공용 키)을 사용할 수 없습니다.</div>';
    var dw = '';
    if (d) dw = drawerShell('OpenRouter API 키', '<p class="dr-hint">저장하면 즉시 검증합니다. 키는 음성 섹션과 공유됩니다.</p>' +
      '<div class="fld"><label>API 키</label><input class="inp m" type="password" data-f="key" placeholder="sk-or-v1-…" autocomplete="off"></div>',
      d.err, btn('dr-cancel', '취소', { p: 'or' }) + btn('or-save', d.busy ? '검증 중…' : '저장 · 검증', { cls: 'solid', dis: d.busy }));
    return prov('ic-or', 'R', 'OpenRouter <span class="tag">API</span>', has ? '공용 API 키 · ' + (R.state === 'verified' ? '검증됨' : R.state === 'rejected' ? '거부됨' : '미검증') : '공용 API 키 · 미설정', acts, row, dw);
  }

  /* ---------- 로컬 엔드포인트 ---------- */
  function epBlock() {
    var d = ui.drawer.ep, list = S.endpoints;
    var rows = list.length ? list.map(function (e) {
      return '<div class="row"><div class="lead"><span class="tag">' + esc(KIND[e.kind] || e.kind) + '</span></div><div class="row-main"><span class="row-name">' + esc(e.name) + '</span>' +
        (e.model ? '<span class="tag">' + esc(e.model) + '</span>' : '') + '<span class="row-sub" title="' + esc(e.base_url) + '">' + esc(e.base_url) + '</span></div>' +
        '<div class="row-act">' + btn('ep-edit', '편집', { id: e.id }) + btn('ep-del', '삭제', { id: e.id, hold: 1, cls: 'danger', title: '1.5초 길게 눌러 삭제' }) + '</div></div>';
    }).join('') : '<div class="empty-row">등록된 로컬 엔드포인트가 없습니다. Ollama · LM Studio · vLLM 같은 OpenAI 호환 서버를 추가하세요.</div>';
    var dw = '';
    if (d) {
      var e = d.id ? list.filter(function (x) { return x.id === d.id; })[0] || {} : {};
      dw = drawerShell(d.id ? '엔드포인트 편집' : '엔드포인트 추가',
        '<div class="fields"><div class="fld"><label>이름</label><input class="inp" data-f="name" value="' + esc(e.name || '') + '" placeholder="예: Ollama (로컬)"></div>' +
        '<div class="fld"><label>종류</label><select class="inp" data-f="kind">' + Object.keys(KIND).map(function (k) { return '<option value="' + k + '"' + (e.kind === k ? ' selected' : '') + '>' + KIND[k] + '</option>'; }).join('') + '</select></div>' +
        '<div class="fld"><label>base_url</label><input class="inp m" data-f="base_url" value="' + esc(e.base_url || '') + '" placeholder="http://127.0.0.1:11434/v1"></div>' +
        '<div class="fld"><label>기본 모델</label><input class="inp m" data-f="model" value="' + esc(e.model || '') + '" placeholder="qwen3:14b"></div></div>',
        d.err, btn('dr-cancel', '취소', { p: 'ep' }) + btn('ep-save', d.busy ? '저장 중…' : '저장', { cls: 'solid', dis: d.busy }));
    }
    return prov('ic-local', 'L', '로컬 Werk 엔드포인트 <span class="tag">agent.werk_endpoints</span>', list.length ? '엔드포인트 ' + list.length + '개 · OpenAI 호환' : 'OpenAI 호환 로컬 서버', btn('ep-new', '+ 엔드포인트', { cls: 'pri' }), rows, dw);
  }

  /* ---------- 섹션 렌더 ---------- */
  function accountHtml() {
    return '<div class="pane-head"><h1>계정</h1><p>사용 목적별로 나눠 관리합니다.</p></div>' +
      grp('1', '외부 CLI 하네스 계정', 'External CLI Harness Accounts', '에이전트가 실행하는 외부 CLI 도구(Claude Code · Codex · Antigravity)가 사용하는 로그인 계정입니다.', harnessBlock('claude') + harnessBlock('codex') + agyBlock()) +
      grp('2', '내장 Werk 런타임 · API', 'Built-in Werk Runtime & Endpoints', 'Werkschaft 내장 Werk 런타임이 직접 사용하는 자격 증명과 로컬 모델 서버입니다.', openaiBlock() + orBlock() + epBlock());
  }
  function voiceHtml() {
    var R = S.openrouter, ok = R.masked && R.state === 'verified';
    return '<div class="pane-head"><h1>음성</h1><p>음성 입력(STT) 설정 — 키는 계정 섹션과 공유합니다.</p></div>' +
      '<section class="vcard"><h2>음성 인식 제공자</h2><p>받아쓰기에 사용할 서비스를 고릅니다.</p>' +
      '<div class="vrow"><div class="mono-ic ic-or" aria-hidden="true">R</div><div class="grow"><b>OpenRouter</b><small>공용 API 키 사용 · ' + (R.masked ? esc(R.masked) : '키 없음') + '</small></div>' +
      (R.masked ? '<span class="pill ' + (ok ? 'ok' : 'fail') + ' dot">' + (ok ? '검증됨' : R.state === 'rejected' ? '거부됨' : '미검증') + '</span>' : '<span class="pill warn dot">키 필요</span>') +
      '<button class="chip-link" data-act="goto" data-id="account">공용 키는 계정 섹션에서 관리 →</button></div>' +
      '<div class="vrow"><div class="mono-ic" style="--ic:#888" aria-hidden="true">D</div><div class="grow"><b>기기 내 인식</b><small>별도 키 없음 · 오프라인 동작</small></div><span class="pill">사용 가능</span></div></section>' +
      '<section class="vcard"><h2>입력 동작</h2><p>이 섹션에는 음성 전용 옵션만 남습니다. API 키 입력란은 없습니다.</p>' +
      '<div class="vrow"><div class="grow"><b>누르고 말하기 단축키</b><small>Space 길게 누르기</small></div><span class="tag">Space</span></div></section>';
  }
  function render() {
    if (!S) return;
    pane.innerHTML = ui.section === 'voice' ? voiceHtml() : accountHtml();
    var nav = { account: $('#navAccount'), voice: $('#navVoice') };
    Object.keys(nav).forEach(function (k) { if (k === ui.section) nav[k].setAttribute('aria-current', 'page'); else nav[k].removeAttribute('aria-current'); });
    var rn = $('[data-rn]'); if (rn && !render.focused) { rn.focus(); rn.select(); render.focused = true; } else if (!rn) render.focused = false;
  }
  function val(p, f) { var el = $('[data-f="' + f + '"]', pane); return el ? el.value : ''; }

  /* ---------- 액션 ---------- */
  var A = {
    goto: function (el) { ui.section = el.dataset.id; pane.scrollTop = 0; render(); },
    copy: function (el) { try { navigator.clipboard && navigator.clipboard.writeText(el.dataset.id); } catch (e) {} toast('클립보드에 복사했습니다.'); },
    'dr-cancel': function (el) {
      var p = el.dataset.p, d = ui.drawer[p];
      if (d && d.flow) {
        var path = p === 'openai' ? '/api/account/sign-in/cancel' : '/api/account/slots/sign-in/cancel';
        if (p !== 'agy') api('POST', path, { flow_id: d.flow.flow_id });
      }
      setDrawer(p, null);
    },
    /* 하네스 슬롯 */
    'slot-use': function (el) { api('POST', '/api/account/slots/switch', { harness: el.dataset.p, id: el.dataset.id }).then(function () { toast('활성 슬롯을 변경했습니다.'); return refresh(); }); },
    browser: function (el) { startSlot(el.dataset.p, 'browser'); },
    device: function (el) { startSlot(el.dataset.p, 'device'); },
    reauth: function (el) { startSlot(el.dataset.p, 'browser', el.dataset.id); },
    addopen: function (el) { setDrawer(el.dataset.p, { type: 'add' }); },
    'add-submit': function (el) {
      var h = el.dataset.p, d = ui.drawer[h], dir = val(h, 'dir'), label = val(h, 'label');
      d.busy = true; d.err = ''; render(); restore(h, { dir: dir, label: label });
      run(h, function () { return api('POST', '/api/account/slots/add', { harness: h, config_dir: dir, label: label }).then(function () { toast('슬롯을 추가했습니다.'); setDrawer(h, null); return refresh(); }); })
        .then(function () { restore(h, { dir: dir, label: label }); });
    },
    'flow-done': function (el) {
      var h = el.dataset.p, d = ui.drawer[h], code = val(h, 'code'), label = val(h, 'label');
      d.busy = true; d.err = ''; render(); restore(h, { code: code, label: label });
      run(h, function () { return api('POST', '/api/account/slots/sign-in/complete', { harness: h, flow_id: d.flow.flow_id, code: code, label: label }).then(function () { toast('로그인을 완료했습니다.'); setDrawer(h, null); return refresh(); }); })
        .then(function () { restore(h, { code: code, label: label }); });
    },
    'slot-del': function (el) { api('POST', '/api/account/slots/remove', { harness: el.dataset.p, id: el.dataset.id }).then(function () { toast('슬롯을 삭제했습니다.'); return refresh(); }).catch(function (e) { toast(e.error); }); },
    'rn-open': function (el) { ui.rename = { h: el.dataset.p, id: el.dataset.id }; render(); },
    'rn-cancel': function () { ui.rename = null; render(); },
    'rn-save': function (el) {
      var v = $('[data-rn]', pane).value;
      api('POST', '/api/account/slots/rename', { harness: el.dataset.p, id: el.dataset.id, label: v }).then(function () { ui.rename = null; return refresh(); }).catch(function (e) { toast(e.error); });
    },
    /* AgY */
    'agy-login': function () { api('POST', '/api/account/harness-auth/start', { harness: 'agy' }).then(function (r) { setDrawer('agy', { type: 'login', flow: r }); }); },
    'agy-done': function () { var d = ui.drawer.agy; d.busy = true; render(); run('agy', function () { return api('POST', '/api/account/harness-auth/complete', { harness: 'agy', flow_id: d.flow.flow_id }).then(function () { toast('Antigravity 로그인을 완료했습니다.'); setDrawer('agy', null); return refresh(); }); }); },
    'agy-verify': function () { ui.busy.agy = true; render(); api('POST', '/api/account/harness-auth/reverify', { harness: 'agy' }).then(function () { toast('Antigravity 로그인이 유효합니다.'); }).catch(function (e) { toast(e.error); }).then(function () { ui.busy.agy = false; return refresh(); }); },
    /* OpenAI */
    'oa-use': function (el) { api('POST', '/api/account/openai/switch', { id: el.dataset.id }).then(function () { toast('활성 계정을 변경했습니다.'); return refresh(); }); },
    'oa-out': function (el) { api('POST', '/api/account/openai/sign-out', { id: el.dataset.id }).then(function () { toast('로그아웃했습니다.'); return refresh(); }); },
    'oa-add': function () { startOpenai(null); },
    'oa-reauth': function (el) { startOpenai(el.dataset.id); },
    'oa-done': function () {
      var d = ui.drawer.openai, code = val('openai', 'code'); d.busy = true; d.err = ''; render(); restore('openai', { code: code });
      run('openai', function () { return api('POST', '/api/account/sign-in/complete', { flow_id: d.flow.flow_id, code: code }).then(function () { toast('OpenAI 계정을 연결했습니다.'); setDrawer('openai', null); return refresh(); }); })
        .then(function () { restore('openai', { code: code }); });
    },
    consent: function (el) {
      var on = el.getAttribute('aria-checked') !== 'true'; el.setAttribute('aria-checked', on);
      api('POST', '/api/account/consent', { enabled: on }).then(function () { toast(on ? '사용량 수집에 동의했습니다.' : '사용량 수집을 중지했습니다.'); return refresh(); });
    },
    /* OpenRouter */
    'or-edit': function () { setDrawer('or', { type: 'key' }); },
    'or-save': function () {
      var d = ui.drawer.or, key = val('or', 'key'); d.busy = true; d.err = ''; render(); restore('or', { key: key });
      run('or', function () { return api('PUT', '/api/account/key', { key: key }).then(function (r) { toast(r.state === 'verified' ? '키를 검증하고 저장했습니다.' : '저장했지만 키가 거부되었습니다.'); setDrawer('or', null); return refresh(); }); })
        .then(function () { restore('or', { key: key }); });
    },
    'or-recheck': function () { ui.busy.or = true; render(); api('POST', '/api/account/key/recheck').then(function (r) { toast(r.state === 'verified' ? '키가 유효합니다.' : '키가 거부되었습니다.'); }).catch(function (e) { toast(e.error); }).then(function () { ui.busy.or = false; return refresh(); }); },
    'or-del': function () { api('DELETE', '/api/account/key').then(function () { toast('키를 삭제했습니다.'); return refresh(); }); },
    /* 로컬 엔드포인트 */
    'ep-new': function () { setDrawer('ep', { type: 'edit', id: null }); },
    'ep-edit': function (el) { setDrawer('ep', { type: 'edit', id: el.dataset.id }); },
    'ep-save': function () {
      var d = ui.drawer.ep, b = { id: d.id, name: val('ep', 'name'), kind: val('ep', 'kind'), base_url: val('ep', 'base_url'), model: val('ep', 'model') };
      d.busy = true; d.err = ''; render(); restore('ep', b);
      run('ep', function () { return api(d.id ? 'PUT' : 'POST', '/api/account/werk-endpoints', b).then(function () { toast('엔드포인트를 저장했습니다.'); setDrawer('ep', null); return refresh(); }); })
        .then(function () { restore('ep', b); });
    },
    'ep-del': function (el) { api('DELETE', '/api/account/werk-endpoints', { id: el.dataset.id }).then(function () { toast('엔드포인트를 삭제했습니다.'); return refresh(); }); }
  };
  function restore(p, o) { Object.keys(o).forEach(function (k) { var f = k === 'dir' ? 'dir' : k; var el = $('[data-f="' + f + '"]', pane); if (el && o[k] != null) el.value = o[k]; }); }
  function startSlot(h, method, slotId) {
    return api('POST', '/api/account/slots/sign-in/start', { harness: h, method: method, slot_id: slotId || undefined })
      .then(function (r) { setDrawer(h, { type: method, flow: r, slot_id: slotId || null }); });
  }
  function startOpenai(accountId) {
    return api('POST', '/api/account/sign-in/start', { account_id: accountId || undefined }).then(function (r) { setDrawer('openai', { type: 'oauth', flow: r, account_id: accountId }); });
  }

  /* 위임 이벤트 */
  pane.addEventListener('click', function (e) {
    var el = e.target.closest('[data-act]'); if (!el || el.disabled) return;
    if (el.classList.contains('hold')) return;       // 길게 누르기로만 실행
    var fn = A[el.dataset.act]; if (fn) fn(el);
  });
  pane.addEventListener('keydown', function (e) {
    if (e.key === 'Enter' && e.target.matches('[data-rn]')) { var b = $('[data-act="rn-save"]', pane); if (b) A['rn-save'](b); }
    if (e.key === 'Escape' && e.target.matches('[data-rn]')) A['rn-cancel']();
    if ((e.key === ' ' || e.key === 'Enter') && e.target.classList && e.target.classList.contains('hold') && !e.repeat) { e.preventDefault(); holdStart(e.target); }
  });
  pane.addEventListener('keyup', function (e) { if (e.target.classList && e.target.classList.contains('hold')) holdCancel(); });

  /* 1.5초 길게 눌러 확인 */
  var hold = null;
  function holdStart(el) {
    holdCancel(); el.classList.add('holding');
    hold = { el: el, t: setTimeout(function () { var h = hold; hold = null; h.el.classList.remove('holding'); h.el.classList.add('ready'); A[h.el.dataset.act](h.el); }, 1500) };
  }
  function holdCancel() { if (hold) { clearTimeout(hold.t); hold.el.classList.remove('holding'); hold = null; } }
  pane.addEventListener('pointerdown', function (e) { var el = e.target.closest('.hold'); if (el && e.button === 0) holdStart(el); });
  ['pointerup', 'pointerleave', 'pointercancel'].forEach(function (t) { pane.addEventListener(t, holdCancel, true); });
  window.addEventListener('blur', holdCancel);
  document.addEventListener('visibilitychange', holdCancel);
  document.addEventListener('keydown', function (e) { if (e.key === 'Escape') holdCancel(); });

  /* ---------- 컨트롤 바 ---------- */
  function seg(id, attr, cb) {
    $(id).addEventListener('click', function (e) {
      var b = e.target.closest('button'); if (!b) return;
      [].forEach.call($(id).children, function (x) { x.setAttribute('aria-pressed', x === b); });
      cb(b.dataset[attr]);
    });
  }
  seg('#simTheme', 'theme', function (t) { document.documentElement.dataset.theme = t; });
  seg('#simScn', 'scn', loadScenario);
  document.querySelector('.nav').addEventListener('click', function (e) {
    var b = e.target.closest('[data-section]'); if (b) A.goto({ dataset: { id: b.dataset.section } });
  });

  function loadScenario(name) {
    MockApi.reset(name); ui.drawer = {}; ui.rename = null; ui.busy = {}; ui.section = 'account';
    return refresh().then(function () {
      if (name !== 'flows') return;
      return Promise.all([
        api('POST', '/api/account/slots/sign-in/start', { harness: 'codex', method: 'device' }),
        api('POST', '/api/account/sign-in/start', {})
      ]).then(function (r) {
        ui.drawer.claude = { type: 'add' };
        ui.drawer.codex = { type: 'device', flow: r[0], slot_id: null };
        ui.drawer.openai = { type: 'oauth', flow: r[1], account_id: null };
        ui.drawer.or = { type: 'key' };
        render();
      });
    });
  }
  loadScenario('multi');
})();
