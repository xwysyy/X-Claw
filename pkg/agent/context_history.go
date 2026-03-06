package agent

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/xwysyy/X-Claw/pkg/logger"
	"github.com/xwysyy/X-Claw/pkg/providers"
)

func (cb *ContextBuilder) pruneHistoryForContext(
	history []providers.Message,
	systemPrompt string,
) []providers.Message {
	if len(history) == 0 || cb.settings.PruningMode == "off" || cb.settings.ContextWindowTokens <= 0 {
		return history
	}

	totalTokens := estimateTotalTokens(systemPrompt, history)
	ratio := float64(totalTokens) / float64(cb.settings.ContextWindowTokens)
	if ratio < cb.settings.TriggerRatio {
		return history
	}

	cutoff := len(history) - 8
	if cutoff <= 0 {
		return history
	}

	pruned := make([]providers.Message, 0, len(history))
	for i := 0; i < cutoff; i++ {
		msg := history[i]

		if cb.settings.PruningMode == "tools_only" && msg.Role == "tool" && cb.settings.SoftToolResultChars > 0 {
			raw := msg.Content
			if len(raw) > cb.settings.SoftToolResultChars {
				head := cb.settings.SoftToolResultChars * 7 / 10
				tail := cb.settings.SoftToolResultChars * 2 / 10
				if head+tail > len(raw) {
					head = len(raw)
					tail = 0
				}
				msg.Content = raw[:head] +
					"\n...\n[tool result condensed for context stability]\n...\n" +
					raw[len(raw)-tail:]
			}
		}

		pruned = append(pruned, msg)
	}
	pruned = append(pruned, history[cutoff:]...)

	if cb.settings.IncludeOldChitChat {
		pruned = compactOldChitChat(pruned, cutoff)
	}

	totalTokens = estimateTotalTokens(systemPrompt, pruned)
	ratio = float64(totalTokens) / float64(cb.settings.ContextWindowTokens)
	if ratio < cb.settings.TriggerRatio || cb.settings.HardToolResultChars <= 0 {
		return pruned
	}

	scanLimit := minInt(cutoff, len(pruned))
	for i := 0; i < scanLimit; i++ {
		if ratio < cb.settings.TriggerRatio {
			break
		}
		msg := pruned[i]
		if msg.Role != "tool" || len(msg.Content) <= cb.settings.HardToolResultChars {
			continue
		}
		pruned[i].Content = "[tool result omitted for context stability; details preserved in session history]"
		totalTokens = estimateTotalTokens(systemPrompt, pruned)
		ratio = float64(totalTokens) / float64(cb.settings.ContextWindowTokens)
	}

	return pruned
}

func compactOldChitChat(history []providers.Message, cutoff int) []providers.Message {
	if len(history) == 0 || cutoff <= 0 {
		return history
	}

	isLowSignal := func(msg providers.Message) bool {
		if msg.Role != "user" && msg.Role != "assistant" {
			return false
		}
		if len(msg.ToolCalls) > 0 || msg.ToolCallID != "" {
			return false
		}
		text := strings.ToLower(strings.TrimSpace(msg.Content))
		if text == "" || len(text) > 40 {
			return false
		}
		switch text {
		case "ok", "okay", "thanks", "thank you", "got it", "roger", "understood", "好的", "收到", "谢谢":
			return true
		}
		return false
	}

	result := make([]providers.Message, 0, len(history))
	i := 0
	for i < len(history) {
		if i >= cutoff || !isLowSignal(history[i]) {
			result = append(result, history[i])
			i++
			continue
		}

		j := i
		for j < cutoff && isLowSignal(history[j]) {
			j++
		}
		runLen := j - i
		if runLen >= 2 {
			result = append(result, providers.Message{
				Role:    "assistant",
				Content: fmt.Sprintf("[History note: %d brief acknowledgements condensed]", runLen),
			})
		} else {
			result = append(result, history[i])
		}
		i = j
	}

	return result
}

func sanitizeHistoryForProvider(history []providers.Message) []providers.Message {
	if len(history) == 0 {
		return history
	}

	sanitized := make([]providers.Message, 0, len(history))
	var pendingToolCalls map[string]struct{}
	var pendingToolCallOrder []string
	flushPendingToolCalls := func() {
		if len(pendingToolCalls) == 0 {
			pendingToolCalls = nil
			pendingToolCallOrder = nil
			return
		}
		for _, id := range pendingToolCallOrder {
			if _, ok := pendingToolCalls[id]; !ok {
				continue
			}
			sanitized = append(sanitized, providers.Message{
				Role:       "tool",
				ToolCallID: id,
				Content:    "[tool result missing in transcript; synthesized placeholder for provider compatibility]",
			})
		}
		pendingToolCalls = nil
		pendingToolCallOrder = nil
	}

	for _, msg := range history {
		switch msg.Role {
		case "system":
			logger.DebugCF("agent", "Dropping system message from history", map[string]any{})
			continue

		case "tool":
			if pendingToolCalls == nil {
				logger.DebugCF("agent", "Dropping orphaned tool message", map[string]any{})
				continue
			}

			if len(pendingToolCalls) > 0 {
				if msg.ToolCallID == "" {
					logger.DebugCF("agent", "Dropping orphaned tool message with empty call id", map[string]any{})
					continue
				}
				if _, ok := pendingToolCalls[msg.ToolCallID]; !ok {
					logger.DebugCF(
						"agent",
						"Dropping duplicate/orphaned tool message with unknown call id",
						map[string]any{"tool_call_id": msg.ToolCallID},
					)
					continue
				}
				delete(pendingToolCalls, msg.ToolCallID)
			}
			sanitized = append(sanitized, msg)

		case "assistant":
			flushPendingToolCalls()

			if len(msg.ToolCalls) > 0 {
				if len(sanitized) == 0 {
					logger.DebugCF("agent", "Dropping assistant tool-call turn at history start", map[string]any{})
					continue
				}
				prev := sanitized[len(sanitized)-1]
				if prev.Role != "user" && prev.Role != "tool" {
					logger.DebugCF(
						"agent",
						"Dropping assistant tool-call turn with invalid predecessor",
						map[string]any{"prev_role": prev.Role},
					)
					continue
				}

				pendingToolCalls = make(map[string]struct{}, len(msg.ToolCalls))
				pendingToolCallOrder = make([]string, 0, len(msg.ToolCalls))
				for _, tc := range msg.ToolCalls {
					if tc.ID != "" {
						if _, exists := pendingToolCalls[tc.ID]; exists {
							continue
						}
						pendingToolCalls[tc.ID] = struct{}{}
						pendingToolCallOrder = append(pendingToolCallOrder, tc.ID)
					}
				}
			}
			sanitized = append(sanitized, msg)

		default:
			flushPendingToolCalls()
			sanitized = append(sanitized, msg)
		}
	}
	flushPendingToolCalls()

	return sanitized
}

func estimateMessageTokens(msg providers.Message) int {
	chars := utf8.RuneCountInString(msg.Content)
	for _, tc := range msg.ToolCalls {
		chars += utf8.RuneCountInString(tc.Name)
		if tc.Function != nil {
			chars += utf8.RuneCountInString(tc.Function.Name)
			chars += utf8.RuneCountInString(tc.Function.Arguments)
		}
	}
	if chars == 0 {
		return 0
	}
	return chars * 2 / 5
}

func estimateTotalTokens(systemPrompt string, messages []providers.Message) int {
	total := utf8.RuneCountInString(systemPrompt) * 2 / 5
	for _, msg := range messages {
		total += estimateMessageTokens(msg)
	}
	return total
}
