package artwork

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	_ "golang.org/x/image/webp"
)

const maxImageBytes = 10 << 20

// Public artwork URLs must not turn an API response or redirect into access
// to local services. Resolve once and dial only validated public addresses.
func imageClient() *http.Client {
	transport := &http.Transport{
		TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 10 * time.Second,
		MaxIdleConns: 8, MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
			if len(addresses) == 0 {
				return nil, fmt.Errorf("image host has no addresses")
			}
			for _, ip := range addresses {
				if !publicAddress(ip) {
					return nil, fmt.Errorf("image host resolves to a non-public address")
				}
			}
			var failures []string
			for _, ip := range addresses {
				connection, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if err == nil {
					return connection, nil
				}
				failures = append(failures, err.Error())
			}
			return nil, fmt.Errorf("connect to image host: %s", strings.Join(failures, "; "))
		},
	}
	return &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(req *http.Request, previous []*http.Request) error {
		if len(previous) >= 5 {
			return fmt.Errorf("too many image redirects")
		}
		return validImageURL(req.URL)
	}}
}

func publicAddress(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() &&
		!netip.MustParsePrefix("100.64.0.0/10").Contains(ip) && !netip.MustParsePrefix("0.0.0.0/8").Contains(ip)
}

func validImageURL(u *url.URL) error {
	if u == nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || len(u.String()) > 8192 {
		return fmt.Errorf("image URL must be an absolute http(s) URL without credentials")
	}
	return nil
}

func fetchImage(ctx context.Context, client *http.Client, address string) ([]byte, string, error) {
	u, err := url.Parse(address)
	if err != nil {
		return nil, "", err
	}
	if err := validImageURL(u); err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "anime-renamer (+https://github.com/yude/anime-renamer)")
	// Do not forward the Annict token or any authentication to image hosts.
	response, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("image request returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > maxImageBytes {
		return nil, "", fmt.Errorf("image exceeds 10 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxImageBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > maxImageBytes {
		return nil, "", fmt.Errorf("image exceeds 10 MiB")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("invalid image: %w", err)
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > 8192 || config.Height > 8192 || int64(config.Width)*int64(config.Height) > 16_000_000 {
		return nil, "", fmt.Errorf("image dimensions exceed limit")
	}
	extensions := map[string]string{"jpeg": ".jpg", "png": ".png", "webp": ".webp"}
	ext, ok := extensions[format]
	if !ok {
		return nil, "", fmt.Errorf("unsupported image format %q", format)
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return nil, "", fmt.Errorf("incomplete image: %w", err)
	}
	return data, ext, nil
}
