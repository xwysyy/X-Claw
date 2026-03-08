package channels

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/xwysyy/X-Claw/pkg/health"
	"github.com/xwysyy/X-Claw/pkg/logger"
	"github.com/xwysyy/X-Claw/pkg/media"
)

func (m *Manager) initChannel(name, displayName string) {
	f, ok := getFactory(name)
	if !ok {
		logger.WarnCF("channels", "Factory not registered", map[string]any{
			"channel": displayName,
		})
		return
	}
	logger.DebugCF("channels", "Attempting to initialize channel", map[string]any{
		"channel": displayName,
	})
	ch, err := f(m.config, m.bus)
	if err != nil {
		logger.ErrorCF("channels", "Failed to initialize channel", map[string]any{
			"channel": displayName,
			"error":   err.Error(),
		})
		return
	}

	m.configureChannelRuntime(ch)
	m.channels[name] = ch
	logger.InfoCF("channels", "Channel enabled successfully", map[string]any{
		"channel": displayName,
	})
}

func (m *Manager) configureChannelRuntime(ch Channel) {
	if ch == nil {
		return
	}
	if m.mediaStore != nil {
		if setter, ok := ch.(interface{ SetMediaStore(s media.MediaStore) }); ok {
			setter.SetMediaStore(m.mediaStore)
		}
	}
	if setter, ok := ch.(interface{ SetPlaceholderRecorder(r PlaceholderRecorder) }); ok {
		setter.SetPlaceholderRecorder(m)
	}
	if setter, ok := ch.(interface{ SetOwner(ch Channel) }); ok {
		setter.SetOwner(ch)
	}
}

func (m *Manager) initChannels() error {
	logger.InfoC("channels", "Initializing channel manager")

	for _, spec := range selectedChannelInitializers(m.config) {
		if spec.enabled != nil && spec.enabled(m.config) {
			m.initChannel(spec.name, spec.displayName)
		}
	}

	logger.InfoCF("channels", "Channel initialization completed", map[string]any{
		"enabled_channels": len(m.channels),
	})

	return nil
}

// SetupHTTPServer creates a shared HTTP server with the given listen address.
// It registers health endpoints from the health server and discovers channels
// that implement WebhookHandler and/or HealthChecker to register their handlers.
func (m *Manager) SetupHTTPServer(addr string, healthServer *health.Server) {
	m.mux = http.NewServeMux()

	if healthServer != nil {
		m.healthServer = healthServer
		healthServer.RegisterOnMux(m.mux)
	}

	m.registerChannelHTTPHandlers()
	m.httpServer = &http.Server{
		Addr:         addr,
		Handler:      withSecurityHeaders(m.mux),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 3 * time.Minute,
		IdleTimeout:  3 * time.Minute,
	}
}

func (m *Manager) registerChannelHTTPHandlers() {
	for name, ch := range m.channels {
		if wh, ok := ch.(WebhookHandler); ok {
			m.mux.Handle(wh.WebhookPath(), wh)
			logger.InfoCF("channels", "Webhook handler registered", map[string]any{
				"channel": name,
				"path":    wh.WebhookPath(),
			})
		}
		if hc, ok := ch.(HealthChecker); ok {
			m.mux.HandleFunc(hc.HealthPath(), hc.HealthHandler)
			logger.InfoCF("channels", "Health endpoint registered", map[string]any{
				"channel": name,
				"path":    hc.HealthPath(),
			})
		}
	}
}

// RegisterHTTPHandler registers an extra HTTP handler on the shared gateway server.
// SetupHTTPServer must be called before using this.
func (m *Manager) RegisterHTTPHandler(pattern string, handler http.Handler) (err error) {
	if m.mux == nil {
		return fmt.Errorf("http server not initialized: call SetupHTTPServer first")
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("register http handler %q: %v", pattern, r)
		}
	}()
	m.mux.Handle(pattern, handler)
	return nil
}

