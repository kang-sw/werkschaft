# Pi retirement Phase 1 x track/harness integration

Goal: merge current `track/harness` into `impl/track/harness/jolt-false-coat`
(Pi retirement Phase 1, Result in `261123-chore-ws-retire-pi-harness`) and
resolve conflicts so the lead can merge the branch into `track/harness` cleanly.

Keep both intents:
- werk Phase 4: durable in-daemon builtin runner, removal of `werk-run` and
  the holder launch, fs.edit/fs.write, steer receipts, exec recovery,
  Full Quit, compaction.
- Pi retirement: removal of the Pi backend, launch, probe, policy and rejoin;
  retained Pi history reads, `ws-pi-bridge` recognition, startup sweep guards
  for legacy Pi and unidentifiable legacy lead holders, and the
  `pi.<tier>` -> `werk.<tier>` alias.

Where Pi Phase 1 deleted code that Phase 4 modified, keep the deletion
unless Phase 4's change serves werk or a generic path; then keep the generic
part. Migrate tests rather than deleting them.

Verification: as listed in the spawn prompt; known track baseline dashboard
failures are excluded.
