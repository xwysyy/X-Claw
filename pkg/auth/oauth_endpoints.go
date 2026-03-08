package auth

import "strings"

func resolveOAuthTokenURL(cfg OAuthProviderConfig) string {
	if cfg.TokenURL != "" {
		return cfg.TokenURL
	}
	return cfg.Issuer + "/oauth/token"
}

func resolveOAuthProvider(cfg OAuthProviderConfig) string {
	if cfg.TokenURL != "" && strings.Contains(cfg.TokenURL, "googleapis.com") {
		return "google-antigravity"
	}
	return "openai"
}

func carryForwardRefreshCredential(refreshed, original *AuthCredential) *AuthCredential {
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = original.RefreshToken
	}
	if refreshed.AccountID == "" {
		refreshed.AccountID = original.AccountID
	}
	if original.Email != "" && refreshed.Email == "" {
		refreshed.Email = original.Email
	}
	if original.ProjectID != "" && refreshed.ProjectID == "" {
		refreshed.ProjectID = original.ProjectID
	}
	return refreshed
}
