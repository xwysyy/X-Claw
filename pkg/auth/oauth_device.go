package auth

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type deviceCodeResponse struct {
	DeviceAuthID string
	UserCode     string
	Interval     int
}

// DeviceCodeInfo holds the device code information returned by the OAuth provider.
type DeviceCodeInfo struct {
	DeviceAuthID string `json:"device_auth_id"`
	UserCode     string `json:"user_code"`
	VerifyURL    string `json:"verify_url"`
	Interval     int    `json:"interval"`
}

// RequestDeviceCode requests a device code from the OAuth provider.
// Returns the info needed for the user to authenticate in a browser.
func RequestDeviceCode(cfg OAuthProviderConfig) (*DeviceCodeInfo, error) {
	return requestDeviceCodeInfo(cfg)
}

func requestDeviceCodeInfo(cfg OAuthProviderConfig) (*DeviceCodeInfo, error) {
	reqBody, _ := json.Marshal(map[string]string{
		"client_id": cfg.ClientID,
	})

	resp, err := http.Post(
		cfg.Issuer+"/api/accounts/deviceauth/usercode",
		"application/json",
		strings.NewReader(string(reqBody)),
	)
	if err != nil {
		return nil, fmt.Errorf("requesting device code: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device code request failed: %s", string(body))
	}

	deviceResp, err := parseDeviceCodeResponse(body)
	if err != nil {
		return nil, fmt.Errorf("parsing device code response: %w", err)
	}

	return buildDeviceCodeInfo(cfg, deviceResp), nil
}

func buildDeviceCodeInfo(cfg OAuthProviderConfig, deviceResp deviceCodeResponse) *DeviceCodeInfo {
	if deviceResp.Interval < 1 {
		deviceResp.Interval = 5
	}
	return &DeviceCodeInfo{
		DeviceAuthID: deviceResp.DeviceAuthID,
		UserCode:     deviceResp.UserCode,
		VerifyURL:    cfg.Issuer + "/codex/device",
		Interval:     deviceResp.Interval,
	}
}

// PollDeviceCodeOnce makes a single poll attempt to check if the user has authenticated.
// Returns (credential, nil) on success, (nil, nil) if still pending, or (nil, err) on failure.
func PollDeviceCodeOnce(cfg OAuthProviderConfig, deviceAuthID, userCode string) (*AuthCredential, error) {
	return pollDeviceCode(cfg, deviceAuthID, userCode)
}

func parseDeviceCodeResponse(body []byte) (deviceCodeResponse, error) {
	var raw struct {
		DeviceAuthID string          `json:"device_auth_id"`
		UserCode     string          `json:"user_code"`
		Interval     json.RawMessage `json:"interval"`
	}

	if err := json.Unmarshal(body, &raw); err != nil {
		return deviceCodeResponse{}, err
	}

	interval, err := parseFlexibleInt(raw.Interval)
	if err != nil {
		return deviceCodeResponse{}, err
	}

	return deviceCodeResponse{
		DeviceAuthID: raw.DeviceAuthID,
		UserCode:     raw.UserCode,
		Interval:     interval,
	}, nil
}

func parseFlexibleInt(raw json.RawMessage) (int, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, nil
	}

	var interval int
	if err := json.Unmarshal(raw, &interval); err == nil {
		return interval, nil
	}

	var intervalStr string
	if err := json.Unmarshal(raw, &intervalStr); err == nil {
		intervalStr = strings.TrimSpace(intervalStr)
		if intervalStr == "" {
			return 0, nil
		}
		interval, convErr := strconv.Atoi(intervalStr)
		if convErr != nil {
			return 0, fmt.Errorf("invalid integer value: %s", string(raw))
		}
		return interval, nil
	}

	return 0, fmt.Errorf("invalid integer value: %s", string(raw))
}

func LoginDeviceCode(cfg OAuthProviderConfig) (*AuthCredential, error) {
	deviceInfo, err := requestDeviceCodeInfo(cfg)
	if err != nil {
		return nil, err
	}

	fmt.Printf(
		"\nTo authenticate, open this URL in your browser:\n\n  %s\n\nThen enter this code: %s\n\nWaiting for authentication...\n",
		deviceInfo.VerifyURL,
		deviceInfo.UserCode,
	)

	deadline := time.After(15 * time.Minute)
	ticker := time.NewTicker(time.Duration(deviceInfo.Interval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			return nil, fmt.Errorf("device code authentication timed out after 15 minutes")
		case <-ticker.C:
			cred, err := pollDeviceCode(cfg, deviceInfo.DeviceAuthID, deviceInfo.UserCode)
			if err != nil {
				continue
			}
			if cred != nil {
				return cred, nil
			}
		}
	}
}

func pollDeviceCode(cfg OAuthProviderConfig, deviceAuthID, userCode string) (*AuthCredential, error) {
	reqBody, _ := json.Marshal(map[string]string{
		"device_auth_id": deviceAuthID,
		"user_code":      userCode,
	})

	resp, err := http.Post(
		cfg.Issuer+"/api/accounts/deviceauth/token",
		"application/json",
		strings.NewReader(string(reqBody)),
	)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pending")
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	var tokenResp struct {
		AuthorizationCode string `json:"authorization_code"`
		CodeChallenge     string `json:"code_challenge"`
		CodeVerifier      string `json:"code_verifier"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, err
	}

	redirectURI := cfg.Issuer + "/deviceauth/callback"
	return ExchangeCodeForTokens(cfg, tokenResp.AuthorizationCode, tokenResp.CodeVerifier, redirectURI)
}
