package main

import "strings"

func extractListId(s string) string {
	words := strings.Split(s, " ")
	last := words[len(words)-1]
	if !strings.HasPrefix(last, "[") || !strings.HasSuffix(last, "]") {
		return ""
	}
	return strings.TrimSuffix(strings.TrimPrefix(last, "["), "]")
}
