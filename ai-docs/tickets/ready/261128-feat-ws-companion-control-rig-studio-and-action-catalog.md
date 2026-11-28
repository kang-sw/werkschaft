---
title: "컴패니언 Control-Rig 스튜디오·팩 클립 저작 및 동적 에이전트 액션 카탈로그"
parent: 261122-epic-werkschaft-lead-companion
related:
  261128-feat-ws-companion-visual-polish-and-metafiction-overlay: "(1769)=a in-flight frontend VRM polish (vrm-pose.js / vrm-studio.js / vrm-props.js split, 30-bone finger presets, bust framing); Phase 2 of this ticket starts only after it merges to develop"
  261128-feat-ws-companion-soul-memory-and-pacing: ws/companion.act, companion-body Tier-1 launch prompt and per-wake SOUL/MOOD/RECALL envelope this ticket extends
  261126-feat-ws-companion-vrm-pack-store: ws-companion-vrm/v1 manifest, .vrm/.vrma validation, importVRM and the pack snapshot this ticket extends
  261126-feat-ws-companion-vrm-engine: VRM 0.x/1.0 runtime bundle, expression controller and .vrma playback this ticket's resolver extends
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: c4a486b2b383bc21
sage-review-completeness-reviewed: c4a486b2b383bc21
---

# Companion Control-Rig & Pack Clip Studio, Dynamic `companion-body` Action Catalog, and Reserved Prop/Target-Attach Contract

## Background

The VRM companion animates only through hardcoded procedural code (built-in
gesture tables in `companion_director.go` and `dashboard-companion-vrm*.js`)
plus an optional flat `animations` map of `.vrma` files per pack. Three gaps
remain:

1. **No authoring path.** A user cannot pose, keyframe, or save a motion for
   their own VRM kit. Procedural motions tuned for one bone layout drift on
   another kit (hands miss the laptop/phone, gaze misses the camera), and there
   is no way to override a state-machine motion per kit.
2. **No dynamic agent vocabulary.** The `companion-body` Tier-1 session learns
   gesture IDs only from fixed tool-description examples and launch-prompt
   prose, so a pack's own motions are invisible to it.
3. **No forward-compatible prop contract.** The owner's later game-dev prop
   layer (equippable rigid/chain props with socket handoffs) needs schema slots
   reserved now so packs authored today keep loading tomorrow — without ever
   attempting skinned clothing replacement.

This ticket adds a pack-scoped clip format and action catalog (Layer 1), a
runtime motion resolver (Layer 2), dynamic catalog injection into the
`companion-body` prompt (Layer 3), and a standalone Control-Rig & Pack Clip
Studio page (Layer 4), and reserves the Stage 2/3 prop/target-attach schema.

## Verified Codebase Surface

Paths are relative to `agents-plugin-tool/` unless stated; `develop @ 9bfb17422`.

- **Pack manifest is read by exact key; unknown fields are ignored and kept in raw bytes.**
  `internal/wsdashboard/companion_pack_schema.go:26-28,169-187` (`companionObject.field`
  returns nil for an absent key; `fields` reads only the listed keys);
  `companion_pack_vrm.go:63-81` (the `ws-companion-vrm/v1` validator reads only
  `id, name, schema, model, thumbnail, vroidSourceUrl, autoMatch, expressionMap, animations`);
  `companion_pack_store.go:311` (the snapshot keeps the original manifest bytes) and
  `companion_pack_routes.go:103` (served verbatim); preservation fixture
  `companion_pack_vrm_test.go:238-259` (an unknown `framing` extension survives).
- **The loader reads only the files the validator lists.** `companion_pack_store.go:298-335`
  (`loadCompanionPackWithValidator` reads `pack.json`, then exactly the returned
  `files`); VRM file-name rules `companion_pack_vrm.go:34-36,39-45` (flat
  `.vrm`/`.vrma`, no `/`, no `..`); flat `animations` key→`.vrma` map
  `companion_pack_vrm.go:149-163` (≤ 16 entries, key `^[a-z][a-z0-9_]{0,31}$`).
- **Child opens are single-component and pinned.** `companion_pack_file_unix.go:15-30`
  (`openat` relative to the pinned parent with `O_NOFOLLOW`; `O_NOFOLLOW` guards
  only the final component, so a nested path must open each directory in turn).
- **Zip install refuses unreferenced files.** `companion_pack_zip.go:413-416`
  ("file is not referenced by pack.json"); flat-name rule `companion_pack_zip.go:295-298`.
- **Install swaps whole pack directories.** `companion_pack_install.go:667-686`
  (`writeStaged` writes only manifest-named files), `:707-727` (`swapIn` moves the
  old directory aside, then the staging run is removed), `:591-597` (a hand-made
  pack without the `.ws-install.json` sidecar is never replaced), `:39-49`
  (sidecar `Kind`), `:875` (`importVRM` creates a `Kind: "local"` VRM pack).
- **Reload rescans every pack.** `companion_pack_store.go:118-149,162-220`
  (`reload` re-reads all local packs, up to `companionCacheCap = 128 MiB`); test
  file-op hook `companion_pack_store.go:44` (`trace func(op string)`).
- **Pack write routes gate on local viewer + same origin.** `companion_pack_install_routes.go:40-57`
  (`companionTrustedPost`: 405, then `TrustedWrite` 404 non-local / 403 cross-origin),
  `companion_pack_routes.go:15-27` (route registration), `:84-143`
  (`serveCompanionPackFile`: snapshot bytes, `?rev=` immutable caching, ETag, `sandbox` CSP).
- **`ws/companion.act` gesture is a free-form string broadcast verbatim.**
  `internal/mcp/companion_tools.go:258-273` (schema: optional strings),
  `:548-557` (`Gesture: strings.TrimSpace(stringValue(args["gesture"]))`, no allowlist);
  `internal/wsdashboard/companion_act.go:311-319` (published as-is on the `companion-command` rail).
- **Tier-1 prompt seams.** `companion_soul_prompt.go:14-23` (rune budgets, preamble line),
  `:507-570` (`BuildCompanionWakeEnvelopeText`: SOUL/MOOD/RECALL per-wake envelope),
  `:682-717` (`BuildCompanionBodyLaunchPrompt`), `:812-864` (`BuildCompanionWakeEnvelope`);
  `companion_director.go:869-871` (`companionQuote` neutralizes `<<<`/`>>>`);
  active pack resolution `companion_phone.go:1251-1273` (`CompanionActivePersona`:
  last pack reported by a dashboard stream, else `default`).
- **VRM vocabulary today.** `companion_director.go:630-634,678-683` (built-in VRM
  gesture/prop tables for the ambient director); `assets/dashboard-companion-vrm.js:58-70,90-102`
  (`BUILTIN_VRM_GESTURES`, `vrmVocabulary` merges `pack.animations`),
  `:115-128` (`loadVrmRuntime` with the `C.vrmRuntimeLoader` override seam),
  `:833-861` (`ensureVrmaClip`: lazy `.vrma` fetch + `createVRMAnimationClip`).
- **VRM runtime bundle and gate.** `scripts/build-companion-vrm-runtime.mjs:19-24`
  (pins `three` 0.187.0, `@pixiv/three-vrm` 3.6.0, `@pixiv/three-vrm-animation` 3.6.0,
  `esbuild` 0.28.2), `:26` (`MAX_GZIP_BYTES = 300 * 1024`), `:28-34` (entry exports);
  `internal/wsdashboard/companion_vrm_asset.go` (content-hashed `/s/companion-vrm-<hash>.mjs`,
  embedded, behind `accessGuard`), `page_routes.go:154-155`.
- **Pages.** `daemon_http.go:74` (`/settings` standalone page handler pattern), `:113` (`/` catch-all);
  `pages.go:100-112` (`companionScriptFiles` concatenated into the main dashboard),
  `:429-431` (`CompanionVrmFile`); Settings avatar group
  `assets/dashboard-settings-companion.js:470` (`renderAvatarGroup`); 11 locale tables in
  `internal/wsi18n/tables/` (`de en es fr it ja ko pt-BR ru zh-Hans zh-Hant`).
- **In-flight `(1769)=a` frontend split (branch `impl/develop/companion-visual-polish-1769`,
  read via `git show`, not on develop).** Adds `dashboard-companion-vrm-pose.js`
  (`C.vrmCoLeadPose`: pure procedural FK — "zero 2-bone IK" — `FINGER_BONES`
  15 per hand in Thumb `Metacarpal/Proximal/Distal`, other digits
  `Proximal/Intermediate/Distal` order, `FINGER_PRESETS` `relaxed|typing|phone-grip|point`,
  `computeFingerTargets`, `computePalmFrame`), `dashboard-companion-vrm-studio.js`
  (`C.vrmStudio`: **lighting/framing helpers**, not an authoring tool —
  `solveBustFraming`, `createStudioLighting`, Dark/Light `STUDIO_PRESETS`),
  `dashboard-companion-vrm-props.js` (`C.vrmCoLeadProps`), `dashboard-companion-vrm-fx.js`,
  and reorders `pages.go` `companionScriptFiles`. It touches no Go pack/prompt file.

## Decisions

