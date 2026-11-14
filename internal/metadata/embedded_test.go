package metadata

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func requireFFmpeg(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed", tool)
		}
	}
}

func makeVideo(t *testing.T, ext string) string {
	t.Helper()
	requireFFmpeg(t)
	dir := t.TempDir()
	// Exercise non-ASCII paths, audio, subtitles, chapters and unrelated tags.
	sub := filepath.Join(dir, "captions.srt")
	if err := os.WriteFile(sub, []byte("1\n00:00:00,000 --> 00:00:00,800\n字幕\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	chapters := filepath.Join(dir, "chapters.txt")
	if err := os.WriteFile(chapters, []byte(";FFMETADATA1\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=0\nEND=1000\ntitle=冒頭\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "録画 #13"+ext)
	subCodec := "mov_text"
	if ext == ".mkv" {
		subCodec = "srt"
	}
	args := []string{"-nostdin", "-v", "error", "-f", "lavfi", "-i", "color=c=black:s=64x64:r=2", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-i", sub, "-f", "ffmetadata", "-i", chapters, "-map", "0:v", "-map", "1:a"}
	if ext == ".mkv" {
		args = append(args, "-map", "2:s")
	}
	args = append(args, "-map_chapters", "3", "-t", "1", "-c:v", "mpeg4", "-c:a", "aac", "-c:s", subCodec, "-metadata", "artist=保持する値", "-metadata", "comment=既存のコメント")
	if ext != ".mkv" {
		args = append(args, "-f", "mp4")
	}
	args = append(args, path)
	_, err := run("ffmpeg", args...)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestEmbeddedRoundTripPreservesStreamsBackupAndReruns(t *testing.T) {
	for _, ext := range []string{".mp4", ".mkv", ".m4v"} {
		t.Run(ext, func(t *testing.T) {
			path := makeVideo(t, ext)
			original, _ := os.ReadFile(path)
			when := time.Unix(1700000000, 0)
			if err := os.Chtimes(path, when, when); err != nil {
				t.Fatal(err)
			}
			info, _ := FromMatch(verifiedMatch(), 2)
			preview, err := Embed(path, info, true)
			if err != nil || preview.Status != Previewed {
				t.Fatalf("preview: %+v, %v", preview, err)
			}
			if _, err := os.Stat(BackupPath(path)); !os.IsNotExist(err) {
				t.Fatal("dry-run made a backup")
			}
			if got, _ := os.ReadFile(path); !bytes.Equal(got, original) {
				t.Fatal("dry-run changed video")
			}
			result, err := Embed(path, info, false)
			if err != nil || result.Status != Created {
				t.Fatalf("embed: %+v, %v", result, err)
			}
			if backup, _ := os.ReadFile(result.Backup); !bytes.Equal(backup, original) {
				t.Fatal("backup differs from original video")
			}
			probe, err := readProbe(path)
			if err != nil || !tagsMatch(probe, info.tags()) || probe.Format.Tags["artist"] != "保持する値" || !strings.HasPrefix(probe.Format.Tags["comment"], "既存のコメント\n") {
				t.Fatalf("tags: %+v, %v", probe, err)
			}
			stat, _ := os.Stat(path)
			if !stat.ModTime().Equal(when) {
				t.Fatal("recording timestamp changed")
			}
			written, _ := os.ReadFile(path)
			result, err = Embed(path, info, false)
			if err != nil || result.Status != Unchanged {
				t.Fatalf("rerun: %+v, %v", result, err)
			}
			info.Title = "変更された字幕"
			if _, err := Embed(path, info, false); err == nil {
				t.Fatal("must not overwrite an existing backup on a later update")
			}
			if got, _ := os.ReadFile(path); !bytes.Equal(got, written) {
				t.Fatal("failed update changed source")
			}
		})
	}
}

func TestEmbeddedFailureLeavesOriginalAndNoBackup(t *testing.T) {
	requireFFmpeg(t)
	path := filepath.Join(t.TempDir(), "broken.mp4")
	original := []byte("not a video")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	info, _ := FromMatch(verifiedMatch(), 1)
	if _, err := Embed(path, info, false); err == nil {
		t.Fatal("must fail for corrupt video")
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, original) {
		t.Fatal("failed probing changed source")
	}
	if _, err := os.Stat(BackupPath(path)); !os.IsNotExist(err) {
		t.Fatal("failed probing created backup")
	}
}
