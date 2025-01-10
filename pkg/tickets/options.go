package tickets

import (
	"fmt"

	"github.com/mattn/go-redmine"
)

type ticketData struct {
	redmine.Issue

	newDID    string
	author    string
	preUpdate *redmine.Issue
}

type TicketOption func(ticket *ticketData) error

func Subject(s string) TicketOption {
	return func(ticket *ticketData) error {
		ticket.Subject = s
		return nil
	}
}

func Description(s string) TicketOption {
	return func(ticket *ticketData) error {
		ticket.Description = s
		return nil
	}
}

func Priority(prio TicketPriority) TicketOption {
	return func(ticket *ticketData) error {
		p := -1
		switch prio {
		case PriorityLow:
			p = mappings.Priorities.Low
		case PriorityNormal:
			p = mappings.Priorities.Normal
		case PriorityHigh:
			p = mappings.Priorities.High
		case PriorityUrgent:
			p = mappings.Priorities.Urgent
		default:
			return fmt.Errorf("unknown priority value %+v", prio)
		}
		if p < 0 {
			return fmt.Errorf("missing mapping for priority %+v", prio)
		}

		ticket.PriorityId = p
		return nil
	}
}

func Attachments(list []*redmine.Upload) TicketOption {
	return func(ticket *ticketData) error {
		if len(list) == 0 {
			return nil
		}
		ticket.Uploads = append(ticket.Uploads, list...)
		return nil
	}
}

func Type(typ TicketType) TicketOption {
	return func(ticket *ticketData) error {
		value := -1
		switch typ {
		case TypeTicket:
			value = mappings.TicketTypes.Ticket
		case TypeAppeal:
			value = mappings.TicketTypes.Appeal
		}
		if value < 0 {
			return fmt.Errorf("missing mapping for ticket type %+v", typ)
		}
		ticket.TrackerId = value
		return nil
	}
}

func DID(did string) TicketOption {
	return func(ticket *ticketData) error {
		field := mappings.Fields.DID
		if field == 0 {
			return fmt.Errorf("missing mapping for DID field")
		}
		ticket.CustomFields = append(ticket.CustomFields, &redmine.CustomField{
			Id:    field,
			Value: did,
		})
		return nil
	}
}

func Handle(handle string) TicketOption {
	return func(ticket *ticketData) error {
		field := mappings.Fields.Handle
		if field == 0 {
			return fmt.Errorf("missing mapping for handle field")
		}
		ticket.CustomFields = append(ticket.CustomFields, &redmine.CustomField{
			Id:    field,
			Value: handle,
		})
		return nil
	}
}

func DisplayName(displayName string) TicketOption {
	return func(ticket *ticketData) error {
		field := mappings.Fields.DisplayName
		if field == 0 {
			return fmt.Errorf("missing mapping for display name field")
		}
		ticket.CustomFields = append(ticket.CustomFields, &redmine.CustomField{
			Id:    field,
			Value: displayName,
		})
		return nil
	}
}

func Status(status TicketStatus) TicketOption {
	return func(ticket *ticketData) error {
		value := -1
		switch status {
		case StatusApplied:
			value = mappings.Statuses.Applied
		case StatusCompleted:
			value = mappings.Statuses.Completed
		case StatusDuplicate:
			value = mappings.Statuses.Duplicate
		case StatusInProgress:
			value = mappings.Statuses.InProgress
		}
		if value < 0 {
			return fmt.Errorf("missing mapping for ticket status %+v", status)
		}
		ticket.StatusId = value
		ticket.Status = nil
		return nil
	}
}

func Author(user string) TicketOption {
	return func(ticket *ticketData) error {
		ticket.author = user
		return nil
	}
}

func WithNote(text string) TicketOption {
	return func(ticket *ticketData) error {
		ticket.Notes = text
		return nil
	}
}
