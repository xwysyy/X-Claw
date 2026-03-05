package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
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
		fmt.Printf("Brave API Error Body: %s\n", string(body))
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

type TavilySearchProvider struct {
	keys    *apiKeyPool
	baseURL string
	proxy   string
}

func (p *TavilySearchProvider) Search(ctx context.Context, query string, count int) (SearchProviderResult, error) {
	apiKey, keyID, ok := p.keys.Next()
	if !ok {
		return SearchProviderResult{}, fmt.Errorf("tavily api key not configured")
	}
	searchURL := p.baseURL
	if searchURL == "" {
		searchURL = "https://api.tavily.com/search"
	}

	payload := map[string]any{
		"api_key":             apiKey,
		"query":               query,
		"search_depth":        "advanced",
		"include_answer":      false,
		"include_images":      false,
		"include_raw_content": false,
		"max_results":         count,
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return SearchProviderResult{}, fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", searchURL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return SearchProviderResult{}, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)

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
		return SearchProviderResult{}, fmt.Errorf("tavily api error (status %d): %s", resp.StatusCode, string(body))
	}

	var searchResp struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}

	if err := json.Unmarshal(body, &searchResp); err != nil {
		return SearchProviderResult{}, fmt.Errorf("failed to parse response: %w", err)
	}

	results := searchResp.Results
	if len(results) == 0 {
		return SearchProviderResult{Text: fmt.Sprintf("No results for: %s", query), KeyID: keyID}, nil
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("Results for: %s (via Tavily)", query))
	for i, item := range results {
		if i >= count {
			break
		}
		lines = append(lines, fmt.Sprintf("%d. %s\n   %s", i+1, item.Title, item.URL))
		if item.Content != "" {
			lines = append(lines, fmt.Sprintf("   %s", item.Content))
		}
	}

	return SearchProviderResult{Text: strings.Join(lines, "\n"), KeyID: keyID}, nil
}

type DuckDuckGoSearchProvider struct {
	proxy string
}

func (p *DuckDuckGoSearchProvider) Search(ctx context.Context, query string, count int) (SearchProviderResult, error) {
	searchURL := fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s", url.QueryEscape(query))

	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return SearchProviderResult{}, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", userAgent)

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

	text, err := p.extractResults(string(body), count, query)
	if err != nil {
		return SearchProviderResult{}, err
	}
	return SearchProviderResult{Text: text}, nil
}

func (p *DuckDuckGoSearchProvider) extractResults(html string, count int, query string) (string, error) {
	// Simple regex based extraction for DDG HTML
	// Strategy: Find all result containers or key anchors directly

	// Try finding the result links directly first, as they are the most critical
	// Pattern: <a class="result__a" href="...">Title</a>
	// The previous regex was a bit strict. Let's make it more flexible for attributes order/content
	matches := reDDGLink.FindAllStringSubmatch(html, count+5)

	if len(matches) == 0 {
		return fmt.Sprintf("No results found or extraction failed. Query: %s", query), nil
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("Results for: %s (via DuckDuckGo)", query))

	// Pre-compile snippet regex to run inside the loop
	// We'll search for snippets relative to the link position or just globally if needed
	// But simple global search for snippets might mismatch order.
	// Since we only have the raw HTML string, let's just extract snippets globally and assume order matches (risky but simple for regex)
	// Or better: Let's assume the snippet follows the link in the HTML

	// A better regex approach: iterate through text and find matches in order
	// But for now, let's grab all snippets too
	snippetMatches := reDDGSnippet.FindAllStringSubmatch(html, count+5)

	maxItems := min(len(matches), count)

	for i := 0; i < maxItems; i++ {
		urlStr := matches[i][1]
		title := stripTags(matches[i][2])
		title = strings.TrimSpace(title)

		// URL decoding if needed
		if strings.Contains(urlStr, "uddg=") {
			if u, err := url.QueryUnescape(urlStr); err == nil {
				idx := strings.Index(u, "uddg=")
				if idx != -1 {
					urlStr = u[idx+5:]
				}
			}
		}

		lines = append(lines, fmt.Sprintf("%d. %s\n   %s", i+1, title, urlStr))

		// Attempt to attach snippet if available and index aligns
		if i < len(snippetMatches) {
			snippet := stripTags(snippetMatches[i][1])
			snippet = strings.TrimSpace(snippet)
			if snippet != "" {
				lines = append(lines, fmt.Sprintf("   %s", snippet))
			}
		}
	}

	return strings.Join(lines, "\n"), nil
}

