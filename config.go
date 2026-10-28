package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config is persisted as config.json next to the extracted files.
type Config struct {
	Strategy   string `json:"strategy"`
	GameFilter string `json:"gameFilter"` // off | all | tcp | udp
	IPSet      string `json:"ipset"`      // none | loaded | any
	Autostart  bool   `json:"autostart"`
	Theme      string `json:"theme"` // auto | dark | light
	// Fake packets used instead of the default ACTIVE_*_UDP.bin ("" = default); names without ".bin".
	DiscordFake  string `json:"discordFake"`
	GameFake     string `json:"gameFake"`
	CheckUpdates bool   `json:"checkUpdates"` // look for a new version at start (notify only)
}

func defaultConfig() Config {
	return Config{Strategy: "general", GameFilter: "off", IPSet: "none", Theme: themeAuto, CheckUpdates: true}
}

// loadConfig never fails: a missing or broken file yields the defaults.
func loadConfig(path string) Config {
	cfg := defaultConfig()
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &cfg)
	}
	if indexOf(gameModes, cfg.GameFilter) < 0 {
		cfg.GameFilter = "off"
	}
	if indexOf(ipsetModes, cfg.IPSet) < 0 {
		cfg.IPSet = "none"
	}
	if !validTheme(cfg.Theme) { // also covers configs written by older versions
		cfg.Theme = themeAuto
	}
	return cfg
}

func saveConfig(path string, cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
