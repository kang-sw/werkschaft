# Ship: ws

Ships the `ws` / `wsflow` plugin packages as a cut-point release: a
`release/<version>` branch cut at the review frontier head carries the version
bump, `main` fast-forwards to it, and a `v<version>` tag on it triggers the
`ws-mcp release` GitHub Actions workflow (`.github/workflows/ws-mcp-release.yml`)
to build cross-platform `ws-mcp` assets plus signed macOS/Windows Tauri packages and
publish them to the same GitHub release. `develop` keeps taking merges for the whole release; the release branch is merged back
into it afterwards, which keeps `main` an ancestor of `develop`.

## Version Strategy

The Pi retirement cut uses **0.52.0**, the approved T3 minor for the launcher
and tool boundary. The release lead applies that bump at the cut. For other
cuts, use the patch strategy below unless an approved T3 boundary specifies
the minor. A version counts as claimed once its `v<version>` tag exists on
origin. Read the version on the release branch after the Cut and Merge
release target steps, so a
merged `main` brings in the previous release's version: read
`agents-plugin/runtime.json` `.plugin_version` and run
`git ls-remote --tags origin v<current>`:

- Non-empty (the previous ship tagged it): choose the version required for
  this cut, normally the next patch, and run
  `agents-plugin-tool/scripts/bump-ws-version.sh <next-version>`. Commit the
  script-generated version edits on the release
  branch before testing or tagging.
- Empty (an earlier bump reached the cut point but its tag was never pushed):
  reuse `<current>` as this ship's version. No bump script run, no new version
  commit.

Ordinary merges into `develop` do not bump the version. The tag is `.release_tag`
(`v<plugin_version>`). Ship refuses if `.release_tag != "v" + .plugin_version`
for either `agents-plugin/runtime.json` or `agents-plugin-wsflow/runtime.json`
(the workflow enforces the same contract).

## Release Branch

All release-branch work runs in a dedicated worktree, never in the main
checkout: parallel lead sessions share that checkout, and moving its HEAD would
land their commits on the release branch. Steps marked **(lead)** call
lead-only tools (`ws/worktree.*`, `ws/git.merge`) or hand work to
`ws:lead-delegate`, so they run in the lead session; the Execute delegate runs
the rest of Pre-flight in the worktree the lead hands it.

- Cut **(lead)**: first clear what an earlier attempt left, because a leftover
  lease or branch refuses the acquire and the later rename: release each
  `ws/worktree.list` worktree whose branch is `release/*`
  (`ws/worktree.release(path: <worktree>)`), then delete each local
  `release/*` branch (`git branch -D`), `release/pending-<short cut sha>`
  included. Then `ws/worktree.acquire(base: <cut>, target_branch:
  release/pending-<short cut sha>, profile: "full")`; run every
  release-branch command below in the returned worktree. Once Version
  Strategy has the version, rename the branch there with
  `git branch -m release/<version>`.