func stripTags(content string) string {
	return reTags.ReplaceAllString(content, "")
}

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

type WebSearchTool struct {
	provider      SearchProvider
	providerName  string
	secondary     SearchProvider
	secondaryName string

	maxResults int

	evidenceMode      bool
	evidenceMinDomain int
}

type WebSearchToolOptions struct {
	BraveAPIKey          string
	BraveAPIKeys         []string
	BraveMaxResults      int
	BraveEnabled         bool
	TavilyAPIKey         string
	TavilyAPIKeys        []string
	TavilyBaseURL        string
	TavilyMaxResults     int
	TavilyEnabled        bool
	DuckDuckGoMaxResults int
	DuckDuckGoEnabled    bool
	PerplexityAPIKey     string
	PerplexityMaxResults int
	PerplexityEnabled    bool
	GLMSearchAPIKey      string
	GLMSearchBaseURL     string
	GLMSearchEngine      string
	GLMSearchMaxResults  int
	GLMSearchEnabled     bool
	GrokAPIKey           string
	GrokAPIKeys          []string
	GrokEndpoint         string
	GrokModel            string
	GrokMaxResults       int
	GrokEnabled          bool
	Proxy                string

	EvidenceModeEnabled bool
	EvidenceMinDomains  int
}

func NewWebSearchTool(opts WebSearchToolOptions) *WebSearchTool {
	type candidate struct {
		name       string
		provider   SearchProvider
		maxResults int
	}

	maxResults := 5
	candidates := make([]candidate, 0, 6)

	// Priority: Perplexity > Grok > Brave > Tavily > DuckDuckGo > GLM Search
	if opts.PerplexityEnabled && strings.TrimSpace(opts.PerplexityAPIKey) != "" {
		mr := maxResults
		if opts.PerplexityMaxResults > 0 {
			mr = opts.PerplexityMaxResults
		}
		candidates = append(candidates, candidate{
			name: "perplexity",
			provider: &PerplexitySearchProvider{
				apiKey: opts.PerplexityAPIKey,
				proxy:  opts.Proxy,
			},
			maxResults: mr,
		})
	}

	if opts.GrokEnabled {
		if pool := newAPIKeyPool(opts.GrokAPIKey, opts.GrokAPIKeys); pool != nil {
			mr := maxResults
			if opts.GrokMaxResults > 0 {
				mr = opts.GrokMaxResults
			}
			candidates = append(candidates, candidate{
				name: "grok",
				provider: &GrokSearchProvider{
					keys:     pool,
					endpoint: opts.GrokEndpoint,
					model:    opts.GrokModel,
					proxy:    opts.Proxy,
				},
				maxResults: mr,
			})
		}
	}

	if opts.BraveEnabled {
		if pool := newAPIKeyPool(opts.BraveAPIKey, opts.BraveAPIKeys); pool != nil {
			mr := maxResults
			if opts.BraveMaxResults > 0 {
				mr = opts.BraveMaxResults
			}
			candidates = append(candidates, candidate{
				name:       "brave",
				provider:   &BraveSearchProvider{keys: pool, proxy: opts.Proxy},
				maxResults: mr,
			})
		}
	}

	if opts.TavilyEnabled {
		if pool := newAPIKeyPool(opts.TavilyAPIKey, opts.TavilyAPIKeys); pool != nil {
			mr := maxResults
			if opts.TavilyMaxResults > 0 {
				mr = opts.TavilyMaxResults
			}
			candidates = append(candidates, candidate{
				name: "tavily",
				provider: &TavilySearchProvider{
					keys:    pool,
					baseURL: opts.TavilyBaseURL,
					proxy:   opts.Proxy,
				},
				maxResults: mr,
			})
		}
	}

	if opts.DuckDuckGoEnabled {
		mr := maxResults
		if opts.DuckDuckGoMaxResults > 0 {
			mr = opts.DuckDuckGoMaxResults
		}
		candidates = append(candidates, candidate{
			name:       "duckduckgo",
			provider:   &DuckDuckGoSearchProvider{proxy: opts.Proxy},
			maxResults: mr,
		})
	}

	if opts.GLMSearchEnabled && strings.TrimSpace(opts.GLMSearchAPIKey) != "" {
		mr := maxResults
		if opts.GLMSearchMaxResults > 0 {
			mr = opts.GLMSearchMaxResults
		}
		candidates = append(candidates, candidate{
			name: "glm_search",
			provider: &GLMSearchProvider{
				apiKey:       opts.GLMSearchAPIKey,
				baseURL:      opts.GLMSearchBaseURL,
				searchEngine: opts.GLMSearchEngine,
				proxy:        opts.Proxy,
			},
			maxResults: mr,
		})
	}

	if len(candidates) == 0 {
		return nil
	}

	primary := candidates[0]
	secondary := candidate{}
	hasSecondary := len(candidates) > 1
	if hasSecondary {
		secondary = candidates[1]
	}

	minDomains := opts.EvidenceMinDomains
	if minDomains <= 0 {
		minDomains = 2
	}

	tool := &WebSearchTool{
		provider:          primary.provider,
		providerName:      primary.name,
		secondary:         nil,
		secondaryName:     "",
		maxResults:        primary.maxResults,
		evidenceMode:      opts.EvidenceModeEnabled,
		evidenceMinDomain: minDomains,
	}
	if tool.evidenceMode && hasSecondary {
		tool.secondary = secondary.provider
		tool.secondaryName = secondary.name
		// Use the smaller max results across providers as a safe default.
		if secondary.maxResults > 0 && secondary.maxResults < tool.maxResults {
			tool.maxResults = secondary.maxResults
		}
	}

	return tool
}

