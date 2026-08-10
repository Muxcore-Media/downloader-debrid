# Downloader Debrid

MuxCore sidecar bridging **Real-Debrid** and **AllDebrid** for hoster-link unrestrict (+ Real-Debrid downloads list/delete).

Exposes `muxcore.debrid.v1.DebridDownloaderService` and SettingsProvider.

## Config

| Env | Setting | Default |
|-----|---------|---------|
| `DEBRID_PROVIDER` | `provider` | `realdebrid` (`alldebrid` supported) |
| `DEBRID_TOKEN` / `REAL_DEBRID_TOKEN` | `token` | — |
| gRPC | — | `:9630` |
| Health | — | `:9631` |

## Build / test

```bash
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go build -o bin/downloader-debrid ./cmd/module
```

## Status

v0.1.0 scaffold — optional peer (not default host). Magnet/cloud add flows can expand later.
