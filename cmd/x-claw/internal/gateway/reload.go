package gateway

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/xwysyy/X-Claw/cmd/x-claw/internal"
	"github.com/xwysyy/X-Claw/pkg/auditlog"
	"github.com/xwysyy/X-Claw/pkg/channels"
	"github.com/xwysyy/X-Claw/pkg/config"
	"github.com/xwysyy/X-Claw/pkg/health"
	"github.com/xwysyy/X-Claw/pkg/logger"
)

func watchConfigFile(ctx context.Context, path string, interval time.Duration, onChange func()) {
	path = strings.TrimSpace(path)
	if path == "" || interval <= 0 || onChange == nil {
		return
	}

	lastStamp := ""
	if fi, err := os.Stat(path); err == nil && fi != nil {
		lastStamp = fmt.Sprintf("%d:%d", fi.ModTime().UnixNano(), fi.Size())
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fi, err := os.Stat(path)
			if err != nil || fi == nil {
				continue
			}
			stamp := fmt.Sprintf("%d:%d", fi.ModTime().UnixNano(), fi.Size())
			if stamp == lastStamp {
				continue
			}
			lastStamp = stamp
			onChange()
		}
	}
}

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

func (svc *gatewayServices) reload(ctx context.Context, reason string) error {
	if svc == nil {
		return fmt.Errorf("gateway services is nil")
	}

	svc.reloadMu.Lock()
	defer svc.reloadMu.Unlock()

	path := strings.TrimSpace(svc.configPath)
	if path == "" {
		path = internal.GetConfigPath()
	}

	newCfg, err := config.LoadConfig(path)
	if err != nil {
		return fmt.Errorf("reload config: %w", err)
	}

	if _, err := resolveGatewayAPIKey(newCfg); err != nil {
		return fmt.Errorf("reload: resolve gateway api_key: %w", err)
	}

	newManager, err := channels.NewManager(newCfg, svc.msgBus, svc.mediaStore)
	if err != nil {
		return fmt.Errorf("reload: create channel manager: %w", err)
	}
	if len(newManager.GetEnabledChannels()) == 0 {
		return fmt.Errorf("reload aborted: no channels enabled in new config")
	}

	newHealth := health.NewServer(newCfg.Gateway.Host, newCfg.Gateway.Port)
	addr, err := prepareGatewayRuntime(svc, newCfg, newManager, newHealth)
	if err != nil {
		return fmt.Errorf("reload: prepare runtime: %w", err)
	}

	previous := gatewayRuntimeState{
		cfg:            svc.cfg,
		channelManager: svc.channelManager,
		healthServer:   svc.healthServer,
	}

	if previous.channelManager != nil {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		stopErr := previous.channelManager.StopAll(stopCtx)
		cancel()
		if stopErr != nil {
			return fmt.Errorf("reload: stop previous channels: %w", stopErr)
		}
	}

	if err := newManager.StartAll(ctx); err != nil {
		restoreCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if restoreErr := restoreGatewayRuntime(restoreCtx, svc, previous); restoreErr != nil {
			return fmt.Errorf("reload: start channels: %w; rollback failed: %v", err, restoreErr)
		}
		return fmt.Errorf("reload: start channels: %w", err)
	}

	svc.cfg = newCfg
	svc.channelManager = newManager
	svc.healthServer = newHealth
	if svc.agentLoop != nil {
		svc.agentLoop.SetConfig(newCfg)
		svc.agentLoop.ReloadMCPTools(ctx)
		svc.agentLoop.SetChannelManager(newManager)
	}

	logger.InfoCF("gateway", "Config reloaded", map[string]any{
		"reason":           reason,
		"config_path":      path,
		"enabled_channels": svc.channelManager.GetEnabledChannels(),
		"listen":           addr,
	})

	auditlog.Record(newCfg.WorkspacePath(), auditlog.Event{
		Type:   "config.reload",
		Source: "gateway",
		Note: fmt.Sprintf(
			"reason=%s path=%s listen=%s channels=%v",
			strings.TrimSpace(reason),
			strings.TrimSpace(path),
			strings.TrimSpace(addr),
			svc.channelManager.GetEnabledChannels(),
		),
	})

	return nil
}

func resolveGatewayAPIKey(cfg *config.Config) (string, error) {
	if cfg == nil {
		return "", nil
	}
	if !cfg.Gateway.APIKey.Present() {
		return "", nil
	}
	v, err := cfg.Gateway.APIKey.Resolve("")
	if err != nil {
		return "", err
	}
	v = strings.TrimSpace(v)
	if v == "" {
		return "", fmt.Errorf("secret resolved empty")
	}
	return v, nil
}
