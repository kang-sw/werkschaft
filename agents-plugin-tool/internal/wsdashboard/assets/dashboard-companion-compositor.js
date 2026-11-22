(function (C) {
  "use strict";
  const { own } = C;
  const TAU = 2 * Math.PI;
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

  function finite(x, fallback = 0) { return Number.isFinite(x) ? x : fallback; }
  function clamp(x, low, high) { return Math.min(high, Math.max(low, x)); }
  function mix(a, b, t) { return a + (b - a) * t; }
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
      if (usable(item)) return { id, item };
      id = item?.fallback || fallback;
    }
    const item = own(registry, fallback);
    return usable(item) ? { id: fallback, item } : null;
  }

  // The upper bound is the first key strictly after time. At a duplicated
  // time the last equal key wins, while the approach ends at the first one.
  function keyframes(keys, loop, time, out) {
    const duration = keys.at(-1).t;
    const t = duration > 0 && loop !== false ? time % duration : time;
    let lo = 0, hi = keys.length;
    while (lo < hi) {
      const i = (lo + hi) >>> 1;
      if (keys[i].t <= t) lo = i + 1;
      else hi = i;
    }
    const before = keys[Math.max(0, lo - 1)];
    const after = keys[lo];
    const amount = lo > 0 && after ? (t - before.t) / (after.t - before.t) : 0;
    for (const name of channelNames) {
      const from = before[name] ?? channels[name];
      out[name] = amount ? mix(from, after[name] ?? channels[name], amount) : from;
    }
    return out;
  }

  function motion(spec, motions) {
    const name = typeof spec === "string" ? spec : spec?.use;
    const track = name ? own(motions, name) || { oscillate: own(aliases, name) } : spec;
    const output = { ...channels };
    if (track?.keyframes?.length) {
      return time => keyframes(track.keyframes, track.loop, time, output);
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

  function makeRig(pack, shared, presetId) {
    const { manifest, bitmaps } = pack;
    const [width, height] = manifest.canvas;
    const [sharedWidth, sharedHeight] = shared?.manifest.canvas || manifest.canvas;
    const tinted = new Map();
    const rigViews = new Map();
    const iris = manifest.head?.eyes?.sclera ? buffer(width, height) : null;
    const composite = buffer(width, height);
    const boundsBuffer = buffer(128, 128, { willReadFrequently: true });
    const decals = new Map();
    let palette, hair;

    function setPreset(id) {
      const preset = own(manifest.presets, id);
      palette = { ...colors, ...manifest.palette, ...preset?.palette };
      hair = resolve(manifest.hairs, preset?.hair || Object.keys(manifest.hairs || {})[0])?.item;
      if (preset?.acc === false || preset?.accessory === false) hair = hair && { ...hair, acc: null };
      tinted.clear();
      rigViews.clear();
      decals.clear();
    }
    setPreset(presetId);

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

    function rasterList(layer, sharedAsset) {
      if (typeof layer === "string") return [texture(layer, null, sharedAsset)];
      if (layer.mode === "static") return [texture(layer.file, null, sharedAsset)];
      return [
        texture(layer.file, null, sharedAsset), texture(layer.detail, null, sharedAsset),
        ...fills.map(([field, color]) => texture(layer[field], palette[color], sharedAsset)),
        texture(layer.line, null, sharedAsset)
      ].filter(Boolean);
    }

    function draw(ctx, image, x = 0, y = 0) {
      if (image) ctx.drawImage(image, x, y, width, height);
    }

    function layerPaint(layer, z, sharedAsset = false) {
      const pivot = point(layer.pivot, [width / 2, height / 2]);
      const transform = motion(layer.motion, manifest.motions);
      const frames = layer.frames?.length ? layer.frames.map(frame => rasterList(frame, sharedAsset)) : null;
      const rasters = frames ? null : rasterList(layer, sharedAsset);
      return {
        z: finite(layer.z, z),
        paint(ctx, t) {
          const list = frames ? frames[Math.floor(t * layer.fps) % frames.length] : rasters;
          ctx.save();
          if (transform) {
            const { rotDeg, tx, ty, sx, sy } = transform(t);
            ctx.translate(pivot[0] + tx, pivot[1] + ty);
            ctx.rotate(rotDeg * Math.PI / 180);
            ctx.scale(sx, sy);
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
      const eyes = head?.eyes;
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

    function view(pose, gesture, expression, props) {
      const key = JSON.stringify([pose, gesture, expression, props]);
      if (rigViews.has(key)) return rigViews.get(key);
      const act = resolve(manifest.gestures, gesture, null, x => !!x?.layers?.length)?.item;
      const resolved = resolve(manifest.poses, act?.pose || pose, "stand", x => !!x?.layers?.length);
      const body = resolved.item;
      const layers = [];
      const head = manifest.head;
      const pivot = point(head?.pivot, [width / 2, height * 0.28]);
      const anchor = point(body.headAnchor, pivot);
      const scale = clamp(finite(body.headScale, 1), 0.1, 4);
      const headLayer = body.layers.find(layer => layer.mode === "head-egg");
      const egg = head ? rasterList(head.egg || headLayer || {}) : [];
      const face = expressionPaint(resolve(manifest.expressions, expression, "idle")?.item, egg);
      const headMotion = motion(headLayer?.motion, manifest.motions);

      function onHead(z, paint) {
        return { z, paint(ctx, t, gaze) {
          const movement = headMotion?.(t);
          ctx.save();
          ctx.translate(anchor[0] + (movement?.tx || 0), anchor[1] + (movement?.ty || 0));
          ctx.rotate((movement?.rotDeg || 0) * Math.PI / 180 + gaze.x * 0.018);
          ctx.scale(scale * (movement?.sx ?? 1), scale * (movement?.sy ?? 1));
          ctx.translate(-pivot[0], -pivot[1]);
          paint(ctx, t, gaze);
          ctx.restore();
        } };
      }
      function addHead(layer, z) {
        if (!layer) return;
        const part = layerPaint(layer, z);
        layers.push(onHead(part.z, part.paint));
      }

      const replacements = new Set(act?.replaces || []);
      for (const layer of [...body.layers.filter(layer => !replacements.has(layer.id)), ...(act?.layers || [])]) {
        if (layer.mode !== "head-egg") layers.push(layerPaint(layer, 20));
      }
      if (resolved.id === "typing" && !body.layers.some(layer =>
        layer.id === "prop-typing-desk" || layer.file?.startsWith("prop-typing-desk"))) {
        layers.push(...propLayers("prop-typing-desk"));
      }
      if (head) {
        layers.push(onHead(finite(headLayer?.z, 30), ctx => egg.forEach(image => draw(ctx, image))));
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
        seat: body.seatY === undefined ? null : clamp(finite(body.seatY) / height, 0, 1),
        render(t, gaze) {
          composite.clearRect(0, 0, width, height);
          for (const layer of layers) layer.paint(composite, t, gaze);
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
        draw(ctx, t) { for (const layer of layers) layer.paint(ctx, t); }
      };
    }

    return { view, prop, setPreset, width, height };
  }

  Object.assign(C, { makeRig, motion, finite, clamp, mix });
})(window.WsCompanion);
