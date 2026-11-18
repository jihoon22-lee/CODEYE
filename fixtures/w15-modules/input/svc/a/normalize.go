// Package a normalizes account identifiers.
package a

import "strings"

// Normalize returns id without surrounding spaces and in lower case. An empty
// result means the identifier is invalid.
func Normalize(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}
