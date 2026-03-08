package gateway

import (
	"context"
	"fmt"
	"time"

	coregateway "github.com/xwysyy/X-Claw/internal/gateway"
	pkghttpapi "github.com/xwysyy/X-Claw/pkg/httpapi"
	"github.com/xwysyy/X-Claw/pkg/session"
)

func buildGatewayHTTPRegistrations(svc *gatewayServices) ([]gatewayHTTPRegistration, error) {
	if svc == nil || svc.cfg == nil {
		return nil, nil
	}

	apiKey, err := resolveGatewayAPIKey(svc.cfg)
	if err != nil {
		return nil, fmt.Errorf("gateway.api_key: %w", err)
	}

	regs := []gatewayHTTPRegistration{
		buildGatewayNotifyRegistration(svc, apiKey),
		buildGatewayResumeRegistration(svc, apiKey),
		buildGatewaySessionModelRegistration(svc, apiKey),
	}
	regs = append(regs, buildGatewayConsoleRegistrations(svc, apiKey)...)
	return regs, nil
}

func buildGatewayNotifyRegistration(svc *gatewayServices, apiKey string) gatewayHTTPRegistration {
	return gatewayHTTPRegistration{
		pattern: "/api/notify",
		handler: coregateway.NewNotifyHandler(coregateway.NotifyHandlerOptions{
			Sender:     svc.channelManager,
			APIKey:     apiKey,
			LastActive: gatewayLastActiveFunc(svc),
		}),
	}
}

func buildGatewayResumeRegistration(svc *gatewayServices, apiKey string) gatewayHTTPRegistration {
	return gatewayHTTPRegistration{
		pattern: "/api/resume_last_task",
		handler: coregateway.NewResumeLastTaskHandler(coregateway.ResumeLastTaskHandlerOptions{
			APIKey:  apiKey,
			Timeout: 2 * time.Minute,
			Resume: func(ctx context.Context) (any, string, error) {
				if svc.agentLoop == nil {
					return nil, "", fmt.Errorf("agent loop not available")
				}
				candidate, response, err := svc.agentLoop.ResumeLastTask(ctx)
				return candidate, response, err
			},
		}),
	}
}

func buildGatewaySessionModelRegistration(svc *gatewayServices, apiKey string) gatewayHTTPRegistration {
	return gatewayHTTPRegistration{
		pattern: "/api/session_model",
		handler: pkghttpapi.NewSessionModelHandler(pkghttpapi.SessionModelHandlerOptions{
			APIKey:       apiKey,
			Workspace:    svc.cfg.WorkspacePath(),
			Sessions:     gatewaySessionStore(svc),
			Enabled:      true,
			MaxBodyBytes: 8 << 10,
		}),
	}
}

func buildGatewayConsoleRegistrations(svc *gatewayServices, apiKey string) []gatewayHTTPRegistration {
	console := coregateway.NewConsoleHandler(coregateway.ConsoleHandlerOptions{
		Workspace:  svc.cfg.WorkspacePath(),
		APIKey:     apiKey,
		LastActive: gatewayLastActiveFunc(svc),
		Info:       gatewayConsoleInfo(svc),
	})
	return []gatewayHTTPRegistration{
		{pattern: "/console/", handler: console},
		{pattern: "/api/console/", handler: console},
	}
}

func gatewayLastActiveFunc(svc *gatewayServices) func() (string, string) {
	return func() (string, string) {
		if svc.agentLoop == nil {
			return "", ""
		}
		return svc.agentLoop.LastActive()
	}
}

func gatewaySessionStore(svc *gatewayServices) session.Store {
	if svc == nil || svc.agentLoop == nil {
		return nil
	}
	return svc.agentLoop.SessionStore()
}

func gatewayConsoleInfo(svc *gatewayServices) coregateway.ConsoleInfo {
	return coregateway.ConsoleInfo{
		Model:                      svc.cfg.Agents.Defaults.ModelName,
		NotifyOnTaskComplete:       svc.cfg.Notify.OnTaskComplete,
		ToolTraceEnabled:           svc.cfg.Tools.Trace.Enabled,
		RunTraceEnabled:            svc.cfg.Tools.Trace.Enabled,
		WebEvidenceMode:            svc.cfg.Tools.Web.Evidence.Enabled,
		InboundQueueEnabled:        svc.cfg.Gateway.InboundQueue.Enabled,
		InboundQueueMaxConcurrency: svc.cfg.Gateway.InboundQueue.MaxConcurrency,
	}
}
