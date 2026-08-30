package renamer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yude/anime-renamer/internal/annict"
	"github.com/yude/anime-renamer/internal/matcher"
)

func float64Ptr(f float64) *float64 {
	return &f
}

func TestBuildPath(t *testing.T) {
	tests := []struct {
		name         string
		originalPath string
		match        *matcher.MatchResult
		wantPath     string
		wantErr      bool
	}{
		{
			name:         "standard",
			originalPath: "/recordings/花ざかりの君たちへ 第2期 ep．7「ずっとそばにいたいから」 (20260813).mp4",
			match: &matcher.MatchResult{
				Work:    &annict.Work{ID: 4168, Title: "花ざかりの君たちへ 第2期"},
				Episode: &annict.Episode{ID: 1007, Number: float64Ptr(7), Title: "ずっとそばにいたいから"},
			},
			wantPath: "/recordings/花ざかりの君たちへ 第2期/花ざかりの君たちへ 第2期 #7 「ずっとそばにいたいから」.mp4",
		},
		{
			name:         "episode 10 no zero pad",
			originalPath: "/recordings/作品 ep.10「テスト」 (20260801).mp4",
			match: &matcher.MatchResult{
				Work:    &annict.Work{ID: 1, Title: "作品"},
				Episode: &annict.Episode{ID: 1, Number: float64Ptr(10), Title: "テスト"},
			},
			wantPath: "/recordings/作品/作品 #10 「テスト」.mp4",
		},
		{
			name:         "no subtitle",
			originalPath: "/recordings/作品 ep.1 (20260801).mp4",
			match: &matcher.MatchResult{
				Work:    &annict.Work{ID: 1, Title: "作品"},
				Episode: &annict.Episode{ID: 1, Number: float64Ptr(1)},
			},
			wantPath: "/recordings/作品/作品 #1.mp4",
		},
		{
			name:         "nil match",
			originalPath: "/recordings/test.mp4",
			match:        nil,
			wantErr:      true,
		},
		{
			name:         "nil episode",
			originalPath: "/recordings/test.mp4",
			match: &matcher.MatchResult{
				Work: &annict.Work{ID: 1, Title: "作品"},
			},
			wantErr: true,
		},
		{
			name:         "title and subtitle with filesystem-illegal characters",
			originalPath: "/recordings/test.mp4",
			match: &matcher.MatchResult{
				Work:    &annict.Work{ID: 1, Title: "作品/2"},
				Episode: &annict.Episode{ID: 1, Number: float64Ptr(1), Title: "A:B?"},
			},
			// "/" would otherwise be split into an unintended subdirectory,
			// and ":" "?" are rejected in file names on Windows.
			wantPath: "/recordings/作品／2/作品／2 #1 「A：B？」.mp4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BuildPath(tt.originalPath, tt.match)
			if tt.wantErr {
				if err == nil {
					t.Errorf("BuildPath() succeeded, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("BuildPath() error = %v", err)
			}
			if got != tt.wantPath {
				t.Errorf("BuildPath() = %q, want %q", got, tt.wantPath)
			}
		})
	}
}

func TestRenameDryRun(t *testing.T) {
	// Create temp dir with a test file
	dir := t.TempDir()
	src := filepath.Join(dir, "test.mp4")
	if err := os.WriteFile(src, []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := &matcher.MatchResult{
		Work:    &annict.Work{ID: 1, Title: "作品"},
		Episode: &annict.Episode{ID: 1, Number: float64Ptr(7), Title: "テスト"},
	}

	r := Rename(src, result, true, "")
	if r.Error != nil {
		t.Fatalf("Rename(dry-run) error = %v", r.Error)
	}
	if r.Renamed {
		t.Error("Rename(dry-run) should not rename files")
	}

	// Original file should still exist
	if _, err := os.Stat(src); os.IsNotExist(err) {
		t.Error("Original file should still exist after dry-run")
	}
}

func TestRenameActual(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "test.mp4")
	if err := os.WriteFile(src, []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := &matcher.MatchResult{
		Work:    &annict.Work{ID: 1, Title: "作品"},
		Episode: &annict.Episode{ID: 1, Number: float64Ptr(7), Title: "テスト"},
	}

	r := Rename(src, result, false, "")
	if r.Error != nil {
		t.Fatalf("Rename() error = %v", r.Error)
	}
	if !r.Renamed {
		t.Error("Rename() should have renamed the file")
	}

	// New file should exist
	expectedPath := filepath.Join(dir, "作品", "作品 #7 「テスト」.mp4")
	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Errorf("Renamed file should exist at %s", expectedPath)
	}

	// Old file should not exist
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("Original file should not exist after rename")
	}
}

