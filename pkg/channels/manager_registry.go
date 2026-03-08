package channels

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/xwysyy/X-Claw/pkg/bus"
)

func (m *Manager) GetChannel(name string) (Channel, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	channel, ok := m.channels[name]
	return channel, ok
}

func (m *Manager) GetStatus() map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()

	status := make(map[string]any)
	for name, channel := range m.channels {
		status[name] = map[string]any{
			"enabled": true,
			"running": channel.IsRunning(),
		}
	}
	return status
}

func (m *Manager) GetEnabledChannels() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, 0, len(m.channels))
	for name := range m.channels {
		names = append(names, name)
	}
	return names
}

// EnabledChannels implements the agent.ChannelDirectory port.
// It is a small adapter shim to avoid agent core depending on the full Manager API.
func (m *Manager) EnabledChannels() []string {
	return m.GetEnabledChannels()
}

// HasChannel implements the agent.ChannelDirectory port.
func (m *Manager) HasChannel(channelName string) bool {
	_, ok := m.GetChannel(channelName)
	return ok
}

// ReasoningChannelID implements the agent.ChannelDirectory port.
func (m *Manager) ReasoningChannelID(channelName string) string {
	ch, ok := m.GetChannel(channelName)
	if !ok || ch == nil {
		return ""
	}
	return ch.ReasoningChannelID()
}

func (m *Manager) RegisterChannel(name string, channel Channel) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.channels[name] = channel
}

func (m *Manager) UnregisterChannel(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if w, ok := m.workers[name]; ok && w != nil {
		close(w.queue)
		<-w.done
		close(w.mediaQueue)
		<-w.mediaDone
	}
	delete(m.workers, name)
	delete(m.channels, name)
}

func (m *Manager) SendToChannel(ctx context.Context, channelName, chatID, content string) error {
	m.mu.RLock()
	_, exists := m.channels[channelName]
	w, wExists := m.workers[channelName]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("channel %s not found", channelName)
	}

	msg := bus.OutboundMessage{
		Channel: channelName,
		ChatID:  chatID,
		Content: content,
	}

	if !wExists || w == nil {
		return fmt.Errorf("%w: channel %s has no active worker", ErrNotRunning, channelName)
	}

	sent, stopped := enqueueOutboundMessage(ctx, channelName, w, msg)
	if stopped {
		return ctx.Err()
	}
	if !sent {
		return fmt.Errorf("%w: channel %s has no active worker", ErrNotRunning, channelName)
	}
	return nil
}

var (
	uniqueIDCounter uint64
	uniqueIDPrefix  string
)

func init() {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		binary.BigEndian.PutUint64(b[:], uint64(time.Now().UnixNano()))
	}
	uniqueIDPrefix = hex.EncodeToString(b[:])
}

// uniqueID generates a process-unique ID using a random prefix and an atomic counter.
// This ID is intended for internal correlation (e.g. media scope keys) and is NOT
// cryptographically secure — it must not be used in contexts where unpredictability matters.
func uniqueID() string {
	n := atomic.AddUint64(&uniqueIDCounter, 1)
	return uniqueIDPrefix + strconv.FormatUint(n, 16)
}
