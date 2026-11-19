package config

import (
	"errors"
	"path/filepath"
)

type DeFiConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Manifest string `yaml:"manifest"`
}

func validateDeFiConfig(c DeFiConfig) error {
	if c.Enabled && (!filepath.IsAbs(c.Manifest) || len(c.Manifest) > 4096) {
		return errors.New("defi.enabled requires an absolute defi.manifest path")
	}
	if !c.Enabled && c.Manifest != "" {
		return errors.New("defi.manifest requires defi.enabled")
	}
	return nil
}
