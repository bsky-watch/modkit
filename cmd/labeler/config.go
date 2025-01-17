package main

import (
	"time"

	"bsky.watch/modkit/pkg/cliutil"
)

var cfg Config

type Config struct {
	cliutil.LoggingConfig
	ConfigPath          string        `split_words:"true"`
	ListenAddr          string        `split_words:"true"`
	MetricsAddr         string        `split_words:"true"`
	AdminAddr           string        `split_words:"true"`
	PostgresURL         string        `split_words:"true"`
	CloneFrom           string        `split_words:"true"`
	ListServerURL       string        `split_words:"true"`
	ListRefreshInterval time.Duration `split_words:"true"`
}
