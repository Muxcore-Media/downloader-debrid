package debrid

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

// MockRealDebrid is an httptest stand-in for api.real-debrid.com (no real tokens).
type MockRealDebrid struct {
	Server    *httptest.Server
	downloads map[string]map[string]any
	torrents  map[string]map[string]any
	Token     string
	mu        sync.Mutex
}

// NewMockRealDebrid starts a mock RD API. Token is the Bearer value accepted.
func NewMockRealDebrid(token string) *MockRealDebrid {
	if token == "" {
		token = "mock-token"
	}
	m := &MockRealDebrid{
		Token:     token,
		downloads: map[string]map[string]any{},
		torrents:  map[string]map[string]any{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/1.0/unrestrict/link", m.handleUnrestrict)
	mux.HandleFunc("/rest/1.0/torrents/addMagnet", m.handleAddMagnet)
	mux.HandleFunc("/rest/1.0/torrents/selectFiles/", m.handleSelectFiles)
	mux.HandleFunc("/rest/1.0/torrents/info/", m.handleTorrentInfo)
	mux.HandleFunc("/rest/1.0/torrents", m.handleTorrentList)
	mux.HandleFunc("/rest/1.0/downloads", m.handleList)
	mux.HandleFunc("/rest/1.0/downloads/delete/", m.handleDelete)
	mux.HandleFunc("/rest/1.0/torrents/delete/", m.handleTorrentDelete)
	m.Server = httptest.NewServer(mux)
	return m
}

func (m *MockRealDebrid) URL() string { return m.Server.URL }

func (m *MockRealDebrid) Close() { m.Server.Close() }

func (m *MockRealDebrid) Client() *Client {
	return &Client{
		Provider:   ProviderRealDebrid,
		Token:      m.Token,
		BaseURL:    m.URL(),
		HTTPClient: m.Server.Client(),
	}
}

// SetDownloadURL overrides the direct download URL for a mocked download id (VFS tests).
func (m *MockRealDebrid) SetDownloadURL(id, downloadURL string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if row, ok := m.downloads[id]; ok {
		row["download"] = downloadURL
	}
}

func (m *MockRealDebrid) handleUnrestrict(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+m.Token {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	_ = r.ParseForm()
	link := r.Form.Get("link")
	id := "rd-" + strings.TrimPrefix(link, "https://")
	if len(id) > 24 {
		id = id[:24]
	}
	row := map[string]any{
		"id": id, "filename": "fixture.bin", "download": "https://cdn.fixture.test/fixture.bin",
		"filesize": int64(1024), "host": "fixture.test", "link": link,
	}
	m.mu.Lock()
	m.downloads[id] = row
	m.mu.Unlock()
	writeJSON(w, row)
}

func (m *MockRealDebrid) handleAddMagnet(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+m.Token {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	_ = r.ParseForm()
	magnet := r.Form.Get("magnet")
	id := "rd-mag-" + strings.TrimPrefix(magnet, "magnet:")
	if len(id) > 24 {
		id = id[:24]
	}
	m.mu.Lock()
	m.torrents[id] = map[string]any{
		"id": id, "filename": "magnet-fixture.bin", "status": "waiting_files_selection",
		"links": []string{"https://host.example/magnet-fixture.bin"}, "bytes": int64(2048),
	}
	m.mu.Unlock()
	writeJSON(w, map[string]string{"id": id})
}

func (m *MockRealDebrid) handleSelectFiles(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+m.Token {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/rest/1.0/torrents/selectFiles/")
	m.mu.Lock()
	t, ok := m.torrents[id]
	if ok {
		t["status"] = "downloaded"
		links, _ := t["links"].([]string)
		if len(links) > 0 {
			dlID := "rd-dl-" + id
			m.downloads[dlID] = map[string]any{
				"id": dlID, "filename": t["filename"],
				"download": links[0], "filesize": t["bytes"],
			}
		}
	}
	m.mu.Unlock()
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *MockRealDebrid) handleTorrentInfo(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+m.Token {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/rest/1.0/torrents/info/")
	m.mu.Lock()
	t, ok := m.torrents[id]
	m.mu.Unlock()
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, t)
}

func (m *MockRealDebrid) handleTorrentList(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+m.Token {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	m.mu.Lock()
	rows := make([]map[string]any, 0, len(m.torrents))
	for _, t := range m.torrents {
		rows = append(rows, t)
	}
	m.mu.Unlock()
	writeJSON(w, rows)
}

func (m *MockRealDebrid) handleList(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+m.Token {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	m.mu.Lock()
	rows := make([]map[string]any, 0, len(m.downloads))
	for _, d := range m.downloads {
		rows = append(rows, map[string]any{
			"id": d["id"], "filename": d["filename"], "link": d["download"], "filesize": d["filesize"],
		})
	}
	m.mu.Unlock()
	writeJSON(w, rows)
}

func (m *MockRealDebrid) handleDelete(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+m.Token {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/rest/1.0/downloads/delete/")
	m.mu.Lock()
	delete(m.downloads, id)
	m.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (m *MockRealDebrid) handleTorrentDelete(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+m.Token {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/rest/1.0/torrents/delete/")
	m.mu.Lock()
	delete(m.torrents, id)
	m.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// MockAllDebrid is an httptest stand-in for api.alldebrid.com.
type MockAllDebrid struct {
	Server  *httptest.Server
	magnets map[string]map[string]any
	Token   string
	mu      sync.Mutex
	nextID  int
}

func NewMockAllDebrid(token string) *MockAllDebrid {
	if token == "" {
		token = "mock-token"
	}
	m := &MockAllDebrid{Token: token, magnets: map[string]map[string]any{}, nextID: 1}
	mux := http.NewServeMux()
	mux.HandleFunc("/v4/link/unlock", m.handleUnlock)
	mux.HandleFunc("/v4/user", m.handleUser)
	mux.HandleFunc("/v4/magnet/upload", m.handleMagnetUpload)
	mux.HandleFunc("/v4/magnet/status", m.handleMagnetStatus)
	mux.HandleFunc("/v4/magnet/delete", m.handleMagnetDelete)
	m.Server = httptest.NewServer(mux)
	return m
}

func (m *MockAllDebrid) URL() string { return m.Server.URL }
func (m *MockAllDebrid) Close()      { m.Server.Close() }

func (m *MockAllDebrid) Client() *Client {
	return &Client{
		Provider:   ProviderAllDebrid,
		Token:      m.Token,
		BaseURL:    m.URL(),
		HTTPClient: m.Server.Client(),
	}
}

func (m *MockAllDebrid) handleUser(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("apikey") != m.Token {
		writeJSON(w, map[string]any{"status": "error", "error": map[string]any{"message": "bad key"}})
		return
	}
	writeJSON(w, map[string]any{"status": "success", "data": map[string]any{"username": "mock"}})
}

func (m *MockAllDebrid) handleUnlock(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("apikey") != m.Token {
		writeJSON(w, map[string]any{
			"status": "error",
			"error":  map[string]any{"message": "bad key"},
		})
		return
	}
	writeJSON(w, map[string]any{
		"status": "success",
		"data": map[string]any{
			"id": "ad-1", "filename": "ad-fixture.bin",
			"link":     "https://cdn.fixture.test/ad-fixture.bin",
			"filesize": int64(2048), "host": "fixture.test",
		},
	})
}

func (m *MockAllDebrid) handleMagnetUpload(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("apikey") != m.Token {
		writeJSON(w, map[string]any{"status": "error", "error": map[string]any{"message": "bad key"}})
		return
	}
	magnet := q.Get("magnets[]")
	m.mu.Lock()
	m.nextID++
	id := m.nextID
	idStr := fmt.Sprintf("%d", id)
	m.magnets[idStr] = map[string]any{
		"id": id, "name": "ad-magnet.bin", "size": int64(4096),
		"status": map[string]any{"text": "Ready"},
		"links":  []string{"https://host.example/ad-magnet.bin"},
	}
	m.mu.Unlock()
	writeJSON(w, map[string]any{
		"status": "success",
		"data": map[string]any{
			"magnets": []map[string]any{{"id": id, "name": "ad-magnet.bin", "hash": magnet}},
		},
	})
}

func (m *MockAllDebrid) handleMagnetStatus(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("apikey") != m.Token {
		writeJSON(w, map[string]any{"status": "error", "error": map[string]any{"message": "bad key"}})
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if id := q.Get("id"); id != "" {
		row, ok := m.magnets[id]
		if !ok {
			writeJSON(w, map[string]any{"status": "error", "error": map[string]any{"message": "not found"}})
			return
		}
		writeJSON(w, map[string]any{
			"status": "success",
			"data":   map[string]any{"magnets": []map[string]any{row}},
		})
		return
	}
	rows := make([]map[string]any, 0, len(m.magnets))
	for _, row := range m.magnets {
		rows = append(rows, row)
	}
	writeJSON(w, map[string]any{"status": "success", "data": map[string]any{"magnets": rows}})
}

func (m *MockAllDebrid) handleMagnetDelete(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("apikey") != m.Token {
		writeJSON(w, map[string]any{"status": "error", "error": map[string]any{"message": "bad key"}})
		return
	}
	id := q.Get("id")
	m.mu.Lock()
	delete(m.magnets, id)
	m.mu.Unlock()
	writeJSON(w, map[string]any{"status": "success"})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
