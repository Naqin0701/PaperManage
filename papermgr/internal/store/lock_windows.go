//go:build windows

package store

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// acquireLock uses LockFileEx so a second instance fails immediately and the
// lock is released when the file handle is closed.
func acquireLock(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	var overlapped windows.Overlapped
	err = windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, &overlapped)
	if err != nil {
		f.Close()
		return nil, ErrLocked
	}
	return f, nil
}
