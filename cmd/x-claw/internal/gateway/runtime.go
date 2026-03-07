package gateway

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/xwysyy/X-Claw/pkg/logger"
	"github.com/xwysyy/X-Claw/pkg/providers"
)

func runGateway(svc *gatewayServices) error {
	fmt.Printf("✓ Gateway started on %s:%d\n", svc.cfg.Gateway.Host, svc.cfg.Gateway.Port)
	fmt.Println("Press Ctrl+C to stop")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := svc.cronService.Start(); err != nil {
		return fmt.Errorf("start cron service: %w", err)
	}
	fmt.Println("✓ Cron service started")

	if err := svc.heartbeatService.Start(); err != nil {
		return fmt.Errorf("start heartbeat service: %w", err)
	}
	fmt.Println("✓ Heartbeat service started")

	addr := gatewayListenAddr(svc.cfg)
	svc.channelManager.SetupHTTPServer(addr, svc.healthServer)
	if err := registerGatewayHTTPAPI(svc); err != nil {
		return fmt.Errorf("register http api: %w", err)
	}

	if err := svc.channelManager.StartAll(ctx); err != nil {
		return err
	}

	fmt.Printf("✓ Health endpoints available at http://%s:%d/health (/healthz) and /ready (/readyz)\n", svc.cfg.Gateway.Host, svc.cfg.Gateway.Port)

	go svc.agentLoop.Run(ctx)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)

	if svc != nil && svc.cfg != nil && svc.cfg.Gateway.Reload.Enabled && svc.cfg.Gateway.Reload.Watch {
		interval := time.Duration(svc.cfg.Gateway.Reload.IntervalSeconds) * time.Second
		if interval <= 0 {
			interval = 2 * time.Second
		}
		go watchConfigFile(ctx, svc.configPath, interval, func() {
			if err := svc.reload(ctx, "watch"); err != nil {
				logger.WarnCF("gateway", "Config hot reload failed (watch)", map[string]any{
					"error": err.Error(),
				})
			}
		})
	}

	for {
		sig := <-sigChan
		if sig == syscall.SIGHUP {
			if svc != nil && svc.cfg != nil && !svc.cfg.Gateway.Reload.Enabled {
				logger.InfoCF("gateway", "Ignoring SIGHUP (gateway.reload.enabled=false)", nil)
				continue
			}
			if err := svc.reload(ctx, "signal"); err != nil {
				logger.WarnCF("gateway", "Config hot reload failed (SIGHUP)", map[string]any{
					"error": err.Error(),
				})
			} else {
				logger.InfoCF("gateway", "Config hot reload applied", map[string]any{
					"source": "SIGHUP",
				})
			}
			continue
		}

		return shutdownGateway(svc, cancel)
	}
}

func shutdownGateway(svc *gatewayServices, cancel context.CancelFunc) error {
	fmt.Println("\nShutting down...")
	if cp, ok := svc.provider.(providers.StatefulProvider); ok {
		cp.Close()
	}
	cancel()
	svc.msgBus.Close()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()

	svc.channelManager.StopAll(shutdownCtx)
	svc.heartbeatService.Stop()
	svc.cronService.Stop()
	svc.mediaStore.Stop()
	svc.agentLoop.Stop()
	fmt.Println("✓ Gateway stopped")
	return nil
}