- **D1 — Hybrid Control-Rig & Keyframe Studio with unified clip playback (`(1780)=a`, owner):**
  - **Chosen:** A hybrid control-rig (IK effectors, elbow poles, gaze/head, fingers,
    expressions) + mini keyframe timeline studio that saves a lightweight
    `.clip.json` studio keyframe format, and a single runtime that plays both
    `.clip.json` and `.vrma` (`@pixiv/three-vrm-animation`) through one resolver.
  - **Rejected:** `.vrma`-only authoring (needs an external DCC tool; cannot express
    target attach or kit-relative IK); a pure FK bone-rotation editor (clips stop
    converging on props/camera once bone lengths differ across kits).
- **D2 — Approved first visual mockup as Stage 1 starting point (`(1781)=b`, owner):**
  The tracked mockup [`ai-docs/assets/mockups/261128/control-rig-studio/index.html`](../../assets/mockups/261128/control-rig-studio/index.html)
  (with `studio.css`, `studio-data.js`, `studio.js`) is the layout and interaction
  reference: top bar (pack pill, kit chip, Import `.vrma` / Export JSON / Save to Pack),
  left clip rail (Sentinel vs Custom, override/expose tags), centre viewport with
  `IK | Pole | Look | Prop` mode segment, `220/280/360` slot segment, prop segment and
  Dark/Light segment, bottom timeline (play/loop/speed/duration/ease/snap), right
  inspector with `rig` and `agent` (`에이전트 노출`) tabs and the expose strip.
  Deliberate deviations from the mockup: the description counter caps at **120**
  runes (the mockup shows `/140`), the timeline allows up to **8** keyframes (the
  mockup fixtures use 4), the viewport is a real Three.js scene instead of SVG,
  and the inspector gains the `Target Attach` section (D7) the mockup predates.
