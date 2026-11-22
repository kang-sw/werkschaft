# Werkschaft Phase 4 macOS owner checklist

Run on the authorized Mac. Record PASS or FAIL and the result or evidence for each step. Expected results below are acceptance criteria.

## Prepare the two builds

1. Record `sw_vers`, `uname -m`, display resolution and refresh rate, and the version of `/System/Library/Frameworks/WebKit.framework/Resources/Info.plist`. Record the WKWebView and Electron user agents from their consoles. **Expected** — The run identifies the OS, architecture, display, and both browser engines.
2. Prepare the packaged Tauri candidate and packaged Electron baseline with the same `ws-mcp` binary. Record the app versions, Git revisions, archive SHA-256 values, and the daemon SHA-256 from both apps. **Expected** — The two daemon hashes match.
3. Set up an isolated account or test environment on the authorized Mac. Use fresh app and WebView profiles, a dedicated `WS_CACHE_HOME`, a dedicated `WS_CONFIG_HOME`, a fixed loopback port, and an empty provider configuration. Use the same isolated daemon and board for both shells. **Expected** — Both apps reach only the test dashboard.
4. Seed a board with eight topics, 12 formatted paragraphs in each topic, 16 open check items, and a conversation with 180 alternating user and reply entries. Include one local text file, one small PNG, one same-origin panel, and a test composer. Record the fixture and served dashboard asset ID. **Expected** — Both shells render the same fixture and asset revision.

## WKWebView viability

1. Use a dashboard action that requires an in-page confirmation. Cancel the first attempt and confirm the second. **Expected** — The action takes effect only on confirmation.
2. Type an unsent composer draft, paste a small PNG into it, click the red close button, and reopen the app and session. **Expected** — The window closes without a dialog and the same text and image reappear.
3. Use an isolated renderer fixture that fails the native close recovery handshake. Click the red close button and choose Stay. **Expected** — The native fallback prompt appears and Stay leaves the window open.
4. Simulate a send accepted by the test daemon with its response interrupted. Close and reopen the app. **Expected** — The delivery-unknown draft waits for an explicit user action.
5. Press Command-Plus, Command-Minus, and Command-0 from a focused composer. **Expected** — The page grows, shrinks, and returns to its default zoom in order.
6. Open an approved dashboard link in a new window. **Expected** — A separate Werkschaft window opens the pinned dashboard route.
7. Open an external HTTPS link from the board. **Expected** — The system browser opens the link while the dashboard remains in Werkschaft.
8. Open the fixture text-file link. **Expected** — The expected file opens through the dashboard's file handling.
9. Drag the fixture PNG onto the dashboard composer. **Expected** — The composer shows one image attachment.
10. Copy plain text from the conversation and paste it into the composer. **Expected** — The composer receives the exact selected text.
11. Compose and commit Korean text using the system Korean IME. **Expected** — The full string appears exactly once and composition Enter does not send it.
12. Check the actual Whiteboard controls that use `:has()`, then evaluate `CSS.supports("selector(:has(*))")` in the console. **Expected** — The selector is supported and the control applies the expected style.
13. Scroll to deferred conversation entries and evaluate `CSS.supports("content-visibility","auto")`. **Expected** — Entries appear at the correct offsets and the reported feature support is recorded.
14. Resize the board column across a container-query breakpoint. **Expected** — The affected panel changes layout at its expected width.
15. Commit and reopen an isolated dashboard edit. **Expected** — The edit persists.
16. Open a test terminal, run `echo p4-terminal`, and resize the pane. **Expected** — Output and dimensions update through the live transport.
17. Append a fixture conversation entry while the session page is open. **Expected** — The page receives it without a refresh.
18. Open an approved same-origin content frame, then attempt a test frame navigation to a second loopback port. **Expected** — The first frame renders and the second destination is refused.
19. Trigger a test notification with the app unfocused and OS notifications unavailable. **Expected** — The dashboard fallback surfaces the event after returning to the app.
20. Open Developer Tools from the release menu and its keyboard shortcut. **Expected** — The inspector opens through both controls.
21. For each failure or behavior difference, record the trigger, actual result, a capture, and the native change needed. **Expected** — Each gap has enough evidence to reproduce it and estimate the porting work.

## Size, memory, and rendering

1. Produce paired daemon-included and daemon-excluded packages with the same app executable and other payload files. Use the shipping Electron ZIP path and the Tauri updater app archive. Record the Tauri migration ZIP and DMG separately when available. **Expected** — Each reported download format has two measured byte lengths.
2. Unpack each variant and sum its regular-file byte lengths, preserving framework symlinks during extraction. Count symlinks consistently and state the convention. Record the daemon resources separately and exclude the test cache and OS WebKit. **Expected** — Each installed app payload has a reproducible byte count.
3. Run one shell at a time on the same display with the same daemon PID and board open in a 1080 × 650 CSS-pixel viewport at 1× scale. Close the developer tools before sampling. **Expected** — The shell samples share the same workload and display conditions.
4. Record resident memory in MiB for the shell and each associated renderer, GPU or network helper. Associate WKWebView services with this app using Instruments or the app attribution available on the host. Verify process identity before each sample and report the daemon separately. **Expected** — Each sample shows the app and webview subtotal and one daemon value.
5. Start with the conversation scroll pane at its top and the composer empty. After the board loads and 90 display-frame intervals are captured, take three idle samples starting 0.75 s later, about 0.9 s apart. **Expected** — Each shell has three idle samples at the same point in the scenario.
6. Run 60 wheel inputs at 100 ms intervals, 30 at +80 CSS px and 30 at −64 CSS px. Enter one character of `phase four benchmark typing text` every other wheel event without submitting. Take three active samples using the same offsets as idle, starting with the first input. **Expected** — The conversation scrolls and the composer receives exactly 30 characters.
7. Record median and p95 `requestAnimationFrame` intervals, gaps over 1.5× the median idle interval, and the median and p95 time from each trusted input event to its next animation frame. Save raw timestamps and record the tool used to dispatch input. **Expected** — The frame and input metrics can be reproduced in both shells.
8. Report absolute byte, MiB, and millisecond values and the Tauri minus Electron deltas. **Expected** — The owner can compare both builds against the same workload.

## Record Phase 4 evidence

1. Attach the size and timing records, viability results, and native porting gaps to Phase 4. **Expected** — The report includes the Mac and browser versions, package hashes, fixture description and raw evidence.
2. Use the native acceptance steps in [Werkschaft release packaging](../../../../agents-plugin-tool/desktop/RELEASE.md#native-acceptance) for signed updater and Electron swap work on authorized disposable profiles. Record any VM-only work as deferred with its prerequisites. **Expected** — The Phase 4 record shows which operations evidence the owner has and which checks still need an authorized environment.
