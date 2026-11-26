//go:build !linux

package wsagent

func oneShotProcessZombie(int) bool { return false }
