(function (C) {
  "use strict";
  const { own } = C;
  const TAU = 2 * Math.PI;
  const RAD = Math.PI / 180;
  const channels = { rotDeg: 0, tx: 0, ty: 0, sx: 1, sy: 1 };
  const channelNames = Object.keys(channels);
  const aliases = {
    "wave-pivot": { rotDeg: [-22, 18], hz: 2.2 },
    "bob-pivot": { ty: [-3, 0], hz: 0.7 },
    "sway-pivot": { rotDeg: [-7, 7], hz: 0.6 },
    breathe: { sy: [1, 1.018], ty: [0, -2], hz: 0.35 }
  };
  const fills = [
    ["jacketFill", "outfitPrimary"], ["accentFill", "outfitAccent"],
    ["skinFill", "skin"], ["hairFill", "hair"]
  ];
  const colors = { hair: "#ec8468", eye: "#dfa040", skin: "#f4cdb4", outfitPrimary: "#f2e5d4", outfitAccent: "#e6aa34" };
  // Wardrobe identifiers, matching the dashboard.companion_* config patterns.
  const wardrobeId = /^[a-z0-9][a-z0-9_-]{0,63}$/;
  const slotId = /^[a-z][a-z0-9_-]{0,31}$/;
  const channelId = /^[a-zA-Z][a-zA-Z0-9_-]{0,31}$/;
  const hexColor = /^#[0-9a-fA-F]{6}$/;
  const wardrobePairMax = 16;
  // Attachment default z by slot: tail behind the body, the rest around the
  // front hair. An extended slot defaults by its binding.
  const slotZ = { tail: 11, ears: 45, headdress: 56, accessory: 57 };
  const bindZ = { head: 57, body: 21 };
  const easings = {
    linear: [0, 0, 1, 1], easeIn: [0.42, 0, 1, 1], easeOut: [0, 0, 0.58, 1],
    easeInOut: [0.42, 0, 0.58, 1], anticipate: [0.36, -0.45, 0.65, 1],
    overshoot: [0.34, 1.45, 0.64, 1], snapBounce: [0.32, -0.35, 0.22, 1.32]
  };
  const stageChannels = { lift: [-2, 2, 0], tx: [-2, 2, 0], sx: [0.1, 3, 1], sy: [0.1, 3, 1], rotDeg: [-180, 180, 0] };
  const propChannels = {
    along: [-2, 3, 1], arcY: [-3, 3, 0], offsetX: [-3, 3, 0], offsetY: [-3, 3, 0],
    scale: [0, 4, 1], rotDeg: [-720, 720, 0], alpha: [0, 1, 1]
  };

  function finite(x, fallback = 0) { return Number.isFinite(x) ? x : fallback; }
  function clamp(x, low, high) { return Math.min(high, Math.max(low, x)); }
  function mix(a, b, t) { return a + (b - a) * t; }
  function inRange(x, low, high) { return Number.isFinite(x) && x >= low && x <= high; }
  function bounded(x, low, high, fallback = 0) { return inRange(x, low, high) ? x : fallback; }
  function record(value) { return value && typeof value === "object" && !Array.isArray(value); }
  function timingId(value) { return typeof value === "string" && /^[a-z\d_-]{1,64}$/i.test(value); }
  function bezierTuple(value) {
    return Array.isArray(value) && value.length === 4 && inRange(value[0], 0, 1) && inRange(value[1], -4, 4) &&
      inRange(value[2], 0, 1) && inRange(value[3], -4, 4);
  }
  function cubic(t, a, b) {
    const u = 1 - t;
    return 3 * u * u * t * a + 3 * u * t * t * b + t * t * t;
  }
  function solveBezier(p, curve) {
    if (!curve || p <= 0 || p >= 1) return clamp(p, 0, 1);
    const x1 = curve[0], y1 = curve[1], x2 = curve[2], y2 = curve[3];
    if (x1 === y1 && x2 === y2) return p;
    // X is monotonic, including curves with a horizontal tangent. Bisection
    // keeps the interpolation deterministic at those tangents as well.
    let lo = 0, hi = 1;
    for (let i = 0; i < 24; i++) {
      const mid = (lo + hi) / 2;
      if (cubic(mid, x1, x2) < p) lo = mid;
      else hi = mid;
    }
    return cubic((lo + hi) / 2, y1, y2);
  }
  function easing(value, declared) {
    if (bezierTuple(value)) return value;
    if (!timingId(value)) return null;
    const custom = own(declared, value);
    return bezierTuple(custom) ? custom : own(easings, value);
  }
  function buffer(width, height, options) {
    const canvas = new OffscreenCanvas(width, height);
    return canvas.getContext("2d", options);
  }
  function point(value, fallback) {
    return Array.isArray(value) && Number.isFinite(value[0]) && Number.isFinite(value[1]) ? value : fallback;
  }

  function resolve(registry, id, fallback, usable = item => !!item &&
    (!item.fallback || Object.keys(item).some(key => key !== "fallback"))) {
    const visited = new Set();
    while (id && !visited.has(id)) {
      visited.add(id);
      const item = own(registry, id);
      if (usable(item, id)) return { id, item };
      id = item?.fallback || fallback;
    }
    const item = own(registry, fallback);
    return usable(item, fallback) ? { id: fallback, item } : null;
  }

  // wardrobePairs parses a flat "<key>=<value> ..." config string into an
  // object, skipping malformed pairs and keeping at most 16.
  function wardrobePairs(text, keyPattern, valuePattern) {
    const out = {};
    if (typeof text !== "string") return out;
    let count = 0;
    for (const pair of text.trim().split(/\s+/)) {
      const at = pair.indexOf("=");
      const key = pair.slice(0, at), value = pair.slice(at + 1);
      if (at <= 0 || !keyPattern.test(key) || !valuePattern.test(value) || Object.hasOwn(out, key)) continue;
      out[key] = value;
      if (++count >= wardrobePairMax) break;
    }
    return out;
  }
  function attachmentPairs(text) { return wardrobePairs(text, slotId, wardrobeId); }
  function palettePairs(text) { return wardrobePairs(text, channelId, hexColor); }

  function ids(registry) {
    return record(registry) ? Object.keys(registry).filter(id => record(registry[id])).sort() : [];
  }

  // wardrobeOptions lists what a pack offers each wardrobe picker: outfits,
  // hairs, eye sets, attachment IDs per slot, and tint channels (built-in,
  // palette keys and every fills channel the pack draws). It omits every ID,
  // slot and channel the config patterns cannot select, so a pick never
  // produces a write the server refuses.
  function wardrobeOptions(manifest) {
    const m = record(manifest) ? manifest : {};
    const selectable = registry => ids(registry).filter(id => wardrobeId.test(id));
    // slotId never matches __proto__, so a plain object keyed by slot is safe.
    const slots = {};
    for (const id of selectable(m.attachments)) {
      const slot = m.attachments[id].slot;
      if (id === "none" || typeof slot !== "string" || !slotId.test(slot)) continue;
      (Object.hasOwn(slots, slot) ? slots[slot] : (slots[slot] = [])).push(id);
    }
    const channels = new Set(Object.keys(colors));
    const keys = obj => { if (record(obj)) for (const key of Object.keys(obj)) channels.add(key); };
    const layer = item => {
      if (!record(item)) return;
      keys(item.fills);
      if (Array.isArray(item.frames)) item.frames.forEach(frame => record(frame) && keys(frame.fills));
    };
    const layers = list => { if (Array.isArray(list)) list.forEach(layer); };
    keys(m.palette);
    for (const preset of Object.values(record(m.presets) ? m.presets : {})) keys(preset?.palette);
    for (const id of ids(m.outfits)) {
      const outfit = m.outfits[id];
      keys(outfit.palette);
      for (const section of ["poses", "gestures"]) for (const column of Object.values(record(outfit[section]) ? outfit[section] : {})) layers(column?.layers);
    }
    for (const id of ids(m.attachments)) {
      layers(m.attachments[id].layers);
      for (const column of Object.values(record(m.attachments[id].columns) ? m.attachments[id].columns : {})) layers(column?.layers);
    }
    for (const section of ["poses", "gestures"]) for (const entry of Object.values(record(m[section]) ? m[section] : {})) layers(entry?.layers);
    for (const id of ids(m.hairs)) for (const part of ["back", "front", "acc"]) layer(m.hairs[id][part]);
    layer(m.head?.egg);
    return {
      outfits: selectable(m.outfits), hairs: selectable(m.hairs), eyesets: selectable(m.eyesets), slots,
      channels: [...channels].filter(id => channelId.test(id)).sort()
    };
  }

  // The upper bound is the first key strictly after time. At a duplicated
  // time the last equal key wins, while the approach ends at the first one.
  function upperBound(keys, field, value) {
    let lo = 0, hi = keys.length;
    while (lo < hi) {
      const i = (lo + hi) >>> 1;
      if (keys[i][field] <= value) lo = i + 1;
      else hi = i;
    }
    return lo;
  }

  function keyframes(keys, curves, loop, time, out) {
    const duration = keys.at(-1).t;
    const t = duration > 0 && loop !== false ? time % duration : time;
    const lo = upperBound(keys, "t", t);
    const before = keys[Math.max(0, lo - 1)];
    const after = keys[lo];
    const amount = lo > 0 && after ? solveBezier((t - before.t) / (after.t - before.t), curves[lo - 1]) : 0;
    for (const name of channelNames) {
      const from = before[name] ?? channels[name];
      out[name] = amount ? mix(from, after[name] ?? channels[name], amount) : from;
    }
    return out;
  }

  function motion(spec, motions, declared) {
    const name = typeof spec === "string" ? spec : spec?.use;
    const track = name ? own(motions, name) || { oscillate: own(aliases, name) } : spec;
    const output = { ...channels };
    if (track?.keyframes?.length) {
      const curves = track.keyframes.map(key => easing(key.ease, declared));
      return time => keyframes(track.keyframes, curves, track.loop, time, output);
    }
    if (!track?.oscillate) return null;
    const osc = track.oscillate;
    const phase = finite(spec?.phase, finite(osc.phase));
    return time => {
      const cycle = time * (osc.hz ?? osc.freqHz) + phase / TAU;
      const triangular = 1 - Math.abs(2 * (cycle - Math.floor(cycle)) - 1);
      const t = osc.ease === "linear" ? triangular : osc.ease === "easeInOut" ?
        triangular * triangular * (3 - 2 * triangular) : (1 - Math.cos(cycle * TAU)) / 2;
      for (const key of channelNames) {
        const range = osc[key];
        output[key] = range ? mix(range[0], range[1], t) : channels[key];
      }
      return output;
    };
  }

  function compileCurve(def, bounds, declared, isProp) {
    if (!record(def) || !Array.isArray(def.keyframes) || def.keyframes.length < 1 || def.keyframes.length > 64) return null;
    const names = Object.keys(bounds);
    const keys = [], curves = [];
    let previous = 0;
    for (const frame of def.keyframes) {
      if (!record(frame) || !inRange(frame.p, previous, 1)) return null;
      for (const key of names) {
        const [low, high] = bounds[key];
        if (Object.hasOwn(frame, key) && !inRange(frame[key], low, high)) return null;
      }
      previous = frame.p;
      keys.push(frame);
      curves.push(easing(frame.ease, declared));
    }
    const { edge, recoil } = def;
    if (isProp && Object.hasOwn(def, "edge") && !["away", "left", "right"].includes(edge)) return null;
    if (isProp && Object.hasOwn(def, "recoil") && (!record(recoil) || !inRange(recoil.start, 0, 1) ||
        !inRange(recoil.peak, 0, 1) || !inRange(recoil.end, 0, 1) || !(recoil.start < recoil.peak && recoil.peak < recoil.end) ||
        Object.hasOwn(recoil, "lift") && !inRange(recoil.lift, -1, 1) ||
        Object.hasOwn(recoil, "angle") && !inRange(recoil.angle, -1.5, 1.5))) return null;
    return { edge: isProp && edge, recoil: isProp && recoil, sample(p, out) {
      const i = upperBound(keys, "p", p);
      const before = keys[Math.max(0, i - 1)], after = keys[i];
      const amount = i && after ? solveBezier((p - before.p) / (after.p - before.p), curves[i - 1]) : 0;
      for (const key of names) {
        const range = bounds[key], from = before[key] ?? range[2];
        out[key] = amount ? clamp(mix(from, after[key] ?? range[2], amount), range[0], range[1]) : from;
      }
      return out;
    } };
  }

  function compileCurves(starters, custom, declared, isProp = false) {
    const table = Object.create(null);
    const channels = isProp ? propChannels : stageChannels;
    for (const [id, definition] of Object.entries(starters)) table[id] = compileCurve(definition, channels, declared, isProp);
    for (const [id, definition] of Object.entries(record(custom) ? custom : {})) {
      if (!timingId(id)) continue;
      const curve = compileCurve(definition, channels, declared, isProp);
      if (curve) table[id] = curve;
    }
    return table;
  }

  function swingWeight(layer) { return bounded(layer?.swingWeight, -2, 2, null); }
  function swingAngle(weight, lag, physics) {
    return physics ? weight * physics.swing - lag * physics.swingVel : 0;
  }

  // makeRig composes one pack. wardrobe carries the dashboard.companion_*
  // picks ({outfit, hair, eyes, attachments, palette}, empty = follow the
  // preset); each component resolves config key > preset > pack default, and
  // an ID the pack does not define falls through to the next source.
  function makeRig(pack, shared, presetId, wardrobe) {
    const { manifest, bitmaps } = pack;
    const [width, height] = manifest.canvas;
    const declared = record(manifest.easings) ? manifest.easings : null;
    const [sharedWidth, sharedHeight] = shared?.manifest.canvas || manifest.canvas;
    const tinted = new Map();
    const rigViews = new Map();
    const composite = buffer(width, height);
    const boundsBuffer = buffer(128, 128, { willReadFrequently: true });
    const decals = new Map();
    const outfits = record(manifest.outfits) ? manifest.outfits : null;
    let palette, hair, eyes, iris = null, outfit = null, outfitId = null, attachments = [], picks = {};
    let presetKey = presetId, config = record(wardrobe) ? wardrobe : {};

    function defined(registry, id) { return typeof id === "string" && id !== "" && record(own(registry, id)); }

    function select() {
      const preset = own(manifest.presets, presetKey);
      const choose = (registry, ...candidates) => candidates.find(id => defined(registry, id)) ?? null;
      outfitId = outfits ? choose(outfits, config.outfit, preset?.outfit, manifest.defaultOutfit, ids(outfits)[0]) : null;
      outfit = outfitId ? outfits[outfitId] : null;
      palette = { ...colors, ...(record(manifest.palette) ? manifest.palette : {}),
        ...(record(outfit?.palette) ? outfit.palette : {}), ...(record(preset?.palette) ? preset.palette : {}),
        ...palettePairs(config.palette) };
      // The pack-default hair is the pre-wardrobe behavior, unchanged.
      const hairId = typeof config.hair === "string" && config.hair && resolve(manifest.hairs, config.hair) ?
        config.hair : preset?.hair || Object.keys(manifest.hairs || {})[0];
      hair = resolve(manifest.hairs, hairId)?.item;
      if (preset?.acc === false || preset?.accessory === false) hair = hair && { ...hair, acc: null };
      const eyesId = choose(manifest.eyesets, config.eyes, preset?.eyes);
      eyes = eyesId ? manifest.eyesets[eyesId] : manifest.head?.eyes;
      // The iris buffer exists only while the eye block has a sclera to clip to.
      iris = eyes?.sclera ? iris || buffer(width, height) : null;
      // Attachments merge per slot: preset, then config. "none" clears a slot;
      // an ID the pack lacks, or one made for another slot, is ignored.
      const slots = {};
      for (const source of [record(preset?.attachments) ? preset.attachments : {}, attachmentPairs(config.attachments)]) {
        for (const [slot, id] of Object.entries(source)) {
          if (typeof slot !== "string" || !slotId.test(slot) || typeof id !== "string") continue;
          if (id === "none") slots[slot] = null;
          else if (defined(manifest.attachments, id) && manifest.attachments[id].slot === slot) slots[slot] = id;
        }
      }
      attachments = Object.keys(slots).sort().filter(slot => slots[slot]).map(slot => ({ slot, id: slots[slot], data: manifest.attachments[slots[slot]] }));
      picks = { outfit: outfitId, hair: hairId || null, eyes: eyesId,
        attachments: Object.fromEntries(attachments.map(item => [item.slot, item.id])), palette: { ...palette } };
      tinted.clear();
      rigViews.clear();
      decals.clear();
    }

    function setPreset(id) {
      presetKey = id;
      select();
    }
    function setWardrobe(next) {
      config = record(next) ? next : {};
      select();
    }
    select();

    function texture(file, color, sharedAsset) {
      const source = (sharedAsset ? shared?.bitmaps : bitmaps)?.get(file);
      if (!source || typeof color !== "string" || !/^#[0-9a-f]{3,8}$/i.test(color)) return source;
      const key = (sharedAsset ? "shared:" : "") + file + ":" + color;
      if (!tinted.has(key)) {
        const ctx = buffer(sharedAsset ? sharedWidth : width, sharedAsset ? sharedHeight : height);
        ctx.drawImage(source, 0, 0, ctx.canvas.width, ctx.canvas.height);
        ctx.globalCompositeOperation = "multiply";
        ctx.fillStyle = color;
        ctx.fillRect(0, 0, ctx.canvas.width, ctx.canvas.height);
        ctx.globalCompositeOperation = "destination-in";
        ctx.drawImage(source, 0, 0, ctx.canvas.width, ctx.canvas.height);
        tinted.set(key, ctx.canvas);
      }
      return tinted.get(key);
    }

    // channelFills draws a layer's generalized tint channels in manifest
    // order, each tinted by its palette channel; a channel with no color (or
    // an ID outside the channel pattern) draws untinted.
    function channelFills(layer, sharedAsset) {
      if (!record(layer.fills)) return [];
      return Object.entries(layer.fills).map(([channel, file]) =>
        texture(file, channelId.test(channel) ? own(palette, channel) : null, sharedAsset));
    }

    function rasterList(layer, sharedAsset) {
      if (typeof layer === "string") return [texture(layer, null, sharedAsset)].filter(Boolean);
      if (layer.mode === "static") return [texture(layer.file, null, sharedAsset)].filter(Boolean);
      return [
        texture(layer.file, null, sharedAsset), texture(layer.detail, null, sharedAsset),
        ...fills.map(([field, color]) => texture(layer[field], palette[color], sharedAsset)),
        ...channelFills(layer, sharedAsset),
        texture(layer.line, null, sharedAsset)
      ].filter(Boolean);
    }

    function draw(ctx, image, x = 0, y = 0) {
      if (image) ctx.drawImage(image, x, y, width, height);
    }

    function layerPaint(layer, z, sharedAsset = false) {
      const pivot = point(layer.pivot, [width / 2, height / 2]);
      const transform = motion(layer.motion, manifest.motions, declared);
      const weight = swingWeight(layer), lag = bounded(layer.swingLag, -0.5, 0.5);
      const frames = layer.frames?.length ? layer.frames.map(frame => rasterList(frame, sharedAsset)) : null;
      const rasters = frames ? null : rasterList(layer, sharedAsset);
      return {
        z: finite(layer.z, z),
        drawable: frames ? frames.some(list => list.length > 0) : rasters.length > 0,
        paint(ctx, t, gaze, physics) {
          const list = frames ? frames[Math.floor(t * layer.fps) % frames.length] : rasters;
          ctx.save();
          const angle = swingAngle(weight, lag, physics);
          if (transform || angle) {
            const movement = transform?.(t) || channels;
            ctx.translate(pivot[0] + movement.tx, pivot[1] + movement.ty);
            ctx.rotate(movement.rotDeg * RAD + angle);
            ctx.scale(movement.sx, movement.sy);
            ctx.translate(-pivot[0], -pivot[1]);
          }
          for (const image of list) draw(ctx, image);
          ctx.restore();
        }
      };
    }

    function expressionPaint(expression, egg) {
      const head = manifest.head;
      const decal = texture(expression?.decal);
      const ex = expression || {};
      if (decal) {
        let image = decals.get(decal);
        if (!image) {
          const face = buffer(width, height);
          draw(face, decal);
          if (egg.length) {
            const mask = buffer(width, height);
            for (const layer of egg) draw(mask, layer);
            face.globalCompositeOperation = "destination-in";
            draw(face, mask.canvas);
          }
          image = face.canvas;
          decals.set(decal, image);
        }
        return ctx => draw(ctx, image);
      }
      const mouth = texture(own(head?.mouths, ex.mouth) || own(head?.mouths, "idle"));
      const eye = {};
      if (eyes) {
        for (const key of ["sclera", "irisFill", "irisLine", "frameLower", "lidCoverL", "lidCoverR", "lidLashL", "lidLashR", "browL", "browR"]) {
          const color = key === "irisFill" ? palette.eye : key.startsWith("lidCover") ? palette.skin : null;
          eye[key] = texture(eyes[key], color);
        }
      }
      const maxPan = point(eyes?.maxPan, [finite(eyes?.maxPanX, 7), finite(eyes?.maxPanY, 4)]);
      const overlay = propLayers(ex.overlay || ex.overlayFx, "fx");
      return (ctx, t, gaze) => {
        draw(ctx, eye.sclera);
        if (iris && eye.sclera) {
          const x = clamp(gaze.x, -1, 1) * maxPan[0], y = clamp(gaze.y, -1, 1) * maxPan[1];
          iris.clearRect(0, 0, width, height);
          iris.globalCompositeOperation = "source-over";
          draw(iris, eye.irisFill, x, y);
          draw(iris, eye.irisLine, x, y);
          iris.globalCompositeOperation = "destination-in";
          draw(iris, eye.sclera);
          draw(ctx, iris.canvas);
        }
        draw(ctx, eye.frameLower);
        const cycle = (t * finite(ex.blinkHz, 0.23)) % 1;
        const blink = Math.max(0, 1 - Math.abs(cycle - 0.82) * 28);
        const lid = finite(ex.lid, finite(ex.lidClose));
        for (const side of ["L", "R"]) {
          const close = clamp(Math.max(blink, finite(ex["lid" + side], lid)), 0, 1);
          draw(ctx, eye["lidCover" + side], 0, close * height * 0.035);
          draw(ctx, eye["lidLash" + side], 0, close * height * 0.035);
          if (eye["brow" + side]) {
            const bx = width * (side === "L" ? 0.44 : 0.56);
            const by = height * 0.25;
            ctx.save();
            ctx.translate(bx, by);
            ctx.rotate(finite(ex.brow, finite(ex.browTilt)) * (side === "L" ? 1 : -1));
            ctx.translate(-bx, -by);
            draw(ctx, eye["brow" + side]);
            ctx.restore();
          }
        }
        draw(ctx, mouth);
        for (const layer of overlay) layer.paint(ctx, t);
      };
    }

    function effect(id, registry = "props") {
      const local = resolve(manifest[registry], id)?.item;
      const foreign = local ? null : resolve(shared?.manifest[registry], id)?.item;
      return { data: local || foreign, shared: !!foreign };
    }

    function propLayers(id, registry) {
      const { data, shared: sharedAsset } = effect(id, registry);
      if (!data) return [];
      const z = finite(data.z, 60);
      const list = (data.layers || []).map(layer => layerPaint(layer, z, sharedAsset));
      if (data.file || data.frames || fills.some(([field]) => data[field]) || data.detail || data.line) {
        list.push(layerPaint(data, z, sharedAsset));
      }
      return list.sort((a, b) => a.z - b.z);
    }

    // column returns the active outfit's binding for a column: its entry when
    // it carries layers, merged over the same-named top-level entry (the
    // column registry). The top-level entry supplies pose and fallback and
    // any field the outfit entry omits, except layers, which would borrow the
    // default outfit's legacy projection.
    function column(section, id) {
      const entry = record(outfit?.[section]) ? own(outfit[section], id) : undefined;
      if (!record(entry) || !Array.isArray(entry.layers) || !entry.layers.length) return null;
      const base = own(manifest[section], id);
      const registry = record(base) ? base : {};
      return { ...registry, ...entry, pose: registry.pose, fallback: registry.fallback, layers: entry.layers };
    }

    // bound walks a column's fallback chain (with the resolver's implicit
    // step) inside the active outfit and returns the first bound column.
    function bound(section, id, implicit) {
      const visited = new Set();
      while (id && !visited.has(id)) {
        visited.add(id);
        const item = column(section, id);
        if (item) return { id, item };
        id = own(manifest[section], id)?.fallback || implicit;
      }
      const item = implicit ? column(section, implicit) : null;
      return item ? { id: implicit, item } : null;
    }

    function view(pose, gesture, expression, props) {
      const key = JSON.stringify([pose, gesture, expression, props]);
      if (rigViews.has(key)) return rigViews.get(key);
      function hand(value) {
        return Array.isArray(value) && value.length === 2 && inRange(value[0], 0, width) && inRange(value[1], 0, height) ? value : null;
      }
      const usableGesture = item => !!(item?.pose || item?.replaces?.length || item?.layers?.length || hand(item?.handAnchor));
      let gestureHit = outfit ? bound("gestures", gesture, null) : null;
      if (!gestureHit) {
        gestureHit = resolve(manifest.gestures, gesture, null, usableGesture);
        // Inside an outfit, an unbound gesture keeps its pose and anchors but
        // never draws the legacy art, and drops replaces unless it is a pure
        // removal, so no limb is removed without its replacement.
        if (gestureHit && outfit) {
          const legacy = gestureHit.item;
          gestureHit = { id: gestureHit.id, item: { ...legacy, layers: [], replaces: legacy.layers?.length ? [] : legacy.replaces } };
        }
      }
      const act = gestureHit?.item;
      const requested = act?.pose || pose;
      // A column the outfit cannot resolve draws the legacy projection stack,
      // so the body is never empty.
      const resolved = (outfit && bound("poses", requested, "stand")) ||
        resolve(manifest.poses, requested, "stand", x => !!x?.layers?.length);
      const body = resolved.item;
      const layers = [];
      const head = manifest.head;
      const pivot = point(head?.pivot, [width / 2, height * 0.28]);
      // A gesture column may carry its own head anchor inside an outfit.
      const anchor = point(outfit ? act?.headAnchor : null, null) || point(body.headAnchor, pivot);
      const scale = clamp(finite(body.headScale, 1), 0.1, 4);
      const headLayer = body.layers.find(layer => layer.mode === "head-egg");
      const egg = head ? rasterList(head.egg || headLayer || {}) : [];
      const face = expressionPaint(resolve(manifest.expressions, expression, "idle")?.item, egg);
      const headMotion = motion(headLayer?.motion, manifest.motions, declared);
      const headWeight = swingWeight(head) ?? swingWeight(headLayer);
      const headLag = bounded(head?.swingLag, -0.5, 0.5, bounded(headLayer?.swingLag, -0.5, 0.5));
      const gazeTilt = bounded(head?.gazeTiltDeg, 0, 30, 1.5) * RAD;
      const propTilt = bounded(head?.propGazeTiltDeg, 0, 30, 6) * RAD;

      function onHead(z, paint) {
        return { z, paint(ctx, t, gaze, physics) {
          const movement = headMotion?.(t);
          ctx.save();
          ctx.translate(anchor[0] + (movement?.tx || 0), anchor[1] + (movement?.ty || 0));
          ctx.rotate((movement?.rotDeg || 0) * RAD + gaze.x * (gaze.prop ? propTilt : gazeTilt) + swingAngle(headWeight, headLag, physics));
          ctx.scale(scale * (movement?.sx ?? 1), scale * (movement?.sy ?? 1));
          ctx.translate(-pivot[0], -pivot[1]);
          paint(ctx, t, gaze, physics);
          ctx.restore();
        } };
      }
      function addHead(layer, z) {
        if (!layer) return;
        const part = layerPaint(layer, z);
        layers.push(onHead(part.z, part.paint));
      }

      const replacements = new Set(Array.isArray(act?.replaces) ? act.replaces : []);
      // An outfit gesture also replaces by slot; legacy packs keep id-only
      // matching so their stacks stay identical.
      const replaced = layer => replacements.has(layer.id) || !!outfit && layer.slot !== undefined && replacements.has(layer.slot);
      const bodyLayers = [...body.layers.filter(layer => !replaced(layer)), ...(act?.layers || [])];
      for (const layer of bodyLayers) {
        if (layer.mode !== "head-egg") layers.push(layerPaint(layer, 20));
      }
      if (resolved.id === "typing" && !body.layers.some(layer =>
        layer.id === "prop-typing-desk" || layer.file?.startsWith("prop-typing-desk"))) {
        layers.push(...propLayers("prop-typing-desk"));
      }
      // Body attachments follow the drawn column (gesture, then pose), with
      // their own layers as the default.
      for (const { slot, data } of attachments) {
        const z = finite(data.z, own(slotZ, slot) ?? (data.bind === "body" ? bindZ.body : bindZ.head));
        if (data.bind === "body") {
          const columns = record(data.columns) ? data.columns : {};
          const entry = [gestureHit?.id, resolved.id].map(id => own(columns, id)).find(item => record(item) && Array.isArray(item.layers));
          for (const layer of entry ? entry.layers : Array.isArray(data.layers) ? data.layers : []) {
            if (record(layer)) layers.push(layerPaint(layer, z));
          }
        } else if (head && Array.isArray(data.layers)) {
          for (const layer of data.layers) if (record(layer)) addHead(layer, z);
        }
      }
      if (head) {
        layers.push(onHead(finite(headLayer?.z, 30), ctx => { for (const image of egg) draw(ctx, image); }));
        layers.push(onHead(40, face));
        addHead(hair?.back, 10);
        addHead(hair?.front, 50);
        addHead(hair?.acc, 55);
      }
      for (const id of props) {
        const { data } = effect(id);
        for (const layer of propLayers(id)) {
          layers.push(data?.anchor === "behind-head" && head ? onHead(layer.z, layer.paint) : layer);
        }
      }
      layers.sort((a, b) => a.z - b.z);
      const result = {
        pose: resolved.id,
        handAnchor: hand(act?.handAnchor) || hand(body.handAnchor) || [width * 0.5, height * 0.48],
        articulatedDrag: bodyLayers.some(layer => swingWeight(layer) !== null) || headWeight !== null || swingWeight(hair?.back) !== null,
        seat: body.seatY === undefined ? null : clamp(finite(body.seatY) / height, 0, 1),
        render(t, gaze, physics) {
          composite.clearRect(0, 0, width, height);
          for (const layer of layers) layer.paint(composite, t, gaze, physics);
          return composite.canvas;
        }
      };

      // Measure the rig once to align the visible pixels to the divider and
      // keep the hitbox clear of transparent padding in minimal packs.
      const neutral = { x: 0, y: 0 };
      boundsBuffer.clearRect(0, 0, 128, 128);
      boundsBuffer.drawImage(result.render(0, neutral), 0, 0, 128, 128);
      const rgba = boundsBuffer.getImageData(0, 0, 128, 128).data;
      let minX = 128, minY = 128, maxX = 0, maxY = 0;
      for (let i = 3; i < rgba.length; i += 4) {
        if (rgba[i] < 24) continue;
        const pixel = i >> 2, x = pixel % 128, y = pixel >> 7;
        minX = Math.min(minX, x); minY = Math.min(minY, y);
        maxX = Math.max(maxX, x + 1); maxY = Math.max(maxY, y + 1);
      }
      result.bounds = maxX ? [minX / 128, minY / 128, maxX / 128, maxY / 128] : [0, 0, 1, 1];
      if (rigViews.size >= 48) rigViews.clear();
      rigViews.set(key, result);
      return result;
    }

    function prop(id, registry) {
      const { data } = effect(id, registry);
      const layers = propLayers(id, registry);
      return {
        data,
        drawable: layers.some(layer => layer.drawable),
        draw(ctx, t) { for (const layer of layers) layer.paint(ctx, t); }
      };
    }

    return {
      view, prop, setPreset, setWardrobe, width, height,
      // wardrobe reports the effective picks: the outfit, hair and eye-set
      // IDs, the attachment ID per slot, and the merged palette.
      wardrobe: () => picks
    };
  }

  Object.assign(C, { makeRig, motion, finite, clamp, mix, inRange, bounded, record, timingId, solveBezier, compileCurves, RAD, resolveEntry: resolve,
    wardrobeOptions, attachmentPairs, palettePairs });
})(window.WsCompanion);
