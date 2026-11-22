// IP-clean mesh-deform fixture: one synthetic outfit-baked `dangle` column on a
// 512 x 512 canvas (a torso, hanging arms, shorts and two legs with a knee and
// shoe each), drawn from plain rounded rectangles. It exercises a two-bone leg
// chain per side, a breath bone with pinned hands, and the whole-body roll.
// `deformFixture(makeCanvas)` takes a canvas factory (width, height) -> a
// canvas with a 2D context, so node tests and a browser both build it.
const SIZE = 512;
const INK = "#1e2433";

// Shapes by raster layer: [x, y, w, h, radius].
const SHAPES = {
  jacketFill: [
    [212, 168, 92, 128, 18],   // torso
    [190, 170, 28, 84, 12], [294, 170, 28, 84, 12],  // sleeves
    [214, 292, 88, 52, 8]      // shorts
  ],
  skinFill: [
    [240, 146, 32, 28, 10],    // neck
    [190, 250, 28, 44, 12], [294, 250, 28, 44, 12],  // hands
    [220, 344, 32, 126, 10], [262, 344, 32, 126, 10] // legs
  ],
  accentFill: [
    [214, 330, 88, 14, 4],     // hem trim
    [214, 462, 42, 26, 10], [258, 462, 42, 26, 10]   // shoes
  ]
};
const LINED = [].concat(...Object.values(SHAPES));

function rounded(ctx, x, y, w, h, r) {
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.lineTo(x + w - r, y); ctx.quadraticCurveTo(x + w, y, x + w, y + r);
  ctx.lineTo(x + w, y + h - r); ctx.quadraticCurveTo(x + w, y + h, x + w - r, y + h);
  ctx.lineTo(x + r, y + h); ctx.quadraticCurveTo(x, y + h, x, y + h - r);
  ctx.lineTo(x, y + r); ctx.quadraticCurveTo(x, y, x + r, y);
  ctx.closePath();
}

function mask(makeCanvas, shapes) {
  const canvas = makeCanvas(SIZE, SIZE), ctx = canvas.getContext("2d");
  ctx.fillStyle = "#ffffff";
  for (const shape of shapes) { rounded(ctx, ...shape); ctx.fill(); }
  return canvas;
}

function ink(makeCanvas) {
  const canvas = makeCanvas(SIZE, SIZE), ctx = canvas.getContext("2d");
  ctx.strokeStyle = INK;
  ctx.lineWidth = 3;
  for (const shape of LINED) { rounded(ctx, ...shape); ctx.stroke(); }
  // Knee creases: the line a deformed shin must keep.
  for (const x of [220, 262]) { ctx.beginPath(); ctx.moveTo(x, 408); ctx.lineTo(x + 32, 408); ctx.stroke(); }
  return canvas;
}

const box = (x0, y0, x1, y1) => [[x0, y0], [x1, y0], [x1, y1], [x0, y1]];
const swing = (phase, amplitude) => ({ oscillate: { rotDeg: [-amplitude, amplitude], hz: 1, phase } });

// deformFixture returns { manifest, bitmaps }. `amplitudes` overrides the
// pack-authored defaults: legs and roll degrees and the breath lift in px.
function deformFixture(makeCanvas, { legs = 6, roll = 2, breath = 4, withDeform = true } = {}) {
  const bitmaps = new Map([
    ["fill.png", mask(makeCanvas, SHAPES.jacketFill)],
    ["skin.png", mask(makeCanvas, SHAPES.skinFill)],
    ["accent.png", mask(makeCanvas, SHAPES.accentFill)],
    ["line.png", ink(makeCanvas)],
    ["head.png", mask(makeCanvas, [[226, 70, 60, 74, 28]])]
  ]);
  const deform = {
    grid: [12, 16],
    bones: {
      // Two-bone chain per leg: the hip swings the whole leg, the knee the shin
      // on top of it. The hip region stops above the shorts hem; the shin
      // region's notch sits above the shoe collar.
      hipL: { pivot: [236, 344], region: box(214, 340, 256, 480), feather: 14, motion: swing(0, legs) },
      kneeL: { pivot: [236, 408], parent: "hipL", region: box(214, 408, 256, 480), feather: 14, motion: swing(0.6, legs) },
      hipR: { pivot: [278, 344], region: box(258, 340, 300, 480), feather: 14, motion: swing(0.25, legs) },
      kneeR: { pivot: [278, 408], parent: "hipR", region: box(258, 408, 300, 480), feather: 14, motion: swing(0.85, legs) },
      // Shoulder breath; the hands (y 250..294) sit outside its falloff, so
      // they stay pinned.
      breath: { pivot: [256, 200], region: box(186, 160, 326, 214), feather: 20,
        motion: { oscillate: { ty: [0, -breath], hz: 1 } } }
    },
    roll: { pivot: [256, 150], motion: swing(0, roll) }
  };
  const manifest = {
    schema: "ws-companion/v1", id: "deform-fixture", name: "Deform Fixture", canvas: [SIZE, SIZE],
    registration: { headAnchor: [256, 150], feetPivot: [256, 488] },
    defaultOutfit: "plain",
    head: { pivot: [256, 108], egg: { skinFill: "head.png" } },
    poses: { stand: { headAnchor: [256, 150], layers: [{ file: "line.png" }] }, dangle: { fallback: "stand", headAnchor: [256, 150] } },
    gestures: {},
    outfits: { plain: { poses: {
      stand: { headAnchor: [256, 150], layers: [{ skinFill: "skin.png", jacketFill: "fill.png", accentFill: "accent.png", line: "line.png" }] },
      dangle: { headAnchor: [256, 150], layers: [{ skinFill: "skin.png", jacketFill: "fill.png", accentFill: "accent.png", line: "line.png" }],
        ...(withDeform ? { deform } : {}) }
    } } }
  };
  return { manifest, bitmaps };
}

module.exports = { deformFixture, SIZE };
