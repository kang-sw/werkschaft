# Keep daemon-opened agy worker turns running across reattach

The worker transcript for `rakingvarnishmousinessskeleton` contains textless completions at 07:33:09, 07:52:30, and 08:04:18 KST, after the reported daemon swaps at 07:30:24, 07:50:06, and 08:03:49. Compare those timestamps with the replay classification and quiet-timer paths in `internal/wsagent/agy.go`. The transcript summarizes each turn and omits the raw ACP updates and timer callbacks, so attribute specific timer firings only where the evidence supports them.

1. Carry the existing worker record's open, daemon-sent turn through holder reattach. The send path records a start time and holder sequence. A harness-initiated wakeup also opens a turn but does not record a send start time.
2. Keep wakeup timers off that recovered prompt until its ACP response settles it. Use the saved start sequence to distinguish its response from responses to older prompts in the same replay. Retain quiet completion for genuine wakeups.
3. Exercise the usage and idle quiet gaps with shortened timer intervals in the existing agy tests. Verify the real response produces the worker result and a genuine unsolicited wakeup still closes.
4. Run `go build ./...` and `go test ./internal/wsagent -count=1` from `agents-plugin-tool`. The change is T0 because it uses existing records and output with an in-memory classification. Commit this plan and the fix together with `## AI Context` and no co-author trailer.
