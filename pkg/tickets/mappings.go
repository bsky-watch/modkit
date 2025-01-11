package tickets

import (
	"fmt"
	"os"
	"slices"
	"sync"

	"bsky.watch/redmine"
	"gopkg.in/yaml.v3"
)

type TicketPriority int

const (
	PriorityLow TicketPriority = iota
	PriorityNormal
	PriorityHigh
	PriorityUrgent
)

type TicketType int

const (
	TypeTicket TicketType = iota
	TypeAppeal
	TypeRecordTicket
)

type TicketStatus int

const (
	StatusNew TicketStatus = iota
	StatusInProgress
	StatusCompleted
	StatusApplied
	StatusDuplicate
)

type IDMappings struct {
	ProjectID int `yaml:"projectId"`

	Priorities struct {
		Normal int `yaml:"normal"`
		Low    int `yaml:"low"`
		High   int `yaml:"high"`
		Urgent int `yaml:"urgent"`
	} `yaml:"priorities"`

	Statuses struct {
		New        int `yaml:"new"`
		Completed  int `yaml:"completed"`
		Applied    int `yaml:"applied"`
		Duplicate  int `yaml:"duplicate"`
		InProgress int `yaml:"inProgress"`
	} `yaml:"statuses"`

	TicketTypes struct {
		Ticket       int `yaml:"ticket"`
		Appeal       int `yaml:"appeal"`
		RecordTicket int `yaml:"recordTicket"`
	} `yaml:"ticketTypes"`

	Fields struct {
		DID         int `yaml:"did"`
		Handle      int `yaml:"handle"`
		DisplayName int `yaml:"displayName"`
		Bluesky     int `yaml:"bluesky"`
		Clearsky    int `yaml:"clearsky"`
		Approver    int `yaml:"approver"`
		AddToLists  int `yaml:"addToLists"`
		Subject     int `yaml:"subject"`
		Labels      int `yaml:"labels"`
	} `yaml:"fields"`

	Users []struct {
		Username string   `yaml:"username"`
		DIDs     []string `yaml:"dids"`
	} `yaml:"users"`
}

var mappings struct {
	IDMappings
	sync.RWMutex
}

func LoadMappingsFromFile(filename string) error {
	b, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("reading %q: %w", filename, err)
	}
	if err := yaml.Unmarshal(b, &mappings.IDMappings); err != nil {
		return err
	}
	return nil
}

func Mappings() IDMappings {
	return mappings.IDMappings
}

func GetPriority(ticket *redmine.Issue) (TicketPriority, bool) {
	if ticket.Priority == nil {
		return 0, false
	}
	switch ticket.Priority.Id {
	case mappings.Priorities.Low:
		return PriorityLow, true
	case mappings.Priorities.Normal:
		return PriorityNormal, true
	case mappings.Priorities.High:
		return PriorityHigh, true
	case mappings.Priorities.Urgent:
		return PriorityUrgent, true
	default:
		return 0, false
	}
}

func UserForDID(did string) string {
	for _, u := range mappings.Users {
		if slices.Contains(u.DIDs, did) {
			return u.Username
		}
	}
	return ""
}
