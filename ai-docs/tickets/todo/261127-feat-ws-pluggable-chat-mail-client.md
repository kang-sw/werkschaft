---
title: "공통 채팅·메일 클라이언트 인터페이스 및 플러그인 어댑터 확장"
blocked-by: 261127-feat-ws-plugin-marketplace-python-ipc#phase3
related:
  261127-feat-ws-plugin-marketplace-python-ipc: provides ws-extension-marketplace/v1, Python stdio JSON-RPC extension host, and Go SQLite WAL+FTS5 cache
---

# Common Chat & Mail Client Interface and Pluggable Adapter Extension

## Background

Users want unified visibility and interaction across external chat and mail
systems from within the Werkschaft dashboard, including rich structured
messages and local full-text search. However, per `AGENTS.md` Architecture Rules
1 and 4 and `ai-docs/manuals/shipped-surface-boundary.md`, `devenv` is a
downstream-neutral meta-workflow product: proprietary or corporate-internal
HTTP-hooking chat/mail adapters must never be embedded inside this repository.

By building atop the extension marketplace, Python stdio JSON-RPC host, and
Go-owned SQLite WAL+FTS5 cache defined in
`261127-feat-ws-plugin-marketplace-python-ipc`, Werkschaft can ship a clean,
generic Chat & Mail client core UI/API, capability negotiation contract, and
progressive-enhancement rendering pipeline while keeping environment-specific
adapters external.

## Decisions

*(Settled on whiteboard `(1737)=a`)*

- **Downstream-neutral core vs. external adapters — settled `(1737)=a`:**
  - **Chosen:** Keep **only** the generic, downstream-neutral architecture in
    `devenv`:
    1. Unified Chat & Mail client core UI and daemon API.
    2. Per-adapter **Capability Option Set** enabling graceful feature
       degradation across chat, mail, threading, reactions, and attachment
       capabilities.
    3. Go-owned local **SQLite WAL+FTS5 cache DB** (via
       `261127-feat-ws-plugin-marketplace-python-ipc`) for fast offline/local
       search and timeline rendering without multi-process SQLite contention.
    4. **`<wsext:.../>` in-band progressive-enhancement extension tags** for
       rich cross-client payloads that degrade cleanly to plain text/links when
       unsupported by a target adapter or viewer.
    Corporate or internal HTTP-hooking adapters stay strictly outside `devenv`
    as external/local plugins installed through `ws-extension-marketplace/v1`.
  - **Rejected:** Bundling organization-specific chat/mail scrapers or
    proprietary HTTP adapters inside `agents-plugin-tool/`.
- **Compatibility tier — T0 additive:**
  - Introduces new additive dashboard client surfaces, adapter RPC contracts,
    and SQLite cache tables without modifying existing wire protocols, `/mcp`
    hello, or existing dashboard conversation/board stores.

## Constraints

- **Repo & shipped-surface boundary (`AGENTS.md` Architecture Rules 1 & 4; `ai-docs/manuals/shipped-surface-boundary.md`):**
  - Zero corporate-internal endpoints, vendor names, or proprietary HTTP-hooking
    adapters in `agents-plugin-tool/` or `ai-docs/`. Any reference adapter in
    the repository must use standard open protocols (e.g., IMAP/SMTP) or a
    test/mock adapter.
- **Performance & Windows AV (`ai-docs/manuals/performance.md`):**
  - Store normalized messages in the Go-owned SQLite WAL+FTS5 cache (single held
    connection, Rule 4) and store large binary attachments as hash-keyed Type 5
    files outside SQLite. Render lists via keyed/incremental DOM updates rather
    than full-list `innerHTML` rebuilds.
- **Visual design (`ai-docs/manuals/dashboard-style.md`):**
  - Style the unified Chat & Mail client views, capability-gated toolbars, and
    `<wsext:.../>` progressive-enhancement cards using Werkschaft's borderless
    Material tonal surface ladder (`--wb-bg` / `--wb-card` / `--wb-muted`),
    `--wb-*` tokens, and compact icon controls.
- **Release compatibility (`ai-docs/manuals/release-compatibility.md`):**
  - T0 additive surface; preserve backward compatibility across existing
    dashboard views and extension-host protocol versions.

## Phases

### Phase 1: Capability Option Set Contract & Normalized Chat/Mail Schema

*Depends on `261127-feat-ws-plugin-marketplace-python-ipc#phase3` (extension host + Go SQLite WAL+FTS5 cache).*

1. **Capability Option Set contract:**
   - Define the adapter capability manifest/RPC schema declaring supported
     dimensions (`chat`, `mail`, `threads`, `reactions`, `attachments`, `search`,
     `webhook_push`, `rich_ext_tags`) so the core client degrades cleanly when
     an adapter omits optional features.
2. **Normalized SQLite WAL+FTS5 schema:**
   - Define Go-owned SQLite tables and FTS5 indexes for accounts/channels,
     threads, messages, participants, read/unread state, and Type 5 external
     attachment references, populated via `cache/upsert` deltas from adapters.
3. **Verification:**
   - Schema migration, FTS5 search, capability-negotiation, and graceful
     degradation tests across minimal vs. full capability sets.

### Phase 2: `<wsext:.../>` In-Band Progressive-Enhancement Parser & Renderer

*Depends on Phase 1 schema & contract.*

1. **Safe in-band tag parser/sanitizer:**
   - Implement a strict parser and sanitizer for `<wsext:.../>` extension tags
     embedded in message bodies, extracting structured payload metadata with
     clean plain-text fallback when stripped, unsupported, or viewed in legacy
     surfaces.
2. **Progressive-enhancement card renderer:**
   - Render supported `<wsext:.../>` blocks as sanitized tonal cards in the
     dashboard client while keeping untrusted markup sandboxed (no raw HTML/JS
     injection).
3. **Verification:**
   - Fuzz/unit tests for malformed tags, XSS/script injection rejection, and
     plain-text fallback equivalence.

### Phase 3: Unified Dashboard Chat & Mail Client Surface & Extension Hook Bindings

*Depends on Phase 1 and Phase 2.*

1. **Daemon API & extension hook bindings:**
   - Add daemon routes bridging the dashboard client to cached SQLite threads,
     live adapter send/reply/reaction actions (gated by the adapter's Capability
     Option Set), and Type 5 attachment downloads.
2. **Dashboard Chat & Mail UI:**
   - Build the unified Chat & Mail client view following
     `ai-docs/manuals/dashboard-style.md` (thread list, message timeline,
     FTS5 search bar, compose/reply bar gated by adapter capabilities, and
     `<wsext:.../>` card rendering) with full `wsi18n` localization.
3. **Verification:**
   - End-to-end Go + frontend tests verifying send/receive/search, capability
     gating (disabled/hidden unsupported actions), and keyed DOM updates.

### Phase 4: Reference Open-Protocol / Mock Adapter

*Depends on Phase 3.*

1. **Reference adapter:**
   - Provide a minimal, downstream-clean reference adapter (standard open
     IMAP/SMTP or deterministic mock extension package) demonstrating
     `ws-extension-marketplace/v1` packaging, `config_schema` + `wscred`
     configuration, `cache/upsert` sync, Capability Option Set declaration, and
     `<wsext:.../>` emission for automated integration tests and downstream
     plugin authors.
2. **Verification:**
   - Automated integration test installing/running the reference adapter over
     the Python stdio JSON-RPC host end-to-end without any corporate/internal
     dependencies.
