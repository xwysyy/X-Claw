package gateway

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/xwysyy/X-Claw/pkg/logger"
)

func startGatewayServicesPrepared(ctx context.Context, svc *gatewayServices) error {
	if err := svc.cronService.Start(); err != nil {
		return fmt.Errorf("start cron service: %w", err)
	}
	fmt.Println("✓ Cron service started")

	if err := svc.heartbeatService.Start(); err != nil {
		return fmt.Errorf("start heartbeat service: %w", err)
	}
	fmt.Println("✓ Heartbeat service started")

	addr, err := prepareGatewayRuntime(svc, svc.cfg, svc.channelManager, svc.healthServer)
	if err != nil {
		return fmt.Errorf("prepare runtime: %w", err)
	}
	if err := svc.channelManager.StartAll(ctx); err != nil {
		return err
	}

	fmt.Printf("✓ Health endpoints available at http://%s/health (/healthz) and /ready (/readyz)\n", addr)
	go svc.agentLoop.Run(ctx)
	return nil
}

func gatewayPreparedReloadWatchInterval(svc *gatewayServices) time.Duration {
	if svc == nil || svc.cfg == nil || !svc.cfg.Gateway.Reload.Enabled || !svc.cfg.Gateway.Reload.Watch {
		return 0
	}
	interval := time.Duration(svc.cfg.Gateway.Reload.IntervalSeconds) * time.Second
	if interval <= 0 {
		interval = 2 * time.Second
	}
	return interval
}

func startGatewayConfigWatcherPrepared(ctx context.Context, svc *gatewayServices) {
	interval := gatewayPreparedReloadWatchInterval(svc)
	if interval <= 0 {
		return
	}
	go watchConfigFile(ctx, svc.configPath, interval, func() {
		if err := svc.reload(ctx, "watch"); err != nil {
			logger.WarnCF("gateway", "Config hot reload failed (watch)", map[string]any{
				"error": err.Error(),
			})
		}
	})
}

func handleGatewaySignalPrepared(ctx context.Context, svc *gatewayServices, sig os.Signal) (bool, error) {
	if sig != syscall.SIGHUP {
		return false, nil
	}
	if svc != nil && svc.cfg != nil && !svc.cfg.Gateway.Reload.Enabled {
		logger.InfoCF("gateway", "Ignoring SIGHUP (gateway.reload.enabled=false)", nil)
		return true, nil
	}
	if err := svc.reload(ctx, "signal"); err != nil {
		logger.WarnCF("gateway", "Config hot reload failed (SIGHUP)", map[string]any{
			"error": err.Error(),
		})
		return true, nil
	}
	logger.InfoCF("gateway", "Config hot reload applied", map[string]any{
		"source": "SIGHUP",
	})
	return true, nil
}
