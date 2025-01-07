package main

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"bsky.watch/modkit/pkg/cliutil"
	"bsky.watch/modkit/pkg/config"
)

var cfg Config

type Config struct {
	cliutil.LoggingConfig
	ConfigPath            string   `split_words:"true"`
	OwnDIDs               []string `envconfig:"OWN_DIDS"`
	MetricsAddr           string   `split_words:"true"`
	AtprotoListenAddr     string   `split_words:"true"`
	TicketIDEncryptionKey string   `split_words:"true"`
	PersistentValkeyAddr  string   `split_words:"true"`
	NodeId                int      `split_words:"true"`
}

func (cfg *Config) LoadDefaultsFromConfig(filename string) error {
	var modkitConfig config.Config
	b, err := os.ReadFile(cfg.ConfigPath)
	if err != nil {
		return fmt.Errorf("reading %q: %w", cfg.ConfigPath, err)
	}
	if err := yaml.Unmarshal(b, &modkitConfig); err != nil {
		return fmt.Errorf("parsing %q: %w", cfg.ConfigPath, err)
	}

	if len(cfg.OwnDIDs) == 0 {
		if modkitConfig.ModerationAccount.DID != "" {
			cfg.OwnDIDs = append(cfg.OwnDIDs, modkitConfig.ModerationAccount.DID)
		}
	}
	if cfg.TicketIDEncryptionKey == "" {
		cfg.TicketIDEncryptionKey = modkitConfig.TicketIDEncryptionKey
	}
	return nil
}
