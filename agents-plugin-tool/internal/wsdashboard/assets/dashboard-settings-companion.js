(function () {
  "use strict";

  var MAX_CUSTOM_SOURCES = 16;
  var MAX_WARDROBE_PAIRS = 16;
  var BUILTIN_PALETTE_DEFAULTS = {
    eye: "#dfa040",
    hair: "#ec8468",
    outfitAccent: "#e6aa34",
    outfitPrimary: "#f2e5d4",
    skin: "#f4cdb4"
  };
  var HEX_COLOR_RE = /^#[0-9a-f]{6}$/i;
  var CORE_KEYS = [
    "dashboard.companion_enabled",
    "dashboard.companion_director",
    "dashboard.companion_agent_cooldown_seconds",
    "dashboard.companion_agent_hourly_cap"
  ];
  var MEMORY_KEYS = [
    "dashboard.companion_soul",
    "dashboard.companion_soul_dir"
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
    zipFeedback: null,
    // Portable Soul & Local Episodes Memory Drawer state
    memoryTab: "soul",          // "soul" | "approvals" | "episodes" | "sync"
    memoryKey: "",
    soulData: null,
    soulLoading: false,
    soulError: "",
    personaDraft: "",
    profileDraft: "",
    personaDirty: false,
    profileDirty: false,
    soulSavingDoc: "",          // "" | "persona.md" | "profile.md"
    memoryFeedback: null,       // { kind: "ok"|"warn"|"err", text }
    pendingItems: null,
    pendingLoading: false,
    pendingDrafts: new Map(),   // id -> edited content
    pendingBusyId: 0,
    episodesData: null,         // { episodes, projects, total }
    episodesLoading: false,
    episodesQuery: "",
    episodesProject: "",
    episodesPruneConfirm: false,
    episodesBusy: false,
    soulExporting: false,
    soulImporting: false,
    soulImportOverwrite: false,
    syncFeedback: null
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

  function formatPairs(map, keepKey) {
    var entries = Array.from(map.entries())
      .filter(function (pair) { return pair[0] && pair[1]; })
      .sort(function (a, b) { return a[0].localeCompare(b[0]); });
    if (entries.length > MAX_WARDROBE_PAIRS) {
      var kept = entries.slice(0, MAX_WARDROBE_PAIRS);
      if (keepKey && !kept.some(function (pair) { return pair[0] === keepKey; })) {
        var target = entries.find(function (pair) { return pair[0] === keepKey; });
        if (target) kept[kept.length - 1] = target;
        kept.sort(function (a, b) { return a[0].localeCompare(b[0]); });
      }
      entries = kept;
    }
    return entries.map(function (pair) { return pair[0] + "=" + pair[1]; }).join(" ");
  }

  function isBuiltinPack(pack) {
    return !!(pack && (pack.builtin === true || pack.source === "builtin" || pack.source === "embedded"));
  }

  function packVersion(pack) {
    if (!pack) return "";
    return pack.version || (pack.origin && pack.origin.version) || "";
  }

  function ownerLabel(owner) {
    if (!owner) return "";
    if (typeof owner === "string") return owner;
    return owner.name || "";
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
    var m = manifest && typeof manifest === "object" ? manifest : {};
    var sortedKeys = function (obj) {
      return obj && typeof obj === "object" && !Array.isArray(obj) ? Object.keys(obj).sort() : [];
    };
    var slots = {};
    var attachments = m.attachments && typeof m.attachments === "object" ? m.attachments : {};
    for (var attId of sortedKeys(attachments)) {
      var att = attachments[attId];
      if (!att || typeof att !== "object" || attId === "none") continue;
      if (typeof att.slot === "string" && att.slot) {
        if (!Object.prototype.hasOwnProperty.call(slots, att.slot)) slots[att.slot] = [];
        slots[att.slot].push(attId);
      }
    }
    // Collect built-in channels plus any pack/preset/outfit/layer-fill palette channels.
    var paletteDefaults = Object.assign({}, BUILTIN_PALETTE_DEFAULTS);
    var channelSet = new Set(Object.keys(BUILTIN_PALETTE_DEFAULTS));
    function collectPalette(obj) {
      if (!obj || typeof obj !== "object" || Array.isArray(obj)) return;
      for (var k of Object.keys(obj)) {
        channelSet.add(k);
        if (typeof obj[k] === "string" && HEX_COLOR_RE.test(obj[k])) paletteDefaults[k] = obj[k].toLowerCase();
      }
    }
    function collectLayer(item) {
      if (!item || typeof item !== "object") return;
      collectPalette(item.fills);
      if (Array.isArray(item.frames)) {
        for (var f of item.frames) if (f && typeof f === "object") collectPalette(f.fills);
      }
    }
    function collectLayers(list) {
      if (Array.isArray(list)) for (var item of list) collectLayer(item);
    }
    collectPalette(m.palette);
    for (var preset of Object.values(m.presets && typeof m.presets === "object" ? m.presets : {})) {
      if (preset && typeof preset === "object") collectPalette(preset.palette);
    }
    for (var outfit of Object.values(m.outfits && typeof m.outfits === "object" ? m.outfits : {})) {
      if (!outfit || typeof outfit !== "object") continue;
      collectPalette(outfit.palette);
      for (var sec of ["poses", "gestures"]) {
        for (var col of Object.values(outfit[sec] && typeof outfit[sec] === "object" ? outfit[sec] : {})) {
          if (col) collectLayers(col.layers);
        }
      }
    }
    for (var attObj of Object.values(attachments)) {
      if (!attObj || typeof attObj !== "object") continue;
      collectLayers(attObj.layers);
      for (var acol of Object.values(attObj.columns && typeof attObj.columns === "object" ? attObj.columns : {})) {
        if (acol) collectLayers(acol.layers);
      }
    }
    for (var topSec of ["poses", "gestures"]) {
      for (var topEntry of Object.values(m[topSec] && typeof m[topSec] === "object" ? m[topSec] : {})) {
        if (topEntry) collectLayers(topEntry.layers);
      }
    }
    for (var hair of Object.values(m.hairs && typeof m.hairs === "object" ? m.hairs : {})) {
      if (hair && typeof hair === "object") {
        for (var part of ["back", "front", "acc"]) collectLayer(hair[part]);
      }
    }
    if (m.head && m.head.egg) collectLayer(m.head.egg);
    return {
      presets: sortedKeys(m.presets),
      outfit: sortedKeys(m.outfits),
      hair: sortedKeys(m.hairs),
      eyes: sortedKeys(m.eyesets),
      attachments: slots,
      palette: Array.from(channelSet).sort(),
      paletteDefaults: paletteDefaults
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
    var ra = state.catalog && state.catalog.resolvedAuto;
    var autoId = typeof ra === "string" ? ra : (ra && ra.packId) || "";
    var targetId = rawPack === "auto"
      ? autoId || (packs[0] && packs[0].id) || "default"
      : rawPack;
    var active = packs.find(function (p) { return p.id === targetId; }) ||
      packs.find(function (p) { return p.id === "default"; }) ||
      packs[0] ||
      { id: targetId, name: targetId, renderer: "2d", source: targetId === "default" ? "builtin" : "local", rev: "" };
    return { configured: rawPack, isAuto: rawPack === "auto", active: active, packs: packs };
  }

  function packOriginText(tr, pack) {
    if (isBuiltinPack(pack)) return tr("js.settings.companion.packs.origin_builtin");
    var origin = pack.origin;
    if (!origin) return tr("js.settings.companion.badge.local");
    if (origin.official) return tr("js.settings.companion.packs.origin_official");
    if (origin.kind === "local") return tr("js.settings.companion.packs.origin_local", origin.source || pack.id);
    if (origin.source) return tr("js.settings.companion.packs.origin_remote", origin.source);
    return tr("js.settings.companion.badge.local");
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
    if (state.catalog || state.catalogLoading || state.catalogError) return;
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
      state.manifests.set(key, res.ok && res.data ? res.data : {});
      rerender();
    }).catch(function () {
      state.manifestInFlight.delete(key);
      state.manifests.set(key, {});
      rerender();
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
    // Companion Agents card (dashboard-settings-companion-agents.js).
    var agentsCard = window.WsSettingsCompanionAgents ? window.WsSettingsCompanionAgents.render(ctx) : null;
    if (!visibleKeys.length && !showStageReset && !agentsCard) return null;

    var rawEnabled = effectiveValue(ctx, "dashboard.companion_enabled");
    var enabledVal = rawEnabled === true || rawEnabled === "true";
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
    if (agentsCard) group.append(agentsCard);
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

    var group = makeGroup(ctx, "avatar", "i-person",
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
    var activeVer = packVersion(active);
    if (activeVer) {
      titleRow.append(ctx.el("span", "cs-pill", "v" + activeVer));
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
      ctx.append(ctx.el("div", "cs-vrm-drop-icon"), ctx.svgIcon("i-archive-restore")),
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
    var builtin = isBuiltinPack(pack);
    var li = ctx.el("li", "cs-pack-row" + (isActive ? " is-active" : ""));
    li.dataset.companionPackRow = pack.id;

    var title = ctx.append(ctx.el("div", "cs-pack-title"),
      ctx.el("strong", "", pack.name || pack.id),
      ctx.el("span", "cs-pill " + (isVrm ? "vrm" : ""),
        ctx.tr(isVrm ? "js.settings.companion.badge.vrm" : "js.settings.companion.badge.2d")));
    if (isActive) title.append(ctx.el("span", "cs-pill ok", ctx.tr("js.settings.companion.badge.active")));
    if (builtin) title.append(ctx.el("span", "cs-pill", ctx.tr("js.settings.companion.badge.builtin")));
    else if (pack.origin && pack.origin.official) title.append(ctx.el("span", "cs-pill official", ctx.tr("js.settings.companion.badge.official")));
    else if (pack.origin && pack.origin.kind === "local") title.append(ctx.el("span", "cs-pill", ctx.tr("js.settings.companion.badge.local")));

    var metaParts = [pack.id];
    var ver = packVersion(pack);
    if (ver) metaParts.push("v" + ver);
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
    var originSource = !builtin && pack.origin && (pack.origin.official ? "official" : (pack.origin.kind !== "local" ? pack.origin.source : ""));
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
    if (!builtin && canManagePacks) {
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
        } else {
          var fresh = await fetchJson("/api/companion/packs");
          if (fresh.ok && fresh.data && Array.isArray(fresh.data.packs)) state.catalog = fresh.data;
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
        var failed = ((res.data.listing && res.data.listing.packs) || []).filter(function (p) { return p.error && p.error.message; });
        if (failed.length) {
          var detail = failed.map(function (p) { return p.id + ": " + p.error.message; }).join("; ");
          state.sourceFeedback = { kind: "err", text: detail };
        } else {
          state.updateStatus.delete(packId);
          state.sourceFeedback = { kind: "ok", text: ctx.tr("js.settings.companion.sources.install_done", packId) };
          ctx.notifyPacksChanged();
        }
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
    var group = makeGroup(ctx, "sources", "i-fetch",
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
        var owner = ownerLabel(entry.listing.owner);
        if (owner) metaParts.push(owner);
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
    var isVrm = item.renderer === "vrm" || item.format === "vrm";
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
    } else if (status === "manual") {
      foot.append(ctx.el("span", "cs-pill", ctx.tr("js.settings.companion.badge.local")));
    } else if (!item.error) {
      var isReplace = status === "replace" || status === "other";
      var labelKey = status === "update"
        ? "js.settings.companion.packs.update"
        : isReplace
          ? "js.settings.companion.sources.replace"
          : "js.settings.companion.sources.install";
      var btn = ctx.button(
        "cs-btn sm " + (isReplace ? "" : "pri"),
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
        swBtn.setAttribute("data-companion-switch-2d", "1");
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
    var attMap = parsePairs(effectiveValue(ctx, "dashboard.companion_attachments"));
    var slotNames = Object.keys(opts.attachments);
    for (var cfgSlot of attMap.keys()) {
      if (!slotNames.includes(cfgSlot)) slotNames.push(cfgSlot);
    }
    if (attEntry && attSpec && ctx.matches(attEntry, attSpec) && slotNames.length) {
      added++;
      var attEditable = ctx.editable(attEntry, attSpec);
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
          var choices = (opts.attachments[slotName] || []).slice();
          if (cur && cur !== "none" && !choices.includes(cur)) choices.push(cur);
          for (var optId of choices) {
            sel.add(new Option(optId, optId, false, cur === optId));
          }
          sel.addEventListener("change", function () {
            if (!attEditable) return;
            var next = new Map(attMap);
            if (sel.value) next.set(slotName, sel.value);
            else next.delete(slotName);
            var serialized = formatPairs(next, slotName);
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
    var palMap = parsePairs(effectiveValue(ctx, "dashboard.companion_palette"));
    var palChannels = opts.palette.slice();
    for (var cfgCh of palMap.keys()) {
      if (!palChannels.includes(cfgCh)) palChannels.push(cfgCh);
    }
    if (palEntry && palSpec && ctx.matches(palEntry, palSpec) && palChannels.length) {
      added++;
      var palEditable = ctx.editable(palEntry, palSpec);
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
      for (var channel of palChannels) {
        (function (ch) {
          var rawHex = palMap.get(ch) || opts.paletteDefaults[ch] || "#888888";
          var hex = HEX_COLOR_RE.test(rawHex) ? rawHex.toLowerCase() : "#888888";
          var chip = ctx.el("label", "cs-swatch-chip");
          var picker = ctx.el("input");
          picker.type = "color";
          picker.value = hex;
          picker.disabled = !palEditable;
          picker.setAttribute("aria-label", ch);
          var hexText = ctx.el("span", "cs-swatch-hex", hex);
          picker.addEventListener("change", function () {
            if (!palEditable) return;
            var picked = String(picker.value || "").toLowerCase();
            if (!HEX_COLOR_RE.test(picked)) return;
            var next = new Map(palMap);
            next.set(ch, picked);
            hexText.textContent = picked;
            ctx.submitConfig(palEntry, "set", formatPairs(next, ch));
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
      zfb.setAttribute("role", state.zipFeedback.kind === "err" ? "alert" : "status");
      zipCard.append(zfb);
    }

    if (state.zipListing && Array.isArray(state.zipListing.packs) && state.zipListing.packs.length) {
      var preview = ctx.el("div", "cs-zip-preview");
      var zipMetaParts = [];
      if (state.zipListing.name) zipMetaParts.push(state.zipListing.name);
      if (state.zipListing.source) zipMetaParts.push(state.zipListing.source);
      if (zipMetaParts.length) {
        preview.append(ctx.el("div", "cs-catalog-meta", zipMetaParts.join(" · ")));
      }
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
          } else if (packItem.renderer === "vrm" || packItem.format === "vrm") {
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
              } else {
                var freshCat = await fetchJson("/api/companion/packs");
                if (freshCat.ok && freshCat.data && Array.isArray(freshCat.data.packs)) state.catalog = freshCat.data;
              }
              var listingPacks = (res.data.listing && res.data.listing.packs) || [];
              var failedPacks = listingPacks.filter(function (p) { return p.error && p.error.message; });
              var writtenCount = listingPacks.filter(function (p) { return p.installed; }).length;
              state.zipListing = null;
              state.zipFile = null;
              state.zipSelected = new Set();
              if (failedPacks.length) {
                var errDetail = failedPacks.map(function (p) { return p.id + ": " + p.error.message; }).join("; ");
                state.zipFeedback = { kind: "err", text: writtenCount + "/" + ids.length + " — " + errDetail };
              } else {
                state.zipFeedback = { kind: "ok", text: ctx.tr("js.settings.companion.advanced.zip_imported", writtenCount || ids.length) };
              }
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
      a.append(ctx.svgIcon("i-repo"), ctx.el("span", "", ctx.tr(doc.key)));
      links.append(a);
    }
    docsCard.append(links);

    advGrid.append(zipCard, docsCard);
    body.append(advGrid);
    group.append(body);
    return group;
  }

  function loadSoulState(soulKey, rerender, force) {
    if (!force && state.memoryKey === soulKey && (state.soulData || state.soulLoading || state.soulError)) return;
    if (state.memoryKey !== soulKey) {
      state.pendingItems = null;
    }
    state.memoryKey = soulKey;
    state.soulLoading = true;
    state.soulError = "";
    fetchJson("/api/companion/soul").then(function (res) {
      state.soulLoading = false;
      if (res.ok && res.data) {
        state.soulData = res.data;
        if (!state.personaDirty || force) {
          state.personaDraft = res.data.persona || "";
          state.personaDirty = false;
        }
        if (!state.profileDirty || force) {
          state.profileDraft = res.data.profile || "";
          state.profileDirty = false;
        }
        if (state.memoryTab === "approvals" && state.pendingItems === null) {
          loadPendingItems(rerender, true);
        }
      } else {
        state.soulError = extractErrorMessage(res, "load_failed");
      }
      rerender();
    }).catch(function () {
      state.soulLoading = false;
      state.soulError = "load_failed";
      rerender();
    });
  }

  function loadPendingItems(rerender, force) {
    if (!force && (state.pendingItems !== null || state.pendingLoading)) return;
    state.pendingLoading = true;
    var activeSoulId = (state.soulData && state.soulData.soulId) ? String(state.soulData.soulId) : "";
    var url = "/api/companion/memory/pending" + (activeSoulId ? "?soul=" + encodeURIComponent(activeSoulId) : "");
    fetchJson(url).then(function (res) {
      state.pendingLoading = false;
      if (res.ok && res.data && Array.isArray(res.data.items)) {
        state.pendingItems = res.data.items;
        if (state.soulData) state.soulData.pendingCount = res.data.items.length;
      } else {
        state.pendingItems = [];
      }
      rerender();
    }).catch(function () {
      state.pendingLoading = false;
      state.pendingItems = [];
      rerender();
    });
  }

  function loadEpisodes(rerender, force) {
    if (!force && (state.episodesData !== null || state.episodesLoading)) return;
    state.episodesLoading = true;
    var qs = [];
    if (state.episodesQuery.trim()) qs.push("q=" + encodeURIComponent(state.episodesQuery.trim()));
    if (state.episodesProject) qs.push("project=" + encodeURIComponent(state.episodesProject));
    var url = "/api/companion/memory/episodes" + (qs.length ? "?" + qs.join("&") : "");
    fetchJson(url).then(function (res) {
      state.episodesLoading = false;
      if (res.ok && res.data) {
        state.episodesData = res.data;
        if (state.soulData && typeof res.data.total === "number") {
          state.soulData.episodeCount = res.data.total;
        }
      } else {
        state.episodesData = { episodes: [], projects: [], total: 0 };
      }
      rerender();
    }).catch(function () {
      state.episodesLoading = false;
      state.episodesData = { episodes: [], projects: [], total: 0 };
      rerender();
    });
  }

  function renderMemoryGroup(ctx) {
    var visibleKeys = MEMORY_KEYS.filter(function (key) {
      var entry = ctx.byKey.get(key);
      var spec = ctx.specFor(key);
      return entry && spec && ctx.matches(entry, spec);
    });
    if (!groupMatchesFilter(ctx, MEMORY_KEYS, [
      ctx.tr("js.settings.companion.group.memory.title"),
      ctx.tr("js.settings.companion.group.memory.sub"),
      "soul", "memory", "persona", "profile", "episodes", "approval", "scrubber", "zip"
    ])) return null;

    var activeSoulVal = String(effectiveValue(ctx, "dashboard.companion_soul") || "");
    var activeDirVal = String(effectiveValue(ctx, "dashboard.companion_soul_dir") || "");
    var soulKey = activeSoulVal + "\n" + activeDirVal;
    loadSoulState(soulKey, ctx.rerender, false);

    var canManageMemory = ctx.scope === "global" && canEditKey(ctx, "dashboard.companion_soul");
    var pendingCount = state.pendingItems !== null
      ? state.pendingItems.length
      : (state.soulData && typeof state.soulData.pendingCount === "number" ? state.soulData.pendingCount : 0);

    var rightPill = null;
    if (pendingCount > 0) {
      rightPill = ctx.el("span", "cs-pill warn", ctx.tr("js.settings.companion.memory.pending_badge", pendingCount));
      rightPill.dataset.companionMemoryPendingBadge = String(pendingCount);
    }

    var group = makeGroup(ctx, "memory", "i-repo",
      "js.settings.companion.group.memory.title",
      "js.settings.companion.group.memory.sub",
      rightPill);

    if (visibleKeys.length) {
      var rowsWrap = ctx.el("div", "cs-core-rows");
      for (var key of visibleKeys) {
        var entry = ctx.byKey.get(key);
        var spec = ctx.specFor(key);
        rowsWrap.append(ctx.renderRow(entry, spec));
      }
      group.append(rowsWrap);
    }

    var body = ctx.el("div", "cs-group-body cs-memory-body");

    if (!canManageMemory) {
      body.append(ctx.el("div", "cs-trust-notice", ctx.tr("js.settings.companion.memory.readonly")));
    }

    // Segmented tab bar (borderless Material tonal pills)
    var tabBar = ctx.el("div", "cs-mem-tabs");
    tabBar.setAttribute("role", "tablist");
    var tabs = [
      { id: "soul", label: ctx.tr("js.settings.companion.memory.tab.soul") },
      { id: "approvals", label: ctx.tr("js.settings.companion.memory.tab.approvals") + (pendingCount > 0 ? " (" + pendingCount + ")" : "") },
      { id: "episodes", label: ctx.tr("js.settings.companion.memory.tab.episodes") },
      { id: "sync", label: ctx.tr("js.settings.companion.memory.tab.sync") }
    ];
    for (var tab of tabs) {
      (function (t) {
        var btn = ctx.button("cs-mem-tab" + (state.memoryTab === t.id ? " active" : ""), t.label, function () {
          state.memoryTab = t.id;
          state.episodesPruneConfirm = false;
          if (t.id === "approvals") loadPendingItems(ctx.rerender, true);
          if (t.id === "episodes") loadEpisodes(ctx.rerender, true);
          ctx.rerender();
        });
        btn.setAttribute("role", "tab");
        btn.setAttribute("aria-selected", String(state.memoryTab === t.id));
        btn.dataset.companionMemoryTab = t.id;
        tabBar.append(btn);
      })(tab);
    }
    var refreshBtn = ctx.button("cs-btn sm cs-mem-refresh", ctx.tr("js.settings.companion.memory.refresh"), function () {
      state.memoryFeedback = null;
      state.syncFeedback = null;
      loadSoulState(soulKey, ctx.rerender, true);
      if (state.memoryTab === "approvals") loadPendingItems(ctx.rerender, true);
      if (state.memoryTab === "episodes") loadEpisodes(ctx.rerender, true);
      ctx.rerender();
    });
    refreshBtn.dataset.companionMemoryRefresh = "1";
    var tabHeader = ctx.append(ctx.el("div", "cs-mem-bar"), tabBar, refreshBtn);
    body.append(tabHeader);

    if (state.memoryFeedback) {
      var mfb = ctx.el("div", "cs-feedback " + state.memoryFeedback.kind, state.memoryFeedback.text);
      mfb.setAttribute("role", state.memoryFeedback.kind === "err" ? "alert" : "status");
      mfb.dataset.companionMemoryFeedback = state.memoryFeedback.kind;
      body.append(mfb);
    }

    if (state.soulLoading && !state.soulData) {
      body.append(ctx.el("div", "cs-mem-empty", ctx.tr("js.settings.companion.memory.loading")));
      group.append(body);
      return group;
    }
    if (state.soulError && !state.soulData) {
      body.append(ctx.el("div", "cs-feedback err", ctx.tr("js.settings.companion.memory.load_failed")));
      group.append(body);
      return group;
    }

    if (state.memoryTab === "soul") {
      body.append(renderMemorySoulTab(ctx, canManageMemory, soulKey));
    } else if (state.memoryTab === "approvals") {
      loadPendingItems(ctx.rerender, false);
      body.append(renderMemoryApprovalsTab(ctx, canManageMemory, soulKey));
    } else if (state.memoryTab === "episodes") {
      loadEpisodes(ctx.rerender, false);
      body.append(renderMemoryEpisodesTab(ctx, canManageMemory));
    } else if (state.memoryTab === "sync") {
      body.append(renderMemorySyncTab(ctx, canManageMemory, soulKey));
    }

    group.append(body);
    return group;
  }

  // presetLabel picks a preset label for the dashboard locale from a
  // {locale: text} map (exact, case-insensitive, Chinese script, then base
  // language), falling back to the English text.
  function presetLabel(byLang, fallback) {
    if (!byLang || typeof byLang !== "object") return fallback || "";
    var root = document.documentElement || {};
    var lang = String(root.lang || (root.dataset && root.dataset.lang) || "en").replace(/_/g, "-");
    var lower = lang.toLowerCase();
    var keys = Object.keys(byLang);
    var find = function (pred) {
      for (var i = 0; i < keys.length; i++) {
        if (pred(keys[i].toLowerCase()) && byLang[keys[i]]) return byLang[keys[i]];
      }
      return "";
    };
    var hit = find(function (k) { return k === lower; });
    if (!hit && /^(zh|yue)\b/.test(lower)) {
      var hant = lower.indexOf("yue") === 0 || /-(hant|tw|hk|mo)\b/.test(lower);
      hit = find(function (k) { return k === (hant ? "zh-hant" : "zh-hans"); });
    }
    if (!hit) {
      var base = lower.split("-")[0];
      hit = find(function (k) { return k.split("-")[0] === base; });
    }
    return hit || fallback || "";
  }

  // renderPersonaPresets renders the embedded official persona presets as
  // one-click picks. Picking one replaces a custom persona.md, so while the
  // persona is customized each pick needs the shared hold-to-confirm.
  function renderPersonaPresets(ctx, canManageMemory, soulKey, personaSource) {
    var presets = (state.soulData && Array.isArray(state.soulData.presets)) ? state.soulData.presets : [];
    if (!presets.length) return null;
    var activeId = String((state.soulData && state.soulData.activePresetId) || presets[0].id || "");
    var custom = personaSource === "user";
    // A pack persona is in effect: no preset is the one speaking.
    var packOwned = personaSource === "pack";
    var row = ctx.el("div", "cs-mem-presets");
    row.dataset.companionPersonaPresets = "1";
    row.append(ctx.el("span", "cs-mem-presets-title", ctx.tr("js.settings.companion.memory.persona_presets_title")));
    var list = ctx.el("div", "cs-mem-preset-list");
    presets.forEach(function (p) {
      if (!p || !p.id) return;
      var title = presetLabel(p.titleByLang, p.title || p.name || p.id);
      var summary = presetLabel(p.summaryByLang, p.summary || "");
      var active = !custom && !packOwned && p.id === activeId;
      var btn = ctx.button("cs-btn sm cs-mem-preset" + (active ? " active" : ""), "");
      btn.textContent = "";
      btn.append(ctx.el("span", "cs-mem-preset-title", title));
      if (summary) btn.append(ctx.el("span", "cs-mem-preset-sub", summary));
      btn.dataset.companionPersonaPreset = p.id;
      btn.setAttribute("aria-pressed", active ? "true" : "false");
      var pick = async function () {
        if (!canManageMemory || state.soulSavingDoc || active) return;
        state.soulSavingDoc = "persona.md:preset";
        state.memoryFeedback = null;
        ctx.rerender();
        try {
          var res = await fetchJson("/api/companion/soul", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              action: "preset",
              soulId: (state.soulData && state.soulData.soulId) || "",
              presetId: p.id
            })
          });
          if (res.ok) {
            state.personaDirty = false;
            state.memoryFeedback = { kind: "ok", text: ctx.tr("js.settings.companion.memory.persona_preset_done", title) };
            loadSoulState(soulKey, ctx.rerender, true);
          } else {
            state.memoryFeedback = { kind: "err", text: extractErrorMessage(res, ctx.tr("js.settings.error.config_error")) };
          }
        } catch (e) {
          state.memoryFeedback = { kind: "err", text: ctx.tr("js.settings.error.config_error") };
        } finally {
          state.soulSavingDoc = "";
          ctx.rerender();
        }
      };
      if (custom && window.WsHoldConfirm) {
        btn.title = (summary ? summary + " · " : "") + ctx.tr("js.settings.account.hold_hint");
        window.WsHoldConfirm.attach(btn, { mode: "bar", onConfirm: pick });
      } else {
        if (summary) btn.title = summary;
        btn.addEventListener("click", pick);
      }
      btn.disabled = !canManageMemory || !!state.soulSavingDoc;
      list.append(btn);
    });
    row.append(list);
    if (custom) {
      row.append(ctx.el("p", "cs-mem-hint cs-mem-preset-warn", ctx.tr("js.settings.companion.memory.persona_preset_replaces_custom")));
    }
    return row;
  }

  function renderMemorySoulTab(ctx, canManageMemory, soulKey) {
    var wrap = ctx.el("div", "cs-mem-pane");
    wrap.dataset.companionMemoryPane = "soul";

    var editorsGrid = ctx.el("div", "cs-mem-doc-grid");
    var docs = [
      {
        target: "persona.md",
        titleKey: "js.settings.companion.memory.persona_title",
        hintKey: "js.settings.companion.memory.persona_hint",
        getValue: function () { return state.personaDraft; },
        setValue: function (v) { state.personaDraft = v; state.personaDirty = true; }
      },
      {
        target: "profile.md",
        titleKey: "js.settings.companion.memory.profile_title",
        hintKey: "js.settings.companion.memory.profile_hint",
        getValue: function () { return state.profileDraft; },
        setValue: function (v) { state.profileDraft = v; state.profileDirty = true; }
      }
    ];

    for (var doc of docs) {
      (function (d) {
        var card = ctx.el("div", "cs-mem-card");
        var head = ctx.el("div", "cs-mem-card-head");
        head.append(ctx.el("h4", "mono", ctx.tr(d.titleKey)));
        var isPersona = d.target === "persona.md";
        var personaSource = isPersona && state.soulData ? String(state.soulData.personaSource || "") : "";
        var personaInherited = isPersona && !!(state.soulData && state.soulData.personaInherited);
        var personaSourceKey = {
          official: "js.settings.companion.memory.persona_source_official",
          preset: "js.settings.companion.memory.persona_source_preset",
          pack: "js.settings.companion.memory.persona_source_pack",
          user: "js.settings.companion.memory.persona_source_user"
        }[personaSource];
        if (personaSourceKey) {
          var sourcePill = ctx.el("span", "cs-pill cs-mem-persona-source" + (personaInherited ? "" : " ok"),
            ctx.tr(personaSourceKey));
          sourcePill.dataset.companionPersonaSource = personaSource;
          head.append(sourcePill);
        }
        // Reset to default drops both a custom persona.md and a picked preset.
        if (isPersona && (personaSource === "user" || personaSource === "preset")) {
          var resetLabel = ctx.tr("js.settings.companion.memory.persona_reset");
          var resetBtn = ctx.button("cs-btn sm ghost cs-mem-reset",
            state.soulSavingDoc === "persona.md:reset"
              ? ctx.tr("js.settings.companion.memory.saving")
              : resetLabel);
          var doReset = async function () {
            if (!canManageMemory || state.soulSavingDoc) return;
            state.soulSavingDoc = "persona.md:reset";
            state.memoryFeedback = null;
            ctx.rerender();
            try {
              var res = await fetchJson("/api/companion/soul", {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({
                  action: "reset",
                  soulId: (state.soulData && state.soulData.soulId) || "",
                  target: "persona.md"
                })
              });
              if (res.ok) {
                state.personaDirty = false;
                state.memoryFeedback = { kind: "ok", text: ctx.tr("js.settings.companion.memory.persona_reset_done") };
                loadSoulState(soulKey, ctx.rerender, true);
              } else {
                state.memoryFeedback = { kind: "err", text: extractErrorMessage(res, ctx.tr("js.settings.error.config_error")) };
              }
            } catch (e) {
              state.memoryFeedback = { kind: "err", text: ctx.tr("js.settings.error.config_error") };
            } finally {
              state.soulSavingDoc = "";
              ctx.rerender();
            }
          };
          // Reset removes the user's persona.md and preset pick: a destructive
          // action, so it uses the shared 1.5s hold-to-confirm when available.
          if (window.WsHoldConfirm) {
            resetBtn.title = resetLabel + " · " + ctx.tr("js.settings.account.hold_hint");
            window.WsHoldConfirm.attach(resetBtn, { mode: "bar", onConfirm: doReset });
          } else {
            resetBtn.addEventListener("click", doReset);
          }
          resetBtn.disabled = !canManageMemory || !!state.soulSavingDoc;
          resetBtn.dataset.companionPersonaReset = "1";
          head.append(resetBtn);
        }
        var saveBtn = ctx.button("cs-btn sm pri",
          state.soulSavingDoc === d.target
            ? ctx.tr("js.settings.companion.memory.saving")
            : ctx.tr("js.settings.companion.memory.save_doc"),
          async function () {
            if (!canManageMemory || state.soulSavingDoc) return;
            state.soulSavingDoc = d.target;
            state.memoryFeedback = null;
            ctx.rerender();
            try {
              var res = await fetchJson("/api/companion/soul", {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({
                  soulId: (state.soulData && state.soulData.soulId) || "",
                  target: d.target,
                  content: d.getValue()
                })
              });
              if (res.ok && res.data) {
                if (res.data.status === "held_pending") {
                  state.memoryFeedback = { kind: "warn", text: ctx.tr("js.settings.companion.memory.held_notice") };
                  loadPendingItems(ctx.rerender, true);
                } else {
                  if (d.target === "persona.md") state.personaDirty = false;
                  else state.profileDirty = false;
                  state.memoryFeedback = { kind: "ok", text: ctx.tr("js.settings.companion.memory.saved", d.target) };
                  loadSoulState(soulKey, ctx.rerender, true);
                }
              } else {
                state.memoryFeedback = { kind: "err", text: extractErrorMessage(res, ctx.tr("js.settings.error.config_error")) };
              }
            } catch (e) {
              state.memoryFeedback = { kind: "err", text: ctx.tr("js.settings.error.config_error") };
            } finally {
              state.soulSavingDoc = "";
              ctx.rerender();
            }
          });
        // An untouched inherited persona is not saved: saving would pin the
        // live default into a user-owned persona.md.
        saveBtn.disabled = !canManageMemory || !!state.soulSavingDoc || (personaInherited && !state.personaDirty);
        saveBtn.dataset.companionSoulSave = d.target;
        head.append(saveBtn);

        var ta = ctx.el("textarea", "cs-mem-textarea mono");
        ta.rows = 6;
        ta.value = d.getValue();
        ta.disabled = !canManageMemory;
        ta.dataset.companionSoulEditor = d.target;
        ta.addEventListener("input", function () {
          d.setValue(ta.value);
          if (personaInherited) saveBtn.disabled = !canManageMemory || !!state.soulSavingDoc;
        });

        card.append(head, ctx.el("p", "cs-mem-hint", ctx.tr(d.hintKey)));
        if (isPersona) {
          var presetRow = renderPersonaPresets(ctx, canManageMemory, soulKey, personaSource);
          if (presetRow) card.append(presetRow);
        }
        card.append(ta);
        editorsGrid.append(card);
      })(doc);
    }
    wrap.append(editorsGrid);

    // Recent Soul shards panel
    var shardCard = ctx.el("div", "cs-mem-card");
    var recent = (state.soulData && Array.isArray(state.soulData.recentEntries)) ? state.soulData.recentEntries : [];
    var totalEntries = (state.soulData && state.soulData.totalEntries) || recent.length;
    var shardCount = (state.soulData && state.soulData.shardCount) || 0;
    var shardHead = ctx.append(ctx.el("div", "cs-mem-card-head"),
      ctx.el("h4", "", ctx.tr("js.settings.companion.memory.shards_title")),
      ctx.el("span", "cs-group-sub", ctx.tr("js.settings.companion.memory.shards_meta", totalEntries, shardCount)));
    shardCard.append(shardHead);

    if (!recent.length) {
      shardCard.append(ctx.el("div", "cs-mem-empty", ctx.tr("js.settings.companion.memory.shards_empty")));
    } else {
      var list = ctx.el("ul", "cs-mem-list");
      list.dataset.companionSoulShards = "1";
      for (var item of recent) {
        var li = ctx.el("li", "cs-mem-item");
        var meta = ctx.el("div", "cs-mem-item-meta");
        if (item.kind) meta.append(ctx.el("span", "cs-pill", item.kind));
        if (item.machine_id) meta.append(ctx.el("span", "cs-mem-tag mono", item.machine_id));
        if (item.ts) meta.append(ctx.el("span", "cs-mem-time mono", item.ts));
        li.append(meta, ctx.el("div", "cs-mem-item-body", item.content || ""));
        list.append(li);
      }
      shardCard.append(list);
    }
    wrap.append(shardCard);
    return wrap;
  }

  function renderMemoryApprovalsTab(ctx, canManageMemory, soulKey) {
    var wrap = ctx.el("div", "cs-mem-pane");
    wrap.dataset.companionMemoryPane = "approvals";
    wrap.append(ctx.el("p", "cs-mem-hint", ctx.tr("js.settings.companion.memory.approvals_intro")));

    if (state.pendingLoading && state.pendingItems === null) {
      wrap.append(ctx.el("div", "cs-mem-empty", ctx.tr("js.settings.companion.memory.loading")));
      return wrap;
    }
    var items = Array.isArray(state.pendingItems) ? state.pendingItems : [];
    if (!items.length) {
      wrap.append(ctx.el("div", "cs-mem-empty", ctx.tr("js.settings.companion.memory.approvals_empty")));
      return wrap;
    }

    var list = ctx.el("div", "cs-mem-list");
    list.dataset.companionPendingList = "1";
    for (var item of items) {
      (function (it) {
        var card = ctx.el("div", "cs-mem-card cs-mem-pending-card");
        card.dataset.companionPendingId = String(it.id);

        var meta = ctx.el("div", "cs-mem-item-meta");
        meta.append(ctx.el("span", "cs-pill warn mono", it.target || "shard"));
        for (var f of (it.findings || [])) {
          meta.append(ctx.el("span", "cs-pill fail", f.category + (f.sample ? ": " + f.sample : "")));
        }
        if (it.createdAt) meta.append(ctx.el("span", "cs-mem-time mono", it.createdAt));

        var draftVal = state.pendingDrafts.has(it.id) ? state.pendingDrafts.get(it.id) : (it.content || "");
        var ta = ctx.el("textarea", "cs-mem-textarea mono");
        ta.rows = 3;
        ta.value = draftVal;
        ta.disabled = !canManageMemory || state.pendingBusyId === it.id;
        ta.dataset.companionPendingEditor = String(it.id);
        ta.addEventListener("input", function () { state.pendingDrafts.set(it.id, ta.value); });

        var actions = ctx.el("div", "cs-pack-actions");
        var approveBtn = ctx.button("cs-btn sm pri", ctx.tr("js.settings.companion.memory.approve"), async function () {
          if (!canManageMemory || state.pendingBusyId) return;
          state.pendingBusyId = it.id;
          state.memoryFeedback = null;
          ctx.rerender();
          try {
            var edited = state.pendingDrafts.has(it.id) ? state.pendingDrafts.get(it.id) : it.content;
            var res = await fetchJson("/api/companion/memory/pending/" + encodeURIComponent(it.id) + "/approve", {
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify({ content: edited })
            });
            if (res.ok) {
              state.pendingDrafts.delete(it.id);
              state.memoryFeedback = { kind: "ok", text: ctx.tr("js.settings.companion.memory.approved_ok") };
              loadPendingItems(ctx.rerender, true);
              loadSoulState(soulKey, ctx.rerender, true);
            } else {
              state.memoryFeedback = { kind: "err", text: extractErrorMessage(res, ctx.tr("js.settings.error.config_error")) };
            }
          } catch (e) {
            state.memoryFeedback = { kind: "err", text: ctx.tr("js.settings.error.config_error") };
          } finally {
            state.pendingBusyId = 0;
            ctx.rerender();
          }
        });
        approveBtn.disabled = !canManageMemory || state.pendingBusyId === it.id;
        approveBtn.dataset.companionPendingApprove = String(it.id);

        var discardBtn = ctx.button("cs-btn sm danger", ctx.tr("js.settings.companion.memory.discard"), async function () {
          if (!canManageMemory || state.pendingBusyId) return;
          state.pendingBusyId = it.id;
          state.memoryFeedback = null;
          ctx.rerender();
          try {
            var res = await fetchJson("/api/companion/memory/pending/" + encodeURIComponent(it.id) + "/discard", {
              method: "POST"
            });
            if (res.ok) {
              state.pendingDrafts.delete(it.id);
              state.memoryFeedback = { kind: "ok", text: ctx.tr("js.settings.companion.memory.discarded_ok") };
              loadPendingItems(ctx.rerender, true);
            } else {
              state.memoryFeedback = { kind: "err", text: extractErrorMessage(res, ctx.tr("js.settings.error.config_error")) };
            }
          } catch (e) {
            state.memoryFeedback = { kind: "err", text: ctx.tr("js.settings.error.config_error") };
          } finally {
            state.pendingBusyId = 0;
            ctx.rerender();
          }
        });
        discardBtn.disabled = !canManageMemory || state.pendingBusyId === it.id;
        discardBtn.dataset.companionPendingDiscard = String(it.id);

        actions.append(approveBtn, discardBtn);
        card.append(meta, ta, actions);
        list.append(card);
      })(item);
    }
    wrap.append(list);
    return wrap;
  }

  function renderMemoryEpisodesTab(ctx, canManageMemory) {
    var wrap = ctx.el("div", "cs-mem-pane");
    wrap.dataset.companionMemoryPane = "episodes";
    wrap.append(ctx.el("p", "cs-mem-hint", ctx.tr("js.settings.companion.memory.episodes_intro")));

    var searchForm = ctx.el("form", "cs-mem-search-row");
    var qInput = ctx.el("input", "inp");
    qInput.type = "search";
    qInput.value = state.episodesQuery;
    qInput.placeholder = ctx.tr("js.settings.companion.memory.episodes_search_placeholder");
    qInput.dataset.companionEpisodesSearch = "1";
    qInput.addEventListener("input", function () { state.episodesQuery = qInput.value; });

    var projSel = ctx.el("select", "sel");
    projSel.dataset.companionEpisodesProject = "1";
    var allOpt = ctx.el("option", "", ctx.tr("js.settings.companion.memory.episodes_all_projects"));
    allOpt.value = "";
    projSel.append(allOpt);
    var projects = (state.episodesData && Array.isArray(state.episodesData.projects)) ? state.episodesData.projects : [];
    for (var p of projects) {
      var opt = ctx.el("option", "", p);
      opt.value = p;
      projSel.append(opt);
    }
    projSel.value = state.episodesProject;
    projSel.addEventListener("change", function () {
      state.episodesProject = projSel.value;
      state.episodesPruneConfirm = false;
      loadEpisodes(ctx.rerender, true);
      ctx.rerender();
    });

    var searchBtn = ctx.button("cs-btn sm soft-pri", ctx.tr("js.settings.companion.memory.episodes_search"));
    searchBtn.type = "submit";
    searchForm.addEventListener("submit", function (ev) {
      ev.preventDefault();
      loadEpisodes(ctx.rerender, true);
      ctx.rerender();
    });
    searchForm.append(qInput, projSel, searchBtn);
    wrap.append(searchForm);

    var episodes = (state.episodesData && Array.isArray(state.episodesData.episodes)) ? state.episodesData.episodes : [];
    if (state.episodesLoading && !state.episodesData) {
      wrap.append(ctx.el("div", "cs-mem-empty", ctx.tr("js.settings.companion.memory.loading")));
      return wrap;
    }

    if (!episodes.length) {
      wrap.append(ctx.el("div", "cs-mem-empty", ctx.tr("js.settings.companion.memory.episodes_empty")));
      return wrap;
    }

    var list = ctx.el("ul", "cs-mem-list");
    list.dataset.companionEpisodesList = "1";
    for (var ep of episodes) {
      (function (item) {
        var li = ctx.el("li", "cs-mem-item");
        li.dataset.companionEpisodeId = String(item.id);
        var top = ctx.el("div", "cs-mem-item-top");
        var meta = ctx.el("div", "cs-mem-item-meta");
        if (item.projectKey) meta.append(ctx.el("span", "cs-pill", item.projectKey));
        for (var tag of (item.tags || [])) {
          meta.append(ctx.el("span", "cs-mem-tag", "#" + tag));
        }
        if (item.createdAt) meta.append(ctx.el("span", "cs-mem-time mono", item.createdAt));
        var delBtn = ctx.button("cs-btn sm danger", ctx.tr("js.settings.companion.memory.episodes_delete"), async function () {
          if (!canManageMemory || state.episodesBusy) return;
          state.episodesBusy = true;
          ctx.rerender();
          try {
            var res = await fetchJson("/api/companion/memory/episodes/prune", {
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify({ id: item.id })
            });
            if (res.ok) {
              loadEpisodes(ctx.rerender, true);
            } else {
              state.memoryFeedback = { kind: "err", text: extractErrorMessage(res, ctx.tr("js.settings.error.config_error")) };
            }
          } catch (e) {
            state.memoryFeedback = { kind: "err", text: ctx.tr("js.settings.error.config_error") };
          } finally {
            state.episodesBusy = false;
            ctx.rerender();
          }
        });
        delBtn.disabled = !canManageMemory || state.episodesBusy;
        delBtn.dataset.companionEpisodeDelete = String(item.id);
        top.append(meta, delBtn);
        li.append(top, ctx.el("div", "cs-mem-item-body", item.summary || ""));
        list.append(li);
      })(ep);
    }
    wrap.append(list);

    // 2-step inline prune action
    var footer = ctx.el("div", "cs-pack-actions");
    if (!state.episodesPruneConfirm) {
      var pruneBtn = ctx.button("cs-btn sm danger", ctx.tr("js.settings.companion.memory.episodes_prune_all"), function () {
        state.episodesPruneConfirm = true;
        ctx.rerender();
      });
      pruneBtn.disabled = !canManageMemory || state.episodesBusy;
      pruneBtn.dataset.companionEpisodesPrune = "1";
      footer.append(pruneBtn);
    } else {
      var confirmBtn = ctx.button("cs-btn sm danger", ctx.tr("js.settings.companion.memory.episodes_prune_confirm"), async function () {
        if (!canManageMemory || state.episodesBusy) return;
        state.episodesBusy = true;
        state.episodesPruneConfirm = false;
        ctx.rerender();
        try {
          var payload = state.episodesProject ? { project: state.episodesProject } : { all: true };
          var res = await fetchJson("/api/companion/memory/episodes/prune", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(payload)
          });
          if (res.ok && res.data) {
            state.memoryFeedback = { kind: "ok", text: ctx.tr("js.settings.companion.memory.episodes_pruned", res.data.deleted || 0) };
            loadEpisodes(ctx.rerender, true);
          } else {
            state.memoryFeedback = { kind: "err", text: extractErrorMessage(res, ctx.tr("js.settings.error.config_error")) };
          }
        } catch (e) {
          state.memoryFeedback = { kind: "err", text: ctx.tr("js.settings.error.config_error") };
        } finally {
          state.episodesBusy = false;
          ctx.rerender();
        }
      });
      confirmBtn.dataset.companionEpisodesPruneConfirm = "1";
      var cancelBtn = ctx.button("cs-btn sm", ctx.tr("js.settings.companion.packs.cancel"), function () {
        state.episodesPruneConfirm = false;
        ctx.rerender();
      });
      footer.append(confirmBtn, cancelBtn);
    }
    wrap.append(footer);
    return wrap;
  }

  function renderMemorySyncTab(ctx, canManageMemory, soulKey) {
    var wrap = ctx.el("div", "cs-mem-pane");
    wrap.dataset.companionMemoryPane = "sync";

    var infoCard = ctx.el("div", "cs-mem-card");
    var dirVal = (state.soulData && (state.soulData.soulDir || state.soulData.defaultSoulDir)) || "~/.ws/companion-soul";
    var machineVal = (state.soulData && state.soulData.machineId) || "—";
    var kv = ctx.el("div", "cs-mem-kv");
    kv.append(
      ctx.append(ctx.el("div", "cs-mem-kv-row"),
        ctx.el("span", "cs-group-sub", ctx.tr("js.settings.companion.memory.sync_dir_label")),
        ctx.el("code", "mono", dirVal)),
      ctx.append(ctx.el("div", "cs-mem-kv-row"),
        ctx.el("span", "cs-group-sub", ctx.tr("js.settings.companion.memory.sync_machine_label")),
        ctx.el("code", "mono", machineVal)));
    infoCard.append(kv, ctx.el("p", "cs-mem-hint", ctx.tr("js.settings.companion.memory.sync_note")));
    wrap.append(infoCard);

    var actionCard = ctx.el("div", "cs-mem-card");
    var actions = ctx.el("div", "cs-pack-actions");

    var exportBtn = ctx.button("cs-btn sm soft-pri",
      state.soulExporting
        ? ctx.tr("js.settings.companion.memory.exporting")
        : ctx.tr("js.settings.companion.memory.export_zip"),
      async function () {
        if (!canManageMemory || state.soulExporting) return;
        state.soulExporting = true;
        state.syncFeedback = null;
        ctx.rerender();
        try {
          var soulId = (state.soulData && state.soulData.soulId) || "default";
          var resp = await fetch("/api/companion/soul/export?soul=" + encodeURIComponent(soulId), {
            method: "POST",
            credentials: "same-origin",
            cache: "no-store"
          });
          if (!resp.ok) {
            var errData = null;
            try { errData = await resp.json(); } catch (e) { /* ignore */ }
            state.syncFeedback = { kind: "err", text: extractErrorMessage({ data: errData }, ctx.tr("js.settings.error.config_error")) };
          } else {
            var blob = await resp.blob();
            var filename = "companion-soul-" + soulId + ".zip";
            var url = URL.createObjectURL(blob);
            var a = document.createElement("a");
            a.href = url;
            a.download = filename;
            document.body.appendChild(a);
            a.click();
            a.remove();
            setTimeout(function () { URL.revokeObjectURL(url); }, 1000);
            state.syncFeedback = { kind: "ok", text: ctx.tr("js.settings.companion.memory.exported", filename) };
          }
        } catch (e) {
          state.syncFeedback = { kind: "err", text: ctx.tr("js.settings.error.config_error") };
        } finally {
          state.soulExporting = false;
          ctx.rerender();
        }
      });
    exportBtn.disabled = !canManageMemory || state.soulExporting;
    exportBtn.dataset.companionSoulExport = "1";

    var importInput = ctx.el("input", "cs-sr-only");
    importInput.type = "file";
    importInput.accept = ".zip,application/zip";
    importInput.tabIndex = -1;
    importInput.dataset.companionSoulImportInput = "1";
    importInput.addEventListener("change", async function () {
      var file = importInput.files && importInput.files[0];
      importInput.value = "";
      if (!file || !canManageMemory || state.soulImporting) return;
      state.soulImporting = true;
      state.syncFeedback = null;
      ctx.rerender();
      try {
        var soulId = (state.soulData && state.soulData.soulId) || "";
        var qs = "overwrite=" + (state.soulImportOverwrite ? "1" : "0") +
          (soulId ? "&soul=" + encodeURIComponent(soulId) : "");
        var res = await fetchJson("/api/companion/soul/import?" + qs, {
          method: "POST",
          headers: { "Content-Type": "application/zip" },
          body: file
        });
        if (res.ok && res.data) {
          state.syncFeedback = {
            kind: "ok",
            text: ctx.tr("js.settings.companion.memory.imported", res.data.soulId || "default", res.data.mergedEntries || 0)
          };
          loadSoulState(soulKey, ctx.rerender, true);
        } else {
          state.syncFeedback = { kind: "err", text: extractErrorMessage(res, ctx.tr("js.settings.error.config_error")) };
        }
      } catch (e) {
        state.syncFeedback = { kind: "err", text: ctx.tr("js.settings.error.config_error") };
      } finally {
        state.soulImporting = false;
        ctx.rerender();
      }
    });

    var importBtn = ctx.button("cs-btn sm pri",
      state.soulImporting
        ? ctx.tr("js.settings.companion.memory.importing")
        : ctx.tr("js.settings.companion.memory.import_zip"),
      function () { importInput.click(); });
    importBtn.disabled = !canManageMemory || state.soulImporting;
    importBtn.dataset.companionSoulImport = "1";

    var owChk = ctx.el("input");
    owChk.type = "checkbox";
    owChk.checked = state.soulImportOverwrite;
    owChk.disabled = !canManageMemory || state.soulImporting;
    owChk.addEventListener("change", function () { state.soulImportOverwrite = owChk.checked; });
    var owLabel = ctx.append(ctx.el("label", "cs-inline-check"), owChk,
      ctx.el("span", "", ctx.tr("js.settings.companion.memory.import_overwrite")));

    actions.append(exportBtn, importBtn, importInput);
    actionCard.append(actions, owLabel);

    if (state.syncFeedback) {
      var sfb = ctx.el("div", "cs-feedback " + state.syncFeedback.kind, state.syncFeedback.text);
      sfb.setAttribute("role", state.syncFeedback.kind === "err" ? "alert" : "status");
      actionCard.append(sfb);
    }

    wrap.append(actionCard);
    return wrap;
  }

  function render(options) {
    var ctx = Object.assign({}, options, { byKey: entryMap(options.entries) });
    ensureCatalog(ctx.rerender);

    var panel = ctx.el("div", "cs-panel");
    panel.dataset.companionSettingsPanel = "1";

    var groups = [
      renderCoreGroup(ctx),
      renderAvatarGroup(ctx),
      renderMemoryGroup(ctx),
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
      state.memoryTab = "soul";
      state.memoryKey = "";
      state.soulData = null;
      state.soulLoading = false;
      state.soulError = "";
      state.personaDraft = "";
      state.profileDraft = "";
      state.personaDirty = false;
      state.profileDirty = false;
      state.soulSavingDoc = "";
      state.memoryFeedback = null;
      state.pendingItems = null;
      state.pendingLoading = false;
      state.pendingDrafts.clear();
      state.pendingBusyId = 0;
      state.episodesData = null;
      state.episodesLoading = false;
      state.episodesQuery = "";
      state.episodesProject = "";
      state.episodesPruneConfirm = false;
      state.episodesBusy = false;
      state.soulExporting = false;
      state.soulImporting = false;
      state.soulImportOverwrite = false;
      state.syncFeedback = null;
      if (window.WsSettingsCompanionAgents) window.WsSettingsCompanionAgents.resetState();
    }
  };
})();