- Merge release target: when `git merge-base --is-ancestor main <cut>` fails
  (the cut predates the previous release's merge back), run `git merge --no-ff
  main` on the release branch before Version Strategy reads the version,
  resolving any conflict per Conflicts below.
- Promote (Publish): `git checkout main && git merge --ff-only
  <release-head>` in the release worktree.
- Merge back (Post-ship), when `git merge-base --is-ancestor <release-head>
  develop` fails (the release branch carries a bump commit or the merged
  `main`). Without it, `main` would stop being an ancestor of `develop` and
  the next release would need the Merge release target step.
  1. In the release worktree, `git checkout release/<version> && git merge
     --no-ff develop`, resolving any conflict per Conflicts below.
  2. **(lead)** When `git merge-base --is-ancestor develop release/<version>`
     fails (`develop` moved after step 1), repeat step 1, so this merge
     cannot conflict and leave an in-progress merge in the shared main
     checkout. Then, from the main checkout, `ws/git.merge(branch:
     "release/<version>", target: "develop")` and `git push origin develop`.
- Conflicts (Merge release target and Merge back) resolve in the release
  worktree. On a line `bump-ws-version.sh` writes, take the higher version.
  Hand other conflicts **(lead)** to `ws:lead-delegate` with the worktree path. Commit the merge, then rerun Pre-flight's release-contract check on
  the release branch head. A conflict is never a stop: a restart cuts at the
  same point and meets the same conflict.
- Stops **(lead)**: every stop after Cut releases the release worktree
  (`ws/worktree.release(path: <worktree>)`), which detaches its HEAD and frees
  the release branch for the restart's Cut. That covers a red Pre-flight
  test, a failed release-contract or marketplace check, a conflict
  `ws:lead-delegate` could not resolve, a declined final gate, a rejected
  push, and a red CI gate. A rejected `main` push also restores local `main`
  with `git branch -f main origin/main` once the worktree is released, and
  deletes the unpushed local tag (`git tag -d v<version>`), so the restart's
  ref check passes. A stop before the tag push restarts Pre-flight from the
  top, after a fix forward on `develop` through ordinary review when a check
  was red; the version's tag was never pushed, so Version Strategy derives
  the same version again. A Merge back stop keeps `release/<version>` for a
  later Merge back.

## Pre-flight

**Sync against origin FIRST — never ship from stale local refs.** This is the
recurring failure mode: a parallel session ships a patch, `origin/main` and the
`v<version>` tag advance, and a local ship that skipped `fetch` cuts from stale
refs (colliding on the version/tag). Every check below runs against freshly
fetched state.

- `git fetch origin --tags` — refresh remote-tracking refs and tags before any
  other check.
- Local refs are up to date: `git merge-base --is-ancestor origin/develop
  develop` (local `develop` contains all of `origin/develop`), and
  `git rev-parse main` equals `git rev-parse origin/main`. If either fails,
  **stop and reconcile first** (`git merge origin/develop` into `develop`;
  fast-forward local `main` to `origin/main`), then restart Pre-flight.
- **Release gate** **(lead)** (this project declares `release-boundary:
  present` in `AGENTS.md` `### Review Policy`): run `lead-ship`'s Release
  gate over `develop`. Check `review.marker(format: json)` `found` **first,
  before any rev-list call** — an empty `head` substituted into `git rev-list
  --count <frontier-head>..develop` resolves the empty side to `develop`'s own
  tip and silently reports `0`, which would wrongly read as clear. An override at any
  gate stop proceeds without stamping the marker (only `lead-review`'s own
  `review.stamp` step ever advances it).
- Resolve the cut point **(lead)**: `<cut>` is the head of the `pass` or
  `concern` stamp the gate's review wrote (the clean-prefix stamp's when it
  wrote one), or the frontier head when the gate ran no review. The
  `bootstrap` entry the review seeds on an empty ledger clears nothing: a
  review that wrote no `pass` or `concern` stamp stops for an explicit
  override. For an override, or the gate's own bootstrap choice, `<cut>` is
  `develop`'s tip at that decision. Proceed only when `git merge-base
  --is-ancestor <cut> main` fails; otherwise nothing is newly cleared and the
  ship stops.
- Long tests **(lead)**: ask once, before handing off Pre-flight (and thus
  before Publish's final gate), via a whiteboard ask: "long test 돌릴까요?"
  Record the choice for this ship and pass it to the delegate. Default **no**
  selects the regular group; explicit **yes** adds the long group locally and
  in CI. This choice covers only the long group; all other gates still apply.
- Release Branch: Cut **(lead)** at `<cut>` and hand the worktree to the
  delegate, which runs Merge release target when its guard fails and the
  steps below.
- Resolve this ship's version per Version Strategy on the release branch, bump
  and commit when the current version's tag is claimed, then rename the branch
  to `release/<version>`. A `release/<version>` an earlier attempt left is
  gone locally (Cut deleted it) and is replaced on origin by Publish's
  force-push: its bump never reached `develop`, so Version Strategy derives
  the same version again.
- Version tag unclaimed: `git ls-remote --tags origin v<version>` is empty for the
  bumped or reused version. A non-empty result means a concurrent release claimed the
  version; increment the patch again, commit the regenerated version edits,
  rename the branch, re-run tests, and restart the tag check.
- `cd agents-plugin-tool && bash scripts/ci-test-shard.sh all` on the release
  branch head — the regular group must pass. When the owner chose **yes**,
  also run `bash scripts/ci-test-shard.sh long` from `agents-plugin-tool` and
  require it to pass. The script owns long-group membership; `all` excludes it.
- Release contract on the release branch head: for
  `agents-plugin/runtime.json` and `agents-plugin-wsflow/runtime.json`, assert
  `.release_tag == "v" + .plugin_version`.
- Marketplace sanity on the release branch head: `.plugins[].name` in both
  `.agents/plugins/marketplace.json` and `.claude-plugin/marketplace.json` is
  exactly `ws wsflow`.
- Record `git rev-parse HEAD` in the release worktree as `<release-head>`. The
  CI gate, the main promotion, and the tag in Publish all target this exact
  SHA. `develop` moving afterwards changes nothing that ships, so there is no
  tip re-check.

## Build

- No local build. The tagged push drives `ws-mcp release` on GitHub Actions,
  which requires a green `ws-mcp CI` run on the tagged commit (it does not
  re-run `go test`; Publish's CI gate is the release's only test run),
  validates the plugin release contract, builds release assets
  (`agents-plugin-tool/scripts/build-release-assets.sh`), and runs the Windows
  executable smoke.
- The `Tauri desktop` jobs reuse `ws-mcp-assets` for macOS arm64 and Windows
  x64. They assemble wsflow with
  `agents-plugin-tool/desktop/scripts/assemble-daemon-input.cjs` and run
  `desktop/scripts/package.cjs --release` without launching the app. The
  validated daemon metadata sets the app version.
- Both channels retain `werkschaft-tauri-assets`: `Werkschaft-darwin-arm64.app.tar.gz`,
  `Werkschaft-win32-x64-setup.exe`, `Werkschaft-win32-x64.msi`, their three
  `.sig` files, and `werkschaft-tauri-update.json`. The combine job verifies
  hashes, sizes, signatures and the feed. Stable tags publish native and Tauri
  assets together; exact `vX.Y.Z-canary.N` tags add Tauri assets to the native
  prerelease. Other tags retain native-only behavior.
- Updater signing uses the configured Tauri keys. Apple Developer ID signing
  and notarization and Windows Authenticode signing remain deferred; retirement
  does not approve new OS signing. Gatekeeper or SmartScreen may prompt. A
  green packaging job does not establish installed-app or platform acceptance.
- Wait for all `Tauri desktop` package, combine and publish jobs. Existing
  Electron users reinstall Tauri manually; migration ZIPs and desktop
  SHA256SUMS are retired.

### Desktop dry run before shipping

After the workflow changes are on `develop`, the lead can build both platforms
without publishing. From a checkout of that candidate, use its explicit
numeric version (or that version with `-canary.N`):

```sh
core=$(jq -r '.plugin_version' agents-plugin-wsflow/runtime.json)
gh workflow run ws-mcp-release.yml --ref develop -f tauri_dry_run=true -f version="$core"
gh run list --workflow ws-mcp-release.yml --branch develop --event workflow_dispatch
gh run watch <run-id> --exit-status
gh run download <run-id> -n werkschaft-tauri-assets -D tauri-dry-run
export WERKSCHAFT_UPDATER_PUBLIC_KEY="$(gh variable get WERKSCHAFT_EVAL_UPDATER_PUBLIC_KEY --repo kang-sw/werkschaft --json value --jq .value)"
node agents-plugin-tool/desktop/scripts/release.cjs --verify --input tauri-dry-run --version "$core"
```

Confirm the selected run's head SHA is the intended `develop` commit. The
native build and Windows executable smoke also run. `tauri_dry_run=true`
vetoes native and Tauri publication even on a tag. The default development
version is rejected. For a canary dry run, pass `version="$core-canary.1"`
and verify with that same version. Configure the evaluation keys and follow
[desktop release packaging](../../agents-plugin-tool/desktop/RELEASE.md)
for the two-channel feed and native acceptance checks. Keep this verification
before the tag push; Publish's CI gate and packaged-app acceptance still apply.

## Tag

Format: `v<version>` (e.g. `v0.42.1`), read from `agents-plugin/runtime.json`
`.release_tag`. Placed on `<release-head>` after Publish's CI gate passes and
main is promoted to it. Push: yes (covered by Publish's single final-gate
approval).

## Publish

Before the 0.52.0 publish gate, the owner verifies live OpenRouter, Sign in
with ChatGPT, and vLLM. Record those results with the gate evidence.

Order is deliberate: publish the release branch first, let `ws-mcp CI`
(`.github/workflows/ws-mcp-ci.yml`, ubuntu + sharded windows matrix, plus the
long legs when requested) pass on `<release-head>`, and only then promote
`main` and push the tag. Neither `main`
nor the tag is published before the release commit is green on Linux and
Windows, which the local Pre-flight `go test` cannot vouch for. The final gate
precedes every push — all pushes below are reversible-only-before-they-run.

- Final gate: show version, tag, `<cut>`, `release/<version>` at
  `<release-head>`, and the publish sequence below; wait for explicit approval.
  This single approval covers the whole sequence (release branch push, CI
  wait, main + tag push): a green CI gate proceeds to the main and tag pushes
  without another prompt, and a failed one stops without further pushes.

1. `git push --force origin release/<version>`. This claims no version and
   publishes no release; `--force` replaces a `release/<version>` an earlier
   stopped attempt pushed.
2. **CI gate** on `<release-head>`. A branch push starts no `ws-mcp CI` run
   (the workflow runs only on pull requests and manual dispatch), so the gate
   dispatches one on the release branch.
   1. Reuse a run only when it already passed: `gh run list --workflow
      ws-mcp-ci.yml --commit <release-head> --json
      databaseId,headSha,event,status,conclusion`, and take the newest entry
      whose `conclusion` is `success` and whose jobs (`gh run view <run-id>
      --json jobs --jq '.jobs[] | [.name, .conclusion]'`) meet step 3 for this
      ship's long-test choice. Then continue with step 3 (`gh run watch`
      returns at once for a finished run).
   2. No such run: dispatch one with
      `gh workflow run ws-mcp-ci.yml --ref release/<version>` (append
      `-f long_tests=true` when the owner chose **yes**), then take the
      newest run from `gh run list --workflow ws-mcp-ci.yml --event
      workflow_dispatch --json databaseId,headSha,status,conclusion` and
      confirm its `headSha` equals `<release-head>`, the commit that gets
      tagged (`ws-mcp-release.yml` requires a green ws-mcp CI run on the tag's
      commit). A different `headSha` means `origin/release/<version>` moved
      after the push: stop.
   3. `gh run watch <run-id> --exit-status` (the Windows shards run in
      parallel), then `gh run view <run-id> --json jobs --jq '.jobs[] |
      [.name, .conclusion]'`: the ubuntu `all` leg and every Windows shard
      must conclude `success`. With **yes**, both `go test (ubuntu-latest,
      long)` and `go test (windows-latest, long)` must also be present and
      conclude `success`. With **no**, only those two named long legs may
      conclude `skipped` (a successful long leg is also acceptable). Every
      other job must conclude `success`.

   **CI gate failure** (a failed, cancelled, timed-out, or otherwise
   nonconforming job): stop per Release Branch's Stops; `main` and the tag
   stay unpushed.
