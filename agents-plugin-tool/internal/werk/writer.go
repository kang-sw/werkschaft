package werk

import (
	"encoding/json"
	"sync"
)

// writer is the single ordered background writer of the conversation log.
// The loop hands it an item and goes on: a model request or a continuation
// never waits on a disk write.
//
// Why a delayed write is safe: the in-memory transcript is the source of the
// next request, and the log is read only by a process that resumes the
// session. Items reach the log in the order they were added, so a crash
// loses a tail, never the middle. A call that may change something does not
// run until the log holds its function_call line (Agent.runCall flushes
// first), so a tail that ends in such a call has the call on record and resume
// answers it as interrupted and never runs it again. A read-only call may
// lose its line with the tail; repeating it is harmless. The writer is also
// flushed where the log must be whole: the end of a turn (also after an
// interrupt), shutdown, and Close.
type writer struct {
	store *Store
	// before runs ahead of each append (a seam for a test to delay the write).
	before func(json.RawMessage)
	// onErr reports the first failed write; the writer goes on.
	onErr func(error)

	mu       sync.Mutex
	cond     *sync.Cond
	queue    []json.RawMessage
	inflight bool
	closed   bool
	failed   bool
	done     chan struct{}
}

func newWriter(store *Store, before func(json.RawMessage), onErr func(error)) *writer {
	w := &writer{store: store, before: before, onErr: onErr, done: make(chan struct{})}
	w.cond = sync.NewCond(&w.mu)
	go w.run()
	return w
}

// Add queues an item behind those already queued.
func (w *writer) Add(item json.RawMessage) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	w.queue = append(w.queue, item)
	w.cond.Broadcast()
}

func (w *writer) run() {
	defer close(w.done)
	for {
		w.mu.Lock()
		for len(w.queue) == 0 && !w.closed {
			w.cond.Wait()
		}
		if len(w.queue) == 0 {
			w.mu.Unlock()
			return
		}
		batch := w.queue
		w.queue = nil
		w.inflight = true
		w.mu.Unlock()

		for _, item := range batch {
			if w.before != nil {
				w.before(item)
			}
			if err := w.store.Append(item); err != nil {
				w.mu.Lock()
				first := !w.failed
				w.failed = true
				w.mu.Unlock()
				if first && w.onErr != nil {
					w.onErr(err)
				}
			}
		}

		w.mu.Lock()
		w.inflight = false
		w.cond.Broadcast()
		w.mu.Unlock()
	}
}

// Flush returns once every item added before it has been written.
func (w *writer) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for len(w.queue) > 0 || w.inflight {
		w.cond.Wait()
	}
}

// Close writes what is queued and stops the writer; the store stays open.
func (w *writer) Close() {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		<-w.done
		return
	}
	w.closed = true
	w.cond.Broadcast()
	w.mu.Unlock()
	<-w.done
}
