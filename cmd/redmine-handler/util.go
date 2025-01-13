package main

import (
	"fmt"
	"strings"

	"bsky.watch/redmine"
)

func extractListId(s string) string {
	words := strings.Split(s, " ")
	last := words[len(words)-1]
	if !strings.HasPrefix(last, "[") || !strings.HasSuffix(last, "]") {
		return ""
	}
	return strings.TrimSuffix(strings.TrimPrefix(last, "["), "]")
}

func updateCustomFieldValues(ticketsClient *redmine.Client, field *redmine.CustomFieldDefinition, values map[string]string) error {
	// XXX: Redmine doesn't return this field, so we're just hardcoding it for now.
	field.EditTagStyle = ptr("check_box")

	changed := false
	have := map[string]bool{}
	var newValues []redmine.CustomFieldPossibleValue
	for _, pv := range field.PossibleValues {
		if pv.Value == "dummy" {
			changed = true
			continue
		}
		// We explicitly don't delete any values here (except for "dummy"),
		// leaving it to the admin to decide how to handle
		// removing the values from existing tickets.
		id := extractListId(pv.Value)
		if id == "" {
			// No ID provided in the entry, let's just keep it as it and move on.
			newValues = append(newValues, pv)
			continue
		}
		if values[id] != "" && pv.Value != values[id] {
			pv.Value = values[id]
			changed = true
		}
		newValues = append(newValues, pv)
		have[id] = true
	}
	for id := range values {
		if have[id] {
			continue
		}
		newValues = append(newValues, redmine.CustomFieldPossibleValue{
			Value: values[id],
		})
		changed = true
	}

	if changed {
		field.PossibleValues = newValues
		if err := ticketsClient.UpdateCustomField(*field); err != nil {
			return fmt.Errorf("updating '%s' field: %w", field.Name, err)
		}
	}
	return nil
}
