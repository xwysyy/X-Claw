package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type PerplexitySearchProvider struct {
	apiKey string
	proxy  string
}

func (p *PerplexitySearchProvider) Search(ctx context.Context, query string, count int) (SearchProviderResult, error) {
	apiKey := strings.TrimSpace(p.apiKey)
	if apiKey == "" {
		return SearchProviderResult{}, fmt.Errorf("perplexity api key not configured")
	}
	keyID := makeKeyID(apiKey)

	searchURL := "https://api.perplexity.ai/chat/completions"
	payload := map[string]any{
		"model": "sonar",
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "You are a search assistant. Provide concise search results with titles, URLs, and brief descriptions in the following format:\n1. Title\n   URL\n   Description\n\nDo not add extra commentary.",
			},
			{
				"role":    "user",
				"content": fmt.Sprintf("Search for: %s. Provide up to %d relevant results.", query, count),
			},
		},
		"max_tokens": 1000,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return SearchProviderResult{}, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", searchURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return SearchProviderResult{}, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("User-Agent", userAgent)

	client, err := createHTTPClient(p.proxy, perplexityTimeout)
	if err != nil {
		return SearchProviderResult{}, fmt.Errorf("failed to create HTTP client: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return SearchProviderResult{}, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return SearchProviderResult{}, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return SearchProviderResult{}, fmt.Errorf("perplexity api error (status %d): %s", resp.StatusCode, string(body))
	}

	var searchResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &searchResp); err != nil {
		return SearchProviderResult{}, fmt.Errorf("failed to parse response: %w", err)
	}
	if len(searchResp.Choices) == 0 {
		return SearchProviderResult{Text: fmt.Sprintf("No results for: %s", query), KeyID: keyID}, nil
	}

	content := strings.TrimSpace(searchResp.Choices[0].Message.Content)
	if content == "" {
		return SearchProviderResult{Text: fmt.Sprintf("No results for: %s", query), KeyID: keyID}, nil
	}
	return SearchProviderResult{
		Text:  fmt.Sprintf("Results for: %s (via Perplexity)\n%s", query, content),
		KeyID: keyID,
	}, nil
}

type GLMSearchProvider struct {
	apiKey       string
	baseURL      string
	searchEngine string
	proxy        string
}

