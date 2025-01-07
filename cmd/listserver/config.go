package main

import (
	"bsky.watch/modkit/pkg/cliutil"
)

var cfg Config

type Config struct {
	cliutil.LoggingConfig
	ConfigPath  string `split_words:"true"`
	ListenAddr  string `split_words:"true"`
	MetricsAddr string `split_words:"true"`
}
