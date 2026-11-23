---
title: "Electron 데스크톱 셸 은퇴: 빌드·배포 중단과 코드 제거"
related:
  261121-feat-werkschaft-tauri-shell-return: "adopted the Tauri shell; its Constraints required a separate owner decision for Electron retirement"
  261122-chore-werkschaft-tauri-deferred-verification: "carries the Electron-to-Tauri migration verification cases"
---

# Retire the Electron desktop shell: stop building and shipping, remove code

## Background

The owner is dogfooding the Tauri shell and it runs well. The Tauri shell was
adopted at `b25b9f368`, but Electron is still built and shipped by the
release workflow. This ticket plans the retirement of the Electron shell. It
is a todo outline, not ready for dispatch (see Open ordering question).

## Decisions

- **(1396)=a, owner, 2026-11-23: retire Electron.** The owner is dogfooding
  Tauri successfully. This supersedes the guard in
  `.done/261121-feat-werkschaft-tauri-shell-return.md` Constraints: "Retirement
  or deletion of `agents-plugin-tool/desktop-electron/`, and removal of shipped
  functionality, require a separate owner decision."

## Constraints

- `.github/workflows/ws-mcp-release.yml` has a `desktop-build` job (l.233)
  that packages Electron. It is gated by `tauri_route` (set from the
  `WERKSCHAFT_TAURI_RELEASE` variable, l.75-92); `tauri_route=stable` skips it.
- `.github/workflows/werkschaft-tauri-build.yml` (l.80) runs
  `node ../desktop-electron/scripts/assemble-daemon-input.cjs`. That script
  must be relocated out of `agents-plugin-tool/desktop-electron/` before the
  directory is deleted. `ws-mcp-release.yml` (l.304) uses it too.
- The Electron-to-Tauri user migration relies on the shipped Electron updater
  swapping in Tauri-compatible payloads (Tauri ticket decisions (2057) and
  (2065)=a; no separate bridge release).

## Open ordering question (blocks promotion to ready)

The order of "stop shipping Electron builds" versus "existing Electron
installs have been migrated" is **not settled**. Stopping Electron builds
while installs still depend on the Electron updater could strand them. The
owner must settle this ordering before this ticket is promoted to ready. No
decision is assumed here.

## Phases

### Phase 1: Retire Electron (rough outline)

Pending the open ordering question: relocate `assemble-daemon-input.cjs`,
remove the `desktop-build` job and Electron release assets, then delete
`agents-plugin-tool/desktop-electron/`. Refine and split once the ordering is
settled.
