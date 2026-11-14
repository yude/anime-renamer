package metadata

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestReplacementFailureRecoveryAndCleanup(t *testing.T) {
	for _, scenario := range []string{"success", "keep", "backup fails", "backup sync fails", "source changes", "replace fails", "commit sync fails", "cleanup fails", "other backup"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			source, temp := filepath.Join(dir, "video.mp4"), filepath.Join(dir, "prepared.tmp")
			if err := os.WriteFile(source, []byte("original"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(temp, []byte("verified new file"), 0o644); err != nil {
				t.Fatal(err)
			}
			before, _ := os.Stat(source)
			backup := BackupPath(source)
			fault := errors.New("injected I/O failure")
			ops := replacementOps{backup: makeBackup, replace: os.Rename, syncDir: func(string) error { return nil }, remove: os.Remove}
			syncCount := 0
			ops.syncDir = func(string) error {
				syncCount++
				if scenario == "backup sync fails" && syncCount == 1 || scenario == "commit sync fails" && syncCount == 3 {
					return fault
				}
				return nil
			}
			switch scenario {
			case "backup fails":
				ops.backup = func(_, target string) error { _ = os.MkdirAll(filepath.Dir(target), 0o700); return fault }
			case "source changes":
				ops.backup = func(src, dst string) error {
					if err := makeBackup(src, dst); err != nil {
						return err
					}
					return os.WriteFile(src, []byte("changed by recorder"), 0o644)
				}
			case "replace fails":
				ops.replace = func(string, string) error { return fault }
			case "cleanup fails":
				ops.remove = func(string) error { return fault }
			case "other backup":
				if err := os.MkdirAll(filepath.Dir(backup), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(filepath.Dir(backup), "other.original"), []byte("other job"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			replaced, err := commitReplacement(source, temp, backup, before, scenario == "keep", ops)
			wantReplaced := scenario == "success" || scenario == "keep" || scenario == "commit sync fails" || scenario == "cleanup fails" || scenario == "other backup"
			if replaced != wantReplaced {
				t.Fatalf("replaced=%v, error=%v", replaced, err)
			}
			wantError := scenario != "success" && scenario != "keep" && scenario != "other backup"
			if (err != nil) != wantError {
				t.Fatalf("unexpected error: %v", err)
			}
			data, _ := os.ReadFile(source)
			want := "original"
			if wantReplaced {
				want = "verified new file"
			} else if scenario == "source changes" {
				want = "changed by recorder"
			}
			if string(data) != want {
				t.Fatalf("source=%q, want %q", data, want)
			}
			_, statErr := os.Stat(backup)
			wantBackup := scenario != "success" && scenario != "backup fails" && scenario != "other backup"
			if (statErr == nil) != wantBackup {
				t.Fatalf("backup existence: %v", statErr)
			}
			if scenario == "success" || scenario == "backup fails" {
				if _, err := os.Stat(filepath.Dir(backup)); !os.IsNotExist(err) {
					t.Fatal("empty backup directory remained")
				}
			}
			if scenario == "other backup" {
				if data, _ := os.ReadFile(filepath.Join(filepath.Dir(backup), "other.original")); string(data) != "other job" {
					t.Fatal("removed another job's backup")
				}
			}
		})
	}
}

func TestDefaultEmbedCleansAndCanUpdateAgain(t *testing.T) {
	path := makeVideo(t, ".mp4")
	info, _ := FromMatch(verifiedMatch(), 1)
	for _, title := range []string{info.Title, "更新後のサブタイトル"} {
		info.Title = title
		result, err := Embed(path, info, false)
		if err != nil || result.Status != Created || result.Backup != "" {
			t.Fatalf("embed: %+v, %v", result, err)
		}
		if _, err := os.Stat(filepath.Dir(BackupPath(path))); !os.IsNotExist(err) {
			t.Fatal("successful operation left backups")
		}
		if files, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".anime-renamer-*.tmp")); len(files) > 0 {
			t.Fatal("successful operation left temp files")
		}
	}
}

func TestMovieProvenanceRequiresExactWorkID(t *testing.T) {
	path := makeVideo(t, ".mp4")
	for _, workID := range []int{100, 10} {
		if _, err := Embed(path, Info{Title: "映画", WorkID: workID, Movie: true}, false); err != nil {
			t.Fatal(err)
		}
	}
	current, err := readProbe(path)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(strings.Split(current.Format.Tags["comment"], "\n"), "Annict: https://annict.com/works/10") {
		t.Fatal("a different work ID prefix suppressed the new provenance")
	}
}

func TestCompletedLegacyBackupIsVerifiedBeforeRemoval(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(map[bool]string{false: "valid", true: "unverified"}[corrupt], func(t *testing.T) {
			path := makeVideo(t, ".mp4")
			info, _ := FromMatch(verifiedMatch(), 1)
			result, err := EmbedWithOptions(path, info, false, EmbedOptions{KeepBackup: true})
			if err != nil {
				t.Fatal(err)
			}
			if corrupt {
				if err := os.WriteFile(result.Backup, []byte("unrelated data"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(path)
			_, err = Embed(path, info, false)
			if (err != nil) != corrupt {
				t.Fatalf("cleanup error: %v", err)
			}
			if after, _ := os.ReadFile(path); !bytes.Equal(after, before) {
				t.Fatal("cleanup changed video")
			}
			_, statErr := os.Stat(result.Backup)
			if corrupt && statErr != nil || !corrupt && !os.IsNotExist(statErr) {
				t.Fatalf("backup cleanup status: %v", statErr)
			}
		})
	}
}
