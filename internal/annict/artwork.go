package annict

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

var ErrWorkNotFound = errors.New("work ID was not found in Annict")

type ArtworkWork struct {
	ID         int    `json:"id"`
	Title      string `json:"title"`
	SeasonName string `json:"season_name"`
	Images     struct {
		RecommendedURL string `json:"recommended_url"`
		Facebook       struct {
			OGImageURL string `json:"og_image_url"`
		} `json:"facebook"`
	} `json:"images"`
}

// GetArtwork resolves only an already-established work ID. It never searches
// titles or guesses a replacement identity when an ID is missing or obsolete.
func (c *Client) GetArtwork(ctx context.Context, workID int) (*ArtworkWork, error) {
	if workID <= 0 {
		return nil, ErrWorkNotFound
	}
	params := url.Values{"filter_ids": {strconv.Itoa(workID)}, "fields": {"id,title,season_name,images"}, "per_page": {"50"}}
	var response struct {
		Works []ArtworkWork `json:"works"`
	}
	client := *c
	client.ctx = ctx
	if err := client.get("/works", params, &response); err != nil {
		return nil, err
	}
	if len(response.Works) == 0 {
		return nil, ErrWorkNotFound
	}
	if len(response.Works) != 1 || response.Works[0].ID != workID || strings.TrimSpace(response.Works[0].Title) == "" {
		return nil, fmt.Errorf("artwork response has an ambiguous or conflicting Annict identity for %d", workID)
	}
	return &response.Works[0], nil
}
