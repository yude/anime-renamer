// Package fileops provides shared no-overwrite publication and source checks.
package fileops

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

func Unchanged(path string, before os.FileInfo) error {
	after, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if before == nil || !before.Mode().IsRegular() || !after.Mode().IsRegular() || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || before.Mode() != after.Mode() {
		return fmt.Errorf("source changed while processing: %s", path)
	}
	return nil
}

// ReadRegular bounds sidecar reads even if the file grows after the initial
// stat. Reject symlinks and changes instead of trusting stale metadata.
func ReadRegular(path string, limit int64) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() > limit || limit < 0 {
		return nil, fmt.Errorf("file is not regular or exceeds %d bytes: %s", limit, path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(before, opened) {
		return nil, fmt.Errorf("file changed while opening: %s", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file exceeds %d bytes: %s", limit, path)
	}
	if err := Unchanged(path, before); err != nil {
		return nil, err
	}
	return data, nil
}

// SyncDir makes directory-entry changes durable where the OS supports it.
// Windows does not expose a portable directory fsync through os.File.Sync.
func SyncDir(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}

// MkdirAll persists the new directory chain before callers can remove a
// recording's old name. Syncing only the final directory does not persist its
// own entry in its parent after a crash.
func MkdirAll(path string, mode os.FileMode) error {
	path = filepath.Clean(path)
	var missing []string
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Stat(current)
		if err == nil {
			if !info.IsDir() {
				return fmt.Errorf("not a directory: %s", current)
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		missing = append(missing, current)
		if filepath.Dir(current) == current {
			return err
		}
	}
	if err := os.MkdirAll(path, mode); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("destination is not a real directory: %s", path)
	}
	for _, dir := range missing {
		if err := SyncDir(dir); err != nil {
			return err
		}
		if err := SyncDir(filepath.Dir(dir)); err != nil {
			return err
		}
	}
	return nil
}

// RemoveEmpty never recursively removes recovery data or another job's files.
func RemoveEmpty(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refuse to remove non-directory: %s", path)
	}
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || len(entries) != 0 {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		// Another job may have populated the shared directory in the meantime.
		if entries, readErr := os.ReadDir(path); readErr == nil && len(entries) > 0 {
			return nil
		}
		return err
	}
	return SyncDir(filepath.Dir(path))
}

// Publish installs a complete temporary file without replacing an existing
// destination. Filesystems without hard links use an exclusive, synced copy;
// ordinary errors remove only the incomplete file created by this operation.
func Publish(temp, destination string, mode os.FileMode) (err error) {
	return publishWith(temp, destination, mode, os.Link, io.Copy)
}

func publishWith(temp, destination string, mode os.FileMode, link func(string, string) error, copyContents func(io.Writer, io.Reader) (int64, error)) (err error) {
	if err := link(temp, destination); err == nil {
		return SyncDir(filepath.Dir(destination))
	} else if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("destination already exists: %s", destination)
	}
	in, err := os.Open(temp)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("completed temp is not a regular file")
	}
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		_ = out.Close()
		if !committed {
			if removeErr := os.Remove(destination); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				err = errors.Join(err, fmt.Errorf("remove incomplete destination %s: %w", destination, removeErr))
			}
		}
	}()
	if _, err = copyContents(out, in); err != nil {
		return err
	}
	if err = PreserveOwner(destination, info); err != nil {
		return err
	}
	if err = os.Chtimes(destination, info.ModTime(), info.ModTime()); err != nil {
		return err
	}
	if err = out.Chmod(mode); err != nil {
		return err
	}
	if err = PreserveXattrs(temp, destination); err != nil {
		return err
	}
	if err = errors.Join(out.Sync(), out.Close()); err != nil {
		return err
	}
	if err = SyncDir(filepath.Dir(destination)); err != nil {
		return err
	}
	committed = true
	return nil
}

func WriteNew(path string, data []byte, mode os.FileMode) (err error) {
	temp, err := os.CreateTemp(filepath.Dir(path), ".anime-renamer-nfo-*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		_ = temp.Close()
		if cleanup := os.Remove(temp.Name()); cleanup != nil && !errors.Is(cleanup, os.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("remove temp %s: %w", temp.Name(), cleanup))
		}
	}()
	if err := temp.Chmod(mode); err != nil {
		return err
	}
	if _, err := temp.Write(data); err != nil {
		return err
	}
	if err := errors.Join(temp.Sync(), temp.Close()); err != nil {
		return err
	}
	return Publish(temp.Name(), path, mode)
}
