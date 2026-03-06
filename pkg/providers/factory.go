package providers

import (
	"github.com/xwysyy/X-Claw/pkg/auth"
	"github.com/xwysyy/X-Claw/pkg/config"
)

const defaultAnthropicAPIBase = "https://api.anthropic.com/v1"

var getCredential = auth.GetCredential

type providerType int

const (
	providerTypeHTTPCompat providerType = iota
	providerTypeClaudeAuth
	providerTypeCodexAuth
	providerTypeCodexCLIToken
	providerTypeClaudeCLI
	providerTypeCodexCLI
	providerTypeGitHubCopilot
)

type providerSelection struct {
	providerType    providerType
	apiKey          string
	apiBase         string
	proxy           string
	model           string
	workspace       string
	connectMode     string
	enableWebSearch bool
}

// applyProviderConfig copies the standard provider config fields into the selection.
// If the resolved apiBase is empty, defaultBase is used as fallback.
func applyProviderConfig(sel *providerSelection, pc config.ProviderConfig, defaultBase string) error {
	key := ""
	if pc.APIKey.Present() {
		v, err := pc.APIKey.Resolve("")
		if err != nil {
			return err
		}
		key = v
	}
	sel.apiKey = key
	sel.apiBase = pc.APIBase
	sel.proxy = pc.Proxy
	if sel.apiBase == "" && defaultBase != "" {
		sel.apiBase = defaultBase
	}
	return nil
}
