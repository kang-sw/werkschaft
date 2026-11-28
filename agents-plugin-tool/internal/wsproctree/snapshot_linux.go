//go:build linux

package wsproctree

import (
	"os"
	"strconv"
)

const supported = true

// userHZ is the clock-tick rate /proc reports times in (USER_HZ, 100 on
// every Linux ABI Go supports).
const userHZ = 100

// snapshot reads /proc/<pid>/stat of every process, in process, and keeps
// roots' subtrees. A process that ends mid-read is skipped.
func snapshot(roots []int) ([]Proc, error) {
	dir, err := os.Open("/proc")
	if err != nil {
		return nil, err
	}
	names, err := dir.Readdirnames(-1)
	dir.Close()
	if err != nil {
		return nil, err
	}
	page := int64(os.Getpagesize())
	all := make([]Proc, 0, len(names))
	for _, name := range names {
		if _, err := strconv.Atoi(name); err != nil {
			continue
		}
		b, err := os.ReadFile("/proc/" + name + "/stat")
		if err != nil {
			continue
		}
		p, err := parseStat(string(b), userHZ, page)
		if err != nil {
			continue
		}
		all = append(all, p)
	}
	return keep(all, roots), nil
}