- **D3 — Layer 1: Pack SSOT (owner 4-layer architecture):**
  - Files: `<config-home>/companions/<id>/pack.json` + `clips/<key>.clip.json` + `clips/<key>.vrma`.
    Only `ws-companion-vrm/v1` packs carry clips; the embedded `default` pack and
    `ws-companion/v1` 2D packs are never authored (2D `props` is already a map
    there, so the reserved array would collide — `companion_pack_schema.go:240`).
  - **Required Sentinel Keys** — fixed set `idle, laptop_work, phone_hold, phone_typing,
    phone_putdown, knock_startle, wakeup_jolt`: state-machine anchors. Absent from the
    pack → the engine's procedural motion runs; present → the pack override wins
    (per-kit customization). Declared in a top-level `sentinels` object; never exposed
    to the agent. Entry schema: `sentinels.<key> = { kind: "clip"|"vrma", file, loop?, duration_s? }`
    where `<key>` must be one of the seven (any other key → entry dropped + warning),
    `file` must equal the key-derived name (`clips/<key>.clip.json` / `clips/<key>.vrma`),
    `loop` is a boolean and `duration_s` is 0.1–30. Unknown entry fields are preserved.
  - **Sentinel → co-lead state-machine mapping** (hook site `dashboard-companion-vrm-colead.js`
    `triggerGesture`/`setPhoneState`/knock handling, (1769)=a branch `:825-856`, `KNOCK_*_DURATION` `:61-63`):

    | sentinel | co-lead trigger | default playback | duration precedence |
    |---|---|---|---|
    | `idle` | `laptop_ambient` embodiment, no phone/knock beat | loop | clip length |
    | `laptop_work` | `laptop_work` embodiment | loop | clip length |
    | `phone_hold` | `phoneMode` `pickup` (and `phone_active` hold) | one-shot into hold, then holds the last keyframe while `phone_active` | clip length |
    | `phone_typing` | `phoneMode` `typing` | loop | clip length |
    | `phone_putdown` | `phoneMode` `putdown` | one-shot | clip length |
    | `knock_startle` | `knockKind` `knock-startle` | one-shot | clip `duration_s` replaces `KNOCK_STARTLE_DURATION` for that beat |
    | `wakeup_jolt` | `knockKind` `wakeup-jolt` | one-shot | clip `duration_s` replaces `KNOCK_WAKEUP_DURATION` for that beat |

    A sentinel entry's `loop`/`duration_s` override the clip file's values; absent, the
    clip file's values apply; absent there too, the table default applies. The
    procedural beats without a sentinel (`phone_buzz`, `phone_merong_facedown`,
    `annoyed_tap`) stay procedural. A built-in gesture call with a sentinel's name
    (e.g. `act(gesture: "knock_startle")`, `phone_typing`) resolves to the sentinel
    override too, because it enters the same co-lead beat.
  - **Custom Action Catalog** — top-level `actions` array of
    `{ key, kind: "clip"|"vrma", file, description, expression?, prop?, expose_to_agent?, loop?, duration_s? }`
    (owner-settled snake_case field names, deliberately unlike the manifest's older camelCase keys).
    `expose_to_agent` defaults to `false`; an entry's `loop`/`duration_s` override the
    clip file's. Entries past the 64th are dropped with one warning (the pack still loads).
  - **Key collision rules:** an `actions` key equal to a sentinel key is dropped with a
    warning (sentinel behaviour lives only in `sentinels`); duplicate `actions` keys keep
    the first; an `actions` key equal to a built-in gesture (`wave`, `point`, …)
    overrides that gesture for this pack. The legacy flat `animations` map keeps
    working unchanged as playable gesture keys and is never injected into the agent
    catalog; on a key collision the `actions` entry wins and the pack reports a warning.
  - **What reaches the agent:** only `actions` entries with `expose_to_agent: true`
    **whose clip file loaded and validated** appear in `<<<ACTIONS>>>`. A pack is
    *authorable* when `Origin == nil` (hand-made, no `.ws-install.json` sidecar,
    `companion_pack_store.go:71-73`) **or** `Origin.Kind == "local"`; only authorable
    packs contribute ACTIONS lines. Packs installed from a remote source contribute
    **none**: zip/marketplace installs cannot ship `clips/`, so their descriptions
    would point at motions that do not exist, and remote text must not reach the
    agent prompt unreviewed.
- **D4 — Layer 2: Runtime Motion Resolver (owner):** For a requested sentinel state or
  gesture key the frontend resolves **pack clip (`.clip.json` / `.vrma`) → pack kit
  calibration offsets → engine procedural fallback**, i.e. the pack clip plays when
  present and loads; the pack's `calibration` offsets apply to prop placement for
  both a pack clip and the procedural fallback; a missing, unloadable or
  schema-invalid clip falls back to the procedural motion (built-in gesture, or
  idle for an unknown key) without retry storms. `.clip.json` and `.vrma` share one
  playback path (one `AnimationMixer` action or the clip sampler, cross-faded).
  The sampler rides the existing stage tick (`dashboard-companion-stage.js` idle 6 fps
  cadence, `:8-9,1245-1262`) and never schedules its own frames; one-shot gesture
  clips may call `markVrmActivity` like built-in gestures, but looping sentinel
  clips (`idle`, `laptop_work`, `phone_typing`, held `phone_hold`) never do, so an
  overridden idle keeps the idle cadence.
- **D5 — Layer 3: Dynamic action catalog and `companion-body` API (owner):**
  - The pack store parses `actions` once at load and keeps the catalog in the
    snapshot (Rule 1: no per-wake JSON parse or file read). The active pack is the
    one `CompanionActivePersona` resolves (`companion_phone.go:1251-1273`), factored
    into a shared `companionActivePackID()`.
  - Injection channel: a new `<<<ACTIONS>>>` block in the **per-wake** envelope
    (`BuildCompanionWakeEnvelopeText`), not the launch prompt, so a pack switch or a
    Studio save reaches the next wake without a session restart. The launch prompt
    gains one rule line telling Tier-1 to pick `ws/companion.act(gesture=...)` keys
    from that block (built-in gestures stay valid). The preamble line extends to
    "SOUL, MOOD, ACTIONS, and RECALL blocks are data".
  - Budget and sanitization (constants): `CompanionActionsMaxExposed = 24` (first 24
    `expose_to_agent: true` entries in catalog order; the rest are dropped and the
    block ends with `(+N more not shown)`), `CompanionActionDescMaxRunes = 120`,
    `CompanionActionsBudgetRunes = 3200` as its own budget. `Budget.TotalRunes` (and
    its scaling at `companion_soul_prompt.go:520-554`) keeps capping SOUL+MOOD+RECALL
    only; ACTIONS is added on top with its own cap, so existing budgets and tests are
    unchanged, and `CompanionEnvelopeMaxRunes` grows by 3200. Lines are dropped whole,
    never cut mid-line, down to `(none)`. Descriptions: at write time the API refuses
    control characters (`companionText`, `companion_pack_zip.go:675-688`) **and**
    Unicode format (`Cf`: bidi overrides, zero-width), line-separator (`Zl`) and
    paragraph-separator (`Zp`) characters; at load (hand-edited `pack.json`) an entry
    with such a description keeps playing but is not exposed (warning). At injection
    each description is collapsed with `strings.Fields`, `[`/`]` are replaced with
    `(`/`)` so a description cannot forge `[loop]`/`[expression: …]` tags, and the
    result passes through `companionQuote`. Keys are regex-bound
    (`^[a-z][a-z0-9_]{0,31}$`), so they cannot carry markup.
  - Line format (mirrors the mockup's agent tab): `- <key>: <description> [expression: <id>] [prop: <id>] [loop]`.
    An empty catalog renders `(none)`; sentinels never appear.
  - Reserved equipped-prop slot: `CompanionWakeEnvelopeData` gains
    `Equipped []CompanionEquippedProp` rendering an `<<<EQUIPPED>>>` block **only when
    non-empty**. Phase 1 never populates it, so it costs zero runes until Stage 2.
  - `ws/companion.act` keeps its free-form `gesture` string (no allowlist); only its
    `gesture` property description gains "or an action key from the ACTIONS block".
  - The ambient director (`companionPrompt`, `companion_director.go:875`) is unchanged.
- **D6 — Layer 4: standalone Control-Rig & Pack Clip Studio (owner):**
  - Isolated page `GET /companion/studio?pack=<id>`: a **top-level page opened in a new
    browser tab** (not shell-framed, so `shell.js`/`dashboard-lifecycle.js` routing is
    untouched), gated like `/settings` on `localBrowseViewer` (non-local viewer → 404,
    `config_routes.go:298-336`) behind `accessGuard`, served by `companion_rig_studio.go`
    with its own template and script list (shared tokens/i18n bootstrap reused from the
    settings page assets, WsCompanion playback modules, `dashboard-companion-rig-studio.js`)
    inlined with a per-response nonce, and an explicit CSP: `default-src 'none'`,
    `script-src 'nonce-…' 'self'` (for the hashed `/s/` runtime module), `connect-src 'self'`,
    `img-src 'self' blob: data:`, `style-src 'nonce-…'`, `frame-ancestors 'none'`.
    An unknown or non-authorable `pack` renders an in-page error state, not a 404.
    It ships a **separate esbuild entry**
    `companion-rig-studio-runtime.mjs` (same pins, adds `OrbitControls` and
    `TransformControls` from `three/examples/jsm/controls/`), content-hashed and served
    at `/s/companion-rig-studio-<hash>.mjs`. The studio page installs it through the
    existing `C.vrmRuntimeLoader` seam (`dashboard-companion-vrm.js:115-128`), so one
    page never loads two copies of `three`.
  - Isolation guarantee: `companion-vrm-runtime.mjs` stays **byte-identical**
    (still ≤ 300 KiB gzip) and studio-only code (controls, studio UI, IK gizmos,
    timeline) is never added to `companionScriptFiles` or `dashboard.tmpl`. The
    studio runtime has its own gate `MAX_STUDIO_GZIP_BYTES = 360 * 1024`. The shared
    playback modules the dashboard needs to *play* clips (D4,
    `dashboard-companion-vrm-clip.js` + `dashboard-companion-vrm-ik.js`) are the only new
    bytes in the main companion script, capped at **24 KiB combined unminified** by a
    Go test. Isolation is proven by a Go test pinning the SHA-256 of the embedded
    `companion-vrm-runtime.mjs` to its pre-ticket value (minification renames
    identifiers, so a symbol-name grep is not proof) plus file-list checks on
    `companionScriptFiles` and `dashboard.tmpl`.
  - **Live pack refresh:** after a successful Studio write, the Studio posts
    `{type: "companion-packs-changed", pack, rev}` on a same-origin
    `BroadcastChannel("ws-companion-packs")`; the dashboard (`dashboard-companion-stage.js`)
    listens and calls its existing `packsChanged()` → `requestPack(true)` (`:509,600`),
    the same refresh the Settings page triggers through the shell `companion-packs-changed`
    message (`dashboard-settings.js:1913`, `dashboard-lifecycle.js:1429-1430`). An open
    dashboard therefore plays a new key without a page reload, and stale-`?rev=` clip
    fetches are re-issued against the new revision.
  - Naming: the authoring tool uses the `rig-studio` stem everywhere
    (`dashboard-companion-rig-studio.js/.css`, `companion_rig_studio.go`,
    `js.companion.rig_studio.*` i18n keys) because `(1769)=a` already owns
    `dashboard-companion-vrm-studio.js` / `C.vrmStudio` for lighting and framing.
  - Features: VRM 0.x/1.0 pack editing; Sentinel vs Custom clip library; real 3D
    analytic 2-bone IK with elbow pole swivel; Head/Gaze & Hand IK `Target Attach`
    (D7); 30-bone finger presets and per-segment sliders (reusing `(1769)=a`
    `FINGER_BONES`/`FINGER_PRESETS`); expression blend; kit prop offsets; mini
    timeline of up to 8 keyframes; real `220/280/360px` slot and Dark/Light framing
    overlay (reusing `C.vrmStudio.solveBustFraming` and lighting presets); live
    `에이전트 노출` prompt catalog preview rendered by the same formatter rules as D5;
    Save to Pack / Import `.vrma` / Export JSON; and a Settings button that opens the
    studio for the active pack.
- **D7 — Target Attach space constraint (owner):** Each keyframe may carry
  `target_attach` with independent `gaze` (eyes), `head`, `hand_l`, `hand_r` entries,
  each `{ space, offset?: [x,y,z] (metres, |each| ≤ 1), weight?: 0..1 (default 1) }`.
  `space` grammar: `free | camera | prop:<id>#<anchor> | bone:<humanoidBone>`
  (`<id>`/`<anchor>` match `^[a-z][a-z0-9_]{0,31}$`; `<humanoidBone>` matches
  `^[a-z][A-Za-z0-9]{1,31}$`). The eye/head split lets the eyes lock on a target while
  the head follows partially. At runtime a non-`free` space replaces the effector or
  look target with the resolved target world position plus the local offset, so kits
  with different bone lengths converge on the same object. Phase 2 resolves
  `free`, `camera`, `bone:*` and `prop:<built-in prop>#<anchor>`; `prop:<pack props[] id>`
  is Stage 2 and resolves as `free` with a one-time console warning.
  - **Phase 2 built-in prop vocabulary** (the only valid `prop:` ids, clip `prop.id`
    values and `calibration.props` keys in Stage 1; the Studio's prop segment maps the
    mockup's `phone`/`laptop`/`none` onto these, and the mockup's `mug` is Stage 2):

    | prop id | source | anchors |
    |---|---|---|
    | `smartphone` | `dashboard-companion-vrm.js:68-70`, (1769)=a `vrm-props.js` held phone + palm grip | `grip` (phone centre the palm grip targets), `screen` (screen-face centre, gaze target) |
    | `laptop` | (1769)=a `vrm-props.js` `laptopGroup` | `keyboard` (home-row centre), `screen` (lid display centre) |

    An unknown built-in id or anchor in a clip validates (forward compatibility) but
    resolves as `free` with a one-time warning. `calibration.props` keys other than the
    built-ins are preserved and ignored until Stage 2 pack props exist.
- **D8 — Reserved future-proof prop schema (owner game-dev prop layer, Stage 2/3):**
  - **Permanent non-goal:** skeletal/skinned mesh clothing replacement.
  - Reserved now, validated (leniently) and preserved byte-for-byte on load and on
    every Studio write; runtime deferred:
    - `pack.json` `props[]` (≤ 16): `{ id, kind: "rigid"|"chain", sockets: { <name>: { bone, offset?, rotation_deg? } }, kit_offsets?: { <socket>: { offset?, rotation_deg? } }, lifecycle?: { equip?, unequip?, enter?, using?: [<action key>...], exit? } }`
      — the 5-state lifecycle names action keys.
    - `.clip.json` keyframe `socket_handoff: { prop, to_socket }` marker and the D7
      `target_attach` schema.
  - A malformed reserved field never refuses the pack: the field is ignored for that
    load, a warning is reported, and its raw bytes stay in `pack.json` (the
    `persona` leniency pattern, `companion_pack_schema.go:98-135`).
- **D9 — `.clip.json` v1 contract (technical, recovers D1/D7/D8):**

  ```json
  {
    "schema": "ws-companion-clip/v1",
    "key": "phone_peek",
    "duration_s": 1.4,
    "loop": false,
    "easing": "ease_out_back",
    "prop": { "id": "smartphone", "offset": [0, 0, 0], "tilt_deg": 0 },
    "keyframes": [
      {
        "t": 0.35,
        "label": "act",
        "easing": "ease_in_out",
        "expression": { "happy": 0.7, "half_lid": 0.2 },
        "head": { "yaw_deg": 0, "pitch_deg": 4, "roll_deg": 3, "lean_deg": -4, "turn_deg": 0 },
        "hands": {
          "left":  { "ik": [0.42, -0.55, 0.38], "pole": [-0.6, -0.4, -0.7],
                     "fingers": { "preset": "relaxed", "curl": [0.16, 0.18, 0.14, 0.20, 0.24, 0.14, 0.28, 0.32, 0.18, 0.34, 0.38, 0.22, 0.40, 0.42, 0.26] } },
          "right": { "ik": [-0.30, -0.10, 0.45], "pole": [0.6, -0.4, -0.7],
                     "fingers": { "preset": "phone-grip", "thumb_opp": 0.6, "spread": 0.1 } }
        },
        "target_attach": {
          "gaze":   { "space": "prop:smartphone#screen" },
          "head":   { "space": "camera", "weight": 0.4 },
          "hand_r": { "space": "prop:smartphone#grip", "offset": [0, 0.01, 0] }
        },
        "socket_handoff": { "prop": "smartphone", "to_socket": "hand_r" }
      }
    ]
  }
  ```

  - `hands.<side>.ik` is the wrist effector in **chest-local space in units of that
    side's arm chain length** (upper arm + lower arm); `pole` is a chest-local
    direction (normalized on load). The runtime multiplies by the current kit's arm
    length, which is what makes a clip retarget across kits; `target_attach` then
    pins exact contact. `curl` is 15 radians per hand in `(1769)=a` `FINGER_BONES`
    order, each clamped to `[-0.5, 1.8]`; `preset` alone is allowed; when both are
    present `curl` wins. Optional `thumb_opp` (radians, −0.5–1.5) and `spread` (radians, −0.3–0.5) mirror the
    (1769)=a preset fields (`vrm-pose.js:35-55` on that branch); absent, they take the
    named preset's values, or `relaxed`'s when only `curl` is given.
    `socket_handoff` is preserved and ignored at runtime in Stage 1 (D8).
  - Bounds: file ≤ 256 KiB UTF-8 JSON object; `key` equals the file stem;
    `duration_s` 0.1–30; `keyframes` 1–32 with `t` strictly ascending within
    `[0, duration_s]` (the Studio UI edits at most 8); `expression` ≤ 8 entries,
    keys `^[a-z][a-z0-9_]{0,31}$`, weights 0–1; all numbers finite; `label` ≤ 32
    runes without control characters. Unknown `easing` values read as `linear`.
    Unknown fields anywhere are preserved (the Studio round-trips them).
  - A newer `schema` value or a file failing validation makes **that clip**
    unavailable (procedural fallback + pack warning), never the pack.
- **D10 — Authoring Write API (technical, recovers the owner's Phase 1 route list):**
  - Routes (all additive, registered next to the pack routes):
    - `GET /api/companion/packs/{id}/clips/{file}` — serves `<key>.clip.json`
      (`application/json`) or `<key>.vrma` (`model/gltf-binary`) from snapshot memory
      with the same `?rev=`/ETag/`sandbox` CSP logic as `serveCompanionPackFile`.
    - `PUT /api/companion/packs/{id}/clips/{key}` — body
      `{ "clip": <.clip.json object>?, "entry": <actions entry or sentinel entry> }`;
      creates or replaces `clips/<key>.clip.json` and the `actions`/`sentinels` entry.
      A `PUT` whose `entry.kind`/`file` changes kind removes the other-kind file after
      `pack.json` commits.
    - `DELETE /api/companion/packs/{id}/clips/{key}` — removes the entry, then the file:
      `pack.json` is rewritten first (so a crash leaves only an orphan file the loader
      never reads), then `clips/<key>.*` is unlinked; an unlink failure is reported as a
      warning, not a failed delete. Unknown key → 404.
    - `POST /api/companion/packs/{id}/clips/import-vrma?key=<key>` — raw `.vrma`
      body ≤ `companionVrmaAssetCap` (4 MiB), validated by `validateCompanionVRMA`,
      written to `clips/<key>.vrma` with an `actions` entry `kind: "vrma"`,
      `expose_to_agent: false`, empty description. A key that already exists (any
      kind, `actions` or `sentinels`) → 409 unless `&replace=1`, which replaces the file
      and keeps the existing entry's other fields **except** `expose_to_agent`, which is
      reset to `false` unless the request also sets `&expose=1` (so a description
      inherited from an uploaded zip never reaches `<<<ACTIONS>>>` without an explicit
      Studio expose toggle, where the inherited description is shown before saving);
      a sentinel key is accepted only with
      `&sentinel=1` (writes `sentinels.<key>` instead of an `actions` entry).
    - `PUT /api/companion/packs/{id}/calibration` — replaces the `calibration` object
      (`{ props: { <propId>: { offset?: [x,y,z] (|each| ≤ 0.5 m), rotation_deg?: [x,y,z], scale?: 0.25–4 } } }`,
      unknown subkeys preserved).
    The literal `import-vrma` cannot collide with a key (keys have no `-`).
  - Gate: method check, then `TrustedWrite` (404 non-local, 403 cross-origin), the
    `companionTrustedPost` order extended to `PUT`/`DELETE`. Requests carry
    `If-Match: "<pack rev>"`; a missing `If-Match` answers 428, a stale revision 412
    with `X-Companion-Current-Rev`, so two Studio tabs cannot silently overwrite each other.
  - **Concurrency and on-disk edits:** a write takes `installMu` then `scanMu` (the
    documented order, `companion_pack_store.go:41-42`) and holds `scanMu` from the
    first disk write through the snapshot swap, like install's `swapAndLoad`
    (`companion_pack_install.go:193-194`), so a concurrent `reload` can neither store
    a stale scan over the write nor interleave. Under the lock the handler re-reads
    `pack.json` once (counted in the file-op budget) and compares it with the
    snapshot's raw manifest bytes; a difference (a hand edit since the last scan)
    answers 409 "pack.json changed on disk; reload the pack" and writes nothing, so a
    Studio save never silently reverts a person's edit to this Type 5 file.
  - Target rules: the pack must be a loaded authorable (`Origin == nil || Origin.Kind == "local"`)
    `ws-companion-vrm/v1` pack; `default` → 409; 2D → 409; a pack with a sidecar whose `Kind` is not `"local"` (installed from
    a remote source, replaced wholesale by updates) → 409 "packs installed from a source
    are replaced on update; import the .vrm as a local pack to author clips". Hand-made
    packs and `importVRM`/zip-upload packs (`Kind: "local"`) are authorable.
  - Path safety: file names are derived **only** from the validated key
    (`clips/<key>.clip.json`, `clips/<key>.vrma`), never from request names; keys
    matching `companionReservedName` (Windows device names) are refused; the catalog
    `file` field must equal the derived name. A symlink, junction or non-directory
    pack directory or `clips` refuses the write. The write path itself is pinned, not
    only checked (`openCompanionPackEntry` is read-only, and
    `wsstate.AtomicReplaceFile` is path-based):
    - **Unix:** open the pack directory from the pinned companions root, then `clips`
      (`mkdirat` on first save), each with `O_NOFOLLOW|O_DIRECTORY`; create the temp with
      `unix.Openat(clipsFD, tmp, O_CREAT|O_EXCL|O_NOFOLLOW|O_WRONLY, 0o644)`, `fsync`, then
      `unix.Renameat(clipsFD, tmp, clipsFD, name)`; `pack.json` likewise relative to the
      pinned pack-directory fd. No path string is resolved after the pins.
    - **Windows:** verify the pack directory and `clips` carry no reparse point through
      the existing `companion_pack_file_windows.go` handle check, write the temp
      `O_EXCL` and replace with `wsstate.AtomicReplaceFile`, then reopen the result
      through the pinned check and compare its file identity
      (`GetFileInformationByHandle` volume serial + file index) with the written temp;
      a mismatch removes the result and fails the write. The remaining
      check-to-use window equals the existing install path's (`writeStaged`/`swapIn`
      are path-based too) and needs local write access to the config home; it is
      recorded here as an accepted residual, not claimed closed.
  - Caps: `actions` ≤ 64 entries, `.clip.json` ≤ 256 KiB, `.vrma` ≤ 4 MiB, and the
    pack stays within `companionVrmPackCap` (40 MiB) and the `companionCacheCap`
    (128 MiB) budget; an over-cap write is refused before any file is touched.
  - `pack.json` rewrite: decode to `companionObject`, replace only the edited
    `actions` element (other elements keep their raw bytes), `sentinels` key, or
    `calibration`, and re-marshal. Values of every other key, including reserved
    `props`, unknown extensions and `persona`, are preserved byte-for-byte as
    `json.RawMessage`; only top-level key order and whitespace may normalize.
  - Write order and failure: clip file first, then `pack.json`; if the `pack.json`
    replace fails, the new clip file is removed (or the previous file restored), and
    the snapshot is untouched. An unchanged entry does not rewrite `pack.json`
    (Rule 2). Writes hold `installMu` (serializing with install/uninstall/import).
  - Reload without rescan: after a successful write the store builds the updated
    `companionPack` from bytes already in memory (old assets + new bytes), revalidates
    it, recomputes `Rev` (clip bytes are hashed in, so `?rev=` caches invalidate), and
    swaps a copy-on-write snapshot sharing every other pack — no directory rescan and
    no re-read of other packs (Rules 1, 4, 5).
  - **Revision stability:** clip parts are hashed into `Rev` only when at least one
    clip loaded, each under a `clips/`-prefixed part name appended after today's
    parts, so a pack without clips keeps a byte-identical `Rev` to today's formula and
    install's `loaded.info.Rev == staged Rev` check (`companion_pack_install.go:562`)
    keeps passing for `archive.prepare`/`importVRM` packs.
  - Install interplay: an install/update that would replace a `Kind: "local"` pack
    whose loaded snapshot carries authored content (`actions`, `sentinels`,
    `calibration`, or loaded clips) is refused for that pack with "this pack has
    Studio-authored clips; export them and uninstall it first" (decided from memory,
    no extra file touch). Uninstall still removes the directory (explicit user action).
- **D11 — Compatibility tier: T0 transparent (`ai-docs/manuals/release-compatibility.md`):**
  - **Old binary reads a new pack directory** (`actions`, `sentinels`, `calibration`,
    `props` keys + `clips/`): the VRM validator reads only its nine known keys
    (`companion_pack_vrm.go:76-80`, `companion_pack_schema.go:169-187`), returns only
    flat model/thumbnail/`animations` files, and the loader reads only those
    (`companion_pack_store.go:298-335`), so the pack loads normally; `clips/` is never
    opened; requested custom gestures fall back to procedural motions. **T0.**
  - **Old binary writes:** it never rewrites `pack.json` outside install, and its
    `/api/companion/packs/reload` only re-reads. **T0.**
  - **New routes and page** (`/api/companion/packs/{id}/clips/...`,
    `/api/companion/packs/{id}/calibration`, `/companion/studio`, `/s/companion-rig-studio-<hash>.mjs`)
    are additive; their only client is the frontend embedded in the same daemon binary,
    so no older client calls them and no existing route changes. **T0.**
  - **`ws/companion.act`:** arguments, schema and handler are unchanged; custom keys travel
    in the existing free-form `gesture` string (`companion_tools.go:258-273,548-557`,
    `companion_act.go:311-319`); an older daemon simply plays its procedural fallback for
    an unknown key. Only the per-wake envelope text and one launch-prompt line change. **T0.**
  - **New binary reading future data:** unknown fields are preserved, a newer clip
    `schema` or unknown `kind` disables only that clip/entry. No old data becomes
    unreadable (no T4).
  - **Disclosed residual (downgrade-only, user-initiated):** on a downgrade to an
    older daemon, explicitly re-uploading a zip with the same pack ID as an authored
    `Kind: "local"` pack replaces the directory (including `clips/`), exactly as that
    older daemon already does for any file inside an installed pack
    (`companion_pack_install.go:707-727`). Hand-made packs are never replaced by any
    version (`:591-597`). The new binary refuses this replacement (D10). This path
    needs a deliberate same-ID re-upload during a downgrade and touches no other data.
    **Classification:** the release-compatibility tier table (`release-compatibility.md:56-62`)
    does not grade this as T0 or T2, because files are lost with no way to rebuild them. The ticket's
    position is that it is a *user-requested replacement*: the same semantics as an explicit
    uninstall, which every version honours, not an old binary silently overwriting
    data it does not understand. The overall T0 grade stands with the owner's explicit
    acknowledgement of this downgrade-only residual: **owner `(1786)=a` (2026-11-28,
    "네, 미배포니까 완화")**: the feature is unreleased, and the downgrade-only explicit
    same-ID zip re-upload replacement is accepted under T0, the same pattern as
    row 12 (owner `(92)=a`). Phase 1 adds a scenario-sheet row to
    `ai-docs/manuals/release-compatibility.md` recording both the T0 read path and
    this residual, with its owner decision reference `(1786)=a`.
  - **Distribution note:** zip/marketplace distribution of clips is not added; an older
    daemon would refuse a zip carrying `clips/` (row 14 of the scenario sheet), and the
    new daemon keeps refusing nested zip paths too.
- **D12 — Phase split (technical):** Phase 1 is Go-only (pack store, routes, prompt) and
  touches none of the `(1769)=a` frontend files, so it runs immediately in parallel.
  Phase 2 is frontend + studio bundle and **starts only after `(1769)=a` merges to
  develop**, building on its `vrm-pose.js` / `vrm-studio.js` / `vrm-props.js` modules
  rather than re-splitting `dashboard-companion-vrm*.js`.

## Constraints

- **Implementation convention manuals:**
  - Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for `agents-plugin/`, `agents-plugin-wsflow/`, `agents-plugin-tool/`) — prompt text, tool descriptions and error strings added here must not name this repository's tickets, commits or layout.
  - Convention: ai-docs/manuals/performance.md (declared for `agents-plugin-tool/`)
  - Convention: ai-docs/manuals/release-compatibility.md (declared for `agents-plugin-tool/internal/`)
  - Convention: ai-docs/manuals/dashboard-style.md (declared for `agents-plugin-tool/internal/wsdashboard/assets/`, `ai-docs/assets/mockups/`)
  - Convention: ai-docs/manuals/config-settings-ui.md (declared for `agents-plugin/dashboard-config-manifest.json`, `agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings*`)
  - Reference: ai-docs/manuals/companion-pet-rigging.md (living companion spec; record the clip/catalog/Target Attach contract there when Phase 2 lands)
  - Convention: ai-docs/manuals/ws-mcp.md (declared for `agents-plugin-tool/internal/mcp/`)