type WebSearchDualTool struct {
	base *WebSearchTool
}

func NewWebSearchDualTool(opts WebSearchToolOptions) *WebSearchDualTool {
	opts.EvidenceModeEnabled = true
	tool := NewWebSearchTool(opts)
	if tool == nil {
		return nil
	}
	return &WebSearchDualTool{base: tool}
}

func (t *WebSearchDualTool) Name() string {
	return "web_search_dual"
}

func (t *WebSearchDualTool) ParallelPolicy() ToolParallelPolicy {
	return ToolParallelReadOnly
}

func (t *WebSearchDualTool) Description() string {
	return "Search the web using two providers in parallel (when available) and return a structured JSON payload. " +
		"Input: query (string, required), count (integer, optional, 1-10, default 5). " +
		"Output: JSON including per-provider status, extracted sources, and an evidence summary."
}

func (t *WebSearchDualTool) Parameters() map[string]any {
	if t == nil || t.base == nil {
		return map[string]any{"type": "object"}
	}
	return t.base.Parameters()
}

func (t *WebSearchDualTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if t == nil || t.base == nil {
		return ErrorResult("web_search_dual tool is not configured")
	}
	return t.base.Execute(ctx, args)
}

func (t *WebSearchTool) Name() string {
	return "web_search"
}

func (t *WebSearchTool) ParallelPolicy() ToolParallelPolicy {
	return ToolParallelReadOnly
}

func (t *WebSearchTool) Description() string {
	desc := "Search the web for current information. " +
		"Input: query (string, required), count (integer, optional, 1-10, default 5). " +
		"Output: list of results with title, URL, and snippet for each. " +
		"Use this for questions about current events, recent data, or facts you are unsure about. " +
		"For reading a specific URL after searching, use the 'web_fetch' tool."
	if t != nil && t.evidenceMode {
		desc += " Evidence mode is enabled: the tool returns structured JSON with extracted sources and an evidence summary."
	}
	return desc
}

func (t *WebSearchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Search query",
			},
			"count": map[string]any{
				"type":        "integer",
				"description": "Number of results (1-10)",
				"minimum":     1.0,
				"maximum":     10.0,
			},
		},
		"required": []string{"query"},
	}
}

