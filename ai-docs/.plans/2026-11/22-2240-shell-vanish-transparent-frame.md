# Shell chrome vanishes while a framed page shows a modal

## Symptom (owner, desktop WKWebView)

When a modal opens or closes inside the framed session page (terminal close
confirmation, other dialogs), the shell chrome (session rail + header) is
sometimes wiped and shows the page's plain background instead.

## Verified cause (read-only explorer, headless WebKit repro)

The rail and header are siblings of `#frames` (`dashboard.tmpl:74,82-85`).
While a child modal is open the shell sets `data-fdim`, which raises `.frames`
to `--z-modal` (70) above the chrome and dims the chrome with
`filter: brightness(var(--fdim))` (`dashboard-shell.css:26-42`). The visible
iframe keeps an opaque `background: var(--wb-bg)` while dimmed (only the
`data-fpopover:not([data-fdim])` case is made transparent, and the child's
`data-lifted` transparency rule). On close the dialog disappears before the
parent's rAF-scheduled layout update clears `data-fdim`
(`dashboard-lifecycle.js:1087-1096,1164,1232-1291`), so the raised opaque
iframe covers the chrome with its plain background for that window. A 250 ms
delayed parent close update turned the rail pixel gray in the repro.

## Settled fix (CSS only; validated in memory by the explorer)

1. The iframe element and the framed page canvas (html/body/.dash of a framed
   document) are always transparent, not only while lifted for a popover; the
   outer shell document supplies the page background behind the frame.
2. Remove the parent-side `brightness` filter on the header/rail under
   `data-fdim`; the child dialog's own scrim (which now shows through the
   transparent frame) dims the chrome instead.
3. Keep the existing z-order rules (`data-fpopover`/`data-fdim` raising
   `.frames`) and the pointer-events rules unchanged.
4. Check that nothing visually regresses when no modal is open: the framed
   page's regions that should be opaque (panels, cards, composer, tracker
   drawer) must still paint their own backgrounds; only the canvas becomes
   transparent. Check the cold-load / hidden-iframe states (`visibility:
   hidden` frames) and the light theme. If the shell's own body background
   does not already match `--wb-bg` in both themes, set it so.
5. If a full-transparent canvas breaks some state you find (e.g. the index
   page framed, the placeholder rail in the framed doc showing through),
   fix the minimal rule for that state; report anything that forces a
   different approach before widening scope.

## Tests

- Add a headless WebKit browser test (cached Playwright; follow the existing
  `testdata/*.browser.cjs` pattern, e.g. `shell-topbar.browser.cjs`, and how
  the Go harness runs them) that opens a framed page, opens a confirmation
  dialog in the child, delays the parent's close update, and asserts the
  rail pixel stays rail-colored (not the page background) during the dialog
  and immediately after close while `data-fdim` is still set. It must fail on
  the current CSS and pass with the fix.
- `go test ./internal/wsdashboard -count=1` (full suite) and `go vet`
  (darwin + GOOS=windows).
- Headless before/after captures (dark + light): normal page (no modal) and
  with the terminal close confirmation open. Save PNGs under
  `ai-docs/assets/captures/261122/shell-vanish/` (NOT committed) and report
  the paths.

## Boundary

Files: `agents-plugin-tool/internal/wsdashboard/assets/dashboard-shell.css`,
child-page canvas CSS if the framed-document rule lives elsewhere, the new
browser test, and the Go test registration if needed. No JS behavior change
unless a CSS-only fix provably cannot hold (report first). Never launch
Electron or any GUI app; headless only. Do not create checkouts with raw
`git worktree add`. Commit on `impl/develop/shell-vanish-transparent-frame`;
never merge or push. No session keys, local absolute paths, or PIDs in
commits.