func TestRenameExistingDestination(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "test.mp4")
	dst := filepath.Join(dir, "作品", "作品 #7 「テスト」.mp4")

	if err := os.WriteFile(src, []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := &matcher.MatchResult{
		Work:    &annict.Work{ID: 1, Title: "作品"},
		Episode: &annict.Episode{ID: 1, Number: float64Ptr(7), Title: "テスト"},
	}

	r := Rename(src, result, false, "")
	if r.Error == nil {
		t.Error("Rename() should error when destination exists")
	}
}

func TestRenameWithOutputDir(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "test.mp4")
	if err := os.WriteFile(src, []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}

	outputDir := filepath.Join(dir, "out")

	result := &matcher.MatchResult{
		Work:    &annict.Work{ID: 1, Title: "作品"},
		Episode: &annict.Episode{ID: 1, Number: float64Ptr(7), Title: "テスト"},
	}

	r := Rename(src, result, false, outputDir)
	if r.Error != nil {
		t.Fatalf("Rename() error = %v", r.Error)
	}
	if !r.Renamed {
		t.Error("Rename() should have renamed the file")
	}

	expectedPath := filepath.Join(outputDir, "作品", "作品 #7 「テスト」.mp4")
	if r.NewPath != expectedPath {
		t.Errorf("Rename() NewPath = %q, want %q", r.NewPath, expectedPath)
	}
	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Errorf("renamed file should exist under outputDir at %s", expectedPath)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("original file should not exist after rename")
	}
}

func TestRenameDryRunReportsExistingDestination(t *testing.T) {
	// Regression test: dry-run must surface the same "already exists"
	// error an actual run would, so previews are accurate.
	dir := t.TempDir()
	src := filepath.Join(dir, "test.mp4")
	dst := filepath.Join(dir, "作品", "作品 #7 「テスト」.mp4")

	if err := os.WriteFile(src, []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := &matcher.MatchResult{
		Work:    &annict.Work{ID: 1, Title: "作品"},
		Episode: &annict.Episode{ID: 1, Number: float64Ptr(7), Title: "テスト"},
	}

	r := Rename(src, result, true, "")
	if r.Error == nil {
		t.Error("Rename(dry-run) should report an error when destination exists")
	}
	if _, err := os.Stat(src); os.IsNotExist(err) {
		t.Error("dry-run must not touch the original file")
	}
}

func TestCopyFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp4")
	dst := filepath.Join(dir, "dst.mp4")

	content := []byte("copied content")
	if err := os.WriteFile(src, content, 0o640); err != nil {
		t.Fatal(err)
	}

	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copyFile() error = %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read copied file: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("copied content = %q, want %q", got, content)
	}

	// Source must be untouched; copyFile never removes it.
	if _, err := os.Stat(src); err != nil {
		t.Errorf("source should still exist after copyFile: %v", err)
	}

	// No leftover temp file.
	if _, err := os.Stat(dst + ".anime-renamer-tmp"); !os.IsNotExist(err) {
		t.Error("copyFile should not leave a temp file behind on success")
	}
}

func TestCopyFileCleansUpTempFileOnFailure(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp4")
	if err := os.WriteFile(src, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A destination inside a non-existent directory makes the temp file
	// creation fail immediately.
	dst := filepath.Join(dir, "no-such-subdir", "dst.mp4")

	if err := copyFile(src, dst); err == nil {
		t.Fatal("copyFile() into a missing directory should fail")
	}
	if _, err := os.Stat(dst + ".anime-renamer-tmp"); !os.IsNotExist(err) {
		t.Error("copyFile should not leave a temp file behind on failure")
	}
}

func TestMoveFile_AcrossFilesystems(t *testing.T) {
	// os.CreateTemp("", ...) lands under the OS temp dir (tmpfs on many
	// systems), while "." here is the package's own directory on the
	// repo's regular filesystem — commonly a genuine cross-device pair,
	// exercising moveFile's copy+remove fallback for os.Rename's
	// "invalid cross-device link". If the environment happens to put
	// both on the same filesystem, this still validates moveFile's
	// plain-rename fast path instead — either way, the move must
	// succeed and the content must be intact.
	src, err := os.CreateTemp("", "anime-renamer-move-src-*")
	if err != nil {
		t.Fatal(err)
	}
	srcPath := src.Name()
	defer os.Remove(srcPath)

	content := []byte("cross-device content")
	if _, err := src.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}

	dstDir, err := os.MkdirTemp(".", "scratch-move-dst-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dstDir)
	dstPath := filepath.Join(dstDir, "moved.mp4")

	if err := moveFile(srcPath, dstPath); err != nil {
		t.Fatalf("moveFile() error = %v", err)
	}

	got, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("read moved file: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("moved content = %q, want %q", got, content)
	}
	if _, err := os.Stat(srcPath); !os.IsNotExist(err) {
		t.Error("original file should not exist after moveFile")
	}
}
