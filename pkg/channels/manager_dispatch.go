package channels

import (
	"context"
	"math"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/xwysyy/X-Claw/pkg/bus"
	"github.com/xwysyy/X-Claw/pkg/constants"
	"github.com/xwysyy/X-Claw/pkg/logger"
)

// channelRateConfig maps channel name to per-second rate limit.
var channelRateConfig = map[string]float64{
	"telegram": 20,
	"discord":  1,
	"slack":    1,
	"line":     10,
}

// newChannelWorker creates a channelWorker with a rate limiter configured
// for the given channel name.
func newChannelWorker(name string, ch Channel) *channelWorker {
	rateVal := float64(defaultRateLimit)
	if r, ok := channelRateConfig[name]; ok {
		rateVal = r
	}
	burst := int(math.Max(1, math.Ceil(rateVal/2)))

	return &channelWorker{
		ch:         ch,
		queue:      make(chan bus.OutboundMessage, defaultChannelQueueSize),
		mediaQueue: make(chan bus.OutboundMediaMessage, defaultChannelQueueSize),
		done:       make(chan struct{}),
		mediaDone:  make(chan struct{}),
		limiter:    rate.NewLimiter(rate.Limit(rateVal), burst),
	}
}

func enqueueOutboundMessage(ctx context.Context, channel string, w *channelWorker, msg bus.OutboundMessage) (sent bool, stopped bool) {
	defer func() {
		if r := recover(); r != nil {
			logger.WarnCF("channels", "Channel worker queue closed, skipping message", map[string]any{
				"channel": channel,
			})
			sent = false
			stopped = false
		}
	}()

	select {
	case w.queue <- msg:
		return true, false
	case <-ctx.Done():
		return false, true
	}
}

func (m *Manager) bindReplyContext(channel, chatID, messageID, sessionKey string) {
	if m == nil || m.bus == nil {
		return
	}
	messageID = strings.TrimSpace(messageID)
	sessionKey = strings.TrimSpace(sessionKey)
	if messageID == "" || sessionKey == "" {
		return
	}
	m.bus.BindReplyContext(channel, chatID, messageID, bus.ReplyContext{SessionKey: sessionKey})
}

func enqueueOutboundMediaMessage(ctx context.Context, channel string, w *channelWorker, msg bus.OutboundMediaMessage) (sent bool, stopped bool) {
	defer func() {
		if r := recover(); r != nil {
			logger.WarnCF("channels", "Channel media worker queue closed, skipping media message", map[string]any{
				"channel": channel,
			})
			sent = false
			stopped = false
		}
	}()

	select {
	case w.mediaQueue <- msg:
		return true, false
	case <-ctx.Done():
		return false, true
	}
}

func dispatchLoop[M any](
	ctx context.Context,
	m *Manager,
	subscribe func(context.Context) (M, bool),
	getChannel func(M) string,
	enqueue func(context.Context, *channelWorker, M) bool,
	startMsg, stopMsg, unknownMsg, noWorkerMsg string,
) {
	logger.InfoC("channels", startMsg)

	for {
		msg, ok := subscribe(ctx)
		if !ok {
			logger.InfoC("channels", stopMsg)
			return
		}

		channel := getChannel(msg)
		if constants.IsInternalChannel(channel) {
			continue
		}

		m.mu.RLock()
		_, exists := m.channels[channel]
		w, wExists := m.workers[channel]
		m.mu.RUnlock()

		if !exists {
			logger.WarnCF("channels", unknownMsg, map[string]any{"channel": channel})
			continue
		}

		if wExists && w != nil {
			if !enqueue(ctx, w, msg) {
				return
			}
		} else if exists {
			logger.WarnCF("channels", noWorkerMsg, map[string]any{"channel": channel})
		}
	}
}

func (m *Manager) dispatchOutbound(ctx context.Context) {
	dispatchLoop(
		ctx, m,
		m.bus.SubscribeOutbound,
		func(msg bus.OutboundMessage) string { return msg.Channel },
		func(ctx context.Context, w *channelWorker, msg bus.OutboundMessage) bool {
			_, stopped := enqueueOutboundMessage(ctx, msg.Channel, w, msg)
			return !stopped
		},
		"Outbound dispatcher started",
		"Outbound dispatcher stopped",
		"Unknown channel for outbound message",
		"Channel has no active worker, skipping message",
	)
}

func (m *Manager) dispatchOutboundMedia(ctx context.Context) {
	dispatchLoop(
		ctx, m,
		m.bus.SubscribeOutboundMedia,
		func(msg bus.OutboundMediaMessage) string { return msg.Channel },
		func(ctx context.Context, w *channelWorker, msg bus.OutboundMediaMessage) bool {
			_, stopped := enqueueOutboundMediaMessage(ctx, msg.Channel, w, msg)
			return !stopped
		},
		"Outbound media dispatcher started",
		"Outbound media dispatcher stopped",
		"Unknown channel for outbound media message",
		"Channel has no active worker, skipping media message",
	)
}

// lookupWorker finds the active worker for a channel, logging warnings for unknown or inactive channels.
func (m *Manager) lookupWorker(channel, label string) *channelWorker {
	m.mu.RLock()
	_, exists := m.channels[channel]
	w, wExists := m.workers[channel]
	m.mu.RUnlock()

	if !exists {
		logger.WarnCF("channels", "Unknown channel for "+label+" message", map[string]any{
			"channel": channel,
		})
		return nil
	}
	if !wExists || w == nil {
		logger.WarnCF("channels", "Channel has no active worker, skipping "+label+" message", map[string]any{
			"channel": channel,
		})
		return nil
	}
	return w
}

// runTTLJanitor periodically scans the typingStops, reactionUndos, and placeholders maps
// and evicts entries that have exceeded their TTL.
func (m *Manager) runTTLJanitor(ctx context.Context) {
	ticker := time.NewTicker(janitorInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			m.evictStaleEntries(now)
		}
	}
}

// evictStaleEntries removes expired typing stops, reactions, and placeholders.
func (m *Manager) evictStaleEntries(now time.Time) {
	m.typingStops.Range(func(key, value any) bool {
		if entry, ok := value.(typingEntry); ok {
			if now.Sub(entry.createdAt) > typingStopTTL {
				if _, loaded := m.typingStops.LoadAndDelete(key); loaded {
					entry.stop()
				}
			}
		}
		return true
	})
	m.reactionUndos.Range(func(key, value any) bool {
		if entry, ok := value.(reactionEntry); ok {
			if now.Sub(entry.createdAt) > typingStopTTL {
				if _, loaded := m.reactionUndos.LoadAndDelete(key); loaded {
					entry.undo()
				}
			}
		}
		return true
	})
	m.placeholders.Range(func(key, value any) bool {
		if entry, ok := value.(placeholderEntry); ok {
			if now.Sub(entry.createdAt) > placeholderTTL {
				m.placeholders.Delete(key)
			}
		}
		return true
	})
}
