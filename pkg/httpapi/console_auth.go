package httpapi

import (
	"net"
	"net/http"
	"strings"
)

func isLoopbackRemote(remoteAddr string) bool {
	host := strings.TrimSpace(remoteAddr)
	if host == "" {
		return false
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

func authorizeAPIKeyOrLoopback(apiKey string, r *http.Request) bool {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return isLoopbackRemote(r.RemoteAddr)
	}

	if strings.TrimSpace(r.Header.Get("X-API-Key")) == apiKey {
		return true
	}

	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(auth) > 7 && strings.EqualFold(auth[:7], "bearer ") {
		token := strings.TrimSpace(auth[7:])
		return token != "" && token == apiKey
	}

	return false
}
