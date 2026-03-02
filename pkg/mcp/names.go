package mcp

import (
	"crypto/sha1"
	"encoding/hex"
	"strings"
	"unicode"
)

// sanitizeToolName converts an arbitrary string into a conservative tool name
// compatible with common function-calling name constraints.
//
// Allowed: letters, numbers, '_' and '-'. Everything else becomes '_'.
// The result is trimmed and length-bounded with a stable hash suffix.
func sanitizeToolName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}

	var sb strings.Builder
	sb.Grow(len(name))
	for _, r := range name {
		switch {
		case r == '_' || r == '-':
			sb.WriteRune(r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			sb.WriteRune(r)
		default:
			sb.WriteByte('_')
		}
	}

	out := strings.Trim(sb.String(), "_-")
	if out == "" {
		return ""
	}

	// Keep tool names reasonably short for provider compatibility.
	const maxLen = 64
	if len(out) <= maxLen {
		return out
	}

	sum := sha1.Sum([]byte(out))
	suffix := hex.EncodeToString(sum[:])[:10]
	head := out[:maxLen-11]
	head = strings.Trim(head, "_-")
	if head == "" {
		head = "tool"
	}
	return head + "_" + suffix
}
