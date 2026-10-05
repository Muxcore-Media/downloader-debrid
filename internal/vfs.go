package internal

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type vfsItem struct { //nolint:govet // JSON response shape; field order is kept as the documented wire order
	ID       string `json:"id"`
	Filename string `json:"filename"`
	Status   string `json:"status"`
	Filesize int64  `json:"filesize"`
	Stream   string `json:"stream"`
}

func (m *Module) handleVFS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := m.configured(); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error(), "items": []vfsItem{}})
		return
	}
	limit := 50
	items, err := m.listVFSItems(r.Context(), limit)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error(), "items": []vfsItem{}})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "total": len(items)})
}

func (m *Module) listVFSItems(ctx context.Context, limit int) ([]vfsItem, error) {
	downloads, err := m.client.ListDownloads(ctx, limit)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, limit)
	out := make([]vfsItem, 0, limit)
	appendItem := func(id, filename, status string, filesize int64) {
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		out = append(out, vfsItem{
			ID: id, Filename: filename, Status: status, Filesize: filesize,
			Stream: "/api/vfs/stream?id=" + url.QueryEscape(id),
		})
	}
	for _, it := range downloads {
		appendItem(it.ID, it.Filename, it.Status, it.Filesize)
	}
	torrents, err := m.client.ListTorrents(ctx, limit)
	if err == nil {
		for _, t := range torrents {
			if t.Status != "downloaded" && t.Status != "Ready" && t.Status != "Downloaded" {
				continue
			}
			appendItem(t.ID, t.Filename, t.Status, t.Filesize)
		}
	}
	return out, nil
}

func (m *Module) handleVFSStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !m.requireHTTPAuth(w, r) {
		return
	}
	if err := m.configured(); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		http.Error(w, "id required", http.StatusBadRequest)
		return
	}
	dl, err := m.client.ResolveDownload(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	direct := strings.TrimSpace(dl.Link)
	if direct == "" {
		http.Error(w, "download link unavailable", http.StatusBadGateway)
		return
	}
	parsed, ok := allowedUpstreamURL(direct)
	if !ok {
		http.Error(w, "invalid upstream URL", http.StatusBadGateway)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, parsed.String(), http.NoBody)
	if err != nil {
		http.Error(w, "proxy build failed", http.StatusInternalServerError)
		return
	}
	if rng := r.Header.Get("Range"); rng != "" {
		req.Header.Set("Range", rng)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	for _, h := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, resp.Body)
}

func allowedUpstreamURL(raw string) (*url.URL, bool) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return nil, false
	}
	switch parsed.Scheme {
	case "https":
		return parsed, true
	case "http":
		host := parsed.Hostname()
		if host == "localhost" || host == "127.0.0.1" || host == "::1" {
			return parsed, true
		}
	}
	return nil, false
}