- **Storage type and file-touch budget (`performance.md`):** pack files are Type 5
  (plain files people read directly). Reads come only from the snapshot (Rule 1);
  warm requests and per-wake prompt builds touch no file. A single-clip Save performs
  at most: pinned pack-directory open, `clips` open (+ `mkdirat` on first save), one
  `pack.json` drift re-read, clip temp create/write/fsync/close/rename, and `pack.json`
  temp create/write/fsync/close/rename — **≤ 16 file operations**, counted
  through the store's `trace` hook, with zero directory listings and zero re-reads of
  other packs. A keyframe-only edit whose catalog entry is unchanged skips the
  `pack.json` rewrite. `.vrma` import adds only its own file write.
- **Frontend minimal updates (`performance.md`):** the studio pauses its render loop
  when hidden or idle (no orbit/drag/playback for 2 s) and renders on demand; the
  dashboard clip sampler runs only while a clip action is active. No new frame
  scheduling on an idle dashboard.
- **Dashboard style (`dashboard-style.md`):** borderless tonal surfaces, canonical
  `--wb-*` tokens, 24 px icon buttons with tooltips, `WsHoldConfirm` for clip delete,
  Dark/Light parity, `tabular-nums` on the timeline.
- **No new MCP tool, config key or manifest field.** The Settings entry is a button,
  not a config key, so `dashboard-config-manifest.json` and `runtime.json` are untouched.
