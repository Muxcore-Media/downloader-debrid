package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"

	"github.com/Muxcore-Media/core/pkg/contracts"
	modulesdk "github.com/Muxcore-Media/core/sdk/go/module"
	"github.com/Muxcore-Media/downloader-debrid/internal/debrid"
	debridv1 "github.com/Muxcore-Media/downloader-debrid/proto/gen/muxcore/debrid/v1"
)

// EventPublisher emits download.* domain events (test sink or mesh adapter).
type EventPublisher func(ctx context.Context, eventType string, payload []byte) error

type Module struct {
	id       string
	grpcAddr string
	httpAddr string

	cfgMu    sync.RWMutex
	provider debrid.Provider
	token    string
	baseURL  string
	client   *debrid.Client

	grpcSrv *grpc.Server
	lis     net.Listener
	httpSrv *http.Server

	pubMu   sync.RWMutex
	publish EventPublisher
}

type Config struct {
	ID         string
	Provider   string
	Token      string
	BaseURL    string // optional API base override (httptest mocks)
	GRPCAddr   string
	HTTPAddr   string
	Publish    EventPublisher
	HTTPClient *http.Client
}

func NewModule(cfg Config) *Module {
	if cfg.ID == "" {
		cfg.ID = "downloader-debrid"
	}
	if cfg.GRPCAddr == "" {
		cfg.GRPCAddr = ":9630"
	}
	if cfg.HTTPAddr == "" {
		cfg.HTTPAddr = ":9631"
	}
	if v := os.Getenv("DEBRID_PROVIDER"); v != "" {
		cfg.Provider = v
	}
	if v := os.Getenv("DEBRID_TOKEN"); v != "" {
		cfg.Token = v
	}
	if v := os.Getenv("REAL_DEBRID_TOKEN"); v != "" && cfg.Token == "" {
		cfg.Token = v
		if cfg.Provider == "" {
			cfg.Provider = string(debrid.ProviderRealDebrid)
		}
	}
	if v := os.Getenv("DEBRID_API_BASE"); v != "" {
		cfg.BaseURL = v
	}
	if v := os.Getenv("MUXCORE_HTTP_ADDR"); v != "" {
		cfg.HTTPAddr = v
	}
	prov := debrid.Provider(cfg.Provider)
	if prov == "" {
		prov = debrid.ProviderRealDebrid
	}
	m := &Module{
		id: cfg.ID, grpcAddr: cfg.GRPCAddr, httpAddr: cfg.HTTPAddr,
		provider: prov, token: cfg.Token, baseURL: cfg.BaseURL,
		publish: cfg.Publish,
	}
	m.rebuildClient(cfg.HTTPClient)
	return m
}

func (m *Module) rebuildClient(httpClient *http.Client) {
	c := &debrid.Client{Provider: m.provider, Token: m.token, BaseURL: m.baseURL}
	if httpClient != nil {
		c.HTTPClient = httpClient
	} else if m.client != nil && m.client.HTTPClient != nil {
		c.HTTPClient = m.client.HTTPClient
	}
	m.client = c
}

func (m *Module) SetPublisher(p EventPublisher) {
	m.pubMu.Lock()
	m.publish = p
	m.pubMu.Unlock()
}

func (m *Module) Info() contracts.ModuleInfo {
	return contracts.ModuleInfo{
		ID: m.id, Name: "Debrid Downloader", Version: "0.1.0",
		Roles:       []string{"downloader", "debrid"},
		Description: "Real-Debrid / AllDebrid link unrestrict + downloads",
		Capabilities: []string{"downloader", "downloader.debrid", "debrid", "settings"},
		HTTPAddr:     m.grpcAddr,
	}
}

func (m *Module) Init(ctx context.Context) error { return nil }

func (m *Module) Start(ctx context.Context) error {
	lis, err := net.Listen("tcp", m.grpcAddr)
	if err != nil {
		return fmt.Errorf("listen gRPC %s: %w", m.grpcAddr, err)
	}
	m.lis = lis
	m.grpcSrv = grpc.NewServer()
	debridv1.RegisterDebridDownloaderServiceServer(m.grpcSrv, &debridServer{m: m})
	modulesdk.RegisterSettings(m.grpcSrv, m.id, m)
	go func() {
		slog.Info("debrid gRPC listening", "addr", m.grpcAddr)
		if err := m.grpcSrv.Serve(lis); err != nil {
			slog.Error("gRPC serve", "error", err)
		}
	}()
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/api/add", m.handleHTTPAdd)
	mux.HandleFunc("/api/vfs", m.handleVFS)
	mux.HandleFunc("/api/vfs/stream", m.handleVFSStream)
	m.httpSrv = &http.Server{Addr: m.httpAddr, Handler: mux}
	go func() {
		slog.Info("health listening", "addr", m.httpAddr)
		if err := m.httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("health serve", "error", err)
		}
	}()
	return nil
}

func (m *Module) Stop(ctx context.Context) error {
	if m.grpcSrv != nil {
		m.grpcSrv.GracefulStop()
	}
	if m.httpSrv != nil {
		_ = m.httpSrv.Shutdown(ctx)
	}
	return nil
}

func (m *Module) Health(ctx context.Context) error {
	m.cfgMu.RLock()
	tok := m.token
	m.cfgMu.RUnlock()
	if tok == "" {
		return nil
	}
	_, err := m.client.ListDownloads(ctx, 1)
	return err
}