func (m *Manager) StartAll(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.channels) == 0 {
		logger.WarnC("channels", "No channels enabled")
		return errors.New("no channels enabled")
	}

	logger.InfoC("channels", "Starting all channels")

	dispatchCtx, cancel := context.WithCancel(ctx)
	m.dispatchTask = &asyncTask{cancel: cancel}
	started := 0

	for name, channel := range m.channels {
		if m.startChannelWorker(ctx, dispatchCtx, name, channel) {
			started++
		}
	}

	if started == 0 {
		cancel()
		m.dispatchTask = nil
		if m.healthServer != nil {
			m.healthServer.SetReady(false)
		}
		return errors.New("no channels started successfully")
	}

	m.startDispatchers(dispatchCtx)
	m.startSharedHTTPServer()
	if m.healthServer != nil {
		m.healthServer.SetReady(true)
	}

	logger.InfoC("channels", "All channels started")
	return nil
}

func (m *Manager) startChannelWorker(ctx, dispatchCtx context.Context, name string, channel Channel) bool {
	logger.InfoCF("channels", "Starting channel", map[string]any{
		"channel": name,
	})
	if err := channel.Start(ctx); err != nil {
		logger.ErrorCF("channels", "Failed to start channel", map[string]any{
			"channel": name,
			"error":   err.Error(),
		})
		return false
	}

	w := newChannelWorker(name, channel)
	m.workers[name] = w
	go m.runWorker(dispatchCtx, name, w)
	go m.runMediaWorker(dispatchCtx, name, w)
	return true
}

func (m *Manager) startDispatchers(ctx context.Context) {
	go m.dispatchOutbound(ctx)
	go m.dispatchOutboundMedia(ctx)
	go m.runTTLJanitor(ctx)
}

func (m *Manager) startSharedHTTPServer() {
	if server := m.httpServer; server != nil {
		go func(server *http.Server) {
			logger.InfoCF("channels", "Shared HTTP server listening", map[string]any{
				"addr": server.Addr,
			})
			if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logger.ErrorCF("channels", "Shared HTTP server error", map[string]any{
					"error": err.Error(),
				})
			}
		}(server)
	}
}

func (m *Manager) StopAll(ctx context.Context) error {
	m.mu.Lock()

	logger.InfoC("channels", "Stopping all channels")

	if m.healthServer != nil {
		m.healthServer.SetReady(false)
	}

	m.shutdownSharedHTTPServer(ctx)
	if m.dispatchTask != nil {
		m.dispatchTask.cancel()
		m.dispatchTask = nil
	}

	workers := make([]*channelWorker, 0, len(m.workers))
	for _, w := range m.workers {
		if w != nil {
			workers = append(workers, w)
		}
	}
	m.workers = make(map[string]*channelWorker)
	m.mu.Unlock()

	waitForChannelWorkers(workers)

	m.mu.Lock()
	defer m.mu.Unlock()
	for name, channel := range m.channels {
		logger.InfoCF("channels", "Stopping channel", map[string]any{
			"channel": name,
		})
		if err := channel.Stop(ctx); err != nil {
			logger.ErrorCF("channels", "Error stopping channel", map[string]any{
				"channel": name,
				"error":   err.Error(),
			})
		}
	}

	logger.InfoC("channels", "All channels stopped")
	return nil
}

func (m *Manager) shutdownSharedHTTPServer(ctx context.Context) {
	if m.httpServer == nil {
		return
	}
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := m.httpServer.Shutdown(shutdownCtx); err != nil {
		logger.ErrorCF("channels", "Shared HTTP server shutdown error", map[string]any{
			"error": err.Error(),
		})
	}
	m.httpServer = nil
}

func waitForChannelWorkers(workers []*channelWorker) {
	for _, w := range workers {
		<-w.done
	}
	for _, w := range workers {
		<-w.mediaDone
	}
}
