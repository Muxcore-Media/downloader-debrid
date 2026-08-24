// Package debrid provides HTTP clients for Real-Debrid and AllDebrid.
package debrid

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Provider selects the backend API.
type Provider string

const (
	ProviderRealDebrid Provider = "realdebrid"
	ProviderAllDebrid  Provider = "alldebrid"

	defaultRDBase = "https://api.real-debrid.com"
	defaultADBase = "https://api.alldebrid.com"
)

// Client is a minimal debrid API facade.
type Client struct {
	HTTPClient *http.Client
	Provider   Provider
	Token      string
	BaseURL    string // optional override for httptest mocks; defaults per provider
}

func (c *Client) http() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 45 * time.Second}
}

func (c *Client) base() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	if c.Provider == ProviderAllDebrid {
		return defaultADBase
	}
	return defaultRDBase
}

// Unrestricted is a resolved direct download.
type Unrestricted struct {
	Filesize int64
	ID       string
	Filename string
	Download string
	Host     string
}

// Download is a cloud download / unrestricted entry.
type Download struct {
	Filesize int64
	ID       string
	Filename string
	Status   string
	Link     string
}

// Unrestrict resolves a hoster link to a direct URL.
func (c *Client) Unrestrict(ctx context.Context, link, password string) (Unrestricted, error) {
	switch c.Provider {
	case ProviderAllDebrid:
		return c.alldebridUnrestrict(ctx, link, password)
	default:
		return c.realdebridUnrestrict(ctx, link, password)
	}
}

// ListDownloads returns recent unrestricted / cloud downloads.
func (c *Client) ListDownloads(ctx context.Context, limit int) ([]Download, error) {
	switch c.Provider {
	case ProviderAllDebrid:
		return c.alldebridList(ctx, limit)
	default:
		return c.realdebridList(ctx, limit)
	}
}

// DeleteDownload removes a cloud download by id (Real-Debrid downloads API).
func (c *Client) DeleteDownload(ctx context.Context, id string) error {
	switch c.Provider {
	case ProviderAllDebrid:
		return c.alldebridDelete(ctx, id)
	default:
		return c.realdebridDelete(ctx, id)
	}
}

// AddMagnet queues a magnet or torrent URL on Real-Debrid (no-op on AllDebrid v0.1.0).
func (c *Client) AddMagnet(ctx context.Context, magnetOrURL string) (string, error) {
	switch c.Provider {
	case ProviderAllDebrid:
		return "", fmt.Errorf("alldebrid: magnet add not supported in v0.1.0")
	default:
		return c.realdebridAddMagnet(ctx, magnetOrURL)
	}
}

func (c *Client) realdebridAddMagnet(ctx context.Context, magnetOrURL string) (string, error) {
	form := url.Values{"magnet": {magnetOrURL}}
	body, err := c.rdDo(ctx, http.MethodPost, c.base()+"/rest/1.0/torrents/addMagnet", form)
	if err != nil {
		return "", err
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", fmt.Errorf("real-debrid: empty torrent id")
	}
	return out.ID, nil
}

func (c *Client) realdebridUnrestrict(ctx context.Context, link, password string) (Unrestricted, error) {
	form := url.Values{"link": {link}}
	if password != "" {
		form.Set("password", password)
	}
	body, err := c.rdDo(ctx, http.MethodPost, c.base()+"/rest/1.0/unrestrict/link", form)
	if err != nil {
		return Unrestricted{}, err
	}
	var out struct {
		Filesize int64  `json:"filesize"`
		ID       string `json:"id"`
		Filename string `json:"filename"`
		Download string `json:"download"`
		Host     string `json:"host"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return Unrestricted{}, err
	}
	return Unrestricted{
		ID: out.ID, Filename: out.Filename, Download: out.Download,
		Filesize: out.Filesize, Host: out.Host,
	}, nil
}

func (c *Client) realdebridList(ctx context.Context, limit int) ([]Download, error) {
	u := c.base() + "/rest/1.0/downloads"
	if limit > 0 {
		u += fmt.Sprintf("?limit=%d", limit)
	}
	body, err := c.rdDo(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		ID       string `json:"id"`
		Filename string `json:"filename"`
		Link     string `json:"link"`
		Filesize int64  `json:"filesize"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, err
	}
	out := make([]Download, 0, len(rows))
	for _, r := range rows {
		out = append(out, Download{
			ID: r.ID, Filename: r.Filename, Status: "downloaded",
			Link: r.Link, Filesize: r.Filesize,
		})
	}
	return out, nil
}

func (c *Client) realdebridDelete(ctx context.Context, id string) error {
	_, err := c.rdDo(ctx, http.MethodDelete, c.base()+"/rest/1.0/downloads/delete/"+url.PathEscape(id), nil)
	return err
}

func (c *Client) rdDo(ctx context.Context, method, endpoint string, form url.Values) ([]byte, error) {
	if c.Token == "" {
		return nil, fmt.Errorf("debrid token required")
	}
	var rdr io.Reader
	if form != nil {
		rdr = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("real-debrid HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func (c *Client) alldebridUnrestrict(ctx context.Context, link, password string) (Unrestricted, error) {
	q := url.Values{"agent": {"muxcore"}, "apikey": {c.Token}, "link": {link}}
	if password != "" {
		q.Set("password", password)
	}
	body, err := c.adDo(ctx, c.base()+"/v4/link/unlock?"+q.Encode())
	if err != nil {
		return Unrestricted{}, err
	}
	var out struct {
		Status string `json:"status"`
		Error  struct {
			Message string `json:"message"`
		} `json:"error"`
		Data struct {
			Filesize int64  `json:"filesize"`
			ID       string `json:"id"`
			Link     string `json:"link"`
			Filename string `json:"filename"`
			Host     string `json:"host"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return Unrestricted{}, err
	}
	if out.Status != "success" {
		return Unrestricted{}, fmt.Errorf("alldebrid: %s", out.Error.Message)
	}
	return Unrestricted{
		ID: out.Data.ID, Filename: out.Data.Filename, Download: out.Data.Link,
		Filesize: out.Data.Filesize, Host: out.Data.Host,
	}, nil
}

func (c *Client) alldebridList(_ context.Context, _ int) ([]Download, error) {
	// AllDebrid has no stable downloads list equivalent in the free unlock API;
	// return empty for v0.1.0.
	return nil, nil
}

func (c *Client) alldebridDelete(_ context.Context, _ string) error {
	return fmt.Errorf("alldebrid: delete not supported in v0.1.0")
}

func (c *Client) adDo(ctx context.Context, endpoint string) ([]byte, error) {
	if c.Token == "" {
		return nil, fmt.Errorf("debrid token required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return nil, err
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("alldebrid HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}
