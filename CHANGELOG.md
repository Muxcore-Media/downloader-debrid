# Changelog

## [0.1.5] - 2026-10-05

### Changed
- Built on core v0.6.14 / sdk/go/module v0.6.4: unregisters on shutdown and re-registers after core restarts (ADR-0022).

## [0.1.4] - 2026-10-05


### Changed
- Reported version comes from muxcore.json (ADR-0021); built on core v0.6.12 / sdk/go/module v0.6.3 (mesh enrollment, ADR-0017).

## [0.1.2] - 2026-10-05

### Changed
- CI runs on GitHub-hosted runners from the umbrella template; retired-origin workflows removed.
- Dependencies resolve from published GitHub tags (no filesystem `replace`); requires core v0.6.0.

## [v0.1.2] — 2026-08-31

### Added
- Mesh `EventPublisher` via `dialCore` → `c.Events.Publish`
- Real-Debrid magnet `selectFiles` + poll; AllDebrid magnet upload/status/delete
- VFS torrent listing + `ResolveDownload`; stream proxy uses client default HTTP
- `AddCloud` gRPC; HTTP auth gate (`DEBRID_HTTP_TOKEN` / loopback bind)
- `golangci-lint` CI job; `ReadHeaderTimeout` on HTTP server

## [v0.1.1] — 2026-08-10

### Added
- Injectable `BaseURL` / `DEBRID_API_BASE` for `httptest` mocks
- `NewMockRealDebrid` + `NewMockAllDebrid` offline fixtures
- `OfflineDispatch` → `download.started` / `download.completed` events
- Soft-empty without tokens; live path documented as operator opt-in (`DEBRID_LIVE_TEST`)

## [v0.1.0] — 2026-08-10

### Added
- Real-Debrid + AllDebrid HTTP clients (unrestrict; RD downloads list/delete)
- `DebridDownloaderService` gRPC + SettingsProvider
- Health `:9631`
