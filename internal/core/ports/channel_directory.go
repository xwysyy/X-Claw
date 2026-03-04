package ports

// ChannelDirectory is the minimal channel lookup surface the agent core needs.
//
// This is intentionally an interface (port) so the agent loop does not depend on
// concrete channel manager implementations (adapters live in pkg/channels).
type ChannelDirectory interface {
	// ReasoningChannelID returns the configured "reasoning channel" chat id for
	// a given channel name, if the channel supports it.
	ReasoningChannelID(channelName string) string

	// EnabledChannels returns a list of enabled channel names.
	EnabledChannels() []string

	// HasChannel returns true if the channel exists and is enabled.
	HasChannel(channelName string) bool
}
