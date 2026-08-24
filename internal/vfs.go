package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Muxcore-Media/downloader-debrid/internal/debrid"
)

type vfsItem struct {
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
	items, err := m.client.ListDownloads(r.Context(), limit)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error(), "items": []vfsItem{}})
		return
	}
	out := make([]vfsItem, 0, len(items))
	for _, it := range items {
		out = append(out, vfsItem{
			ID: it.ID, Filename: it.Filename, Status: it.Status, Filesize: it.Filesize,
			Stream: "/api/vfs/stream?id=" + it.ID,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"items": out, "total": len(out)})
}

func (m *Module) handleVFSStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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
	dl, err := m.findDownload(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	direct := strings.TrimSpace(dl.Link)
	if direct == "" {
		http.Error(w, "download link unavailable", http.StatusBadGateway)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, direct, nil)
	if err != nil {
		http.Error(w, "proxy build failed", http.StatusInternalServerError)
		return
	}
	if rng := r.Header.Get("Range"); rng != "" {
		req.Header.Set("Range", rng)
	}
	resp, err := m.client.HTTPClient.Do(req)
	if err != nil {
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
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

func (m *Module) findDownload(ctx context.Context, id string) (debrid.Download, error) {
	items, err := m.client.ListDownloads(ctx, 100)
	if err != nil {
		return debrid.Download{}, err
	}
	for _, it := range items {
		if it.ID == id {
			return it, nil
		}
	}
	return debrid.Download{}, fmt.Errorf("download %q not found", id)
}
