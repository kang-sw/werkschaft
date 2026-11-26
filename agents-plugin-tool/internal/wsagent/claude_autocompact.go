// Claude worker automatic compaction from compaction.worker_hard.

package wsagent

import (
	"math"
	"strconv"

	"github.com/kang-sw/devenv/internal/wsconfig"
)

// Claude Code's own automatic compaction, read from the 2.1.332 binary and
// checked against a live stream-json probe (2026-11-26):
//
//	window  = min(model window, CLAUDE_CODE_AUTO_COMPACT_WINDOW); the
//	          variable beats settings, and a value under 100000 counts as
//	          100000
//	reserve = min(model max output tokens, 20000)
//	trigger = window - reserve - 13000, or with
//	          CLAUDE_AUTOCOMPACT_PCT_OVERRIDE=q (0 < q <= 100)
//	          min(floor((window-reserve)*q/100), window-reserve-13000)
//
// get_context_usage answers rawMaxTokens = window (autocompactSource "env"
// when the variable set it), the figure the mapper reports as the context
// window (claudeMapper.contextUsage). Its autoCompactThreshold is always
// window-reserve-13000: the probe's q=30 compacted at 62k tokens in a 100k
// window while the answer still read 67000, so that field ignores q and
// the trigger honours it. The result's modelUsage contextWindow stays the
// model's own window.
const (
	claudeAutoCompactWindowEnv = "CLAUDE_CODE_AUTO_COMPACT_WINDOW"
	claudeAutoCompactPctEnv    = "CLAUDE_AUTOCOMPACT_PCT_OVERRIDE"
	// claudeAutoCompactWindowFloor is the smallest window the variable sets.
	claudeAutoCompactWindowFloor = 100_000
)

// claudeAutoCompactEnv maps a Claude worker's compaction.worker_advisory and
// compaction.worker_hard to the variables above. Claude then compacts by
// itself, mid-turn, at or before the hard point. The daemon's own hard
// compaction runs only at idle, and a Claude worker gets no compaction steer
// (NoWorkerCompactionSteer), so without these one long tool loop ran a 1M
// window to 342k against a 200k hard point. A zero hard threshold (off or
// unreadable) sets nothing. The model's window is unknown at launch, so
// both values must hold for every model window n:
//
//  1. The daemon's thresholds resolve as without them. The daemon
//     evaluates Threshold.Point against the window the harness reports,
//     w = min(n, W), and not against n. W is the window set, or n when
//     none is. Setting W to the hard point's token cap would shrink twice:
//     "80%|200k" resolves to min(80% of 200k, 200k) = 160k against w = 200k,
//     where the user meant min(80% of 1M, 200k) = 200k. So W only covers
//     the token terms (claudeAutoCompactWindow). For n >= W every term
//     resolves to the same point against w as against n, or both resolve
//     at or past the hard point, where only the hard compaction counts;
//     for n < W, w = n.
//  2. Claude's trigger is at or below the hard point. The trigger is under
//     w*q/100, so q is the hard percent, capped at the hard token count
//     over W. A percent-only hard point sets no window and only q: the
//     trigger stays under that percent of any window.
//
// For worker_advisory "50%|160k" and worker_hard "80%|200k" the result is
// W = 320000 and q = 62. On a 1M model the meter reads tokens/320k with the
// advisory at 160k and the hard point at 200k, both as configured, and
// Claude compacts at 186k. On a 200k model, w = 200k: advisory 100k, hard
// 160k, Claude at about 112k. The cost of not knowing n at launch is that
// a model smaller than W compacts early, never late. The defaults ("50%",
// "80%") set only q = 80, and rawMaxTokens stays the model's window.
// Changing either key takes effect from the worker's next process start.
func claudeAutoCompactEnv(advisory, hard wsconfig.Threshold) map[string]string {
	if hard.Percent <= 0 && hard.Tokens <= 0 {
		return nil
	}
	window := claudeAutoCompactWindow(advisory, hard)
	pct := float64(100)
	if hard.Percent > 0 {
		pct = hard.Percent
	}
	if window > 0 {
		pct = math.Min(pct, hard.Tokens*100/window)
	}
	pct = math.Max(1, math.Floor(pct))
	set := map[string]string{}
	if window > 0 {
		set[claudeAutoCompactWindowEnv] = strconv.FormatInt(int64(window), 10)
	}
	if pct < 100 {
		set[claudeAutoCompactPctEnv] = strconv.FormatInt(int64(pct), 10)
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

// claudeAutoCompactWindow is the window W claudeAutoCompactEnv sets, or 0
// for none. The hard point needs W >= its token count T, and W >= T/P for
// a combined "P%|T": any larger window then resolves it to T, as the
// model's own window does. The advisory needs, with T' the smaller of its
// token count and T: W >= its token count when that is under T, and W >=
// T'/P' for its percent P'. Its point then resolves exactly, or at or past
// the hard point on both windows.
func claudeAutoCompactWindow(advisory, hard wsconfig.Threshold) float64 {
	if hard.Tokens <= 0 {
		return 0
	}
	need := hard.Tokens
	if hard.Percent > 0 {
		need = math.Ceil(hard.Tokens * 100 / hard.Percent)
	}
	target := hard.Tokens
	if advisory.Tokens > 0 && advisory.Tokens < target {
		target = advisory.Tokens
		need = math.Max(need, math.Ceil(advisory.Tokens))
	}
	if advisory.Percent > 0 {
		need = math.Max(need, math.Ceil(target*100/advisory.Percent))
	}
	return math.Max(need, claudeAutoCompactWindowFloor)
}
