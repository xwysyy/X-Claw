package gateway

import (
	"fmt"
	"sync"

	"github.com/xwysyy/X-Claw/pkg/agent"
	"github.com/xwysyy/X-Claw/pkg/bus"
	"github.com/xwysyy/X-Claw/pkg/channels"
	"github.com/xwysyy/X-Claw/pkg/config"
	"github.com/xwysyy/X-Claw/pkg/cron"
	"github.com/xwysyy/X-Claw/pkg/health"
	"github.com/xwysyy/X-Claw/pkg/heartbeat"
	"github.com/xwysyy/X-Claw/pkg/logger"
	"github.com/xwysyy/X-Claw/pkg/media"
	"github.com/xwysyy/X-Claw/pkg/providers"
)

type gatewayServices struct {
	cfg              *config.Config
	configPath       string
	provider         providers.LLMProvider
	msgBus           *bus.MessageBus
	agentLoop        *agent.AgentLoop
	cronService      *cron.CronService
	heartbeatService *heartbeat.HeartbeatService
	mediaStore       *media.FileMediaStore
	channelManager   *channels.Manager
	healthServer     *health.Server

	reloadMu sync.Mutex
}

func gatewayCmd(debug bool) error {
	if debug {
		logger.SetLevel(logger.DEBUG)
		fmt.Println("🔍 Debug mode enabled")
	}

	svc, err := initGatewayServices(debug)
	if err != nil {
		return err
	}

	return runGateway(svc)
}
