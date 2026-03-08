package providers

import (
	"fmt"
	"strings"
)

func missingOAuthCredentialError(provider string) error {
	provider = strings.TrimSpace(provider)
	return fmt.Errorf(
		"no credentials for %s. Configure `api_key` in config, or populate the local auth store before using oauth/token auth",
		provider,
	)
}

func expiredOAuthCredentialError(provider string) error {
	provider = strings.TrimSpace(provider)
	return fmt.Errorf(
		"%s credentials expired. Refresh the local auth store credential and retry",
		provider,
	)
}

func createClaudeAuthProvider() (LLMProvider, error) {
	cred, err := getCredential("anthropic")
	if err != nil {
		return nil, fmt.Errorf("loading auth credentials: %w", err)
	}
	if cred == nil {
		return nil, missingOAuthCredentialError("anthropic")
	}
	return NewClaudeProviderWithTokenSource(cred.AccessToken, createClaudeTokenSource()), nil
}

func createCodexAuthProvider() (LLMProvider, error) {
	cred, err := getCredential("openai")
	if err != nil {
		return nil, fmt.Errorf("loading auth credentials: %w", err)
	}
	if cred == nil {
		return nil, missingOAuthCredentialError("openai")
	}
	return NewCodexProviderWithTokenSource(cred.AccessToken, cred.AccountID, createCodexTokenSource()), nil
}
