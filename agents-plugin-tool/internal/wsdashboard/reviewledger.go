package wsdashboard

import (
	"context"
	"net/http"

	"github.com/kang-sw/devenv/internal/wsgit"
	"github.com/kang-sw/devenv/internal/wsreview"
)

// reviewLedgerRunner runs the review-ledger read's git commands; wsreview adds
// the local-only environment (no lazy fetch, no optional locks, no prompts). A
// test replaces it to count or fail the commands.
var reviewLedgerRunner wsgit.Runner = wsgit.ExecRunner{}

// handleReviewLedger serves GET /s/<reply-id>/review-ledger: the session
// project's review-watermark standing as wsreview.CheckpointStatus JSON. It
// only reads (the ledger and ai-docs/_review.local.md, plus local git) and is
// called on a page's open, refresh, and after an action, never per stream tick.
// A project with no ledger, no review track, or no repository answers 200 with
// state missing or norepo, never an error.
func (d *Daemon) handleReviewLedger(w http.ResponseWriter, r *http.Request, replyID string) {
	root, ok := d.sessionRoot(w, replyID)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), gitReadTimeout)
	defer cancel()
	writeGitJSON(w, wsreview.CheckpointStatusWith(ctx, reviewLedgerRunner, root))
}
