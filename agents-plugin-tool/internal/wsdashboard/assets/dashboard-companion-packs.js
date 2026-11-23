(function () {
  "use strict";
  const base = "/api/companion/packs";
  const layerFields = ["file", "line", "jacketFill", "accentFill", "skinFill", "detail", "hairFill"];
  const eyeFields = ["sclera", "irisFill", "irisLine", "frameLower", "lidCoverL", "lidCoverR",
    "lidLashL", "lidLashR", "browL", "browR", "closedL", "closedR"];

  function own(obj, key) { return obj && Object.hasOwn(obj, key) ? obj[key] : undefined; }
  function values(obj) { return Object.values(obj || {}); }
  function raster(file) { return typeof file === "string" && /^[a-z\d][a-z\d._-]{0,127}\.(webp|png)$/i.test(file) && !file.includes(".."); }

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

  async function decode(info, signal) {
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
    if (!failure && !signal.aborted) return { id: info.id, rev: info.rev, manifest, bitmaps };
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
            const needsDefaults = id !== "default" && defaultInfo?.rev &&
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

  window.WsCompanion = { own, packLoader };
})();
