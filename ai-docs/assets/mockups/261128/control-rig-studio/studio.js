/* Control-Rig & Pack Clip Studio — interaction logic (mock, no network). */
(function () {
  'use strict';
  const { EXPRS, PROPS, FINGER_PRESETS, KITS, SENTINELS, buildClips, P, TIMES } = window.SD;
  const $ = (s, r = document) => r.querySelector(s);
  const $$ = (s, r = document) => Array.from(r.querySelectorAll(s));
  const clamp = (v, a, b) => Math.min(b, Math.max(a, v));
  const lerp = (a, b, t) => a + (b - a) * t;
  const r1 = (n) => Math.round(n * 10) / 10;
  const esc = (s) => String(s).replace(/[&<>"]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]));
  const NS = 'http://www.w3.org/2000/svg';

  /* ───────── State ───────── */
  const clips = buildClips();
  const S = {
    kit: 'v1', key: 'phone_hold', tab: 'rig', mode: 'ik', slot: 280, theme: 'dark', propOv: null,
    t: 0, kf: 0, playing: false, loop: false, speed: 1, sym: true, side: 'R', sel: 'rh',
    dirty: new Set(), ov: { v1: new Set(KITS.v1.overrides), v0: new Set(KITS.v0.overrides) }, drafts: 0, imports: 0,
  };
  const SPEEDS = [0.5, 1, 1.5, 2];
  const DURS = [0.8, 1.0, 1.4, 2.0, 3.0];
  const EASES = ['Ease Out Back', 'Ease In Out', 'Linear', 'Spring'];
  const EASE = {
    'Ease Out Back': (u) => { const c1 = 1.70158, c3 = c1 + 1; return 1 + c3 * Math.pow(u - 1, 3) + c1 * Math.pow(u - 1, 2); },
    'Ease In Out': (u) => (u < 0.5 ? 2 * u * u : 1 - Math.pow(-2 * u + 2, 2) / 2),
    Linear: (u) => u,
    Spring: (u) => 1 - Math.cos(u * Math.PI * 4.5) * Math.exp(-u * 6),
  };
  const kit = () => KITS[S.kit];
  const clipOf = (k) => clips.find((c) => c.key === k);
  const cur = () => clipOf(S.key);
  const kitKey = () => (S.kit === 'v1' ? 'v1' : 'v0');
  const offOf = (c) => c.off[kitKey()];
  const isSentinel = (c) => c.kind === 'sentinel';
  const srcOf = (c) => (isSentinel(c) ? (S.ov[S.kit].has(c.key) ? 'override' : 'engine') : c.draft ? 'draft' : c.src);
  const propKind = (c) => S.propOv || c.prop;

  /* ───────── Pose math ───────── */
  function mix(a, b, u) {
    if (typeof a === 'number') return lerp(a, b, u);
    if (Array.isArray(a)) return a.map((v, i) => mix(v, b[i], u));
    return u < 0.5 ? a : b;
  }
  function mixPose(a, b, u) { const o = {}; for (const k in a) o[k] = mix(a[k], b[k], u); return o; }
  function poseAt(c, t, easeName) {
    const k = c.kfs;
    if (t <= k[0].t) return k[0].pose;
    if (t >= k[k.length - 1].t) return k[k.length - 1].pose;
    for (let i = 0; i < k.length - 1; i++) {
      if (t >= k[i].t && t <= k[i + 1].t) {
        const u = (t - k[i].t) / Math.max(1e-6, k[i + 1].t - k[i].t);
        return mixPose(k[i].pose, k[i + 1].pose, EASE[easeName](u));
      }
    }
    return k[0].pose;
  }
  function ik(Sh, H, Pl, a, b) {
    const dx = H[0] - Sh[0], dy = H[1] - Sh[1];
    const d = Math.hypot(dx, dy) || 1e-3;
    const dd = clamp(d, Math.abs(a - b) + 2, a + b - 0.5);
    const ux = dx / d, uy = dy / d;
    const x = (dd * dd + a * a - b * b) / (2 * dd);
    const h = Math.sqrt(Math.max(0, a * a - x * x));
    const bx = Sh[0] + ux * x, by = Sh[1] + uy * x;
    const e1 = [bx - uy * h, by + ux * h], e2 = [bx + uy * h, by - ux * h];
    const d1 = Math.hypot(e1[0] - Pl[0], e1[1] - Pl[1]), d2 = Math.hypot(e2[0] - Pl[0], e2[1] - Pl[1]);
    return { E: d1 < d2 ? e1 : e2, H: [Sh[0] + ux * dd, Sh[1] + uy * dd] };
  }
  function rig(p) {
    const sq = 1 - Math.abs(p.turn) * 0.4, tx = p.turn * 10;
    const sR = [140 + tx - 56 * sq, 238 + p.body], sL = [140 + tx + 56 * sq, 238 + p.body];
    const R = ik(sR, p.rh, p.rp, 78, 74), L = ik(sL, p.lh, p.lp, 78, 74);
    return { sR, sL, eR: R.E, hR: R.H, eL: L.E, hL: L.H };
  }
  function propInfo(c, p, g) {
    const kind = propKind(c);
    if (kind === 'none') return null;
    const o = offOf(c);
    let b;
    if (kind === 'laptop') b = [140, 322];
    else if (kind === 'phone') b = [g.hL[0] - 12, g.hL[1] - 12];
    else b = [g.hR[0] + 2, g.hR[1] - 4];
    return { kind, bx: b[0], by: b[1], x: b[0] + o.x, y: b[1] + o.y, s: 1 + o.z / 100, tilt: o.tilt };
  }

  /* ───────── Character drawing (local 280×350 slot space) ───────── */
  const SKIN = '#f1d0b6', SKIN2 = '#e0b79a', DARK = '#3a2f36';
  function hand(H, E, p, side) {
    const th = Math.atan2(H[1] - E[1], H[0] - E[0]);
    const deg = (th * 180) / Math.PI;
    const f = side === 'R' ? p.fR : p.fL;
    const sg = side === 'R' ? 1 : -1;
    const ang = [-52, -14, -2, 10, 22];       // thumb, index … pinky (R hand; mirrored for L)
    const base = [12, 17, 19, 17, 13];
    let s = '';
    for (let i = 0; i < 5; i++) {
      const a = (ang[i] * sg * Math.PI) / 180;
      const len = base[i] * (1 - 0.62 * f[i]);
      const sx = 9 + Math.cos(a) * 5, sy = Math.sin(a) * 6 + (i === 0 ? sg * -4 : 0);
      s += `<path d="M${r1(sx)} ${r1(sy)} L${r1(sx + Math.cos(a) * len)} ${r1(sy + Math.sin(a) * len)}" stroke="${SKIN2}" stroke-width="${i === 0 ? 6.5 : 5.4}" stroke-linecap="round" fill="none"/>`;
    }
    return `<g transform="translate(${r1(H[0])} ${r1(H[1])}) rotate(${r1(deg)})">
      <rect x="-9" y="-12.5" width="8" height="25" rx="4" fill="${kit().hood2}"/>
      ${s}<ellipse cx="7" cy="0" rx="10.5" ry="11.5" fill="${SKIN}"/></g>`;
  }
  function arm(Sh, E, H) {
    const dx = H[0] - E[0], dy = H[1] - E[1], l = Math.hypot(dx, dy) || 1;
    const hx = H[0] - (dx / l) * 5, hy = H[1] - (dy / l) * 5;
    return `<path d="M${r1(Sh[0])} ${r1(Sh[1])} L${r1(E[0])} ${r1(E[1])} L${r1(hx)} ${r1(hy)}" fill="none" stroke="${kit().hood2}" stroke-width="29" stroke-linecap="round" stroke-linejoin="round"/>
      <path d="M${r1(Sh[0])} ${r1(Sh[1])} L${r1(E[0])} ${r1(E[1])} L${r1(hx)} ${r1(hy)}" fill="none" stroke="${kit().hood}" stroke-width="24" stroke-linecap="round" stroke-linejoin="round"/>`;
  }
  function eye(cx, cy, dOuter, dInner, side, p, id, irisC) {
    const rx = p.expr === 'startled' ? 10.8 : 9.6, ry = p.expr === 'startled' ? 13.2 : 11.6;
    const lx = p.lx * 3.6, ly = p.ly * 3.6;
    const pr = p.expr === 'startled' ? 3 : 3.8;
    const left = side === 'R' ? dOuter : dInner, right = side === 'R' ? dInner : dOuter;
    const y0 = cy - ry - 3, x0 = cx - rx - 2, x1 = cx + rx + 2;
    const lid = (left > 0 || right > 0)
      ? `<polygon points="${x0},${y0} ${x1},${y0} ${x1},${r1(y0 + right + 3)} ${x0},${r1(y0 + left + 3)}" fill="${SKIN}"/>
         <line x1="${x0}" y1="${r1(y0 + left + 3)}" x2="${x1}" y2="${r1(y0 + right + 3)}" stroke="${DARK}" stroke-width="2.4"/>` : '';
    return `<clipPath id="${id}"><ellipse cx="${cx}" cy="${cy}" rx="${rx}" ry="${ry}"/></clipPath>
      <ellipse cx="${cx}" cy="${cy}" rx="${rx}" ry="${ry}" fill="#fbfaf8"/>
      <g clip-path="url(#${id})"><circle cx="${r1(cx + lx)}" cy="${r1(cy + ly)}" r="${rx - 2.2}" fill="${irisC}"/>
      <circle cx="${r1(cx + lx)}" cy="${r1(cy + ly)}" r="${pr}" fill="#1c1a22"/>
      <circle cx="${r1(cx + lx - 2.6)}" cy="${r1(cy + ly - 3.2)}" r="2" fill="#fff"/>${lid}</g>
      <path d="M${cx - rx} ${cy - 2} Q${cx} ${cy - ry - 3} ${cx + rx} ${cy - 2}" fill="none" stroke="${DARK}" stroke-width="2" stroke-linecap="round"/>`;
  }
  const arcEye = (cx, cy, happy) => happy
    ? `<path d="M${cx - 9} ${cy + 3} Q${cx} ${cy - 9} ${cx + 9} ${cy + 3}" fill="none" stroke="${DARK}" stroke-width="3" stroke-linecap="round"/>`
    : `<path d="M${cx - 9} ${cy} Q${cx} ${cy + 6} ${cx + 9} ${cy}" fill="none" stroke="${DARK}" stroke-width="3" stroke-linecap="round"/>`;
  function face(p) {
    const x = p.expr, ic = kit().iris;
    const open = { idle: [0, 0], happy: null, focused: [6, 12], half_lid: [11, 11], wink: [0, 0], merong: [0, 0], startled: [0, 0], annoyed: [7, 15] }[x];
    let eR, eL;
    if (x === 'happy') { eR = arcEye(120, 131, true); eL = arcEye(160, 131, true); }
    else {
      eR = x === 'wink' ? arcEye(120, 131, false) : eye(120, 131, open[0], open[1], 'R', p, 'ecR', ic);
      eL = eye(160, 131, open[0], open[1], 'L', p, 'ecL', ic);
    }
    const brow = { idle: [0, 0], happy: [-2, 0], focused: [2, 3], half_lid: [2, -1], wink: [0, 0], merong: [-3, 0], startled: [-9, -2], annoyed: [1, 6] }[x];
    const by = 110 + brow[0], ti = brow[1];
    const brows = `<path d="M108 ${by + 1 + ti * 0.1} Q119 ${by - 3} 130 ${by + ti * 0.9}" fill="none" stroke="${kit().hair}" stroke-width="3" stroke-linecap="round"/>
      <path d="M150 ${by + ti * 0.9} Q161 ${by - 3} 172 ${by + 1 + ti * 0.1}" fill="none" stroke="${kit().hair}" stroke-width="3" stroke-linecap="round"/>`;
    const mouth = {
      idle: `<path d="M131 160 Q140 166 149 160" fill="none" stroke="${DARK}" stroke-width="2.4" stroke-linecap="round"/>`,
      happy: `<path d="M127 156 Q140 178 153 156 Z" fill="#7a2f3a"/><path d="M133 166 Q140 172 147 166 Q140 163 133 166Z" fill="#e8808f"/>`,
      focused: `<path d="M133 162 L147 162" stroke="${DARK}" stroke-width="2.4" stroke-linecap="round"/>`,
      half_lid: `<path d="M133 162 Q140 164 147 162" fill="none" stroke="${DARK}" stroke-width="2.4" stroke-linecap="round"/>`,
      wink: `<path d="M128 158 Q140 171 152 158" fill="none" stroke="${DARK}" stroke-width="2.6" stroke-linecap="round"/>`,
      merong: `<path d="M129 156 Q140 168 151 156 Z" fill="#7a2f3a"/><path d="M134 160 Q134 176 140 177 Q146 176 146 160 Z" fill="#e8808f"/><path d="M140 162 L140 171" stroke="#c4606f" stroke-width="1.4"/>`,
      startled: `<ellipse cx="140" cy="164" rx="5.5" ry="7.5" fill="#6a2630"/>`,
      annoyed: `<path d="M131 166 Q140 158 149 166" fill="none" stroke="${DARK}" stroke-width="2.4" stroke-linecap="round"/>`,
    }[x];
    const blush = (x === 'happy' || x === 'wink' || x === 'merong')
      ? `<ellipse cx="109" cy="148" rx="8" ry="4.5" fill="#f08a96" opacity=".38"/><ellipse cx="171" cy="148" rx="8" ry="4.5" fill="#f08a96" opacity=".38"/>` : '';
    return `${blush}${eR}${eL}${brows}<path d="M140 143 l-1.5 6 l3 0" fill="none" stroke="${SKIN2}" stroke-width="1.6" stroke-linecap="round"/>${mouth}`;
  }
  function head(p) {
    const k = kit();
    return `<g transform="translate(${r1(p.turn * 14)} ${r1(p.body)}) rotate(${r1(p.rot)} 140 182)">
      <path d="M84 104 C84 56 106 42 140 42 C174 42 196 56 196 104 L198 166 C190 176 182 170 180 152 L100 152 C98 170 90 176 82 166 Z" fill="${k.hair2}"/>
      <circle cx="94" cy="130" r="8" fill="${SKIN2}"/><circle cx="186" cy="130" r="8" fill="${SKIN2}"/>
      <ellipse cx="140" cy="122" rx="47" ry="60" fill="${SKIN}"/>
      ${face(p)}
      <path d="M89 120 C88 70 112 50 140 50 C170 50 192 70 191 120 C182 98 170 86 152 80 C146 96 124 102 89 120 Z" fill="${k.hair}"/>
      <path d="M118 52 C126 60 126 70 120 80" fill="none" stroke="${k.hair2}" stroke-width="2.4" stroke-linecap="round" opacity=".7"/></g>`;
  }
  function torso(p) {
    const sq = 1 - Math.abs(p.turn) * 0.4, k = kit();
    return `<g transform="translate(${r1(140 + p.turn * 10)} ${r1(p.body)}) scale(${r1(sq * 100) / 100} 1) translate(-140 0)">
      <ellipse cx="140" cy="214" rx="64" ry="22" fill="${k.hood2}"/>
      <path d="M26 360 C30 292 48 250 96 232 C112 226 124 222 140 222 C156 222 168 226 184 232 C232 250 250 292 254 360 Z" fill="${k.hood}"/>
      <rect x="127" y="168" width="26" height="62" rx="12" fill="${SKIN2}"/>
      <path d="M100 224 Q140 258 180 224 Q170 210 140 210 Q110 210 100 224Z" fill="${k.hood2}"/>
      <path d="M133 244 L130 290 M147 244 L150 290" stroke="#d9dcea" stroke-width="2.2" stroke-linecap="round" opacity=".75"/>
      <circle cx="130" cy="291" r="2.6" fill="#d9dcea"/><circle cx="150" cy="291" r="2.6" fill="#d9dcea"/></g>`;
  }
  function propSVG(pi, behind) {
    if (!pi) return '';
    const isLap = pi.kind === 'laptop';
    if (behind === isLap) return '';
    const T = `translate(${r1(pi.x)} ${r1(pi.y)}) rotate(${pi.tilt}) scale(${r1(pi.s * 100) / 100})`;
    if (isLap) {
      return `<g transform="${T}"><rect x="-92" y="-30" width="184" height="86" rx="9" fill="#767b86"/><rect x="-92" y="-30" width="184" height="86" rx="9" fill="url(#lapG)"/>
        <rect x="-89" y="-27" width="178" height="3" rx="1.5" fill="#fff" opacity=".35"/><circle cx="0" cy="12" r="7" fill="#d9dce4" opacity=".55"/></g>`;
    }
    if (pi.kind === 'phone') {
      return `<g transform="${T}"><rect x="-17" y="-31" width="34" height="62" rx="7" fill="#1b1d22"/><rect x="-14" y="-28" width="28" height="56" rx="5" fill="url(#scrG)"/>
        <rect x="-5" y="-26" width="10" height="3" rx="1.5" fill="#0c1424" opacity=".7"/></g>`;
    }
    return `<g transform="${T}"><path d="M11 -6 q12 0 12 8 q0 8 -12 8" fill="none" stroke="#cfcac0" stroke-width="4"/><rect x="-12" y="-13" width="24" height="26" rx="4" fill="#e3ded4"/>
      <rect x="-12" y="-13" width="24" height="7" rx="3" fill="#8b5a3a"/><path d="M-4 -20 q3 -4 0 -8 M4 -20 q3 -4 0 -8" stroke="#fff" stroke-width="1.6" fill="none" opacity=".5" stroke-linecap="round"/></g>`;
  }
  function drawChar(p, c) {
    const g = rig(p), pi = propInfo(c, p, g);
    return `${torso(p)}${head(p)}${arm(g.sR, g.eR, g.hR)}${arm(g.sL, g.eL, g.hL)}${propSVG(pi, true)}${hand(g.hR, g.eR, p, 'R')}${hand(g.hL, g.eL, p, 'L')}${propSVG(pi, false)}`;
  }
  function mini(p, c) {
    const g = rig(p);
    const line = (a, e, h) => `<path d="M${a[0]} ${a[1]} L${e[0]} ${e[1]} L${h[0]} ${h[1]}" fill="none" stroke="${kit().hood}" stroke-width="30" stroke-linecap="round" stroke-linejoin="round"/>`;
    return `<svg viewBox="0 0 280 350" preserveAspectRatio="xMidYMid slice"><path d="M26 360 C30 292 48 250 96 232 C112 226 168 226 184 232 C232 250 254 292 254 360Z" fill="${kit().hood}"/>
      <ellipse cx="${140 + p.turn * 14}" cy="${122 + p.body}" rx="50" ry="62" fill="${SKIN}"/><path d="M86 112 C86 60 112 46 140 46 C168 46 194 60 194 112 C176 94 160 84 150 80 C140 98 118 102 86 112Z" transform="translate(${p.turn * 14} ${p.body})" fill="${kit().hair}"/>
      ${line(g.sR, g.eR, g.hR)}${line(g.sL, g.eL, g.hL)}<circle cx="${g.hR[0]}" cy="${g.hR[1]}" r="15" fill="${SKIN}"/><circle cx="${g.hL[0]}" cy="${g.hL[1]}" r="15" fill="${SKIN}"/>
      ${propKind(c) === 'laptop' ? '<rect x="48" y="' + (292 + offOf(c).y) + '" width="184" height="80" rx="9" fill="#767b86"/>' : ''}</svg>`;
  }

  /* ───────── SVG scene (frame, floor, gizmos) ───────── */
  const svg = $('#svg'), vp = $('#vp');
  let VW = 800, VH = 500, FS = 1, FX = 0, FY = 0;
  svg.innerHTML = `<defs>
    <linearGradient id="lapG" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#8d929d"/><stop offset="1" stop-color="#5f646e"/></linearGradient>
    <linearGradient id="scrG" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#7aa2ff"/><stop offset="1" stop-color="#3c4d8a"/></linearGradient>
    <linearGradient id="floorFade" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#fff" stop-opacity="0"/><stop offset=".35" stop-color="#fff" stop-opacity=".7"/><stop offset="1" stop-color="#fff" stop-opacity="1"/></linearGradient>
    <mask id="floorMask"><rect id="floorMaskR" x="0" y="0" width="800" height="500" fill="url(#floorFade)"/></mask></defs>
    <g id="floor" mask="url(#floorMask)"></g>
    <g id="frameG"><g id="char" class="char"></g><g id="over"></g><g id="giz"></g></g>`;
  const gFrame = $('#frameG'), gChar = $('#char'), gOver = $('#over'), gGiz = $('#giz'), gFloor = $('#floor');

  function layout() {
    const r = vp.getBoundingClientRect();
    VW = Math.max(300, r.width); VH = Math.max(260, r.height);
    svg.setAttribute('viewBox', `0 0 ${VW} ${VH}`);
    const availH = VH - 136;
    let s = S.slot / 280;
    if (350 * s > availH) s = availH / 350;
    FS = s; FX = (VW - 280 * s) / 2; FY = Math.max(78, (VH - 350 * s) / 2 + 6);
    gFrame.setAttribute('transform', `translate(${r1(FX)} ${r1(FY)}) scale(${FS})`);
    drawFloor(); drawOverlay();
  }
  function drawFloor() {
    const hor = VH * 0.40, vx = VW / 2;
    let s = '';
    for (let i = -14; i <= 14; i++) s += `<line x1="${vx}" y1="${hor}" x2="${vx + i * VW * 0.16}" y2="${VH + 10}"/>`;
    for (let j = 1; j <= 9; j++) { const y = hor + (VH - hor) * Math.pow(j / 9, 2.1); s += `<line x1="0" y1="${r1(y)}" x2="${VW}" y2="${r1(y)}"/>`; }
    gFloor.innerHTML = `<g stroke="${vp.dataset.theme === 'light' ? 'rgba(40,44,60,.16)' : 'rgba(190,205,255,.12)'}" stroke-width="1">${s}</g>`;
    $('#floorMaskR').setAttribute('y', hor); $('#floorMaskR').setAttribute('height', VH - hor); $('#floorMaskR').setAttribute('width', VW);
  }
  function drawOverlay() {
    const fs = (n) => r1(n / FS);
    const dim = 3000;
    const col = vp.dataset.theme === 'light' ? '#3a3f4a' : '#c4cad8';
    gOver.innerHTML = `
      <path d="M${-dim} ${-dim} H${dim} V${dim} H${-dim} Z M0 0 V350 H280 V0 Z" fill-rule="evenodd" fill="${vp.dataset.theme === 'light' ? 'rgba(30,32,40,.16)' : 'rgba(8,9,12,.44)'}" pointer-events="none"/>
      <rect x="0" y="0" width="280" height="350" rx="3" fill="none" stroke="#7aa2ff" stroke-opacity=".75" stroke-width="${fs(1.4)}" stroke-dasharray="${fs(6)} ${fs(4)}" pointer-events="none"/>
      <line x1="0" y1="42" x2="280" y2="42" stroke="${col}" stroke-opacity=".32" stroke-width="${fs(1)}" stroke-dasharray="${fs(3)} ${fs(4)}" pointer-events="none"/>
      <line x1="0" y1="182" x2="280" y2="182" stroke="${vp.dataset.theme === 'light' ? '#8a5200' : '#ffd479'}" stroke-opacity=".55" stroke-width="${fs(1)}" stroke-dasharray="${fs(3)} ${fs(4)}" pointer-events="none"/>
      <path d="M0 42 h${-fs(14)} M280 182 h${fs(14)}" stroke="${col}" stroke-width="${fs(1.4)}" pointer-events="none"/>
      <path d="M${-fs(14)} 42 m0 ${-fs(4)} v${fs(8)}" stroke="${col}" stroke-width="${fs(1.4)}" pointer-events="none"/>
      <text x="${-fs(20)}" y="${42 + fs(3.5)}" text-anchor="end" font-size="${fs(10.5)}" fill="${col}" fill-opacity=".85" font-family="var(--wb-mono)" font-weight="700" pointer-events="none">12% Headroom</text>
      <text x="${280 + fs(20)}" y="${182 + fs(3.5)}" text-anchor="start" font-size="${fs(10.5)}" fill="${vp.dataset.theme === 'light' ? '#8a5200' : '#ffd479'}" font-family="var(--wb-mono)" font-weight="700" pointer-events="none">Chin 52%</text>
      <text x="0" y="${350 + fs(16)}" font-size="${fs(10.5)}" fill="${col}" fill-opacity=".8" font-family="var(--wb-mono)" font-weight="700" pointer-events="none">RAIL SLOT · ${S.slot}px · 4:5</text>`;
  }

  /* gizmos (persistent) */
  const gz = {};
  function mkG(cls, html, id) { const g = document.createElementNS(NS, 'g'); g.setAttribute('class', cls); g.dataset.h = id; g.innerHTML = html; gGiz.appendChild(g); gz[id] = g; return g; }
  const dia = (c) => `<circle class="halo" r="22" fill="${c}" opacity="0"/><rect class="ring" x="-8" y="-8" width="16" height="16" rx="2.5" transform="rotate(45)" fill="${c}" fill-opacity=".2" stroke="${c}" stroke-width="2.4"/>`;
  const ringHTML = (c, l) => `<circle class="halo" r="26" fill="${c}"/><circle class="ring" r="15" fill="${c}" fill-opacity=".16" stroke="${c}" stroke-width="2.4"/><circle r="2.6" fill="${c}"/><text x="0" y="-21" text-anchor="middle" fill="${c}">${l}</text>`;
  ['rp', 'lp'].forEach((id) => { const l = document.createElementNS(NS, 'line'); l.setAttribute('id', 'pl-' + id); l.setAttribute('stroke', 'var(--c-pole)'); l.setAttribute('stroke-opacity', '.6'); l.setAttribute('stroke-dasharray', '3 3'); l.setAttribute('stroke-width', '1.4'); l.style.pointerEvents = 'none'; gGiz.appendChild(l); });
  mkG('hd', ringHTML('#ff8a80', 'R'), 'rh');
  mkG('hd', ringHTML('#5fd0e0', 'L'), 'lh');
  mkG('hd', dia('#ffd479'), 'rp');
  mkG('hd', dia('#ffd479'), 'lp');
  mkG('hd', `<circle class="halo" r="28" fill="#b7a5f5"/><circle class="ring" r="14" fill="#b7a5f5" fill-opacity=".1" stroke="#b7a5f5" stroke-width="2.2"/><circle r="3" fill="#b7a5f5"/><path d="M-22 0H-9M9 0H22M0 -22V-9M0 9V22" stroke="#b7a5f5" stroke-width="2" stroke-linecap="round"/>`, 'look');
  mkG('hd', `<circle class="halo" r="26" fill="#8fe0a8"/><rect class="ring" x="-13" y="-13" width="26" height="26" rx="6" fill="#8fe0a8" fill-opacity=".1" stroke="#8fe0a8" stroke-width="2.2" stroke-dasharray="4 3"/><circle r="3" fill="#8fe0a8"/>`, 'prop');
  const triad = document.createElementNS(NS, 'g');
  triad.setAttribute('class', 'triad');
  triad.innerHTML = `<g stroke-width="2.4" stroke-linecap="round" fill="none"><path d="M0 0H30" stroke="#ff6b6b"/><path d="M0 0V-30" stroke="#6bdc8a"/><path d="M0 0L-15 15" stroke="#6b9bff"/></g>
    <path d="M30 0l-6 -3.5v7z" fill="#ff6b6b"/><path d="M0 -30l-3.5 6h7z" fill="#6bdc8a"/><path d="M-15 15l1 -8l7 7z" fill="#6b9bff"/>
    <g font-size="8.5" font-weight="800" font-family="var(--wb-mono)"><text x="34" y="3" fill="#ff6b6b">X</text><text x="-3" y="-34" fill="#6bdc8a">Y</text><text x="-26" y="26" fill="#6b9bff">Z</text></g>`;
  gGiz.appendChild(triad);

  const MODE_HANDLES = { ik: ['rh', 'lh'], pole: ['rp', 'lp'], look: ['look'], prop: ['prop'] };

  function updateGizmos(p, c) {
    const g = rig(p), pi = propInfo(c, p, g);
    const place = (id, x, y) => gz[id].setAttribute('transform', `translate(${r1(x)} ${r1(y)})`);
    place('rh', g.hR[0], g.hR[1]); place('lh', g.hL[0], g.hL[1]);
    // pole diamonds are drawn at the pole target; dashed line links the solved elbow to it
    place('rp', p.rp[0], p.rp[1]); place('lp', p.lp[0], p.lp[1]);
    [['rp', g.eR, p.rp], ['lp', g.eL, p.lp]].forEach(([id, e, pt]) => { const l = $('#pl-' + id); l.setAttribute('x1', r1(e[0])); l.setAttribute('y1', r1(e[1])); l.setAttribute('x2', r1(pt[0])); l.setAttribute('y2', r1(pt[1])); });
    place('look', 140 + p.turn * 14 + p.lx * 40, 122 + p.body + p.ly * 40);
    if (pi) place('prop', pi.x, pi.y);
    const active = new Set(MODE_HANDLES[S.mode]);
    Object.keys(gz).forEach((id) => {
      gz[id].classList.toggle('dim', !active.has(id) || (id === 'prop' && !pi));
      gz[id].classList.toggle('sel', id === S.sel);
    });
    // selected-handle triad
    let tp = null;
    if (S.mode === 'prop') tp = pi ? [pi.x, pi.y] : null;
    else if (S.mode === 'ik') tp = S.sel === 'lh' ? g.hL : g.hR;
    else if (S.mode === 'pole') tp = S.sel === 'lp' ? p.lp : p.rp;
    else tp = [140 + p.turn * 14 + p.lx * 40, 122 + p.body + p.ly * 40];
    if (tp) { triad.setAttribute('transform', `translate(${r1(tp[0] + 20)} ${r1(tp[1] - 4)}) scale(.62)`); triad.style.display = ''; } else triad.style.display = 'none';
    const hintTxt = ({ ik: '링 드래그 = 손 위치 (팔 길이 제한)', pole: '다이아몬드 드래그 = 팔꿈치 방향', look: '레티클 드래그 = 시선 (±1.0)', prop: pi ? '앵커 드래그 = 프롭 X/Y' : '프롭 없음 — 하단에서 선택' })[S.mode];
    $('#vpHint').textContent = hintTxt; $('#vpHint').title = hintTxt + ' · Space = 재생';
  }

  /* drag */
  let drag = null;
  function toLocal(ev) {
    const m = gFrame.getScreenCTM();
    const pt = svg.createSVGPoint(); pt.x = ev.clientX; pt.y = ev.clientY;
    const q = pt.matrixTransform(m.inverse());
    return [q.x, q.y];
  }
  gGiz.addEventListener('pointerdown', (ev) => {
    const h = ev.target.closest('.hd');
    if (!h || h.classList.contains('dim')) return;
    ev.preventDefault();
    h.setPointerCapture(ev.pointerId);
    pauseAt(S.kf);
    S.sel = h.dataset.h === 'look' || h.dataset.h === 'prop' ? S.sel : h.dataset.h;
    drag = { id: h.dataset.h, el: h };
    h.classList.add('drag');
    $('#giz').dataset.drag = '1';
  });
  gGiz.addEventListener('pointermove', (ev) => {
    if (!drag) return;
    const [x, y] = toLocal(ev);
    const k = cur().kfs[S.kf], p = k.pose, c = cur();
    const id = drag.id;
    if (id === 'rh' || id === 'lh') p[id] = [clamp(x, -6, 286), clamp(y, 30, 372)];
    else if (id === 'rp' || id === 'lp') p[id] = [clamp(x, -30, 310), clamp(y, 30, 420)];
    else if (id === 'look') { p.lx = r1(clamp((x - 140 - p.turn * 14) / 40, -1, 1) * 100) / 100; p.ly = r1(clamp((y - 122 - p.body) / 40, -1, 1) * 100) / 100; }
    else if (id === 'prop') {
      const pi = propInfo(c, poseNow(), rig(poseNow()));
      const o = offOf(c);
      if (pi) { o.x = Math.round(clamp(x - pi.bx, -40, 40)); o.y = Math.round(clamp(y - pi.by, -80, 40)); }
    }
    markEdited();
    renderAll(true);
  });
  const endDrag = () => { if (drag) { drag.el.classList.remove('drag'); drag = null; renderTimeline(); renderInspector(); } };
  gGiz.addEventListener('pointerup', endDrag); gGiz.addEventListener('pointercancel', endDrag);

  /* ───────── Playback ───────── */
  const poseNow = () => poseAt(cur(), S.t, cur().ease);
  function pauseAt(kfIdx) { S.playing = false; if (kfIdx != null) { S.kf = kfIdx; S.t = cur().kfs[kfIdx].t; } syncPlayBtn(); }
  function syncPlayBtn() { $('#btnPlay').textContent = S.playing ? '❚❚' : '▶'; }
  let last = 0;
  function tick(ts) {
    if (S.playing) {
      const dt = Math.min(0.05, (ts - last) / 1000); last = ts;
      const c = cur(); S.t += dt * S.speed;
      if (S.t >= c.dur) { if (S.loop) S.t = S.t % c.dur; else { S.t = c.dur; S.playing = false; syncPlayBtn(); } }
      renderAll(false);
    } else last = ts;
    requestAnimationFrame(tick);
  }
  function togglePlay() {
    const c = cur();
    if (S.playing) { S.playing = false; } else { if (S.t >= c.dur - 1e-3) S.t = 0; S.playing = true; }
    syncPlayBtn(); renderAll(false);
  }
  $('#btnPlay').onclick = togglePlay;
  $('#btnLoop').onclick = () => { S.loop = !S.loop; $('#btnLoop').classList.toggle('on', S.loop); toast(S.loop ? '루프 켬' : '루프 끔'); };
  $('#pSpeed').onclick = () => { S.speed = SPEEDS[(SPEEDS.indexOf(S.speed) + 1) % SPEEDS.length]; renderBar(); };
  $('#pDur').onclick = () => {
    const c = cur(); const i = DURS.indexOf(c.dur); const nd = DURS[(i + 1) % DURS.length]; const f = nd / c.dur;
    c.kfs.forEach((k) => (k.t = r1(k.t * f * 1000) / 1000)); c.kfs[c.kfs.length - 1].t = nd; S.t = S.t * f; c.dur = nd; markEdited(); renderAll(true);
  };
  $('#pEase').onclick = () => { const c = cur(); c.ease = EASES[(EASES.indexOf(c.ease) + 1) % EASES.length]; markEdited(); renderAll(true); };
  $('#btnSnap').onclick = () => {
    const c = cur(); if (c.kfs.length >= 5) return;
    let bi = 0, bg = -1; for (let i = 0; i < c.kfs.length - 1; i++) { const g = c.kfs[i + 1].t - c.kfs[i].t; if (g > bg) { bg = g; bi = i; } }
    const t = r1(((c.kfs[bi].t + c.kfs[bi + 1].t) / 2) * 1000) / 1000;
    const pose = JSON.parse(JSON.stringify(poseNow()));
    c.kfs.splice(bi + 1, 0, { t, label: '스냅샷', pose }); S.playing = false; S.kf = bi + 1; S.t = t; markEdited(); toast(`KF ${bi + 2} · 포즈 스냅샷 추가 (${t.toFixed(2)}s)`); renderAll(true);
  };
  // scrub
  const track = $('#track');
  let scrub = false;
  function scrubTo(ev) {
    const rr = $('#lane').getBoundingClientRect();
    const u = clamp((ev.clientX - rr.left) / rr.width, 0, 1);
    S.playing = false; syncPlayBtn(); S.t = u * cur().dur; renderAll(false);
  }
  track.addEventListener('pointerdown', (ev) => { if (ev.target.closest('.kfc')) return; scrub = true; track.setPointerCapture(ev.pointerId); scrubTo(ev); });
  track.addEventListener('pointermove', (ev) => { if (scrub) scrubTo(ev); });
  track.addEventListener('pointerup', () => { scrub = false; });
  document.addEventListener('keydown', (ev) => {
    if (ev.code === 'Space' && !/INPUT|TEXTAREA|BUTTON/.test(document.activeElement.tagName)) { ev.preventDefault(); togglePlay(); }
  });

  /* ───────── Edits ───────── */
  function markEdited() {
    const c = cur(); S.dirty.add(S.kit + ':' + c.key);
    if (isSentinel(c) && !S.ov[S.kit].has(c.key)) { S.ov[S.kit].add(c.key); toast(`${c.key}: 엔진 기본 → 팩 오버라이드 생성됨`); renderRail(); renderTop(); }
    else renderTop(); updateRailDirty();
  }
  let toastT = 0;
  function toast(msg) { const t = $('#toast'); t.textContent = msg; t.classList.add('show'); clearTimeout(toastT); toastT = setTimeout(() => t.classList.remove('show'), 2200); }

  /* ───────── Top bar ───────── */
  function renderTop() {
    const k = kit();
    $('#packName').textContent = `${k.name} (${k.ver})`;
    $('#packDot').className = 'vdot' + (k.ok ? '' : ' warn');
    const kc = $('#kitChip'); kc.textContent = k.kitText; kc.className = 'kitchip' + (k.ok ? '' : ' warn');
    const ovN = SENTINELS.filter((s) => S.ov[S.kit].has(s)).length;
    $('#sumBadge').textContent = `센티널 ${ovN}/7 오버라이드 · 커스텀 ${clips.filter((c) => c.kind === 'custom').length}개`;
    const nd = Array.from(S.dirty).filter((x) => x.startsWith(S.kit + ':')).length;
    $('#saveCnt').textContent = nd; $('#saveCnt').style.display = nd ? '' : 'none';
    $('#railFootL').textContent = `${k.ver} · ${k.short}`;
    $('#railFootR').textContent = `${clips.length} clips`;
  }
  $('#packPill').onclick = () => {
    S.kit = S.kit === 'v1' ? 'v0' : 'v1'; S.playing = false; syncPlayBtn();
    toast(`팩 전환 → ${kit().name} (${kit().ver})`);
    renderTop(); renderRail(); renderAll(true);
  };
  $('#btnSave').onclick = () => {
    const n = Array.from(S.dirty).filter((x) => x.startsWith(S.kit + ':')).length;
    Array.from(S.dirty).filter((x) => x.startsWith(S.kit + ':')).forEach((x) => S.dirty.delete(x));
    clips.forEach((c) => { if (c.draft && c.key.startsWith('new_action')) { /* stays draft until renamed */ } });
    toast(n ? `팩에 저장됨 — 변경 ${n}개 · .clip.json ${clips.filter((c) => c.src !== 'vrma').length}개 / .vrma ${clips.filter((c) => c.src === 'vrma').length}개 (목업)` : '저장할 변경 사항 없음');
    renderTop(); renderRail();
  };
  function clipJSON(c) {
    const o = offOf(c), rr = (a) => a.map((v) => Math.round(v));
    return JSON.stringify({
      schema: 'werk.companion.clip/1', key: c.key, pack: kit().name, vrm: kit().ver, duration: c.dur, easing: c.ease, loop: S.loop,
      prop: { kind: c.prop, offset: o }, expose: isSentinel(c) ? { sentinel: true } : { companion_body: c.exposed, description: c.desc, expression: c.recExpr },
      keyframes: c.kfs.map((k) => ({ t: k.t, label: k.label, expression: k.pose.expr, head: { look: [k.pose.lx, k.pose.ly], tilt: k.pose.rot, lean: k.pose.body, turn: k.pose.turn },
        R: { ik: rr(k.pose.rh), pole: rr(k.pose.rp), fingers: k.pose.fR }, L: { ik: rr(k.pose.lh), pole: rr(k.pose.lp), fingers: k.pose.fL } })),
    }, null, 1);
  }
  $('#btnExport').onclick = () => { $('#mdTitle').textContent = `Export JSON — ${cur().key}.clip.json`; $('#mdPre').textContent = clipJSON(cur()); $('#scrim').classList.add('show'); };
  $('#mdClose').onclick = () => $('#scrim').classList.remove('show');
  $('#scrim').onclick = (e) => { if (e.target.id === 'scrim') $('#scrim').classList.remove('show'); };
  $('#mdCopy').onclick = () => { try { navigator.clipboard.writeText($('#mdPre').textContent); toast('클립보드에 복사됨'); } catch (e) { toast('복사 불가 (샌드박스) — 텍스트를 직접 선택하세요'); } };
  $('#btnImport').onclick = () => {
    S.imports++; const src = clipOf('victory_spin');
    const c = JSON.parse(JSON.stringify(src)); c.key = `imported_motion_${S.imports}`; c.sub = '.vrma · 방금 임포트'; c.src = 'vrma'; c.exposed = false; c.desc = ''; c.recExpr = [];
    clips.push(c); S.dirty.add(S.kit + ':' + c.key); selectClip(c.key); toast('.vrma 임포트 완료 (모의) — 설명을 작성하면 companion-body에 노출할 수 있습니다');
    S.tab = 'agent'; renderTop(); renderRail(); renderInspector();
  };

  /* ───────── Left rail ───────── */
  function rowHTML(c) {
    const src = srcOf(c);
    const meta = isSentinel(c) ? (src === 'engine' ? '엔진 기본' : `팩 오버라이드 · ${c.sub === '엔진 기본' ? '.clip.json' : c.sub}`) : c.sub;
    const dotCls = isSentinel(c) ? (src === 'engine' ? '' : 'override') : (src === 'vrma' ? 'vrma' : src === 'draft' ? 'draft' : 'clipjson');
    const end = (S.dirty.has(S.kit + ':' + c.key) ? '<span class="cr-dirty" title="저장되지 않은 변경"></span>' : '') +
      (!isSentinel(c) ? `<span class="cr-tag ex${c.exposed ? '' : ' off'}" title="companion-body 프롬프트 노출">${c.exposed ? '노출' : '숨김'}</span>` : '');
    return `<button class="clip-row${c.key === S.key ? ' cur' : ''}" data-key="${c.key}" title="${esc(c.key)} — ${esc(meta)}"><span class="cr-dot ${dotCls}"></span>
      <span class="cr-main"><span class="cr-key">${esc(c.key)}</span><span class="cr-meta">${esc(meta)}</span></span><span class="cr-end">${end}</span></button>`;
  }
  function renderRail() {
    const sent = clips.filter(isSentinel), cust = clips.filter((c) => !isSentinel(c));
    const ovN = sent.filter((c) => S.ov[S.kit].has(c.key)).length;
    $('#railScroll').innerHTML = `
      <div class="sec-lbl"><span>필수 센티널 키</span><small>${ovN}/7 오버라이드</small></div>
      <div class="sec-hint">항상 존재하는 상태 앵커 — 팩 오버라이드가 없으면 엔진 절차 모션으로 폴백합니다.</div>
      ${sent.map(rowHTML).join('')}
      <div class="sec-lbl" style="padding-top:16px"><span>커스텀 액션 카탈로그</span><small>${cust.length}개</small></div>
      <div class="sec-hint">팩 전용 액션 — <span class="mono">companion-body</span>에 설명과 함께 노출됩니다.</div>
      ${cust.map(rowHTML).join('')}
      <button class="add-row" id="btnAdd">＋ 새 액션 추가</button>`;
    $$('.clip-row', $('#railScroll')).forEach((b) => (b.onclick = () => { selectClip(b.dataset.key); }));
    $('#btnAdd').onclick = addDraft;
  }
  function updateRailDirty() { renderRail(); }
  function addDraft() {
    S.drafts++;
    const c = {
      key: `new_action_${S.drafts}`, kind: 'custom', sub: '.clip.json · 초안', src: 'clipjson', draft: true, dur: 1.4, ease: 'Ease Out Back', prop: 'none', desc: '', exposed: false, recExpr: [], recProp: [],
      off: { v1: { x: 0, y: 0, z: 0, tilt: 0 }, v0: { x: 0, y: 0, z: 0, tilt: 0 } },
      kfs: [0, 1, 2, 3].map((i) => ({ t: TIMES[i], label: ['준비', '동작', '유지', '복귀'][i], pose: P({}) })),
    };
    clips.push(c); S.dirty.add(S.kit + ':' + c.key); S.tab = 'agent'; selectClip(c.key);
    toast('새 초안 클립 — 키와 설명을 입력하세요 (키프레임은 idle 포즈로 시작)');
    renderTop();
  }
  function selectClip(k) {
    S.key = k; S.playing = false; S.kf = 0; S.t = 0; S.propOv = null; syncPlayBtn();
    S.sel = 'rh'; renderRail(); renderBar(); renderAll(true);
  }

  /* ───────── Timeline ───────── */
  function renderBar() {
    const c = cur();
    $('#vSpeed').textContent = S.speed.toFixed(1) + 'x'; $('#vDur').textContent = c.dur.toFixed(1) + 's'; $('#vEase').textContent = c.ease;
  }
  function renderTimeline() {
    const c = cur(), d = c.dur;
    const step = d <= 1.6 ? 0.1 : 0.25;
    let rl = '';
    for (let t = 0; t <= d + 1e-6; t += step) {
      const major = Math.abs(t / 0.5 - Math.round(t / 0.5)) < 1e-6;
      rl += `<span class="tick${major ? ' maj' : ''}" style="left:${(t / d) * 100}%"></span>` + (major ? `<span class="tick-l num" style="left:${(t / d) * 100}%">${t.toFixed(1)}</span>` : '');
    }
    $('#ruler').innerHTML = rl;
    $('#lane').innerHTML = c.kfs.map((k, i) => `<button class="kfc${i === S.kf ? ' sel' : ''}" data-i="${i}" style="left:${(k.t / d) * 100}%" title="KF ${i + 1} · ${k.label} ${k.t.toFixed(2)}s — 클릭하면 이 포즈로 스냅">
      ${mini(k.pose, c)}<span class="kt"><b>KF ${i + 1}</b><span>${esc(k.label)} ${k.t.toFixed(2)}s</span></span>${c.kfs.length > 4 && i > 0 && i < c.kfs.length - 1 ? '<span class="kx" data-x="' + i + '" title="스냅샷 삭제">✕</span>' : ''}</button>`).join('');
    $$('.kfc', $('#lane')).forEach((b) => {
      b.onclick = (e) => {
        if (e.target.dataset.x) { const i = +e.target.dataset.x; c.kfs.splice(i, 1); S.kf = Math.min(S.kf, c.kfs.length - 1); pauseAt(S.kf); markEdited(); renderAll(true); return; }
        pauseAt(+b.dataset.i); renderAll(true);
      };
    });
    $('#btnSnap').disabled = c.kfs.length >= 5;
    $('#btnSnap').title = c.kfs.length >= 5 ? '스냅샷은 최대 5개 (미니 타임라인)' : '현재 보이는 포즈를 새 키프레임으로 저장';
  }
  function updatePlayhead() {
    const c = cur(), lane = $('#lane'), tr = $('#track');
    const lr = lane.getBoundingClientRect(), tb = tr.getBoundingClientRect();
    $('#playhead').style.left = (lr.left - tb.left + (S.t / c.dur) * lr.width) + 'px';
    $('#tlTime').innerHTML = `<b>${S.t.toFixed(2)}</b> / ${c.dur.toFixed(2)}s`;
  }

  /* ───────── Inspector ───────── */
  const EXPR_KO = {};
  function curIsSent() { return isSentinel(cur()); }
  function renderHead() {
    const c = cur(), src = srcOf(c);
    const b = isSentinel(c) ? (src === 'engine' ? '<span class="badge mut">엔진 기본</span>' : '<span class="badge info">팩 오버라이드</span>') : `<span class="badge ok">${src === 'vrma' ? '.vrma' : '.clip.json'}</span>`;
    $('#inspHead').innerHTML = `<span class="ck">${esc(c.key)}</span>${b}<span class="grow"></span><span class="sub num">KF ${S.kf + 1}/${c.kfs.length}</span>`;
  }
  function fingerHTML(p) {
    const arr = S.sym || S.side === 'R' ? p.fR : p.fL;
    const nm = ['엄지', '검지', '중지', '약지', '소지'];
    const match = Object.keys(FINGER_PRESETS).find((k) => FINGER_PRESETS[k].every((v, i) => Math.abs(v - arr[i]) < 0.05));
    return `<div class="insp-sec"><div class="sh"><span>30-Bone 손가락</span><span class="acts">
        <span class="lr" id="lrSel" style="${S.sym ? 'display:none' : ''}"><button data-s="R" class="${S.side === 'R' ? 'on' : ''}">우</button><button data-s="L" class="${S.side === 'L' ? 'on' : ''}">좌</button></span>
        <label class="swt${S.sym ? ' on' : ''}" id="swSym"><span class="trk"></span>좌우 대칭</label></span></div>
      <div class="chips" style="margin-bottom:8px">${Object.keys(FINGER_PRESETS).map((k) => `<button class="chip${match === k ? ' on' : ''}" data-fp="${k}">${k}</button>`).join('')}</div>
      ${nm.map((n, i) => `<div class="sl-row"><label>${n}</label><input type="range" min="0" max="1" step="0.01" value="${arr[i]}" data-f="${i}"><output class="num" id="fo${i}">${arr[i].toFixed(2)}</output></div>`).join('')}</div>`;
  }
  function exprHTML(p) {
    return `<div class="insp-sec"><div class="sh"><span>표정 블렌드</span><span class="badge mut mono">${p.expr}</span></div>
      <div class="chips">${EXPRS.map((e) => `<button class="chip mono${p.expr === e ? ' on' : ''}" data-ex="${e}">${e}</button>`).join('')}</div></div>`;
  }
  function offsetHTML(c) {
    const o = offOf(c), pk = propKind(c), has = pk !== 'none';
    const sl = (id, lbl, mn, mx) => `<div class="sl-row"><label>${lbl}</label><input type="range" min="${mn}" max="${mx}" step="1" value="${o[id]}" data-o="${id}" ${has ? '' : 'disabled'}><output class="num" id="oo-${id}">${o[id]}</output></div>`;
    let hint;
    if (!has) hint = '<span class="badge mut">프롭 없음 — 오프셋 비활성</span>';
    else if (c.key === 'phone_hold' && S.kit === 'v1') hint = '<span class="badge accent">이 VRM 1.0 키트 전용 보정</span>';
    else if (isSentinel(c) && !S.ov[S.kit].has(c.key)) hint = '<span class="badge mut">엔진 기본값 · 편집 시 오버라이드 생성</span>';
    else hint = `<span class="badge accent">이 ${kit().ver} 키트 전용 보정</span>`;
    const note = c.key === 'phone_hold' ? (S.kit === 'v1' ? '후드티 소매 관통을 피하기 위한 보정값입니다.' : 'VRM 0.x 키트는 오버라이드 없이 엔진 기본 앵커를 사용합니다.') : '값은 활성 VRM 키트별로 팩에 저장됩니다.';
    return `<div class="insp-sec"><div class="sh"><span>프롭 오프셋 · ${PROPS[pk]}</span><span class="acts"><button class="linkbtn" id="offReset" ${has ? '' : 'disabled'}>↺ 초기화</button></span></div>
      <div style="margin-bottom:7px">${hint}</div>${sl('x', 'X', -40, 40)}${sl('y', 'Y', -80, 40)}${sl('z', 'Z', -30, 30)}${sl('tilt', 'Tilt', -45, 45)}
      <div class="note" style="margin-top:5px">${note}</div></div>`;
  }
  function agentHTML(c) {
    const sent = isSentinel(c);
    const keyField = c.draft
      ? `<input class="inp" id="keyInp" value="${esc(c.key)}" maxlength="32" spellcheck="false" title="초안 클립은 키를 수정할 수 있습니다">`
      : `<div class="chips"><span class="chip mono ro on">${esc(c.key)}</span>${sent ? '<span class="badge info">센티널 · 읽기 전용</span>' : '<span class="badge mut">저장된 키 · 읽기 전용</span>'}</div>`;
    const expo = sent
      ? `<div class="chips"><span class="chip ro on">시스템 센티널 (상태 머신 직결)</span></div><div class="note" style="margin-top:6px">상태 머신이 직접 재생하므로 <span class="mono">companion-body</span> 프롬프트에는 주입되지 않습니다.</div>`
      : `<label class="swt${c.exposed ? ' on' : ''}" id="swExp"><span class="trk"></span>companion-body 프롬프트에 노출</label>`;
    const desc = sent
      ? `<textarea id="descTa" readonly style="opacity:.7">${esc(c.desc)}</textarea><div class="note" style="margin-top:5px">센티널 설명은 문서용이며 에이전트에게 전달되지 않습니다.</div>`
      : `<textarea id="descTa" placeholder="에이전트가 언제 이 동작을 쓸지 한두 문장으로 적어 주세요." maxlength="140">${esc(c.desc)}</textarea>`;
    const recs = sent ? '' : `<div class="field"><div class="fl"><span>추천 표정 / 필요 프롭</span></div><div class="chips">
        ${EXPRS.map((e) => `<button class="chip rec mono${c.recExpr.includes(e) ? ' on' : ''}" data-re="${e}">${e}</button>`).join('')}</div>
        <div class="chips" style="margin-top:6px">${['laptop', 'phone', 'mug'].map((pk) => `<button class="chip rec${c.recProp.includes(pk) ? ' on' : ''}" data-rp="${pk}">⬚ ${PROPS[pk]}</button>`).join('')}</div></div>`;
    return `<div class="insp-sec"><div class="field"><div class="fl"><span>Action Key</span></div>${keyField}</div>
      <div class="field"><div class="fl"><span>노출 대상</span></div>${expo}</div>
      <div class="field"><div class="fl"><span>에이전트용 설명 (description)</span><em class="${c.desc.length > 120 ? '' : ''}" id="descCnt">${c.desc.length}/140</em></div>${desc}</div>
      ${recs}
      <div class="field" style="margin-bottom:4px"><div class="code-top"><span class="sh" style="margin:0">Live 프롬프트 카탈로그 미리보기</span><span class="num" id="tokEst"></span></div><div class="code" id="codeBox"></div></div></div>`;
  }
  function lineFor(c) {
    const rec = c.recExpr.length ? ` [expression: ${c.recExpr.join('|')}]` : '';
    const pr = c.recProp.length ? ` [prop: ${c.recProp.join('|')}]` : '';
    return { k: c.key, d: c.desc || '(설명 없음 — 에이전트가 용도를 알 수 없음)', rec: rec + pr };
  }
  function renderCode() {
    const c = cur(), box = $('#codeBox'); if (!box) return;
    const exposed = clips.filter((x) => !isSentinel(x) && x.exposed);
    const inList = exposed.includes(c);
    const row = (x, hl) => { const l = lineFor(x); return `<span class="ln${hl ? ' hl' : ' dimmed'}"><span class="d">- </span><span class="k">${esc(l.k)}</span><span class="d">: ${esc(l.d)}</span><span class="f">${esc(l.rec)}</span></span>`; };
    let h = `<span class="ln c"># companion-body · 사용 가능한 gesture  (팩: ${esc(kit().name)})</span>`;
    h += exposed.map((x) => row(x, x === c)).join('');
    if (!exposed.length) h += '<span class="ln c"># (노출된 커스텀 액션 없음)</span>';
    if (!isSentinel(c) && !inList) h += `<span class="ln c dimmed"># ${esc(c.key)} — 노출 꺼짐 → 위 목록에 주입되지 않음</span>`;
    h += '<span class="ln">&nbsp;</span>';
    if (isSentinel(c)) h += `<span class="ln c"># ${esc(c.key)}: 프롬프트 비노출 — 상태 머신이 자동 재생</span><span class="ln"><span class="c">#</span> <span class="k">ws/companion.act</span><span class="d">(gesture="</span><span class="s">&lt;custom_key&gt;</span><span class="d">")</span> <span class="c">만 호출 가능</span></span>`;
    else {
      const ex = c.recExpr[0] ? `<span class="d">, expression="</span><span class="s">${esc(c.recExpr[0])}</span><span class="d">"</span>` : '';
      h += `<span class="ln c"># 샘플 호출</span><span class="ln${inList ? '' : ' dimmed'}"><span class="k">ws/companion.act</span><span class="d">(gesture="</span><span class="s">${esc(c.key)}</span><span class="d">"</span>${ex}<span class="d">)</span></span>`;
    }
    box.innerHTML = h;
    const chars = (box.textContent || '').length;
    $('#tokEst').textContent = `≈ ${Math.round(chars / 2.4)} tok`;
  }
  function renderStrip() {
    const c = cur(), st = $('#expoStrip');
    if (isSentinel(c)) { st.className = 'expo-strip sent'; st.innerHTML = `<span class="es-ico">⚙</span><span class="es-t"><b>시스템 센티널 · 상태 머신 직결</b><span>프롬프트에는 주입되지 않음</span></span><span class="es-go">보기 ›</span>`; }
    else if (c.exposed) { st.className = 'expo-strip'; st.innerHTML = `<span class="es-ico">●</span><span class="es-t"><b>companion-body에 노출 중</b><span class="mono">gesture="${esc(c.key)}"</span></span><span class="es-go">프롬프트 ›</span>`; }
    else { st.className = 'expo-strip off'; st.innerHTML = `<span class="es-ico">○</span><span class="es-t"><b>에이전트에게 숨김</b><span>설명을 적고 노출을 켜세요</span></span><span class="es-go">설정 ›</span>`; }
    st.style.display = S.tab === 'rig' ? '' : 'none';
    const n = clips.filter((x) => !isSentinel(x) && x.exposed).length;
    $('#tabBadge').textContent = n; $('#tabBadge').className = 'bdg' + (n ? '' : ' off');
  }
  function renderInspector() {
    const c = cur(), p = poseNow();
    $$('.tab').forEach((t) => t.classList.toggle('on', t.dataset.tab === S.tab));
    renderHead();
    const body = $('#inspBody'), st = body.scrollTop;
    if (S.tab === 'rig') body.innerHTML = fingerHTML(p) + exprHTML(p) + offsetHTML(c);
    else body.innerHTML = agentHTML(c);
    body.scrollTop = st;
    bindInspector(); renderStrip(); if (S.tab === 'agent') renderCode();
  }
  function selPoses() { const k = cur().kfs[S.kf].pose; return k; }
  function bindInspector() {
    const c = cur();
    if (S.tab === 'rig') {
      $('#swSym').onclick = () => { S.sym = !S.sym; renderInspector(); };
      $$('#lrSel button').forEach((b) => (b.onclick = () => { S.side = b.dataset.s; S.sel = b.dataset.s === 'R' ? 'rh' : 'lh'; renderInspector(); renderAll(false); }));
      $$('[data-fp]').forEach((b) => (b.onclick = () => {
        pauseAt(S.kf); const p = selPoses(), v = FINGER_PRESETS[b.dataset.fp].slice();
        if (S.sym) { p.fR = v.slice(); p.fL = v.slice(); } else if (S.side === 'R') p.fR = v; else p.fL = v;
        markEdited(); renderAll(true);
      }));
      $$('[data-f]').forEach((inp) => (inp.oninput = () => {
        pauseAt(S.kf); const p = selPoses(), i = +inp.dataset.f, v = +inp.value;
        if (S.sym) { p.fR[i] = v; p.fL[i] = v; } else if (S.side === 'R') p.fR[i] = v; else p.fL[i] = v;
        $('#fo' + i).textContent = v.toFixed(2); markEdited(); renderAll(false);
      }));
      $$('[data-f]').forEach((inp) => (inp.onchange = () => { renderTimeline(); renderInspector(); }));
      $$('[data-ex]').forEach((b) => (b.onclick = () => { pauseAt(S.kf); selPoses().expr = b.dataset.ex; markEdited(); renderAll(true); }));
      $$('[data-o]').forEach((inp) => (inp.oninput = () => { const id = inp.dataset.o; offOf(c)[id] = +inp.value; $('#oo-' + id).textContent = inp.value; markEdited(); renderAll(false); }));
      $$('[data-o]').forEach((inp) => (inp.onchange = () => renderTimeline()));
      const rs = $('#offReset'); if (rs) rs.onclick = () => { Object.assign(offOf(c), { x: 0, y: 0, z: 0, tilt: 0 }); markEdited(); renderAll(true); };
    } else {
      const sw = $('#swExp'); if (sw) sw.onclick = () => { c.exposed = !c.exposed; markEdited(); renderRail(); renderInspector(); };
      const ta = $('#descTa');
      if (ta && !ta.readOnly) ta.oninput = () => { c.desc = ta.value; $('#descCnt').textContent = `${ta.value.length}/140`; markEdited(); renderCode(); renderStrip(); };
      $$('[data-re]').forEach((b) => (b.onclick = () => { const e = b.dataset.re; c.recExpr = c.recExpr.includes(e) ? c.recExpr.filter((x) => x !== e) : [...c.recExpr, e]; markEdited(); renderInspector(); }));
      $$('[data-rp]').forEach((b) => (b.onclick = () => { const e = b.dataset.rp; c.recProp = c.recProp.includes(e) ? c.recProp.filter((x) => x !== e) : [...c.recProp, e]; markEdited(); renderInspector(); }));
      const ki = $('#keyInp');
      if (ki) {
        ki.oninput = () => {
          const v = ki.value.toLowerCase().replace(/[^a-z0-9_]/g, '_');
          if (v && !clips.some((x) => x !== c && x.key === v)) { S.dirty.delete(S.kit + ':' + c.key); c.key = v; S.key = v; S.dirty.add(S.kit + ':' + v); }
          renderHead(); renderRail(); renderCode(); renderStrip();
          const nm = ki.value; if (nm !== v) ki.value = v;
        };
      }
    }
  }
  $$('.tab').forEach((t) => (t.onclick = () => { S.tab = t.dataset.tab; renderInspector(); }));
  $('#expoStrip').onclick = () => { S.tab = 'agent'; renderInspector(); };

  /* viewport controls */
  $$('#segModes button').forEach((b) => (b.onclick = () => {
    S.mode = b.dataset.mode; $$('#segModes button').forEach((x) => x.classList.toggle('on', x === b));
    S.sel = { ik: S.sel === 'lh' ? 'lh' : 'rh', pole: S.sel === 'lh' || S.sel === 'lp' ? 'lp' : 'rp', look: 'look', prop: 'prop' }[S.mode];
    renderAll(false);
  }));
  $$('#segSlot button').forEach((b) => (b.onclick = () => { S.slot = +b.dataset.slot; $$('#segSlot button').forEach((x) => x.classList.toggle('on', x === b)); layout(); renderAll(false); toast(`레일 슬롯 ${S.slot}px 프레이밍`); }));
  $$('#segTheme button').forEach((b) => (b.onclick = () => { S.theme = b.dataset.theme; vp.dataset.theme = S.theme; $$('#segTheme button').forEach((x) => x.classList.toggle('on', x === b)); drawFloor(); drawOverlay(); }));
  $$('#segProp button').forEach((b) => (b.onclick = () => {
    const k = b.dataset.prop; S.propOv = k === cur().prop ? null : k; renderAll(true);
  }));
  function syncPropSeg() { $$('#segProp button').forEach((b) => b.classList.toggle('on', b.dataset.prop === propKind(cur()))); }

  /* ───────── Master render ───────── */
  let lastKeyRender = '';
  function renderAll(full) {
    const c = cur(), p = poseNow();
    gChar.innerHTML = drawChar(p, c);
    updateGizmos(p, c);
    syncPropSeg();
    updatePlayhead();
    if (full) {
      renderBar(); renderTimeline(); updatePlayhead(); renderInspector(); renderTop(); updateRailCur();
    } else if (S.playing || scrub) {
      // keep inspector live while playing
      $$('[data-f]').forEach((inp, i) => { const arr = S.sym || S.side === 'R' ? p.fR : p.fL; inp.value = arr[i]; const o = $('#fo' + i); if (o) o.textContent = arr[i].toFixed(2); });
      $$('[data-ex]').forEach((b) => b.classList.toggle('on', b.dataset.ex === p.expr));
    }
    const near = c.kfs.findIndex((k) => Math.abs(k.t - S.t) < 1e-3);
    if (near >= 0 && near !== S.kf && !S.playing) { /* keep selection */ }
  }
  function updateRailCur() { $$('.clip-row', $('#railScroll')).forEach((b) => b.classList.toggle('cur', b.dataset.key === S.key)); }

  /* boot */
  new ResizeObserver(() => { layout(); renderAll(false); updatePlayhead(); }).observe(vp);
  new ResizeObserver(() => updatePlayhead()).observe($('#track'));
  layout(); renderTop(); renderRail(); renderBar(); renderAll(true);
  requestAnimationFrame(tick);
  window.__studio = { S, clips, selectClip, togglePlay, poseNow };
})();
