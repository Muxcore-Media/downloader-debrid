package debrid

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

// MockRealDebrid is an httptest stand-in for api.real-debrid.com (no real tokens).
type MockRealDebrid struct {
	Server *httptest.Server
	Token  string

	mu        sync.Mutex
	downloads map[string]map[string]any
}

// NewMockRealDebrid starts a mock RD API. Token is the Bearer value accepted.
func NewMockRealDebrid(token string) *MockRealDebrid {
	if token == "" {
		token = "mock-token"
	}
	m := &MockRealDebrid{
		Token:     token,
		downloads: map[string]map[string]any{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/1.0/unrestrict/link", m.handleUnrestrict)
	mux.HandleFunc("/rest/1.0/torrents/addMagnet", m.handleAddMagnet)
	mux.HandleFunc("/rest/1.0/downloads", m.handleList)
	mux.HandleFunc("/rest/1.0/downloads/delete/", m.handleDelete)
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
	writeJSON(w, map[string]string{"id": id})
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

// MockAllDebrid is an httptest stand-in for api.alldebrid.com.
type MockAllDebrid struct {
	Server *httptest.Server
	Token  string
}

func NewMockAllDebrid(token string) *MockAllDebrid {
	if token == "" {
		token = "mock-token"
	}
	m := &MockAllDebrid{Token: token}
	mux := http.NewServeMux()
	mux.HandleFunc("/v4/link/unlock", m.handleUnlock)
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
			"link": "https://cdn.fixture.test/ad-fixture.bin",
			"filesize": int64(2048), "host": "fixture.test",
		},
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}