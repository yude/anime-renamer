package renamer

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"

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
			name:         "already inside work directory is not nested again",
			originalPath: "/recordings/作品/作品 #1 「第一話」.mp4",
			match: &matcher.MatchResult{
				Work:    &annict.Work{ID: 1, Title: "作品"},
				Episode: &annict.Episode{ID: 1, Number: float64Ptr(1), Title: "第一話"},
			},
			wantPath: "/recordings/作品/作品 #1 「第一話」.mp4",
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
			name:         "matched public label overrides work-local number",
			originalPath: "/recordings/作品 第44話.mp4",
			match: &matcher.MatchResult{
				Work:                &annict.Work{ID: 1, Title: "作品"},
				Episode:             &annict.Episode{ID: 1, Number: float64Ptr(5), NumberText: "第44話", Title: "第四十四話"},
				OutputEpisodeNumber: 44,
			},
			wantPath: "/recordings/作品/作品 #44 「第四十四話」.mp4",
		},
		{
			name:         "explicitly verified episode zero",
			originalPath: "/recordings/作品 #01「life.00 序章」.mp4",
			match: &matcher.MatchResult{
				Work:                &annict.Work{ID: 1, Title: "作品"},
				Episode:             &annict.Episode{ID: 1, Number: float64Ptr(0), NumberText: "life.0", Title: "序章"},
				OutputEpisodeNumber: 0,
				OutputNumberSet:     true,
			},
			wantPath: "/recordings/作品/作品 #0 「序章」.mp4",
		},
		{
			name:         "preserve transport stream extension",
			originalPath: "/recordings/作品 ep.7「テスト」 (20260801).ts",
			match: &matcher.MatchResult{
				Work:    &annict.Work{ID: 1, Title: "作品"},
				Episode: &annict.Episode{ID: 1, Number: float64Ptr(7), Title: "テスト"},
			},
			wantPath: "/recordings/作品/作品 #7 「テスト」.ts",
		},
		{
			name:         "preserve Blu-ray transport stream extension",
			originalPath: "/recordings/作品 ep.7「テスト」 (20260801).m2ts",
			match: &matcher.MatchResult{
				Work:    &annict.Work{ID: 1, Title: "作品"},
				Episode: &annict.Episode{ID: 1, Number: float64Ptr(7), Title: "テスト"},
			},
			wantPath: "/recordings/作品/作品 #7 「テスト」.m2ts",
		},
		{
			name:         "preserve uppercase extension",
			originalPath: "/recordings/作品 ep.7「テスト」 (20260801).MKV",
			match: &matcher.MatchResult{
				Work:    &annict.Work{ID: 1, Title: "作品"},
				Episode: &annict.Episode{ID: 1, Number: float64Ptr(7), Title: "テスト"},
			},
			wantPath: "/recordings/作品/作品 #7 「テスト」.MKV",
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
			name:         "matched file subtitle replaces API placeholder",
			originalPath: "/recordings/義妹生活 #12 「tomorrow and tomorrow」.mp4",
			match: &matcher.MatchResult{
				Work:         &annict.Work{ID: 1, Title: "義妹生活"},
				Episode:      &annict.Episode{ID: 161796, Number: float64Ptr(12), Title: "　　と　　"},
				FileSubtitle: "tomorrow and tomorrow",
			},
			wantPath: "/recordings/義妹生活/義妹生活 #12 「tomorrow and tomorrow」.mp4",
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
			name:         "fractional episode number is rejected instead of truncated",
			originalPath: "/recordings/test.mp4",
			match: &matcher.MatchResult{
				Work:    &annict.Work{ID: 1, Title: "作品"},
				Episode: &annict.Episode{ID: 1, Number: float64Ptr(7.5), SortNumber: 7},
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
		{
			name:         "path traversal title is rejected",
			originalPath: "/recordings/test.mp4",
			match: &matcher.MatchResult{
				Work:    &annict.Work{ID: 1, Title: ".."},
				Episode: &annict.Episode{ID: 1, Number: float64Ptr(1)},
			},
			wantErr: true,
		},
		{
			name:         "Windows reserved directory name is prefixed",
			originalPath: "/recordings/test.mp4",
			match: &matcher.MatchResult{
				Work:    &annict.Work{ID: 1, Title: "CON.txt"},
				Episode: &annict.Episode{ID: 1, Number: float64Ptr(1)},
			},
			wantPath: "/recordings/＿CON.txt/＿CON.txt #1.mp4",
		},
		{
			name:         "control characters and trailing dots are sanitized",
			originalPath: "/recordings/test.mp4",
			match: &matcher.MatchResult{
				Work:    &annict.Work{ID: 1, Title: "作品. "},
				Episode: &annict.Episode{ID: 1, Number: float64Ptr(1), Title: "前編\n後編"},
			},
			wantPath: "/recordings/作品/作品 #1 「前編 後編」.mp4",
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

func TestBuildPathTruncatesLongSubtitleAtUTF8Boundary(t *testing.T) {
	result := &matcher.MatchResult{
		Work:    &annict.Work{ID: 1, Title: "神無き世界のカミサマ活動"},
		Episode: &annict.Episode{ID: 1, Number: float64Ptr(1), Title: strings.Repeat("とても長い公式字幕 ", 30)},
	}
	got, err := BuildPath("/recordings/source.mp4", result)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Base(got)
	if len(name) > maxFilenameBytes {
		t.Fatalf("generated filename is %d bytes, want <= %d: %q", len(name), maxFilenameBytes, name)
	}
	if !utf8.ValidString(name) {
		t.Fatalf("generated filename is not valid UTF-8: %q", name)
	}
	if !strings.HasPrefix(name, "神無き世界のカミサマ活動 #1 「") || !strings.HasSuffix(name, "…」.mp4") {
		t.Fatalf("generated filename did not preserve identity and extension: %q", name)
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
	if !r.Previewed {
		t.Error("Rename(dry-run) should mark the valid destination as previewed")
	}

	// Original file should still exist
	if _, err := os.Stat(src); os.IsNotExist(err) {
		t.Error("Original file should still exist after dry-run")
	}
}

func TestRenameRejectsInvalidSourceState(t *testing.T) {
	t.Run("nil match returns an error instead of panicking", func(t *testing.T) {
		r := Rename(filepath.Join(t.TempDir(), "test.mp4"), nil, true, "")
		if r.Error == nil {
			t.Fatal("Rename() with nil match should return an error")
		}
	})

	result := &matcher.MatchResult{
		Work:    &annict.Work{ID: 1, Title: "作品"},
		Episode: &annict.Episode{ID: 1, Number: float64Ptr(1), Title: "第一話"},
	}

	t.Run("dry-run rejects a missing source", func(t *testing.T) {
		r := Rename(filepath.Join(t.TempDir(), "missing.mp4"), result, true, "")
		if r.Error == nil {
			t.Fatal("Rename(dry-run) should reject a missing source")
		}
	})

	t.Run("dry-run rejects a symlink source", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("creating symlinks may require additional privileges on Windows")
		}
		dir := t.TempDir()
		realPath := filepath.Join(dir, "real.mp4")
		linkPath := filepath.Join(dir, "link.mp4")
		if err := os.WriteFile(realPath, []byte("recording"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(realPath, linkPath); err != nil {
			t.Fatal(err)
		}
		r := Rename(linkPath, result, true, "")
		if r.Error == nil {
			t.Fatal("Rename(dry-run) should reject a symlink source")
		}
	})
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

func TestRenameAlreadyAtDestinationIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	workDir := filepath.Join(dir, "作品")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(workDir, "作品 #1 「第一話」.mp4")
	if err := os.WriteFile(src, []byte("recording"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := &matcher.MatchResult{
		Work:    &annict.Work{ID: 1, Title: "作品"},
		Episode: &annict.Episode{ID: 1, Number: float64Ptr(1), Title: "第一話"},
	}
	r := Rename(src, result, false, "")
	if r.Error != nil || !r.Renamed {
		t.Fatalf("Rename() = %+v, want successful no-op", r)
	}
	if r.NewPath != src {
		t.Errorf("Rename() NewPath = %q, want unchanged %q", r.NewPath, src)
	}
	nested := filepath.Join(workDir, "作品", filepath.Base(src))
	if _, err := os.Stat(nested); !os.IsNotExist(err) {
		t.Errorf("recursive rerun created nested destination %s", nested)
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
	if r.Error != nil || !r.Renamed {
		t.Fatalf("Rename() = %+v, want successful duplicate move", r)
	}
	want := filepath.Join(dir, "作品", "duplicate", "作品 #7 「テスト」 (1).mp4")
	if r.NewPath != want {
		t.Errorf("Rename() NewPath = %q, want %q", r.NewPath, want)
	}
	if got, err := os.ReadFile(dst); err != nil || string(got) != "existing" {
		t.Errorf("canonical destination changed: content=%q error=%v", got, err)
	}
	if got, err := os.ReadFile(want); err != nil || string(got) != "test" {
		t.Errorf("duplicate recording missing: content=%q error=%v", got, err)
	}
}

func TestRenameExistingDestinationUsesNextDuplicateSequence(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "test.mp4")
	workDir := filepath.Join(dir, "作品")
	duplicateDir := filepath.Join(workDir, "duplicate")
	dst := filepath.Join(workDir, "作品 #7 「テスト」.mp4")
	for path, content := range map[string]string{
		src: "source",
		dst: "canonical",
		filepath.Join(duplicateDir, "作品 #7 「テスト」 (1).mp4"): "duplicate 1",
		filepath.Join(duplicateDir, "作品 #7 「テスト」 (2).mp4"): "duplicate 2",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	result := &matcher.MatchResult{
		Work:    &annict.Work{ID: 1, Title: "作品"},
		Episode: &annict.Episode{ID: 1, Number: float64Ptr(7), Title: "テスト"},
	}
	r := Rename(src, result, false, "")
	want := filepath.Join(duplicateDir, "作品 #7 「テスト」 (3).mp4")
	if r.Error != nil || !r.Renamed || r.NewPath != want {
		t.Fatalf("Rename() = %+v, want duplicate path %q", r, want)
	}
	if got, err := os.ReadFile(want); err != nil || string(got) != "source" {
		t.Errorf("third duplicate content=%q error=%v", got, err)
	}
}

func TestRenameDanglingSymlinkDestination(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks may require additional privileges on Windows")
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "test.mp4")
	dst := filepath.Join(dir, "作品", "作品 #7 「テスト」.mp4")
	if err := os.WriteFile(src, []byte("source"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "missing"), dst); err != nil {
		t.Fatal(err)
	}

	result := &matcher.MatchResult{
		Work:    &annict.Work{ID: 1, Title: "作品"},
		Episode: &annict.Episode{ID: 1, Number: float64Ptr(7), Title: "テスト"},
	}
	r := Rename(src, result, false, "")
	if r.Error != nil || !r.Renamed {
		t.Fatalf("Rename() = %+v, want safe duplicate move", r)
	}
	if _, err := os.Lstat(dst); err != nil {
		t.Errorf("destination symlink should remain untouched: %v", err)
	}
	want := filepath.Join(dir, "作品", "duplicate", "作品 #7 「テスト」 (1).mp4")
	if got, err := os.ReadFile(want); err != nil || string(got) != "source" {
		t.Errorf("duplicate recording content=%q error=%v", got, err)
	}
}

func TestRenameDuplicateRerunIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	workDir := filepath.Join(dir, "作品")
	duplicateDir := filepath.Join(workDir, "duplicate")
	canonical := filepath.Join(workDir, "作品 #1 「第一話」.mp4")
	src := filepath.Join(duplicateDir, "作品 #1 「第一話」 (1).mp4")
	if err := os.MkdirAll(duplicateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(canonical, []byte("canonical"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("duplicate"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := &matcher.MatchResult{
		Work:    &annict.Work{ID: 1, Title: "作品"},
		Episode: &annict.Episode{ID: 1, Number: float64Ptr(1), Title: "第一話"},
	}

	r := Rename(src, result, false, "")
	if r.Error != nil || !r.Renamed || r.NewPath != src {
		t.Fatalf("Rename() = %+v, want successful no-op at %q", r, src)
	}
	if got, err := os.ReadFile(src); err != nil || string(got) != "duplicate" {
		t.Errorf("duplicate changed: content=%q error=%v", got, err)
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

func TestRenameWithOutputDirPreservesWorkDirectoryForOrganizedInput(t *testing.T) {
	dir := t.TempDir()
	sourceWorkDir := filepath.Join(dir, "作品")
	if err := os.MkdirAll(sourceWorkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(sourceWorkDir, "作品 #1 「第一話」.mp4")
	if err := os.WriteFile(src, []byte("recording"), 0o644); err != nil {
		t.Fatal(err)
	}
	outputDir := filepath.Join(dir, "out")
	result := &matcher.MatchResult{
		Work:    &annict.Work{ID: 1, Title: "作品"},
		Episode: &annict.Episode{ID: 1, Number: float64Ptr(1), Title: "第一話"},
	}

	r := Rename(src, result, false, outputDir)
	want := filepath.Join(outputDir, "作品", filepath.Base(src))
	if r.Error != nil || !r.Renamed {
		t.Fatalf("Rename() = %+v, want successful move", r)
	}
	if r.NewPath != want {
		t.Errorf("Rename() NewPath = %q, want %q", r.NewPath, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("output file missing: %v", err)
	}
}

func TestRenameDryRunReportsExistingDestination(t *testing.T) {
	// Regression test: dry-run must preview the same numbered duplicate path
	// an actual run would choose without touching either source or destination.
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
	want := filepath.Join(dir, "作品", "duplicate", "作品 #7 「テスト」 (1).mp4")
	if r.Error != nil || !r.Previewed || r.Renamed || r.NewPath != want {
		t.Fatalf("Rename(dry-run) = %+v, want duplicate preview %q", r, want)
	}
	if _, err := os.Stat(src); os.IsNotExist(err) {
		t.Error("dry-run must not touch the original file")
	}
	if _, err := os.Stat(want); !os.IsNotExist(err) {
		t.Errorf("dry-run created duplicate destination: %v", err)
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

	// No leftover uniquely named temp file.
	leftovers, err := filepath.Glob(filepath.Join(dir, ".dst.mp4.anime-renamer-tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Errorf("copyFile left temp files behind: %v", leftovers)
	}
}

func TestCopyFileDoesNotReplaceDestination(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp4")
	dst := filepath.Join(dir, "dst.mp4")
	if err := os.WriteFile(src, []byte("source"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := copyFile(src, dst); err == nil {
		t.Fatal("copyFile() should reject an existing destination")
	}
	if got, err := os.ReadFile(dst); err != nil || string(got) != "existing" {
		t.Errorf("destination changed: content=%q error=%v", got, err)
	}
	if got, err := os.ReadFile(src); err != nil || string(got) != "source" {
		t.Errorf("source changed: content=%q error=%v", got, err)
	}
}

func TestCopyFileIgnoresStaleLegacyTempName(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp4")
	dst := filepath.Join(dir, "dst.mp4")
	legacyTemp := dst + ".anime-renamer-tmp"
	if err := os.WriteFile(src, []byte("source"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyTemp, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copyFile() error = %v", err)
	}
	if got, err := os.ReadFile(dst); err != nil || string(got) != "source" {
		t.Errorf("destination content=%q error=%v", got, err)
	}
	if got, err := os.ReadFile(legacyTemp); err != nil || string(got) != "stale" {
		t.Errorf("legacy temp file changed: content=%q error=%v", got, err)
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
	leftovers, err := filepath.Glob(filepath.Join(dir, "no-such-subdir", ".dst.mp4.anime-renamer-tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Errorf("copyFile left temp files behind on failure: %v", leftovers)
	}
}

func TestMoveFileDoesNotReplaceDestination(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp4")
	dst := filepath.Join(dir, "dst.mp4")
	if err := os.WriteFile(src, []byte("source"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := moveFile(src, dst); err == nil {
		t.Fatal("moveFile() should reject an existing destination")
	}
	if got, err := os.ReadFile(dst); err != nil || string(got) != "existing" {
		t.Errorf("destination changed: content=%q error=%v", got, err)
	}
	if got, err := os.ReadFile(src); err != nil || string(got) != "source" {
		t.Errorf("source changed: content=%q error=%v", got, err)
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
