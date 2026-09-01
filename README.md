# Downloader Debrid

MuxCore sidecar bridging **Real-Debrid** and **AllDebrid** for hoster-link unrestrict, magnet/cloud queue, and VFS streaming.

Exposes `muxcore.debrid.v1.DebridDownloaderService` and SettingsProvider.

## Network safety / CI

- **CI and unit tests never require real provider tokens.** They use in-process `httptest` mocks (`NewMockRealDebrid`, `NewMockAllDebrid`) via `BaseURL` / `DEBRID_API_BASE`.
- Live Real-Debrid / AllDebrid is **operator opt-in only**: set `DEBRID_TOKEN` (or `REAL_DEBRID_TOKEN`) and optionally `DEBRID_PROVIDER`. Leave token empty for soft-empty health.
- Optional live smoke: `DEBRID_LIVE_TEST=1 DEBRID_TOKEN=… go test ./internal/debrid -run Live` — skipped by default; not part of product gates.
- HTTP `/api/add` and `/api/vfs/stream` require `DEBRID_HTTP_TOKEN` when `DEBRID_HTTP_ADDR` is not loopback-only (default bind is `127.0.0.1:9631`).

## Events

`OfflineDispatch`, `UnrestrictLink`, and `AddCloud` emit:

| Event | When |
|-------|------|
| `download.started` | Unrestrict or magnet accepted |
| `download.completed` | Direct URL resolved or magnet finished on provider |
| `download.failed` | Provider error |

When `MUXCORE_GRPC_ADDR` is set, events publish to core mesh (`download.*` → automation/notifications).

## Config

| Env | Setting | Default |
|-----|---------|---------|
| `DEBRID_PROVIDER` | `provider` | `realdebrid` (`alldebrid` supported) |
| `DEBRID_TOKEN` / `REAL_DEBRID_TOKEN` | `token` | — (empty = unconfigured / soft-empty) |
| `DEBRID_API_BASE` | — | provider default; set to `httptest` URL in tests |
| `DEBRID_HTTP_ADDR` | — | `127.0.0.1:9631` |
| `DEBRID_HTTP_TOKEN` | — | required when HTTP bind is not loopback |
| gRPC | — | `:9630` |
| VFS HTTP | — | `GET /api/vfs`, `GET /api/vfs/stream?id=` (cloud library + Range/HEAD proxy) |
| `POST /api/add` | — | queue magnet or hoster link (`{"link":"…"}`) |

## gRPC

- `UnrestrictLink` — resolve hoster link to direct URL
- `AddCloud` — queue magnet/torrent URL or unrestrict hoster link (automation path)
- `ListDownloads` / `DeleteDownload` — cloud library maintenance
- `GetCapabilities` — provider feature flags

## Build / test

```bash
CGO_ENABLED=0 go test ./...   # httptest mocks only; no real tokens
CGO_ENABLED=0 go build -o bin/downloader-debrid ./cmd/module
```

## Status

v0.1.2 — optional peer (not default host). Real-Debrid and AllDebrid magnet add, VFS cloud library, mesh event publish.
