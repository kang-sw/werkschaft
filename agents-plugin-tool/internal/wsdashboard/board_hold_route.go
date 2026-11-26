package wsdashboard

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/kang-sw/devenv/internal/wswhiteboard"
)

// boardHoldBodyLimit bounds the board hold route's request body.
const boardHoldBodyLimit = 4 << 10

type boardHoldRequest struct {
	ID   json.RawMessage `json:"id"`
	Held *bool           `json:"held"`
}

type boardHoldResponse struct {
	Held []string `json:"held"`
}

func parseBoardHoldRequest(raw []byte) (int, bool, error) {
	var req boardHoldRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return 0, false, err
	}
	if req.Held == nil || len(req.ID) == 0 {
		return 0, false, errors.New("missing id or held")
	}
	var s string
	if err := json.Unmarshal(req.ID, &s); err == nil {
		if s == "" {
			return 0, false, errors.New("empty id")
		}
		for i := 0; i < len(s); i++ {
			if s[i] < '0' || s[i] > '9' {
				return 0, false, errors.New("non-numeric id")
			}
		}
		id, err := strconv.Atoi(s)
		if err != nil || id <= 0 {
			return 0, false, errors.New("invalid id")
		}
		return id, *req.Held, nil
	}
	var n int
	if err := json.Unmarshal(req.ID, &n); err == nil && n > 0 {
		return n, *req.Held, nil
	}
	return 0, false, errors.New("invalid id")
}

// handleBoardHold serves POST /s/<reply-id>/board/hold: it sets or clears the
// user-side hold on one open board item and answers {"held": ["<id>", ...]}
// with the board's current held item IDs.
func (d *Daemon) handleBoardHold(w http.ResponseWriter, r *http.Request, replyID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "cross-origin request refused", http.StatusForbidden)
		return
	}
	if d.index == nil || !ValidReplyID(replyID) {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}
	sess, ok := d.index.Session(replyID)
	if !ok {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}
	src, ok := d.source.(BoardHoldSource)
	if !ok {
		http.Error(w, "board hold unavailable", http.StatusNotFound)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, boardHoldBodyLimit))
	if err != nil {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	itemID, held, err := parseBoardHoldRequest(raw)
	if err != nil {
		http.Error(w, "malformed request body", http.StatusBadRequest)
		return
	}
	heldIDs, hasBoard, err := src.SetBoardHold(sess.Registration, itemID, held)
	switch {
	case errors.Is(err, wswhiteboard.ErrHoldItemNotFound):
		http.Error(w, "unknown board item", http.StatusNotFound)
		return
	case errors.Is(err, wswhiteboard.ErrHoldItemNotOpen):
		http.Error(w, "board item is not open", http.StatusConflict)
		return
	case err != nil:
		fmt.Fprintf(d.log, "dashboard: board hold %s: %v\n", replyID, err)
		http.Error(w, "the board hold could not be saved", http.StatusInternalServerError)
		return
	case !hasBoard:
		http.Error(w, "board unavailable", http.StatusNotFound)
		return
	}
	// The re-read picks up the updated held set, raises the session's
	// NonLiveFingerprint, and wakes every board and rail stream without
	// poking agent nudges.
	d.index.Reread(replyID)
	ids := make([]string, len(heldIDs))
	for i, id := range heldIDs {
		ids[i] = strconv.Itoa(id)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, boardHoldResponse{Held: ids})
}