3. Once the CI gate passes: Release Branch's Promote (`main` `--ff-only` to
   `<release-head>`), `git tag v<version> <release-head>`, then
   `git push origin main` and `git push origin v<version>` — the tag push
   triggers the GitHub release workflow.

There is no "push develop first" step: develop pushes are ordinary and
independent of the release.

## Post-ship

- **Automatically** watch the `ws-mcp release` workflow run for `v<version>` to
  green (native build/publish, windows-smoke, both Tauri desktop builds, and combined
  Tauri asset verification/publish). This is an
  unprompted post-ship step: attach to the run without asking the user for
  approval, and report only the terminal result — green, or the failure with
  its failing job/step. Monitoring is read-only and pushes nothing, so it never
  needs a confirmation gate. (`gh run watch <run-id> --exit-status` attaches and
  exits non-zero on failure.)
- Release Branch's Merge back, in the release worktree, then from the main
  checkout.
- **(lead)** Release the release worktree
  (`ws/worktree.release(path: <worktree>)`).
- Delete the release branch: `git push origin --delete release/<version>` and
  `git branch -D release/<version>`. The next develop merge resumes normal
  development without a version bump; the next ship owns the next patch bump.

## Desktop canary procedure

This is a separate owner-operated prerelease path. It publishes a desktop test
build from reviewed `develop` without promoting `main` or merging its canary-only
version bump back into `develop`. Complete the manual install and return checks
below before treating a canary as accepted. Keep the stable procedure above as
the authority for stable publication.

