package fileops

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExclusivePublicationAndFallbackFailure(t *testing.T) {
	for _, scenario := range []string{"link", "copy", "copy fails", "existing"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			temp, dest := filepath.Join(dir, "temp"), filepath.Join(dir, "result.nfo")
			if err := os.WriteFile(temp, []byte("complete metadata"), 0o640); err != nil {
				t.Fatal(err)
			}
			when := time.Unix(1700000000, 0)
			_ = os.Chtimes(temp, when, when)
			link := os.Link
			if scenario != "link" {
				link = func(string, string) error { return errors.New("links unavailable") }
			}
			copyFn := io.Copy
			if scenario == "copy fails" {
				copyFn = func(w io.Writer, _ io.Reader) (int64, error) {
					n, _ := w.Write([]byte("partial"))
					return int64(n), errors.New("disk full")
				}
			}
			if scenario == "existing" {
				if err := os.WriteFile(dest, []byte("manual metadata"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			err := publishWith(temp, dest, 0o640, link, copyFn)
			if scenario == "copy fails" {
				if err == nil {
					t.Fatal("ignored copy failure")
				}
				if _, err := os.Stat(dest); !os.IsNotExist(err) {
					t.Fatal("incomplete destination remained")
				}
				return
			}
			if scenario == "existing" {
				if err == nil {
					t.Fatal("overwrote destination")
				}
				if data, _ := os.ReadFile(dest); string(data) != "manual metadata" {
					t.Fatal("manual file changed")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if data, _ := os.ReadFile(dest); string(data) != "complete metadata" {
				t.Fatal("published incomplete data")
			}
			stat, _ := os.Stat(dest)
			if !stat.ModTime().Equal(when) {
				t.Fatal("fallback lost timestamp")
			}
		})
	}
}

func TestWriteNewNeverOverwritesAndLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "episode.nfo")
	if err := WriteNew(path, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteNew(path, []byte("second"), 0o644); err == nil {
		t.Fatal("overwrote existing NFO")
	}
	if data, _ := os.ReadFile(path); string(data) != "first" {
		t.Fatal("existing data changed")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatal("temporary file remained")
	}
}

func TestReadRegularBoundsAndRejectsSymlinks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sidecar.nfo")
	if err := os.WriteFile(path, []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRegular(path, 4); err == nil {
		t.Fatal("accepted oversized sidecar")
	}
	if data, err := ReadRegular(path, 5); err != nil || string(data) != "12345" {
		t.Fatalf("bounded read: %q, %v", data, err)
	}
	link := filepath.Join(dir, "link.nfo")
	if err := os.Symlink(path, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := ReadRegular(link, 10); err == nil {
		t.Fatal("followed sidecar symlink")
	}
}

func TestMkdirAllCreatesChainWithoutReplacingExistingFiles(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "new", "work", "duplicate")
	if err := MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := MkdirAll(target, 0o755); err != nil {
		t.Fatalf("existing directory: %v", err)
	}
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := MkdirAll(filepath.Join(file, "child"), 0o755); err == nil {
		t.Fatal("accepted a file as a directory")
	}
	if data, _ := os.ReadFile(file); string(data) != "original" {
		t.Fatal("changed existing file")
	}
}