- **Ownership boundary with `(1769)=a`:** Phase 2 extends, never rewrites, the
  `(1769)=a` modules; new playback/IK code goes into new files
  (`dashboard-companion-vrm-clip.js`, `dashboard-companion-vrm-ik.js`) wired by small
  hooks in `dashboard-companion-vrm.js`.

## Prior Decisions

- 261126-feat-ws-companion-vrm-pack-store (2026-11-26, Decisions): `ws-companion-vrm/v1` reads known keys and keeps raw manifest bytes; `.vrma` capped at 4 MiB and validated by `VRMC_vrm_animation` — bearing: constrains
- 261128-feat-ws-companion-soul-memory-and-pacing (2026-11-28, D7/D9): `ws/companion.act` reuses the `companion-command` rail with free-form beat fields; Tier-1 receives a stable launch prompt plus a fresh per-wake delimiter-quoted, rune-budgeted envelope — bearing: constrains (ACTIONS rides the per-wake envelope)
- 261128-feat-ws-companion-visual-polish-and-metafiction-overlay (2026-11-28, Phase 1/2 Results, branch only): procedural co-lead pose is pure FK with "zero 2-bone IK"; studio lighting/framing lives in `dashboard-companion-vrm-studio.js` — bearing: constrains (clip playback adds analytic IK only while a clip with IK targets is active; the authoring tool takes the `rig-studio` name)
- 261122-feat-ws-companion-pack-store (2026-11-22): pack snapshot is immutable and swapped atomically; readers never wait on disk — bearing: supports (copy-on-write single-pack swap)

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | Phase 1: `internal/wsdashboard/` new `companion_pack_clips.go` (schema + validation + catalog), `companion_pack_clip_routes.go` (write API + clip serving), edits to `companion_pack_store.go`, `companion_pack_vrm.go`, `companion_pack_routes.go`, `companion_pack_install.go`, `companion_soul_prompt.go`, `companion_phone.go`; `internal/mcp/companion_tools.go` (description only). Phase 1 also: `companion_pack_file_unix.go`, `companion_pack_file_windows.go` (pinned write helpers), `ai-docs/manuals/release-compatibility.md` (scenario row). Phase 2: `scripts/build-companion-vrm-runtime.mjs`, new `companion_rig_studio.go` (+ template), `assets/dashboard-companion-rig-studio.{js,css}`, `assets/dashboard-companion-vrm-{clip,ik}.js`, hooks in `dashboard-companion-vrm.js`, `dashboard-companion-vrm-colead.js` (sentinel beats), `dashboard-companion-stage.js` (live refresh), `pages.go`, `page_routes.go`, `daemon_http.go`, `dashboard-settings-companion.js`, `internal/wsi18n/tables/*.json` |
| scope.surface | cross-module | pack manifest schema (additive keys), new HTTP write/read routes, new page and runtime bundle, Tier-1 per-wake prompt text, frontend VRM playback |
| scope.new_public_symbol | yes | HTTP routes `/api/companion/packs/{id}/clips/...`, `/api/companion/packs/{id}/calibration`, `/companion/studio`; Go `companionAction`/`companionClip` types, `CompanionEquippedProp`, prompt constants; JS `C.vrmClip`, `C.vrmIk` |
| scope.new_type_contract | yes | `ws-companion-clip/v1` file format, `actions`/`sentinels`/`calibration`/`props` manifest keys, `target_attach` space grammar, `socket_handoff`, `<<<ACTIONS>>>`/`<<<EQUIPPED>>>` envelope blocks |
| scope.test_surface | new-files | Go `companion_pack_clips_test.go`, `companion_pack_clip_routes_test.go`, prompt tests in `companion_soul_prompt_test.go`; Node `frontend-companion-vrm-clip.test.js`, `frontend-companion-rig-studio.test.js`; Playwright `companion_rig_studio_browser_test.go` + `testdata/companion-rig-studio.browser.cjs` |
| complexity.reuse_points | confirmed | `companionObject` exact-key reads (`companion_pack_schema.go:169-187`), `openCompanionPackEntry` (`companion_pack_file_unix.go:15-30`), `companionTrustedPost`/`TrustedWrite` (`companion_pack_install_routes.go:40-57`), `serveCompanionPackFile` (`companion_pack_routes.go:84-143`), `validateCompanionVRMA` (`companion_pack_vrm.go:237-250`), `companionText`/`companionReservedName` (`companion_pack_zip.go:385,675`), `companionQuote` (`companion_director.go:869`), `C.vrmRuntimeLoader` (`dashboard-companion-vrm.js:115-128`), `ensureVrmaClip` (`:833-861`), `(1769)=a` `C.vrmCoLeadPose`/`C.vrmStudio` |
| complexity.side_effect_risk | moderate | Studio writes into user pack directories; mitigated by key-derived names, pinned `clips` open, `If-Match`, atomic replace with rollback, and refusal on remote-sourced packs |
| risk.correctness | moderate | copy-on-write snapshot swap vs concurrent install; raw-preserving `pack.json` rewrite; IK retarget/target-attach convergence across VRM 0.x (`rotateVRM0`) and 1.0 |
| risk.fit | moderate | Phase 2 must sit on top of `(1769)=a` once merged; the `rig-studio` naming avoids the `C.vrmStudio` collision |
| risk.test | moderate | real-VRM Playwright QA for both VRM versions at three slot widths and two themes; deterministic file-op counting |
| risk.security_or_contract | high | path traversal / symlink / Windows device names in a new write API, prompt-injection surface in agent-visible descriptions, T0 manifest additivity |

