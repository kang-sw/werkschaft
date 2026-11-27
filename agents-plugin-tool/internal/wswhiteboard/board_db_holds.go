// Board database item holds (daemon-persisted user deferral state, never sent
// to the agent). Additive schema v1 table: older binaries ignore item_holds
// and never overwrite it.
package wswhiteboard

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
)

// ErrHoldItemNotFound reports a hold mutation targeting an unknown item ID.
var ErrHoldItemNotFound = errors.New("whiteboard: hold item not found")

// ErrHoldItemNotOpen reports a hold mutation targeting a non-open item ID.
var ErrHoldItemNotOpen = errors.New("whiteboard: hold item is not open")

type boardItemHold struct {
	LoadedAt string
	Changed  int
}

// HeldIDs returns the sorted list of currently held open item IDs from memory.
func (b *BoardDB) HeldIDs() []int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.heldIDsLocked()
}

// SnapshotWithHolds returns a detached State copy, current board revision, and
// sorted held open item IDs under a single lock acquisition.
func (b *BoardDB) SnapshotWithHolds() (*State, uint64, []int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == nil {
		return nil, uint64(b.generation), nil
	}
	return b.state.clone(), uint64(b.generation), b.heldIDsLocked()
}

// SetHold explicitly sets or clears the hold for one open item ID (policy,
// check, or ack) and returns the board's current sorted held ID list. It
// writes only item_holds in a single WAL transaction without rewriting
// board_meta/topics or advancing Revision(), so agent nudges never fire.
func (b *BoardDB) SetHold(itemID int, held bool) ([]int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, fmt.Errorf("whiteboard board DB is closed")
	}
	if b.state == nil {
		return nil, ErrHoldItemNotFound
	}
	var target *Item
	for i := range b.state.Items {
		if b.state.Items[i].ID == itemID {
			target = &b.state.Items[i]
			break
		}
	}
	if target == nil {
		return nil, ErrHoldItemNotFound
	}
	if target.Status != StatusOpen {
		return nil, ErrHoldItemNotOpen
	}
	if b.holds == nil {
		b.holds = map[int]boardItemHold{}
	}
	if held {
		want := boardItemHold{LoadedAt: b.state.LoadedAt, Changed: target.Changed}
		if cur, ok := b.holds[itemID]; ok && cur == want {
			return b.heldIDsLocked(), nil
		}
		tx, err := b.db.Begin()
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		if _, err := tx.Exec(`INSERT INTO item_holds(item_id,loaded_at,item_changed) VALUES(?,?,?) ON CONFLICT(item_id) DO UPDATE SET loaded_at=excluded.loaded_at,item_changed=excluded.item_changed`, itemID, want.LoadedAt, want.Changed); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		b.holds[itemID] = want
		b.armCheckpointLocked()
		return b.heldIDsLocked(), nil
	}
	if _, ok := b.holds[itemID]; !ok {
		return b.heldIDsLocked(), nil
	}
	tx, err := b.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM item_holds WHERE item_id=?`, itemID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	delete(b.holds, itemID)
	b.armCheckpointLocked()
	return b.heldIDsLocked(), nil
}

func (b *BoardDB) heldIDsLocked() []int {
	if b.state == nil || len(b.holds) == 0 {
		return nil
	}
	var ids []int
	for _, item := range b.state.Items {
		if item.Status != StatusOpen {
			continue
		}
		h, ok := b.holds[item.ID]
		if ok && h.LoadedAt == b.state.LoadedAt && h.Changed == item.Changed {
			ids = append(ids, item.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	sort.Ints(ids)
	return ids
}

// loadHoldsLocked reads item_holds into memory and prunes any rows whose
// loaded_at or item_changed no longer matches an open item in b.state
// (e.g., after a write by an older binary that ignores item_holds).
func (b *BoardDB) loadHoldsLocked() error {
	b.holds = map[int]boardItemHold{}
	if b.state == nil {
		return nil
	}
	openByID := make(map[int]Item, len(b.state.Items))
	for _, item := range b.state.Items {
		if item.Status == StatusOpen {
			openByID[item.ID] = item
		}
	}
	rows, err := b.db.Query(`SELECT item_id,loaded_at,item_changed FROM item_holds`)
	if err != nil {
		return err
	}
	var stale []int
	for rows.Next() {
		var id, changed int
		var loadedAt string
		if err := rows.Scan(&id, &loadedAt, &changed); err != nil {
			rows.Close()
			return err
		}
		item, ok := openByID[id]
		if ok && loadedAt == b.state.LoadedAt && changed == item.Changed {
			b.holds[id] = boardItemHold{LoadedAt: loadedAt, Changed: changed}
		} else {
			stale = append(stale, id)
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	if len(stale) == 0 {
		return nil
	}
	tx, err := b.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range stale {
		if _, err := tx.Exec(`DELETE FROM item_holds WHERE item_id=?`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// boardHoldClears determines which item holds must be deleted in the same
// transaction as a board state mutation: all holds when LoadedAt changed,
// or individual holds whose item vanished, left StatusOpen, or advanced
// Changed.
func boardHoldClears(beforeState, afterState *State, holds map[int]boardItemHold) (clearAll bool, clearIDs []int) {
	if len(holds) == 0 {
		return afterState.LoadedAt != beforeState.LoadedAt, nil
	}
	if afterState.LoadedAt != beforeState.LoadedAt {
		return true, nil
	}
	afterByID := make(map[int]Item, len(afterState.Items))
	for _, item := range afterState.Items {
		afterByID[item.ID] = item
	}
	for id, h := range holds {
		item, ok := afterByID[id]
		if !ok || item.Status != StatusOpen || item.Changed != h.Changed || h.LoadedAt != afterState.LoadedAt {
			clearIDs = append(clearIDs, id)
		}
	}
	sort.Ints(clearIDs)
	return false, clearIDs
}

func boardApplyHoldClears(tx *sql.Tx, clearAll bool, clearIDs []int) error {
	if clearAll {
		_, err := tx.Exec(`DELETE FROM item_holds`)
		return err
	}
	for _, id := range clearIDs {
		if _, err := tx.Exec(`DELETE FROM item_holds WHERE item_id=?`, id); err != nil {
			return err
		}
	}
	return nil
}
