package providers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/xwysyy/X-Claw/pkg/auth"
	"github.com/xwysyy/X-Claw/pkg/logger"
)

// --- Token source ---

func createAntigravityTokenSource() func() (string, string, error) {
	return func() (string, string, error) {
		cred, err := auth.GetCredential("google-antigravity")
		if err != nil {
			return "", "", fmt.Errorf("loading auth credentials: %w", err)
		}
		if cred == nil {
			return "", "", missingOAuthCredentialError("google-antigravity")
		}

		// Refresh if needed
		if cred.NeedsRefresh() && cred.RefreshToken != "" {
			oauthCfg := auth.GoogleAntigravityOAuthConfig()
			refreshed, err := auth.RefreshAccessToken(cred, oauthCfg)
			if err != nil {
				return "", "", fmt.Errorf("refreshing token: %w", err)
			}
			refreshed.Email = cred.Email
			if refreshed.ProjectID == "" {
				refreshed.ProjectID = cred.ProjectID
			}
			if err := auth.SetCredential("google-antigravity", refreshed); err != nil {
				return "", "", fmt.Errorf("saving refreshed token: %w", err)
			}
			cred = refreshed
		}

		if cred.IsExpired() {
			return "", "", expiredOAuthCredentialError("google-antigravity")
		}

		projectID := cred.ProjectID
		if projectID == "" {
			// Try to fetch project ID from API
			fetchedID, err := FetchAntigravityProjectID(cred.AccessToken)
			if err != nil {
				logger.WarnCF("provider.antigravity", "Could not fetch project ID, using fallback", map[string]any{
					"error": err.Error(),
				})
				projectID = "rising-fact-p41fc" // Default fallback (same as OpenCode)
			} else {
				projectID = fetchedID
				cred.ProjectID = projectID
				if err := auth.SetCredential("google-antigravity", cred); err != nil {
					logger.WarnCF("provider.antigravity", "Failed to save credential", map[string]any{
						"error": err.Error(),
					})
				}
			}
		}

		return cred.AccessToken, projectID, nil
	}
}

// FetchAntigravityProjectID retrieves the Google Cloud project ID from the loadCodeAssist endpoint.
func FetchAntigravityProjectID(accessToken string) (string, error) {
	reqBody, _ := json.Marshal(map[string]any{
		"metadata": map[string]any{
			"ideType":    "IDE_UNSPECIFIED",
			"platform":   "PLATFORM_UNSPECIFIED",
			"pluginType": "GEMINI",
		},
	})

	req, err := http.NewRequest("POST", antigravityBaseURL+"/v1internal:loadCodeAssist", bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", antigravityUserAgent)
	req.Header.Set("X-Goog-Api-Client", antigravityXGoogClient)

	resp, err := antigravityFetchClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("loadCodeAssist failed: %s", string(body))
	}

	var result struct {
		CloudAICompanionProject string `json:"cloudaicompanionProject"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}

	if result.CloudAICompanionProject == "" {
		return "", fmt.Errorf("no project ID in loadCodeAssist response")
	}

	return result.CloudAICompanionProject, nil
}

// FetchAntigravityModels fetches available models from the Cloud Code Assist API.
func FetchAntigravityModels(accessToken, projectID string) ([]AntigravityModelInfo, error) {
	reqBody, _ := json.Marshal(map[string]any{
		"project": projectID,
	})

	req, err := http.NewRequest("POST", antigravityBaseURL+"/v1internal:fetchAvailableModels", bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", antigravityUserAgent)
	req.Header.Set("X-Goog-Api-Client", antigravityXGoogClient)

	resp, err := antigravityFetchClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"fetchAvailableModels failed (HTTP %d): %s",
			resp.StatusCode,
			truncateString(string(body), 200),
		)
	}

	var result struct {
		Models map[string]struct {
			DisplayName string `json:"displayName"`
			QuotaInfo   struct {
				RemainingFraction any    `json:"remainingFraction"`
				ResetTime         string `json:"resetTime"`
				IsExhausted       bool   `json:"isExhausted"`
			} `json:"quotaInfo"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parsing models response: %w", err)
	}

	var models []AntigravityModelInfo
	for id, info := range result.Models {
		models = append(models, AntigravityModelInfo{
			ID:          id,
			DisplayName: info.DisplayName,
			IsExhausted: info.QuotaInfo.IsExhausted,
		})
	}

	// Ensure gemini-3-flash-preview and gemini-3-flash are in the list if they aren't already
	hasFlashPreview := false
	hasFlash := false
	for _, m := range models {
		if m.ID == "gemini-3-flash-preview" {
			hasFlashPreview = true
		}
		if m.ID == "gemini-3-flash" {
			hasFlash = true
		}
	}
	if !hasFlashPreview {
		models = append(models, AntigravityModelInfo{
			ID:          "gemini-3-flash-preview",
			DisplayName: "Gemini 3 Flash (Preview)",
		})
	}
	if !hasFlash {
		models = append(models, AntigravityModelInfo{
			ID:          "gemini-3-flash",
			DisplayName: "Gemini 3 Flash",
		})
	}

	return models, nil
}
