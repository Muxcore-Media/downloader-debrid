package internal_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Muxcore-Media/core/pkg/contracts"
	"github.com/Muxcore-Media/downloader-debrid/internal"
	"github.com/Muxcore-Media/downloader-debrid/internal/debrid"
)

type recPub struct {
	mu   sync.Mutex
	evts []string
}

func (r *recPub) Publish(_ context.Context, eventType string, payload []byte) error {
	var p contracts.DownloadEventPayload
	_ = json.Unmarshal(payload, &p)
	r.mu.Lock()
	r.evts = append(r.evts, eventType+":"+p.Name)
	r.mu.Unlock()
	return nil
}

func (r *recPub) has(typ string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.evts {
		if len(e) >= len(typ) && e[:len(typ)] == typ {
			return true
		}
	}
	return false
}

func TestAddMagnetRD(t *testing.T) {
	mock := debrid.NewMockRealDebrid("ci")
	defer mock.Close()
	c := mock.Client()
	id, err := c.AddMagnet(context.Background(), "magnet:?xt=urn:btih:abc123")
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("empty id")
	}
	list, err := c.ListTorrents(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) == 0 {
		t.Fatal("expected torrent row")
	}
}

func TestOfflineDispatchRDCompleted(t *testing.T) {
	mock := debrid.NewMockRealDebrid("ci")
	defer mock.Close()
	pub := &recPub{}
	m := internal.NewModule(internal.Config{
		Provider:   string(debrid.ProviderRealDebrid),
		Token:      "ci",
		BaseURL:    mock.URL(),
		GRPCAddr:   "127.0.0.1:0",
		HTTPAddr:   "127.0.0.1:0",
		Publish:    pub.Publish,
		HTTPClient: mock.Server.Client(),
	})
	u, err := m.OfflineDispatch(context.Background(), "https://host.example/file", "")
	if err != nil {
		t.Fatal(err)
	}
	if u.Download == "" {
		t.Fatal("empty download url")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if pub.has(contracts.EventDownloadCompleted) && pub.has(contracts.EventDownloadStarted) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("missing events: %+v", pub.evts)
}

func TestOfflineDispatchADCompleted(t *testing.T) {
	mock := debrid.NewMockAllDebrid("ci")
	defer mock.Close()
	pub := &recPub{}
	m := internal.NewModule(internal.Config{
		Provider:   string(debrid.ProviderAllDebrid),
		Token:      "ci",
		BaseURL:    mock.URL(),
		GRPCAddr:   "127.0.0.1:0",
		HTTPAddr:   "127.0.0.1:0",
		Publish:    pub.Publish,
		HTTPClient: mock.Server.Client(),
	})
	u, err := m.OfflineDispatch(context.Background(), "https://host.example/x", "")
	if err != nil {
		t.Fatal(err)
	}
	if u.Filename != "ad-fixture.bin" {
		t.Fatalf("%+v", u)
	}
	if !pub.has(contracts.EventDownloadCompleted) {
		t.Fatalf("events=%+v", pub.evts)
	}
}

func TestAddCloudMagnet(t *testing.T) {
	mock := debrid.NewMockRealDebrid("ci")
	defer mock.Close()
	pub := &recPub{}
	m := internal.NewModule(internal.Config{
		Provider:   string(debrid.ProviderRealDebrid),
		Token:      "ci",
		BaseURL:    mock.URL(),
		GRPCAddr:   "127.0.0.1:0",
		HTTPAddr:   "127.0.0.1:0",
		Publish:    pub.Publish,
		HTTPClient: mock.Server.Client(),
	})
	id, kind, err := m.AddCloud(context.Background(), "magnet:?xt=urn:btih:fixture")
	if err != nil {
		t.Fatal(err)
	}
	if kind != "magnet" || id == "" {
		t.Fatalf("id=%s kind=%s", id, kind)
	}
	if !pub.has(contracts.EventDownloadStarted) {
		t.Fatalf("events=%+v", pub.evts)
	}
	if !pub.has(contracts.EventDownloadCompleted) {
		t.Fatalf("missing completed: %+v", pub.evts)
	}
}

func TestHTTPAddMagnetAndHoster(t *testing.T) {
	mock := debrid.NewMockRealDebrid("ci")
	defer mock.Close()
	m := internal.NewModule(internal.Config{
		Provider:   string(debrid.ProviderRealDebrid),
		Token:      "ci",
		BaseURL:    mock.URL(),
		GRPCAddr:   "127.0.0.1:0",
		HTTPAddr:   "127.0.0.1:0",
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
	post := func(link string) (*http.Response, error) {
		return http.Post(base+"/api/add", "application/json", strings.NewReader(`{"link":"`+link+`"}`))
	}

	magRes, err := post("magnet:?xt=urn:btih:httpadd")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = magRes.Body.Close() }()
	if magRes.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(magRes.Body)
		t.Fatalf("magnet status %d body %s", magRes.StatusCode, body)
	}

	linkRes, err := post("https://host.example/http-host.mkv")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = linkRes.Body.Close() }()
	if linkRes.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(linkRes.Body)
		t.Fatalf("link status %d body %s", linkRes.StatusCode, body)
	}
	var out struct {
		Kind string `json:"kind"`
	}
	if err := json.NewDecoder(linkRes.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Kind != "link" {
		t.Fatalf("kind=%q", out.Kind)
	}
}

func TestAllDebridMagnetListDelete(t *testing.T) {
	mock := debrid.NewMockAllDebrid("ci")
	defer mock.Close()
	c := mock.Client()
	id, err := c.AddMagnet(context.Background(), "magnet:?xt=urn:btih:admag")
	if err != nil {
		t.Fatal(err)
	}
	list, err := c.ListDownloads(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) == 0 {
		t.Fatal("expected list row")
	}
	if err := c.CheckAuth(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteDownload(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	list, err = c.ListDownloads(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("after delete: %+v", list)
	}
}

func TestAllDebridBadTokenHealth(t *testing.T) {
	mock := debrid.NewMockAllDebrid("good")
	defer mock.Close()
	c := &debrid.Client{
		Provider:   debrid.ProviderAllDebrid,
		Token:      "bad",
		BaseURL:    mock.URL(),
		HTTPClient: mock.Server.Client(),
	}
	if err := c.CheckAuth(context.Background()); err == nil {
		t.Fatal("expected auth failure")
	}
}

func TestUnconfiguredSoftEmpty(t *testing.T) {
	m := internal.NewModule(internal.Config{GRPCAddr: "127.0.0.1:0", HTTPAddr: "127.0.0.1:0"})
	if err := m.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, err := m.OfflineDispatch(context.Background(), "https://x", "")
	if err == nil {
		t.Fatal("expected unconfigured error")
	}
}

func TestHTTPAddInvalidJSON(t *testing.T) {
	mock := debrid.NewMockRealDebrid("ci")
	defer mock.Close()
	m := internal.NewModule(internal.Config{
		Provider:   string(debrid.ProviderRealDebrid),
		Token:      "ci",
		BaseURL:    mock.URL(),
		GRPCAddr:   "127.0.0.1:0",
		HTTPAddr:   "127.0.0.1:0",
		HTTPClient: mock.Server.Client(),
	})
	if err := m.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Stop(context.Background()) }()
	res, err := http.Post("http://"+m.HTTPListenAddr()+"/api/add", "application/json", bytes.NewReader([]byte("{")))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d", res.StatusCode)
	}
}
