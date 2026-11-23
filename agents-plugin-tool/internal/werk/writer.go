package werk

import (
	"encoding/json"
	"errors"
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
	// before runs ahead of each append (a seam for a test to delay or fail
	// the write); an error from it is a failed write, and the item is not
	// appended.
	before func(json.RawMessage) error
	// onErr reports the first failed write. The durable log remains a prefix.
	onErr func(error)

	mu       sync.Mutex
	cond     *sync.Cond
	queue    []logAppend
	inflight bool
	closed   bool
	err      error // the first failed write; sticky
	done     chan struct{}
}

type logAppend struct {
	item       json.RawMessage
	response   *Response
	fileIntent *fileIntentLine
}

func newWriter(store *Store, before func(json.RawMessage) error, onErr func(error)) *writer {
	w := &writer{store: store, before: before, onErr: onErr, done: make(chan struct{})}
	w.cond = sync.NewCond(&w.mu)
	go w.run()
	return w
}

// Add queues an item behind those already queued.
func (w *writer) Add(item json.RawMessage) {
	w.enqueue(logAppend{item: item})
}

func (w *writer) AddResponse(response *Response) {
	w.enqueue(logAppend{response: response})
}

func (w *writer) addFileIntent(intent fileIntentLine) error {
	return w.enqueue(logAppend{fileIntent: &intent})
}

func (w *writer) enqueue(record logAppend) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return w.err
	}
	if w.closed {
		return errors.New("the conversation writer is closed")
	}
	w.queue = append(w.queue, record)
	w.cond.Broadcast()
	return nil
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

		for _, record := range batch {
			if err := w.append(record); err != nil {
				w.mu.Lock()
				w.err = err
				w.queue = nil
				w.mu.Unlock()
				if w.onErr != nil {
					w.onErr(err)
				}
				break
			}
		}

		w.mu.Lock()
		w.inflight = false
		w.cond.Broadcast()
		w.mu.Unlock()
	}
}

func (w *writer) append(record logAppend) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("the werk log writer failed unexpectedly")
		}
	}()
	if record.fileIntent != nil {
		if w.before != nil {
			raw, err := json.Marshal(record.fileIntent)
			if err != nil {
				return err
			}
			if err := w.before(raw); err != nil {
				return err
			}
		}
		return w.store.appendFileIntent(*record.fileIntent)
	}
	if record.response != nil {
		if w.before != nil {
			for _, item := range record.response.Output {
				if err := w.before(item); err != nil {
					return err
				}
			}
		}
		return w.store.AppendResponse(record.response)
	}
	if w.before != nil {
		if err := w.before(record.item); err != nil {
			return err
		}
	}
	return w.store.Append(record.item)
}

// Flush returns once every item added before it has been written. It returns
// the first write failure the log has had, if any: a log that has failed once
// is not durable, and a caller that needs durability must not go on.
func (w *writer) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	for len(w.queue) > 0 || w.inflight {
		w.cond.Wait()
	}
	return w.err
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
