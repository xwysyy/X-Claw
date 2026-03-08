package providers

import (
	"encoding/json"
	"fmt"
	"strings"
)

func (p *AntigravityProvider) parseAntigravityError(statusCode int, body []byte) error {
	var errResp struct {
		Error struct {
			Code    int              `json:"code"`
			Message string           `json:"message"`
			Status  string           `json:"status"`
			Details []map[string]any `json:"details"`
		} `json:"error"`
	}

	if err := json.Unmarshal(body, &errResp); err != nil {
		return fmt.Errorf("antigravity API error (HTTP %d): %s", statusCode, truncateString(string(body), 500))
	}

	msg := errResp.Error.Message
	if statusCode == 429 {
		// Try to extract quota reset info
		for _, detail := range errResp.Error.Details {
			if typeVal, ok := detail["@type"].(string); ok && strings.HasSuffix(typeVal, "ErrorInfo") {
				if metadata, ok := detail["metadata"].(map[string]any); ok {
					if delay, ok := metadata["quotaResetDelay"].(string); ok {
						return fmt.Errorf("antigravity rate limit exceeded: %s (reset in %s)", msg, delay)
					}
				}
			}
		}
		return fmt.Errorf("antigravity rate limit exceeded: %s", msg)
	}

	return fmt.Errorf("antigravity API error (%s): %s", errResp.Error.Status, msg)
}
