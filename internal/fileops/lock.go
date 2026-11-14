package fileops

import (
	"errors"
	"fmt"
	"os"
)

// Lock serializes cooperating writers without waiting. An unheld lock file
// left by process termination is reusable; ownership of a live lock is
// determined by the OS, never by guessing from a PID or timestamp.
func Lock(path string) (release func() error, err error) {
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("lock is not a regular file: %s", path)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := tryFileLock(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("another metadata operation is active, or filesystem locking is unavailable (%s): %w", path, err)
	}
	current, err := os.Lstat(path)
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(info, current) {
		_ = unlockFile(f)
		_ = f.Close()
		return nil, fmt.Errorf("lock path changed while acquiring it: %s", path)
	}
	return func() error {
		var cleanup error
		current, err := os.Lstat(path)
		if err == nil && current.Mode().IsRegular() && os.SameFile(info, current) {
			cleanup = os.Remove(path)
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanup = err
		} else if err == nil {
			cleanup = fmt.Errorf("lock path was replaced: %s", path)
		}
		// Unlink while still holding the lock. A late opener must recheck the
		// inode after acquiring it, as done above, so it cannot lock an old inode.
		return errors.Join(cleanup, unlockFile(f), f.Close())
	}, nil
}
