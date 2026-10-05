package internal

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/Muxcore-Media/core/sdk/go/client"
	modulesdk "github.com/Muxcore-Media/core/sdk/go/module"
	manifest "github.com/Muxcore-Media/downloader-debrid"
	"github.com/Muxcore-Media/downloader-debrid/internal/debrid"
	debridv1 "github.com/Muxcore-Media/downloader-debrid/proto/gen/muxcore/debrid/v1"
)

const httpReadHeaderTimeout = 10 * time.Second

// EventPublisher emits download.* domain events (test sink or mesh adapter).
type EventPublisher func(ctx context.Context, eventType string, payload []byte) error

type Module struct {
	httpLis   net.Listener
	grpcLis   net.Listener
	publish   EventPublisher
	client    *debrid.Client
	grpcSrv   *grpc.Server
	mc        *client.Client
	httpSrv   *http.Server
	id        string
	grpcAddr  string
	httpAddr  string
	token     string
	baseURL   string
	provider  debrid.Provider
	httpToken string
	pubMu     sync.RWMutex
	cfgMu     sync.RWMutex
}

type Config struct {
	HTTPClient *http.Client
	Publish    EventPublisher

	ID        string
	Provider  string
	Token     string
	BaseURL   string
	GRPCAddr  string
	HTTPAddr  string
	HTTPToken string
}

func NewModule(cfg Config) *Module {
	if cfg.ID == "" {
		cfg.ID = "downloader-debrid"
	}
	if cfg.GRPCAddr == "" {
		cfg.GRPCAddr = ":9630"
	}
	if cfg.HTTPAddr == "" {
		cfg.HTTPAddr = "127.0.0.1:9631"
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
	if v := os.Getenv("DEBRID_HTTP_ADDR"); v != "" {
		cfg.HTTPAddr = v
	} else if v := os.Getenv("MUXCORE_HTTP_ADDR"); v != "" {
		cfg.HTTPAddr = v
	}
	if v := os.Getenv("DEBRID_HTTP_TOKEN"); v != "" {
		cfg.HTTPToken = v
	}
	prov := debrid.Provider(cfg.Provider)
	if prov == "" {
		prov = debrid.ProviderRealDebrid
	}
	m := &Module{
		id: cfg.ID, grpcAddr: cfg.GRPCAddr, httpAddr: cfg.HTTPAddr,
		httpToken: cfg.HTTPToken, provider: prov, token: cfg.Token, baseURL: cfg.BaseURL,
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
		ID: m.id, Name: "Debrid Downloader", Version: modulesdk.ManifestVersion(manifest.ManifestJSON),
		Roles:        []string{"downloader", "debrid"},
		Description:  "Real-Debrid / AllDebrid link unrestrict + cloud downloads",
		Capabilities: []string{"downloader", "downloader.debrid", "debrid", "settings"},
		HTTPAddr:     m.httpAddr,
	}
}

func (m *Module) Init(_ context.Context) error {
	m.cfgMu.RLock()
	httpAddr := m.httpAddr
	httpToken := m.httpToken
	m.cfgMu.RUnlock()
	if !IsLoopbackBind(httpAddr) && httpToken == "" {
		return fmt.Errorf("DEBRID_HTTP_TOKEN is required when DEBRID_HTTP_ADDR=%q is not loopback-only", httpAddr)
	}
	return nil
}

func (m *Module) Start(ctx context.Context) error {
	var lc net.ListenConfig
	grpcLis, err := lc.Listen(ctx, "tcp", m.grpcAddr)
	if err != nil {
		return fmt.Errorf("listen gRPC %s: %w", m.grpcAddr, err)
	}
	m.grpcLis = grpcLis
	m.grpcAddr = grpcLis.Addr().String()
	m.grpcSrv = grpc.NewServer()
	debridv1.RegisterDebridDownloaderServiceServer(m.grpcSrv, &debridServer{m: m})
	modulesdk.RegisterSettings(m.grpcSrv, m.id, m)
	go func() {
		slog.Info("debrid gRPC listening", "addr", m.grpcAddr)
		if serveErr := m.grpcSrv.Serve(grpcLis); serveErr != nil {
			slog.Error("gRPC serve", "error", serveErr)
		}
	}()

	httpLis, err := lc.Listen(ctx, "tcp", m.httpAddr)
	if err != nil {
		return fmt.Errorf("listen HTTP %s: %w", m.httpAddr, err)
	}
	m.httpLis = httpLis
	m.httpAddr = httpLis.Addr().String()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/api/add", m.handleHTTPAdd)
	mux.HandleFunc("/api/vfs", m.handleVFS)
	mux.HandleFunc("/api/vfs/stream", m.handleVFSStream)
	m.httpSrv = &http.Server{
		Addr:              m.httpAddr,
		Handler:           mux,
		ReadHeaderTimeout: httpReadHeaderTimeout,
	}
	go func() {
		slog.Info("health listening", "addr", m.httpAddr)
		if err := m.httpSrv.Serve(httpLis); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("health serve", "error", err)
		}
	}()
	go m.dialCore(context.WithoutCancel(ctx))
	return nil
}

