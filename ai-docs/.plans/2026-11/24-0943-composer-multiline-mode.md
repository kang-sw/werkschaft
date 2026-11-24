# Plan: Composer Multiline Mode (Auto-Switch & Variant B Send Badge)

## Goal
When global `inputMode` is `"enter"`, automatically switch the composer into temporary multiline mode (`Enter` inserts a newline; `Ctrl+Enter` / macOS `⌘+Enter` sends) on `Shift+Enter` or multiline input/paste, with a Variant B `#csend` badge and `×` revert button, leaving `.cfoot` bottom-right clear.

## Scope & Files
1. **State & UI sync** — `agents-plugin-tool/internal/wsdashboard/assets/dashboard.js`
   - Add `autoMultiline`, `autoMultilineReverted`, `isMacPlatform`, `effectiveInputMode()`, `sendChordLabel()`, `sendKbdLabel()`, `syncSendButtonLabel()`, `syncAutoMultilineUI()`, `setAutoMultiline(on)`, `resetAutoMultiline()`, `revertAutoMultiline()`, `syncAutoMultilineFromValue()`.
   - Update `setInput(v)` to clear `autoMultiline` when switching to global `"mod"` and call `syncAutoMultilineUI()`.
2. **Keyboard, input, and revert wiring** — `agents-plugin-tool/internal/wsdashboard/assets/dashboard-composer-keyboard.js`
   - Use `effectiveInputMode()` in the `Enter` keydown handler (after IME guard and `#`/`@`/`/`/`!` popup handlers).
   - Detect `Shift+Enter` (without Ctrl/Meta/Alt) in `"enter"` mode to activate `setAutoMultiline(true)`.
   - Call `syncAutoMultilineFromValue()` on `input` (guarded with `typeof` for isolated VM tests).
   - Wire `#csend-revert` click to `revertAutoMultiline()` (reverts to `"enter"` mode and refocuses `ctext` without sending).
3. **Send/clear/restore integration**
   - `dashboard-composer-delivery.js`: reset `autoMultiline` in `sendMessage()` when clearing `ctext.value`.
   - `dashboard-composer-exec.js`: reset in `cexecAcknowledged()`; use `effectiveInputMode()` in `bangSendKeyLabel()` and `bangHistSyncMode()`; call `syncSendButtonLabel()` in `bangSync()` when leaving bang mode.
   - `dashboard-composer-pickers.js`: reset in `submitCommand()` when `clearText` is true.
   - `dashboard-composer-restore.js` & `dashboard-lifecycle.js`: call `syncAutoMultilineFromValue()` after restoring held messages or drafts.
4. **Template & CSS (Variant B)**
   - `dashboard.tmpl`: wrap `#csend` in `.csend-wrap`, add `<span class="csend-kbd" id="csend-kbd" aria-hidden="true" hidden></span>` inside `#csend`, and sibling `<button type="button" class="csend-revert" id="csend-revert" ... hidden>×</button>`. Keep `.cfoot` untouched.
   - `dashboard-conversation.css`: style `.csend-wrap`, `.csend.is-multiline` (~48px wide, 2-row vertical stack fitting cleanly within `min-height: 38px`), `.csend-kbd`, `.csend-revert`, and bang-mode coordination.
5. **i18n** — `agents-plugin-tool/internal/wsi18n/tables/*.json`
   - Add `js.composer.send.key` and `js.composer.multiline.revert` across all 11 locale tables.
6. **Tests**
   - Add `agents-plugin-tool/internal/wsdashboard/testdata/composer-multiline.test.cjs` covering all transitions and edge cases.
   - Run affected `testdata/*.test.cjs` with `node --test` locally.
   - Run Go asset/assembly/i18n tests on remote `w5090`.

## Compatibility & Performance
- Compatibility tier: **T0** (frontend-only, no wire/store/manifest changes).
- Performance: static DOM nodes in template, attribute/class updates only on state change, zero DOM writes on unchanged `input` events.
