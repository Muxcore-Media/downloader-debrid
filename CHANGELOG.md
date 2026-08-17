# Changelog

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
