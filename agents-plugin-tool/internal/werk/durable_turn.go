package werk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

type turnStartRecord struct {
	K      string          `json:"k"`
	TurnID string          `json:"turn_id"`
	User   json.RawMessage `json:"user"`
}

type durableEnd struct {
	Reason     string `json:"reason"`
	Error      string `json:"error,omitempty"`
	ErrorCode  string `json:"error_code,omitempty"`
	ResetsAt   int64  `json:"resets_at,omitempty"`
	UsageLimit bool   `json:"usage_limit,omitempty"`
	PlanPaused bool   `json:"plan_paused,omitempty"`
}

type turnEndRecord struct {
	K      string     `json:"k"`
	TurnID string     `json:"turn_id"`
	End    durableEnd `json:"end"`
}

type turnSteerRecord struct {
	K      string          `json:"k"`
	TurnID string          `json:"turn_id"`
	ID     string          `json:"steer_id"`
	User   json.RawMessage `json:"user"`
}

// turnJournal identifies the last turn and the point reached in the log
// when opened. The live turn appends to the transcript in order.
type turnJournal struct {
	id          string
	number      int
	input       int
	user        json.RawMessage
	response    *Response
	responseEnd int
	end         *durableEnd
	steers      []turnSteerRecord
}

// Preserve the completed identity and original input while dropping indexes
// into the previous model input. An already-settled turn may still be
// submitted again during reconciliation.
func (j *turnJournal) selectCheckpoint() {
	j.input, j.responseEnd = 0, 0
	j.response, j.steers = nil, nil
}

func (a *Agent) reconcileClosedTurn() error {
	// Preserve history and complete outstanding tool pairs without invoking
	// their tools. The same end row used by a normal abort makes replay safe.
	a.repaired = a.repairPending()
	end := durableEnd{Reason: ReasonAborted}
	if err := a.log.commit(turnEndRecord{K: "turn_end", TurnID: a.journal.id, End: end}); err != nil {
		return fmt.Errorf("save the closed durable turn: %w", err)
	}
	a.journal.end = &end
	return nil
}

func (a *Agent) beginDurableTurn(cmd Command) (resumed bool, err error) {
	item := userItem(cmd.Text)
	if a.journal.id == cmd.TurnID {
		if !bytes.Equal(a.journal.user, item) {
			return false, errors.New("the durable turn input changed")
		}
		return true, nil
	}
	if a.journal.id != "" && a.journal.end == nil {
		return false, errors.New("the previous durable turn is still active")
	}
	if err := a.log.commit(turnStartRecord{K: "turn_start", TurnID: cmd.TurnID, User: item}); err != nil {
		return false, fmt.Errorf("save the durable turn input: %w", err)
	}
	a.mu.Lock()
	a.journal = turnJournal{id: cmd.TurnID, number: a.journal.number + 1, input: len(a.transcript), user: item}
	a.transcript = append(a.transcript, item)
	a.mu.Unlock()
	return false, nil
}

func (a *Agent) runDurableTurn(ctx, host context.Context, cmd Command) (err error) {
	resumed, err := a.beginDurableTurn(cmd)
	if err != nil {
		return err
	}
	journal := &a.journal
	turn := journal.number
	a.mu.Lock()
	a.turn = turn
	a.running, a.lastReply, a.lastReason, a.lastError, a.taken = true, "", "", "", nil
	a.mu.Unlock()

	var result durableEnd
	defer func() {
		if recover() != nil {
			result = durableEnd{Reason: ReasonError, Error: "the werk turn failed unexpectedly"}
			err = nil
		}
		if err != nil || (result.Reason == ReasonAborted && host.Err() != nil) {
			return
		}
		if journal.end == nil {
			if err = a.log.commit(turnEndRecord{K: "turn_end", TurnID: cmd.TurnID, End: result}); err != nil {
				err = fmt.Errorf("save the durable turn result: %w", err)
				return
			}
			journal.end = &result
		}
		a.mu.Lock()
		a.running, a.lastReason, a.lastError = false, result.Reason, result.Error
		a.mu.Unlock()
		a.out.line(Line{
			T:          LineTurnEnd,
			Turn:       turn,
			TurnID:     cmd.TurnID,
			Reason:     result.Reason,
			Error:      result.Error,
			ErrorCode:  result.ErrorCode,
			ResetsAt:   result.ResetsAt,
			UsageLimit: result.UsageLimit,
			PlanPaused: result.PlanPaused,
		})
	}()

	a.out.line(Line{T: LineTurnStart, Turn: turn, TurnID: cmd.TurnID})
	if resumed {
		if err := a.discardCommittedSteers(journal.steers); err != nil {
			return err
		}
		for _, steer := range journal.steers {
			a.out.line(Line{T: LineUserTaken, TurnID: cmd.TurnID, ID: steer.ID})
		}
		if journal.input+1 < len(a.transcript) {
			a.restoreDurableReply(turn, a.transcript[journal.input+1:])
		}
	} else {
		a.out.line(Line{T: LineUser, Turn: turn, Text: cmd.Text})
	}
	if journal.end != nil {
		result = *journal.end
		return nil
	}
	if journal.response != nil {
		// A complete response with no calls ends a turn unless a later
		// input (e.g. a steer) was already added to the transcript.
		if journal.responseEnd == len(a.transcript) && len(functionCalls(journal.response.Output)) == 0 && !a.hasSteers() {
			result.Reason = ReasonDone
			return nil
		}
		panicked, err := a.recoverToolCalls(ctx, host, turn, journal)
		if err != nil {
			return err
		}
		if panicked {
			result = durableEnd{Reason: ReasonError, Error: "a recovered read-only tool failed unexpectedly"}
			return nil
		}
	}
	result, err = a.loop(ctx, host, turn, cmd.TurnID)
	return err
}

