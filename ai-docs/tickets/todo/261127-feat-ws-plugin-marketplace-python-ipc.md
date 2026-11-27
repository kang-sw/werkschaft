---
title: "플러그인 마켓플레이스(Git 설치) 및 Python stdio JSON-RPC 확장 호스트"
related:
  261127-feat-ws-pluggable-chat-mail-client: first major consumer built atop the marketplace installer, Python stdio JSON-RPC host, and Go SQLite WAL+FTS5 cache
---

# Plugin Marketplace (Git Install) and Python stdio JSON-RPC Extension Host

## Background

Werkschaft currently ships companion pack fetching, zip validation, SSRF-guarded
HTTP download, atomic staging, and `.ws-install.json` metadata tracking inside
`agents-plugin-tool/internal/wsdashboard/companion_pack_{fetch,zip,install,sources,store}.go`
backed by `internal/wsnetguard/guard.go`, and stores sensitive tokens owner-only
under `internal/wscred/`. However, there is no generalized extension marketplace
installer, no out-of-process extension host for third-party / user-installed
adapters, and no safe local cache/webhook ingress for external integrations.

In addition, two Windows Python prerequisites need remediation before relying on
Python-hosted extensions on Windows workstations:

- `agents-plugin-tool/desktop/src/python.rs` currently hardcodes
  `#[cfg(target_os = "windows")] pub fn available() -> bool { true }` instead
  of performing real interpreter discovery/liveness checks (unlike macOS).
- `agents-plugin-tool/internal/wsholder/manager_launch.go` checks only
  `lookPath("python3")`, lacking a `py -3` launcher fallback on Windows.

## Decisions

*(All 5 architecture policies settled as option `a` plus backlog scheduling `(1767)=a`)*

- **Python runtime & dependency model — settled `(1741)=a`:**
  - **Chosen:** Stdlib-first Python runtime plus optional pure-Python vendored
    wheels (`zipimport` or plugin-local directory; no native `.pyd` extensions by
    default). Fix Windows `desktop/src/python.rs` real availability detection and
    add `py -3` fallback in `wsholder/manager_launch.go` as Phase 1 prerequisites.
  - **Rejected:** Shipping a bundled embedded CPython distribution inside every
    binary/installer or permitting arbitrary `pip install` of native wheels on
    locked-down corporate Windows hosts.
- **Subprocess & credential isolation — settled `(1742)=a`:**
  - **Chosen:** Lazy-spawn **one stdio JSON-RPC subprocess per active plugin**
    (zero processes spawned for inactive/disabled plugins), isolating secrets,
    blocking network I/O, crashes, and update/uninstall lifecycles from the Go
    daemon.
  - **Rejected:** Running third-party extension code in-process or sharing a
    single multi-tenant Python process across untrusted/independent plugins.
- **Settings UI & secret boundary — settled `(1743)=a`:**
  - **Chosen:** Host-rendered declarative configuration by default
    (`config_schema` + `wscred` owner-only secret storage + `ext/test_connection`
    status badge) inside a dedicated `Extensions` settings section/modal. Add
    optional sandboxed iframe views (`allow-scripts`, `connect-src 'none'`,
    non-secret UI only) in a follow-up phase.
  - **Rejected:** Giving plugin HTML/JS direct access to daemon secrets or
    unsandboxed dashboard DOM.
- **Local SQLite cache & inbound webhooks — settled `(1744)=a`:**
  - **Chosen:** The **Go daemon solely owns** the local SQLite WAL+FTS5 cache
    (one held connection per `ai-docs/manuals/performance.md` Rule 4, avoiding
    Windows AhnLab V3 multi-process `-wal`/`-shm` scanning/contention) and the
    `/api/ext/<id>/webhook/*` HTTP ingress. Python plugins exchange normalized
    deltas (`cache/upsert`) and inbound webhook payloads (`ext/http_request`)
    over stdio JSON-RPC. Large attachment blobs are stored outside SQLite as
    hash-keyed Type 5 plain files.
  - **Rejected:** Letting Python subprocesses open the same SQLite database
    directly (multi-process WAL/SHM contention and AV scanner amplification).
- **Marketplace installer protocol — settled `(1745)=a`:**
  - **Chosen:** Generalize the existing `companion_pack_fetch/zip/install` core
    (`wsnetguard` SSRF guard, zip-bomb limits, Windows path/long-path safety,
    atomic staging, `.ws-install.json`) into **`ws-extension-marketplace/v1`**
    with explicit `allowFlatAssetsOnly` vs. `allowCodeTree + trustConsent`
    policy flags, plus local `git` / PAT fallback for private and corporate Git
    repositories.
  - **Rejected:** Building a second unguarded installer pipeline separate from
    `wsnetguard` and the proven atomic pack installer.
- **Backlog scheduling — settled `(1767)=a`:**
  - **Chosen:** Capture in `todo/` now for sequential implementation after the
    current `v0.53.2` release cycle.
- **Compatibility tier — T0/T1 additive (preliminary; verify exact tier before `ready/` promotion):**
  - Introduces new extension host, `wscred` entries, additive SQLite cache
    tables, and an `Extensions` dashboard surface without altering existing MCP
    tools or daemon routes. Exact downgrade/reopen behavior of new `wscred` /
    marketplace state will be verified against `release-compatibility.md` prior
    to `ready/` promotion.

## Constraints

