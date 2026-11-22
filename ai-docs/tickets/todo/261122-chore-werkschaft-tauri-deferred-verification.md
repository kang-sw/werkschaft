---
title: "Tauri 셸 Phase 4에서 미룬 확인 항목"
related:
  261121-feat-werkschaft-tauri-shell-return: "origin; Phase 4 closed with these rows deferred (owner (2141)=a)"
  261115-feat-werkschaft-electron-shell: "Phases 2-4 reliability and Windows delivery work to re-home to Tauri"
---

# Tauri shell verification deferred from Phase 4

## Background

The owner adopted the Tauri shell ((2140)=b). It merged into `develop` at
`1aace4c5e`. The owner then closed Phase 4 of
`261121-feat-werkschaft-tauri-shell-return` with the rows below deferred
((2141)=a). Each row needs a person at a console, a throwaway account or
VM, or an Electron launch that this macOS host does not run. None of them
blocks shell implementation work such as
`261122-feat-werkschaft-tray-notifications-full-quit`.

The row definitions, evidence format, and isolation recipe are the ones in
the origin ticket's Phase 4 (Windows pass list rows (d) and (e), item 3, and
item 5). This ticket does not restate them. Record results here.

## Constraints

- Windows runs follow the origin ticket's isolation recipe: a unique run
  folder, environment overrides under that folder, and cleanup of owned
  processes only.
- OS code signing and notarization stay with the Electron retirement
  decision. They are not part of this ticket.

## Phases

### Phase 1: Run the deferred rows

1. **Row (d), staffed console.** Check the release menu bar and physical
   shortcuts, including DevTools and zoom. Check the approved
   `CloseRequested` behavior with a recoverable draft and its fallback.
   Check ordinary physical text copy and paste.
2. **Row (e), throwaway account or VM.** Run the NSIS/MSI installer and the
   updater end to end. Cover restart, failed-update recovery, and file
   locking under antivirus. Cover the installer bootstrapping WebView2 on an
   image without it. Cover the Electron-to-Tauri migration cases listed in
   origin Phase 4 item 5.
3. **macOS paired measurement.** Measure installed and download bytes, idle
   and active memory, and the scroll/typing scenario for Tauri against
   Electron. Use the same machine and workload. Run it on a host where
   launching Electron is allowed.
4. **Re-home.** Move the overlapping reliability and Windows delivery work
   from `261115-feat-werkschaft-electron-shell` Phases 2-4 to Tauri tickets.
   The origin gate asks for this after Phase 4.
