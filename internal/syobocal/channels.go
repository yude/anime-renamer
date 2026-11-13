package syobocal

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

type Channel struct {
	ID   int    `xml:"ChID"`
	Name string `xml:"ChName"`
}

// GetChannels uses the live channel catalogue instead of assuming that an
// EPGStation service ID and a Syobocal channel ID have the same meaning.
func (c *Client) GetChannels() ([]Channel, error) {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("Command", "ChLookup")
	u.RawQuery = q.Encode()
	c.waitForRateLimit()
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request ChLookup: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ChLookup returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxResponseBodyBytes {
		return nil, fmt.Errorf("ChLookup response exceeds size limit")
	}
	var response struct {
		Channels []Channel `xml:"ChItems>ChItem"`
		Result   struct {
			Code int `xml:"Code"`
		} `xml:"Result"`
	}
	if err := xml.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("decode ChLookup: %w", err)
	}
	if response.Result.Code != http.StatusOK {
		return nil, fmt.Errorf("ChLookup result %d", response.Result.Code)
	}
	return response.Channels, nil
}