func (m *Module) Stop(ctx context.Context) error {
	if m.grpcSrv != nil {
		m.grpcSrv.GracefulStop()
	}
	if m.httpSrv != nil {
		_ = m.httpSrv.Shutdown(ctx)
	}
	if m.mc != nil {
		_ = m.mc.Close()
	}
	return nil
}

func (m *Module) GRPCAddr() string {
	if m.grpcLis != nil {
		return m.grpcLis.Addr().String()
	}
	return m.grpcAddr
}

func (m *Module) HTTPListenAddr() string {
	if m.httpLis != nil {
		return m.httpLis.Addr().String()
	}
	return m.httpAddr
}

func (m *Module) dialCore(ctx context.Context) {
	meshAddr := os.Getenv("MUXCORE_GRPC_ADDR")
	if meshAddr == "" {
		return
	}
	insecureMode := os.Getenv("MUXCORE_INSECURE_DISABLE_TLS") == "true" || os.Getenv("MUXCORE_GRPC_INSECURE") == "true"
	var opts []client.Option
	if insecureMode {
		opts = append(opts, client.WithInsecure())
	}
	c, err := client.Dial(meshAddr, opts...)
	if err != nil {
		slog.Warn("debrid: dial core failed", "error", err)
		return
	}
	m.mc = c
	m.SetPublisher(func(ctx context.Context, eventType string, payload []byte) error {
		return c.Events.Publish(ctx, eventType, m.id, payload)
	})
	slog.Info("debrid: connected to core mesh", "addr", meshAddr)
}

func (m *Module) Health(ctx context.Context) error {
	m.cfgMu.RLock()
	tok := m.token
	m.cfgMu.RUnlock()
	if tok == "" {
		return nil
	}
	return m.client.CheckAuth(ctx)
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
		m.publishDownload(ctx, contracts.EventDownloadFailed, "", link, "", err.Error())
		return debrid.Unrestricted{}, err
	}
	m.publishDownload(ctx, contracts.EventDownloadStarted, u.ID, u.Filename, "", "")
	m.publishDownload(ctx, contracts.EventDownloadCompleted, u.ID, u.Filename, u.Download, "")
	return u, nil
}

// AddCloud queues a magnet, torrent URL, or hoster link on the configured provider.
func (m *Module) AddCloud(ctx context.Context, link string) (id, kind string, err error) {
	if cfgErr := m.configured(); cfgErr != nil {
		return "", "", cfgErr
	}
	trim := strings.TrimSpace(link)
	if trim == "" {
		return "", "", fmt.Errorf("link required")
	}
	low := strings.ToLower(trim)
	if strings.HasPrefix(low, "magnet:") || strings.HasSuffix(low, ".torrent") {
		magnetID, addErr := m.client.AddMagnet(ctx, trim)
		if addErr != nil {
			m.publishDownload(ctx, contracts.EventDownloadFailed, "", trim, "", addErr.Error())
			return "", "", addErr
		}
		m.publishDownload(ctx, contracts.EventDownloadStarted, magnetID, trim, "", "")
		m.publishDownload(ctx, contracts.EventDownloadCompleted, magnetID, trim, "", "")
		return magnetID, "magnet", nil
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
	if !m.requireHTTPAuth(w, r) {
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

func (m *Module) publishDownload(ctx context.Context, eventType, id, name, savePath, errStr string) {
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
	// Publish even if the caller's ctx is already cancelled (e.g. failure events).
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
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
		s.m.publishDownload(ctx, contracts.EventDownloadFailed, "", req.GetLink(), "", err.Error())
		return nil, err
	}
	s.m.publishDownload(ctx, contracts.EventDownloadStarted, u.ID, u.Filename, "", "")
	s.m.publishDownload(ctx, contracts.EventDownloadCompleted, u.ID, u.Filename, u.Download, "")
	return &debridv1.UnrestrictLinkResponse{
		Id: u.ID, Filename: u.Filename, Download: u.Download, Filesize: u.Filesize, Host: u.Host,
	}, nil
}

func (s *debridServer) ListDownloads(ctx context.Context, req *debridv1.ListDownloadsRequest) (*debridv1.ListDownloadsResponse, error) {
	if err := s.m.configured(); err != nil {
		return &debridv1.ListDownloadsResponse{}, nil //nolint:nilerr // unconfigured backend reports an empty list, not an RPC error
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
		SupportsCloudDownloads: true,
		Hosts:                  []string{"*"},
	}, nil
}

func (s *debridServer) AddCloud(ctx context.Context, req *debridv1.AddCloudRequest) (*debridv1.AddCloudResponse, error) {
	id, kind, err := s.m.AddCloud(ctx, req.GetLink())
	if err != nil {
		return nil, err
	}
	return &debridv1.AddCloudResponse{Id: id, Kind: kind, Status: "queued"}, nil
}