## Phases

### Phase 1: Pack Clip Schema, Authoring Write API, Reserved Schema Preservation & Dynamic `companion-body` Action Catalog (Go only)

Can start immediately; touches no `(1769)=a` frontend file.

1. **Schema and loading (`companion_pack_clips.go`, `companion_pack_vrm.go`, `companion_pack_store.go`):**
   - Parse `actions`, `sentinels`, `calibration`, `props` from `ws-companion-vrm/v1`
     manifests per D3/D8/D10 with entry-level leniency (a bad entry or reserved field
     is dropped with a warning; the pack still loads). Keep the validator's returned
     `files` flat-only; load clip files through a separate step that opens `clips`
     as a pinned directory and then each `<key>.clip.json`/`<key>.vrma` the catalog
     names (no listing of `clips/`), validating per D9 and `validateCompanionVRMA`.
   - Store the parsed catalog, sentinel map and clip bytes on `companionPack`, count
     clip bytes in `pack.size`, hash them into `Rev`, and expose warnings as an
     additive `clipWarnings` field on `companionPackInfo` (omitted when empty).
2. **Write API (`companion_pack_clip_routes.go`, `companion_pack_routes.go`):** the D10
   routes, gate (405/404/403, 428 missing / 412 stale `If-Match`), target rules, pinned
   write path (`companion_pack_file_unix.go` / `companion_pack_file_windows.go` gain the
   create/rename-relative helpers), caps, on-disk-drift 409, raw-preserving rewrite,
   rollback, DELETE order, import-vrma collision rules, and the copy-on-write
   single-pack swap under `installMu` → `scanMu`.
3. **Install interplay (`companion_pack_install.go`):** refuse replacing an authored
   `Kind: "local"` pack per D10, decided from the in-memory snapshot.
4. **Dynamic catalog (`companion_soul_prompt.go`, `companion_phone.go`, `internal/mcp/companion_tools.go`):**
   `companionActivePackID()`, the `<<<ACTIONS>>>` block with D5 caps, sanitization,
   `TotalRunes` semantics and format (loaded+validated local entries only), the
   reserved `<<<EQUIPPED>>>` slot (empty → absent), the preamble and launch-prompt
   line updates, and the `ws/companion.act` `gesture` description wording.
5. **Release-compatibility record (`ai-docs/manuals/release-compatibility.md`):** add a
   scenario-sheet row for "VRM pack with `actions`/`sentinels`/`calibration`/`props` +
   `clips/` meets an older daemon" grading the read path T0 and recording the D11
   downgrade-only same-ID re-upload residual with the owner acknowledgement reference `(1786)=a`.
6. **Verification:**
   - `go test -race ./internal/wsdashboard -run 'TestCompanion(Pack|Clip|Action|Soul|Wake|BuildCompanion)'` covering:
     - Old-binary simulation: `validateCompanionVRMManifest` on a manifest carrying all new keys loads with identical `files` (only flat assets), and the raw `pack.json` is served byte-identical.
     - Clip schema accept/refuse table (bounds, ascending `t`, `key` ≠ stem, newer `schema`, unknown `easing` → linear, unknown fields preserved, malformed `target_attach` space, `socket_handoff`).
     - Lenient reserved fields: malformed `props`/`calibration` → warning, pack loads, raw bytes intact after a Studio write.
     - Sentinel/action rules: unknown sentinel key dropped; `actions` key equal to a sentinel dropped; duplicate keys keep first; 65th entry dropped with one warning; entry `loop`/`duration_s` precedence over the clip file.
     - Route security: 404 non-local, 403 cross-origin, 405 method, 428 missing and 412 stale `If-Match`, 409 for `default`/2D/remote-sourced packs, 413 over-cap bodies, refusal of keys like `con`/`../x`/`Idle`, refusal when the pack directory or `clips` is a symlink (unix test; the junction test is a `windows` build-tagged test that runs only on a Windows runner and is compile-checked here).
     - Route contract: DELETE rewrites `pack.json` before unlinking (injected unlink failure → success + warning, loader ignores the orphan); unknown key DELETE → 404; import-vrma on an existing key → 409, `&replace=1` replaces the file and resets `expose_to_agent` to false unless `&expose=1`, sentinel key needs `&sentinel=1`.
     - Atomicity: injected `pack.json` replace failure leaves the old clip file and snapshot intact; unchanged entry does not rewrite `pack.json`.
     - On-disk drift: hand-editing `pack.json` after load makes the next PUT answer 409 and write nothing.
     - Revision stability: a pack without clips keeps the exact pre-ticket `Rev` (golden value); adding a clip changes it.
     - File-op budget: single-clip Save ≤ 16 traced operations (including the drift re-read), zero directory listings, other packs' `*companionPack` pointers unchanged after the swap.
     - Concurrency under `-race`: parallel PUTs; PUT vs install; PUT racing a `reload` never leaves the pre-write pack in the snapshot (the reload is held on `scanMu` and then sees the new bytes).
     - Install refusal for an authored local pack; hand-made pack still never replaced.
     - Prompt: `<<<ACTIONS>>>` lists exposed entries only, and only those whose clip loaded and validated (an exposed entry with a missing file is absent); a remote-sourced pack (sidecar `Kind` ≠ `local`) contributes no lines while a hand-made pack (`Origin == nil`) and a `Kind: "local"` pack do; caps at 24 with `(+N more not shown)`; drops lines whole within 3200 runes; a tight `Budget.TotalRunes` (e.g. 420) leaves ACTIONS intact and SOUL/MOOD/RECALL scaling unchanged; descriptions with `Cf`/`Zl`/`Zp` characters are refused at write and not exposed at load; `[`/`]` replaced; `<<<`/`>>>` neutralized; never lists sentinels or legacy `animations`; renders `(none)` for an empty catalog; `<<<EQUIPPED>>>` absent when empty; a pack switch changes the next envelope.
   - `go test -race ./internal/mcp -run 'TestCompanion'` (act schema unchanged apart from the description; free-form custom key passes through).
   - `GOOS=windows go vet ./...` and `GOOS=windows go test -c ./internal/wsdashboard` (compile check of the Windows pinned write path and the junction test).

### Result (a1f0aab88) - 2026-11-28

Phase 1 (Go backend) landed as specified, with the deviations and decisions below.

