package debrid_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Muxcore-Media/downloader-debrid/internal/debrid"
)

func TestRealDebridUnrestrict(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/1.0/unrestrict/link", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "auth", 401)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "abc", "filename": "file.bin", "download": "https://cdn.example/file.bin",
			"filesize": 123, "host": "example.com",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Point Real-Debrid calls at our fake by temporarily using a custom transport via rewritten client methods —
	// instead, exercise through a thin wrapper: call Unrestrict against real URL won't work.
	// Use Client with HTTPClient that rewrites host via RoundTripper.
	c := &debrid.Client{
		Provider: debrid.ProviderRealDebrid,
		Token:    "tok",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			req.URL.Scheme = "http"
			req.URL.Host = srv.Listener.Addr().String()
			return http.DefaultTransport.RoundTrip(req)
		})},
	}
	u, err := c.Unrestrict(context.Background(), "https://host.example/file", "")
	if err != nil {
		t.Fatal(err)
	}
	if u.Download == "" || u.Filename != "file.bin" {
		t.Fatalf("%+v", u)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
