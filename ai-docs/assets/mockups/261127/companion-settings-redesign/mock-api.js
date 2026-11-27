(function (global) {
  "use strict";

  // Portable mock API adapter mirroring Werkschaft's real Dashboard Settings
  // and Companion backend contracts:
  //   GET  /api/settings
  //   POST /api/config/set
  //   POST /api/config/reset
  //   GET  /api/companion/packs
  //   POST /api/companion/packs/import-vrm
  //   POST /api/companion/packs/{id}/uninstall
  //   POST /api/companion/sources/fetch
  //   POST /api/companion/sources/install
  //   POST /api/companion/packs/validate
  //   POST /api/companion/packs/import
  //   POST /api/companion/action  (UI stage reset_size / return_home)

  const OFFICIAL_SOURCE = "official";
  const MAX_SOURCES = 16;
  const MAX_URL = 2048;
  const MAX_VRM_BYTES = 32 * 1024 * 1024;

  const DEFAULTS = {
    "dashboard.companion_enabled": false,
    "dashboard.companion_director": "rules",
    "dashboard.companion_pack": "default",
    "dashboard.companion_preset": "werkschaft-d1",
    "dashboard.companion_outfit": "",
    "dashboard.companion_hair": "",
    "dashboard.companion_eyes": "",
    "dashboard.companion_attachments": "",
    "dashboard.companion_palette": "",
    "dashboard.companion_agent_cooldown_seconds": 30,
    "dashboard.companion_agent_hourly_cap": 20,
    "dashboard.companion_sources": ""
  };

  const SCHEMA_META = {
    "dashboard.companion_enabled": { type: "boolean", scopeOnly: "global" },
    "dashboard.companion_director": { type: "enum", enum: ["rules", "agent"], scopeOnly: "global" },
    "dashboard.companion_pack": { type: "string", scopeOnly: "global" },
    "dashboard.companion_preset": { type: "string", scopeOnly: "global" },
    "dashboard.companion_outfit": { type: "string", scopeOnly: "global" },
    "dashboard.companion_hair": { type: "string", scopeOnly: "global" },
    "dashboard.companion_eyes": { type: "string", scopeOnly: "global" },
    "dashboard.companion_attachments": { type: "string", scopeOnly: "global" },
    "dashboard.companion_palette": { type: "string", scopeOnly: "global" },
    "dashboard.companion_agent_cooldown_seconds": { type: "integer", minimum: 10, maximum: 3600, scopeOnly: "global" },
    "dashboard.companion_agent_hourly_cap": { type: "integer", minimum: 1, maximum: 120, scopeOnly: "global" },
    "dashboard.companion_sources": { type: "string", scopeOnly: "global" }
  };

  const DEFAULT_2D_WARDROBE = {
    presets: ["werkschaft-d1", "werkschaft-d2", "minimal-desk"],
    outfits: ["hoodie-dark", "work-shirt", "apron-craft"],
    hairs: ["short-neat", "bob-cut", "ponytail"],
    eyesets: ["calm-round", "focused-sharp", "cheerful"],
    slots: {
      headwear: ["cap-plain", "headphones", "beret"],
      glasses: ["round-frame", "half-rim", "visor"],
      badge: ["pin-star", "lanyard"]
    },
    channels: ["skin", "hair", "outfit", "accent"],
    defaultPalette: {
      skin: "#f2d2bd",
      hair: "#3c4354",
      outfit: "#3f5b8f",
      accent: "#7aa2ff"
    }
  };

  const BUILTIN_PACK = {
    id: "default",
    name: "2D 기본",
    renderer: "2d",
    schema: "ws-companion/v1",
    rev: "builtin-v1",
    version: "1.0.0",
    builtin: true,
    persona: {
      name: "기본 안내자",
      voice: "차분하고 간결하게 현재 세션 상태, 빌드 결과, 작업 흐름을 옆에서 안내합니다."
    },
    wardrobe: DEFAULT_2D_WARDROBE,
    origin: null
  };

  const OFFICIAL_VRM_PACK = {
    id: "official-vrm-starter",
    name: "Official VRM Starter",
    renderer: "vrm",
    schema: "ws-companion-vrm/v1",
    rev: "vrm-120",
    version: "1.2.0",
    persona: {
      name: "스타터 어시스턴트",
      voice: "세션 진행과 리뷰 상태에 맞춰 자연스러운 표정과 제스처로 조용히 반응하는 기본 3D VRM 컴패니언입니다."
    },
    origin: {
      source: OFFICIAL_SOURCE,
      kind: "zip",
      official: true,
      version: "1.2.0"
    }
  };

  const CUSTOM_VRM_PACK = {
    id: "custom-avatar-a",
    name: "Custom Avatar A",
    renderer: "vrm",
    schema: "ws-companion-vrm/v1",
    rev: "local-100",
    version: "1.0.0",
    persona: {
      name: "커스텀 아바타 A",
      voice: "VRoid Studio에서 내보낸 로컬 .vrm 파일로 등록된 개인 3D 아바타입니다."
    },
    origin: {
      source: "custom-avatar-a.vrm",
      kind: "local",
      official: false,
      version: "1.0.0"
    }
  };

  const COMMUNITY_SOURCE_URL = "https://packs.example.invalid/community-catalog.git";

  const SOURCE_REGISTRIES = {
    [OFFICIAL_SOURCE]: {
      source: OFFICIAL_SOURCE,
      kind: "zip",
      official: true,
      commit: "9e4a11c8b02f",
      name: "Werkschaft 공식 팩 레지스트리",
      owner: { name: "Werkschaft", url: "https://werkschaft.example.invalid/packs" },
      packs: [
        {
          id: "default",
          name: "2D 기본",
          version: "1.0.0",
          format: "2d",
          author: "Werkschaft",
          license: "MIT",
          description: "대시보드에 기본 포함된 경량 2D 페이퍼돌 컴패니언입니다.",
          builtin: true
        },
        {
          id: "official-vrm-starter",
          name: "Official VRM Starter",
          version: "1.2.0",
          format: "vrm",
          author: "Werkschaft",
          license: "CC0-1.0",
          description: "표준 VRM 1.0 표정·시선 추적을 지원하는 공식 3D 스타터 아바타입니다.",
          persona: OFFICIAL_VRM_PACK.persona
        },
        {
          id: "official-vrm-studio-b",
          name: "Official VRM Studio B",
          version: "1.0.4",
          format: "vrm",
          author: "Werkschaft",
          license: "CC0-1.0",
          description: "차분한 스튜디오 톤의 보조 공식 3D VRM 아바타 팩입니다.",
          persona: {
            name: "스튜디오 가이드 B",
            voice: "긴 디버깅이나 테스트 실행 중에도 흔들림 없이 핵심 알림만 전달합니다."
          }
        },
        {
          id: "official-2d-classic",
          name: "Classic 2D Desk Pack",
          version: "2.1.0",
          format: "2d",
          author: "Werkschaft",
          license: "MIT",
          description: "추가 의상과 액세서리 슬롯을 제공하는 공식 2D 확장 팩입니다.",
          persona: {
            name: "클래식 2D 파트너",
            voice: "가볍고 명쾌한 2D 표현으로 세션 이벤트를 알려줍니다."
          }
        }
      ]
    },
    [COMMUNITY_SOURCE_URL]: {
      source: COMMUNITY_SOURCE_URL,
      kind: "git",
      official: false,
      commit: "4d7f881e2c90",
      name: "Community Sample Catalog",
      owner: { name: "Community Maintainers", url: "https://packs.example.invalid" },
      packs: [
        {
          id: "community-vrm-helper",
          name: "Community VRM Helper",
          version: "0.9.3",
          format: "vrm",
          author: "Sample Author",
          license: "MIT",
          description: "로컬 작업 공간에 어울리는 경량 3D VRM 샘플 아바타입니다.",
          persona: {
            name: "커뮤니티 헬퍼",
            voice: "짧은 작업 단위를 응원하고 커밋 타이밍을 부드럽게 환기합니다."
          }
        },
        {
          id: "community-2d-minimal",
          name: "Minimal 2D Companion",
          version: "1.1.0",
          format: "2d",
          author: "Sample Author",
          license: "MIT",
          description: "단순한 실루엣과 커스텀 팔레트 채널을 제공하는 2D 팩입니다.",
          persona: {
            name: "미니멀 컴패니언",
            voice: "불필요한 장식 없이 필요한 상태만 표시합니다."
          }
        }
      ]
    }
  };

  function clone(obj) {
    return JSON.parse(JSON.stringify(obj));
  }

  function delay(ms) {
    return new Promise(resolve => setTimeout(resolve, ms));
  }

  function validateSourceUrl(url) {
    if (url.length > MAX_URL) return "URL이 2048자를 넘습니다";
    if (/[\u0000-\u001f\u007f-\u009f\s]/.test(url)) return "절대 https URL이 아닙니다";
    const match = /^https:\/\/([^/?#]*)/i.exec(url);
    if (!match) return "절대 https URL이 아닙니다";
    const at = match[1].lastIndexOf("@");
    if (!match[1].slice(at + 1)) return "절대 https URL이 아닙니다";
    if (at >= 0) return "URL에 사용자 이름이나 비밀번호를 넣을 수 없습니다";
    return "";
  }

  function slugifyPackId(rawName, existingIds) {
    const base = String(rawName || "custom-vrm")
      .replace(/\.vrm$/i, "")
      .toLowerCase()
      .replace(/[^a-z0-9_-]+/g, "-")
      .replace(/^[^a-z0-9]+/, "")
      .replace(/-+/g, "-")
      .replace(/-$/, "")
      .slice(0, 48) || "custom-vrm";
    if (!existingIds.has(base)) return base;
    let i = 2;
    while (existingIds.has(base + "-" + i)) i++;
    return base + "-" + i;
  }

  function createMockApi() {
    let state = null;

    function buildPreset(name) {
      if (name === "fresh-no-vrm") {
        return {
          presetName: "fresh-no-vrm",
          overrides: {
            "dashboard.companion_enabled": true
          },
          packs: [clone(BUILTIN_PACK)],
          outdatedIds: new Set()
        };
      }
      // Default: "vrm-installed"
      const starterWithUpdate = clone(OFFICIAL_VRM_PACK);
      starterWithUpdate.version = "1.1.0";
      starterWithUpdate.origin.version = "1.1.0";
      return {
        presetName: "vrm-installed",
        overrides: {
          "dashboard.companion_enabled": true,
          "dashboard.companion_director": "rules",
          "dashboard.companion_pack": "official-vrm-starter",
          "dashboard.companion_sources": COMMUNITY_SOURCE_URL
        },
        packs: [clone(BUILTIN_PACK), starterWithUpdate, clone(CUSTOM_VRM_PACK)],
        outdatedIds: new Set(["official-vrm-starter"])
      };
    }

    state = buildPreset("vrm-installed");

    function effectiveValue(key) {
      return Object.prototype.hasOwnProperty.call(state.overrides, key)
        ? state.overrides[key]
        : DEFAULTS[key];
    }

    function buildEntry(key) {
      const hasOverride = Object.prototype.hasOwnProperty.call(state.overrides, key);
      const val = hasOverride ? state.overrides[key] : DEFAULTS[key];
      const meta = SCHEMA_META[key] || { type: "string" };
      return {
        key,
        type: meta.type,
        enum: meta.enum ? meta.enum.slice() : undefined,
        minimum: meta.minimum,
        maximum: meta.maximum,
        default: DEFAULTS[key],
        value: val,
        source_scope: hasOverride ? "global" : "builtin",
        editable: true,
        layers: [
          { scope: "builtin", value: DEFAULTS[key], selected: DEFAULTS[key], status: "ok" },
          { scope: "global", value: hasOverride ? state.overrides[key] : undefined, selected: hasOverride ? state.overrides[key] : undefined, status: hasOverride ? "ok" : "unset" }
        ]
      };
    }

    function buildCatalog() {
      const activeId = effectiveValue("dashboard.companion_pack");
      return {
        activeId,
        packs: state.packs.map(p => ({
          ...clone(p),
          updateAvailable: state.outdatedIds.has(p.id)
        }))
      };
    }

    function resolveRegistry(source) {
      if (SOURCE_REGISTRIES[source]) return clone(SOURCE_REGISTRIES[source]);
      // Synthetic catalog for any newly added user https:// URL
      let host = source;
      try { host = new URL(source).hostname; } catch (_) {}
      return {
        source,
        kind: source.endsWith(".zip") ? "zip" : "git",
        official: false,
        commit: "a1c90f32e7b4",
        name: "Custom Source (" + host + ")",
        owner: { name: host, url: source },
        packs: [
          {
            id: "ext-vrm-" + host.replace(/[^a-z0-9]/gi, "").slice(0, 8).toLowerCase(),
            name: "Custom VRM Pack (" + host + ")",
            version: "1.0.0",
            format: "vrm",
            author: host,
            license: "MIT",
            description: "추가된 사용자 소스에서 불러온 샘플 3D VRM 팩입니다.",
            persona: {
              name: "외부 VRM 아바타",
              voice: "사용자 정의 소스에서 설치된 3D VRM 캐릭터입니다."
            }
          }
        ]
      };
    }

    function buildListing(source) {
      const reg = resolveRegistry(source);
      const byId = new Map(state.packs.map(p => [p.id, p]));
      const packs = reg.packs.map(item => {
        const installed = byId.get(item.id);
        let status = "available";
        if (item.builtin) {
          status = "builtin";
        } else if (installed) {
          const sameOrigin = installed.origin &&
            ((installed.origin.official && reg.official) || installed.origin.source === source);
          if (!sameOrigin && installed.origin) {
            status = "other";
          } else if (installed.version !== item.version || state.outdatedIds.has(item.id)) {
            status = "update";
          } else {
            status = "installed";
          }
        }
        return { ...item, status };
      });
      return {
        source: reg.source,
        kind: reg.kind,
        official: reg.official,
        commit: reg.commit,
        name: reg.name,
        owner: reg.owner,
        packs,
        errors: []
      };
    }

    return {
      OFFICIAL_SOURCE,
      MAX_SOURCES,
      DEFAULTS: clone(DEFAULTS),
      validateSourceUrl,

      loadPreset(name) {
        state = buildPreset(name);
        return {
          settings: this.getSettingsSync(),
          catalog: buildCatalog()
        };
      },

      getPresetName() {
        return state.presetName;
      },

      getSettingsSync() {
        const entries = Object.keys(DEFAULTS).map(buildEntry);
        return {
          scope: "global",
          path: "~/.ws/config.json",
          entries
        };
      },

      async getSettings() {
        await delay(40);
        return this.getSettingsSync();
      },

      async setConfig(key, value) {
        await delay(60);
        if (!Object.prototype.hasOwnProperty.call(DEFAULTS, key)) {
          throw new Error("알 수 없는 설정 키입니다: " + key);
        }
        const meta = SCHEMA_META[key];
        if (meta.type === "integer") {
          const num = Number(value);
          if (!Number.isSafeInteger(num) || num < meta.minimum || num > meta.maximum) {
            throw new Error(meta.minimum + "에서 " + meta.maximum + " 사이의 정수를 입력하세요.");
          }
          value = num;
        }
        if (value === DEFAULTS[key]) {
          delete state.overrides[key];
        } else {
          state.overrides[key] = value;
        }
        const entry = buildEntry(key);
        return { entry, affected_entries: [entry] };
      },

      async resetConfig(key) {
        await delay(50);
        delete state.overrides[key];
        const entry = buildEntry(key);
        return { entry, affected_entries: [entry] };
      },

      async getPacks() {
        await delay(40);
        return buildCatalog();
      },

      async importVrm({ name, filename, sizeBytes = 4200000, activate = true }) {
        await delay(260);
        const raw = String(filename || name || "");
        if (!/\.vrm$/i.test(raw)) {
          throw new Error(".vrm 파일을 선택하세요.");
        }
        if (sizeBytes > MAX_VRM_BYTES) {
          throw new Error("파일 크기가 32 MiB 제한을 초과합니다.");
        }
        const existingIds = new Set(state.packs.map(p => p.id));
        const id = slugifyPackId(raw, existingIds);
        const displayName = (name && name.trim()) || raw.replace(/\.vrm$/i, "");
        const newPack = {
          id,
          name: displayName,
          renderer: "vrm",
          schema: "ws-companion-vrm/v1",
          rev: "local-" + Date.now().toString(36),
          version: "1.0.0",
          persona: {
            name: displayName,
            voice: "로컬 .vrm 파일(" + raw + ")에서 가져온 3D VRM 아바타입니다."
          },
          origin: {
            source: raw,
            kind: "local",
            official: false,
            version: "1.0.0"
          }
        };
        state.packs.push(newPack);
        if (activate) {
          state.overrides["dashboard.companion_pack"] = id;
        }
        return {
          id: newPack.id,
          name: newPack.name,
          renderer: "vrm",
          schema: "ws-companion-vrm/v1",
          activated: Boolean(activate),
          catalog: buildCatalog()
        };
      },

      async uninstallPack(id) {
        await delay(120);
        if (id === "default") {
          throw new Error("내장 기본 팩은 제거할 수 없습니다.");
        }
        const idx = state.packs.findIndex(p => p.id === id);
        if (idx < 0) {
          throw new Error("설치된 팩을 찾을 수 없습니다.");
        }
        const [removed] = state.packs.splice(idx, 1);
        state.outdatedIds.delete(id);
        if (effectiveValue("dashboard.companion_pack") === id) {
          delete state.overrides["dashboard.companion_pack"];
        }
        return {
          removed,
          catalog: buildCatalog()
        };
      },

      async fetchSource(source) {
        await delay(180);
        const listing = buildListing(source);
        return { listing };
      },

      async installFromSource(source, ids, { activateIfVrm = true } = {}) {
        await delay(240);
        const reg = resolveRegistry(source);
        const results = [];
        for (const id of ids) {
          const spec = reg.packs.find(p => p.id === id);
          if (!spec || spec.builtin) continue;
          const packObj = {
            id: spec.id,
            name: spec.name,
            renderer: spec.format === "vrm" ? "vrm" : "2d",
            schema: spec.format === "vrm" ? "ws-companion-vrm/v1" : "ws-companion/v1",
            rev: "inst-" + spec.version,
            version: spec.version,
            persona: spec.persona || {
              name: spec.name,
              voice: spec.description || ""
            },
            wardrobe: spec.format === "2d" ? DEFAULT_2D_WARDROBE : undefined,
            origin: {
              source: reg.official ? OFFICIAL_SOURCE : source,
              kind: reg.kind,
              official: reg.official,
              version: spec.version
            }
          };
          const existingIdx = state.packs.findIndex(p => p.id === id);
          if (existingIdx >= 0) {
            state.packs[existingIdx] = packObj;
          } else {
            state.packs.push(packObj);
          }
          state.outdatedIds.delete(id);
          if (activateIfVrm && spec.format === "vrm") {
            state.overrides["dashboard.companion_pack"] = id;
          }
          results.push(packObj);
        }
        return {
          installed: results,
          listing: buildListing(source),
          catalog: buildCatalog()
        };
      },

      async validateZip(fileName) {
        await delay(200);
        const clean = String(fileName || "sample-bundle.zip");
        if (!/\.zip$/i.test(clean)) {
          throw new Error(".zip 아카이브 파일만 검사할 수 있습니다.");
        }
        const byId = new Map(state.packs.map(p => [p.id, p]));
        const samplePacks = [
          {
            id: "bundle-vrm-sample",
            name: "Bundle VRM Sample",
            version: "1.0.0",
            format: "vrm",
            author: "Local Author",
            license: "MIT",
            description: "로컬 마켓플레이스 .zip 번들에 포함된 3D VRM 테스트 팩입니다.",
            status: byId.has("bundle-vrm-sample") ? "installed" : "available"
          },
          {
            id: "bundle-2d-sample",
            name: "Bundle 2D Sample",
            version: "1.0.0",
            format: "2d",
            author: "Local Author",
            license: "MIT",
            description: "커스텀 의상·팔레트 구성을 테스트하기 위한 2D 번들 팩입니다.",
            status: byId.has("bundle-2d-sample") ? "installed" : "available"
          }
        ];
        return {
          listing: {
            source: clean,
            kind: "local",
            official: false,
            name: clean,
            packs: samplePacks,
            errors: []
          }
        };
      },

      async importZip(fileName, selectedIds) {
        await delay(220);
        const validated = await this.validateZip(fileName);
        const written = [];
        for (const item of validated.listing.packs) {
          if (!selectedIds.includes(item.id)) continue;
          const packObj = {
            id: item.id,
            name: item.name,
            renderer: item.format === "vrm" ? "vrm" : "2d",
            schema: item.format === "vrm" ? "ws-companion-vrm/v1" : "ws-companion/v1",
            rev: "zip-" + item.version,
            version: item.version,
            persona: {
              name: item.name,
              voice: item.description
            },
            wardrobe: item.format === "2d" ? DEFAULT_2D_WARDROBE : undefined,
            origin: {
              source: fileName,
              kind: "local",
              official: false,
              version: item.version
            }
          };
          const idx = state.packs.findIndex(p => p.id === item.id);
          if (idx >= 0) state.packs[idx] = packObj;
          else state.packs.push(packObj);
          written.push(packObj);
        }
        return {
          writtenCount: written.length,
          catalog: buildCatalog()
        };
      },

      async stageAction(kind) {
        await delay(80);
        if (kind === "reset_size") return { ok: true, message: "컴패니언 크기를 기본값(100%)으로 초기화했습니다." };
        if (kind === "return_home") return { ok: true, message: "컴패니언을 대시보드 기본 도킹 위치로 복귀시켰습니다." };
        return { ok: false, message: "지원하지 않는 동작입니다." };
      }
    };
  }

  global.MockCompanionAPI = createMockApi();
})(window);
