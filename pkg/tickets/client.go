package tickets

import (
	"github.com/mattn/go-redmine"
)

func NewClient(addr string, apiKey string) *redmine.Client {
	return redmine.NewClient(addr, apiKey)
}
