//go:build darwin

package wsproctree

import (
	"context"
	"os/exec"
	"time"
)

const supported = true

// psTimeout bounds the one `ps` a snapshot runs.
const psTimeout = 3 * time.Second

// snapshot runs one `ps` over every process (the owner-approved exception
// to "spawn only without an in-process route": the cgo-free kern.proc
// sysctl reports resident memory in a 16-bit field) and keeps roots'
// subtrees.
func snapshot(roots []int) ([]Proc, error) {
	ctx, cancel := context.WithTimeout(context.Background(), psTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-axo", "pid=,ppid=,rss=,cputime=,comm=").Output()
	if err != nil {
		return nil, err
	}
	return keep(parsePS(string(out)), roots), nil
}
