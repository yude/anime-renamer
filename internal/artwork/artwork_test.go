package artwork

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yude/anime-renamer/internal/annict"
)

type workFixture struct {
	work  *annict.ArtworkWork
	err   error
	calls int
	wait  bool
}

func (f *workFixture) GetArtwork(ctx context.Context, _ int) (*annict.ArtworkWork, error) {
	f.calls++
	if f.wait {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return f.work, f.err
}

func sampleWork() *annict.ArtworkWork {
	return &annict.ArtworkWork{ID: 42, Title: "綺麗にしてもらえますか。", SeasonName: "2026-winter"}
}

func samplePNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 120, 63))
	img.Set(0, 0, color.White)
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func writeShow(t *testing.T, dir string, id int) {
	t.Helper()
	data := fmt.Sprintf(`<tvshow><title>作品</title><uniqueid type="annict">%d</uniqueid></tvshow>`, id)
	if err := os.WriteFile(filepath.Join(dir, "tvshow.nfo"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWorkIdentityDoesNotGuessFromEpisodeOrFolder(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "episode.nfo"), []byte(`<episodedetails><uniqueid type="annict">42</uniqueid></episodedetails>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveWorkID(dir); !errors.Is(err, ErrNoWorkID) {
		t.Fatalf("episode ID became a work ID: %v", err)
	}
	for _, data := range []string{
		`<tvshow><uniqueid type="annict">42</uniqueid><uniqueid type="annict">43</uniqueid></tvshow>`,
		`<tvshow><uniqueid type="annict">0</uniqueid></tvshow>`,
		`<tvshow><uniqueid type="annict">1.5</uniqueid></tvshow>`,
		`<episodedetails><uniqueid type="annict">42</uniqueid></episodedetails>`,
		`<invalid`,
	} {
		if err := os.WriteFile(filepath.Join(dir, "tvshow.nfo"), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := ResolveWorkID(dir); err == nil {
			t.Fatalf("accepted unsafe identity: %s", data)
		}
	}
	writeShow(t, dir, 42)
	if id, err := ResolveWorkID(dir); err != nil || id != 42 {
		t.Fatalf("verified work: %d, %v", id, err)
	}
}

func TestPosterIsDeterministicJapanesePNG(t *testing.T) {
	work := sampleWork()
	first, err := Poster(work)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Poster(work)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatal("poster changed on an identical rerun")
	}
	config, err := png.DecodeConfig(bytes.NewReader(first))
	if err != nil || config.Width != 1000 || config.Height != 1500 {
		t.Fatalf("poster dimensions: %+v, %v", config, err)
	}
	for _, title := range []string{"冰剣の魔術師が世界を統べる", "冰剣の魔術師が世界を統べるⅡ"} {
		if _, err := Poster(&annict.ArtworkWork{ID: 42, Title: title}); err != nil {
			t.Fatalf("unsupported real library title %q: %v", title, err)
		}
	}
	work.Title = strings.Repeat("長い作品名", 256)
	if _, err := Poster(work); err == nil {
		t.Fatal("silently truncated an oversized title")
	}
	if seasonLabel("2026-winter") != "2026年 冬" || seasonLabel("invalid") != "" {
		t.Fatal("guessed an invalid release season")
	}
	face, err := posterFace(82)
	if err != nil {
		t.Fatal(err)
	}
	defer face.Close()
	lines, err := wrapTitle(face, "幻日のヨハネ -SUNSHINE in the MIRROR-", 820)
	if err != nil || !strings.Contains(strings.Join(lines, "\n"), "SUNSHINE") {
		t.Fatalf("split an English word across poster lines: %q, %v", lines, err)
	}
}

func TestArtworkDryRunRawImageAndRepeat(t *testing.T) {
	for _, dryRun := range []bool{true, false} {
		t.Run(fmt.Sprint(dryRun), func(t *testing.T) {
			original := samplePNG(t)
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Header.Get("Authorization") != "" {
					t.Error("forwarded Annict credentials to an image host")
				}
				_, _ = w.Write(original)
			}))
			defer server.Close()
			work := sampleWork()
			work.Images.Facebook.OGImageURL = server.URL + "/image.jpg" // Actual bytes are PNG.
			fixture := &workFixture{work: work}
			runner := New(fixture, dryRun, time.Second*5)
			runner.HTTPClient = server.Client()
			dir := t.TempDir()
			writeShow(t, dir, 42)
			results, err := runner.Process(context.Background(), dir, 42)
			if err != nil || len(results) != 2 {
				t.Fatalf("artwork: %+v, %v", results, err)
			}
			if dryRun {
				entries, _ := os.ReadDir(dir)
				if len(entries) != 1 || results[0].Status != Previewed || results[1].Status != Previewed {
					t.Fatal("dry-run changed files or failed to preview images")
				}
				return
			}
			if data, err := os.ReadFile(filepath.Join(dir, "thumb.png")); err != nil || !bytes.Equal(data, original) {
				t.Fatalf("official image was modified: %v", err)
			}
			results, err = runner.Process(context.Background(), dir, 42)
			if err != nil || len(results) != 2 || results[0].Status != Preserved || results[1].Status != Preserved || fixture.calls != 1 || requests.Load() != 1 {
				t.Fatalf("rerun overwrote or refetched existing artwork: %+v, %v", results, err)
			}
			entries, _ := os.ReadDir(dir)
			if len(entries) != 4 { // NFO, poster, thumbnail, identity; no temp or lock.
				t.Fatalf("unexpected leftover files: %v", entries)
			}
			if err := os.Remove(filepath.Join(dir, "tvshow.nfo")); err != nil {
				t.Fatal(err)
			}
			if id, err := ResolveWorkID(dir); err != nil || id != 42 {
				t.Fatalf("cannot reuse previously verified artwork identity: %d, %v", id, err)
			}
			if _, err := runner.Process(context.Background(), dir, 43); err == nil {
				t.Fatal("accepted artwork for a different Annict work")
			}
		})
	}
}

func TestArtworkPreservesAliasesAndSkipsUnknownIDs(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"folder.jpg", "landscape.webp"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("manual artwork"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fixture := &workFixture{work: sampleWork()}
	runner := New(fixture, false, 0)
	results, err := runner.Process(context.Background(), dir, 42)
	if err != nil || len(results) != 2 || fixture.calls != 0 {
		t.Fatalf("replaced manual artwork: %+v, %v", results, err)
	}
	results, err = runner.Process(context.Background(), dir, 0)
	if err != nil || len(results) != 1 || results[0].Status != Skipped || fixture.calls != 0 {
		t.Fatal("guessed an unknown work ID")
	}
}

func TestImageFailureAndConcurrentChanges(t *testing.T) {
	for _, scenario := range []string{"bad image", "alias added", "identity changed", "identity removed", "fallback"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			writeShow(t, dir, 42)
			original := samplePNG(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch scenario {
				case "bad image":
					fmt.Fprint(w, "not an image")
					return
				case "alias added":
					_ = os.WriteFile(filepath.Join(dir, "folder.jpg"), []byte("manual"), 0o644)
				case "identity changed":
					_ = os.WriteFile(filepath.Join(dir, "tvshow.nfo"), []byte(`<tvshow><uniqueid type="annict">43</uniqueid></tvshow>`), 0o644)
				case "identity removed":
					_ = os.Remove(filepath.Join(dir, "tvshow.nfo"))
				case "fallback":
					if r.URL.Path == "/first" {
						http.Error(w, "gone", http.StatusNotFound)
						return
					}
				}
				_, _ = w.Write(original)
			}))
			defer server.Close()
			work := sampleWork()
			work.Images.Facebook.OGImageURL = server.URL + "/first"
			if scenario == "fallback" {
				work.Images.RecommendedURL = server.URL + "/second"
			}
			runner := New(&workFixture{work: work}, false, time.Second*5)
			runner.HTTPClient = server.Client()
			_, err := runner.Process(context.Background(), dir, 42)
			wantError := scenario == "bad image" || strings.HasPrefix(scenario, "identity")
			if (err != nil) != wantError {
				t.Fatalf("unexpected error: %v", err)
			}
			if strings.HasPrefix(scenario, "identity") {
				for _, name := range []string{"poster.png", "thumb.png", IdentityFile} {
					if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
						t.Fatal("published artwork after identity disappeared or changed")
					}
				}
			} else if scenario == "bad image" {
				if _, err := os.Stat(filepath.Join(dir, "poster.png")); err != nil {
					t.Fatal("download failure prevented an independent title poster")
				}
				if _, err := os.Stat(filepath.Join(dir, "thumb.png")); !os.IsNotExist(err) {
					t.Fatal("published a broken image")
				}
			} else if scenario == "alias added" {
				if data, _ := os.ReadFile(filepath.Join(dir, "folder.jpg")); string(data) != "manual" {
					t.Fatal("changed a concurrently supplied poster")
				}
				if _, err := os.Stat(filepath.Join(dir, "poster.png")); !os.IsNotExist(err) {
					t.Fatal("overrode a concurrently supplied poster with another filename")
				}
			}
			if entries, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(entries) != 0 {
				t.Fatal("temporary artwork files remained")
			}
		})
	}
}

func TestArtworkTimeoutAndMissingWorkLeaveNoOutput(t *testing.T) {
	for _, wait := range []bool{true, false} {
		fixture := &workFixture{wait: wait, err: annict.ErrWorkNotFound}
		runner := New(fixture, false, 20*time.Millisecond)
		dir := t.TempDir()
		results, err := runner.Process(context.Background(), dir, 42)
		if wait && !errors.Is(err, context.DeadlineExceeded) || !wait && (err != nil || results[0].Status != Skipped) {
			t.Fatalf("timeout/missing identity: %+v, %v", results, err)
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Fatal("failed lookup left output or lock files")
		}
	}
}

func TestDownloaderRejectsPrivateTargetsAndTruncatedImages(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "10.0.0.1", "100.64.0.1", "169.254.169.254", "::1", "fc00::1", "::ffff:192.168.1.2", "0.1.2.3"} {
		if publicAddress(netip.MustParseAddr(address)) {
			t.Fatalf("accepted private image host %s", address)
		}
	}
	if !publicAddress(netip.MustParseAddr("8.8.8.8")) {
		t.Fatal("rejected public image address")
	}
	data := samplePNG(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(data[:len(data)/2])
	}))
	defer server.Close()
	if _, _, err := fetchImage(context.Background(), imageClient(), server.URL); err == nil {
		t.Fatal("default downloader accessed a local service")
	}
	if _, _, err := fetchImage(context.Background(), server.Client(), server.URL); err == nil {
		t.Fatal("accepted a truncated image")
	}
}
