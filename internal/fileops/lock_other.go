//go:build !linux && !darwin && !windows

package fileops

import (
	"fmt"
	"os"
)

func tryFileLock(*os.File) error {
	return fmt.Errorf("filesystem locking is unsupported on this platform")
}
func unlockFile(*os.File) error { return nil }