- **Windows pinned write path (deviation, stronger than D10):**
  - Writes are fully handle-relative: `NtCreateFile` with `RootDirectory` + `OBJ_DONT_REPARSE`, a reparse-attribute check on every handle, and `wsstate.RetryRenameFileAt` / POSIX delete by handle.
  - The ticket's `AtomicReplaceFile` + identity comparison is not used, so the D10 "accepted residual" check-to-use window no longer exists.
  - The junction test (`companion_pack_clip_routes_windows_test.go`) is compile-checked here and runs only on a Windows runner.
- **Write ordering:**
  - The next pack (Rev, catalog, clips) is rebuilt from memory before the first file operation. A rebuild failure answers 409 with disk and snapshot untouched.
  - Rewritten files keep `pack.json`'s permission bits (unix).
  - On a failed `pack.json` replace, a previous clip copy that had loaded is restored. A previous copy that never loaded (invalid or over budget) was already replaced and is reported as gone in the error message.
- **Duplicate raw keys:** the loader keeps the first `actions` element with a key. PUT replaces the first and drops the shadowed duplicates; DELETE removes every element with the key.
- **Authored detection:** an install also treats a pack as authored when `calibration` or `props` is present but malformed, because the raw bytes are preserved.
- **Warnings:** each `clipWarnings` line is capped at 160 runes and the list at 32 lines, because warnings embed manifest-controlled text. Phase 2 must render them as text.
- **Accepted limit (drift scope):** the on-disk drift check covers `pack.json` only. A hand edit to a clip or flat asset since the last scan is not detected; the Studio write publishes a Rev hashed from the in-memory bytes, and the next reload re-hashes disk. Revision stability holds for the Studio-only flow.
- **Prompt:**
  - ACTIONS is omitted when the catalog is nil and rendered as `(none)` when empty.
  - Hand-made packs (`Origin == nil`) and `Kind: "local"` packs contribute lines; remote-sourced packs contribute none.
  - Rune budgets: `companionCoreEnvelopeMaxRunes = 3600`, `CompanionEnvelopeMaxRunes = 6800`.
  - The preamble now names ACTIONS, which is 9 runes longer. Under a scaled `TotalRunes`, SOUL/MOOD/RECALL therefore shrink by up to 9 runes compared with before. This is inherent to the mandated preamble change, and the existing exact-output scaling tests pass.
