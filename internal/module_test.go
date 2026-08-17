package internal_test

import (
	"context"
	"encoding/json"
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
	r.evts = append(r.evts, eventType+":"+p.SavePath)
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
