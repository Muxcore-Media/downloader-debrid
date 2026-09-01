package internal

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Muxcore-Media/downloader-debrid/internal/debrid"
)

func TestVFSListAndStream(t *testing.T) {
	content := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("debrid-vfs-bytes"))
	}))
	defer content.Close()

	mock := debrid.NewMockRealDebrid("ci")
	defer mock.Close()
	u, err := mock.Client().Unrestrict(context.Background(), "https://host.example/movie.mkv", "")
	if err != nil {
		t.Fatal(err)
	}
	mock.SetDownloadURL(u.ID, content.URL+"/movie.mkv")

	m := NewModule(Config{
		Provider: string(debrid.ProviderRealDebrid),
		Token:    "ci",
		BaseURL:  mock.URL(),
		GRPCAddr: "127.0.0.1:0",
		HTTPAddr: "127.0.0.1:0",
	})

	mux := http.NewServeMux()
	mux.HandleFunc("/api/vfs", m.handleVFS)
	mux.HandleFunc("/api/vfs/stream", m.handleVFSStream)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/api/vfs")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("vfs list status %d", res.StatusCode)
	}
	var list struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.NewDecoder(res.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) == 0 {
		t.Fatal("expected vfs items")
	}

	streamRes, err := http.Get(srv.URL + "/api/vfs/stream?id=" + list.Items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = streamRes.Body.Close() }()
	body, _ := io.ReadAll(streamRes.Body)
	if string(body) != "debrid-vfs-bytes" {
		t.Fatalf("stream body %q", body)
	}
}

func TestVFSStreamWithoutInjectedHTTPClient(t *testing.T) {
	content := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rng := r.Header.Get("Range"); rng != "" {
			w.Header().Set("Content-Range", "bytes 0-4/18")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte("debrid"))
			return
		}
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("debrid-vfs-bytes"))
	}))
	defer content.Close()

	mock := debrid.NewMockRealDebrid("ci")
	defer mock.Close()
	u, err := mock.Client().Unrestrict(context.Background(), "https://host.example/range.mkv", "")
	if err != nil {
		t.Fatal(err)
	}
	mock.SetDownloadURL(u.ID, content.URL+"/range.mkv")

	m := NewModule(Config{
		Provider: string(debrid.ProviderRealDebrid),
		Token:    "ci",
		BaseURL:  mock.URL(),
		GRPCAddr: "127.0.0.1:0",
		HTTPAddr: "127.0.0.1:0",
	})

	mux := http.NewServeMux()
	mux.HandleFunc("/api/vfs/stream", m.handleVFSStream)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	headRes, err := http.Head(srv.URL + "/api/vfs/stream?id=" + u.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = headRes.Body.Close() }()
	if headRes.StatusCode != http.StatusOK {
		t.Fatalf("head status %d", headRes.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/vfs/stream?id="+u.ID, http.NoBody)
	req.Header.Set("Range", "bytes=0-4")
	rangeRes, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rangeRes.Body.Close() }()
	if rangeRes.StatusCode != http.StatusPartialContent {
		t.Fatalf("range status %d", rangeRes.StatusCode)
	}
	body, _ := io.ReadAll(rangeRes.Body)
	if string(body) != "debrid" {
		t.Fatalf("range body %q", body)
	}
}

func TestVFSListsMagnetTorrent(t *testing.T) {
	mock := debrid.NewMockRealDebrid("ci")
	defer mock.Close()
	id, err := mock.Client().AddMagnet(context.Background(), "magnet:?xt=urn:btih:vfsmag")
	if err != nil {
		t.Fatal(err)
	}

	m := NewModule(Config{
		Provider:   string(debrid.ProviderRealDebrid),
		Token:      "ci",
		BaseURL:    mock.URL(),
		GRPCAddr:   "127.0.0.1:0",
		HTTPAddr:   "127.0.0.1:0",
		HTTPClient: mock.Server.Client(),
	})
	items, err := m.listVFSItems(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range items {
		if it.ID == id {
			found = true
		}
	}
	if !found {
		t.Fatalf("magnet torrent %q not in vfs: %+v", id, items)
	}
}

func TestHTTPAddRequiresAuthOnNonLoopback(t *testing.T) {
	mock := debrid.NewMockRealDebrid("ci")
	defer mock.Close()
	m := NewModule(Config{
		Provider:   string(debrid.ProviderRealDebrid),
		Token:      "ci",
		BaseURL:    mock.URL(),
		GRPCAddr:   "127.0.0.1:0",
		HTTPAddr:   "127.0.0.1:0",
		HTTPToken:  "secret",
		HTTPClient: mock.Server.Client(),
	})
	if err := m.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Stop(context.Background()) }()

	base := "http://" + m.HTTPListenAddr()
	res, err := http.Post(base+"/api/add", "application/json", strings.NewReader(`{"link":"magnet:?xt=urn:btih:nauth"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d want 401", res.StatusCode)
	}
}
