package auth

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

func OpenAIOAuthConfig() OAuthProviderConfig {
	return OAuthProviderConfig{
		Issuer:     "https://auth.openai.com",
		ClientID:   "app_EMoamEEZ73f0CkXaXp7hrann",
		Scopes:     "openid profile email offline_access",
		Originator: "codex_cli_rs",
		Port:       1455,
	}
}

func GoogleAntigravityOAuthConfig() OAuthProviderConfig {
	clientID := decodeBase64("MTA3MTAwNjA2MDU5MS10bWhzc2luMmgyMWxjcmUyMzV2dG9sb2poNGc0MDNlcC5hcHBzLmdvb2dsZXVzZXJjb250ZW50LmNvbQ==")
	clientSecret := decodeBase64("R09DU1BYLUs1OEZXUjQ4NkxkTEoxbUxCOHNYQzR6NnFEQWY=")
	return OAuthProviderConfig{
		Issuer:       "https://accounts.google.com/o/oauth2/v2",
		TokenURL:     "https://oauth2.googleapis.com/token",
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Scopes:       "https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile https://www.googleapis.com/auth/cclog https://www.googleapis.com/auth/experimentsandconfigs",
		Port:         51121,
	}
}

func decodeBase64(s string) string {
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return s
	}
	return string(data)
}

func GenerateState() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func LoginBrowser(cfg OAuthProviderConfig) (*AuthCredential, error) {
	pkce, err := GeneratePKCE()
	if err != nil {
		return nil, fmt.Errorf("generating PKCE: %w", err)
	}

	state, err := GenerateState()
	if err != nil {
		return nil, fmt.Errorf("generating state: %w", err)
	}

	redirectURI := fmt.Sprintf("http://localhost:%d/auth/callback", cfg.Port)
	authURL := buildAuthorizeURL(cfg, pkce, state, redirectURI)
	resultCh := make(chan callbackResult, 1)

	server, err := startOAuthCallbackServer(cfg.Port, state, resultCh)
	if err != nil {
		return nil, err
	}
	defer shutdownOAuthCallbackServer(server)

	printOAuthBrowserInstructions(authURL, cfg.Port)

	code, err := waitForAuthorizationCode(resultCh, startManualAuthInput(os.Stdin), time.After(5*time.Minute))
	if err != nil {
		return nil, err
	}
	return ExchangeCodeForTokens(cfg, code, pkce.CodeVerifier, redirectURI)
}

func BuildAuthorizeURL(cfg OAuthProviderConfig, pkce PKCECodes, state, redirectURI string) string {
	return buildAuthorizeURL(cfg, pkce, state, redirectURI)
}

func buildAuthorizeURL(cfg OAuthProviderConfig, pkce PKCECodes, state, redirectURI string) string {
	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {cfg.ClientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {cfg.Scopes},
		"code_challenge":        {pkce.CodeChallenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
	}

	isGoogle := strings.Contains(strings.ToLower(cfg.Issuer), "accounts.google.com")
	if isGoogle {
		params.Set("access_type", "offline")
		params.Set("prompt", "consent")
	} else {
		params.Set("id_token_add_organizations", "true")
		params.Set("codex_cli_simplified_flow", "true")
		if strings.Contains(strings.ToLower(cfg.Issuer), "auth.openai.com") {
			params.Set("originator", "x-claw")
		}
		if cfg.Originator != "" {
			params.Set("originator", cfg.Originator)
		}
	}

	if isGoogle {
		return cfg.Issuer + "/auth?" + params.Encode()
	}
	return cfg.Issuer + "/oauth/authorize?" + params.Encode()
}

func OpenBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "linux":
		return exec.Command("xdg-open", url).Start()
	case "windows":
		return exec.Command("cmd", "/c", "start", url).Start()
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}