func (m *Module) configured() error {
	m.cfgMu.RLock()
	defer m.cfgMu.RUnlock()
	if m.token == "" {
		return fmt.Errorf("debrid unconfigured: set DEBRID_TOKEN (operator opt-in; never required for CI)")
	}
	return nil
}

// OfflineDispatch unrestricts a hoster link against the configured (or mock)
// debrid API and emits download.completed. Used by offline automation paths.
func (m *Module) OfflineDispatch(ctx context.Context, link, password string) (debrid.Unrestricted, error) {
	if err := m.configured(); err != nil {
		return debrid.Unrestricted{}, err
	}
	u, err := m.client.Unrestrict(ctx, link, password)
	if err != nil {
		m.publishDownload(contracts.EventDownloadFailed, "", link, "", err.Error())
		return debrid.Unrestricted{}, err
	}
	m.publishDownload(contracts.EventDownloadStarted, u.ID, u.Filename, "", "")
	m.publishDownload(contracts.EventDownloadCompleted, u.ID, u.Filename, u.Download, "")
	return u, nil
}

// AddCloud queues a magnet, torrent URL, or hoster link on the configured provider.
func (m *Module) AddCloud(ctx context.Context, link string) (id string, kind string, err error) {
	if err := m.configured(); err != nil {
		return "", "", err
	}
	trim := strings.TrimSpace(link)
	if trim == "" {
		return "", "", fmt.Errorf("link required")
	}
	low := strings.ToLower(trim)
	if strings.HasPrefix(low, "magnet:") || strings.HasSuffix(low, ".torrent") {
		id, err := m.client.AddMagnet(ctx, trim)
		if err != nil {
			m.publishDownload(contracts.EventDownloadFailed, "", trim, "", err.Error())
			return "", "", err
		}
		m.publishDownload(contracts.EventDownloadStarted, id, trim, "", "")
		return id, "magnet", nil
	}
	u, err := m.OfflineDispatch(ctx, trim, "")
	if err != nil {
		return "", "", err
	}
	return u.ID, "link", nil
}

func (m *Module) handleHTTPAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Link string `json:"link"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	id, kind, err := m.AddCloud(r.Context(), body.Link)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"id": id, "kind": kind, "status": "queued"})
}

func (m *Module) publishDownload(eventType, id, name, savePath, errStr string) {
	m.pubMu.RLock()
	pub := m.publish
	m.pubMu.RUnlock()
	if pub == nil {
		return
	}
	payload, err := json.Marshal(contracts.DownloadEventPayload{
		ID: id, Name: name, SavePath: savePath, Label: "debrid", Error: errStr,
	})
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := pub(ctx, eventType, payload); err != nil {
		slog.Warn("debrid: publish event failed", "type", eventType, "error", err)
	}
}

type debridServer struct {
	debridv1.UnimplementedDebridDownloaderServiceServer
	m *Module
}

func (s *debridServer) UnrestrictLink(ctx context.Context, req *debridv1.UnrestrictLinkRequest) (*debridv1.UnrestrictLinkResponse, error) {
	if err := s.m.configured(); err != nil {
		return nil, err
	}
	u, err := s.m.client.Unrestrict(ctx, req.GetLink(), req.GetPassword())
	if err != nil {
		s.m.publishDownload(contracts.EventDownloadFailed, "", req.GetLink(), "", err.Error())
		return nil, err
	}
	s.m.publishDownload(contracts.EventDownloadStarted, u.ID, u.Filename, "", "")
	s.m.publishDownload(contracts.EventDownloadCompleted, u.ID, u.Filename, u.Download, "")
	return &debridv1.UnrestrictLinkResponse{
		Id: u.ID, Filename: u.Filename, Download: u.Download, Filesize: u.Filesize, Host: u.Host,
	}, nil
}

func (s *debridServer) ListDownloads(ctx context.Context, req *debridv1.ListDownloadsRequest) (*debridv1.ListDownloadsResponse, error) {
	if err := s.m.configured(); err != nil {
		return &debridv1.ListDownloadsResponse{}, nil
	}
	items, err := s.m.client.ListDownloads(ctx, int(req.GetLimit()))
	if err != nil {
		return nil, err
	}
	out := make([]*debridv1.DownloadItem, 0, len(items))
	for _, it := range items {
		out = append(out, &debridv1.DownloadItem{
			Id: it.ID, Filename: it.Filename, Status: it.Status, Link: it.Link, Filesize: it.Filesize,
		})
	}
	return &debridv1.ListDownloadsResponse{Items: out}, nil
}

func (s *debridServer) DeleteDownload(ctx context.Context, req *debridv1.DeleteDownloadRequest) (*debridv1.DeleteDownloadResponse, error) {
	if err := s.m.configured(); err != nil {
		return nil, err
	}
	if err := s.m.client.DeleteDownload(ctx, req.GetId()); err != nil {
		return nil, err
	}
	return &debridv1.DeleteDownloadResponse{Success: true}, nil
}

func (s *debridServer) GetCapabilities(_ context.Context, _ *debridv1.GetCapabilitiesRequest) (*debridv1.GetCapabilitiesResponse, error) {
	s.m.cfgMu.RLock()
	prov := string(s.m.provider)
	s.m.cfgMu.RUnlock()
	return &debridv1.GetCapabilitiesResponse{
		Provider:               prov,
		SupportsUnrestrict:     true,
		SupportsCloudDownloads: prov == string(debrid.ProviderRealDebrid),
		Hosts:                  []string{"*"},
	}, nil
}