func (t *WebSearchTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	query, ok := args["query"].(string)
	if !ok {
		return ErrorResult("query is required")
	}

	count := t.maxResults
	if c, ok := args["count"].(float64); ok {
		if int(c) > 0 && int(c) <= 10 {
			count = int(c)
		}
	}

	if t == nil || t.provider == nil {
		return ErrorResult("web_search tool is not configured")
	}

	if !t.evidenceMode {
		result, err := t.provider.Search(ctx, query, count)
		if err != nil {
			return ErrorResult(fmt.Sprintf("search failed: %v", err))
		}

		return &ToolResult{
			ForLLM:  result.Text,
			ForUser: result.Text,
		}
	}

	type evidenceSource struct {
		URL      string `json:"url"`
		Domain   string `json:"domain,omitempty"`
		Provider string `json:"provider,omitempty"`
	}
	type providerEvidence struct {
		Name        string           `json:"name"`
		KeyID       string           `json:"key_id,omitempty"`
		OK          bool             `json:"ok"`
		Error       string           `json:"error,omitempty"`
		SourceCount int              `json:"source_count,omitempty"`
		Sources     []evidenceSource `json:"sources,omitempty"`
	}
	type evidenceSummary struct {
		Enabled         bool `json:"enabled"`
		MinDomains      int  `json:"min_domains"`
		DistinctDomains int  `json:"distinct_domains"`
		Satisfied       bool `json:"satisfied"`
	}
	type payload struct {
		Kind      string             `json:"kind"`
		Query     string             `json:"query"`
		Count     int                `json:"count"`
		Providers []providerEvidence `json:"providers,omitempty"`
		Sources   []evidenceSource   `json:"sources,omitempty"`
		Evidence  evidenceSummary    `json:"evidence"`
	}

	extractSources := func(providerName, text string) []evidenceSource {
		if strings.TrimSpace(text) == "" {
			return nil
		}
		seen := make(map[string]bool)
		matches := reURL.FindAllString(text, -1)
		out := make([]evidenceSource, 0, len(matches))
		for _, raw := range matches {
			raw = strings.TrimSpace(raw)
			raw = strings.TrimRight(raw, ".,;:)]}\"'")
			if raw == "" || seen[raw] {
				continue
			}
			seen[raw] = true

			host := ""
			if u, err := url.Parse(raw); err == nil {
				host = strings.TrimSpace(u.Host)
				if host != "" {
					host = strings.ToLower(strings.TrimSpace(strings.Split(host, ":")[0]))
				}
			}
			out = append(out, evidenceSource{
				URL:      raw,
				Domain:   host,
				Provider: providerName,
			})
		}
		return out
	}

	runProvider := func(name string, provider SearchProvider) providerEvidence {
		if provider == nil {
			return providerEvidence{Name: name, OK: false, Error: "provider not configured"}
		}
		res, err := provider.Search(ctx, query, count)
		if err != nil {
			return providerEvidence{Name: name, OK: false, Error: err.Error()}
		}
		sources := extractSources(name, res.Text)
		return providerEvidence{
			Name:        name,
			KeyID:       strings.TrimSpace(res.KeyID),
			OK:          true,
			SourceCount: len(sources),
			Sources:     sources,
		}
	}

	type toRun struct {
		name     string
		provider SearchProvider
	}
	toRunList := []toRun{
		{name: t.providerName, provider: t.provider},
	}
	if t.secondary != nil {
		toRunList = append(toRunList, toRun{name: t.secondaryName, provider: t.secondary})
	}

	var wg sync.WaitGroup
	results := make([]providerEvidence, len(toRunList))
	for i, item := range toRunList {
		wg.Add(1)
		go func(i int, item toRun) {
			defer wg.Done()
			results[i] = runProvider(item.name, item.provider)
		}(i, item)
	}
	wg.Wait()

	merged := make([]evidenceSource, 0, 16)
	seenURL := make(map[string]bool)
	distinctDomains := make(map[string]bool)
	for _, p := range results {
		for _, s := range p.Sources {
			u := strings.TrimSpace(s.URL)
			if u == "" || seenURL[u] {
				continue
			}
			seenURL[u] = true
			merged = append(merged, s)
			if s.Domain != "" {
				distinctDomains[s.Domain] = true
			}
		}
	}
	sort.Slice(merged, func(i, j int) bool {
		if merged[i].Domain == merged[j].Domain {
			return merged[i].URL < merged[j].URL
		}
		return merged[i].Domain < merged[j].Domain
	})

	minDomains := t.evidenceMinDomain
	if minDomains <= 0 {
		minDomains = 2
	}
	summary := evidenceSummary{
		Enabled:         true,
		MinDomains:      minDomains,
		DistinctDomains: len(distinctDomains),
		Satisfied:       len(distinctDomains) >= minDomains,
	}

	out := payload{
		Kind:      "web_search_result",
		Query:     query,
		Count:     count,
		Providers: results,
		Sources:   merged,
		Evidence:  summary,
	}
	data, _ := json.MarshalIndent(out, "", "  ")

	providerNames := make([]string, 0, len(toRunList))
	for _, item := range toRunList {
		if strings.TrimSpace(item.name) != "" {
			providerNames = append(providerNames, strings.TrimSpace(item.name))
		}
	}
	userSummary := fmt.Sprintf(
		"Web search (evidence_mode=true): %q (providers: %s; sources=%d; distinct_domains=%d; satisfied=%v)",
		query,
		strings.Join(providerNames, ", "),
		len(merged),
		len(distinctDomains),
		summary.Satisfied,
	)

	return &ToolResult{
		ForLLM:  string(data),
		ForUser: userSummary,
	}
}
