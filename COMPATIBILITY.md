# Compatibility

| Module Version | Core Version | Status |
|----------------|-------------|--------|
| v0.1.2         | 0.5.8+      | Current |

## gRPC service

| Service | Package | Status |
|---------|---------|--------|
| `DebridDownloaderService` | `muxcore.debrid.v1` | Current |

RPCs: `UnrestrictLink`, `AddCloud`, `ListDownloads`, `DeleteDownload`, `GetCapabilities`.

## Capabilities

- `downloader` / `downloader.debrid` / `debrid`
- `settings`

## Events

Publishes `download.started`, `download.completed`, `download.failed` to core mesh when `MUXCORE_GRPC_ADDR` is configured.
