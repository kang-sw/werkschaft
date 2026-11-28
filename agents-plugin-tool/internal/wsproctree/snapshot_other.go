//go:build !darwin && !linux && !windows

package wsproctree

const supported = false

func snapshot([]int) ([]Proc, error) { return nil, ErrUnsupported }
