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
	ConfigPath            string `split_words:"true"`
	AuthFile              string `split_words:"true"`
	AuthLogin             string `split_words:"true"`
	AuthPassword          string `split_words:"true"`
	ListenAddr            string `split_words:"true"`
	MetricsAddr           string `split_words:"true"`
	RedmineAddr           string `split_words:"true"`
	RedmineAPIKey         string `split_words:"true"`
	Mappings              string
	TicketIDEncryptionKey string `split_words:"true"`
	ListServerURL         string `split_words:"true"`
	DumpPayloads          bool   `split_words:"true"`
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

	if cfg.RedmineAPIKey == "" {
		cfg.RedmineAPIKey = modkitConfig.RedmineAPIKey
	}
	if cfg.AuthLogin == "" {
		cfg.AuthLogin = modkitConfig.ModerationAccount.DID
	}
	if cfg.AuthPassword == "" {
		cfg.AuthPassword = modkitConfig.ModerationAccount.Password
	}
	if cfg.TicketIDEncryptionKey == "" {
		cfg.TicketIDEncryptionKey = modkitConfig.TicketIDEncryptionKey
	}
	return nil
}
