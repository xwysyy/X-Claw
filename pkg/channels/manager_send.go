package channels

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/xwysyy/X-Claw/pkg/bus"
	"github.com/xwysyy/X-Claw/pkg/logger"
)

// runWorker processes outbound messages for a single channel, splitting
// messages that exceed the channel's maximum message length.
func (m *Manager) runWorker(ctx context.Context, name string, w *channelWorker) {
	defer close(w.done)
	for {
		select {
		case msg, ok := <-w.queue:
			if !ok {
				return
			}
			maxLen := 0
			if mlp, ok := w.ch.(MessageLengthProvider); ok {
				maxLen = mlp.MaxMessageLength()
			}
			if maxLen > 0 && len([]rune(msg.Content)) > maxLen {
				chunks := SplitMessage(msg.Content, maxLen)
				for _, chunk := range chunks {
					chunkMsg := msg
					chunkMsg.Content = chunk
					m.sendWithRetry(ctx, name, w, chunkMsg)
				}
			} else {
				m.sendWithRetry(ctx, name, w, msg)
			}
		case <-ctx.Done():
			return
		}
	}
}

// retryWithBackoff executes sendFn with retry logic.
// Callers are responsible for rate limiting before calling this function.
// Error classification determines the retry strategy:
//   - ErrNotRunning / ErrSendFailed: permanent, no retry
//   - ErrRateLimit: fixed delay retry
//   - ErrTemporary / unknown: exponential backoff retry
func (m *Manager) retryWithBackoff(ctx context.Context, w *channelWorker, sendFn func() error) error {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		lastErr = sendFn()
		if lastErr == nil {
			return nil
		}

		if errors.Is(lastErr, ErrNotRunning) || errors.Is(lastErr, ErrSendFailed) {
			break
		}
		if attempt == maxRetries {
			break
		}
		if errors.Is(lastErr, ErrRateLimit) {
			select {
			case <-time.After(rateLimitDelay):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		backoff := min(time.Duration(float64(baseBackoff)*math.Pow(2, float64(attempt))), maxBackoff)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return lastErr
}

// sendWithRetry sends a message through the channel with rate limiting and retry logic.
func (m *Manager) sendWithRetry(ctx context.Context, name string, w *channelWorker, msg bus.OutboundMessage) {
	if err := w.limiter.Wait(ctx); err != nil {
		return
	}
	if m.preSend(ctx, name, msg, w.ch) {
		return
	}

	sentMessageID := ""
	err := m.retryWithBackoff(ctx, w, func() error {
		if sender, ok := w.ch.(MessageIDSender); ok {
			messageID, err := sender.SendWithMessageID(ctx, msg)
			if err != nil {
				return err
			}
			sentMessageID = strings.TrimSpace(messageID)
			return nil
		}
		return w.ch.Send(ctx, msg)
	})
	if err == nil {
		m.bindReplyContext(name, msg.ChatID, sentMessageID, msg.SessionKey)
		return
	}
	if ctx.Err() == nil {
		logger.ErrorCF("channels", "Send failed", map[string]any{
			"channel": name,
			"chat_id": msg.ChatID,
			"error":   err.Error(),
			"retries": maxRetries,
		})
	}
}

// sendMediaWithRetry sends a media message through the channel with rate limiting and retry logic.
func (m *Manager) sendMediaWithRetry(ctx context.Context, name string, w *channelWorker, msg bus.OutboundMediaMessage) {
	ms, ok := w.ch.(MediaSender)
	if !ok {
		logger.DebugCF("channels", "Channel does not support MediaSender, skipping media", map[string]any{
			"channel": name,
		})
		return
	}
	if err := w.limiter.Wait(ctx); err != nil {
		return
	}

	err := m.retryWithBackoff(ctx, w, func() error {
		return ms.SendMedia(ctx, msg)
	})
	if err != nil && ctx.Err() == nil {
		logger.ErrorCF("channels", "SendMedia failed", map[string]any{
			"channel": name,
			"chat_id": msg.ChatID,
			"error":   err.Error(),
			"retries": maxRetries,
		})
	}
}

// runMediaWorker processes outbound media messages for a single channel.
func (m *Manager) runMediaWorker(ctx context.Context, name string, w *channelWorker) {
	defer close(w.mediaDone)
	for {
		select {
		case msg, ok := <-w.mediaQueue:
			if !ok {
				return
			}
			m.sendMediaWithRetry(ctx, name, w, msg)
		case <-ctx.Done():
			return
		}
	}
}
