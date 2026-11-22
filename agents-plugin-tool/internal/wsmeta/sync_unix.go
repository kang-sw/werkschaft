//go:build !windows

package wsmeta

import "os"

// platformSyncDir fsyncs a directory, making its entries durable: a rename
// into it or a removal from it.
func platformSyncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
