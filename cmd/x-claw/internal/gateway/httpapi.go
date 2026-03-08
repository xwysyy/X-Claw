package gateway

import (
	"fmt"
	"net/http"
)

type gatewayHTTPRegistration struct {
	pattern string
	handler http.Handler
}

func registerGatewayHTTPAPI(svc *gatewayServices) error {
	if svc == nil || svc.channelManager == nil {
		return nil
	}

	regs, err := buildGatewayHTTPRegistrations(svc)
	if err != nil {
		return err
	}
	for _, reg := range regs {
		if err := svc.channelManager.RegisterHTTPHandler(reg.pattern, reg.handler); err != nil {
			return fmt.Errorf("register %s: %w", reg.pattern, err)
		}
	}
	return nil
}
