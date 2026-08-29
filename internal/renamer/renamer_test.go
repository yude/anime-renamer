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
			match:       nil,
			wantErr:     true,
		},
		{
			name:         "nil episode",
			originalPath: "/recordings/test.mp4",
			match: &matcher.MatchResult{
				Work: &annict.Work{ID: 1, Title: "作品"},
			},
			wantErr: true,
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

	r := Rename(src, result, true)
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

	r := Rename(src, result, false)
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

	r := Rename(src, result, false)
	if r.Error == nil {
		t.Error("Rename() should error when destination exists")
	}
}