func (p *GLMSearchProvider) Search(ctx context.Context, query string, count int) (SearchProviderResult, error) {
	apiKey := strings.TrimSpace(p.apiKey)
	if apiKey == "" {
		return SearchProviderResult{}, fmt.Errorf("glm_search api key not configured")
	}
	keyID := makeKeyID(apiKey)

	searchURL := strings.TrimSpace(p.baseURL)
	if searchURL == "" {
		searchURL = "https://open.bigmodel.cn/api/paas/v4/web_search"
	}
	searchEngine := strings.TrimSpace(p.searchEngine)
	if searchEngine == "" {
		searchEngine = "search_std"
	}

	payload := map[string]any{
		"search_query":  query,
		"search_engine": searchEngine,
		"search_intent": false,
		"count":         count,
		"content_size":  "medium",
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return SearchProviderResult{}, fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", searchURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return SearchProviderResult{}, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client, err := createHTTPClient(p.proxy, glmSearchTimeout)
	if err != nil {
		return SearchProviderResult{}, fmt.Errorf("failed to create HTTP client: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return SearchProviderResult{}, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return SearchProviderResult{}, fmt.Errorf("failed to read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return SearchProviderResult{}, fmt.Errorf("glm_search api error (status %d): %s", resp.StatusCode, string(body))
	}

	var searchResp struct {
		SearchResult []struct {
			Title   string `json:"title"`
			Content string `json:"content"`
			Link    string `json:"link"`
		} `json:"search_result"`
	}
	if err := json.Unmarshal(body, &searchResp); err != nil {
		return SearchProviderResult{}, fmt.Errorf("failed to parse response: %w", err)
	}
	results := searchResp.SearchResult
	if len(results) == 0 {
		return SearchProviderResult{Text: fmt.Sprintf("No results for: %s", query), KeyID: keyID}, nil
	}

	lines := make([]string, 0, 2+count*2)
	lines = append(lines, fmt.Sprintf("Results for: %s (via GLM Search)", query))
	for i, item := range results {
		if i >= count {
			break
		}
		lines = append(lines, fmt.Sprintf("%d. %s\n   %s", i+1, item.Title, item.Link))
		if strings.TrimSpace(item.Content) != "" {
			lines = append(lines, fmt.Sprintf("   %s", item.Content))
		}
	}

	return SearchProviderResult{Text: strings.Join(lines, "\n"), KeyID: keyID}, nil
}

type GrokSearchProvider struct {
	keys     *apiKeyPool
	endpoint string
	model    string
	proxy    string
}

func (p *GrokSearchProvider) Search(ctx context.Context, query string, count int) (SearchProviderResult, error) {
	apiKey, keyID, ok := p.keys.Next()
	if !ok {
		return SearchProviderResult{}, fmt.Errorf("grok api key not configured")
	}
	searchURL := strings.TrimSpace(p.endpoint)
	if searchURL == "" {
		searchURL = "https://api.x.ai/v1/chat/completions"
	}
	model := strings.TrimSpace(p.model)
	if model == "" {
		model = "grok-4"
	}

	payload := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "You are a search assistant. Provide concise search results with titles, URLs, and brief descriptions in the following format:\n1. Title\n   URL\n   Description\n\nDo not add extra commentary.",
			},
			{
				"role":    "user",
				"content": fmt.Sprintf("Search for: %s. Provide up to %d relevant results.", query, count),
			},
		},
		"max_tokens": 1000,
		"stream":     false,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return SearchProviderResult{}, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", searchURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return SearchProviderResult{}, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("User-Agent", userAgent)

	client, err := createHTTPClient(p.proxy, 30*time.Second)
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
		return SearchProviderResult{}, fmt.Errorf("Grok API error: %s", string(body))
	}

	content, err := parseGrokResponseContent(body)
	if err != nil {
		return SearchProviderResult{}, fmt.Errorf("failed to parse response: %w", err)
	}
	if strings.TrimSpace(content) == "" {
		return SearchProviderResult{Text: fmt.Sprintf("No results for: %s", query), KeyID: keyID}, nil
	}

	return SearchProviderResult{Text: fmt.Sprintf("Results for: %s (via Grok)\n%s", query, content), KeyID: keyID}, nil
}

func parseGrokResponseContent(body []byte) (string, error) {
	// First try regular JSON completion response.
	var searchResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &searchResp); err == nil {
		if len(searchResp.Choices) > 0 {
			return strings.TrimSpace(searchResp.Choices[0].Message.Content), nil
		}
	}

	// Some OpenAI-compatible gateways return SSE chunks even when stream=false.
	// Parse lines in "data: {...}" format and stitch delta content.
	text := strings.TrimSpace(string(body))
	if !strings.Contains(text, "data:") {
		return "", fmt.Errorf("unexpected response format")
	}

	var merged strings.Builder
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		chunk := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if chunk == "" || chunk == "[DONE]" {
			continue
		}

		var sseChunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(chunk), &sseChunk); err != nil {
			continue
		}
		if len(sseChunk.Choices) == 0 {
			continue
		}

		part := strings.TrimSpace(sseChunk.Choices[0].Delta.Content)
		if part == "" {
			part = strings.TrimSpace(sseChunk.Choices[0].Message.Content)
		}
		if part == "" {
			continue
		}
		if merged.Len() > 0 {
			merged.WriteByte(' ')
		}
		merged.WriteString(part)
	}

	if merged.Len() == 0 {
		return "", fmt.Errorf("empty content in SSE response")
	}
	return merged.String(), nil
}
