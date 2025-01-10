package tickets

import (
	"bsky.watch/redmine"
)

func NewClient(addr string, apiKey string) *redmine.Client {
	return redmine.NewClient(addr, apiKey)
}
