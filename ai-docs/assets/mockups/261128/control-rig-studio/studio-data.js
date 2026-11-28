/* Mock fixtures: VRM kits, sentinel/custom clips with 4 keyframe poses each. */
(function () {
  const EXPRS = ['idle', 'happy', 'focused', 'half_lid', 'wink', 'merong', 'startled', 'annoyed'];
  const PROPS = { laptop: '노트북', phone: '스마트폰', mug: '머그컵', none: '없음' };

  const FINGER_PRESETS = {
    Relaxed: [0.25, 0.3, 0.3, 0.35, 0.4],
    Typing: [0.35, 0.5, 0.55, 0.5, 0.45],
    'Phone Grip': [0.2, 0.85, 0.9, 0.9, 0.85],
    Point: [0.5, 0.0, 0.9, 0.95, 0.95],
  };

  const KITS = {
    v1: {
      id: 'v1', name: 'AvatarSample_M2', ver: 'VRM 1.0', short: 'Hoodie-A',
      hood: '#56689f', hood2: '#3f4d7c', hair: '#2d3045', hair2: '#3f4466', iris: '#5fa0d6',
      kitText: '키트 Hoodie-A · 프롭 앵커 4/4', ok: true,
      overrides: ['laptop_work', 'phone_hold', 'phone_typing'],
    },
    v0: {
      id: 'v0', name: '6493143135142452442', ver: 'VRM 0.x', short: 'Generic-0x',
      hood: '#8a5a73', hood2: '#6a4359', hair: '#5a3a28', hair2: '#77523a', iris: '#d08a52',
      kitText: '키트 Generic-0x · 앵커 3/4 (폴백 1)', ok: false,
      overrides: ['laptop_work', 'phone_typing'],
    },
  };

  const SENTINELS = ['idle', 'laptop_work', 'phone_hold', 'phone_typing', 'phone_putdown', 'knock_startle', 'wakeup_jolt'];

  const B = {
    rh: [102, 334], lh: [178, 334], rp: [50, 300], lp: [230, 300],
    rot: 0, lx: 0, ly: 0, body: 0, turn: 0, expr: 'idle',
    fR: FINGER_PRESETS.Relaxed.slice(), fL: FINGER_PRESETS.Relaxed.slice(),
  };
  function P(o) {
    const p = JSON.parse(JSON.stringify(B));
    o = JSON.parse(JSON.stringify(o));
    if (o.fing) { p.fR = o.fing.slice(); p.fL = o.fing.slice(); delete o.fing; }
    return Object.assign(p, o);
  }
  const NAMES = ['준비', '동작', '유지', '복귀'];
  const TIMES = [0, 0.35, 0.9, 1.4];
  function clip(def, poses) {
    const c = Object.assign({
      dur: 1.4, ease: 'Ease Out Back', prop: 'none', desc: '', exposed: false, recExpr: [], recProp: [],
      off: { v1: { x: 0, y: 0, z: 0, tilt: 0 }, v0: { x: 0, y: 0, z: 0, tilt: 0 } },
    }, def);
    c.kfs = poses.map((p, i) => ({ t: TIMES[i], label: NAMES[i], pose: p }));
    return c;
  }

  const open = [0.08, 0.1, 0.12, 0.1, 0.1];
  const grip = FINGER_PRESETS['Phone Grip'];
  const typ = FINGER_PRESETS.Typing;

  function buildClips() {
    return [
      clip({ key: 'idle', kind: 'sentinel', sub: '엔진 기본', desc: '대기 상태의 기본 호흡·시선 이동 (엔진 절차 모션)' }, [
        P({}), P({ body: 2, rot: 1, ly: 0.1 }), P({ body: 3, rot: -1, lx: 0.2 }), P({}),
      ]),
      clip({ key: 'laptop_work', kind: 'sentinel', sub: '.clip.json', prop: 'laptop', desc: '노트북 앞에서 타이핑 — 작업 상태 머신 직결' }, [
        P({ rh: [112, 298], lh: [168, 298], rp: [44, 282], lp: [236, 282], ly: 0.5, expr: 'focused', fing: typ }),
        P({ rh: [106, 288], lh: [170, 298], rp: [44, 282], lp: [236, 282], ly: 0.55, expr: 'focused', fing: typ }),
        P({ rh: [114, 298], lh: [174, 288], rp: [44, 282], lp: [236, 282], ly: 0.5, rot: -1, expr: 'focused', fing: typ }),
        P({ rh: [112, 298], lh: [168, 298], rp: [44, 282], lp: [236, 282], ly: 0.5, expr: 'focused', fing: typ }),
      ]),
      clip({
        key: 'phone_hold', kind: 'sentinel', sub: '.clip.json · 후드티 관통 보정', prop: 'phone',
        desc: '스마트폰을 들고 화면을 보는 자세 — 폰 사용 상태 머신 직결',
        off: { v1: { x: -10, y: 6, z: 14, tilt: -14 }, v0: { x: 0, y: 0, z: 0, tilt: -8 } },
      }, [
        P({ rh: [108, 326], lh: [182, 302], lp: [238, 292], ly: 0.55, expr: 'idle', fR: FINGER_PRESETS.Relaxed.slice(), fL: grip.slice() }),
        P({ rh: [108, 326], lh: [178, 262], lp: [238, 282], ly: 0.6, expr: 'focused', fR: FINGER_PRESETS.Relaxed.slice(), fL: grip.slice() }),
        P({ rh: [108, 326], lh: [176, 254], lp: [238, 276], ly: 0.6, rot: 2, expr: 'focused', fR: FINGER_PRESETS.Relaxed.slice(), fL: grip.slice() }),
        P({ rh: [108, 326], lh: [182, 288], lp: [238, 290], ly: 0.5, expr: 'idle', fR: FINGER_PRESETS.Relaxed.slice(), fL: grip.slice() }),
      ]),
      clip({ key: 'phone_typing', kind: 'sentinel', sub: '.clip.json', prop: 'phone', desc: '양손으로 스마트폰을 잡고 문자를 입력 — 폰 사용 상태 머신 직결' }, [
        P({ rh: [132, 296], rp: [50, 300], lh: [170, 268], lp: [236, 286], ly: 0.6, expr: 'focused', fR: FINGER_PRESETS.Point.slice(), fL: grip.slice() }),
        P({ rh: [140, 272], rp: [60, 300], lh: [170, 268], lp: [236, 286], ly: 0.6, expr: 'focused', fR: FINGER_PRESETS.Point.slice(), fL: grip.slice() }),
        P({ rh: [128, 290], rp: [60, 300], lh: [170, 268], lp: [236, 286], ly: 0.62, expr: 'focused', fR: FINGER_PRESETS.Point.slice(), fL: grip.slice() }),
        P({ rh: [138, 274], rp: [60, 300], lh: [170, 268], lp: [236, 286], ly: 0.6, expr: 'focused', fR: FINGER_PRESETS.Point.slice(), fL: grip.slice() }),
      ]),
      clip({ key: 'phone_putdown', kind: 'sentinel', sub: '엔진 기본', prop: 'phone', desc: '스마트폰을 내려놓는 동작 — 폰 사용 종료 전환' }, [
        P({ rh: [108, 326], lh: [178, 262], lp: [238, 282], ly: 0.6, expr: 'focused', fL: grip.slice() }),
        P({ rh: [108, 326], lh: [190, 302], lp: [240, 296], ly: 0.4, expr: 'idle', fL: grip.slice() }),
        P({ rh: [108, 326], lh: [198, 334], lp: [240, 310], ly: 0.25, expr: 'idle', fL: grip.slice() }),
        P({ rh: [108, 330], lh: [182, 336], lp: [236, 306], ly: 0.1, expr: 'idle' }),
      ]),
      clip({ key: 'knock_startle', kind: 'sentinel', sub: '엔진 기본', desc: '노크 소리에 깜짝 놀라는 반응 — 알림 이벤트 직결' }, [
        P({}),
        P({ body: -8, rot: -4, lx: 0.8, ly: -0.1, expr: 'startled', rh: [96, 296], lh: [184, 296], fing: open }),
        P({ body: -3, rot: -2, lx: 0.8, expr: 'startled', rh: [100, 306], lh: [180, 306], fing: open }),
        P({}),
      ]),
      clip({ key: 'wakeup_jolt', kind: 'sentinel', sub: '엔진 기본', desc: '졸다가 번쩍 깨어나는 반응 — 슬립 해제 이벤트 직결' }, [
        P({ ly: 0.6, rot: 6, body: 6, expr: 'half_lid' }),
        P({ body: -12, rot: -3, ly: -0.2, expr: 'startled', rh: [92, 290], lh: [188, 290], fing: open }),
        P({ body: -2, ly: 0, expr: 'annoyed', rh: [100, 320], lh: [180, 320] }),
        P({ expr: 'half_lid', body: 0 }),
      ]),
      clip({
        key: 'stretch_yawn', kind: 'custom', sub: '.clip.json · 기지개 & 하품', src: 'clipjson', exposed: true,
        desc: '긴 빌드나 작업이 끝났을 때 양팔을 뻗어 기지개를 켜며 휴식감을 표현한다.', recExpr: ['happy', 'half_lid'], recProp: [],
      }, [
        P({ expr: 'idle' }),
        P({ rh: [96, 96], lh: [184, 96], rp: [30, 170], lp: [250, 170], body: -10, ly: -0.5, expr: 'half_lid', fing: open }),
        P({ rh: [92, 74], lh: [188, 74], rp: [30, 160], lp: [250, 160], body: -14, ly: -0.5, rot: 2, expr: 'happy', fing: open }),
        P({ rh: [102, 330], lh: [178, 330], expr: 'happy' }),
      ]),
      clip({
        key: 'coffee_sip', kind: 'custom', sub: '.clip.json · 머그컵 한 모금', src: 'clipjson', prop: 'mug', exposed: true,
        desc: '잠깐 쉬어가는 분위기에서 머그컵을 들어 한 모금 마시며 여유를 표현한다.', recExpr: ['idle', 'happy'], recProp: ['mug'],
      }, [
        P({ rh: [104, 330], fR: grip.slice() }),
        P({ rh: [114, 262], rp: [44, 260], fR: grip.slice(), ly: 0.1 }),
        P({ rh: [120, 202], rp: [46, 230], fR: grip.slice(), rot: 3, ly: 0.1, expr: 'half_lid' }),
        P({ rh: [106, 318], fR: grip.slice(), expr: 'happy' }),
      ]),
      clip({
        key: 'victory_spin', kind: 'custom', sub: '.vrma · 외부 툴 임포트', src: 'vrma', exposed: true,
        desc: '테스트 통과·배포 성공 등 큰 성과를 축하할 때 양팔을 들고 한 바퀴 돈다.', recExpr: ['happy'], recProp: [],
      }, [
        P({ expr: 'happy' }),
        P({ rh: [58, 126], lh: [222, 126], rp: [34, 200], lp: [246, 200], turn: 0.6, body: -8, expr: 'happy', fing: open }),
        P({ rh: [62, 108], lh: [218, 108], rp: [34, 190], lp: [246, 190], turn: -0.5, body: -4, rot: -4, expr: 'happy', fing: open }),
        P({ rh: [70, 150], lh: [210, 150], rp: [34, 220], lp: [246, 220], turn: 0, expr: 'happy' }),
      ]),
      clip({
        key: 'peek_over_laptop', kind: 'custom', sub: '.clip.json · 노트북 너머 빼꼼', src: 'clipjson', prop: 'laptop', exposed: true,
        desc: '사용자가 오랫동안 말이 없을 때 노트북 너머로 빼꼼 쳐다보며 반응을 살핀다.', recExpr: ['half_lid', 'wink'], recProp: ['laptop'],
        off: { v1: { x: 0, y: -66, z: 0, tilt: 0 }, v0: { x: 0, y: -66, z: 0, tilt: 0 } },
      }, [
        P({ rh: [112, 306], lh: [168, 306], rp: [44, 290], lp: [236, 290], ly: 0.4, expr: 'focused', fing: typ }),
        P({ rh: [112, 306], lh: [168, 306], rp: [44, 290], lp: [236, 290], body: 40, rot: 7, lx: 0.6, expr: 'half_lid', fing: typ }),
        P({ rh: [112, 306], lh: [168, 306], rp: [44, 290], lp: [236, 290], body: 62, rot: 10, lx: 0.8, ly: -0.2, expr: 'wink', fing: typ }),
        P({ rh: [112, 306], lh: [168, 306], rp: [44, 290], lp: [236, 290], ly: 0.4, expr: 'focused', fing: typ }),
      ]),
    ];
  }

  window.SD = { EXPRS, PROPS, FINGER_PRESETS, KITS, SENTINELS, buildClips, P, TIMES, NAMES };
})();
