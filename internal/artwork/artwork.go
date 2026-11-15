// Package artwork creates per-work Jellyfin images without changing recordings.
package artwork

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yude/anime-renamer/internal/annict"
	"github.com/yude/anime-renamer/internal/fileops"
)

const DefaultTimeout = 45 * time.Second

type Status string

const (
	Created   Status = "created"
	Previewed Status = "would write"
	Preserved Status = "preserved existing image"
	Skipped   Status = "skipped"
	Failed    Status = "failed"
)

type Result struct {
	Path, Kind, Source, Reason string
	Status                     Status
}

type WorkClient interface {
	GetArtwork(context.Context, int) (*annict.ArtworkWork, error)
}

// Runner is used sequentially. Metadata is cached per work for one batch; image
// files themselves are the persistent cache and are never overwritten.
type Runner struct {
	Client     WorkClient
	HTTPClient *http.Client
	Timeout    time.Duration
	DryRun     bool
	works      map[int]*annict.ArtworkWork
	nextLookup time.Time
}

func New(client WorkClient, dryRun bool, timeout time.Duration) *Runner {
	return &Runner{Client: client, HTTPClient: imageClient(), Timeout: timeout, DryRun: dryRun, works: make(map[int]*annict.ArtworkWork)}
}

func (r *Runner) Process(parent context.Context, dir string, workID int) (results []Result, err error) {
	if workID <= 0 {
		return []Result{{Path: dir, Kind: "series", Status: Skipped, Reason: ErrNoWorkID.Error()}}, nil
	}
	timeout := r.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	if timeout < 0 {
		return nil, fmt.Errorf("artwork timeout must be positive")
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(dir); err != nil {
		if !r.DryRun || !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("artwork destination is not a real directory: %s", dir)
	}
	if !r.DryRun {
		release, err := fileops.Lock(filepath.Join(dir, ".anime-renamer-artwork.lock"))
		if err != nil {
			return nil, err
		}
		defer func() { err = errors.Join(err, release()) }()
	}
	hasIdentity, err := checkIdentity(dir, workID)
	if err != nil {
		return nil, err
	}
	startingID, startingErr := ResolveWorkID(dir)
	if startingErr != nil && !errors.Is(startingErr, ErrNoWorkID) {
		return nil, startingErr
	}
	poster, err := existingImage(dir, []string{"poster", "folder", "cover", "default", "show", filepath.Base(dir)})
	if err != nil {
		return nil, err
	}
	thumb, err := existingImage(dir, []string{"landscape", "thumb"})
	if err != nil {
		return nil, err
	}
	if poster != "" {
		results = append(results, Result{Kind: "poster", Path: poster, Status: Preserved})
	}
	if thumb != "" {
		results = append(results, Result{Kind: "thumb", Path: thumb, Status: Preserved})
	}
	if poster != "" && thumb != "" {
		return results, nil
	}
	work, err := r.work(ctx, workID)
	if errors.Is(err, annict.ErrWorkNotFound) {
		return append(results, Result{Kind: "series", Path: dir, Status: Skipped, Reason: err.Error()}), nil
	}
	if err != nil {
		return results, err
	}
	type pendingImage struct {
		result Result
		data   []byte
	}
	var pending []pendingImage
	var failures []error
	if poster == "" {
		result := Result{Kind: "poster", Path: filepath.Join(dir, "poster.png"), Status: Previewed}
		data, err := Poster(work)
		if err != nil {
			result.Status, result.Reason = Failed, err.Error()
			results, failures = append(results, result), append(failures, err)
		} else {
			pending = append(pending, pendingImage{result: result, data: data})
		}
	}
	imageURL := ""
	if thumb == "" {
		var candidates []string
		// Dedicated OGP paths may distinguish seasons while recommended_url
		// sometimes points every season at the same changing franchise page.
		for _, address := range []string{work.Images.Facebook.OGImageURL, work.Images.RecommendedURL} {
			address = strings.TrimSpace(address)
			if address != "" && (len(candidates) == 0 || candidates[0] != address) {
				candidates = append(candidates, address)
			}
		}
		result := Result{Kind: "thumb", Path: dir, Status: Skipped, Reason: "no public image URL registered"}
		var downloadErrors []error
		for _, address := range candidates {
			data, ext, err := fetchImage(ctx, r.HTTPClient, address)
			if err != nil {
				downloadErrors = append(downloadErrors, fmt.Errorf("%s: %w", address, err))
				continue
			}
			imageURL = address
			result = Result{Kind: "thumb", Path: filepath.Join(dir, "thumb"+ext), Source: address, Status: Previewed}
			pending = append(pending, pendingImage{result: result, data: data})
			break
		}
		if imageURL == "" {
			if len(downloadErrors) > 0 {
				failure := errors.Join(downloadErrors...)
				result.Status, result.Reason = Failed, failure.Error()
				failures = append(failures, failure)
			}
			results = append(results, result)
		}
	}
	if err := ctx.Err(); err != nil {
		return results, err
	}
	if !r.DryRun && len(pending) > 0 {
		// Recheck the NFO after network I/O before publishing anything.
		if _, err := checkIdentity(dir, workID); err != nil {
			return results, err
		}
		if startingID > 0 {
			current, err := ResolveWorkID(dir)
			if err != nil || current != startingID {
				return results, fmt.Errorf("work identity changed during artwork download")
			}
		}
		if !hasIdentity {
			data, err := json.MarshalIndent(identity{Version: 1, WorkID: work.ID, Title: work.Title, ImageURL: imageURL}, "", "  ")
			if err != nil {
				return results, err
			}
			if err := fileops.WriteNew(filepath.Join(dir, IdentityFile), append(data, '\n'), 0o644); err != nil {
				return results, err
			}
		}
	}
	for _, image := range pending {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		if !r.DryRun {
			stems := []string{"landscape", "thumb"}
			if image.result.Kind == "poster" {
				stems = []string{"poster", "folder", "cover", "default", "show", filepath.Base(dir)}
			}
			existing, err := existingImage(dir, stems)
			if err == nil && existing != "" {
				image.result.Path, image.result.Status = existing, Preserved
			} else if err == nil {
				err = fileops.WriteNew(image.result.Path, image.data, 0o644)
				if err == nil {
					image.result.Status = Created
				}
			}
			if err != nil {
				image.result.Status, image.result.Reason = Failed, err.Error()
				failures = append(failures, err)
			}
		}
		results = append(results, image.result)
	}
	return results, errors.Join(failures...)
}

func (r *Runner) work(ctx context.Context, id int) (*annict.ArtworkWork, error) {
	if cached := r.works[id]; cached != nil {
		return cached, nil
	}
	if wait := time.Until(r.nextLookup); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	r.nextLookup = time.Now().Add(500 * time.Millisecond)
	work, err := r.Client.GetArtwork(ctx, id)
	if err != nil {
		return nil, err
	}
	if work == nil || work.ID != id || strings.TrimSpace(work.Title) == "" || !utf8.ValidString(work.Title) || utf8.RuneCountInString(work.Title) > 1024 {
		return nil, fmt.Errorf("artwork lookup returned a different Annict work")
	}
	r.works[id] = work
	return work, nil
}

func existingImage(dir string, stems []string) (string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	extensions := map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".gif": true, ".bmp": true, ".tbn": true, ".avif": true, ".tif": true, ".tiff": true}
	for _, entry := range entries {
		ext := filepath.Ext(entry.Name())
		if !extensions[strings.ToLower(ext)] {
			continue
		}
		stem := strings.TrimSuffix(entry.Name(), ext)
		for _, expected := range stems {
			if !strings.EqualFold(stem, expected) && !strings.EqualFold(stem, filepath.Base(dir)+"-"+expected) {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				return "", err
			}
			if !info.Mode().IsRegular() {
				return "", fmt.Errorf("existing artwork is not a regular file: %s", entry.Name())
			}
			return filepath.Join(dir, entry.Name()), nil
		}
	}
	return "", nil
}
