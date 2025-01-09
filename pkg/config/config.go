package config

type Config struct {
	RedmineAPIKey         string                `yaml:"redmineApiKey"`
	Lists                 map[string]ListConfig `yaml:"lists"`
	ModerationAccount     ModAccountConfig      `yaml:"moderationAccount"`
	TicketIDEncryptionKey string                `yaml:"ticketIDEncryptionKey"`
	PublicHostname        string                `yaml:"publicHostname"`
	LabelSigningKey       string                `yaml:"labelSigningKey"`
}

type ListConfig struct {
	Name string `yaml:"name"`
	URI  string `yaml:"uri"`
}

type ModAccountConfig struct {
	DID      string `yaml:"did"`
	Password string `yaml:"password"`
}
