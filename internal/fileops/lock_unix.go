//go:build linux || darwin

package fileops

import (
	"golang.org/x/sys/unix"
	"os"
)

func tryFileLock(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) }
func unlockFile(f *os.File) error  { return unix.Flock(int(f.Fd()), unix.LOCK_UN) }
