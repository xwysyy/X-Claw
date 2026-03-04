package utils

import (
	"strings"
	"unicode"
)

// SanitizeMessageContent removes Unicode control characters, format characters (RTL overrides,
// zero-width characters), and other non-graphic characters that could confuse an LLM
// or cause display issues in the agent UI.
func SanitizeMessageContent(input string) string {
	var sb strings.Builder
	// Pre-allocate memory to avoid multiple allocations
	sb.Grow(len(input))

	for _, r := range input {
		// unicode.IsGraphic returns true if the rune is a Unicode graphic character.
		// This includes letters, marks, numbers, punctuation, and symbols.
		// It excludes control characters (Cc), format characters (Cf),
		// surrogates (Cs), and private use (Co).
		if unicode.IsGraphic(r) || r == '\n' || r == '\r' || r == '\t' {
			sb.WriteRune(r)
		}
	}

	return sb.String()
}

// Truncate returns a truncated version of s with at most maxLen runes.
// Handles multi-byte Unicode characters properly.
// If the string is truncated, "..." is appended to indicate truncation.
func Truncate(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	// Reserve 3 chars for "..."
	if maxLen <= 3 {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-3]) + "..."
}

const defaultHeadTailMarker = "\n... (truncated) ...\n"

// TruncateHeadTail truncates s to at most maxLen runes by keeping both the head
// and the tail. This is useful for preserving diagnostics that often appear at
// the end of tool outputs (stack traces, error summaries, etc).
//
//   - If len(s) <= maxLen, s is returned unchanged.
//   - If maxLen is too small to fit a head+marker+tail layout, this falls back to
//     Truncate(s, maxLen).
//   - tailMin is a best-effort lower bound for the number of tail runes to keep.
//     The function may keep fewer tail runes if maxLen is small.
func TruncateHeadTail(s string, maxLen int, tailMin int) string {
	return TruncateHeadTailWithMarker(s, maxLen, defaultHeadTailMarker, tailMin)
}

// TruncateHeadTailWithMarker behaves like TruncateHeadTail but allows customizing
// the marker inserted between head and tail segments.
func TruncateHeadTailWithMarker(s string, maxLen int, marker string, tailMin int) string {
	if maxLen <= 0 {
		return ""
	}

	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}

	markerRunes := []rune(marker)
	if len(markerRunes) == 0 {
		markerRunes = []rune(defaultHeadTailMarker)
	}

	if maxLen <= len(markerRunes)+3 {
		// Not enough budget for a stable head+tail layout; return a simple head truncation.
		return Truncate(s, maxLen)
	}

	if tailMin < 0 {
		tailMin = 0
	}

	// Default tail budget: 1/3 of the total.
	tailLen := maxLen / 3
	if tailLen < tailMin {
		tailLen = tailMin
	}
	if tailLen < 1 {
		tailLen = 1
	}

	// Ensure we have room for at least 1 rune of head + marker.
	maxTail := maxLen - len(markerRunes) - 1
	if maxTail < 1 {
		return Truncate(s, maxLen)
	}
	if tailLen > maxTail {
		tailLen = maxTail
	}

	headLen := maxLen - len(markerRunes) - tailLen
	if headLen < 1 {
		return Truncate(s, maxLen)
	}

	return string(runes[:headLen]) + string(markerRunes) + string(runes[len(runes)-tailLen:])
}

// DerefStr dereferences a pointer to a string and
// returns the value or a fallback if the pointer is nil.
func DerefStr(s *string, fallback string) string {
	if s == nil {
		return fallback
	}
	return *s
}
