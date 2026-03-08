package gateway

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/xwysyy/X-Claw/cmd/x-claw/internal"
	"github.com/xwysyy/X-Claw/pkg/auditlog"
	"github.com/xwysyy/X-Claw/pkg/config"
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

	path, newCfg, err := svc.loadReloadConfig()
	if err != nil {
		return err
	}
	candidate, addr, err := svc.buildReloadRuntime(newCfg)
	if err != nil {
		return err
	}

	previous := captureGatewayRuntimeState(svc)
	if err := stopGatewayChannels(previous.channelManager); err != nil {
		return fmt.Errorf("reload: stop previous channels: %w", err)
	}

	if err := candidate.channelManager.StartAll(ctx); err != nil {
		restoreCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if restoreErr := restoreGatewayRuntime(restoreCtx, svc, previous); restoreErr != nil {
			return fmt.Errorf("reload: start channels: %w; rollback failed: %v", err, restoreErr)
		}
		return fmt.Errorf("reload: start channels: %w", err)
	}

	svc.applyReloadRuntime(ctx, candidate)
	logReloadSuccess(svc, reason, path, addr)
	recordReloadAudit(newCfg, reason, path, addr, svc.channelManager.GetEnabledChannels())
	return nil
}

func (svc *gatewayServices) loadReloadConfig() (string, *config.Config, error) {
	path := strings.TrimSpace(svc.configPath)
	if path == "" {
		path = internal.GetConfigPath()
	}

	newCfg, err := config.LoadConfig(path)
	if err != nil {
		return "", nil, fmt.Errorf("reload config: %w", err)
	}
	if _, err := resolveGatewayAPIKey(newCfg); err != nil {
		return "", nil, fmt.Errorf("reload: resolve gateway api_key: %w", err)
	}
	return path, newCfg, nil
}

func (svc *gatewayServices) applyReloadRuntime(ctx context.Context, next gatewayRuntimeState) {
	svc.cfg = next.cfg
	svc.channelManager = next.channelManager
	svc.healthServer = next.healthServer
	if svc.agentLoop != nil {
		svc.agentLoop.SetConfig(next.cfg)
		svc.agentLoop.ReloadMCPTools(ctx)
		svc.agentLoop.SetChannelManager(next.channelManager)
	}
}

func logReloadSuccess(svc *gatewayServices, reason, path, addr string) {
	logger.InfoCF("gateway", "Config reloaded", map[string]any{
		"reason":           reason,
		"config_path":      path,
		"enabled_channels": svc.channelManager.GetEnabledChannels(),
		"listen":           addr,
	})
}

func recordReloadAudit(cfg *config.Config, reason, path, addr string, channels []string) {
	auditlog.Record(cfg.WorkspacePath(), auditlog.Event{
		Type:   "config.reload",
		Source: "gateway",
		Note: fmt.Sprintf(
			"reason=%s path=%s listen=%s channels=%v",
			strings.TrimSpace(reason),
			strings.TrimSpace(path),
			strings.TrimSpace(addr),
			channels,
		),
	})
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
