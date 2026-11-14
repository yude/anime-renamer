//go:build linux || darwin

package fileops

import (
	"os"
	"syscall"
)

// PreserveOwner avoids silently changing ownership when replacing a video.
func PreserveOwner(path string, source os.FileInfo) error {
	stat, ok := source.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	current, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if owner, ok := current.Sys().(*syscall.Stat_t); ok && owner.Uid == stat.Uid && owner.Gid == stat.Gid {
		return nil
	}
	return os.Chown(path, int(stat.Uid), int(stat.Gid))
}
