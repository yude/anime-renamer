package renamer

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCopyRejectsChangedSourceWithoutPublishing(t *testing.T) {
	dir := t.TempDir()
	source, dest := filepath.Join(dir, "source.mp4"), filepath.Join(dir, "dest.mp4")
	if err := os.WriteFile(source, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := copyFileWith(source, dest, func(out io.Writer, in io.Reader, n int64) (int64, error) {
		copied, err := io.CopyN(out, in, n)
		if err != nil {
			return copied, err
		}
		return copied, os.WriteFile(source, []byte("recorder changed contents"), 0o644)
	})
	if err == nil {
		t.Fatal("accepted changed recording")
	}
	if data, _ := os.ReadFile(source); string(data) != "recorder changed contents" {
		t.Fatal("changed recording was overwritten")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("published a partial snapshot")
	}
	if temps, _ := filepath.Glob(filepath.Join(dir, ".anime-renamer-move-*.tmp")); len(temps) != 0 {
		t.Fatal("temp files remained")
	}
}

func TestCopyLongFilenameAndTimestamp(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.mp4")
	dest := filepath.Join(dir, strings.Repeat("a", 251)+".mp4")
	data := []byte("recording")
	if err := os.WriteFile(source, data, 0o640); err != nil {
		t.Fatal(err)
	}
	when := time.Unix(1700000000, 0)
	if err := os.Chtimes(source, when, when); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(source, dest); err != nil {
		t.Fatal(err)
	}
	if actual, _ := os.ReadFile(dest); !bytes.Equal(actual, data) {
		t.Fatal("copied contents differ")
	}
	stat, _ := os.Stat(dest)
	if !stat.ModTime().Equal(when) {
		t.Fatal("copy lost recording timestamp")
	}
}
