package gateway

import (
	"context"
	"fmt"
	"time"

	"github.com/xwysyy/X-Claw/pkg/channels"
	"github.com/xwysyy/X-Claw/pkg/config"
	"github.com/xwysyy/X-Claw/pkg/health"
)

type gatewayRuntimeState struct {
	cfg            *config.Config
	channelManager *channels.Manager
	healthServer   *health.Server
}

func gatewayListenAddr(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	return fmt.Sprintf("%s:%d", cfg.Gateway.Host, cfg.Gateway.Port)
}

func captureGatewayRuntimeState(svc *gatewayServices) gatewayRuntimeState {
	return gatewayRuntimeState{
		cfg:            svc.cfg,
		channelManager: svc.channelManager,
		healthServer:   svc.healthServer,
	}
}

func (svc *gatewayServices) buildReloadRuntime(cfg *config.Config) (gatewayRuntimeState, string, error) {
	newManager, err := channels.NewManager(cfg, svc.msgBus, svc.mediaStore)
	if err != nil {
		return gatewayRuntimeState{}, "", fmt.Errorf("reload: create channel manager: %w", err)
	}
	if len(newManager.GetEnabledChannels()) == 0 {
		return gatewayRuntimeState{}, "", fmt.Errorf("reload aborted: no channels enabled in new config")
	}

	newHealth := health.NewServer(cfg.Gateway.Host, cfg.Gateway.Port)
	addr, err := prepareGatewayRuntime(svc, cfg, newManager, newHealth)
	if err != nil {
		return gatewayRuntimeState{}, "", fmt.Errorf("reload: prepare runtime: %w", err)
	}
	return gatewayRuntimeState{cfg: cfg, channelManager: newManager, healthServer: newHealth}, addr, nil
}

func prepareGatewayRuntime(svc *gatewayServices, cfg *config.Config, channelManager *channels.Manager, healthServer *health.Server) (string, error) {
	if svc == nil {
		return "", fmt.Errorf("gateway services is nil")
	}
	if cfg == nil {
		return "", fmt.Errorf("config is nil")
	}
	if channelManager == nil {
		return "", fmt.Errorf("channel manager is nil")
	}

	addr := gatewayListenAddr(cfg)
	channelManager.SetupHTTPServer(addr, healthServer)

	candidate := &gatewayServices{
		cfg:            cfg,
		agentLoop:      svc.agentLoop,
		channelManager: channelManager,
		healthServer:   healthServer,
	}
	if err := registerGatewayHTTPAPI(candidate); err != nil {
		return "", err
	}
	return addr, nil
}

func restoreGatewayRuntime(ctx context.Context, svc *gatewayServices, previous gatewayRuntimeState) error {
	if svc == nil || previous.channelManager == nil || previous.cfg == nil {
		return nil
	}
	if previous.healthServer == nil {
		previous.healthServer = health.NewServer(previous.cfg.Gateway.Host, previous.cfg.Gateway.Port)
	}

	if _, err := prepareGatewayRuntime(svc, previous.cfg, previous.channelManager, previous.healthServer); err != nil {
		return fmt.Errorf("prepare previous runtime: %w", err)
	}
	if err := previous.channelManager.StartAll(ctx); err != nil {
		return fmt.Errorf("restart previous runtime: %w", err)
	}
	return nil
}

func stopGatewayChannels(manager *channels.Manager) error {
	if manager == nil {
		return nil
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return manager.StopAll(stopCtx)
}
