package utils

import "strings"

// CanonicalSessionKey normalizes a session key for consistent lookup across
// CLI/Gateway/Console/Channels surfaces.
//
// Session keys are treated as case-insensitive identifiers, so this function
// trims whitespace and lowercases the input.
func CanonicalSessionKey(key string) string {
	return strings.ToLower(strings.TrimSpace(key))
}