func (a *Agent) discardCommittedSteers(committed []turnSteerRecord) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	byID := make(map[string]json.RawMessage, len(committed))
	for _, row := range committed {
		byID[row.ID] = row.User
	}
	for _, steer := range a.steers {
		if user, found := byID[steer.ID]; found && !bytes.Equal(user, userItem(steer.Text)) {
			return errors.New("the durable steer input changed")
		}
	}
	a.steers = slices.DeleteFunc(a.steers, func(steer Steer) bool {
		_, found := byID[steer.ID]
		return found
	})
	return nil
}

func (a *Agent) commitSteer(turnID string, steer Steer) error {
	row := turnSteerRecord{K: "turn_steer", TurnID: turnID, ID: steer.ID, User: userItem(steer.Text)}
	if previous := slices.IndexFunc(a.journal.steers, func(row turnSteerRecord) bool { return row.ID == steer.ID }); previous >= 0 {
		if !bytes.Equal(a.journal.steers[previous].User, row.User) {
			return errors.New("the durable steer input changed")
		}
	} else {
		if steer.ID == "" {
			return errors.New("the durable steer has no identity")
		}
		if err := a.log.commit(row); err != nil {
			return fmt.Errorf("save the durable steer: %w", err)
		}
		a.mu.Lock()
		a.journal.steers = append(a.journal.steers, row)
		a.transcript = append(a.transcript, row.User)
		a.mu.Unlock()
	}
	a.out.line(Line{T: LineUserTaken, TurnID: turnID, ID: steer.ID, Text: steer.Text})
	return nil
}

func (a *Agent) restoreDurableReply(turn int, items []json.RawMessage) {
	for i := len(items) - 1; i >= 0; i-- {
		var item responseItem
		if json.Unmarshal(items[i], &item) == nil && item.Type == "message" {
			if text := item.reply(); text != "" {
				a.reportReply(Line{T: LineReply, Turn: turn, Item: item.ID, Text: text, Final: true})
				return
			}
		}
	}
}

func (a *Agent) recoverToolCalls(ctx, host context.Context, turn int, journal *turnJournal) (bool, error) {
	pending := make(map[string]bool)
	for _, id := range pendingCalls(a.transcript[journal.input+1:]) {
		pending[id] = true
	}
	if len(pending) == 0 {
		return false, nil
	}
	if err := a.loadTools(ctx); err != nil && ctx.Err() == nil {
		return false, fmt.Errorf("restore the tool list for the durable turn: %w", err)
	}
	a.mu.Lock()
	tools := a.tools
	a.mu.Unlock()
	panicked := false
	for _, call := range functionCalls(journal.response.Output) {
		if !pending[call.CallID] {
			continue
		}
		delete(pending, call.CallID)
		if err := host.Err(); err != nil {
			return panicked, err
		}
		if a.readOnlyCall(tools, call) && ctx.Err() == nil && !panicked {
			panicked = a.runCall(ctx, host, turn, journal.response.ID, tools, call)
			continue
		}
		if a.cfg.Approve != nil && a.store.pendingApproval[call.CallID] && ctx.Err() == nil && !panicked {
			call.reask = true
			panicked = a.runCall(ctx, host, turn, journal.response.ID, tools, call)
			continue
		}
		output := "Error: the previous tool call's outcome is unknown. It was not run again; check the current state before repeating it."
		if intent, ok := a.store.fileIntents[call.CallID]; ok && canonicalFileTool(call.Name) != "" {
			output = "Recovered file mutation: " + intent.Outcome() + ". The tool was not run again."
		}
		if job := a.store.execJobs[call.CallID]; job != "" && canonicalExecLaunch(call.Name) != "" && a.cfg.RecoverExec != nil {
			if text, err := a.cfg.RecoverExec(ctx, job); err == nil {
				output = "Recovered exec launch:\n" + text
			}
		}
		a.append(functionOutputItem(call.CallID, output))
	}
	if len(pending) != 0 {
		return panicked, errors.New("the committed response does not match the unfinished tool calls")
	}
	return panicked, nil
}
