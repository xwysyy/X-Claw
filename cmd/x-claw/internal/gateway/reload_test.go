package gateway

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/xwysyy/X-Claw/pkg/bus"
	"github.com/xwysyy/X-Claw/pkg/channels"
	telegramchannel "github.com/xwysyy/X-Claw/pkg/channels/telegram"
	"github.com/xwysyy/X-Claw/pkg/config"
	"github.com/xwysyy/X-Claw/pkg/health"
)

type reloadTestChannel struct {
	*channels.BaseChannel
	startErr   error
	startCalls int
	stopCalls  int
}

func (c *reloadTestChannel) Start(ctx context.Context) error {
	c.startCalls++
	if c.startErr != nil {
		c.SetRunning(false)
		return c.startErr
	}
	c.SetRunning(true)
	return nil
}

func (c *reloadTestChannel) Stop(ctx context.Context) error {
	c.stopCalls++
	c.SetRunning(false)
	return nil
}

func (c *reloadTestChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	return nil
}

func pickFreeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for free port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func newReloadConfig(t *testing.T, workspace, token string, port int) *config.Config {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = workspace
	cfg.Gateway.Host = "127.0.0.1"
	cfg.Gateway.Port = port
	cfg.Channels.Telegram.Enabled = true
	cfg.Channels.Telegram.Token = config.SecretRef{Inline: token}
	return cfg
}

func installReloadTestTelegramFactory(t *testing.T) {
	t.Helper()
	channels.RegisterFactory("telegram", func(cfg *config.Config, b *bus.MessageBus) (channels.Channel, error) {
		token, err := cfg.Channels.Telegram.Token.Resolve("")
		if err != nil {
			return nil, err
		}
		ch := &reloadTestChannel{
			BaseChannel: channels.NewBaseChannel("telegram", cfg.Channels.Telegram, b, cfg.Channels.Telegram.AllowFrom),
		}
		if strings.Contains(token, "fail") {
			ch.startErr = errors.New("boom: start failed")
		}
		return ch, nil
	})
	t.Cleanup(func() {
		channels.RegisterFactory("telegram", func(cfg *config.Config, b *bus.MessageBus) (channels.Channel, error) {
			return telegramchannel.NewTelegramChannel(cfg, b)
		})
	})
}

func TestReloadKeepsPreviousRuntimeWhenNewChannelManagerStartFails(t *testing.T) {
	installReloadTestTelegramFactory(t)

	workspace := t.TempDir()
	configPath := workspace + "/config.json"
	port := pickFreeTCPPort(t)
	oldCfg := newReloadConfig(t, workspace, "old-ok", port)
	if err := config.SaveConfig(configPath, oldCfg); err != nil {
		t.Fatalf("save old config: %v", err)
	}

	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	oldManager, err := channels.NewManager(oldCfg, msgBus, nil)
	if err != nil {
		t.Fatalf("create old manager: %v", err)
	}
	oldHealth := health.NewServer(oldCfg.Gateway.Host, oldCfg.Gateway.Port)
	svc := &gatewayServices{
		cfg:            oldCfg,
		configPath:     configPath,
		msgBus:         msgBus,
		channelManager: oldManager,
		healthServer:   oldHealth,
	}

	if _, err := prepareGatewayRuntime(svc, oldCfg, oldManager, oldHealth); err != nil {
		t.Fatalf("prepare old runtime: %v", err)
	}
	if err := oldManager.StartAll(context.Background()); err != nil {
		t.Fatalf("start old runtime: %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithCancel(context.Background())
		defer cancel()
		_ = svc.channelManager.StopAll(shutdownCtx)
	}()

	oldChannelRaw, ok := oldManager.GetChannel("telegram")
	if !ok {
		t.Fatal("expected old telegram channel to exist")
	}
	oldChannel, ok := oldChannelRaw.(*reloadTestChannel)
	if !ok {
		t.Fatalf("old channel type = %T, want *reloadTestChannel", oldChannelRaw)
	}
	if !oldChannel.IsRunning() {
		t.Fatal("expected old channel to be running before reload")
	}

	newCfg := newReloadConfig(t, workspace, "new-fail", port)
	if err := config.SaveConfig(configPath, newCfg); err != nil {
		t.Fatalf("save new config: %v", err)
	}

	err = svc.reload(context.Background(), "test")
	if err == nil {
		t.Fatal("expected reload to fail")
	}
	if !strings.Contains(err.Error(), "reload: start channels") {
		t.Fatalf("expected start failure, got %v", err)
	}
	if svc.cfg != oldCfg {
		t.Fatal("expected gateway services to keep previous config after failed reload")
	}
	if svc.channelManager != oldManager {
		t.Fatal("expected gateway services to keep previous manager after failed reload")
	}
	if svc.healthServer != oldHealth {
		t.Fatal("expected gateway services to keep previous health server after failed reload")
	}
	if !oldChannel.IsRunning() {
		t.Fatal("expected previous channel manager to be restored and running")
	}
	if oldChannel.startCalls < 2 {
		t.Fatalf("expected previous channel to be restarted during rollback, startCalls=%d", oldChannel.startCalls)
	}
}
