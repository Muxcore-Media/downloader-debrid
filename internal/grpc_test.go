package internal

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/Muxcore-Media/downloader-debrid/internal/debrid"
	debridv1 "github.com/Muxcore-Media/downloader-debrid/proto/gen/muxcore/debrid/v1"
)

func TestGRPCSurface(t *testing.T) {
	mock := debrid.NewMockRealDebrid("ci")
	defer mock.Close()

	m := NewModule(Config{
		Provider:   string(debrid.ProviderRealDebrid),
		Token:      "ci",
		BaseURL:    mock.URL(),
		HTTPClient: mock.Server.Client(),
	})

	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	debridv1.RegisterDebridDownloaderServiceServer(srv, &debridServer{m: m})
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	dialer := func(context.Context, string) (net.Conn, error) { return lis.Dial() }
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	cli := debridv1.NewDebridDownloaderServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	unres, err := cli.UnrestrictLink(ctx, &debridv1.UnrestrictLinkRequest{Link: "https://host.example/grpc.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	if unres.GetDownload() == "" {
		t.Fatal("empty download url")
	}

	add, err := cli.AddCloud(ctx, &debridv1.AddCloudRequest{Link: "magnet:?xt=urn:btih:grpcmag"})
	if err != nil {
		t.Fatal(err)
	}
	if add.GetKind() != "magnet" || add.GetId() == "" {
		t.Fatalf("%+v", add)
	}

	list, err := cli.ListDownloads(ctx, &debridv1.ListDownloadsRequest{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.GetItems()) == 0 {
		t.Fatal("expected downloads")
	}

	if _, err := cli.DeleteDownload(ctx, &debridv1.DeleteDownloadRequest{Id: unres.GetId()}); err != nil {
		t.Fatal(err)
	}

	caps, err := cli.GetCapabilities(ctx, &debridv1.GetCapabilitiesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !caps.GetSupportsCloudDownloads() {
		t.Fatal("expected cloud downloads")
	}
}

func TestModuleStartStopHealth(t *testing.T) {
	mock := debrid.NewMockRealDebrid("ci")
	defer mock.Close()
	m := NewModule(Config{
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
	if err := m.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestInitRejectsOpenBindWithoutToken(t *testing.T) {
	m := NewModule(Config{HTTPAddr: ":9631"})
	if err := m.Init(context.Background()); err == nil {
		t.Fatal("expected init error for open bind without DEBRID_HTTP_TOKEN")
	}
}
