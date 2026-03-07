package agent

import (
	"sync/atomic"

	"github.com/xwysyy/X-Claw/pkg/bus"
)

type bucket struct {
	ch     chan bus.InboundMessage
	steer  chan bus.InboundMessage
	active atomic.Bool
}
