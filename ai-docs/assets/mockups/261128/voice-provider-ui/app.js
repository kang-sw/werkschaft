/* app.js — UI 뷰/상호작용. 데이터는 전부 VoiceApi(mock-api.js)를 통해서만 접근한다. */
(function () {
  'use strict';
  var $ = function (s, r) { return (r || document).querySelector(s); };
  var $$ = function (s, r) { return Array.prototype.slice.call((r || document).querySelectorAll(s)); };

  var META = {
    native:     { label: 'OS 네이티브', row: 'OS 네이티브 STT', av: 'OS' },
    openrouter: { label: 'OpenRouter',  row: 'OpenRouter (음성)', av: 'OR' },
    openai:     { label: 'OpenAI',      row: 'OpenAI API', av: 'OA' },
    groq:       { label: 'Groq',        row: 'Groq', av: 'GQ' },
    local:      { label: '로컬 STT',    row: '로컬 STT (Whisper)', av: 'WH' }
  };
  var ui = { theme: 'light', view: 'split' };
  var cfg = VoiceApi.snapshot();
  var focusId = null;

  function osName() { return cfg.env === 'win' ? 'Windows' : 'macOS'; }
  function toast(msg) {
    var t = $('#toast'); t.textContent = msg; t.hidden = false;
    clearTimeout(toast._t); toast._t = setTimeout(function () { t.hidden = true; }, 2600);
  }

  /* ===== 시뮬레이터 바 ===== */
  function renderSim() {
    var cur = { theme: ui.theme, view: ui.view, env: cfg.env, nativeStt: cfg.nativeStt, llmKey: String(cfg.llm.configured) };
    $$('#sim button[data-sim]').forEach(function (b) {
      b.setAttribute('aria-pressed', String(cur[b.dataset.sim] === b.dataset.val));
    });
    $('#simStt').classList.toggle('is-off', cfg.env === 'web');
    var h = $('#sim').offsetHeight; document.documentElement.style.setProperty('--simh', h + 'px');
  }
  $('#sim').addEventListener('click', function (e) {
    var b = e.target.closest('button[data-sim]'); if (!b) return;
    var k = b.dataset.sim, v = b.dataset.val;
    if (k === 'theme') { ui.theme = v; document.documentElement.dataset.theme = v; renderSim(); }
    else if (k === 'view') setView(v);
    else if (k === 'llmKey') VoiceApi.setSim({ llmKey: v === 'true' });
    else { var p = {}; p[k] = v; VoiceApi.setSim(p); }
  });
  function setView(v) { ui.view = v; document.body.dataset.view = v; renderSim(); }
  window.addEventListener('resize', function () { var h = $('#sim').offsetHeight; document.documentElement.style.setProperty('--simh', h + 'px'); });

  /* ===== 표시 텍스트 매핑 (원시 상태 -> 한국어) ===== */
  function nativeUnavail(code) {
    if (code === 'desktop_only') return { seg: '데스크톱 전용', pill: '데스크톱 전용',
      hint: '웹 브라우저에서는 OS 네이티브 STT를 사용할 수 없습니다. 데스크톱 앱에서 사용하세요.' };
    if (code === 'permission_denied') return { seg: '권한 필요', pill: osName() + ' 음성 인식 권한 필요',
      hint: cfg.env === 'win' ? '설정 ▸ 개인 정보 ▸ 음성에서 온라인 음성 인식을 켠 뒤 앱을 다시 시작하세요.'
        : '시스템 설정 ▸ 개인정보 보호 및 보안 ▸ 음성 인식에서 Werkschaft를 허용하세요.',
      action: '시스템 설정 열기' };
    if (code === 'offline_pack_missing') return { seg: '언어팩 필요', pill: '오프라인 언어팩 미설치',
      hint: '한국어 오프라인 인식 팩이 없습니다. 설치하면 네트워크 없이 기기 안에서 인식합니다.',
      action: '언어팩 설치 안내' };
    return null;
  }
  function orStatus(p) {
    if (p.mode === 'shared') return { tone: 'pri', text: '상단 OpenRouter 키 공유 중', hint: '에이전트용 LLM 키를 음성 입력에도 함께 사용합니다. 사용량은 같은 계정에 합산됩니다.' };
    if (p.mode === 'dedicated') return { tone: 'ok', text: '음성 전용 키 구성됨', hint: '음성 입력에만 쓰이는 별도 키 ' + p.maskedKey + ' 를 사용합니다.' };
    var why = (p.sharedFlag && !cfg.llm.configured) ? '공유가 켜져 있지만 상단 LLM 키가 없어 사용할 수 없습니다.' : '구성하면 우선순위 목록에 OpenRouter가 추가됩니다.';
    return { tone: 'off', text: '미구성', hint: why };
  }

  /* ===== 상단 LLM 카드 ===== */
  function renderLlm() {
    var c = cfg.llm, or = cfg.providers.openrouter;
    $('#llmCard').innerHTML =
      '<span class="mark">LLM</span><div class="llmmain"><div class="ln">OpenRouter 키 ' +
      (c.configured ? '<span class="pill ok">구성됨</span>' : '<span class="pill off">미구성</span>') + '</div>' +
      '<div class="ls">' + (c.configured ? '<code>' + c.masked + '</code> · 에이전트 대화와 요약에 쓰이는 상단 키' : '상단 LLM 키가 설정되어 있지 않습니다.') + '</div></div>' +
      '<div class="llmlink">' + (c.configured
        ? (or.mode === 'shared' ? '이 키를 <b>음성 입력과 공유 중</b>입니다. 아래 OpenRouter (음성)에서 해제할 수 있습니다.'
          : '음성 입력은 이 키를 <b>자동으로 쓰지 않습니다.</b> 아래 OpenRouter (음성) ▸ 구성에서 “상단 OpenRouter 키 사용”을 켜야 공유됩니다.')
        : '음성 입력에 OpenRouter를 쓰려면 아래에서 <b>음성 전용 키</b>를 직접 입력하세요.') + '</div>';
  }

  /* ===== 파워라인 우선순위 바 ===== */
  function renderBar() {
    var pl = $('#pl'), n = cfg.chain.length;
    var html = cfg.chain.map(function (c, i) {
      var m = META[c.id], info;
      if (c.state === 'active') info = '현재 활성';
      else if (c.state === 'standby') info = '대기';
      else info = (c.id === 'native' && nativeUnavail(c.code).seg) || '사용 불가';
      return '<div class="seg is-' + c.state + '" role="listitem" tabindex="0" draggable="true" data-id="' + c.id + '" aria-label="' + (i + 1) + '순위 ' + m.label + ', ' + info + '. 좌우 화살표로 순서 변경">' +
        '<div class="sn"><span class="num">' + (i + 1) + '</span><span class="nm">' + m.label + '</span></div><div class="si">' + info + '</div>' +
        '<span class="mv"><button type="button" data-dir="-1" aria-label="앞으로" title="앞으로" ' + (i === 0 ? 'disabled' : '') + '>‹</button><button type="button" data-dir="1" aria-label="뒤로" title="뒤로" ' + (i === n - 1 ? 'disabled' : '') + '>›</button></span></div>';
    }).join('');
    pl.innerHTML = html + '<div class="dropbar" id="dropbar"></div>';
    if (focusId) { var f = $('.seg[data-id="' + focusId + '"]', pl); if (f) f.focus(); focusId = null; }
  }

  function moveDisplayed(id, to) { /* to: 표시 목록에서의 최종 인덱스 */
    var shown = cfg.chain.map(function (c) { return c.id; });
    var from = shown.indexOf(id);
    if (from < 0) return;
    to = Math.max(0, Math.min(shown.length - 1, to));
    if (to === from) return;
    shown.splice(from, 1); shown.splice(to, 0, id);
    var k = 0;
    var next = cfg.voice_backend_order.map(function (oid) { return shown.indexOf(oid) >= 0 ? shown[k++] : oid; });
    focusId = id;
    VoiceApi.putBackendOrder(next).then(function (r) {
      if (!r.ok) { toast(r.error.message); return; }
      $('#plLive').textContent = META[id].label + ' 를 ' + (to + 1) + '순위로 이동했습니다.';
    });
  }

  (function bindBar() {
    var pl = $('#pl'), dragId = null, dropIdx = -1;
    function idxAt(x) {
      var segs = $$('.seg', pl);
      for (var i = 0; i < segs.length; i++) { var r = segs[i].getBoundingClientRect(); if (x < r.left + r.width / 2) return i; }
      return segs.length;
    }
    function showDrop(i) {
      var segs = $$('.seg', pl), bar = $('#dropbar'), plr = pl.getBoundingClientRect(), x;
      if (!segs.length) return;
      if (i >= segs.length) x = segs[segs.length - 1].getBoundingClientRect().right - plr.left - 6;
      else x = segs[i].getBoundingClientRect().left - plr.left + (i === 0 ? 2 : 8);
      bar.style.left = x + 'px'; bar.style.display = 'block';
    }
    pl.addEventListener('dragstart', function (e) {
      var s = e.target.closest && e.target.closest('.seg'); if (!s) return;
      dragId = s.dataset.id; s.classList.add('is-drag');
      e.dataTransfer.effectAllowed = 'move'; e.dataTransfer.setData('text/plain', dragId);
    });
    pl.addEventListener('dragover', function (e) {
      if (!dragId) return; e.preventDefault(); e.dataTransfer.dropEffect = 'move';
      dropIdx = idxAt(e.clientX); showDrop(dropIdx);
    });
    pl.addEventListener('dragleave', function (e) { if (!pl.contains(e.relatedTarget)) $('#dropbar').style.display = 'none'; });
    pl.addEventListener('drop', function (e) {
      if (!dragId) return; e.preventDefault();
      var shown = cfg.chain.map(function (c) { return c.id; });
      var from = shown.indexOf(dragId), to = dropIdx > from ? dropIdx - 1 : dropIdx;
      var id = dragId; dragId = null; $('#dropbar').style.display = 'none';
      if (from >= 0 && to !== from) moveDisplayed(id, to); else renderBar();
    });
    pl.addEventListener('dragend', function () { dragId = null; $('#dropbar').style.display = 'none'; $$('.seg.is-drag', pl).forEach(function (s) { s.classList.remove('is-drag'); }); });
    pl.addEventListener('keydown', function (e) {
      var s = e.target.closest('.seg'); if (!s || e.target !== s) return;
      var shown = cfg.chain.map(function (c) { return c.id; }), i = shown.indexOf(s.dataset.id);
      if (e.key === 'ArrowLeft') { e.preventDefault(); moveDisplayed(s.dataset.id, i - 1); }
      else if (e.key === 'ArrowRight') { e.preventDefault(); moveDisplayed(s.dataset.id, i + 1); }
    });
    pl.addEventListener('click', function (e) {
      var b = e.target.closest('.mv button'); if (!b || b.disabled) return;
      var s = b.closest('.seg'), shown = cfg.chain.map(function (c) { return c.id; });
      moveDisplayed(s.dataset.id, shown.indexOf(s.dataset.id) + Number(b.dataset.dir));
    });
  })();

  /* ===== 제공자 행 ===== */
  function renderRows() {
    var order = cfg.voice_backend_order;
    $('#prows').innerHTML = order.map(function (id) {
      var m = META[id], p = cfg.providers[id], pill = '', hint = '', act = '';
      if (id === 'native') {
        if (p.available) { pill = '<span class="pill ok">사용 가능 · ' + (cfg.env === 'win' ? 'Windows 음성 인식' : 'macOS Speech') + '</span>'; hint = '기기 안에서 인식하며 무료입니다. 오프라인에서도 동작합니다.'; }
        else { var u = nativeUnavail(p.code); pill = '<span class="pill warn">' + u.pill + '</span>'; hint = u.hint;
          if (u.action) act = '<button class="btn" type="button" data-act="nativefix">' + u.action + '</button>';
          if (p.code === 'desktop_only') pill = '<span class="pill off">' + u.pill + '</span>'; }
      } else if (id === 'openrouter') {
        var s = orStatus(p); pill = '<span class="pill ' + s.tone + '">' + s.text + '</span>'; hint = s.hint;
        act = '<button class="btn tint" type="button" data-act="orcfg">구성</button>';
      } else {
        if (p.configured) { pill = '<span class="pill ok">구성됨 · ' + p.maskedKey + '</span>'; hint = id === 'local' ? '로컬 Whisper 서버에 연결됩니다.' : '데모: 키가 구성된 상태입니다.'; act = '<button class="btn" type="button" data-act="unset" data-id="' + id + '">구성 해제</button>'; }
        else { pill = '<span class="pill off">미구성</span>'; hint = id === 'local' ? '로컬 Whisper 서버 주소를 등록하면 목록에 추가됩니다.' : '키를 등록하면 우선순위 목록에 추가됩니다. (데모: 버튼으로 즉시 구성)'; act = '<button class="btn tint" type="button" data-act="set" data-id="' + id + '">구성</button>'; }
      }
      return '<div class="prow" data-id="' + id + '"><span class="pav">' + m.av + '</span><div class="pmain"><div class="pname">' + m.row + pill + '</div><div class="phint">' + hint + '</div></div>' + act + '</div>';
    }).join('');
  }
  $('#prows').addEventListener('click', function (e) {
    var b = e.target.closest('button[data-act]'); if (!b) return;
    var a = b.dataset.act;
    if (a === 'orcfg') openModal();
    else if (a === 'nativefix') toast('(목업) OS 설정 화면으로 이동합니다 — 시뮬레이터의 “네이티브 STT”로 상태를 바꿔 보세요.');
    else { b.disabled = true; VoiceApi.setProviderKey(b.dataset.id, a === 'set').then(function () { toast(META[b.dataset.id].row + (a === 'set' ? ' 구성됨 — 우선순위 목록에 추가되었습니다.' : ' 구성 해제 — 목록에서 제외되었습니다.')); }); }
  });

  /* ===== OpenRouter(음성) 모달 ===== */
  var modal = { open: false, shared: false, key: '', saving: false, error: '' };
  function openModal() {
    modal = { open: true, shared: cfg.providers.openrouter.sharedFlag && cfg.llm.configured, key: '', saving: false, error: '' };
    $('#scrim').hidden = false; renderModal(); setTimeout(function () { var f = $('#dlg .sw'); if (f && !f.disabled) f.focus(); else { var i = $('#orKey'); if (i && !i.disabled) i.focus(); } }, 0);
  }
  function closeModal() { modal.open = false; $('#scrim').hidden = true; var b = $('.prow[data-id="openrouter"] .btn'); if (b) b.focus(); }
  function renderModal() {
    if (!modal.open) return;
    var llm = cfg.llm.configured, shared = llm && modal.shared, p = cfg.providers.openrouter;
    var ph = shared ? '상단 LLM 키 재사용 중 (' + cfg.llm.masked + ')' : 'sk-or-v1-… (비워 두면 미구성)';
    var cur = p.maskedKey && !shared ? '<div class="dnote">현재 저장된 음성 전용 키: ' + p.maskedKey + '</div>' : '';
    $('#dlg').innerHTML =
      '<h4 id="dlgTitle">OpenRouter (음성) 구성</h4><div class="dsub">음성 입력(STT)에 쓸 OpenRouter 키를 정합니다.</div>' +
      '<div class="swrow ' + (shared ? 'on' : '') + '"><div class="sl"><b>상단 OpenRouter 키 사용</b><span>' + (llm ? '상단 LLM 키 <code>' + cfg.llm.masked + '</code> 를 음성 입력에도 사용합니다.' : '상단 LLM 키가 없어 켤 수 없습니다.') + '</span></div>' +
      '<button class="sw" id="orShared" type="button" role="switch" aria-checked="' + shared + '" aria-label="상단 OpenRouter 키 사용" ' + (llm ? '' : 'disabled') + '></button></div>' +
      (llm ? '<div class="dnote">' + (shared ? '명시적으로 켠 경우에만 공유됩니다. 끄면 음성 전용 키를 따로 쓸 수 있습니다.' : '꺼져 있으면 음성은 상단 키를 사용하지 않습니다.') + '</div>'
           : '<div class="dnote warn">상단 LLM OpenRouter 키가 설정되어 있지 않습니다. 음성 전용 키를 직접 입력하세요.</div>') +
      '<label class="flbl" for="orKey">음성 전용 API 키 <span style="text-transform:none;letter-spacing:0;font-weight:500">(openrouter_voice)</span></label>' +
      '<input class="fin" id="orKey" type="password" autocomplete="off" spellcheck="false" placeholder="' + ph + '" value="' + (shared ? '' : modal.key.replace(/"/g, '&quot;')) + '" ' + (shared ? 'disabled' : '') + '>' + cur +
      (modal.error ? '<div class="merr" role="alert">' + modal.error + '</div>' : '') +
      '<div class="dfoot"><button class="btn" id="orCancel" type="button">취소</button><button class="btn pri" id="orSave" type="button" ' + (modal.saving ? 'disabled' : '') + '>' + (modal.saving ? '<span class="spin"></span>저장 중…' : '저장') + '</button></div>';
  }
  $('#dlg').addEventListener('click', function (e) {
    if (e.target.closest('#orShared')) { var b = e.target.closest('#orShared'); if (!b.disabled) { modal.key = ($('#orKey') || {}).value || modal.key; modal.shared = !modal.shared; modal.error = ''; renderModal(); var s = $('#orShared'); if (s) s.focus(); } }
    else if (e.target.closest('#orCancel')) closeModal();
    else if (e.target.closest('#orSave') && !modal.saving) {
      var inp = $('#orKey'); if (inp && !inp.disabled) modal.key = inp.value;
      modal.saving = true; modal.error = ''; renderModal();
      VoiceApi.putOpenRouterVoice({ shared: cfg.llm.configured && modal.shared, apiKey: modal.key }).then(function (r) {
        modal.saving = false;
        if (!r.ok) { modal.error = r.error.message; renderModal(); return; }
        closeModal();
        var s = orStatus(cfg.providers.openrouter);
        toast('OpenRouter (음성): ' + s.text + (cfg.providers.openrouter.configured ? ' — 우선순위 목록에 포함됩니다.' : ' — 우선순위 목록에서 제외되었습니다.'));
      });
    }
  });
  $('#dlg').addEventListener('input', function (e) { if (e.target.id === 'orKey') modal.key = e.target.value; });
  $('#scrim').addEventListener('mousedown', function (e) { if (e.target === $('#scrim')) closeModal(); });
  document.addEventListener('keydown', function (e) { if (e.key === 'Escape' && modal.open) closeModal(); });

  /* ===== 컴포저 ===== */
  var ed = $('#cfield'), mic = $('#cvox'), ptt = $('#ptt'), pttText = $('#pttText');
  var vstate = 'idle', dict = null, liveSpan = null;
  var pttTimer = null, pttDown = false, pttFired = false, pttSrc = false;

  function setPttHint(text, cls) { pttText.textContent = text; ptt.className = 'ptt' + (cls ? ' ' + cls : ''); }
  function renderComposer() {
    var a = cfg.active;
    var st = vstate !== 'idle' ? vstate : (a ? 'idle' : 'unconfigured');
    mic.dataset.state = st;
    var tip = { idle: '음성 입력 · 현재: ' + (a ? META[a].label : ''), unconfigured: '음성 제공자 설정 필요 — 눌러서 설정으로 이동',
      recording: '눌러서 입력 완료', transcribing: '텍스트 다듬는 중…' }[st];
    $('#cvoxTip').textContent = tip; mic.setAttribute('aria-label', tip);
    $('#provChip').innerHTML = a ? '음성 · <b>' + META[a].label + '</b>' : '음성 · 제공자 없음';
    if (vstate === 'idle' && !pttDown) {
      if (!a) setPttHint('음성 제공자를 먼저 설정하세요', 'warn'); else setPttHint('0.5초 길게 눌러 음성 입력');
    }
    var env = cfg.env === 'web' ? '웹 브라우저' : '데스크톱 앱 · ' + osName();
    $('#envChipS').textContent = env; $('#envChipC').textContent = env;
  }

  function caretRange() {
    var sel = getSelection();
    if (sel.rangeCount && ed.contains(sel.anchorNode)) { var r = sel.getRangeAt(0).cloneRange(); r.collapse(false); return r; }
    var r2 = document.createRange(); r2.selectNodeContents(ed); r2.collapse(false); return r2;
  }
  function startVoice(fromPtt) {
    if (vstate !== 'idle') return false;
    if (!cfg.active) { gotoSettings(); return false; }
    var r = caretRange();
    liveSpan = document.createElement('span');
    liveSpan.setAttribute('data-voice-span', 'live'); liveSpan.dataset.label = '음성 입력 중'; liveSpan.contentEditable = 'false';
    r.insertNode(liveSpan);
    var after = document.createRange(); after.setStartAfter(liveSpan); after.collapse(true);
    var sel = getSelection(); sel.removeAllRanges(); sel.addRange(after);
    ed.focus();
    dict = VoiceApi.startDictation(function (ev) { if (ev.type === 'partial' && liveSpan) { liveSpan.textContent = ev.text; ed.scrollTop = ed.scrollHeight; } });
    vstate = 'recording'; pttSrc = !!fromPtt;
    setPttHint(fromPtt ? '손을 떼면 입력 완료' : '다시 눌러 입력 완료', 'live');
    renderComposer(); return true;
  }
  function stopVoice() {
    if (vstate !== 'recording') return;
    var span = liveSpan, d = dict; dict = null;
    if (!span.textContent.trim()) { d.stop(); span.remove(); liveSpan = null; vstate = 'idle'; renderComposer(); return; }
    vstate = 'transcribing'; span.setAttribute('data-voice-span', 'refining'); span.dataset.label = '텍스트 다듬기 중…';
    setPttHint('텍스트 다듬는 중…'); renderComposer();
    d.stop().then(function (res) {
      span.textContent = res.refined; span.setAttribute('data-voice-span', 'done'); span.dataset.label = '다듬기 완료';
      setTimeout(function () { unwrap(span); liveSpan = null; vstate = 'idle'; renderComposer(); }, 1100);
    });
  }
  function unwrap(span) {
    var prev = span.previousSibling, ptxt = prev && prev.nodeType === 3 ? prev.data : '';
    var need = prev && !/\s$/.test(ptxt);
    var node = document.createTextNode((need ? ' ' : '') + span.textContent + ' ');
    span.replaceWith(node);
    var r = document.createRange(); r.setStart(node, node.length); r.collapse(true);
    var sel = getSelection();
    if (document.activeElement === ed || ed.contains(sel.anchorNode)) { sel.removeAllRanges(); sel.addRange(r); }
  }
  function rollbackSpace() {
    var sel = getSelection(); if (!sel.rangeCount) return;
    var r = sel.getRangeAt(0), n = r.startContainer;
    if (!r.collapsed || n.nodeType !== 3 || r.startOffset < 1) return;
    var ch = n.data[r.startOffset - 1];
    if (ch === ' ' || ch === ' ') {
      var off = r.startOffset - 1; n.deleteData(off, 1);
      var nr = document.createRange(); nr.setStart(n, off); nr.collapse(true); sel.removeAllRanges(); sel.addRange(nr);
    }
  }
  function gotoSettings() {
    if (ui.view === 'composer') setView('split');
    $('#route').textContent = '/settings?section=account&provider=voice';
    var sec = $('#voiceSection');
    sec.scrollIntoView({ block: 'start' }); sec.classList.remove('focus-ring'); void sec.offsetWidth; sec.classList.add('focus-ring');
    clearTimeout(gotoSettings._t); gotoSettings._t = setTimeout(function () { sec.classList.remove('focus-ring'); }, 2400);
    toast('사용 가능한 음성 제공자가 없어 설정으로 이동했습니다.');
  }

  mic.addEventListener('mousedown', function (e) { e.preventDefault(); });
  mic.addEventListener('click', function () {
    if (vstate === 'idle') startVoice(false);
    else if (vstate === 'recording') stopVoice();
  });

  ed.addEventListener('keydown', function (e) {
    if (e.key === ' ' || e.code === 'Space') {
      if (e.repeat) { if (pttDown) e.preventDefault(); return; }
      if (vstate !== 'idle' || e.isComposing) return;
      pttDown = true; pttFired = false;
      ptt.className = 'ptt holding'; void ptt.offsetWidth; ptt.className = 'ptt holding';
      pttTimer = setTimeout(function () {
        pttFired = true; rollbackSpace();
        if (!startVoice(true)) { setPttHint(cfg.active ? '' : '음성 제공자가 없습니다', 'warn'); }
      }, 500);
    } else if (pttDown && !pttFired) { clearTimeout(pttTimer); pttDown = false; renderComposer(); }
    else if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) { e.preventDefault(); send(); }
  });
  function pttRelease() {
    if (!pttDown) return;
    clearTimeout(pttTimer); var fired = pttFired; pttDown = false; pttFired = false;
    if (fired && vstate === 'recording' && pttSrc) stopVoice(); else renderComposer();
  }
  ed.addEventListener('keyup', function (e) { if (e.key === ' ' || e.code === 'Space') { if (pttFired) e.preventDefault(); pttRelease(); } });
  ed.addEventListener('blur', function () { if (pttDown) pttRelease(); });
  ed.addEventListener('input', function () { if (!ed.textContent && !ed.querySelector('[data-voice-span]')) ed.innerHTML = ''; });

  function send() {
    if (vstate !== 'idle') return;
    var t = ed.textContent.replace(/\s+/g, ' ').trim(); if (!t) return;
    var m = document.createElement('div'); m.className = 'msg me';
    m.innerHTML = '<div class="mh">나 · 방금</div><div class="mb"></div>'; $('.mb', m).textContent = t;
    $('#convo').appendChild(m); ed.innerHTML = ''; $('#convo').scrollTop = 1e6;
  }
  $('#csend').addEventListener('click', send);

  /* ===== 렌더 루프 ===== */
  function renderAll() {
    renderSim(); renderLlm(); renderBar(); renderRows(); renderComposer(); renderModal();
  }
  VoiceApi.subscribe(function (c) {
    cfg = c;
    if (vstate === 'recording' && !cfg.active) { /* 제공자 소실 시 중단 */ stopVoice(); }
    renderAll();
  });
  VoiceApi.getVoiceConfig().then(function (r) { if (r.ok) { cfg = r.data; renderAll(); } });
  renderAll();
})();
