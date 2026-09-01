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

	torrentPollInterval = 2 * time.Second
	torrentPollTimeout  = 30 * time.Minute
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

// Do performs an HTTP request using the client's configured or default HTTP client.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	return c.http().Do(req)
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

// Torrent is a cloud torrent job on Real-Debrid / AllDebrid.
type Torrent struct {
	Filesize int64
	ID       string
	Filename string
	Status   string
	Links    []string
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

// ListTorrents returns active or completed cloud torrents.
func (c *Client) ListTorrents(ctx context.Context, limit int) ([]Torrent, error) {
	switch c.Provider {
	case ProviderAllDebrid:
		return c.alldebridListTorrents(ctx, limit)
	default:
		return c.realdebridListTorrents(ctx, limit)
	}
}

// ResolveDownload finds a download or completed torrent by id and returns a streamable row.
func (c *Client) ResolveDownload(ctx context.Context, id string) (Download, error) {
	items, err := c.ListDownloads(ctx, 100)
	if err != nil {
		return Download{}, err
	}
	for _, it := range items {
		if it.ID == id {
			return it, nil
		}
	}
	torrents, err := c.ListTorrents(ctx, 100)
	if err != nil {
		return Download{}, err
	}
	for _, t := range torrents {
		if t.ID != id {
			continue
		}
		if len(t.Links) == 0 {
			return Download{}, fmt.Errorf("torrent %q has no links yet (status=%s)", id, t.Status)
		}
		u, err := c.Unrestrict(ctx, t.Links[0], "")
		if err != nil {
			return Download{}, err
		}
		return Download{
			ID: t.ID, Filename: u.Filename, Status: t.Status,
			Link: u.Download, Filesize: u.Filesize,
		}, nil
	}
	return Download{}, fmt.Errorf("download %q not found", id)
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

// AddMagnet queues a magnet or torrent URL and waits until the provider marks it downloaded.
func (c *Client) AddMagnet(ctx context.Context, magnetOrURL string) (string, error) {
	switch c.Provider {
	case ProviderAllDebrid:
		return c.alldebridAddMagnet(ctx, magnetOrURL)
	default:
		return c.realdebridAddMagnet(ctx, magnetOrURL)
	}
}

// CheckAuth verifies the configured token against the provider API.
func (c *Client) CheckAuth(ctx context.Context) error {
	switch c.Provider {
	case ProviderAllDebrid:
		return c.alldebridCheckAuth(ctx)
	default:
		_, err := c.realdebridList(ctx, 1)
		return err
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
	if err := c.realdebridSelectFiles(ctx, out.ID, "all"); err != nil {
		return "", err
	}
	if err := c.realdebridWaitTorrent(ctx, out.ID); err != nil {
		return "", err
	}
	return out.ID, nil
}

func (c *Client) realdebridSelectFiles(ctx context.Context, id, files string) error {
	form := url.Values{"files": {files}}
	_, err := c.rdDo(ctx, http.MethodPost, c.base()+"/rest/1.0/torrents/selectFiles/"+url.PathEscape(id), form)
	return err
}

func (c *Client) realdebridWaitTorrent(ctx context.Context, id string) error {
	deadline := time.Now().Add(torrentPollTimeout)
	for {
		info, err := c.realdebridTorrentInfo(ctx, id)
		if err != nil {
			return err
		}
		switch info.Status {
		case "downloaded":
			return nil
		case "error", "magnet_error", "dead", "virus":
			return fmt.Errorf("real-debrid torrent %s failed: %s", id, info.Status)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("real-debrid torrent %s timed out (status=%s)", id, info.Status)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(torrentPollInterval):
		}
	}
}

func (c *Client) realdebridTorrentInfo(ctx context.Context, id string) (Torrent, error) {
	body, err := c.rdDo(ctx, http.MethodGet, c.base()+"/rest/1.0/torrents/info/"+url.PathEscape(id), nil)
	if err != nil {
		return Torrent{}, err
	}
	var out struct {
		ID       string   `json:"id"`
		Filename string   `json:"filename"`
		Status   string   `json:"status"`
		Links    []string `json:"links"`
		Bytes    int64    `json:"bytes"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return Torrent{}, err
	}
	return Torrent{
		ID: out.ID, Filename: out.Filename, Status: out.Status,
		Links: out.Links, Filesize: out.Bytes,
	}, nil
}

func (c *Client) realdebridListTorrents(ctx context.Context, limit int) ([]Torrent, error) {
	u := c.base() + "/rest/1.0/torrents"
	if limit > 0 {
		u += fmt.Sprintf("?limit=%d", limit)
	}
	body, err := c.rdDo(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		ID       string   `json:"id"`
		Filename string   `json:"filename"`
		Status   string   `json:"status"`
		Links    []string `json:"links"`
		Bytes    int64    `json:"bytes"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, err
	}
	out := make([]Torrent, 0, len(rows))
	for _, r := range rows {
		out = append(out, Torrent{
			ID: r.ID, Filename: r.Filename, Status: r.Status,
			Links: r.Links, Filesize: r.Bytes,
		})
	}
	return out, nil
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
	if err == nil {
		return nil
	}
	_, err2 := c.rdDo(ctx, http.MethodDelete, c.base()+"/rest/1.0/torrents/delete/"+url.PathEscape(id), nil)
	return err2
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

func (c *Client) alldebridAddMagnet(ctx context.Context, magnetOrURL string) (string, error) {
	q := url.Values{
		"agent":     {"muxcore"},
		"apikey":    {c.Token},
		"magnets[]": {magnetOrURL},
	}
	body, err := c.adDo(ctx, c.base()+"/v4/magnet/upload?"+q.Encode())
	if err != nil {
		return "", err
	}
	var out struct {
		Status string `json:"status"`
		Error  struct {
			Message string `json:"message"`
		} `json:"error"`
		Data struct {
			Magnets []struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"magnets"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	if out.Status != "success" || len(out.Data.Magnets) == 0 {
		msg := out.Error.Message
		if msg == "" {
			msg = "magnet upload failed"
		}
		return "", fmt.Errorf("alldebrid: %s", msg)
	}
	id := fmt.Sprintf("%d", out.Data.Magnets[0].ID)
	if err := c.alldebridWaitMagnet(ctx, id); err != nil {
		return "", err
	}
	return id, nil
}

func (c *Client) alldebridWaitMagnet(ctx context.Context, id string) error {
	deadline := time.Now().Add(torrentPollTimeout)
	for {
		status, err := c.alldebridMagnetStatus(ctx, id)
		if err != nil {
			return err
		}
		switch status {
		case "Ready", "Downloaded":
			return nil
		case "Error":
			return fmt.Errorf("alldebrid magnet %s failed", id)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("alldebrid magnet %s timed out (status=%s)", id, status)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(torrentPollInterval):
		}
	}
}

func (c *Client) alldebridMagnetStatus(ctx context.Context, id string) (string, error) {
	q := url.Values{"agent": {"muxcore"}, "apikey": {c.Token}, "id": {id}}
	body, err := c.adDo(ctx, c.base()+"/v4/magnet/status?"+q.Encode())
	if err != nil {
		return "", err
	}
	var out struct {
		Status string `json:"status"`
		Error  struct {
			Message string `json:"message"`
		} `json:"error"`
		Data struct {
			Magnets []struct {
				Status struct {
					Code int    `json:"code"`
					Text string `json:"text"`
				} `json:"status"`
			} `json:"magnets"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	if out.Status != "success" || len(out.Data.Magnets) == 0 {
		msg := out.Error.Message
		if msg == "" {
			msg = "magnet status failed"
		}
		return "", fmt.Errorf("alldebrid: %s", msg)
	}
	return out.Data.Magnets[0].Status.Text, nil
}

func (c *Client) alldebridListTorrents(ctx context.Context, limit int) ([]Torrent, error) {
	q := url.Values{"agent": {"muxcore"}, "apikey": {c.Token}}
	body, err := c.adDo(ctx, c.base()+"/v4/magnet/status?"+q.Encode())
	if err != nil {
		return nil, err
	}
	var out struct {
		Status string `json:"status"`
		Data   struct {
			Magnets []struct {
				ID     int    `json:"id"`
				Name   string `json:"name"`
				Size   int64  `json:"size"`
				Status struct {
					Text string `json:"text"`
				} `json:"status"`
				Links []string `json:"links"`
			} `json:"magnets"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	if out.Status != "success" {
		return nil, fmt.Errorf("alldebrid: magnet list failed")
	}
	rows := out.Data.Magnets
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	result := make([]Torrent, 0, len(rows))
	for _, m := range rows {
		result = append(result, Torrent{
			ID: fmt.Sprintf("%d", m.ID), Filename: m.Name, Status: m.Status.Text,
			Links: m.Links, Filesize: m.Size,
		})
	}
	return result, nil
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

func (c *Client) alldebridList(ctx context.Context, limit int) ([]Download, error) {
	if err := c.alldebridCheckAuth(ctx); err != nil {
		return nil, err
	}
	torrents, err := c.alldebridListTorrents(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Download, 0, len(torrents))
	for _, t := range torrents {
		if t.Status != "Ready" && t.Status != "Downloaded" {
			continue
		}
		link := ""
		if len(t.Links) > 0 {
			link = t.Links[0]
		}
		out = append(out, Download{
			ID: t.ID, Filename: t.Filename, Status: t.Status,
			Link: link, Filesize: t.Filesize,
		})
	}
	return out, nil
}

func (c *Client) alldebridCheckAuth(ctx context.Context) error {
	q := url.Values{"agent": {"muxcore"}, "apikey": {c.Token}}
	body, err := c.adDo(ctx, c.base()+"/v4/user?"+q.Encode())
	if err != nil {
		return err
	}
	var out struct {
		Status string `json:"status"`
		Error  struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return err
	}
	if out.Status != "success" {
		return fmt.Errorf("alldebrid: %s", out.Error.Message)
	}
	return nil
}

func (c *Client) alldebridDelete(ctx context.Context, id string) error {
	q := url.Values{"agent": {"muxcore"}, "apikey": {c.Token}, "id": {id}}
	body, err := c.adDo(ctx, c.base()+"/v4/magnet/delete?"+q.Encode())
	if err != nil {
		return err
	}
	var out struct {
		Status string `json:"status"`
		Error  struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return err
	}
	if out.Status != "success" {
		return fmt.Errorf("alldebrid: %s", out.Error.Message)
	}
	return nil
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