- **Performance & Windows AV (`ai-docs/manuals/performance.md`):**
  - Follow Rule 4 (Go daemon holds the single SQLite connection; Python never
    opens `-wal`/`-shm` files directly), Rule 1 & 2 (memory/WAL delta updates,
    no per-message whole-file rewrites), Rule 6 (lazy-spawn only active plugins;
    zero processes when inactive), and Type 5 storage for large attachment blobs
    outside SQLite.
- **Release compatibility (`ai-docs/manuals/release-compatibility.md`):**
  - Grade additive `wscred`, `.ws-install.json`, and SQLite schema changes
    before `ready/` promotion; keep existing `companion_pack_*` behavior intact.
- **Shipped surface boundary (`ai-docs/manuals/shipped-surface-boundary.md`):**
  - All marketplace manifests, `ws-extension-marketplace/v1` docs, and
    `Extensions` UI strings must be downstream-generic with zero devenv-only or
    corporate-internal references.
- **Visual design & settings catalog (`ai-docs/manuals/dashboard-style.md`, `ai-docs/manuals/config-settings-ui.md`):**
  - Render declarative `Extensions` settings/modals and connection badges using
    Werkschaft's borderless Material tonal surface ladder, `--wb-*` tokens, and
    full `wsi18n` locale parity; update the settings catalog / drift tests if
    any `wsconfig` keys are introduced.

## Phases

### Phase 1: Windows Python Detection & Generalized `ws-extension-marketplace/v1` Installer

1. **Windows Python prerequisites:**
   - Replace the hardcoded `true` stub in `agents-plugin-tool/desktop/src/python.rs`
     with real bounded interpreter detection (supporting `WS_MCP_HOLDER_PYTHON`,
     `python3`, `python`, and `py -3`).
   - Add `py -3` launcher fallback in `agents-plugin-tool/internal/wsholder/manager_launch.go`
     when `python3` is absent on Windows `PATH`.
2. **Generalized marketplace installer (`ws-extension-marketplace/v1`):**
   - Extract and generalize the shared fetch/zip/SSRF/atomic-staging core from
     `wsdashboard/companion_pack_{fetch,zip,install,sources,store}.go` +
     `wsnetguard` so both companion packs (`allowFlatAssetsOnly`) and code
     extensions (`allowCodeTree + trustConsent`) share one hardened pipeline
     writing `.ws-install.json` receipts.
   - Add local `git` / PAT clone-or-archive fallback for private and corporate
     Git repositories.
3. **Verification:**
   - Unit tests for Windows Python discovery / `py -3` fallback, SSRF/zip-bomb/
     long-path/trust-consent enforcement, `allowFlatAssetsOnly` parity with
     companion packs, and Git/PAT fallback handling.

### Phase 2: Lazy Per-Plugin Python stdio JSON-RPC Host & `wscred` Secret Injection

*Depends on Phase 1 installer & Python discovery.*

1. **Extension host lifecycle:**
   - Implement lazy-spawn process supervisor (one stdio JSON-RPC subprocess per
     active plugin; zero processes for inactive plugins; clean restart/stop on
     crash, upgrade, or uninstall).
   - Define the core JSON-RPC handshake and capability registration
     (`register_extension` hooks, requests, and health/timeout handling).
2. **Secret boundary (`internal/wscred`):**
   - Persist plugin secrets in owner-only `wscred` storage and inject resolved
     secrets into the plugin subprocess at launch/RPC boundary without exposing
     them to dashboard JS or plain config files.
   - Support `ext/test_connection` RPC for live status verification.
3. **Verification:**
   - Subprocess lifecycle, idle/inactive zero-spawn, crash-recovery, JSON-RPC
     framing, and `wscred` redaction/isolation tests.

### Phase 3: Go-Owned SQLite WAL+FTS5 Cache, Webhook Ingress, & Declarative `Extensions` UI

*Depends on Phase 2 extension host.*

1. **Go-owned SQLite WAL+FTS5 cache & blob store:**
   - Create Go-owned SQLite WAL+FTS5 tables with a single held connection
     serving `cache/upsert` / query primitives over stdio JSON-RPC, plus
     hash-keyed Type 5 external blob storage for large attachments.
2. **Inbound webhook ingress:**
   - Expose `/api/ext/<id>/webhook/*` on the daemon, forwarding validated
     inbound payloads to the target plugin via `ext/http_request` stdio
     JSON-RPC.
3. **Declarative `Extensions` settings UI:**
   - Render `config_schema` forms, `wscred` masked secret inputs, marketplace
     install/trust-consent flows, and `ext/test_connection` status badges in a
     dedicated `Extensions` settings section/modal with full `wsi18n` coverage.
4. **Verification:**
   - SQLite WAL+FTS5 single-connection concurrency/search tests, Type 5 blob
     dedup/retention tests, webhook ingress routing tests, and dashboard UI/i18n
     tests.

### Phase 4: Optional Non-Secret Sandboxed Iframe Views

*Depends on Phase 3 `Extensions` UI & extension host.*

1. **Sandboxed custom UI slots:**
   - Allow extensions to declare optional non-secret UI panels rendered inside
     strictly sandboxed iframes (`sandbox="allow-scripts"`, CSP
     `connect-src 'none'`), communicating only via host-mediated postMessage
     capabilities with zero access to `wscred` secrets or parent DOM.
2. **Verification:**
   - Security boundary tests verifying CSP/sandbox restrictions and zero
     secret leakage into iframe frames.