1. Fetch origin and tags, confirm local `develop` contains `origin/develop`, and
   run the review-watermark release gate over the intended `develop` cut as in
   Pre-flight. Cut an ephemeral `release/canary-<core>` branch in its own worktree
   at that reviewed commit. Record the cut SHA. No unreviewed commit may enter
   the branch except its generated version bump, which is tested on its final
   SHA. Stop if the review gate has no clear cut or needs an override that the
   owner has not approved.
2. Choose the **next patch core** after the latest claimed stable tag, within
   the current minor (for example, stable `v0.50.8` gives core `0.50.9`). Run
   `agents-plugin-tool/scripts/bump-ws-version.sh <core>` on the ephemeral
   branch and commit its generated changes. Confirm both runtime contracts
   retain numeric `plugin_version=<core>` and `release_tag=v<core>`; the canary
   suffix belongs to the tag and native binary metadata. **Do not publish a
   minor-core canary** unless a separately approved T3 flow is defined. A
   higher minor would replace the shared daemon across minors, break older
   plugin sessions, and Back to stable would not restore that daemon.
3. Grade the actual canary payload under
   `ai-docs/manuals/release-compatibility.md`: cite old/new read and write
   paths, the highest tier, and recovery on reopening. The canary label alone
   has no compatibility grade. T4 cannot ship; a T3 change stops this patch-core
   procedure. Run the normal local release tests and contract/marketplace
   checks on the branch head. Push the ephemeral branch, dispatch `ws-mcp CI`
   for that exact SHA, and require its Linux and Windows jobs to pass with the
   selected long-test policy, as in Publish. Record that SHA as the tag target.
