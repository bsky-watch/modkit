package config

import "github.com/bluesky-social/indigo/api/bsky"

type Config struct {
	RedmineAPIKey          string                           `yaml:"redmineApiKey"`
	Lists                  map[string]ListConfig            `yaml:"lists"`
	LabelerPolicies        bsky.LabelerDefs_LabelerPolicies `yaml:"labelerPolicies"`
	ModerationAccount      ModAccountConfig                 `yaml:"moderationAccount"`
	TicketIDEncryptionKey  string                           `yaml:"ticketIDEncryptionKey"`
	PublicHostname         string                           `yaml:"publicHostname"`
	LabelSigningKey        string                           `yaml:"labelSigningKey"`
	EnablePerRecordTickets bool                             `yaml:"enablePerRecordTickets"`
}

type ListConfig struct {
	Name string `yaml:"name"`
	URI  string `yaml:"uri"`
}

type ModAccountConfig struct {
	DID      string `yaml:"did"`
	Password string `yaml:"password"`
}
