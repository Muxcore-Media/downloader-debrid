package debrid_test

import (
	"context"
	"os"
	"testing"

	"github.com/Muxcore-Media/downloader-debrid/internal/debrid"
)

func TestRealDebridUnrestrictListDeleteOffline(t *testing.T) {
	mock := debrid.NewMockRealDebrid("tok")
	defer mock.Close()

	c := mock.Client()
	u, err := c.Unrestrict(context.Background(), "https://host.example/file", "")
	if err != nil {
		t.Fatal(err)
	}
	if u.Download == "" || u.Filename != "fixture.bin" {
		t.Fatalf("%+v", u)
	}
	list, err := c.ListDownloads(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("list=%+v", list)
	}
	if err := c.DeleteDownload(context.Background(), u.ID); err != nil {
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

func TestAllDebridUnrestrictOffline(t *testing.T) {
	mock := debrid.NewMockAllDebrid("adtok")
	defer mock.Close()
	c := mock.Client()
	u, err := c.Unrestrict(context.Background(), "https://host.example/x", "")
	if err != nil {
		t.Fatal(err)
	}
	if u.Filename != "ad-fixture.bin" || u.Download == "" {
		t.Fatalf("%+v", u)
	}
}

func TestMissingToken(t *testing.T) {
	c := &debrid.Client{Provider: debrid.ProviderRealDebrid, BaseURL: "http://127.0.0.1:9"}
	_, err := c.Unrestrict(context.Background(), "https://x", "")
	if err == nil {
		t.Fatal("expected token error")
	}
}

// Optional live smoke — never runs in CI. Requires DEBRID_LIVE_TEST=1 and DEBRID_TOKEN.
func TestLiveRealDebridOptional(t *testing.T) {
	if os.Getenv("DEBRID_LIVE_TEST") != "1" {
		t.Skip("operator opt-in only: set DEBRID_LIVE_TEST=1 and DEBRID_TOKEN")
	}
	tok := os.Getenv("DEBRID_TOKEN")
	if tok == "" {
		t.Skip("DEBRID_TOKEN unset")
	}
	c := &debrid.Client{Provider: debrid.ProviderRealDebrid, Token: tok}
	_, err := c.ListDownloads(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
}
