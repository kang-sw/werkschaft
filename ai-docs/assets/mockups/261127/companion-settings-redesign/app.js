(function () {
  "use strict";

  const api = window.MockCompanionAPI;
  if (!api) return;

  const SIDEBAR_SECTIONS = [
    { id: "workflow", label: "워크플로 · 리뷰", count: 2 },
    { id: "agents", label: "에이전트 · 티어", count: 1 },
    { id: "tier_routing", label: "티어 라우팅", count: 0 },
    { id: "account", label: "계정", count: 0 },
    { id: "compaction", label: "컴팩션 · 워크트리", count: 0 },
    { id: "adapters", label: "하네스 어댑터", count: 1 },
    { id: "companion", label: "컴패니언", count: 0 },
    { id: "profiles", label: "프로필", count: 0 },
    { id: "prompts", label: "프롬프트 오버라이드", count: 0 }
  ];

  const AUTHORING_DOCS = {
    guide: {
      title: "제작 가이드 (/api/companion/authoring/guide.md)",
      body: [
        "# Werkschaft Companion Pack Authoring Guide",
        "",
        "## 3D VRM Pack (ws-companion-vrm/v1)",
        "- 단일 .vrm 파일(VRoid Studio 등)은 설정 화면에 끌어다 놓으면 자동 패키징됩니다.",
        "- 고급 표정 매핑(expressionMap)이나 애니메이션(.vrma)을 포함하려면 pack.json과 함께 .zip으로 묶습니다.",
        "- 최대 파일 크기: .vrm 32 MiB / .vrma 4 MiB / 전체 팩 40 MiB"
      ].join("\n")
    },
    packSchema: {
      title: "pack.json 스키마 (/api/companion/authoring/pack.schema.json)",
      body: JSON.stringify({
        schema: "ws-companion-vrm/v1",
        id: "my-vrm-avatar",
        name: "My VRM Avatar",
        model: "avatar.vrm",
        persona: { name: "Assistant", voice: "Concise status updates." }
      }, null, 2)
    },
    marketplaceSchema: {
      title: "marketplace.json 스키마 (/api/companion/authoring/marketplace.schema.json)",
      body: JSON.stringify({
        schema: "ws-companion-marketplace/v1",
        name: "Example Pack Registry",
        owner: { name: "Maintainer", url: "https://example.invalid" },
        packs: [{ id: "my-vrm-avatar", version: "1.0.0", format: "vrm", path: "packs/my-vrm-avatar" }]
      }, null, 2)
    }
  };

  const ui = {
    activeSection: "companion",
    search: "",
    filter: "all",
    settings: null,
    catalog: null,
    openLayers: new Set(),
    confirmDeletePack: null,
    confirmRemoveSource: null,
    expandedSources: new Map(), // sourceUrl -> listing
    loadingSource: null,
    addSourceDraft: "",
    addSourceError: "",
    autoActivateVrm: true,
    vrmUploading: false,
    vrmProgress: 0,
    vrmFeedback: null, // { type: "ok"|"err", text: string }
    packCheckMessage: new Map(), // packId -> string
    stageActionFeedback: "",
    advancedOpen: false,
    zipPreview: null, // { fileName, listing, selected: Set<string> }
    activeDocKey: null,
    sampleCounter: 1
  };

  const navEl = document.querySelector("[data-settings-sections]");
  const contentEl = document.querySelector("[data-settings-content]");
  const searchEl = document.querySelector("[data-settings-search]");
  const filtersEl = document.querySelector("[data-settings-filters]");
  const countEl = document.querySelector("[data-settings-count]");
  const toastRegion = document.querySelector("[data-toast-region]");

  function el(tag, cls, text) {
    const node = document.createElement(tag);
    if (cls) node.className = cls;
    if (text !== undefined) node.textContent = text;
    return node;
  }

  function icon(id) {
    const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    svg.setAttribute("class", "i");
    svg.setAttribute("aria-hidden", "true");
    const use = document.createElementNS("http://www.w3.org/2000/svg", "use");
    use.setAttribute("href", "#" + id);
    svg.append(use);
    return svg;
  }

  function btn(cls, label, onClick, opts = {}) {
    const b = el("button", cls);
    b.type = "button";
    if (opts.icon) b.append(icon(opts.icon));
    if (label) b.append(el("span", "", label));
    if (opts.disabled) b.disabled = true;
    if (opts.title) b.title = opts.title;
    if (opts.ariaLabel) b.setAttribute("aria-label", opts.ariaLabel);
    if (onClick) b.addEventListener("click", onClick);
    return b;
  }

  function showToast(text, kind = "ok") {
    while (toastRegion.children.length >= 2) {
      toastRegion.firstElementChild.remove();
    }
    const t = el("div", "toast " + kind);
    t.append(icon(kind === "err" ? "i-shield" : "i-check"), el("span", "", text));
    toastRegion.append(t);
    setTimeout(() => {
      t.remove();
    }, 2600);
  }

  function entryMap() {
    const map = new Map();
    for (const e of (ui.settings?.entries || [])) map.set(e.key, e);
    return map;
  }

  function valOf(key) {
    return entryMap().get(key)?.value;
  }

  function isOverridden(key) {
    const e = entryMap().get(key);
    return e && e.source_scope === "global";
  }

  function activePackObj() {
    const selectedId = valOf("dashboard.companion_pack") || "default";
    const packs = ui.catalog?.packs || [];
    if (selectedId === "auto") {
      return packs.find(p => p.renderer === "vrm") || packs[0] || null;
    }
    return packs.find(p => p.id === selectedId) || packs[0] || null;
  }

  function isActiveVrm() {
    const pack = activePackObj();
    return Boolean(pack && pack.renderer === "vrm");
  }

  function hasAnyVrmInstalled() {
    return (ui.catalog?.packs || []).some(p => p.renderer === "vrm");
  }

  function parsePairs(raw) {
    const out = {};
    for (const token of String(raw || "").trim().split(/\s+/).filter(Boolean)) {
      const idx = token.indexOf("=");
      if (idx > 0) out[token.slice(0, idx)] = token.slice(idx + 1);
    }
    return out;
  }

  function composePairs(map) {
    return Object.keys(map)
      .filter(k => map[k] !== "" && map[k] !== undefined)
      .sort()
      .map(k => k + "=" + String(map[k]).toLowerCase())
      .join(" ");
  }

  function parseSourceList() {
    return String(valOf("dashboard.companion_sources") || "")
      .trim()
      .split(/\s+/)
      .filter(Boolean);
  }

  function companionOverrideCount() {
    return (ui.settings?.entries || []).filter(e => e.source_scope === "global").length;
  }

  function matchesSearch(haystack) {
    const q = ui.search.trim().toLowerCase();
    if (!q) return true;
    return haystack.toLowerCase().includes(q);
  }

  function matchesRowFilter(key, editableFlag, searchText) {
    if (!matchesSearch(searchText)) return false;
    if (ui.filter === "overridden" && !isOverridden(key)) return false;
    if (ui.filter === "editable" && !editableFlag) return false;
    return true;
  }

  async function mutateSetting(key, nextValue, feedbackToast) {
    try {
      const res = await api.setConfig(key, nextValue);
      const affected = new Map((res.affected_entries || [res.entry]).map(item => [item.key, item]));
      ui.settings.entries = ui.settings.entries.map(item => affected.get(item.key) || item);
      if (feedbackToast) showToast(feedbackToast, "ok");
      renderAll();
    } catch (err) {
      showToast(err.message || "설정을 저장하지 못했습니다.", "err");
    }
  }

  async function resetSetting(key) {
    try {
      const res = await api.resetConfig(key);
      const affected = new Map((res.affected_entries || [res.entry]).map(item => [item.key, item]));
      ui.settings.entries = ui.settings.entries.map(item => affected.get(item.key) || item);
      showToast("기본값으로 되돌렸습니다.", "ok");
      renderAll();
    } catch (err) {
      showToast(err.message || "초기화에 실패했습니다.", "err");
    }
  }

  /* ========================================================================
     Provenance Pill + Layer Inheritance Drawer
     ======================================================================== */
  function buildProvenance(entry, canEdit) {
    const wrap = el("div", "kprov");
    const overridden = entry.source_scope === "global";
    const expanded = ui.openLayers.has(entry.key);
    const pill = el("button", "origin-pill" + (overridden ? " here" : ""));
    pill.type = "button";
    pill.setAttribute("aria-expanded", String(expanded));
    pill.title = "레이어 상속 경로 보기";
    pill.append(
      el("span", "", overridden ? "전역" : "내장 기본값"),
      el("span", "", expanded ? "▴" : "▾")
    );
    pill.addEventListener("click", () => {
      if (ui.openLayers.has(entry.key)) ui.openLayers.delete(entry.key);
      else ui.openLayers.add(entry.key);
      renderContent();
    });
    wrap.append(pill);

    const resetBtn = el("button", "reset-icon-btn" + (overridden && canEdit ? "" : " placeholder"));
    resetBtn.type = "button";
    resetBtn.title = "오버라이드 해제";
    resetBtn.setAttribute("aria-label", entry.key + " 오버라이드 해제");
    resetBtn.append(icon("i-reset"));
    if (overridden && canEdit) {
      resetBtn.addEventListener("click", () => resetSetting(entry.key));
    } else {
      resetBtn.tabIndex = -1;
      resetBtn.setAttribute("aria-hidden", "true");
    }
    wrap.append(resetBtn);
    return wrap;
  }

  function formatDisplayValue(key, v) {
    if (v === undefined || v === null || v === "") return "(비어 있음)";
    if (v === true) return "켜짐";
    if (v === false) return "꺼짐";
    if (key === "dashboard.companion_director") return v === "agent" ? "에이전트" : "규칙 기반";
    return String(v);
  }

  function buildLayerDrawer(entry) {
    const drawer = el("div", "layer-drawer");
    drawer.hidden = !ui.openLayers.has(entry.key);
    drawer.append(el("span", "layer-drawer-lbl", "레이어 보기"));
    entry.layers.forEach((layer, idx) => {
      if (idx > 0) drawer.append(el("span", "prec-arrow", "→"));
      const isWinner = layer.scope === entry.source_scope;
      const isEmpty = layer.selected === undefined;
      const chip = el("span", "lchip" + (isWinner ? " is-winner" : "") + (isEmpty ? " is-empty" : ""));
      chip.append(
        el("span", "lscope", layer.scope === "global" ? "전역" : "내장 기본값"),
        el("span", "", "·"),
        el("span", "lval", isEmpty ? "미지정" : formatDisplayValue(entry.key, layer.selected))
      );
      if (isWinner) chip.append(el("span", "pill ok", "적용"));
      drawer.append(chip);
    });
    return drawer;
  }

  /* ========================================================================
     Section 1: Core & Director (.sgroup)
     ======================================================================== */
  function renderCoreSection() {
    const map = entryMap();
    const enabledEntry = map.get("dashboard.companion_enabled");
    const directorEntry = map.get("dashboard.companion_director");
    const cooldownEntry = map.get("dashboard.companion_agent_cooldown_seconds");
    const capEntry = map.get("dashboard.companion_agent_hourly_cap");
    const isAgent = directorEntry.value === "agent";

    const group = el("section", "sgroup");
    const head = el("div", "sgroup-head");
    const hLeft = el("div", "sgroup-head-left");
    hLeft.append(icon("i-sliders"), el("h3", "", "기본 설정"), el("span", "sgroup-sub", "표시 여부, 디렉터 모드, 화면 위치"));
    head.append(hLeft);
    if (!enabledEntry.value) {
      head.append(el("span", "pill warn", "비활성화됨 — 설정은 계속 편집 가능"));
    }
    group.append(head);

    if (!enabledEntry.value) {
      const banner = el("div", "disabled-banner");
      banner.append(
        icon("i-info"),
        el("span", "", "컴패니언 표시가 꺼져 있습니다. 아바타 팩, VRM 업로드, 소스 및 외형 설정은 계속 미리 편집할 수 있습니다.")
      );
      group.append(banner);
    }

    let visibleCount = 0;

    // 1) dashboard.companion_enabled
    if (matchesRowFilter(enabledEntry.key, true, "컴패니언 표시 companion_enabled 활성화 토글")) {
      visibleCount++;
      const row = el("article", "srow" + (isOverridden(enabledEntry.key) ? " overridden" : ""));
      const meta = el("div", "kmeta");
      const line = el("div", "klabel-line");
      line.append(el("span", "klabel", "컴패니언 표시"), el("code", "skey", enabledEntry.key));
      meta.append(line, el("p", "kdesc", "대시보드 화면에 컴패니언 캐릭터를 표시합니다. 꺼도 아래 아바타와 소스 설정은 그대로 유지·편집됩니다."));

      const rail = el("div", "sctl-rail");
      const wrap = el("div", "kctl-wrap");
      const ctl = el("div", "ctl-row");
      const tg = el("button", "tg");
      tg.type = "button";
      tg.setAttribute("role", "switch");
      tg.setAttribute("aria-label", "컴패니언 표시");
      tg.setAttribute("aria-checked", String(Boolean(enabledEntry.value)));
      tg.addEventListener("click", () => {
        const next = !enabledEntry.value;
        mutateSetting(enabledEntry.key, next, next ? "컴패니언 표시를 켰습니다." : "컴패니언 표시를 껐습니다. 설정은 계속 편집할 수 있습니다.");
      });
      ctl.append(tg, el("span", "tgl", enabledEntry.value ? "켜짐" : "꺼짐"));
      wrap.append(ctl);
      rail.append(wrap, buildProvenance(enabledEntry, true));
      row.append(meta, rail, buildLayerDrawer(enabledEntry));
      group.append(row);
    }

    // 2) dashboard.companion_director
    if (matchesRowFilter(directorEntry.key, true, "디렉터 모드 companion_director 규칙 기반 에이전트")) {
      visibleCount++;
      const row = el("article", "srow" + (isOverridden(directorEntry.key) ? " overridden" : ""));
      const meta = el("div", "kmeta");
      const line = el("div", "klabel-line");
      line.append(el("span", "klabel", "디렉터 모드"), el("code", "skey", directorEntry.key));
      meta.append(line, el("p", "kdesc", "컴패니언의 반응 방식을 선택합니다. 규칙 기반(내장 이벤트 반응) 또는 별도의 경량 에이전트를 사용합니다."));

      const rail = el("div", "sctl-rail");
      const wrap = el("div", "kctl-wrap");
      const kctl = el("div", "kctl");
      const seg = el("div", "seg");
      seg.setAttribute("role", "group");
      seg.setAttribute("aria-label", "디렉터 모드");
      [
        ["rules", "규칙 기반"],
        ["agent", "에이전트"]
      ].forEach(([val, lbl]) => {
        const b = el("button", "", lbl);
        b.type = "button";
        b.setAttribute("aria-pressed", String(directorEntry.value === val));
        b.addEventListener("click", () => {
          if (directorEntry.value === val) return;
          mutateSetting(directorEntry.key, val, "디렉터 모드를 '" + lbl + "'(으)로 전환했습니다.");
        });
        seg.append(b);
      });
      kctl.append(seg);
      wrap.append(kctl);
      rail.append(wrap, buildProvenance(directorEntry, true));
      row.append(meta, rail, buildLayerDrawer(directorEntry));
      group.append(row);
    }

    // 3) dashboard.companion_agent_cooldown_seconds (coupled via enabled_when: director === "agent")
    if (matchesRowFilter(cooldownEntry.key, isAgent, "에이전트 쿨다운 초 cooldown agent_cooldown_seconds")) {
      visibleCount++;
      group.append(buildCoupledNumberRow({
        entry: cooldownEntry,
        label: "에이전트 쿨다운",
        desc: "에이전트 디렉터를 연속 호출하는 사이의 최소 간격입니다. (10~3600초)",
        unit: "초",
        enabled: isAgent
      }));
    }

    // 4) dashboard.companion_agent_hourly_cap (coupled via enabled_when: director === "agent")
    if (matchesRowFilter(capEntry.key, isAgent, "시간당 호출 상한 한도 hourly_cap agent_hourly_cap")) {
      visibleCount++;
      group.append(buildCoupledNumberRow({
        entry: capEntry,
        label: "시간당 호출 상한",
        desc: "최근 1시간 동안 에이전트 디렉터를 호출할 수 있는 최대 횟수입니다. (1~120회)",
        unit: "회/시간",
        enabled: isAgent
      }));
    }

    // 5) Reset size / Return home action row
    if (ui.filter !== "overridden" && matchesSearch("크기 위치 초기화 reset size return home 기본 위치")) {
      visibleCount++;
      const row = el("article", "srow");
      const meta = el("div", "kmeta");
      const line = el("div", "klabel-line");
      line.append(el("span", "klabel", "크기 · 위치 초기화"));
      meta.append(line, el("p", "kdesc", "대시보드에서 드래그·리사이즈한 컴패니언의 크기나 위치를 즉시 기본 상태로 되돌립니다."));
      if (ui.stageActionFeedback) {
        meta.append(el("span", "hint ok", ui.stageActionFeedback));
      }

      const rail = el("div", "sctl-rail");
      const actions = el("div", "action-pair");
      actions.append(
        btn("btn sm", "크기 초기화", async () => {
          const res = await api.stageAction("reset_size");
          ui.stageActionFeedback = res.message;
          showToast(res.message, "ok");
          renderContent();
        }, { icon: "i-reset" }),
        btn("btn sm", "기본 위치로 복귀", async () => {
          const res = await api.stageAction("return_home");
          ui.stageActionFeedback = res.message;
          showToast(res.message, "ok");
          renderContent();
        }, { icon: "i-home" })
      );
      rail.append(actions);
      row.append(meta, rail);
      group.append(row);
    }

    return visibleCount > 0 ? group : null;
  }

  function buildCoupledNumberRow({ entry, label, desc, unit, enabled }) {
    const row = el("article", "srow coupled-sub" + (isOverridden(entry.key) ? " overridden" : "") + (enabled ? "" : " disabled"));
    const meta = el("div", "kmeta");
    const line = el("div", "klabel-line");
    line.append(el("span", "klabel", label), el("code", "skey", entry.key));
    if (!enabled) {
      line.append(el("span", "pill", "에이전트 모드에서 활성화"));
    }
    meta.append(line, el("p", "kdesc", desc));

    const rail = el("div", "sctl-rail");
    const wrap = el("div", "kctl-wrap");
    const form = el("form", "kctl");
    const input = el("input", "inp mono");
    input.type = "number";
    input.step = "1";
    input.min = String(entry.minimum);
    input.max = String(entry.maximum);
    input.value = String(entry.value);
    input.disabled = !enabled;
    input.setAttribute("aria-label", label);

    const saveBtn = el("button", "btn pri sm", "저장");
    saveBtn.type = "submit";
    saveBtn.hidden = true;

    const hint = el("span", "hint");
    input.addEventListener("input", () => {
      const dirty = String(input.value) !== String(entry.value);
      saveBtn.hidden = !dirty;
      hint.textContent = "";
      hint.className = "hint";
    });
    form.addEventListener("submit", async ev => {
      ev.preventDefault();
      const num = Number(input.value);
      if (input.value === "" || !Number.isSafeInteger(num) || num < entry.minimum || num > entry.maximum) {
        hint.className = "hint err";
        hint.textContent = entry.minimum + "~" + entry.maximum + " 사이 정수를 입력하세요.";
        return;
      }
      await mutateSetting(entry.key, num, label + "을(를) " + num + unit + "(으)로 저장했습니다.");
    });

    form.append(input, el("span", "unit", unit), saveBtn);
    wrap.append(form, hint);
    rail.append(wrap, buildProvenance(entry, enabled));
    row.append(meta, rail, buildLayerDrawer(entry));
    return row;
  }

  /* ========================================================================
     Section 2: Avatar & VRM Management (VRM-First Hero Section)
     ======================================================================== */
  function buildAvatarSvg(isVrm) {
    const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    svg.setAttribute("viewBox", "0 0 48 48");
    svg.setAttribute("fill", "none");
    svg.setAttribute("aria-hidden", "true");
    if (isVrm) {
      svg.innerHTML =
        '<circle cx="24" cy="16.5" r="7.5" stroke="currentColor" stroke-width="2.2"/>' +
        '<path d="M12.5 37.5c1.5-6.2 6-9.5 11.5-9.5s10 3.3 11.5 9.5" stroke="currentColor" stroke-width="2.2" stroke-linecap="round"/>' +
        '<path d="M8 12.5 14 9m26 3.5L34 9M8 35.5l4-2m28 2-4-2" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" opacity=".5"/>';
    } else {
      svg.innerHTML =
        '<rect x="12" y="9" width="24" height="30" rx="6" stroke="currentColor" stroke-width="2.2"/>' +
        '<circle cx="20" cy="20" r="1.8" fill="currentColor"/>' +
        '<circle cx="28" cy="20" r="1.8" fill="currentColor"/>' +
        '<path d="M20 27.5c1.2 1.6 6.8 1.6 8 0" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>';
    }
    return svg;
  }

  async function handleVrmImport(fileObj) {
    if (ui.vrmUploading) return;
    ui.vrmUploading = true;
    ui.vrmProgress = 25;
    ui.vrmFeedback = null;
    renderContent();

    const tick = setTimeout(() => {
      if (ui.vrmUploading) {
        ui.vrmProgress = 72;
        const fill = contentEl.querySelector("[data-vrm-progress]");
        if (fill) fill.style.width = "72%";
      }
    }, 90);

    try {
      const res = await api.importVrm({
        filename: fileObj.name,
        name: fileObj.displayName || "",
        sizeBytes: fileObj.size || 3800000,
        activate: ui.autoActivateVrm
      });
      clearTimeout(tick);
      ui.catalog = res.catalog;
      ui.settings = api.getSettingsSync();
      ui.vrmProgress = 100;
      ui.vrmUploading = false;
      ui.vrmFeedback = {
        type: "ok",
        text: res.activated
          ? "VRM 팩 '" + res.name + "'을(를) 설치하고 바로 적용했습니다."
          : "VRM 팩 '" + res.name + "'을(를) 설치했습니다."
      };
      showToast(ui.vrmFeedback.text, "ok");
      renderAll();
    } catch (err) {
      clearTimeout(tick);
      ui.vrmUploading = false;
      ui.vrmProgress = 0;
      ui.vrmFeedback = { type: "err", text: err.message || "VRM 파일 가져오기에 실패했습니다." };
      showToast(ui.vrmFeedback.text, "err");
      renderContent();
    }
  }

  function renderAvatarSection() {
    const packEntry = entryMap().get("dashboard.companion_pack");
    const activePack = activePackObj();
    const isVrm = isActiveVrm();
    const isAutoMode = packEntry.value === "auto";
    const packs = ui.catalog?.packs || [];

    if (!matchesSearch("아바타 vrm 팩 드래그 업로드 공식 다운로드 설치된 팩 페르소나 companion_pack " + packs.map(p => p.name + " " + p.id).join(" "))) {
      return null;
    }
    if (ui.filter === "overridden" && !isOverridden("dashboard.companion_pack")) {
      return null;
    }

    const group = el("section", "sgroup");
    const head = el("div", "sgroup-head");
    const hLeft = el("div", "sgroup-head-left");
    hLeft.append(
      icon("i-cube"),
      el("h3", "", "아바타 · VRM 관리"),
      el("code", "skey", "dashboard.companion_pack"),
      el("span", "pill vrm", "3D VRM 우선")
    );
    head.append(hLeft, buildProvenance(packEntry, true));
    group.append(head);

    if (ui.openLayers.has(packEntry.key)) {
      const drawerWrap = el("div", "");
      drawerWrap.style.padding = "0 18px 12px";
      drawerWrap.append(buildLayerDrawer(packEntry));
      group.append(drawerWrap);
    }

    const body = el("div", "sgroup-body");

    // 1. Active Avatar Summary Card
    const hero = el("div", "avatar-hero");
    const emblem = el("div", "avatar-emblem" + (isVrm ? "" : " is-2d"));
    emblem.append(buildAvatarSvg(isVrm));

    const info = el("div", "avatar-info");
    const titleRow = el("div", "avatar-title-row");
    titleRow.append(
      el("span", "avatar-name", activePack ? activePack.name : "2D 기본"),
      el("span", "pill " + (isVrm ? "vrm" : ""), isVrm ? "3D VRM" : "2D 기본"),
      el("span", "pill ok", isAutoMode ? "자동 선택(auto)" : "현재 적용 중")
    );
    if (activePack?.version) {
      titleRow.append(el("span", "skey", activePack.id + " · v" + activePack.version));
    }
    info.append(titleRow);

    const personaBox = el("div", "avatar-persona");
    if (activePack?.persona) {
      personaBox.append(
        el("span", "avatar-persona-name", "페르소나 · " + activePack.persona.name),
        el("span", "avatar-persona-voice", activePack.persona.voice)
      );
    } else {
      personaBox.append(el("span", "avatar-persona-voice", "이 팩에는 페르소나가 없습니다."));
    }
    info.append(personaBox);

    const heroRight = el("div", "avatar-hero-right");
    const autoBtn = btn(
      "btn sm " + (isAutoMode ? "soft-pri" : "ghost"),
      isAutoMode ? "세션별 자동 선택 켜짐" : "세션별 자동 선택(auto)",
      () => {
        if (isAutoMode) {
          mutateSetting("dashboard.companion_pack", activePack ? activePack.id : "default", "현재 아바타로 고정했습니다.");
        } else {
          mutateSetting("dashboard.companion_pack", "auto", "세션별 자동 팩 선택(auto)을 적용했습니다.");
        }
      },
      { icon: "i-spark" }
    );
    heroRight.append(autoBtn);

    hero.append(emblem, info, heroRight);
    body.append(hero);

    // 2. Official 3D VRM Download Banner (only when no VRM pack installed yet)
    if (!hasAnyVrmInstalled()) {
      const banner = el("div", "official-vrm-banner");
      const copy = el("div", "official-vrm-copy");
      const bHead = el("div", "official-vrm-head");
      bHead.append(el("strong", "", "공식 3D VRM 캐릭터"), el("span", "pill vrm", "3D VRM"), el("span", "pill official", "공식"));
      copy.append(
        bHead,
        el("p", "", "아직 설치된 3D VRM 아바타가 없습니다. 공식 팩 레지스트리에서 스타터 3D VRM 캐릭터를 선택적으로 다운로드해 바로 적용할 수 있습니다.")
      );
      const installBtn = btn("btn pri", "Official VRM Starter 다운로드", async () => {
        installBtn.disabled = true;
        try {
          const res = await api.installFromSource(api.OFFICIAL_SOURCE, ["official-vrm-starter"], { activateIfVrm: true });
          ui.catalog = res.catalog;
          ui.settings = api.getSettingsSync();
          showToast("Official VRM Starter를 설치하고 바로 적용했습니다.", "ok");
          renderAll();
        } catch (err) {
          showToast(err.message || "다운로드에 실패했습니다.", "err");
          renderContent();
        }
      }, { icon: "i-download" });
      banner.append(copy, installBtn);
      body.append(banner);
    }

    // 3. Borderless Tonal .vrm Drag-and-Drop Zone (NO 1px dashed, NO raw <input type="file">)
    const dropzone = el("div", "vrm-dropzone");
    dropzone.setAttribute("data-drag", "");

    const hiddenFile = el("input", "sr-only");
    hiddenFile.type = "file";
    hiddenFile.accept = ".vrm,model/gltf-binary";
    hiddenFile.setAttribute("aria-label", "가져올 .vrm 파일 선택");
    hiddenFile.addEventListener("change", () => {
      const file = hiddenFile.files?.[0];
      if (file) handleVrmImport(file);
      hiddenFile.value = "";
    });

    const dropMain = el("div", "vrm-drop-main");
    const dropIcon = el("div", "vrm-drop-icon");
    dropIcon.append(icon("i-upload"));
    const dropCopy = el("div", "vrm-drop-copy");
    dropCopy.append(
      el("strong", "", ".vrm 파일 끌어다 놓기 또는 선택"),
      el("p", "", "VRoid Studio 등에서 내보낸 단일 .vrm 파일(최대 32 MiB)을 끌어다 놓으면 자동으로 컴패니언 팩으로 변환해 설치합니다.")
    );
    const dropActions = el("div", "vrm-drop-actions");
    dropActions.append(
      btn("btn pri sm", ".vrm 파일 선택", () => hiddenFile.click(), { icon: "i-upload", disabled: ui.vrmUploading }),
      btn("btn soft-pri sm", "샘플 .vrm 드롭 시뮬레이션", () => {
        const idx = ui.sampleCounter++;
        handleVrmImport({
          name: "custom-vrm-avatar-" + idx + ".vrm",
          displayName: "Custom VRM Avatar " + idx,
          size: 4600000
        });
      }, { disabled: ui.vrmUploading })
    );
    dropMain.append(dropIcon, dropCopy, dropActions, hiddenFile);
    dropzone.append(dropMain);

    const dropFoot = el("div", "vrm-drop-foot");
    const checkLbl = el("label", "inline-check");
    const checkBox = el("input");
    checkBox.type = "checkbox";
    checkBox.checked = ui.autoActivateVrm;
    checkBox.addEventListener("change", () => {
      ui.autoActivateVrm = checkBox.checked;
    });
    checkLbl.append(checkBox, el("span", "", "가져온 뒤 바로 적용"));
    dropFoot.append(checkLbl, el("span", "", "지원 형식: 단일 .vrm (VRM 0.x / 1.0 · 최대 32 MiB)"));
    dropzone.append(dropFoot);

    if (ui.vrmUploading) {
      const track = el("div", "progress-track");
      const fill = el("div", "progress-fill");
      fill.setAttribute("data-vrm-progress", "1");
      fill.style.width = ui.vrmProgress + "%";
      track.append(fill);
      dropzone.append(track);
    }

    if (ui.vrmFeedback) {
      const fb = el("div", "inline-feedback " + ui.vrmFeedback.type);
      fb.setAttribute("role", ui.vrmFeedback.type === "err" ? "alert" : "status");
      fb.append(icon(ui.vrmFeedback.type === "err" ? "i-shield" : "i-check"), el("span", "", ui.vrmFeedback.text));
      dropzone.append(fb);
    }

    dropzone.addEventListener("dragover", ev => {
      ev.preventDefault();
      if (!ui.vrmUploading) dropzone.setAttribute("data-drag", "over");
    });
    dropzone.addEventListener("dragleave", ev => {
      if (!dropzone.contains(ev.relatedTarget)) dropzone.setAttribute("data-drag", "");
    });
    dropzone.addEventListener("drop", ev => {
      ev.preventDefault();
      dropzone.setAttribute("data-drag", "");
      const file = ev.dataTransfer?.files?.[0];
      if (file) handleVrmImport(file);
    });

    body.append(dropzone);

    // 4. Installed Packs List
    const listHead = el("div", "sub-heading");
    listHead.append(el("h4", "", "설치된 팩 목록"), el("span", "", "총 " + packs.length + "개"));
    body.append(listHead);

    const list = el("ul", "pack-list");
    for (const pack of packs) {
      const isPackActive = !isAutoMode && packEntry.value === pack.id;
      const isPackVrm = pack.renderer === "vrm";
      const li = el("li", "pack-row" + (isPackActive ? " is-active" : ""));

      const main = el("div", "pack-row-main");
      const title = el("div", "pack-row-title");
      title.append(
        el("strong", "", pack.name),
        el("span", "pill " + (isPackVrm ? "vrm" : ""), isPackVrm ? "3D VRM" : "2D")
      );
      if (pack.origin?.official) title.append(el("span", "pill official", "공식"));
      if (pack.builtin) title.append(el("span", "pill", "내장"));
      if (pack.updateAvailable) title.append(el("span", "pill warn", "업데이트 있음"));
      if (isPackActive) title.append(el("span", "pill ok", "사용 중"));

      const originLabel = pack.builtin
        ? "내장 기본 팩"
        : pack.origin?.official
          ? "공식 팩에서 설치됨"
          : pack.origin?.kind === "local"
            ? "로컬 파일 " + pack.origin.source
            : (pack.origin?.source || "설치됨");
      const metaParts = [pack.id, pack.version ? "v" + pack.version : "", originLabel].filter(Boolean);
      main.append(title, el("span", "pack-row-meta", metaParts.join(" · ")));

      const checkMsg = ui.packCheckMessage.get(pack.id);
      if (checkMsg) {
        main.append(el("span", "hint ok", checkMsg));
      }

      const actions = el("div", "pack-row-actions");
      if (!isPackActive) {
        actions.append(btn("btn soft-pri sm", "적용", () => {
          ui.confirmDeletePack = null;
          mutateSetting("dashboard.companion_pack", pack.id, "'" + pack.name + "' 팩을 적용했습니다.");
        }));
      }

      if (pack.origin && pack.origin.kind !== "local") {
        const src = pack.origin.official ? api.OFFICIAL_SOURCE : pack.origin.source;
        if (pack.updateAvailable) {
          actions.append(btn("btn pri sm", "업데이트", async () => {
            const res = await api.installFromSource(src, [pack.id], { activateIfVrm: false });
            ui.catalog = res.catalog;
            ui.packCheckMessage.set(pack.id, "최신 버전으로 업데이트했습니다.");
            if (ui.expandedSources.has(src)) ui.expandedSources.set(src, res.listing);
            showToast("'" + pack.name + "' 팩을 업데이트했습니다.", "ok");
            renderAll();
          }, { icon: "i-refresh" }));
        } else {
          actions.append(btn("btn sm", "업데이트 확인", async () => {
            const res = await api.fetchSource(src);
            const found = res.listing.packs.find(item => item.id === pack.id);
            if (found && found.status === "update") {
              ui.packCheckMessage.set(pack.id, "새 버전(v" + found.version + ")이 있습니다.");
            } else {
              ui.packCheckMessage.set(pack.id, "이미 최신 버전입니다.");
            }
            showToast(ui.packCheckMessage.get(pack.id), "ok");
            renderContent();
          }, { icon: "i-refresh" }));
        }
      }

      if (!pack.builtin) {
        if (ui.confirmDeletePack === pack.id) {
          const confirmWrap = el("span", "confirm-inline");
          confirmWrap.append(
            el("span", "", "삭제할까요?"),
            btn("btn danger-solid sm", "확인", async () => {
              try {
                const res = await api.uninstallPack(pack.id);
                ui.confirmDeletePack = null;
                ui.catalog = res.catalog;
                ui.settings = api.getSettingsSync();
                // Sync any open source listings
                for (const srcKey of ui.expandedSources.keys()) {
                  const fresh = await api.fetchSource(srcKey);
                  ui.expandedSources.set(srcKey, fresh.listing);
                }
                showToast("'" + pack.name + "' 팩을 삭제했습니다.", "ok");
                renderAll();
              } catch (err) {
                showToast(err.message || "삭제에 실패했습니다.", "err");
              }
            }),
            btn("btn ghost sm", "취소", () => {
              ui.confirmDeletePack = null;
              renderContent();
            })
          );
          actions.append(confirmWrap);
        } else {
          actions.append(btn("btn danger sm", "삭제", () => {
            ui.confirmDeletePack = pack.id;
            renderContent();
          }, { icon: "i-trash" }));
        }
      }

      li.append(main, actions);
      list.append(li);
    }

    body.append(list);
    group.append(body);
    return group;
  }

  /* ========================================================================
     Section 3: Pack Sources & Store (.sgroup)
     ======================================================================== */
  function renderSourcesSection() {
    const srcEntry = entryMap().get("dashboard.companion_sources");
    const customUrls = parseSourceList();

    if (!matchesSearch("팩 소스 스토어 공식 레지스트리 companion_sources url 카탈로그 " + customUrls.join(" "))) {
      return null;
    }
    if (ui.filter === "overridden" && !isOverridden("dashboard.companion_sources")) {
      return null;
    }

    const group = el("section", "sgroup");
    const head = el("div", "sgroup-head");
    const hLeft = el("div", "sgroup-head-left");
    hLeft.append(
      icon("i-globe"),
      el("h3", "", "팩 소스 및 스토어"),
      el("code", "skey", "dashboard.companion_sources")
    );
    head.append(hLeft, buildProvenance(srcEntry, true));
    group.append(head);

    if (ui.openLayers.has(srcEntry.key)) {
      const drawerWrap = el("div", "");
      drawerWrap.style.padding = "0 18px 12px";
      drawerWrap.append(buildLayerDrawer(srcEntry));
      group.append(drawerWrap);
    }

    const body = el("div", "sgroup-body");

    // Single calm third-party trust notice (NOT repeated per source/card)
    const notice = el("div", "trust-notice");
    notice.append(
      icon("i-shield"),
      el("span", "", "Werkschaft는 제3자 팩을 검토하지 않습니다. 신뢰하는 소스의 팩만 설치하세요. 소스는 직접 카탈로그를 열 때만 요청됩니다.")
    );
    body.append(notice);

    // Inline Add Source URL Bar with live origin preview
    const addBox = el("div", "source-add-box");
    const addRow = el("form", "source-add-row");
    const addInput = el("input", "inp mono");
    addInput.type = "text";
    addInput.placeholder = "https://example.invalid/packs.git 또는 .zip URL";
    addInput.value = ui.addSourceDraft;
    addInput.setAttribute("aria-label", "추가할 팩 소스 URL");

    const originPreview = el("div", "source-origin-preview");
    function updateOriginPreview(v) {
      const trimmed = v.trim();
      originPreview.textContent = trimmed ? "소스 미리보기: " + trimmed : "";
    }
    updateOriginPreview(ui.addSourceDraft);

    addInput.addEventListener("input", () => {
      ui.addSourceDraft = addInput.value;
      ui.addSourceError = "";
      updateOriginPreview(addInput.value);
      const errNode = addBox.querySelector("[data-add-source-err]");
      if (errNode) errNode.textContent = "";
    });

    addRow.addEventListener("submit", async ev => {
      ev.preventDefault();
      const url = addInput.value.trim();
      if (!url) return;
      const err = api.validateSourceUrl(url);
      if (err) {
        ui.addSourceError = err;
        renderContent();
        return;
      }
      if (customUrls.includes(url)) {
        ui.addSourceError = "이미 등록된 소스입니다.";
        renderContent();
        return;
      }
      if (customUrls.length >= api.MAX_SOURCES) {
        ui.addSourceError = "소스는 최대 16개까지 등록할 수 있습니다.";
        renderContent();
        return;
      }
      ui.addSourceDraft = "";
      ui.addSourceError = "";
      await mutateSetting("dashboard.companion_sources", [...customUrls, url].join(" "), "새 팩 소스를 추가했습니다.");
    });

    const addBtn = el("button", "btn pri", "");
    addBtn.type = "submit";
    addBtn.append(icon("i-plus"), el("span", "", "소스 추가"));
    addRow.append(addInput, addBtn);
    addBox.append(addRow, originPreview);
    const errEl = el("span", "hint err", ui.addSourceError);
    errEl.setAttribute("data-add-source-err", "1");
    addBox.append(errEl);
    body.append(addBox);

    // Sources List (Official + Custom URLs)
    const list = el("ul", "source-list");
    list.append(buildSourceItem({
      source: api.OFFICIAL_SOURCE,
      label: "공식 팩 레지스트리",
      official: true
    }));
    customUrls.forEach(url => {
      list.append(buildSourceItem({
        source: url,
        label: url,
        official: false
      }));
    });
    body.append(list);

    group.append(body);
    return group;
  }

  function buildSourceItem({ source, label, official }) {
    const li = el("li", "source-item");
    const isOpen = ui.expandedSources.has(source);
    const isLoading = ui.loadingSource === source;

    const head = el("div", "source-item-head");
    const info = el("div", "source-item-info");
    const title = el("div", "source-item-title");
    if (official) {
      title.append(el("strong", "", label), el("span", "pill official", "공식"), el("span", "pill", "기본 내장"));
      info.append(title, el("span", "pack-row-meta", "Werkschaft 공식 VRM 및 2D 팩 카탈로그"));
    } else {
      title.append(el("span", "source-url", label), el("span", "pill", "사용자 소스"));
      info.append(title);
    }

    const actions = el("div", "pack-row-actions");
    actions.append(btn(
      "btn sm " + (isOpen ? "soft-pri" : ""),
      isLoading ? "불러오는 중…" : isOpen ? "카탈로그 닫기" : "카탈로그 보기",
      async () => {
        if (isOpen) {
          ui.expandedSources.delete(source);
          renderContent();
          return;
        }
        ui.loadingSource = source;
        renderContent();
        try {
          const res = await api.fetchSource(source);
          ui.expandedSources.set(source, res.listing);
        } catch (err) {
          showToast(err.message || "소스를 불러오지 못했습니다.", "err");
        } finally {
          ui.loadingSource = null;
          renderContent();
        }
      },
      { disabled: isLoading }
    ));

    if (isOpen) {
      actions.append(btn("btn ghost sm", "새로고침", async () => {
        ui.loadingSource = source;
        renderContent();
        const res = await api.fetchSource(source);
        ui.expandedSources.set(source, res.listing);
        ui.loadingSource = null;
        showToast("카탈로그를 새로고침했습니다.", "ok");
        renderContent();
      }, { icon: "i-refresh", disabled: isLoading }));
    }

    if (!official) {
      if (ui.confirmRemoveSource === source) {
        const confirmWrap = el("span", "confirm-inline");
        confirmWrap.append(
          el("span", "", "제거할까요?"),
          btn("btn danger-solid sm", "확인", async () => {
            const next = parseSourceList().filter(u => u !== source);
            ui.confirmRemoveSource = null;
            ui.expandedSources.delete(source);
            await mutateSetting("dashboard.companion_sources", next.join(" "), "소스를 제거했습니다.");
          }),
          btn("btn ghost sm", "취소", () => {
            ui.confirmRemoveSource = null;
            renderContent();
          })
        );
        actions.append(confirmWrap);
      } else {
        actions.append(btn("btn danger sm", "제거", () => {
          ui.confirmRemoveSource = source;
          renderContent();
        }, { icon: "i-trash" }));
      }
    }

    head.append(info, actions);
    li.append(head);

    if (isOpen) {
      const listing = ui.expandedSources.get(source);
      const cat = el("div", "source-catalog");
      const meta = el("div", "catalog-meta");
      meta.append(
        el("span", "", (listing.name || source) + (listing.owner?.name ? " · 제작: " + listing.owner.name : "")),
        el("span", "skey", listing.commit ? "commit " + listing.commit.slice(0, 10) : listing.kind)
      );
      cat.append(meta);

      const grid = el("div", "catalog-grid");
      for (const pack of listing.packs) {
        const card = el("article", "catalog-card");
        const cHead = el("div", "catalog-card-head");
        cHead.append(
          el("strong", "", pack.name),
          el("span", "pill " + (pack.format === "vrm" ? "vrm" : ""), pack.format === "vrm" ? "3D VRM" : "2D")
        );
        if (pack.status === "installed") cHead.append(el("span", "pill ok", "설치됨"));
        else if (pack.status === "update") cHead.append(el("span", "pill warn", "업데이트 있음"));
        else if (pack.status === "builtin") cHead.append(el("span", "pill", "내장"));
        else if (pack.status === "other") cHead.append(el("span", "pill warn", "다른 소스"));

        card.append(
          cHead,
          el("div", "catalog-card-meta", pack.id + " · v" + pack.version + (pack.license ? " · " + pack.license : "")),
          el("p", "catalog-card-desc", pack.description || "")
        );

        const foot = el("div", "catalog-card-foot");
        foot.append(el("span", "catalog-card-meta", pack.author || ""));
        if (pack.status === "available" || pack.status === "update" || pack.status === "other") {
          const actionLabel = pack.status === "update" ? "업데이트" : pack.status === "other" ? "교체" : "설치";
          const actionCls = pack.status === "update" ? "btn pri sm" : "btn soft-pri sm";
          foot.append(btn(actionCls, actionLabel, async () => {
            const res = await api.installFromSource(source, [pack.id], { activateIfVrm: true });
            ui.catalog = res.catalog;
            ui.settings = api.getSettingsSync();
            ui.expandedSources.set(source, res.listing);
            showToast("'" + pack.name + "' 팩을 " + (pack.status === "update" ? "업데이트" : "설치") + "했습니다.", "ok");
            renderAll();
          }));
        } else if (pack.status === "installed") {
          foot.append(btn("btn ghost sm", "설치됨", null, { disabled: true, icon: "i-check" }));
        }
        card.append(foot);
        grid.append(card);
      }
      cat.append(grid);
      li.append(cat);
    }

    return li;
  }

  /* ========================================================================
     Section 4: Contextual Legacy 2D Customization (.sgroup)
     ======================================================================== */
  function render2DWardrobeSection() {
    const vrmActive = isActiveVrm();
    const map = entryMap();
    const wardrobeKeys = [
      "dashboard.companion_preset",
      "dashboard.companion_outfit",
      "dashboard.companion_hair",
      "dashboard.companion_eyes",
      "dashboard.companion_attachments",
      "dashboard.companion_palette"
    ];

    if (!matchesSearch("2d 외형 커스터마이징 프리셋 의상 머리 눈 액세서리 팔레트 색상 초기화 preset outfit hair eyes attachments palette")) {
      return null;
    }
    if (ui.filter === "overridden" && !wardrobeKeys.some(isOverridden)) {
      return null;
    }

    // When a 3D VRM pack is active: hide 2D controls and show one concise muted info note
    if (vrmActive) {
      const note = el("div", "info-note");
      const left = el("div", "info-note-left");
      left.append(
        icon("i-info"),
        el("span", "", "현재 3D VRM 아바타가 활성화되어 2D 외형 커스터마이징은 숨겨져 있습니다. 3D 아바타 외형 변경은 VRoid Studio 등에서 편집한 .vrm 파일을 위 영역으로 가져와 적용하세요.")
      );
      note.append(
        left,
        btn("btn sm", "2D 기본으로 전환해 편집", () => {
          mutateSetting("dashboard.companion_pack", "default", "2D 기본 팩으로 전환했습니다. 2D 외형 커스터마이징을 편집할 수 있습니다.");
        })
      );
      return note;
    }

    const activePack = activePackObj();
    const wardrobe = activePack?.wardrobe || {
      presets: ["werkschaft-d1", "werkschaft-d2", "minimal-desk"],
      outfits: ["hoodie-dark", "work-shirt", "apron-craft"],
      hairs: ["short-neat", "bob-cut", "ponytail"],
      eyesets: ["calm-round", "focused-sharp", "cheerful"],
      slots: { headwear: ["cap-plain", "headphones"], glasses: ["round-frame", "half-rim"] },
      channels: ["skin", "hair", "outfit", "accent"],
      defaultPalette: { skin: "#f2d2bd", hair: "#3c4354", outfit: "#3f5b8f", accent: "#7aa2ff" }
    };

    const group = el("section", "sgroup");
    const head = el("div", "sgroup-head");
    const hLeft = el("div", "sgroup-head-left");
    hLeft.append(
      icon("i-palette"),
      el("h3", "", "2D 외형 커스터마이징"),
      el("span", "pill", activePack?.name || "2D 기본")
    );
    head.append(hLeft, el("span", "sgroup-sub", "2D 팩 활성 시 표시되는 의상·파츠·팔레트 설정"));
    group.append(head);

    const body = el("div", "sgroup-body");
    const grid = el("div", "wardrobe-grid");

    // Preset / Outfit / Hair / Eyes selects
    const selectDefs = [
      { key: "dashboard.companion_preset", label: "프리셋", options: wardrobe.presets, allowFollow: false },
      { key: "dashboard.companion_outfit", label: "의상", options: wardrobe.outfits, allowFollow: true },
      { key: "dashboard.companion_hair", label: "머리", options: wardrobe.hairs, allowFollow: true },
      { key: "dashboard.companion_eyes", label: "눈", options: wardrobe.eyesets, allowFollow: true }
    ];

    for (const def of selectDefs) {
      const entry = map.get(def.key);
      const card = el("div", "wardrobe-field");
      const fHead = el("div", "wardrobe-field-head");
      const lbl = el("label", "", def.label);
      const id = "w-" + def.key.replace(/\./g, "-");
      lbl.htmlFor = id;
      fHead.append(lbl, el("code", "skey", def.key));
      if (isOverridden(def.key)) {
        fHead.append(btn("btn ghost sm", "초기화", () => resetSetting(def.key)));
      }

      const sel = el("select", "sel");
      sel.id = id;
      if (def.allowFollow) {
        sel.append(new Option("프리셋 따르기", "", false, entry.value === ""));
      }
      for (const opt of def.options) {
        sel.append(new Option(opt, opt, false, entry.value === opt));
      }
      sel.addEventListener("change", () => {
        mutateSetting(def.key, sel.value, def.label + " 설정을 업데이트했습니다.");
      });
      card.append(fHead, sel);
      grid.append(card);
    }

    // Attachments (per-slot selects)
    {
      const attKey = "dashboard.companion_attachments";
      const stored = parsePairs(map.get(attKey).value);
      const card = el("div", "wardrobe-field full");
      const fHead = el("div", "wardrobe-field-head");
      fHead.append(el("span", "wlbl", "액세서리 슬롯"), el("code", "skey", attKey));
      if (isOverridden(attKey)) {
        fHead.append(btn("btn ghost sm", "모두 프리셋 따르기", () => resetSetting(attKey)));
      }
      const slotGrid = el("div", "slot-grid");
      for (const [slotName, choices] of Object.entries(wardrobe.slots)) {
        const slotWrap = el("label", "slot-item");
        slotWrap.append(el("span", "", slotName));
        const sel = el("select", "sel");
        const cur = stored[slotName] || "";
        sel.append(new Option("프리셋 따르기", "", false, cur === ""));
        sel.append(new Option("없음 (비움)", "none", false, cur === "none"));
        for (const c of choices) {
          sel.append(new Option(c, c, false, cur === c));
        }
        sel.addEventListener("change", () => {
          const next = { ...stored };
          if (sel.value === "") delete next[slotName];
          else next[slotName] = sel.value;
          mutateSetting(attKey, composePairs(next), "액세서리(" + slotName + ")를 변경했습니다.");
        });
        slotWrap.append(sel);
        slotGrid.append(slotWrap);
      }
      card.append(fHead, slotGrid);
      grid.append(card);
    }

    // Palette (color swatches + reset colors button)
    {
      const palKey = "dashboard.companion_palette";
      const stored = parsePairs(map.get(palKey).value);
      const card = el("div", "wardrobe-field full");
      const fHead = el("div", "wardrobe-field-head");
      const leftHead = el("div", "klabel-line");
      leftHead.append(el("span", "wlbl", "팔레트 색상"), el("code", "skey", palKey));
      fHead.append(
        leftHead,
        btn("btn sm", "색상 초기화", () => resetSetting(palKey), {
          icon: "i-reset",
          disabled: !isOverridden(palKey)
        })
      );

      const bar = el("div", "palette-bar");
      for (const ch of wardrobe.channels) {
        const hex = stored[ch] || wardrobe.defaultPalette[ch] || "#7aa2ff";
        const chip = el("label", "swatch-chip");
        const colorInp = el("input");
        colorInp.type = "color";
        colorInp.value = hex;
        colorInp.setAttribute("aria-label", ch + " 색상");
        colorInp.addEventListener("change", () => {
          const next = { ...stored, [ch]: colorInp.value.toLowerCase() };
          mutateSetting(palKey, composePairs(next), ch + " 팔레트 색상을 변경했습니다.");
        });
        const meta = el("span", "swatch-meta");
        meta.append(el("span", "swatch-name", ch), el("span", "swatch-hex", hex));
        chip.append(colorInp, meta);
        bar.append(chip);
      }
      card.append(fHead, bar);
      grid.append(card);
    }

    body.append(grid);
    group.append(body);
    return group;
  }

  /* ========================================================================
     Section 5: Advanced & Pack Authoring (Borderless Tonal Collapsible)
     ======================================================================== */
  function renderAdvancedSection() {
    if (ui.filter === "overridden") return null;
    if (!matchesSearch("고급 팩 제작자 zip 번들 가져오기 스키마 제작 가이드 authoring pack.json marketplace.json")) {
      return null;
    }

    const group = el("section", "sgroup");
    const trigger = el("button", "disclosure-trigger");
    trigger.type = "button";
    trigger.setAttribute("aria-expanded", String(ui.advancedOpen));

    const tLeft = el("div", "sgroup-head-left");
    tLeft.append(
      icon("i-archive"),
      el("h3", "", "고급 · 팩 제작자"),
      el("span", "sgroup-sub", ".zip 마켓플레이스 번들 가져오기 및 제작 스키마")
    );
    const chev = icon("i-chev");
    chev.classList.add("chev");
    trigger.append(tLeft, chev);
    trigger.addEventListener("click", () => {
      ui.advancedOpen = !ui.advancedOpen;
      renderContent();
    });
    group.append(trigger);

    if (!ui.advancedOpen) return group;

    const body = el("div", "sgroup-body");
    const grid = el("div", "adv-grid");

    // Card A: .zip bundle import (NO raw file input)
    const zipCard = el("div", "adv-card");
    zipCard.append(
      el("h4", "", ".zip 번들 가져오기"),
      el("p", "", "로컬 마켓플레이스 .zip 아카이브를 검사한 뒤 원하는 팩만 선택해 설치합니다. 단일 .vrm 파일은 위 아바타 영역을 이용하세요.")
    );
    const hiddenZip = el("input", "sr-only");
    hiddenZip.type = "file";
    hiddenZip.accept = ".zip,application/zip";
    hiddenZip.setAttribute("aria-label", "가져올 .zip 번들 선택");
    hiddenZip.addEventListener("change", async () => {
      const f = hiddenZip.files?.[0];
      if (f) await runZipValidate(f.name);
      hiddenZip.value = "";
    });

    const zipActions = el("div", "vrm-drop-actions");
    zipActions.append(
      btn("btn sm", ".zip 파일 선택", () => hiddenZip.click(), { icon: "i-archive" }),
      btn("btn soft-pri sm", "샘플 .zip 검사 시뮬레이션", () => runZipValidate("sample-pack-bundle.zip")),
      hiddenZip
    );
    zipCard.append(zipActions);
    grid.append(zipCard);

    // Card B: Authoring Docs & Schemas
    const docCard = el("div", "adv-card");
    docCard.append(
      el("h4", "", "나만의 팩 만들기"),
      el("p", "", "커스텀 VRM 표정 매핑, VRMA 애니메이션 또는 2D 팩 매니페스트 작성용 문서와 JSON 스키마입니다.")
    );
    const links = el("div", "doc-links");
    [
      ["guide", "제작 가이드"],
      ["packSchema", "pack.json 스키마"],
      ["marketplaceSchema", "marketplace.json 스키마"]
    ].forEach(([key, label]) => {
      const b = el("button", "doc-link-btn");
      b.type = "button";
      b.append(icon("i-book"), el("span", "", label));
      b.addEventListener("click", () => {
        ui.activeDocKey = ui.activeDocKey === key ? null : key;
        renderContent();
      });
      links.append(b);
    });
    docCard.append(links);
    grid.append(docCard);
    body.append(grid);

    // Inline Zip Validate Preview
    if (ui.zipPreview) {
      const p = ui.zipPreview;
      const box = el("div", "zip-preview");
      const pHead = el("div", "sub-heading");
      pHead.append(el("h4", "", "가져오기 미리보기 · " + p.fileName), el("span", "", p.listing.packs.length + "개 팩 발견"));
      box.append(pHead);

      const pList = el("ul", "zip-preview-list");
      for (const item of p.listing.packs) {
        const li = el("li", "zip-preview-row");
        const chk = el("label", "inline-check");
        const input = el("input");
        input.type = "checkbox";
        input.checked = p.selected.has(item.id);
        input.addEventListener("change", () => {
          if (input.checked) p.selected.add(item.id);
          else p.selected.delete(item.id);
          renderContent();
        });
        chk.append(
          input,
          el("span", "", item.name),
          el("span", "pill " + (item.format === "vrm" ? "vrm" : ""), item.format === "vrm" ? "3D VRM" : "2D")
        );
        li.append(chk, el("span", "pack-row-meta", item.id + " · v" + item.version));
        pList.append(li);
      }
      box.append(pList);

      const pActions = el("div", "pack-row-actions");
      pActions.append(
        btn("btn pri sm", "선택 항목 가져오기 (" + p.selected.size + ")", async () => {
          const res = await api.importZip(p.fileName, Array.from(p.selected));
          ui.catalog = res.catalog;
          ui.zipPreview = null;
          showToast("번들에서 " + res.writtenCount + "개 팩을 가져왔습니다.", "ok");
          renderAll();
        }, { disabled: p.selected.size === 0 }),
        btn("btn ghost sm", "닫기", () => {
          ui.zipPreview = null;
          renderContent();
        })
      );
      box.append(pActions);
      body.append(box);
    }

    // Inline Authoring Doc / Schema Preview
    if (ui.activeDocKey && AUTHORING_DOCS[ui.activeDocKey]) {
      const doc = AUTHORING_DOCS[ui.activeDocKey];
      const box = el("div", "schema-preview");
      const dHead = el("div", "schema-preview-head");
      dHead.append(
        el("strong", "", doc.title),
        btn("btn ghost sm", "닫기", () => {
          ui.activeDocKey = null;
          renderContent();
        })
      );
      const pre = el("pre", "", doc.body);
      pre.style.margin = "0";
      box.append(dHead, pre);
      body.append(box);
    }

    group.append(body);
    return group;
  }

  async function runZipValidate(fileName) {
    try {
      const res = await api.validateZip(fileName);
      const selected = new Set(res.listing.packs.filter(p => p.status !== "installed").map(p => p.id));
      ui.zipPreview = { fileName, listing: res.listing, selected };
      renderContent();
    } catch (err) {
      showToast(err.message || "번들 검사에 실패했습니다.", "err");
    }
  }

  /* ========================================================================
     Sidebar & Full View Rendering
     ======================================================================== */
  function renderSidebar() {
    navEl.replaceChildren();
    const compOv = companionOverrideCount();
    for (const sec of SIDEBAR_SECTIONS) {
      const count = sec.id === "companion" ? compOv : sec.count;
      const b = el("button", "snav-btn");
      b.type = "button";
      b.setAttribute("aria-current", String(ui.activeSection === sec.id));
      b.append(
        el("span", "dot" + (count > 0 ? "" : " none")),
        el("span", "", sec.label),
        el("span", "cnt", count > 0 ? String(count) : "")
      );
      b.addEventListener("click", () => {
        ui.activeSection = sec.id;
        renderAll();
      });
      navEl.append(b);
      if (ui.activeSection === sec.id) {
        requestAnimationFrame(() => {
          if (typeof b.scrollIntoView === "function") {
            b.scrollIntoView({ inline: "center", block: "nearest" });
          }
        });
      }
    }
  }

  function renderCounter() {
    const ov = companionOverrideCount();
    const total = (ui.settings?.entries || []).length;
    countEl.textContent = "컴패니언 설정 " + total + "개 · 전역 오버라이드 " + ov + "개";
  }

  function renderContent() {
    contentEl.replaceChildren();

    if (ui.activeSection !== "companion") {
      const sec = SIDEBAR_SECTIONS.find(s => s.id === ui.activeSection);
      const ph = el("div", "placeholder-section");
      ph.append(
        el("h3", "", (sec?.label || ui.activeSection) + " 섹션"),
        el("p", "", "이 목업은 대시보드 설정에 통합된 '컴패니언' 섹션의 레이아웃과 상호작용을 시연합니다."),
        btn("btn pri", "컴패니언 설정으로 돌아가기", () => {
          ui.activeSection = "companion";
          renderAll();
        })
      );
      contentEl.append(ph);
      return;
    }

    // Section Header
    const secHead = el("div", "sec-head");
    const left = el("div", "");
    const titleLine = el("div", "sec-head-main");
    titleLine.append(
      el("h2", "", "컴패니언"),
      el("span", "pill " + (isActiveVrm() ? "vrm" : ""), isActiveVrm() ? "현재 3D VRM 활성" : "현재 2D 활성")
    );
    left.append(titleLine, el("p", "", "컴패니언 아바타(.vrm), 반응 디렉터, 팩 소스 및 2D 외형을 한곳에서 관리합니다."));
    secHead.append(left);
    contentEl.append(secHead);

    const blocks = [
      renderCoreSection(),
      renderAvatarSection(),
      renderSourcesSection(),
      render2DWardrobeSection(),
      renderAdvancedSection()
    ].filter(Boolean);

    if (blocks.length === 0) {
      const empty = el("div", "placeholder-section");
      empty.append(
        el("h3", "", "일치하는 설정이 없습니다"),
        el("p", "", "검색어 또는 필터 조건을 초기화하면 모든 컴패니언 설정을 볼 수 있습니다."),
        btn("btn sm", "필터 초기화", () => {
          ui.search = "";
          ui.filter = "all";
          searchEl.value = "";
          syncFilterButtons();
          renderContent();
        })
      );
      contentEl.append(empty);
      return;
    }

    contentEl.append(...blocks);
  }

  function syncFilterButtons() {
    filtersEl.querySelectorAll("[data-filter]").forEach(b => {
      b.setAttribute("aria-pressed", String(b.dataset.filter === ui.filter));
    });
  }

  function renderAll() {
    renderSidebar();
    renderCounter();
    renderContent();
  }

  /* ========================================================================
     Wire Global Controls (Search, Filters, Preview Scenario & Theme)
     ======================================================================== */
  searchEl.addEventListener("input", () => {
    ui.search = searchEl.value;
    renderContent();
  });

  filtersEl.addEventListener("click", ev => {
    const b = ev.target.closest("[data-filter]");
    if (!b) return;
    ui.filter = b.dataset.filter;
    syncFilterButtons();
    renderContent();
  });

  document.querySelector("[data-preview-scenario]").addEventListener("click", ev => {
    const b = ev.target.closest("[data-scenario]");
    if (!b) return;
    const scenario = b.dataset.scenario;
    document.querySelectorAll("[data-scenario]").forEach(btnEl => {
      btnEl.setAttribute("aria-pressed", String(btnEl === b));
    });
    const loaded = api.loadPreset(scenario);
    ui.settings = loaded.settings;
    ui.catalog = loaded.catalog;
    ui.activeSection = "companion";
    ui.openLayers.clear();
    ui.expandedSources.clear();
    ui.confirmDeletePack = null;
    ui.confirmRemoveSource = null;
    ui.vrmFeedback = null;
    ui.packCheckMessage.clear();
    ui.stageActionFeedback = "";
    ui.zipPreview = null;
    ui.activeDocKey = null;
    showToast(scenario === "fresh-no-vrm" ? "초기 상태(VRM 미설치 · 2D 기본)로 전환했습니다." : "VRM 설치됨 상태로 전환했습니다.", "ok");
    renderAll();
  });

  document.querySelector("[data-preview-theme]").addEventListener("click", ev => {
    const b = ev.target.closest("[data-theme-choice]");
    if (!b) return;
    const theme = b.dataset.themeChoice;
    document.documentElement.setAttribute("data-theme", theme);
    document.querySelectorAll("[data-theme-choice]").forEach(btnEl => {
      btnEl.setAttribute("aria-pressed", String(btnEl === b));
    });
  });

  // Boot
  Promise.all([api.getSettings(), api.getPacks()]).then(([settings, catalog]) => {
    ui.settings = settings;
    ui.catalog = catalog;
    renderAll();
  });
})();
