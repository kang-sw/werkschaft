(function () {
  "use strict";
  const base = "/api/companion/packs";
  const layerFields = ["file", "line", "jacketFill", "accentFill", "skinFill", "detail", "hairFill"];
  const eyeFields = ["sclera", "irisFill", "irisLine", "frameLower", "lidCoverL", "lidCoverR",
    "lidLashL", "lidLashR", "browL", "browR", "closedL", "closedR"];

  function own(obj, key) { return obj && Object.hasOwn(obj, key) ? obj[key] : undefined; }
  function values(obj) { return Object.values(obj || {}); }
  function raster(file) { return typeof file === "string" && /^[a-z\d][a-z\d._-]{0,127}\.(webp|png)$/i.test(file) && !file.includes(".."); }
  function vrmModel(file) { return typeof file === "string" && /^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}\.vrm$/.test(file) && !file.includes(".."); }
  function vrmaFile(file) { return typeof file === "string" && /^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}\.vrma$/.test(file) && !file.includes(".."); }
  function clipRelFile(file) { return typeof file === "string" && /^clips\/[a-z][a-z0-9_]{0,31}\.(clip\.json|vrma)$/.test(file); }
  function clipKeyId(key) { return typeof key === "string" && /^[a-z][a-z0-9_]{0,31}$/.test(key); }

  // Match the server's known raster fields. Extension keys may contain raster
  // names as ordinary text and never imply extra asset requests.
  function filesOf(pack) {
    const files = new Set();
    function add(file) { if (raster(file)) files.add(file); }
    // A layer's raster keys plus its generalized tint channels (fills).
    function fields(obj, keys) {
      if (!obj) return;
      keys.forEach(key => add(own(obj, key)));
      const fills = own(obj, "fills");
      if (keys === layerFields && fills && typeof fills === "object" && !Array.isArray(fills)) values(fills).forEach(add);
    }
    function layer(obj) {
      fields(obj, layerFields);
      if (Array.isArray(obj?.frames)) obj.frames.forEach(frame => {
        if (typeof frame === "string") add(frame);
        else fields(frame, layerFields);
      });
    }
    function layers(obj) { if (Array.isArray(obj?.layers)) obj.layers.forEach(layer); }

    fields(pack.head?.egg, layerFields);
    fields(pack.head?.eyes, eyeFields);
    values(pack.head?.mouths).forEach(add);
    values(pack.hairs).forEach(hair => ["front", "back", "acc"].forEach(key => fields(hair[key], layerFields)));
    values(pack.expressions).forEach(expression => add(expression.decal));
    [...values(pack.poses), ...values(pack.gestures)].forEach(layers);
    [...values(pack.props), ...values(pack.fx)].forEach(effect => { layer(effect); layers(effect); });
    // Paper-doll sections; the server enumerates the same fields.
    values(pack.outfits).forEach(outfit => [...values(outfit?.poses), ...values(outfit?.gestures)].forEach(layers));
    values(pack.attachments).forEach(attachment => { layers(attachment); values(attachment?.columns).forEach(layers); });
    values(pack.eyesets).forEach(eyes => fields(eyes, eyeFields));
    return files;
  }

  async function request(url, signal, method) {
    const response = await fetch(url, { signal, method, credentials: "same-origin" });
    if (!response.ok) {
      const error = new Error("Companion request failed");
      error.status = response.status;
      throw error;
    }
    return response;
  }

  function release(bitmaps) {
    for (const bitmap of bitmaps.values()) bitmap.close();
    bitmaps.clear();
  }

  async function decodeVrm(info, signal) {
    const path = base + "/" + encodeURIComponent(info.id);
    const rev = "?rev=" + encodeURIComponent(info.rev);
    const manifest = await (await request(path + "/pack.json" + rev, signal)).json();
    if (manifest.schema !== "ws-companion-vrm/v1" || manifest.id !== info.id || !vrmModel(manifest.model)) {
      throw new Error("Invalid VRM companion manifest");
    }
    const animations = Object.create(null);
    if (manifest.animations && typeof manifest.animations === "object" && !Array.isArray(manifest.animations)) {
      for (const [key, file] of Object.entries(manifest.animations)) {
        if (vrmaFile(file)) animations[key] = file;
      }
    }
    const expressionMap = manifest.expressionMap && typeof manifest.expressionMap === "object" && !Array.isArray(manifest.expressionMap)
      ? manifest.expressionMap : Object.create(null);
    const normalizeEntry = (raw, keyFallback) => {
      if (!raw || typeof raw !== "object") return null;
      const key = clipKeyId(raw.key) ? raw.key : (clipKeyId(keyFallback) ? keyFallback : "");
      if (!key) return null;
      const kind = raw.kind === "vrma" ? "vrma" : "clip";
      const file = clipRelFile(raw.file) ? raw.file : ("clips/" + key + (kind === "vrma" ? ".vrma" : ".clip.json"));
      const entry = {
        key,
        kind,
        file,
        description: typeof raw.description === "string" ? raw.description : "",
        expression: typeof raw.expression === "string" ? raw.expression : "",
        prop: typeof raw.prop === "string" ? raw.prop : "",
        expose: raw.expose_to_agent === true,
        loop: typeof raw.loop === "boolean" ? raw.loop : undefined,
        duration_s: Number.isFinite(raw.duration_s) ? raw.duration_s : undefined
      };
      return entry;
    };
    const sentinels = Object.create(null);
    if (manifest.sentinels && typeof manifest.sentinels === "object" && !Array.isArray(manifest.sentinels)) {
      for (const [key, raw] of Object.entries(manifest.sentinels)) {
        const entry = normalizeEntry(raw, key);
        if (entry) sentinels[key] = entry;
      }
    }
    const actions = [];
    const actionMap = Object.create(null);
    if (Array.isArray(manifest.actions)) {
      for (const raw of manifest.actions) {
        const entry = normalizeEntry(raw, "");
        if (entry && !Object.hasOwn(actionMap, entry.key)) {
          actions.push(entry);
          actionMap[entry.key] = entry;
        }
      }
    }
    const calibration = manifest.calibration && typeof manifest.calibration === "object" && !Array.isArray(manifest.calibration)
      ? manifest.calibration : null;
    const clipWarnings = Array.isArray(info.clipWarnings) ? info.clipWarnings.slice() : [];
    return {
      id: info.id,
      rev: info.rev,
      renderer: "vrm",
      manifest,
      modelFile: manifest.model,
      modelUrl: path + "/assets/" + encodeURIComponent(manifest.model) + rev,
      expressionMap,
      animations,
      sentinels,
      actions,
      actionMap,
      calibration,
      clipWarnings,
      bitmaps: new Map()
    };
  }

  // fetchPackClip fetches a clip file (.clip.json or .vrma) from /clips/<basename>
  // keyed on pack.rev, with one 409 stale-revision retry matching fetchPackAsset.
  async function fetchPackClip(pack, file, signal) {
    const basename = String(file || "").replace(/^clips\//, "");
    const clipPath = base + "/" + encodeURIComponent(pack.id) + "/clips/" + encodeURIComponent(basename);
    let rev = pack.rev || "";
    const urlFor = r => clipPath + "?rev=" + encodeURIComponent(r);
    let response = await fetch(urlFor(rev), { signal, credentials: "same-origin" });
    if (response.status === 409) {
      let nextRev = response.headers?.get?.("X-Companion-Current-Rev") || "";
      try {
        const catalogResp = await fetch(base, { signal, credentials: "same-origin" });
        if (catalogResp.ok) {
          const catalog = await catalogResp.json();
          const info = Array.isArray(catalog?.packs) ? catalog.packs.find(p => p.id === pack.id) : null;
          if (info?.rev) nextRev = info.rev;
        }
      } catch (err) {
        if (signal?.aborted) throw err;
      }
      if (nextRev && nextRev !== rev) {
        rev = nextRev;
        pack.rev = nextRev;
        response = await fetch(urlFor(rev), { signal, credentials: "same-origin" });
      }
    }
    if (!response.ok) {
      const error = new Error("Companion clip request failed");
      error.status = response.status;
      throw error;
    }
    if (basename.endsWith(".clip.json")) {
      return { json: await response.json(), rev };
    }
    return { buffer: await response.arrayBuffer(), rev };
  }

  // fetchPackAsset fetches a binary VRM/VRMA asset keyed on pack.rev. On a
  // 409 (stale revision), it re-reads the catalog once to pick up the fresh
  // revision (or X-Companion-Current-Rev) and retries a single time.
  async function fetchPackAsset(pack, file, signal) {
    const assetPath = base + "/" + encodeURIComponent(pack.id) + "/assets/" + encodeURIComponent(file);
    let rev = pack.rev || "";
    const urlFor = r => assetPath + "?rev=" + encodeURIComponent(r);
    let response = await fetch(urlFor(rev), { signal, credentials: "same-origin" });
    if (response.status === 409) {
      let nextRev = response.headers?.get?.("X-Companion-Current-Rev") || "";
      try {
        const catalogResp = await fetch(base, { signal, credentials: "same-origin" });
        if (catalogResp.ok) {
          const catalog = await catalogResp.json();
          const info = Array.isArray(catalog?.packs) ? catalog.packs.find(p => p.id === pack.id) : null;
          if (info?.rev) nextRev = info.rev;
        }
      } catch (err) {
        if (signal?.aborted) throw err;
      }
      if (nextRev && nextRev !== rev) {
        rev = nextRev;
        pack.rev = nextRev;
        if (pack.modelFile === file) pack.modelUrl = urlFor(nextRev);
        response = await fetch(urlFor(rev), { signal, credentials: "same-origin" });
      }
    }
    if (!response.ok) {
      const error = new Error("Companion asset request failed");
      error.status = response.status;
      throw error;
    }
    return { buffer: await response.arrayBuffer(), rev };
  }

  async function decode(info, signal) {
    if (info.renderer === "vrm") return decodeVrm(info, signal);
    const path = base + "/" + encodeURIComponent(info.id);
    const rev = "?rev=" + encodeURIComponent(info.rev);
    const manifest = await (await request(path + "/pack.json" + rev, signal)).json();
    if (manifest.schema !== "ws-companion/v1" || manifest.id !== info.id || !manifest.poses?.stand ||
        !Array.isArray(manifest.canvas) || manifest.canvas.length !== 2 ||
        !manifest.canvas.every(n => Number.isInteger(n) && n >= 64 && n <= 2048)) {
      throw new Error("Invalid companion manifest");
    }
    const bitmaps = new Map();
    const pending = filesOf(manifest).values();
    let failure, pixels = 0;
    // Limit simultaneous decoded layers and wait for every in-flight worker
    // before releasing partial bitmaps on abort or a stale revision.
    async function worker() {
      try {
        for (const file of pending) {
          if (failure) return;
          const response = await request(path + "/assets/" + encodeURIComponent(file) + rev, signal);
          const bitmap = await createImageBitmap(await response.blob());
          bitmaps.set(file, bitmap);
          pixels += bitmap.width * bitmap.height;
          if (bitmap.width > 4096 || bitmap.height > 4096 || pixels > 40 * 1024 * 1024) {
            throw new Error("Companion pack exceeds the decoded raster budget");
          }
          signal.throwIfAborted();
        }
      } catch (error) {
        if (!failure || retryable(error)) failure = error;
      }
    }
    await Promise.all([worker(), worker(), worker(), worker()]);
    if (!failure && !signal.aborted) return { id: info.id, rev: info.rev, renderer: info.renderer || "2d", manifest, bitmaps };
    release(bitmaps);
    signal.throwIfAborted();
    throw failure;
  }

  function personaOf(persona) {
    if (!persona || typeof persona !== "object") return null;
    const text = value => typeof value === "string" ? value : "";
    const result = { name: text(persona.name), voice: text(persona.voice), quipLanguage: text(persona.quipLanguage) };
    return result.name || result.voice ? result : null;
  }

  function retryable(error) { return error.status === 409 || error.status === 404; }
  function query(selector, hints) {
    const params = new URLSearchParams();
    if (selector === "auto" && hints.routeKind === "session") {
      if (hints.harness) params.set("harness", hints.harness);
      if (hints.model) params.set("model", hints.model);
    }
    return params.size ? "?" + params : "";
  }

  function packLoader(activate, failed) {
    let loadGen = 0, controller, wanted, current = null, shared = null;

    function stop() {
      ++loadGen;
      controller?.abort();
      controller = null;
      wanted = null;
    }

    function load(selector, hints, force = false) {
      const search = query(selector, hints);
      const key = selector + search;
      if (!force && key === wanted) return;
      stop();
      wanted = key;
      const gen = loadGen;
      controller = new AbortController();
      const signal = controller.signal;
      run().catch(() => {
        if (gen !== loadGen) return;
        wanted = null;
        failed();
      });

      async function run() {
        let fallback = selector === "default", attempts = 0;
        for (;;) {
          const requestSearch = fallback ? "" : search;
          let candidate, defaults, activated = false;
          try {
            const catalog = await (await request(base + requestSearch, signal)).json();
            const id = fallback ? "default" : selector === "auto" ? catalog.resolvedAuto?.packId : selector;
            const info = catalog.packs.find(pack => pack.id === id);
            const defaultInfo = catalog.packs.find(pack => pack.id === "default");
            if (!info?.rev) throw Object.assign(new Error("Companion pack missing"), { status: 404 });
            candidate = matching(id, info.rev) || await decode(info, signal);
            // The catalog carries the validated persona; the raw manifest is not trusted for it.
            candidate.persona = personaOf(info.persona);
            const isVrm = (candidate.renderer || info.renderer) === "vrm";
            const needsDefaults = !isVrm && id !== "default" && defaultInfo?.rev &&
              ["drone", "mail", "prop-typing-desk"].some(prop => !own(candidate.manifest.props, prop));
            if (needsDefaults) defaults = matching("default", defaultInfo.rev) || await decode(defaultInfo, signal);
            if (gen !== loadGen) return;
            if (candidate === current && (defaults || null) === (shared || null)) return;
            activate(candidate, defaults);
            activated = true;
            if (current && current !== candidate && current !== defaults) release(current.bitmaps);
            if (shared !== defaults && shared !== candidate && shared) release(shared.bitmaps);
            current = candidate;
            shared = defaults;
            return;
          } catch (error) {
            if (gen !== loadGen) return;
            if (retryable(error) && attempts++ < 2) continue;
            if (fallback) throw error;
            fallback = true;
            attempts = 0;
          } finally {
            if (!activated && candidate && candidate !== current && candidate !== shared) release(candidate.bitmaps);
            if (!activated && defaults && defaults !== shared && defaults !== current) release(defaults.bitmaps);
          }
        }
      }
    }

    function matching(id, rev) {
      if (current?.id === id && current.rev === rev) return current;
      if (shared?.id === id && shared.rev === rev) return shared;
      return null;
    }

    return {
      load, stop,
      reload(signal) { return request(base + "/reload", signal, "POST"); },
      dispose() {
        stop();
        if (current) release(current.bitmaps);
        if (shared) release(shared.bitmaps);
        current = shared = null;
      }
    };
  }

  window.WsCompanion = Object.assign(window.WsCompanion || {}, { own, packLoader, fetchPackAsset, fetchPackClip });
})();
