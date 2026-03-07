package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/xwysyy/X-Claw/pkg/logger"
	"github.com/xwysyy/X-Claw/pkg/utils"
)

type BraveSearchProvider struct {
	keys  *apiKeyPool
	proxy string
}

func (p *BraveSearchProvider) Search(ctx context.Context, query string, count int) (SearchProviderResult, error) {
	apiKey, keyID, ok := p.keys.Next()
	if !ok {
		return SearchProviderResult{}, fmt.Errorf("brave api key not configured")
	}
	searchURL := fmt.Sprintf("https://api.search.brave.com/res/v1/web/search?q=%s&count=%d",
		url.QueryEscape(query), count)

	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return SearchProviderResult{}, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", apiKey)

	client, err := createHTTPClient(p.proxy, 10*time.Second)
	if err != nil {
		return SearchProviderResult{}, fmt.Errorf("failed to create HTTP client: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return SearchProviderResult{}, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return SearchProviderResult{}, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return SearchProviderResult{}, fmt.Errorf("brave api error (status %d): %s", resp.StatusCode, string(body))
	}

	var searchResp struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}

	if err := json.Unmarshal(body, &searchResp); err != nil {
		// Log error body for debugging
		logger.DebugCF("tools/web_search", "Brave API error body", map[string]any{
			"body": utils.Truncate(string(body), 500),
		})
		return SearchProviderResult{}, fmt.Errorf("failed to parse response: %w", err)
	}

	results := searchResp.Web.Results
	if len(results) == 0 {
		return SearchProviderResult{Text: fmt.Sprintf("No results for: %s", query), KeyID: keyID}, nil
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("Results for: %s", query))
	for i, item := range results {
		if i >= count {
			break
		}
		lines = append(lines, fmt.Sprintf("%d. %s\n   %s", i+1, item.Title, item.URL))
		if item.Description != "" {
			lines = append(lines, fmt.Sprintf("   %s", item.Description))
		}
	}

	return SearchProviderResult{Text: strings.Join(lines, "\n"), KeyID: keyID}, nil
}