- **import-vrma:** `&replace=1` resets `expose_to_agent` to false unless `&expose=1` is set (sage minor #2).
- **File-op budget:** a first single-clip save traces 16 ops (read=1, mkdir=1) and a steady-state save traces 14 (rename=2). No directory is listed.
- **Revision:** a clipless Rev is unchanged, pinned by the golden `98cef2ef6ba4d80a768880e3640f789f`, which equals the legacy formula.
- **Verification:** all of the following passed.
  - `go test -race ./internal/wsdashboard -run 'TestCompanion(Pack|Clip|Action|Soul|Wake|BuildCompanion)'`
  - `go test -race ./internal/wsdashboard -run 'Companion|Ambient|Soul|Wake|Envelope'`
  - `go test -race ./internal/mcp -run TestCompanion`
  - `go vet ./...`, `GOOS=windows go vet ./...` and `GOOS=windows go test -c ./internal/wsdashboard`
  - No embedded assets changed, so `generate-package-assets.py --check` was not needed.
  - The daemon-envelope ACTIONS test was mutation-checked: dropping `Actions:` from `BuildCompanionWakeEnvelope` fails it.
- **Code review (claude.medium):** CONCERN with no Critical findings.
  - Both Important items were fixed: the daemon envelope and pack-switch test, and the rebuild moved before any write.
  - Minor fixes: duplicate keys, warning cap, reserved-presence authored, mode preservation, tighter key-refusal assertions, no `t.Fatal` off the test goroutine, a check for leftover `pack.json` temp files, an unauthored-local-pack-still-replaceable test, and old-binary citations in release-compatibility row 18.
  - The remaining minors are recorded above (drift scope, Windows deviation, preamble runes).

### Phase 2: Isolated Studio Bundle, `/companion/studio` Control-Rig UI, Runtime Clip Resolver, Settings Entry & Real-VRM QA

Depends on Phase 1's Result **and** on `(1769)=a` (`261128-feat-ws-companion-visual-polish-and-metafiction-overlay`) being merged to develop.

1. **Studio runtime bundle (`scripts/build-companion-vrm-runtime.mjs`, `companion_rig_studio.go`):**
   a second esbuild entry producing `assets/companion-rig-studio-runtime.mjs`
   (main exports + `OrbitControls`, `TransformControls`) under the same pins, its own
   360 KiB gzip gate, embedded and served content-hashed at
   `/s/companion-rig-studio-<hash>.mjs` behind `accessGuard`. The main
   `companion-vrm-runtime.mjs` must come out byte-identical.
2. **Runtime resolver (`assets/dashboard-companion-vrm-clip.js`, `assets/dashboard-companion-vrm-ik.js`, hooks in `dashboard-companion-vrm.js` and `dashboard-companion-vrm-colead.js`, refresh listener in `dashboard-companion-stage.js`):**
   D4 resolution order for sentinel states (D3 mapping table, hooked at the co-lead
   `triggerGesture`/`setPhoneState`/knock beats) and `act` gestures, on the existing
   stage tick with the D4 looping-clip cadence rule; the D6 `BroadcastChannel`
   live-refresh listener calling `packsChanged()`; `.clip.json` sampler
   (keyframe interpolation with per-keyframe easing, expression blend through the
   existing expression controller, finger curls through `C.vrmCoLeadPose`
   presets/order, analytic 2-bone IK with pole swivel on kit-scaled targets, D7
   target attach); `.vrma` through the existing `ensureVrmaClip` path extended to
   `clips/<key>.vrma`; one cross-faded playback path; `calibration` prop offsets via
   `C.vrmCoLeadProps`; VRM 0.x handled through the existing `conjugateVrm0*`
   helpers. `vrmVocabulary` adds catalog keys. Both files join `companionScriptFiles`
   (needed for dashboard playback); nothing studio-only does.
3. **Studio page (`/companion/studio`: `companion_rig_studio.go` handler + template + CSP, route in `daemon_http.go`, `assets/dashboard-companion-rig-studio.{js,css}`):**
   the D6 hosting (top-level tab, `localBrowseViewer` gate, nonce CSP) and feature set laid out per the D2 mockup, Target Attach inspector (space picker over the D7 built-in prop/anchor table,
   eye/head split, local offset, weight), kit prop offsets writing `PUT .../calibration`,
   Save to Pack (one `PUT` per dirty clip with `If-Match`; a 412 offers reload),
   Import `.vrma`, Export JSON, `WsHoldConfirm` delete, unknown-field round-trip,
   the agent tab preview using the D5 caps/format/sanitization, a 409 on-disk-drift
   prompt to reload, and the post-save `BroadcastChannel` notification.
4. **Settings entry (`dashboard-settings-companion.js`):** an "Open Studio" icon button in
   `renderAvatarGroup` for an authorable active VRM pack (disabled with a tooltip for
   `default`, 2D, or remote-sourced packs) opening `/companion/studio?pack=<id>` in a new tab.
5. **i18n:** all new strings as `js.companion.rig_studio.*` / `js.settings.companion.*`
   keys in all 11 locale tables.
6. **Verification:**
   - Node: `node --test ./internal/wsdashboard/frontend-companion-vrm-clip.test.js ./internal/wsdashboard/frontend-companion-rig-studio.test.js` — resolver order and fallbacks, sampler interpolation/easing, IK solve (reach clamp, pole swivel), target-attach convergence for two arm lengths (same world contact within 5 mm), unknown-field round-trip, 8-keyframe cap, catalog preview parity with the Go formatter fixture.
   - Node (sentinel mapping): each of the seven sentinels plays its override at the mapped co-lead beat with the table's loop/one-shot default; `knock_startle`/`wakeup_jolt` clip `duration_s` replaces the `KNOCK_*_DURATION` beat length; `act(gesture: "phone_typing")` resolves to the override; a looping `idle` override never calls `markVrmActivity`.
   - Bundle: the build script passes both gates; a Go test pins the SHA-256 of the embedded `companion-vrm-runtime.mjs` to its pre-ticket value; a Go test asserts no `rig-studio` file appears in `companionScriptFiles` or `dashboard.tmpl`; a Go test caps `dashboard-companion-vrm-clip.js` + `dashboard-companion-vrm-ik.js` at 24 KiB combined.
   - Studio page: Go tests for `/companion/studio` (non-local viewer → 404, CSP header carries `frame-ancestors 'none'` and a nonce, unknown pack → in-page error state, studio runtime served at its hashed path).
   - i18n parity: `go test ./internal/wsi18n` (all 11 locales carry every new key).
   - Playwright, real Chromium, real VRM files supplied through `WS_VRM0_SAMPLE` (VRM 0.x `6493143135142452442.vrm`) and `WS_VRM1_SAMPLE` (VRM 1.0 `AvatarSample_M2.vrm`); the new studio browser test **fails** (does not fall back to synthetic VRMs) when QA mode is on and either variable is unset, and the Result records that both real files were used. For each: load the studio, author a clip with IK + `target_attach` to `prop:smartphone#grip` and the camera, Save, and — **without reloading the open dashboard tab** — trigger it via a `companion-command` gesture (live `BroadcastChannel` refresh) and via a sentinel override, and assert playback; Import `.vrma` and play it; capture `220/280/360px` × Dark/Light screenshots for both VRM versions; assert the idle dashboard schedules no extra frames while the studio is closed and while a looping `idle` override plays.
   - Side-by-side comparison of a studio capture against `ai-docs/assets/mockups/261128/control-rig-studio/index.html` (rail, viewport segments, timeline, inspector tabs, expose strip) recorded in the Result.
   - `go test -race ./internal/wsdashboard -run 'TestCompanion(RigStudio|Vrm)'`, `GOOS=windows go vet ./...`.

### Result (7f5d37af5) - 2026-11-28

Phase 2 (Isolated Studio bundle, `/companion/studio` Control-Rig UI, runtime clip/IK/Target-Attach resolver, Settings entry, 11-locale i18n, and real-VRM 0.x/1.0 Playwright QA) landed on top of `develop @ c894b8f49` (Phase 1 + `(1769)=a` Phase 1+2).

- **Studio runtime bundle & isolation (`scripts/build-companion-vrm-runtime.mjs`, `companion_vrm_asset.go`, `companion_rig_studio.go`, `page_routes.go`):**
  - Added second esbuild entry `assets/companion-rig-studio-runtime.mjs` exporting `OrbitControls` and `TransformControls` alongside the shared Three/VRM exports, served content-hashed at `/s/companion-rig-studio-<hash>.mjs` behind `accessGuard`.
  - Main `companion-vrm-runtime.mjs` stays byte-identical (SHA-256 `5145d8be88c31867819dfba084ccee1308e4bc93f2f69eb3cc84194af5309b75`, pinned by `TestCompanionRigStudioBundleIsolationAndCaps`).
  - Caps verified: studio runtime gzip `259,720 B` (≤ `368,640 B`); shared playback `dashboard-companion-vrm-clip.js` + `dashboard-companion-vrm-ik.js` = `23,410 B` combined (≤ `24,576 B`). Zero studio-only files appear in `companionScriptFiles` or `dashboard.tmpl`.
- **Runtime clip, IK & Target Attach playback (`dashboard-companion-vrm-clip.js`, `dashboard-companion-vrm-ik.js`, hooks in `dashboard-companion-packs.js`, `dashboard-companion-vrm-props.js`, `dashboard-companion-vrm-colead.js`, `dashboard-companion-vrm.js`, `dashboard-companion-stage.js`):**
  - Implemented D4 resolution order (`actions` / `sentinels` → legacy `animations` → procedural fallback), 7-sentinel mapping with `SENTINEL_LOOP_DEFAULT` (`phone_hold` uses `holdLast`), `knock_startle`/`wakeup_jolt`/`phone_putdown` `duration_s` override, and looping-`idle` cadence preservation (never calls `markVrmActivity`).
  - Implemented `ws-companion-clip/v1` keyframe sampler with per-keyframe easing (`linear`, `ease_in`, `ease_out`, `ease_in_out`, `ease_out_back`, `spring`), analytic 2-bone arm IK with elbow pole swivel and reach clamp, 30-bone finger curls delegating to `C.vrmCoLeadPose`, D7 Target Attach (`free`, `camera`, `prop:smartphone#{grip,screen}`, `prop:laptop#{keyboard,screen}`, `bone:*`; unknown `prop:*` falls back to `free` with a one-time warning), `calibration.props` offsets via `C.vrmCoLeadProps`, and `.vrma` playback via `clips/<key>.vrma`.
  - Added `BroadcastChannel("ws-companion-packs")` live refresh in `dashboard-companion-stage.js` calling `packsChanged()` so open dashboards play newly saved clips without reload.
- **Control-Rig Studio page (`companion_rig_studio.go`, `daemon_http.go`, `assets/companion-rig-studio.tmpl`, `assets/dashboard-companion-rig-studio.{js,css}`, `assets/dashboard-companion-rig-studio-stage.js`):**
  - Top-level `GET /companion/studio?pack=<id>` gated by `localBrowseViewer` (404 non-local) with per-response nonce CSP (`default-src 'none'`, `frame-ancestors 'none'`, `connect-src 'self'`, `script-src 'nonce-...' 'self'`, `style-src 'nonce-...'`), rendering an in-page error state for unknown/non-authorable packs.
  - Full mockup layout and authoring flow: Sentinel/Custom rail, 3D viewport with OrbitControls + TransformControls (Hand IK, Pole, Look, Prop modes), Target Attach space/offset/weight inspector with SVG tether overlay (contrast-stroked labels), 30-bone finger presets + 15 per-segment sliders, expressions, kit prop calibration (`PUT .../calibration`), 8-keyframe timeline + scrubber + playback, 220/280/360px × Dark/Light bust-framing overlay reusing `C.vrmStudio`, live Agent prompt catalog preview matching Go D5 formatting/caps, `If-Match` save with 409/412 reload prompt, `.vrma` import modal, JSON export, `WsHoldConfirm` delete, unknown-field round-trip, idle rAF auto-pause after 2 s, and post-save `BroadcastChannel` broadcast.
- **Settings entry, i18n & living manual (`dashboard-settings-companion.js`, `internal/wsi18n/tables/*.json`, `ai-docs/manuals/companion-pet-rigging.md`):**
  - Added "Open Studio" button in `renderAvatarGroup` (enabled for authorable local VRM packs; disabled with reason tooltip for `default`, 2D, and remote-sourced packs).
  - Added 108 new i18n keys (`js.companion.rig_studio.*` and `js.settings.companion.open_studio*`) across all 11 locale tables (`4454` keys each, 0 duplicates).
  - Documented the clip/catalog/Target Attach contract and Studio architecture in `ai-docs/manuals/companion-pet-rigging.md`.
- **Real-VRM 0.x & 1.0 Playwright QA & side-by-side mockup comparison:**
  - Ran `TestCompanionRigStudioBrowser` under `-race` with real VRM 0.x (`WS_VRM0_SAMPLE=/Users/kang-sw/Downloads/6493143135142452442.vrm`) and VRM 1.0 (`WS_VRM1_SAMPLE=/Users/kang-sw/Downloads/AvatarSample_M2.vrm`) — QA mode fails hard if either env var is unset (no synthetic fallback).
  - Verified end-to-end: studio load (VRM 1.0 `567,606` lit px, VRM 0.x `564,784` lit px), authoring `wave_qa` with IK + `target_attach` (`hand_r -> prop:smartphone#grip`, `gaze -> camera`) + 3rd snapshot keyframe, Save to Pack, live `BroadcastChannel` refresh + `companion-command` playback on open dashboard without reload, `idle` sentinel override playback with `loop: true` and calm idle cadence, `.vrma` import + playback, idle rAF auto-pause (`rafActive === false`), and pack switch to VRM 0.x.
  - Captured and visually inspected all 14 real-VRM screenshots (`220/280/360px × Dark/Light` for both VRM 1.0 and VRM 0.x, plus Agent Expose tab and live dashboard clip playback) against `ai-docs/assets/mockups/261128/control-rig-studio/index.html`: header bar, left sentinel/custom rail, floating mode/slot/prop/theme segments, dashed headroom framing guide, Target Attach tethers, timeline chips/scrubber, Rig/Agent inspector tabs, and pinned bottom expose strip match the approved mockup cleanly in both themes.
- **Verification:**
  - `go build ./...`, `go vet ./...`, `GOOS=windows go vet ./...` — PASS.
  - `go test -race -v ./internal/wsdashboard -run 'TestCompanionRigStudio'` (with real VRM 0.x + 1.0 Playwright QA) — PASS.
  - `go test -race ./internal/wsdashboard -run 'TestCompanion|TestRig|TestControlRig|TestDashboardAssetSourceAssembly|TestPersona|TestSoul'` (non-browser suite) — PASS.
  - `go test -v ./internal/wsdashboard -run '^TestCompanionVrmBrowser$'` (Playwright default synthetic VRMs) — PASS (`activeFps=30.2`, `idleFps=5.99`, `hiddenFrames=0`, 10× VRM↔2D switch soak).
  - `go test -v ./internal/wsdashboard -run 'TestCompanionVrmSwapDizzyBrowser'` (Playwright real VRMs) — PASS.
  - `go test ./internal/wsi18n` + 11-locale JSON duplicate/parity check — PASS (`4454` keys across all 11 tables).
  - `node --test agents-plugin-tool/internal/wsdashboard/frontend-companion*.test.js` — PASS (`123/123`, 0 failures).
  - `python3 agents-plugin-tool/scripts/generate-package-assets.py --check` — PASS.
  - Note: Two unrelated pre-existing Playwright tests on `develop @ c894b8f49` (`TestCompanionSettingsVrmAndThemeBrowser` waiting on legacy `#rail [data-scope-entry="global"]` moved in `9a7b4eb9e`, and `TestCompanionVrmStep4Phase1Browser` filling hidden `.ws-cm-input.wsc-backing`) were left untouched outside Phase 2 scope.
