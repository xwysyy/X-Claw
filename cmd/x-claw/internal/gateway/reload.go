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

	preflightCM, err := channels.NewManager(newCfg, svc.msgBus, svc.mediaStore)
	if err != nil {
		return fmt.Errorf("reload: create channel manager: %w", err)
	}
	if len(preflightCM.GetEnabledChannels()) == 0 {
		return fmt.Errorf("reload aborted: no channels enabled in new config")
	}

	svc.cfg = newCfg
	if svc.agentLoop != nil {
		svc.agentLoop.SetConfig(newCfg)
		svc.agentLoop.ReloadMCPTools(ctx)
	}

	if svc.channelManager != nil {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = svc.channelManager.StopAll(stopCtx)
		cancel()
	}

	svc.channelManager = preflightCM
	svc.healthServer = health.NewServer(newCfg.Gateway.Host, newCfg.Gateway.Port)

	if svc.agentLoop != nil {
		svc.agentLoop.SetChannelManager(svc.channelManager)
	}

	addr := fmt.Sprintf("%s:%d", newCfg.Gateway.Host, newCfg.Gateway.Port)
	svc.channelManager.SetupHTTPServer(addr, svc.healthServer)
	if err := registerGatewayHTTPAPI(svc); err != nil {
		return fmt.Errorf("reload: register http api: %w", err)
	}

	if err := svc.channelManager.StartAll(ctx); err != nil {
		return fmt.Errorf("reload: start channels: %w", err)
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
