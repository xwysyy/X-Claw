package gateway

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/xwysyy/X-Claw/pkg/providers"
)

func runGateway(svc *gatewayServices) error {
	fmt.Printf("✓ Gateway started on %s:%d\n", svc.cfg.Gateway.Host, svc.cfg.Gateway.Port)
	fmt.Println("Press Ctrl+C to stop")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := startGatewayServicesPrepared(ctx, svc); err != nil {
		return err
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigChan)
	startGatewayConfigWatcherPrepared(ctx, svc)

	for {
		sig := <-sigChan
		handled, err := handleGatewaySignalPrepared(ctx, svc, sig)
		if err != nil {
			return err
		}
		if handled {
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
