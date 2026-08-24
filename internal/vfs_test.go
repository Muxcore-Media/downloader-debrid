package internal

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
		Provider:   string(debrid.ProviderRealDebrid),
		Token:      "ci",
		BaseURL:    mock.URL(),
		GRPCAddr:   "127.0.0.1:0",
		HTTPAddr:   "127.0.0.1:0",
		HTTPClient: mock.Server.Client(),
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
