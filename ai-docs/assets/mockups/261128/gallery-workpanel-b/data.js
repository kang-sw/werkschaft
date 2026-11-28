/* Fixture data for Werkschaft Gallery Workpanel (Graybox B) mockup.
   Matches live Werkschaft shapes: Todo (#r-panels), Agenda (#r-panels),
   Notes (GET /s/<rid>/notes, 4 layers), and Manuals (wsdoc.ManualsList, 13 docs). */

window.WP_FIXTURES = {
  todos: [
    {
      key: "audit-tokens",
      status: "done",
      title: "Verify --wb-* light & dark tokens against page.css and dashboard.css",
      instruction: "Cross-check `--wb-bg`, `--wb-card`, `--wb-muted`, `--wb-fg`, `--wb-primary`, and semantic colors (`--wb-ok`, `--wb-warn`, `--wb-fail`) so dark and light modes require zero translation into production CSS."
    },
    {
      key: "scope-lock",
      status: "done",
      title: "Lock 4-item Workpanel contract & relocate Review Ledger to Git page",
      instruction: "Honor `(1727)=b` and `(1726)=a`: Workpanel strictly contains Todo, Agenda, Notes, and Manuals. Exclude mailbox, tickets drawer card, and Review Ledger."
    },
    {
      key: "rail-dots",
      status: "done",
      title: "Wire right-edge .gal-rail dots (Home ⌂, Workpanel, Ticket Map, Git)",
      instruction: "Reuse `dashboard-rail.css` `.gal-rail` and `.gal-dot.page.active` styling on the right edge of `#bgal` so Workpanel reads as a native pinned gallery page."
    },
    {
      key: "grid-layout",
      status: "wip",
      title: "Implement Graybox B container-query layout (Todo + Agenda / Notes | Manuals)",
      instruction: "Keep `Todo` and `Agenda` simultaneously visible in a 2-column grid at `>= 540px` (`@container wp`), collapsing cleanly into a full-width `[Todo | Agenda]` segmented card below `540px` so narrow `380px` gallery columns never cramp."
    },
    {
      key: "manual-preview",
      status: "pending",
      title: "Connect Manuals rows to simulated WsPreview slide-over document viewer",
      instruction: "Render all 13 `ai-docs/manuals/*.md` entries from `wsdoc.ManualsList` with real `summary:` lines; clicking any row opens the `WsPreview` sheet with path copy and Escape dismissal."
    },
    {
      key: "narrow-polish",
      status: "defer",
      title: "Audit 380px narrow overflow, empty state, and keyboard focus order",
      instruction: "Deferred until visual sign-off: verify `scrollWidth <= clientWidth` across Wide (`780px`), Narrow (`380px`), Dark, Light, and Empty states in headless Chromium."
    }
  ],

  agenda: {
    dashboard_shell_ide: {
      track: "Track 1 · D-Layout",
      locked_decisions: {
        "(1707)": "a — unified outer frame + 12px stage well",
        "(1708)": "a — bottom status bar + upward usage popover",
        "(148)": "a — bottom-left fixed #rail-companion-slot"
      },
      surface_contract: {
        top_bar_px: 36,
        bottom_bar_px: 26,
        borderless_tonal: true
      }
    },
    gallery_workpanel_rework: {
      layout: "Graybox B",
      approved: ["(1727)=b", "(1726)=a"],
      container_breakpoint_px: 540,
      exact_scope: ["Todo", "Agenda", "Notes", "Manuals"],
      review_ledger_target: "git_page",
      reference_tabs: "single scrollable card maximizes list height"
    },
    peer_coordination: {
      active_peers: 2,
      unread_mail: 0,
      delegates: [
        "art-lead (companion rig v3.1)",
        "shell-worker (impl/develop/dashboard-shell)"
      ],
      next_sync: "Post Graybox B mockup to whiteboard for owner verification"
    }
  },

  notes: [
    {
      id: "n-1",
      layer: "worktree",
      priority: 10,
      key: "workpanel.layout_contract",
      age: "4m",
      muted: false,
      body: "Graybox B approved under (1727)=b: upper 2-column always-visible working memory (Todo left, Agenda right) paired with a lower single-card tabbed reference surface (Notes | Manuals). Container query threshold at 540px."
    },
    {
      id: "n-2",
      layer: "worktree",
      priority: 8,
      key: "workpanel.review_ledger_move",
      age: "19m",
      muted: false,
      body: "Per (1726)=a, Review Ledger is removed from Workpanel and colocated on the dedicated Git gallery page beside commit graph and working tree status."
    },
    {
      id: "n-3",
      layer: "worktree",
      priority: 0,
      key: "workpanel.narrow_experiment",
      age: "1h",
      muted: true,
      body: "Superseded draft note: stacking Todo and Agenda vertically in a 380px column starved the Notes/Manuals scroller. Replaced by the segmented [Todo | Agenda] switch below 540px."
    },
    {
      id: "n-4",
      layer: "repo",
      priority: 10,
      key: "style.borderless_tonal",
      age: "2h",
      muted: false,
      body: "Follow ai-docs/manuals/dashboard-style.md strictly: separate canvas (--wb-bg), cards (--wb-card), and headers (--wb-muted) by tonal steps and soft shadows rather than 1px solid outlines on rows or buttons."
    },
    {
      id: "n-5",
      layer: "repo",
      priority: 7,
      key: "gallery.always_open_bgal",
      age: "5h",
      muted: false,
      body: "#bgal stays mounted with .gal-rail on the right edge. Inactive pages stay off-viewport via transform rather than display:none in production so iframes and scroll positions survive switches."
    },
    {
      id: "n-6",
      layer: "repo",
      priority: 4,
      key: "release.compat_tier_default",
      age: "1d",
      muted: false,
      body: "Prefer T0–T2 compatibility for dashboard endpoint additions. New Workpanel gallery layout reads existing SSE #r-panels, GET/POST /s/<rid>/notes, and wsdoc.ManualsList without breaking older clients."
    },
    {
      id: "n-7",
      layer: "clone",
      priority: 9,
      key: "build.windows_av_budget",
      age: "3h",
      muted: false,
      body: "Target Windows workstations run heavy real-time antivirus scanning on file touches. Keep Workpanel Notes filtering and Manuals search purely in-memory on the client; only explicit Refresh hits GET /s/<rid>/notes."
    },
    {
      id: "n-8",
      layer: "clone",
      priority: 5,
      key: "branch.develop_review_track",
      age: "1d",
      muted: false,
      body: "Day-to-day dashboard work lands on develop (review-track). main is reserved for release-worthy merges gated by the review-watermark frontier."
    },
    {
      id: "n-9",
      layer: "clone",
      priority: 0,
      key: "mockup.sandbox_ttl_note",
      age: "2d",
      muted: true,
      body: "Whiteboard /fs/mock/ iframes run in opaque-origin sandbox with a 15-minute capability TTL; load all local JS/CSS/data upfront rather than lazy-fetching JSON after idle."
    },
    {
      id: "n-10",
      layer: "machine",
      priority: 6,
      key: "machine.playwright_cache",
      age: "14h",
      muted: false,
      body: "Headless Chromium Playwright is pre-cached under ~/.npm/_npx/*/node_modules/playwright for fast visual and layout-overflow checks without network downloads."
    },
    {
      id: "n-11",
      layer: "machine",
      priority: 3,
      key: "machine.ssh_windows_dogfood",
      age: "3d",
      muted: false,
      body: "Windows remote dogfood target is reachable via local-machine-context.local.md for native Windows source-build and cold-load acceptance checks."
    }
  ],

  manuals: [
    {
      stem: "codex-integration",
      file: "codex-integration.md",
      summary: "Probed Codex CLI behavior",
      sections: [
        { h: "Plugin & MCP Refresh Behavior", p: "Documents observed Codex CLI plugin loading, marketplace refresh cadence, and MCP server restart mechanics across local development builds." },
        { h: "Key Findings", list: ["Config overlays apply on session launch; reload required after manifest schema edits.", "MCP stdio transport inherits workspace environment at process start."] }
      ]
    },
    {
      stem: "companion-pet-rigging",
      file: "companion-pet-rigging.md",
      summary: "Living production spec for the Werkschaft lead companion — v3.1+ data-driven doll-maker schema, full timing/easing/stage/prop-motion assetization, articulated drag inertia, mesh-deform leg and breath articulation, hybrid global #rail + active-session relay, hover resize UX, and 1-by-1 art craft gates",
      sections: [
        { h: "Docking Contract", p: "The lead companion anchors inside `#rail-companion-slot` at the bottom of the left session rail with 0px vertical jitter during session-list scrolling." },
        { h: "Rigging & Relay", list: ["Data-driven timing and easing curves decouple motion craft from DOM state.", "Hybrid relay mirrors active-session phase without duplicating WebGL/VRM contexts."] }
      ]
    },
    {
      stem: "config-settings-ui",
      file: "config-settings-ui.md",
      summary: "Maintain the dashboard settings catalog, adapter declarations, and translations together so every supported setting remains inspectable.",
      sections: [
        { h: "Catalog Synchronization", p: "Every config key added to `config_registry.go` must declare matching metadata across `config-manifest.json`, `dashboard-config-manifest.json`, and settings UI routes." },
        { h: "Checklist", list: ["Verify default values across both `ws` and `wsflow` distributions.", "Keep proposal diff previews readable in both Dark and Light themes."] }
      ]
    },
    {
      stem: "dashboard-style",
      file: "dashboard-style.md",
      summary: "Apply Werkschaft's borderless Material tonal surface system, canonical CSS tokens, interaction patterns, and dark-theme mockup rules when styling dashboard or whiteboard UI.",
      sections: [
        { h: "1. Borderless Material Tonal Surfaces", p: "Separate canvas (`--wb-bg`), elevated cards (`--wb-card`), and headers (`--wb-muted`) through tonal steps and soft elevation shadows instead of boxing every row or button with `1px solid` borders." },
        { h: "2. Canonical Surface Ladder (Dark / Light)", code: "--wb-bg:    #141518 (dark) / #f3f3f0 (light)\n--wb-card:  #1e2024 (dark) / #ffffff (light)\n--wb-muted: #2b2d32 (dark) / #e9e8e3 (light)\n--wb-fg:    #e7e7e4 (dark) / #1b1d21 (light)" },
        { h: "3. Sticky Headers & Tabular Numerals", list: ["Use `overflow: hidden; overflow: clip;` on rounded containers with sticky headers.", "Apply `font-variant-numeric: tabular-nums` to all counters, progress badges, and ages."] }
      ]
    },
    {
      stem: "local-machine-context.local",
      file: "local-machine-context.local.md",
      summary: "Local machine test hosts and environment-specific verification notes (untracked local context).",
      sections: [
        { h: "Local Context Scope", p: "Machine-local operational notes such as reachable SSH dogfood targets and local tooling paths. Never referenced by shipped plugin surfaces." }
      ]
    },
    {
      stem: "performance",
      file: "performance.md",
      summary: "Required before changing how native tooling or the daemon stores data, touches files, or spawns processes - the five storage types (ring, SQLite, files), memory-first state, delta appends, whole-file retention, held handles, I/O-free interactive paths, and per-action file-operation measurement",
      sections: [
        { h: "File-Touch Budget", p: "Windows workstations with enterprise antivirus scan every file open and write. Keep live session state in memory and persist only appended deltas for crash recovery." },
        { h: "Frontend Minimal Updates", list: ["Patch only changed DOM subtrees on SSE frames rather than re-rendering entire panels.", "Keep search and layer filters 100% client-side with zero disk touches."] }
      ]
    },
    {
      stem: "release-compatibility",
      file: "release-compatibility.md",
      summary: "Required when a change touches a cross-version surface (MCP tool or argument, `/mcp` hello, shared store shape, tracker event type, config/rsrc manifest field) - directional T0–T4 tiers, source-backed scenarios, and release rules",
      sections: [
        { h: "Compatibility Tiers (T0–T4)", list: ["T0 transparent: old binary ignores unknown additions and works normally.", "T1 display-degraded: old binary shows default values and changes no data.", "T2 recoverable overwrite: reopening with the new version restores retained raw data.", "T3 boundary: requires minor bump and explicit owner approval."] }
      ]
    },
    {
      stem: "shipped-surface-boundary",
      file: "shipped-surface-boundary.md",
      summary: "Required before editing any text that ships to a downstream project - playbooks, skills, templates, embedded conventions, and every string the MCP tooling emits to an agent",
      sections: [
        { h: "Downstream-First Rule", p: "Shipped text must never depend on files, scripts, or branch policies that exist only in `devenv`. Use generic `AGENTS.md` hooks instead." }
      ]
    },
    {
      stem: "skill-authoring",
      file: "skill-authoring.md",
      summary: "Authoring standard for skills, playbooks, agent prompts, and conventions written for current-generation models — reader model, rule lifecycle, rule tests, layouts, audits",
      sections: [
        { h: "Invariant Checklist", list: ["State constraints concisely once in the authoritative owner section.", "Prefer deterministic tool outputs over prose reminders.", "Keep playbooks host-neutral with canonical `ws/*` MCP tool names."] }
      ]
    },
    {
      stem: "windows-dogfood",
      file: "windows-dogfood.md",
      summary: "Native-Windows source-build dogfood / Phase C cold-load acceptance procedure",
      sections: [
        { h: "Cold-Load Acceptance", p: "Validates daemon launch latency, tray/webview bootstrap, and antivirus-friendly file-touch counts on native Windows hosts." }
      ]
    },
    {
      stem: "workflow-cost-measurement",
      file: "workflow-cost-measurement.md",
      summary: "Qualitative before/after measurement of workflow cost from git history and the ticket tree; read-only, every indicator states its command, project-neutral",
      sections: [
        { h: "Read-Only Audit Indicators", p: "Measures cycle overhead, review rounds, and ticket churn directly from git log and ticket frontmatter using repeatable read-only commands." }
      ]
    },
    {
      stem: "ws-mcp",
      file: "ws-mcp.md",
      summary: "MCP operational runbook, launcher environment, release and verification steps",
      sections: [
        { h: "Daemon & Launcher Architecture", p: "Covers `ws` MCP server startup, session-key scoping, SSE dashboard streaming (`#r-panels`), and `/s/<rid>/notes` HTTP routes." },
        { h: "Workpanel Data Endpoints", code: "SSE #r-panels        -> Todo + Agenda live working memory\nGET/POST /s/<rid>/notes -> 4-layer Notes (worktree, repo, clone, machine)\nwsdoc.ManualsList    -> ai-docs/manuals/*.md metadata + WsPreview" }
      ]
    },
    {
      stem: "wsflow-mirroring",
      file: "wsflow-mirroring.md",
      summary: "Required before editing full ws skills, shared `agents-plugin/rsrc/` playbooks, or plugin surfaces that may need wsflow mirrors",
      sections: [
        { h: "Mirror Contract", p: "Ensures `agents-plugin-wsflow/` stays in lockstep with `agents-plugin/` for shared playbooks, config keys, and skill shims while preserving conservative defaults." }
      ]
    }
  ]
};