4. Refresh remote tags before choosing `N`. For `v<core>-canary.N`, use the
   lowest non-negative decimal counter not already claimed on origin; never
   overwrite or delete an existing canary tag to resolve a collision. Recheck
   the chosen remote ref immediately before publishing. If another publisher
   claims it, increment `N`, recheck, and tag the same tested SHA. Show the
   owner the core, counter, branch, tested SHA, compatibility verdict, and
   publication sequence for approval. Push only the canary tag; leave `main`
   and `develop` untouched. A tag exactly shaped `vX.Y.Z-canary.N` creates the
   same tag release with `--prerelease --latest=false`; native and desktop jobs
   upload to that one release.
5. Watch the complete `ws-mcp release` run, including native build, Windows
   smoke, and all `Tauri desktop` package, combine and publish jobs. Confirm
   the release is a prerelease, is excluded from latest, and its tag and target
   SHA match the approved values. Download the native assets and check
   `shasum -a 256 -c SHA256SUMS` in their directory. Download the three actual
   Tauri packages listed in Build, their `.sig` files and
   `werkschaft-tauri-update.json` into a separate directory, `tauri-canary`.
   Load the production public key and verify the complete payload and feed:

   ```sh
   export WERKSCHAFT_UPDATER_PUBLIC_KEY="$(gh variable get WERKSCHAFT_UPDATER_PUBLIC_KEY --repo kang-sw/werkschaft --json value --jq .value)"
   node agents-plugin-tool/desktop/scripts/release.cjs --verify --input tauri-canary --version "<core>-canary.N"
   ```

   This checks exact file names, hashes, byte counts, signatures and canonical
   feed URLs for the approved version. A green native upload alone is incomplete.
6. On a test machine, use the explicit `Canary 빌드로 업데이트` action to install
   the canary, restart, and confirm the desktop reports the suffixed version and
   connects to its bundled daemon. Exercise `Back to stable`, confirm the
   selected `/releases/latest` stable desktop installs and reports its stable
   version, then inspect the shared daemon version and source. App SemVer puts
   a same-core canary below stable, while daemon retention keeps a healthy
   equal-core daemon regardless of suffix. Back to stable changes app bytes;
   it never drains or downgrades the shared daemon. If equal-core canary daemon
   bytes must be replaced, use the explicit `Replace daemon (same core)` action
   or shut down the daemon and reconnect with the stable app. A lower-core
   stable app attaches to a healthy higher-core daemon; shut that daemon down
   explicitly before reconnecting if stable daemon bytes are required.
7. Record the acceptance result and remove the ephemeral worktree and local
   branch. Delete the remote canary branch after verification; retain the
   published tag/release for traceability. Do not merge the canary-only bump
   into `develop` and do not promote `main`. A later stable ship derives its own
   version from the stable flow above.
