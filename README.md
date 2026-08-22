# Downloader Debrid

MuxCore sidecar bridging **Real-Debrid** and **AllDebrid** for hoster-link unrestrict (+ Real-Debrid downloads list/delete).

Exposes `muxcore.debrid.v1.DebridDownloaderService` and SettingsProvider.

## Network safety / CI

- **CI and unit tests never require real provider tokens.** They use in-process `httptest` mocks (`NewMockRealDebrid`, `NewMockAllDebrid`) via `BaseURL` / `DEBRID_API_BASE`.
- Live Real-Debrid / AllDebrid is **operator opt-in only**: set `DEBRID_TOKEN` (or `REAL_DEBRID_TOKEN`) and optionally `DEBRID_PROVIDER`. Leave token empty for soft-empty health.
- Optional live smoke: `DEBRID_LIVE_TEST=1 DEBRID_TOKEN=… go test ./internal/debrid -run Live` — skipped by default; not part of product gates.

## Offline / mock path

`OfflineDispatch` (and `UnrestrictLink`) against a mock base URL emits:

| Event | When |
|-------|------|
| `download.started` | Unrestrict accepted |
| `download.completed` | Direct download URL resolved (`save_path` = unrestricted URL) |
| `download.failed` | Provider error |

## Config

| Env | Setting | Default |
|-----|---------|---------|
| `DEBRID_PROVIDER` | `provider` | `realdebrid` (`alldebrid` supported) |
| `DEBRID_TOKEN` / `REAL_DEBRID_TOKEN` | `token` | — (empty = unconfigured / soft-empty) |
| `DEBRID_API_BASE` | — | provider default; set to `httptest` URL in tests |
| gRPC | — | `:9630` |
| Health | — | `:9631` |
| VFS HTTP | — | `GET /api/vfs`, `GET /api/vfs/stream?id=` (cloud library list + Range-aware stream proxy) |

## Build / test

```bash
CGO_ENABLED=0 go test ./...   # httptest mocks only; no real tokens
CGO_ENABLED=0 go build -o bin/downloader-debrid ./cmd/module
```

## Status

v0.1.0 — optional peer (not default host). Magnet/cloud add flows can expand later.
