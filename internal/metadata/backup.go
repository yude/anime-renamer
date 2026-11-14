package metadata

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/yude/anime-renamer/internal/fileops"
)

type replacementOps struct {
	backup  func(string, string) error
	replace func(string, string) error
	syncDir func(string) error
	remove  func(string) error
}

func commitReplacement(path, temp, backup string, before os.FileInfo, keep bool, ops replacementOps) (replaced bool, err error) {
	if err := fileops.Unchanged(path, before); err != nil {
		return false, err
	}
	if err := ops.backup(path, backup); err != nil {
		return false, errors.Join(err, fileops.RemoveEmpty(filepath.Dir(backup)))
	}
	recoveryError := func(err error) (bool, error) {
		return replaced, fmt.Errorf("%w (recovery backup retained at %s)", err, backup)
	}
	if err := ops.syncDir(filepath.Dir(backup)); err != nil {
		return recoveryError(err)
	}
	if err := ops.syncDir(filepath.Dir(path)); err != nil {
		return recoveryError(err)
	}
	if err := fileops.Unchanged(path, before); err != nil {
		return recoveryError(err)
	}
	if err := ops.replace(temp, path); err != nil {
		return recoveryError(err)
	}
	replaced = true
	// Do not discard the original until the replacement directory entry has
	// been committed. A sync failure leaves the recovery copy available.
	if err := ops.syncDir(filepath.Dir(path)); err != nil {
		return recoveryError(err)
	}
	if keep {
		return true, nil
	}
	if err := ops.remove(backup); err != nil {
		return recoveryError(err)
	}
	if err := ops.syncDir(filepath.Dir(backup)); err != nil {
		return true, err
	}
	return true, fileops.RemoveEmpty(filepath.Dir(backup))
}

// Old versions and interrupted commits can leave a completed recovery copy.
// Remove one only after revalidating every original stream and chapter against
// the current video. Unverified, unrelated or unfinished backups are retained.
func finishExistingBackup(ctx context.Context, path string, current os.FileInfo, updated *probe, tags map[string]string, mp4, keep bool) (string, error) {
	backup := BackupPath(path)
	before, err := os.Lstat(backup)
	if errors.Is(err, os.ErrNotExist) {
		return "", fileops.RemoveEmpty(filepath.Dir(backup))
	}
	if err != nil {
		return backup, err
	}
	if !before.Mode().IsRegular() {
		return backup, fmt.Errorf("recovery backup is not a regular file: %s", backup)
	}
	if keep {
		return backup, nil
	}
	original, err := readProbeContext(ctx, backup)
	if err == nil {
		err = verifyEmbeddedFiles(ctx, backup, path, original, updated, tags, mp4)
	}
	if err == nil {
		err = fileops.Unchanged(backup, before)
	}
	if err == nil {
		err = fileops.Unchanged(path, current)
	}
	if err == nil {
		err = fileops.SyncDir(filepath.Dir(path))
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return backup, fmt.Errorf("retain unverified recovery backup %s: %w", backup, err)
	}
	if err := os.Remove(backup); err != nil {
		return backup, fmt.Errorf("remove completed backup %s: %w", backup, err)
	}
	if err := fileops.SyncDir(filepath.Dir(backup)); err != nil {
		return "", err
	}
	return "", fileops.RemoveEmpty(filepath.Dir(backup))
}

func makeBackup(source, destination string) (err error) {
	dir := filepath.Dir(destination)
	if err := fileops.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Link(source, destination); err == nil {
		return nil
	} else if errors.Is(err, os.ErrExist) {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	before, err := in.Stat()
	if err != nil {
		return err
	}
	if !before.Mode().IsRegular() {
		return fmt.Errorf("backup source is not a regular file")
	}
	out, err := os.CreateTemp(dir, ".backup-*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		_ = out.Close()
		if cleanup := os.Remove(out.Name()); cleanup != nil && !errors.Is(cleanup, os.ErrNotExist) {
			err = errors.Join(err, cleanup)
		}
	}()
	if _, err := io.CopyN(out, in, before.Size()); err != nil {
		return err
	}
	if err := fileops.Unchanged(source, before); err != nil {
		return err
	}
	if err := errors.Join(out.Sync(), out.Close()); err != nil {
		return err
	}
	if err := fileops.PreserveOwner(out.Name(), before); err != nil {
		return err
	}
	if err := os.Chmod(out.Name(), before.Mode().Perm()); err != nil {
		return err
	}
	if err := fileops.PreserveXattrs(source, out.Name()); err != nil {
		return err
	}
	if err := os.Chtimes(out.Name(), before.ModTime(), before.ModTime()); err != nil {
		return err
	}
	completed, err := os.OpenFile(out.Name(), os.O_RDWR, 0)
	if err != nil {
		return err
	}
	if err := errors.Join(completed.Sync(), completed.Close()); err != nil {
		return err
	}
	return fileops.Publish(out.Name(), destination, before.Mode().Perm())
}
