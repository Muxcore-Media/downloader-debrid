package internal

import (
	"fmt"

	"github.com/Muxcore-Media/core/pkg/contracts"
	"github.com/Muxcore-Media/downloader-debrid/internal/debrid"
)

func (m *Module) Settings() []contracts.SettingDef {
	m.cfgMu.RLock()
	defer m.cfgMu.RUnlock()
	tok := m.token
	if tok != "" {
		tok = "********"
	}
	return []contracts.SettingDef{
		{
			Key: "provider", Label: "Provider", Type: contracts.SettingTypeSelect,
			Value: string(m.provider), Options: []string{"realdebrid", "alldebrid"},
			Description: "Debrid backend (operator opt-in)", Group: "Connection",
		},
		{
			Key: "token", Label: "API token", Type: contracts.SettingTypeSecret,
			Value: tok, Description: "Provider API token (operator opt-in; never required for CI)", Group: "Connection",
		},
	}
}

func (m *Module) UpdateSetting(key, value string) error {
	m.cfgMu.Lock()
	defer m.cfgMu.Unlock()
	switch key {
	case "provider":
		switch debrid.Provider(value) {
		case debrid.ProviderRealDebrid, debrid.ProviderAllDebrid:
			m.provider = debrid.Provider(value)
		default:
			return fmt.Errorf("unknown provider %q", value)
		}
	case "token":
		if value != "" && value != "********" {
			m.token = value
		}
	default:
		return fmt.Errorf("unknown setting %q", key)
	}
	m.rebuildClient(nil)
	return nil
}
