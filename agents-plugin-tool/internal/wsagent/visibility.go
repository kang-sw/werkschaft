package wsagent

import (
	"fmt"
	"time"

	"github.com/kang-sw/devenv/internal/wsdashboard"
)

// SetHidden archives only an open dashboard-hosted lead. The conversation and
// its held input remain intact. Restoring visibility alone launches nothing;
// queued input or mail uses the ordinary lazy wake path.
func (r *Runner) SetHidden(id string, hidden bool) (State, error) {
	return r.act(id, func(c *conv) error { return c.setHidden(hidden) })
}

func (c *conv) setHidden(hidden bool) error {
	if !c.rec.Lead {
		return badRequest("archive applies to a hosted lead session only")
	}
	if hidden && c.rec.HolderID != "" && !c.r.scanDone() {
		return conflict("the session is rejoining its holder; archive again after the holder scan")
	}
	unlock, ok := c.lockRow()
	if !ok {
		return conflict("the session is not owned by the dashboard")
	}
	// Fence the sidecar change against terminal takeover, as for mail/spawn.
	err := wsdashboard.WriteSessionHidden(c.r.opts.Dir, c.rec.ReplyID, hidden)
	if err == nil {
		c.setStatus(func() { c.hidden = hidden })
	}
	unlock()
	if err != nil {
		return err
	}
	if hidden {
		c.cancelQuotaContinuation(false)
		// Persist the no-start gate first: process-end/replay callbacks cannot
		// consume held input while the process is being stopped.
		c.stopProcess("")
		if err := c.retireHolder(); err != nil {
			// Do not report a successful processless archive when a holder
			// refused to stop. Restore visibility, but retain the error.
			if restore := wsdashboard.WriteSessionHidden(c.r.opts.Dir, c.rec.ReplyID, false); restore == nil {
				c.setStatus(func() { c.hidden = false })
			}
			c.r.notify(c.rec.ReplyID, c.rec.Root)
			return fmt.Errorf("the session was not archived: %w", err)
		}
	} else {
		c.signalMail()
		c.signalInbox()
	}
	c.writeRegistration()
	c.r.notify(c.rec.ReplyID, c.rec.Root)
	return nil
}

// PageInput admits a composer write without waiting for the conversation
// loop. As with Send, wmu serializes admission and the write against close,
// parking, and quota-continuation firing, while c.mu is released before
// invoking write so external callbacks (which may acquire dashboard locks and
// call OwnsLive) can never self-deadlock on c.mu. A committed own-page
// message restores a hidden session asynchronously; generic InboxChanged
// signals and Runner.Send remain passive.
func (r *Runner) PageInput(id string, write func() bool) error {
	c := r.conv(id)
	if c == nil {
		return errUnknown
	}
	committed, hidden, err := func() (bool, bool, error) {
		c.wmu.Lock()
		defer c.wmu.Unlock()
		c.mu.Lock()
		if c.parkedOut {
			c.mu.Unlock()
			return false, false, errUnknown
		}
		if c.closed {
			err := endedError(c.rec)
			c.mu.Unlock()
			return false, false, err
		}
		select {
		case <-c.done:
			c.mu.Unlock()
			return false, false, errClosed
		default:
		}
		c.mu.Unlock()

		committed := write()

		c.mu.Lock()
		if committed && !c.closed {
			c.cancelQuotaContinuationForInput()
			if c.rec.Lead {
				// Update in memory only; rides the next agent.json write (0 extra I/O).
				c.rec.LastActiveAt = r.opts.Now().UTC().Format(time.RFC3339)
			}
		}
		hidden := c.hidden
		c.mu.Unlock()
		return committed, hidden, nil
	}()
	if err != nil {
		return err
	}
	if committed && !hidden {
		c.signalInbox()
	}
	if committed && hidden {
		go c.do(func() {
			if c.closed {
				return
			}
			if c.hidden {
				if err := c.setHidden(false); err != nil {
					c.setStatus(func() { c.lastErr = "restore session for page input: " + err.Error() })
					c.r.notify(c.rec.ReplyID, c.rec.Root)
					return
				}
			}
			// The immediate post-write wake may have run while still hidden.
			c.inboxChanged()
		})
	}
	return nil
}
