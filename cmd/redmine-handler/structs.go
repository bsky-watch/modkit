package main

import (
	"encoding/json"
	"time"

	"github.com/mattn/go-redmine"
)

type webhookRequest struct {
	Payload WebhookPayload `json:"payload"`
}

type WebhookPayload struct {
	Action  string   `json:"action"`
	Issue   *Issue   `json:"issue,omitempty"`
	Journal *Journal `json:"journal,omitempty"`
	URL     string   `json:"url"`
}

type User struct {
	Id        int    `json:"id"`
	Login     string `json:"login"`
	Firstname string `json:"firstname"`
	Lastname  string `json:"lastname"`
}

type Issue struct {
	Id                int                `json:"id"`
	Subject           string             `json:"subject"`
	Description       string             `json:"description"`
	DoneRatio         float64            `json:"done_ratio"`
	IsPrivate         bool               `json:"is_private"`
	LockVersion       int                `json:"lock_version"`
	Assignee          *User              `json:"assignee,omitempty"`
	Author            *User              `json:"author,omitempty"`
	ClosedOn          *time.Time         `json:"closed_on,omitempty"`
	CreatedOn         *time.Time         `json:"created_on,omitempty"`
	UpdatedOn         *time.Time         `json:"updated_on,omitempty"`
	Priority          *redmine.IdName    `json:"priority,omitempty"`
	Status            *redmine.IdName    `json:"status,omitempty"`
	CustomFieldValues []CustomFieldValue `json:"custom_field_values,omitempty"`
	ParentId          *int               `json:"parent_id,omitempty"`
	Project           *redmine.Project   `json:"project,omitempty"`
	Tracker           *redmine.IdName    `json:"tracker,omitempty"`
}

type CustomFieldValue struct {
	Id    int             `json:"custom_field_id"`
	Name  string          `json:"custom_field_name"`
	Value json.RawMessage `json:"value"`
}

type Journal struct {
	Id           int              `json:"id"`
	Notes        string           `json:"notes"`
	PrivateNotes bool             `json:"private_notes"`
	Author       *User            `json:"author,omitempty"`
	CreatedOn    *time.Time       `json:"created_on,omitempty"`
	Details      []JournalDetails `json:"details,omitempty"`
}

type JournalDetails struct {
	Id       int    `json:"id"`
	OldValue any    `json:"old_value,omitempty"`
	PropKey  string `json:"prop_key"`
	Property string `json:"property"`
	Value    string `json:"value"`
}

func (i *Issue) CustomField(id int) (*CustomFieldValue, bool) {
	for _, v := range i.CustomFieldValues {
		if v.Id == id {
			return &v, true
		}
	}
	return nil, false
}
