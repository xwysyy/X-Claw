package agent

import (
	"context"
	"errors"
	"strings"
)

// isLLMTimeoutError checks if an error is a network/HTTP timeout (not a context window error).
func isLLMTimeoutError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "deadline exceeded") ||
		strings.Contains(msg, "client.timeout") ||
		strings.Contains(msg, "timed out") ||
		strings.Contains(msg, "timeout exceeded")
}

// isContextWindowError detects real context window / token limit errors.
func isContextWindowError(err error) bool {
	if isLLMTimeoutError(err) {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "context_length_exceeded") ||
		strings.Contains(msg, "context window") ||
		strings.Contains(msg, "maximum context length") ||
		strings.Contains(msg, "token limit") ||
		strings.Contains(msg, "too many tokens") ||
		strings.Contains(msg, "max_tokens") ||
		strings.Contains(msg, "invalidparameter") ||
		strings.Contains(msg, "prompt is too long") ||
		strings.Contains(msg, "request too large")
}
