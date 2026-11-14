package fileops

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"testing"
)

func TestPreserveExtendedAttributesAndRemoveInheritedExtras(t *testing.T) {
	dir := t.TempDir()
	source, dest := filepath.Join(dir, "source"), filepath.Join(dir, "dest")
	for _, p := range []string{source, dest} {
		if err := os.WriteFile(p, []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := unix.Setxattr(source, "user.anime-renamer", []byte("original"), 0); err != nil {
		if errors.Is(err, unix.ENOTSUP) {
			t.Skip(err)
		}
		t.Fatal(err)
	}
	if err := unix.Setxattr(dest, "user.inherited", []byte("unexpected"), 0); err != nil {
		t.Fatal(err)
	}
	if err := PreserveXattrs(source, dest); err != nil {
		t.Fatal(err)
	}
	if value, err := attributeValue(dest, "user.anime-renamer"); err != nil || string(value) != "original" {
		t.Fatalf("attribute lost: %q, %v", value, err)
	}
	if _, err := attributeValue(dest, "user.inherited"); err == nil {
		t.Fatal("inherited attribute remained")
	}
}
