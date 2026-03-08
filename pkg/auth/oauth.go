package auth

type OAuthProviderConfig struct {
	Issuer       string
	ClientID     string
	ClientSecret string // Required for Google OAuth (confidential client)
	TokenURL     string // Override token endpoint (Google uses a different URL than issuer)
	Scopes       string
	Originator   string
	Port         int
}
