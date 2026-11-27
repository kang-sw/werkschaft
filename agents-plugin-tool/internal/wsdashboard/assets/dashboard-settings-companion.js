(function () {
  "use strict";

  var MAX_CUSTOM_SOURCES = 16;
  var CORE_KEYS = [
    "dashboard.companion_enabled",
    "dashboard.companion_director",
    "dashboard.companion_agent_cooldown_seconds",
    "dashboard.companion_agent_hourly_cap"
  ];
  var WARDROBE_KEYS = [
    "dashboard.companion_preset",
    "dashboard.companion_outfit",
    "dashboard.companion_hair",
    "dashboard.companion_eyes",
    "dashboard.companion_attachments",
    "dashboard.companion_palette"
  ];
  var COUPLED_KEYS = new Set([
    "dashboard.companion_agent_cooldown_seconds",
    "dashboard.companion_agent_hourly_cap"
  ]);
  var AUTHORING_DOCS = [
    { key: "js.settings.companion.advanced.doc_guide", href: "/api/companion/authoring/guide.md" },
    { key: "js.settings.companion.advanced.doc_pack_schema", href: "/api/companion/authoring/pack.schema.json" },
    { key: "js.settings.companion.advanced.doc_market_schema", href: "/api/companion/authoring/marketplace.schema.json" }
  ];

  // Persistent state across re-renders within the Settings session.
  var state = {
    catalog: null,        // { packs: [...], resolvedAuto: "..." }
    catalogLoading: false,
    catalogError: "",
    manifests: new Map(), // `${id}@${rev}` -> manifest object
    manifestInFlight: new Set(),
    sourceListings: new Map(), // source -> { loading, error, listing, expanded }
    updateStatus: new Map(),   // packId -> { state: "checking"|"up-to-date"|"update"|"error", source, rev, message }
    pendingDeletePack: "",
    pendingRemoveSource: "",
    vrmAutoActivate: true,
    vrmBusy: false,
    vrmFeedback: null,    // { kind: "ok"|"err", text }
    sourceInput: "",
    sourceFeedback: null, // { kind: "ok"|"err", text }
    stageFeedback: "",
    advancedOpen: false,
    zipFile: null,
    zipValidating: false,
    zipImporting: false,
    zipListing: null,
    zipSelected: new Set(),
    zipFeedback: null
  };

  function splitTokens(raw) {
    return typeof raw === "string" ? raw.trim().split(/\s+/).filter(Boolean) : [];
  }

  function parsePairs(raw) {
    var map = new Map();
    for (var token of splitTokens(raw)) {
      var eq = token.indexOf("=");
      if (eq > 0 && eq < token.length - 1) map.set(token.slice(0, eq), token.slice(eq + 1));
    }
    return map;
  }

  function formatPairs(map) {
    return Array.from(map.entries())
      .filter(function (pair) { return pair[0] && pair[1]; })
      .sort(function (a, b) { return a[0].localeCompare(b[0]); })
      .map(function (pair) { return pair[0] + "=" + pair[1]; })
      .join(" ");
  }

  function parseHttpsOrigin(raw) {
    var trimmed = (raw || "").trim();
    if (!trimmed) return { ok: false, origin: "" };
    try {
      var u = new URL(trimmed);
      if (u.protocol !== "https:" || !u.hostname || u.username || u.password) return { ok: false, origin: "" };
      if (!u.pathname || u.pathname === "/") return { ok: false, origin: "" };
      var isZip = u.pathname.toLowerCase().endsWith(".zip");
      if (!isZip && (u.search || u.hash)) return { ok: false, origin: "" };
      return { ok: true, origin: u.origin + u.pathname, normalized: trimmed };
    } catch (e) {
      return { ok: false, origin: "" };
    }
  }

  function vrmDisplayStem(filename) {
    var base = (filename || "avatar.vrm").replace(/^.*[/\\]/, "");
    return base.replace(/\.vrm$/i, "").trim() || "avatar";
  }

  function avatarGlyph(isVrm) {
    var ns = "http://www.w3.org/2000/svg";
    var svg = document.createElementNS(ns, "svg");
    svg.setAttribute("viewBox", "0 0 36 36");
    svg.setAttribute("fill", "none");
    svg.setAttribute("stroke", "currentColor");
    svg.setAttribute("stroke-width", "1.9");
    svg.setAttribute("stroke-linecap", "round");
    svg.setAttribute("stroke-linejoin", "round");
    svg.setAttribute("aria-hidden", "true");
    if (isVrm) {
      var head = document.createElementNS(ns, "circle");
      head.setAttribute("cx", "18"); head.setAttribute("cy", "12"); head.setAttribute("r", "5.5");
      var body = document.createElementNS(ns, "path");
      body.setAttribute("d", "M8.5 30c1.4-6.2 5.2-9.5 9.5-9.5s8.1 3.3 9.5 9.5");
      var halo = document.createElementNS(ns, "path");
      halo.setAttribute("d", "M6 12c0-3.5 5.4-6 12-6s12 2.5 12 6");
      halo.setAttribute("opacity", "0.45");
      svg.append(halo, head, body);
    } else {
      var rect = document.createElementNS(ns, "rect");
      rect.setAttribute("x", "8"); rect.setAttribute("y", "8"); rect.setAttribute("width", "20"); rect.setAttribute("height", "20"); rect.setAttribute("rx", "6");
      var eyes = document.createElementNS(ns, "path");
      eyes.setAttribute("d", "M14 16v2M22 16v2M14.5 22.5c1 .9 2.2 1.3 3.5 1.3s2.5-.4 3.5-1.3");
      svg.append(rect, eyes);
    }
    return svg;
  }

  function wardrobeOptions(manifest) {
    var layers = manifest && manifest.layers && typeof manifest.layers === "object" ? manifest.layers : {};
    var keys = function (obj) { return obj && typeof obj === "object" ? Object.keys(obj) : []; };
    var slots = {};
    var attachments = layers.attachments && typeof layers.attachments === "object" ? layers.attachments : {};
    for (var slot of Object.keys(attachments)) {
      var ids = keys(attachments[slot]);
      if (ids.length) slots[slot] = ids;
    }
    var palette = manifest && manifest.palette && typeof manifest.palette === "object" ? manifest.palette : {};
    return {
      presets: keys(manifest && manifest.presets),
      outfit: keys(layers.outfit),
      hair: keys(layers.hair),
      eyes: keys(layers.eyes),
      attachments: slots,
      palette: Object.keys(palette),
      paletteDefaults: palette
    };
  }

  async function fetchJson(url, opts) {
    var response = await fetch(url, Object.assign({ credentials: "same-origin", cache: "no-store" }, opts || {}));
    var data = null;
    try { data = await response.json(); } catch (e) { /* plain error */ }
    return { ok: response.ok, status: response.status, data: data };
  }

  function extractErrorMessage(res, fallback) {
    if (res && res.data) {
      if (typeof res.data.error === "string" && res.data.error) return res.data.error;
      if (res.data.error && typeof res.data.error.message === "string") return res.data.error.message;
      if (res.data.listing && Array.isArray(res.data.listing.errors) && res.data.listing.errors.length) {
        return res.data.listing.errors[0].message || fallback;
      }
    }
    return fallback;
  }

  function entryMap(entries) {
    var map = new Map();
    for (var entry of entries || []) map.set(entry.key, entry);
    return map;
  }

  function effectiveValue(ctx, key) {
    var entry = ctx.byKey.get(key);
    if (!entry) return undefined;
    if (ctx.scope === "builtin") return entry.default;
    var sel = ctx.selected(entry);
    return sel !== undefined ? sel : entry.value;
  }

  function canEditKey(ctx, key) {
    var entry = ctx.byKey.get(key);
    var spec = ctx.specFor(key);
    return !!(entry && spec && ctx.editable(entry, spec));
  }

  function resolveActivePack(ctx) {
    var rawPack = String(effectiveValue(ctx, "dashboard.companion_pack") || "auto").trim() || "auto";
    var packs = state.catalog && Array.isArray(state.catalog.packs) ? state.catalog.packs : [];
    var targetId = rawPack === "auto"
      ? (state.catalog && state.catalog.resolvedAuto) || (packs[0] && packs[0].id) || "default"
      : rawPack;
    var active = packs.find(function (p) { return p.id === targetId; }) ||
      packs.find(function (p) { return p.id === "default"; }) ||
      packs[0] ||
      { id: targetId, name: targetId, renderer: "2d", builtin: targetId === "default", rev: "" };
    return { configured: rawPack, isAuto: rawPack === "auto", active: active, packs: packs };
  }

  function packOriginText(tr, pack) {
    if (pack.builtin) return tr("js.settings.companion.packs.origin_builtin");
    var origin = pack.origin;
    if (!origin) return pack.id;
    if (origin.official) return tr("js.settings.companion.packs.origin_official");
    if (origin.kind === "local") return tr("js.settings.companion.packs.origin_local", origin.source || pack.id);
    if (origin.source) return tr("js.settings.companion.packs.origin_remote", origin.source);
    return pack.id;
  }

  function groupMatchesFilter(ctx, keys, extraTerms) {
    var term = ctx.searchTerm;
    if (ctx.filter !== "all") {
      return keys.some(function (key) {
        var entry = ctx.byKey.get(key);
        var spec = ctx.specFor(key);
        return entry && spec && ctx.matches(entry, spec);
      });
    }
    if (!term) return true;
    for (var key of keys) {
      var entry = ctx.byKey.get(key);
      var spec = ctx.specFor(key);
      if (entry && spec && ctx.matches(entry, spec)) return true;
    }
    var hay = (extraTerms || []).join(" ").toLocaleLowerCase();
    return hay.includes(term);
  }

  function ensureCatalog(rerender) {
    if (state.catalog || state.catalogLoading) return;
    state.catalogLoading = true;
    state.catalogError = "";
    fetchJson("/api/companion/packs").then(function (res) {
      state.catalogLoading = false;
      if (res.ok && res.data && Array.isArray(res.data.packs)) {
        state.catalog = res.data;
      } else {
        state.catalogError = extractErrorMessage(res, "load_failed");
      }
      rerender();
    }).catch(function () {
      state.catalogLoading = false;
      state.catalogError = "load_failed";
      rerender();
    });
  }

  function ensureManifest(pack, rerender) {
    if (!pack || !pack.id || pack.renderer === "vrm") return null;
    var key = pack.id + "@" + (pack.rev || "");
    if (state.manifests.has(key)) return state.manifests.get(key);
    if (state.manifestInFlight.has(key)) return null;
    state.manifestInFlight.add(key);
    var q = pack.rev ? "?rev=" + encodeURIComponent(pack.rev) : "";
    fetchJson("/api/companion/packs/" + encodeURIComponent(pack.id) + "/pack.json" + q).then(function (res) {
      state.manifestInFlight.delete(key);
      if (res.ok && res.data) state.manifests.set(key, res.data);
      rerender();
    }).catch(function () {
      state.manifestInFlight.delete(key);
    });
    return null;
  }

  function makeGroup(ctx, id, iconName, titleKey, subKey, rightNode) {
    var section = ctx.el("section", "cs-group");
    section.dataset.companionGroup = id;
    var head = ctx.el("div", "cs-group-head");
    var left = ctx.append(ctx.el("div", "cs-group-head-left"),
      ctx.svgIcon(iconName),
      ctx.el("h3", "", ctx.tr(titleKey)),
      ctx.el("span", "cs-group-sub", ctx.tr(subKey)));
    head.append(left);
    if (rightNode) head.append(rightNode);
    section.append(head);
    return section;
  }

  function renderCoreGroup(ctx) {
    var visibleKeys = CORE_KEYS.filter(function (key) {
      var entry = ctx.byKey.get(key);
      var spec = ctx.specFor(key);
      return entry && spec && ctx.matches(entry, spec);
    });
    var showStageReset = ctx.filter === "all" && (!ctx.searchTerm ||
      [ctx.tr("js.settings.companion.stage_reset.label"), ctx.tr("js.settings.companion.stage_reset.description"), "reset", "size", "home"]
        .join(" ").toLocaleLowerCase().includes(ctx.searchTerm));
    if (!visibleKeys.length && !showStageReset) return null;

    var enabledVal = effectiveValue(ctx, "dashboard.companion_enabled") === true;
    var statusPill = ctx.el("span", "cs-pill " + (enabledVal ? "ok" : "warn"),
      ctx.tr(enabledVal ? "js.settings.companion.status.on" : "js.settings.companion.status.off"));
    statusPill.dataset.companionStatusPill = enabledVal ? "on" : "off";

    var group = makeGroup(ctx, "core", "i-robot",
      "js.settings.companion.group.core.title",
      "js.settings.companion.group.core.sub",
      statusPill);

    if (!enabledVal) {
      var banner = ctx.el("div", "cs-disabled-banner", ctx.tr("js.settings.companion.disabled_banner"));
      banner.setAttribute("role", "status");
      banner.dataset.companionDisabledBanner = "1";
      group.append(banner);
    }

    var rowsWrap = ctx.el("div", "cs-core-rows");
    for (var key of visibleKeys) {
      var entry = ctx.byKey.get(key);
      var spec = ctx.specFor(key);
      var row = ctx.renderRow(entry, spec);
      if (COUPLED_KEYS.has(key)) row.classList.add("cs-coupled");
      rowsWrap.append(row);
    }

    if (showStageReset) {
      var resetRow = ctx.el("article", "srow krow");
      resetRow.dataset.companionStageReset = "1";
      var meta = ctx.append(ctx.el("div", "kmeta"),
        ctx.append(ctx.el("div", "klabel-line"), ctx.el("span", "klabel", ctx.tr("js.settings.companion.stage_reset.label"))),
        ctx.el("p", "kdesc", ctx.tr("js.settings.companion.stage_reset.description")));
      var feedback = ctx.el("span", "hint ok", state.stageFeedback || "");
      feedback.setAttribute("role", "status");
      var sizeBtn = ctx.button("cs-btn sm", ctx.tr("js.settings.companion.stage_reset.size"), function () {
        try { window.localStorage.removeItem("ws.companion.size"); } catch (e) { /* ignore */ }
        ctx.notifyStageAction("reset-size");
        state.stageFeedback = ctx.tr("js.settings.companion.stage_reset.done_size");
        feedback.textContent = state.stageFeedback;
      });
      sizeBtn.dataset.companionAction = "reset-size";
      var homeBtn = ctx.button("cs-btn sm", ctx.tr("js.settings.companion.stage_reset.home"), function () {
        ctx.notifyStageAction("return-home");
        state.stageFeedback = ctx.tr("js.settings.companion.stage_reset.done_home");
        feedback.textContent = state.stageFeedback;
      });
      homeBtn.dataset.companionAction = "return-home";
      var pair = ctx.append(ctx.el("div", "cs-action-pair"), sizeBtn, homeBtn);
      var rail = ctx.append(ctx.el("div", "sctl-rail"), ctx.append(ctx.el("div", "kctl-wrap"), pair, feedback));
      resetRow.append(meta, rail);
      rowsWrap.append(resetRow);
    }

    group.append(rowsWrap);
    return group;
  }

  function renderAvatarGroup(ctx) {
    var packEntry = ctx.byKey.get("dashboard.companion_pack");
    var packSpec = ctx.specFor("dashboard.companion_pack");
    if (!groupMatchesFilter(ctx, ["dashboard.companion_pack"], [
      ctx.tr("js.settings.companion.group.avatar.title"),
      ctx.tr("js.settings.companion.vrm.drop_title"),
      "vrm", "avatar", "pack", "3d"
    ])) return null;

    var canManagePacks = ctx.scope === "global" && canEditKey(ctx, "dashboard.companion_pack");
    var canWritePackKey = canEditKey(ctx, "dashboard.companion_pack");
    var resolved = resolveActivePack(ctx);
    var active = resolved.active;
    var activeIsVrm = active.renderer === "vrm";
    var hasAnyVrm = resolved.packs.some(function (p) { return p.renderer === "vrm"; });

    var packProv = null;
    if (packEntry && packSpec) {
      packProv = ctx.renderKeyProvenance(packEntry, packSpec);
    }

    var group = makeGroup(ctx, "avatar", "i-star",
      "js.settings.companion.group.avatar.title",
      "js.settings.companion.group.avatar.sub",
      packProv ? packProv.node : null);

    if (packProv && packProv.drawer) group.append(packProv.drawer);

    var body = ctx.el("div", "cs-group-body");

    // 1. Active Avatar Hero Card
    var hero = ctx.el("div", "cs-avatar-hero");
    hero.dataset.companionAvatarHero = "1";
    hero.dataset.activeRenderer = activeIsVrm ? "vrm" : "2d";

    var emblem = ctx.el("div", "cs-avatar-emblem" + (activeIsVrm ? "" : " is-2d"));
    emblem.append(avatarGlyph(activeIsVrm));

    var titleRow = ctx.append(ctx.el("div", "cs-avatar-title-row"),
      ctx.el("span", "cs-avatar-name", active.name || active.id),
      ctx.el("span", "cs-pill " + (activeIsVrm ? "vrm" : ""),
        ctx.tr(activeIsVrm ? "js.settings.companion.badge.vrm" : "js.settings.companion.badge.2d")));
    if (resolved.isAuto) {
      titleRow.append(ctx.el("span", "cs-pill ok", ctx.tr("js.settings.companion.badge.auto")));
    }
    if (active.version) {
      titleRow.append(ctx.el("span", "cs-pill", "v" + active.version));
    }

    var info = ctx.append(ctx.el("div", "cs-avatar-info"), titleRow);
    var originLine = resolved.isAuto
      ? ctx.tr("js.settings.companion.hero.auto_hint", active.id) + " · " + packOriginText(ctx.tr, active)
      : packOriginText(ctx.tr, active);
    info.append(ctx.el("span", "cs-pack-meta", originLine));

    if (active.persona && (active.persona.name || active.persona.voice)) {
      var persona = ctx.el("div", "cs-avatar-persona");
      if (active.persona.name) persona.append(ctx.el("span", "cs-avatar-persona-name", active.persona.name));
      if (active.persona.voice) persona.append(ctx.el("span", "cs-avatar-persona-voice", active.persona.voice));
      info.append(persona);
    }

    var heroRight = ctx.el("div", "cs-avatar-hero-right");
    if (!resolved.isAuto && canWritePackKey && packEntry) {
      var autoBtn = ctx.button("cs-btn sm soft-pri", ctx.tr("js.settings.companion.hero.use_auto"), function () {
        ctx.submitConfig(packEntry, "set", "auto").then(function () {
          ctx.notifyPacksChanged();
        });
      });
      autoBtn.dataset.companionUseAuto = "1";
      heroRight.append(autoBtn);
    }
    hero.append(emblem, info, heroRight);
    body.append(hero);

    // 2. Conditional Official 3D VRM Banner when no VRM pack is installed
    if (state.catalog && !hasAnyVrm) {
      var vrmBanner = ctx.el("div", "cs-official-vrm-banner");
      vrmBanner.dataset.companionOfficialVrmBanner = "1";
      var copy = ctx.append(ctx.el("div", "cs-official-vrm-copy"),
        ctx.append(ctx.el("div", "cs-official-vrm-head"),
          ctx.el("span", "cs-pill vrm", ctx.tr("js.settings.companion.badge.vrm")),
          ctx.el("strong", "", ctx.tr("js.settings.companion.hero.official_vrm_title"))),
        ctx.el("p", "", ctx.tr("js.settings.companion.hero.official_vrm_desc")));
      var cta = ctx.button("cs-btn pri sm", ctx.tr("js.settings.companion.hero.official_vrm_cta"), function () {
        fetchSource(ctx, "official", true);
        var sourcesEl = document.querySelector("[data-companion-group='sources']");
        if (sourcesEl && sourcesEl.scrollIntoView) sourcesEl.scrollIntoView({ behavior: "smooth", block: "nearest" });
      });
      cta.dataset.companionOfficialVrmCta = "1";
      vrmBanner.append(copy, cta);
      body.append(vrmBanner);
    }

    // 3. Borderless tonal .vrm dropzone + styled file picker (no raw file input)
    var dropzone = ctx.el("div", "cs-vrm-dropzone");
    dropzone.dataset.companionVrmDropzone = "1";

    var fileInput = ctx.el("input", "cs-sr-only");
    fileInput.type = "file";
    fileInput.accept = ".vrm";
    fileInput.tabIndex = -1;
    fileInput.dataset.companionVrmInput = "1";

    async function handleVrmFile(file) {
      if (!file || !canManagePacks || state.vrmBusy) return;
      if (!/\.vrm$/i.test(file.name || "")) {
        state.vrmFeedback = { kind: "err", text: ctx.tr("js.settings.companion.vrm.invalid_ext") };
        ctx.rerender();
        return;
      }
      state.vrmBusy = true;
      state.vrmFeedback = { kind: "ok", text: ctx.tr("js.settings.companion.vrm.importing", file.name) };
      ctx.rerender();
      try {
        var stem = vrmDisplayStem(file.name);
        var url = "/api/companion/packs/import-vrm?name=" + encodeURIComponent(stem) +
          "&filename=" + encodeURIComponent(file.name);
        var res = await fetchJson(url, {
          method: "POST",
          headers: { "Content-Type": "model/gltf-binary" },
          body: file
        });
        if (!res.ok || !res.data || !res.data.id) {
          state.vrmFeedback = { kind: "err", text: extractErrorMessage(res, ctx.tr("js.settings.error.config_error")) };
          return;
        }
        var imported = res.data;
        var freshCat = await fetchJson("/api/companion/packs");
        if (freshCat.ok && freshCat.data && Array.isArray(freshCat.data.packs)) {
          state.catalog = freshCat.data;
        }
        if (state.vrmAutoActivate && packEntry && canWritePackKey) {
          await ctx.submitConfig(packEntry, "set", imported.id);
        }
        ctx.notifyPacksChanged();
        state.vrmFeedback = { kind: "ok", text: ctx.tr("js.settings.companion.vrm.imported", imported.name || imported.id) };
      } catch (e) {
        state.vrmFeedback = { kind: "err", text: ctx.tr("js.settings.error.config_error") };
      } finally {
        state.vrmBusy = false;
        ctx.rerender();
      }
    }

    fileInput.addEventListener("change", function () {
      var file = fileInput.files && fileInput.files[0];
      fileInput.value = "";
      if (file) handleVrmFile(file);
    });

    dropzone.addEventListener("dragover", function (e) {
      if (!canManagePacks) return;
      e.preventDefault();
      dropzone.dataset.drag = "over";
    });
    dropzone.addEventListener("dragleave", function (e) {
      if (!dropzone.contains(e.relatedTarget)) delete dropzone.dataset.drag;
    });
    dropzone.addEventListener("drop", function (e) {
      e.preventDefault();
      delete dropzone.dataset.drag;
      if (!canManagePacks) return;
      var file = e.dataTransfer && e.dataTransfer.files && e.dataTransfer.files[0];
      if (file) handleVrmFile(file);
    });

    var pickBtn = ctx.button("cs-btn pri", ctx.tr("js.settings.companion.vrm.choose"), function () {
      fileInput.click();
    });
    pickBtn.prepend(ctx.svgIcon("i-plus"));
    pickBtn.disabled = !canManagePacks || state.vrmBusy;
    pickBtn.dataset.companionVrmChoose = "1";

    var dropMain = ctx.append(ctx.el("div", "cs-vrm-drop-main"),
      ctx.append(ctx.el("div", "cs-vrm-drop-icon"), ctx.svgIcon("i-star")),
      ctx.append(ctx.el("div", "cs-vrm-drop-copy"),
        ctx.el("strong", "", ctx.tr("js.settings.companion.vrm.drop_title")),
        ctx.el("p", "", ctx.tr("js.settings.companion.vrm.drop_desc"))),
      ctx.append(ctx.el("div", "cs-vrm-drop-actions"), pickBtn),
      fileInput);

    var autoCheck = ctx.el("input");
    autoCheck.type = "checkbox";
    autoCheck.checked = state.vrmAutoActivate;
    autoCheck.disabled = !canManagePacks;
    autoCheck.addEventListener("change", function () { state.vrmAutoActivate = autoCheck.checked; });
    var checkLabel = ctx.append(ctx.el("label", "cs-inline-check"), autoCheck,
      ctx.el("span", "", ctx.tr("js.settings.companion.vrm.auto_activate")));

    var dropFoot = ctx.append(ctx.el("div", "cs-vrm-drop-foot"), checkLabel);
    if (!canManagePacks) {
      dropFoot.append(ctx.el("span", "hint", ctx.tr("js.settings.companion.vrm.readonly")));
    }
    dropzone.append(dropMain, dropFoot);

    if (state.vrmFeedback) {
      var fb = ctx.el("div", "cs-feedback " + state.vrmFeedback.kind, state.vrmFeedback.text);
      fb.setAttribute("role", "status");
      dropzone.append(fb);
    }
    body.append(dropzone);

    // 4. Installed Packs List
    var subHead = ctx.append(ctx.el("div", "cs-sub-heading"),
      ctx.el("h4", "", ctx.tr("js.settings.companion.packs.heading")),
      ctx.el("span", "", state.catalog ? ctx.tr("js.settings.companion.packs.count", resolved.packs.length) : ""));
    body.append(subHead);

    if (state.catalogLoading && !state.catalog) {
      body.append(ctx.el("p", "hint", ctx.tr("js.settings.companion.packs.loading")));
    } else if (state.catalogError && !state.catalog) {
      body.append(ctx.el("div", "cs-feedback err", ctx.tr("js.settings.companion.packs.load_failed")));
    } else {
      var list = ctx.el("ul", "cs-pack-list");
      list.dataset.companionPackList = "1";
      for (var pack of resolved.packs) {
        list.append(renderInstalledPackRow(ctx, pack, active, packEntry, canWritePackKey, canManagePacks));
      }
      body.append(list);
    }

    group.append(body);
    return group;
  }

  function renderInstalledPackRow(ctx, pack, active, packEntry, canWritePackKey, canManagePacks) {
    var isActive = active && active.id === pack.id;
    var isVrm = pack.renderer === "vrm";
    var li = ctx.el("li", "cs-pack-row" + (isActive ? " is-active" : ""));
    li.dataset.companionPackRow = pack.id;

    var title = ctx.append(ctx.el("div", "cs-pack-title"),
      ctx.el("strong", "", pack.name || pack.id),
      ctx.el("span", "cs-pill " + (isVrm ? "vrm" : ""),
        ctx.tr(isVrm ? "js.settings.companion.badge.vrm" : "js.settings.companion.badge.2d")));
    if (isActive) title.append(ctx.el("span", "cs-pill ok", ctx.tr("js.settings.companion.badge.active")));
    if (pack.builtin) title.append(ctx.el("span", "cs-pill", ctx.tr("js.settings.companion.badge.builtin")));
    else if (pack.origin && pack.origin.official) title.append(ctx.el("span", "cs-pill official", ctx.tr("js.settings.companion.badge.official")));
    else if (pack.origin && pack.origin.kind === "local") title.append(ctx.el("span", "cs-pill", ctx.tr("js.settings.companion.badge.local")));

    var metaParts = [pack.id];
    if (pack.version) metaParts.push("v" + pack.version);
    metaParts.push(packOriginText(ctx.tr, pack));
    var main = ctx.append(ctx.el("div", "cs-pack-main"), title, ctx.el("span", "cs-pack-meta", metaParts.join(" · ")));

    var actions = ctx.el("div", "cs-pack-actions");
    var applyBtn = ctx.button(
      "cs-btn sm " + (isActive ? "ghost" : "soft-pri"),
      ctx.tr(isActive ? "js.settings.companion.packs.applied" : "js.settings.companion.packs.apply"),
      function () {
        if (!packEntry || !canWritePackKey || isActive) return;
        ctx.submitConfig(packEntry, "set", pack.id).then(function () {
          ctx.notifyPacksChanged();
        });
      }
    );
    applyBtn.disabled = isActive || !canWritePackKey;
    applyBtn.dataset.companionPackApply = pack.id;
    actions.append(applyBtn);

    // Update check / update for remote or official non-builtin packs
    var originSource = !pack.builtin && pack.origin && (pack.origin.official ? "official" : (pack.origin.kind !== "local" ? pack.origin.source : ""));
    if (originSource && canManagePacks) {
      var up = state.updateStatus.get(pack.id);
      if (up && up.state === "update") {
        var updBtn = ctx.button("cs-btn sm pri", ctx.tr("js.settings.companion.packs.update"), function () {
          installFromSource(ctx, originSource, pack.id);
        });
        updBtn.dataset.companionPackUpdate = pack.id;
        actions.append(updBtn);
      } else {
        var chkLabel = up && up.state === "checking"
          ? ctx.tr("js.settings.companion.packs.checking")
          : up && up.state === "up-to-date"
            ? ctx.tr("js.settings.companion.packs.up_to_date")
            : ctx.tr("js.settings.companion.packs.check_update");
        var chkBtn = ctx.button("cs-btn sm ghost", chkLabel, function () {
          checkPackUpdate(ctx, pack, originSource);
        });
        chkBtn.disabled = !!(up && up.state === "checking");
        chkBtn.dataset.companionPackCheck = pack.id;
        actions.append(chkBtn);
      }
    }

    // Inline 2-step delete (no window.confirm) for non-builtin packs
    if (!pack.builtin && canManagePacks) {
      if (state.pendingDeletePack === pack.id) {
        var confirmBox = ctx.el("span", "cs-confirm-inline");
        confirmBox.dataset.companionPackConfirm = pack.id;
        var yesBtn = ctx.button("cs-btn sm danger-solid", ctx.tr("js.settings.companion.packs.confirm"), function () {
          uninstallPack(ctx, pack, isActive, packEntry, canWritePackKey);
        });
        yesBtn.dataset.companionPackConfirmYes = pack.id;
        var noBtn = ctx.button("cs-btn sm ghost", ctx.tr("js.settings.companion.packs.cancel"), function () {
          state.pendingDeletePack = "";
          ctx.rerender();
        });
        confirmBox.append(ctx.el("span", "", ctx.tr("js.settings.companion.packs.delete_prompt")), yesBtn, noBtn);
        actions.append(confirmBox);
      } else {
        var delBtn = ctx.button("cs-btn sm danger", ctx.tr("js.settings.companion.packs.delete"), function () {
          state.pendingDeletePack = pack.id;
          ctx.rerender();
        });
        delBtn.dataset.companionPackDelete = pack.id;
        actions.append(delBtn);
      }
    }

    li.append(main, actions);
    return li;
  }

  async function checkPackUpdate(ctx, pack, source) {
    state.updateStatus.set(pack.id, { state: "checking", source: source });
    ctx.rerender();
    var res = await fetchJson("/api/companion/sources/fetch", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ source: source })
    });
    if (res.ok && res.data && res.data.listing) {
      var listed = (res.data.listing.packs || []).find(function (item) { return item.id === pack.id; });
      if (listed && (listed.status === "update" || (listed.rev && listed.rev !== pack.rev))) {
        state.updateStatus.set(pack.id, { state: "update", source: source, rev: listed.rev });
      } else {
        state.updateStatus.set(pack.id, { state: "up-to-date", source: source });
      }
    } else {
      state.updateStatus.set(pack.id, { state: "error", source: source, message: extractErrorMessage(res, "") });
    }
    ctx.rerender();
  }

  async function uninstallPack(ctx, pack, wasActive, packEntry, canWritePackKey) {
    state.pendingDeletePack = "";
    var res = await fetchJson("/api/companion/packs/" + encodeURIComponent(pack.id) + "/uninstall", { method: "POST" });
    if (!res.ok) {
      state.vrmFeedback = { kind: "err", text: extractErrorMessage(res, ctx.tr("js.settings.error.config_error")) };
      ctx.rerender();
      return;
    }
    if (res.data && Array.isArray(res.data.packs)) {
      state.catalog = res.data;
    } else {
      var fresh = await fetchJson("/api/companion/packs");
      if (fresh.ok && fresh.data) state.catalog = fresh.data;
    }
    if (wasActive && packEntry && canWritePackKey) {
      await ctx.submitConfig(packEntry, "set", "auto");
    }
    state.vrmFeedback = { kind: "ok", text: ctx.tr("js.settings.companion.packs.deleted", pack.name || pack.id) };
    ctx.notifyPacksChanged();
    ctx.rerender();
  }

  async function fetchSource(ctx, source, expand) {
    var prev = state.sourceListings.get(source) || {};
    state.sourceListings.set(source, Object.assign({}, prev, {
      loading: true, error: "", expanded: expand !== undefined ? expand : true
    }));
    ctx.rerender();
    try {
      var res = await fetchJson("/api/companion/sources/fetch", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ source: source })
      });
      if (res.ok && res.data && res.data.listing) {
        var errs = res.data.listing.errors || [];
        state.sourceListings.set(source, {
          loading: false,
          error: errs.length ? errs[0].message : "",
          listing: res.data.listing,
          expanded: true
        });
      } else {
        state.sourceListings.set(source, {
          loading: false,
          error: extractErrorMessage(res, ctx.tr("js.settings.companion.sources.fetch_failed")),
          listing: (res.data && res.data.listing) || null,
          expanded: true
        });
      }
    } catch (e) {
      state.sourceListings.set(source, {
        loading: false,
        error: ctx.tr("js.settings.companion.sources.fetch_failed"),
        listing: null,
        expanded: true
      });
    }
    ctx.rerender();
  }

  async function installFromSource(ctx, source, packId) {
    var prev = state.sourceListings.get(source) || {};
    state.sourceListings.set(source, Object.assign({}, prev, { installingId: packId }));
    ctx.rerender();
    try {
      var res = await fetchJson("/api/companion/sources/install", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ source: source, ids: [packId] })
      });
      if (res.ok && res.data) {
        if (res.data.catalog && Array.isArray(res.data.catalog.packs)) {
          state.catalog = res.data.catalog;
        }
        if (res.data.listing) {
          state.sourceListings.set(source, {
            loading: false,
            error: "",
            listing: res.data.listing,
            expanded: true,
            installingId: ""
          });
        }
        state.updateStatus.delete(packId);
        state.sourceFeedback = { kind: "ok", text: ctx.tr("js.settings.companion.sources.install_done", packId) };
        ctx.notifyPacksChanged();
      } else {
        state.sourceListings.set(source, Object.assign({}, prev, { installingId: "" }));
        state.sourceFeedback = { kind: "err", text: extractErrorMessage(res, ctx.tr("js.settings.error.config_error")) };
      }
    } catch (e) {
      state.sourceListings.set(source, Object.assign({}, prev, { installingId: "" }));
      state.sourceFeedback = { kind: "err", text: ctx.tr("js.settings.error.config_error") };
    }
    ctx.rerender();
  }

  function renderSourcesGroup(ctx) {
    var sourcesEntry = ctx.byKey.get("dashboard.companion_sources");
    var sourcesSpec = ctx.specFor("dashboard.companion_sources");
    if (!groupMatchesFilter(ctx, ["dashboard.companion_sources"], [
      ctx.tr("js.settings.companion.group.sources.title"),
      ctx.tr("js.settings.companion.sources.official_name"),
      "source", "store", "official", "url"
    ])) return null;

    var canEditSources = ctx.scope === "global" && canEditKey(ctx, "dashboard.companion_sources");
    var customSources = splitTokens(effectiveValue(ctx, "dashboard.companion_sources"));

    var prov = sourcesEntry && sourcesSpec ? ctx.renderKeyProvenance(sourcesEntry, sourcesSpec) : null;
    var group = makeGroup(ctx, "sources", "i-book",
      "js.settings.companion.group.sources.title",
      "js.settings.companion.group.sources.sub",
      prov ? prov.node : null);
    if (prov && prov.drawer) group.append(prov.drawer);

    var body = ctx.el("div", "cs-group-body");

    // Single calm trust notice
    var trust = ctx.el("div", "cs-trust-notice", ctx.tr("js.settings.companion.sources.trust_notice"));
    body.append(trust);

    // Inline add source bar with live origin preview
    var addBox = ctx.el("div", "cs-source-add-box");
    var addRow = ctx.el("form", "cs-source-add-row");
    var input = ctx.el("input", "inp mono");
    input.type = "url";
    input.value = state.sourceInput;
    input.placeholder = ctx.tr("js.settings.companion.sources.add_placeholder");
    input.disabled = !canEditSources;
    input.dataset.companionSourceInput = "1";
    input.setAttribute("aria-label", ctx.tr("js.settings.companion.sources.add"));

    var preview = ctx.el("span", "cs-source-preview");
    function updatePreview() {
      state.sourceInput = input.value;
      var parsed = parseHttpsOrigin(input.value);
      preview.textContent = parsed.ok ? ctx.tr("js.settings.companion.sources.preview", parsed.origin) : "";
    }
    input.addEventListener("input", updatePreview);
    updatePreview();

    var addBtn = ctx.button("cs-btn pri", ctx.tr("js.settings.companion.sources.add"));
    addBtn.type = "submit";
    addBtn.disabled = !canEditSources;
    addBtn.dataset.companionSourceAdd = "1";

    addRow.addEventListener("submit", async function (e) {
      e.preventDefault();
      if (!canEditSources || !sourcesEntry) return;
      var parsed = parseHttpsOrigin(input.value);
      if (!parsed.ok) {
        state.sourceFeedback = { kind: "err", text: ctx.tr("js.settings.companion.sources.invalid_url") };
        ctx.rerender();
        return;
      }
      if (customSources.includes(parsed.normalized)) {
        state.sourceFeedback = { kind: "err", text: ctx.tr("js.settings.companion.sources.duplicate") };
        ctx.rerender();
        return;
      }
      if (customSources.length >= MAX_CUSTOM_SOURCES) {
        state.sourceFeedback = { kind: "err", text: ctx.tr("js.settings.companion.sources.limit") };
        ctx.rerender();
        return;
      }
      var nextList = customSources.concat([parsed.normalized]).join(" ");
      state.sourceFeedback = null;
      var ok = await ctx.submitConfig(sourcesEntry, "set", nextList);
      if (ok) {
        state.sourceInput = "";
        ctx.rerender();
      }
    });

    addRow.append(input, addBtn);
    addBox.append(addRow, preview);
    if (state.sourceFeedback) {
      var sfb = ctx.el("div", "cs-feedback " + state.sourceFeedback.kind, state.sourceFeedback.text);
      sfb.setAttribute("role", "status");
      addBox.append(sfb);
    }
    body.append(addBox);

    // Sources list: Official first, then custom URLs
    var list = ctx.el("ul", "cs-source-list");
    list.dataset.companionSourceList = "1";
    list.append(renderSourceItem(ctx, "official", true, canEditSources, sourcesEntry, customSources));
    for (var url of customSources) {
      list.append(renderSourceItem(ctx, url, false, canEditSources, sourcesEntry, customSources));
    }
    body.append(list);

    group.append(body);
    return group;
  }

  function renderSourceItem(ctx, source, isOfficial, canEditSources, sourcesEntry, customSources) {
    var li = ctx.el("li", "cs-source-item");
    li.dataset.companionSourceItem = source;
    var entry = state.sourceListings.get(source);
    var expanded = !!(entry && entry.expanded);

    var head = ctx.el("div", "cs-source-head");
    var title = ctx.el("div", "cs-source-title");
    if (isOfficial) {
      title.append(
        ctx.el("strong", "", ctx.tr("js.settings.companion.sources.official_name")),
        ctx.el("span", "cs-pill official", ctx.tr("js.settings.companion.badge.official")));
    } else {
      title.append(ctx.el("span", "cs-source-url", source));
    }
    var info = ctx.append(ctx.el("div", "cs-source-info"), title);
    if (isOfficial) info.append(ctx.el("span", "cs-pack-meta", ctx.tr("js.settings.companion.sources.official_desc")));

    var actions = ctx.el("div", "cs-pack-actions");
    var browseLabel = entry && entry.loading
      ? ctx.tr("js.settings.companion.sources.fetching")
      : expanded
        ? ctx.tr("js.settings.companion.sources.refresh")
        : ctx.tr("js.settings.companion.sources.browse");
    var browseBtn = ctx.button("cs-btn sm " + (expanded ? "" : "soft-pri"), browseLabel, function () {
      fetchSource(ctx, source, true);
    });
    browseBtn.disabled = !!(entry && entry.loading);
    browseBtn.dataset.companionSourceFetch = source;
    actions.append(browseBtn);

    if (expanded) {
      var hideBtn = ctx.button("cs-btn sm ghost", ctx.tr("js.settings.companion.sources.hide"), function () {
        state.sourceListings.set(source, Object.assign({}, entry, { expanded: false }));
        ctx.rerender();
      });
      actions.append(hideBtn);
    }

    if (!isOfficial && canEditSources && sourcesEntry) {
      if (state.pendingRemoveSource === source) {
        var confirmBox = ctx.el("span", "cs-confirm-inline");
        var yesBtn = ctx.button("cs-btn sm danger-solid", ctx.tr("js.settings.companion.packs.confirm"), async function () {
          state.pendingRemoveSource = "";
          var remaining = customSources.filter(function (s) { return s !== source; }).join(" ");
          state.sourceListings.delete(source);
          await ctx.submitConfig(sourcesEntry, remaining ? "set" : "reset", remaining);
          ctx.rerender();
        });
        var noBtn = ctx.button("cs-btn sm ghost", ctx.tr("js.settings.companion.packs.cancel"), function () {
          state.pendingRemoveSource = "";
          ctx.rerender();
        });
        confirmBox.append(ctx.el("span", "", ctx.tr("js.settings.companion.sources.remove_prompt")), yesBtn, noBtn);
        actions.append(confirmBox);
      } else {
        var rmBtn = ctx.button("cs-btn sm danger", ctx.tr("js.settings.companion.sources.remove"), function () {
          state.pendingRemoveSource = source;
          ctx.rerender();
        });
        rmBtn.dataset.companionSourceRemove = source;
        actions.append(rmBtn);
      }
    }

    head.append(info, actions);
    li.append(head);

    if (expanded && entry) {
      var cat = ctx.el("div", "cs-source-catalog");
      if (entry.loading && !entry.listing) {
        cat.append(ctx.el("span", "hint", ctx.tr("js.settings.companion.sources.fetching")));
      } else if (entry.error) {
        cat.append(ctx.el("div", "cs-feedback err", entry.error));
      }
      if (entry.listing) {
        var metaParts = [];
        if (entry.listing.name) metaParts.push(entry.listing.name);
        if (entry.listing.owner) metaParts.push(entry.listing.owner);
        if (entry.listing.commit) metaParts.push(ctx.tr("js.settings.companion.sources.commit", entry.listing.commit.slice(0, 10)));
        if (metaParts.length) {
          cat.append(ctx.el("div", "cs-catalog-meta", metaParts.join(" · ")));
        }
        var packs = entry.listing.packs || [];
        if (!packs.length) {
          cat.append(ctx.el("span", "hint", ctx.tr("js.settings.companion.sources.empty")));
        } else {
          var grid = ctx.el("div", "cs-catalog-grid");
          for (var item of packs) {
            grid.append(renderCatalogCard(ctx, source, item, entry.installingId === item.id, canEditSources));
          }
          cat.append(grid);
        }
      }
      li.append(cat);
    }

    return li;
  }

  function renderCatalogCard(ctx, source, item, busy, canInstall) {
    var card = ctx.el("div", "cs-catalog-card");
    card.dataset.companionCatalogPack = item.id;
    var isVrm = item.renderer === "vrm";
    var head = ctx.append(ctx.el("div", "cs-catalog-card-head"),
      ctx.el("strong", "", item.name || item.id),
      ctx.el("span", "cs-pill " + (isVrm ? "vrm" : ""),
        ctx.tr(isVrm ? "js.settings.companion.badge.vrm" : "js.settings.companion.badge.2d")));
    if (item.version) head.append(ctx.el("span", "cs-pill", "v" + item.version));

    card.append(head);
    card.append(ctx.el("span", "cs-catalog-card-meta", item.id));
    if (item.description) card.append(ctx.el("p", "cs-catalog-card-desc", item.description));
    if (item.error && item.error.message) {
      card.append(ctx.el("div", "cs-feedback err", item.error.message));
    }

    var foot = ctx.el("div", "cs-catalog-card-foot");
    var status = item.status || "available";
    if (status === "builtin") {
      foot.append(ctx.el("span", "cs-pill", ctx.tr("js.settings.companion.badge.builtin")));
    } else if (status === "installed") {
      foot.append(ctx.el("span", "cs-pill ok", ctx.tr("js.settings.companion.sources.installed")));
    } else if (!item.error) {
      var labelKey = status === "update"
        ? "js.settings.companion.packs.update"
        : status === "replace"
          ? "js.settings.companion.sources.replace"
          : "js.settings.companion.sources.install";
      var btn = ctx.button(
        "cs-btn sm " + (status === "replace" ? "" : "pri"),
        busy ? ctx.tr("js.settings.companion.sources.installing") : ctx.tr(labelKey),
        function () { installFromSource(ctx, source, item.id); }
      );
      btn.disabled = busy || !canInstall;
      btn.dataset.companionSourceInstall = item.id;
      foot.append(btn);
    }
    card.append(foot);
    return card;
  }

  function renderWardrobeGroup(ctx) {
    if (!groupMatchesFilter(ctx, WARDROBE_KEYS, [
      ctx.tr("js.settings.companion.group.wardrobe.title"),
      "wardrobe", "2d", "preset", "outfit", "hair", "eyes", "palette", "vroid"
    ])) return null;

    var resolved = resolveActivePack(ctx);
    var active = resolved.active;
    var isVrm = active.renderer === "vrm";

    var group = makeGroup(ctx, "wardrobe", "i-wrench",
      "js.settings.companion.group.wardrobe.title",
      "js.settings.companion.group.wardrobe.sub",
      ctx.el("span", "cs-pill " + (isVrm ? "vrm" : ""),
        ctx.tr(isVrm ? "js.settings.companion.badge.vrm" : "js.settings.companion.badge.2d")));

    var body = ctx.el("div", "cs-group-body");

    if (isVrm) {
      var note = ctx.el("div", "cs-vrm-note");
      note.dataset.companionWardrobeVrmNote = "1";
      var copy = ctx.append(ctx.el("div", "cs-vrm-note-copy"),
        ctx.el("strong", "", ctx.tr("js.settings.companion.wardrobe.vrm_note_title")),
        ctx.el("span", "", ctx.tr("js.settings.companion.wardrobe.vrm_note_body")));
      note.append(copy);
      var first2d = resolved.packs.find(function (p) { return p.renderer !== "vrm"; });
      var packEntry = ctx.byKey.get("dashboard.companion_pack");
      if (first2d && packEntry && canEditKey(ctx, "dashboard.companion_pack")) {
        var swBtn = ctx.button("cs-btn sm soft-pri", ctx.tr("js.settings.companion.wardrobe.switch_2d"), function () {
          ctx.submitConfig(packEntry, "set", first2d.id).then(function () {
            ctx.notifyPacksChanged();
          });
        });
        swBtn.dataset.companionSwitch2d = "1";
        note.append(swBtn);
      }
      body.append(note);
      group.append(body);
      return group;
    }

    var manifest = ensureManifest(active, ctx.rerender);
    var opts = wardrobeOptions(manifest);
    var grid = ctx.el("div", "cs-wardrobe-grid");
    grid.dataset.companionWardrobeGrid = "1";
    var added = 0;

    function addSelectField(configKey, choices, includeFollowPreset) {
      var entry = ctx.byKey.get(configKey);
      var spec = ctx.specFor(configKey);
      if (!entry || !spec || !ctx.matches(entry, spec)) return;
      if (!choices.length && !effectiveValue(ctx, configKey)) return;
      added++;
      var canEdit = ctx.editable(entry, spec);
      var current = String(effectiveValue(ctx, configKey) || "");
      var field = ctx.el("div", "cs-wardrobe-field");
      field.dataset.configKey = configKey;

      var prov = ctx.renderKeyProvenance(entry, spec);
      var head = ctx.append(ctx.el("div", "cs-wardrobe-head"),
        ctx.el("label", "", ctx.tr(spec.label)),
        prov.node);

      var select = ctx.el("select", "sel mono");
      select.disabled = !canEdit;
      select.setAttribute("aria-label", ctx.tr(spec.label));
      if (includeFollowPreset) {
        select.add(new Option(ctx.tr("js.settings.companion.wardrobe.follow_preset"), "", false, current === ""));
      }
      var list = choices.slice();
      if (current && !list.includes(current)) list.unshift(current);
      for (var id of list) {
        select.add(new Option(id, id, false, id === current));
      }
      select.addEventListener("change", function () {
        if (!canEdit) return;
        ctx.submitConfig(entry, select.value ? "set" : "reset", select.value);
      });
      field.append(head, select);
      if (prov.drawer) field.append(prov.drawer);
      grid.append(field);
    }

    addSelectField("dashboard.companion_preset", opts.presets, false);
    addSelectField("dashboard.companion_outfit", opts.outfit, true);
    addSelectField("dashboard.companion_hair", opts.hair, true);
    addSelectField("dashboard.companion_eyes", opts.eyes, true);

    // Attachments (slot=id pairs)
    var attEntry = ctx.byKey.get("dashboard.companion_attachments");
    var attSpec = ctx.specFor("dashboard.companion_attachments");
    var slotNames = Object.keys(opts.attachments);
    if (attEntry && attSpec && ctx.matches(attEntry, attSpec) && slotNames.length) {
      added++;
      var attEditable = ctx.editable(attEntry, attSpec);
      var attMap = parsePairs(effectiveValue(ctx, "dashboard.companion_attachments"));
      var attField = ctx.el("div", "cs-wardrobe-field full");
      attField.dataset.configKey = "dashboard.companion_attachments";
      var attProv = ctx.renderKeyProvenance(attEntry, attSpec);
      attField.append(ctx.append(ctx.el("div", "cs-wardrobe-head"),
        ctx.el("span", "cs-wlbl", ctx.tr(attSpec.label)), attProv.node));

      var slotGrid = ctx.el("div", "cs-slot-grid");
      for (var slot of slotNames) {
        (function (slotName) {
          var item = ctx.el("label", "cs-slot-item");
          item.append(ctx.el("span", "", slotName));
          var sel = ctx.el("select", "sel mono");
          sel.disabled = !attEditable;
          var cur = attMap.get(slotName) || "";
          sel.add(new Option(ctx.tr("js.settings.companion.wardrobe.follow_preset"), "", false, cur === ""));
          sel.add(new Option(ctx.tr("js.settings.companion.wardrobe.attachment_none"), "none", false, cur === "none"));
          for (var optId of opts.attachments[slotName]) {
            sel.add(new Option(optId, optId, false, cur === optId));
          }
          sel.addEventListener("change", function () {
            if (!attEditable) return;
            var next = new Map(attMap);
            if (sel.value) next.set(slotName, sel.value);
            else next.delete(slotName);
            var serialized = formatPairs(next);
            ctx.submitConfig(attEntry, serialized ? "set" : "reset", serialized);
          });
          item.append(sel);
          slotGrid.append(item);
        })(slot);
      }
      attField.append(slotGrid);
      if (attProv.drawer) attField.append(attProv.drawer);
      grid.append(attField);
    }

    // Palette swatches + Reset colors
    var palEntry = ctx.byKey.get("dashboard.companion_palette");
    var palSpec = ctx.specFor("dashboard.companion_palette");
    if (palEntry && palSpec && ctx.matches(palEntry, palSpec) && opts.palette.length) {
      added++;
      var palEditable = ctx.editable(palEntry, palSpec);
      var palMap = parsePairs(effectiveValue(ctx, "dashboard.companion_palette"));
      var palField = ctx.el("div", "cs-wardrobe-field full");
      palField.dataset.configKey = "dashboard.companion_palette";
      var palProv = ctx.renderKeyProvenance(palEntry, palSpec);

      var resetColorsBtn = ctx.button("cs-btn sm ghost", ctx.tr("js.settings.companion.wardrobe.reset_colors"), function () {
        if (!palEditable) return;
        ctx.submitConfig(palEntry, "reset", "");
      });
      resetColorsBtn.disabled = !palEditable || !palMap.size;
      resetColorsBtn.dataset.companionResetColors = "1";

      var palRight = ctx.append(ctx.el("div", "cs-pack-actions"), resetColorsBtn, palProv.node);
      palField.append(ctx.append(ctx.el("div", "cs-wardrobe-head"),
        ctx.el("span", "cs-wlbl", ctx.tr(palSpec.label)), palRight));

      var bar = ctx.el("div", "cs-palette-bar");
      for (var channel of opts.palette) {
        (function (ch) {
          var rawHex = palMap.get(ch) || opts.paletteDefaults[ch] || "#888888";
          var hex = /^#[0-9a-f]{6}$/i.test(rawHex) ? rawHex : "#888888";
          var chip = ctx.el("label", "cs-swatch-chip");
          var picker = ctx.el("input");
          picker.type = "color";
          picker.value = hex;
          picker.disabled = !palEditable;
          picker.setAttribute("aria-label", ch);
          var hexText = ctx.el("span", "cs-swatch-hex", hex);
          picker.addEventListener("change", function () {
            if (!palEditable) return;
            var next = new Map(palMap);
            next.set(ch, picker.value);
            hexText.textContent = picker.value;
            ctx.submitConfig(palEntry, "set", formatPairs(next));
          });
          chip.append(picker, ctx.append(ctx.el("span", "cs-swatch-meta"),
            ctx.el("span", "cs-swatch-name", ch), hexText));
          bar.append(chip);
        })(channel);
      }
      palField.append(bar);
      if (palProv.drawer) palField.append(palProv.drawer);
      grid.append(palField);
    }

    if (added) {
      body.append(grid);
    } else if (manifest) {
      body.append(ctx.el("p", "hint", ctx.tr("js.settings.companion.wardrobe.empty")));
    } else {
      body.append(ctx.el("p", "hint", ctx.tr("js.settings.companion.packs.loading")));
    }

    group.append(body);
    return group;
  }

  function renderAdvancedGroup(ctx) {
    if (ctx.filter !== "all") return null;
    if (ctx.searchTerm) {
      var hay = [
        ctx.tr("js.settings.companion.group.advanced.title"),
        ctx.tr("js.settings.companion.advanced.zip_title"),
        ctx.tr("js.settings.companion.advanced.docs_title"),
        "zip", "authoring", "schema", "bundle"
      ].join(" ").toLocaleLowerCase();
      if (!hay.includes(ctx.searchTerm)) return null;
    }

    var canManagePacks = ctx.scope === "global" && canEditKey(ctx, "dashboard.companion_pack");
    var group = ctx.el("section", "cs-group");
    group.dataset.companionGroup = "advanced";

    var trigger = ctx.button("cs-disclosure-trigger", "", function () {
      state.advancedOpen = !state.advancedOpen;
      ctx.rerender();
    });
    trigger.setAttribute("aria-expanded", String(state.advancedOpen));
    trigger.dataset.companionAdvancedToggle = "1";
    trigger.append(
      ctx.append(ctx.el("div", "cs-group-head-left"),
        ctx.svgIcon("i-gear"),
        ctx.el("h3", "", ctx.tr("js.settings.companion.group.advanced.title")),
        ctx.el("span", "cs-group-sub", ctx.tr("js.settings.companion.group.advanced.sub"))),
      ctx.el("span", "cs-disclosure-chev", "▾"));
    group.append(trigger);

    if (!state.advancedOpen) return group;

    var body = ctx.el("div", "cs-group-body");
    var advGrid = ctx.el("div", "cs-adv-grid");

    // Card 1: .zip bundle import (styled button, no raw file input)
    var zipCard = ctx.el("div", "cs-adv-card");
    zipCard.append(
      ctx.el("h4", "", ctx.tr("js.settings.companion.advanced.zip_title")),
      ctx.el("p", "", ctx.tr("js.settings.companion.advanced.zip_desc")));

    var zipInput = ctx.el("input", "cs-sr-only");
    zipInput.type = "file";
    zipInput.accept = ".zip,application/zip";
    zipInput.tabIndex = -1;
    zipInput.dataset.companionZipInput = "1";

    zipInput.addEventListener("change", async function () {
      var file = zipInput.files && zipInput.files[0];
      zipInput.value = "";
      if (!file || !canManagePacks) return;
      state.zipFile = file;
      state.zipValidating = true;
      state.zipListing = null;
      state.zipSelected = new Set();
      state.zipFeedback = { kind: "ok", text: ctx.tr("js.settings.companion.advanced.zip_validating", file.name) };
      ctx.rerender();
      try {
        var res = await fetchJson("/api/companion/packs/validate?name=" + encodeURIComponent(file.name), {
          method: "POST",
          headers: { "Content-Type": "application/zip" },
          body: file
        });
        if (res.data && res.data.listing) {
          state.zipListing = res.data.listing;
          for (var p of (res.data.listing.packs || [])) {
            if (!p.error && p.status !== "builtin") state.zipSelected.add(p.id);
          }
          var errs = res.data.listing.errors || [];
          state.zipFeedback = errs.length ? { kind: "err", text: errs[0].message } : null;
        } else {
          state.zipFeedback = { kind: "err", text: extractErrorMessage(res, ctx.tr("js.settings.error.config_error")) };
        }
      } catch (e) {
        state.zipFeedback = { kind: "err", text: ctx.tr("js.settings.error.config_error") };
      } finally {
        state.zipValidating = false;
        ctx.rerender();
      }
    });

    var zipChoose = ctx.button("cs-btn sm soft-pri", ctx.tr("js.settings.companion.advanced.zip_choose"), function () {
      zipInput.click();
    });
    zipChoose.disabled = !canManagePacks || state.zipValidating || state.zipImporting;
    zipChoose.dataset.companionZipChoose = "1";
    zipCard.append(ctx.append(ctx.el("div", "cs-pack-actions"), zipChoose, zipInput));

    if (state.zipFeedback) {
      var zfb = ctx.el("div", "cs-feedback " + state.zipFeedback.kind, state.zipFeedback.text);
      zfb.setAttribute("role", "status");
      zipCard.append(zfb);
    }

    if (state.zipListing && Array.isArray(state.zipListing.packs) && state.zipListing.packs.length) {
      var preview = ctx.el("div", "cs-zip-preview");
      var zlist = ctx.el("ul", "cs-zip-list");
      for (var zp of state.zipListing.packs) {
        (function (packItem) {
          var row = ctx.el("li", "cs-zip-row");
          var chk = ctx.el("input");
          chk.type = "checkbox";
          chk.checked = state.zipSelected.has(packItem.id);
          chk.disabled = !!packItem.error || packItem.status === "builtin" || state.zipImporting;
          chk.addEventListener("change", function () {
            if (chk.checked) state.zipSelected.add(packItem.id);
            else state.zipSelected.delete(packItem.id);
            ctx.rerender();
          });
          var lbl = ctx.append(ctx.el("label", "cs-inline-check"), chk,
            ctx.el("span", "", (packItem.name || packItem.id) + " (" + packItem.id + ")"));
          row.append(lbl);
          if (packItem.error && packItem.error.message) {
            row.append(ctx.el("span", "cs-pill fail", packItem.error.message));
          } else if (packItem.renderer === "vrm") {
            row.append(ctx.el("span", "cs-pill vrm", ctx.tr("js.settings.companion.badge.vrm")));
          }
          zlist.append(row);
        })(zp);
      }
      var importBtn = ctx.button("cs-btn sm pri",
        state.zipImporting
          ? ctx.tr("js.settings.companion.advanced.zip_importing")
          : ctx.tr("js.settings.companion.advanced.zip_import_selected", state.zipSelected.size),
        async function () {
          if (!state.zipFile || !state.zipSelected.size || state.zipImporting) return;
          state.zipImporting = true;
          ctx.rerender();
          try {
            var ids = Array.from(state.zipSelected);
            var qs = "name=" + encodeURIComponent(state.zipFile.name) +
              ids.map(function (id) { return "&id=" + encodeURIComponent(id); }).join("");
            var res = await fetchJson("/api/companion/packs/import?" + qs, {
              method: "POST",
              headers: { "Content-Type": "application/zip" },
              body: state.zipFile
            });
            if (res.ok && res.data) {
              if (res.data.catalog && Array.isArray(res.data.catalog.packs)) {
                state.catalog = res.data.catalog;
              }
              state.zipListing = null;
              state.zipFile = null;
              state.zipSelected = new Set();
              state.zipFeedback = { kind: "ok", text: ctx.tr("js.settings.companion.advanced.zip_imported", ids.length) };
              ctx.notifyPacksChanged();
            } else {
              state.zipFeedback = { kind: "err", text: extractErrorMessage(res, ctx.tr("js.settings.error.config_error")) };
            }
          } catch (e) {
            state.zipFeedback = { kind: "err", text: ctx.tr("js.settings.error.config_error") };
          } finally {
            state.zipImporting = false;
            ctx.rerender();
          }
        });
      importBtn.disabled = !state.zipSelected.size || state.zipImporting;
      importBtn.dataset.companionZipImport = "1";
      preview.append(zlist, ctx.append(ctx.el("div", "cs-pack-actions"), importBtn));
      zipCard.append(preview);
    }

    // Card 2: Authoring docs & schema links
    var docsCard = ctx.el("div", "cs-adv-card");
    docsCard.append(
      ctx.el("h4", "", ctx.tr("js.settings.companion.advanced.docs_title")),
      ctx.el("p", "", ctx.tr("js.settings.companion.advanced.docs_desc")));
    var links = ctx.el("div", "cs-doc-links");
    for (var doc of AUTHORING_DOCS) {
      var a = ctx.el("a", "cs-doc-link");
      a.href = doc.href;
      a.target = "_blank";
      a.rel = "noopener noreferrer";
      a.append(ctx.svgIcon("i-book"), ctx.el("span", "", ctx.tr(doc.key)));
      links.append(a);
    }
    docsCard.append(links);

    advGrid.append(zipCard, docsCard);
    body.append(advGrid);
    group.append(body);
    return group;
  }

  function render(options) {
    var ctx = Object.assign({}, options, { byKey: entryMap(options.entries) });
    ensureCatalog(ctx.rerender);

    var panel = ctx.el("div", "cs-panel");
    panel.dataset.companionSettingsPanel = "1";

    var groups = [
      renderCoreGroup(ctx),
      renderAvatarGroup(ctx),
      renderSourcesGroup(ctx),
      renderWardrobeGroup(ctx),
      renderAdvancedGroup(ctx)
    ].filter(Boolean);

    if (!groups.length) {
      return null;
    }
    panel.append(...groups);
    return panel;
  }

  window.WsSettingsCompanion = {
    render: render,
    resetState: function () {
      state.catalog = null;
      state.catalogLoading = false;
      state.catalogError = "";
      state.manifests.clear();
      state.manifestInFlight.clear();
      state.sourceListings.clear();
      state.updateStatus.clear();
      state.pendingDeletePack = "";
      state.pendingRemoveSource = "";
      state.vrmFeedback = null;
      state.sourceFeedback = null;
      state.stageFeedback = "";
      state.zipFile = null;
      state.zipListing = null;
      state.zipSelected.clear();
      state.zipFeedback = null;
    }
  };
})();
