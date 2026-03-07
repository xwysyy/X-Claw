package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

	perplexityTimeout = 30 * time.Second
	glmSearchTimeout  = 15 * time.Second
)

var (
	reScript     = regexp.MustCompile(`<script[\s\S]*?</script>`)
	reStyle      = regexp.MustCompile(`<style[\s\S]*?</style>`)
	reTags       = regexp.MustCompile(`<[^>]+>`)
	reWhitespace = regexp.MustCompile(`[^\S\n]+`)
	reBlankLines = regexp.MustCompile(`\n{3,}`)
	reNoScript   = regexp.MustCompile(`(?is)<noscript[\s\S]*?</noscript>`)
	reLayoutTags = []*regexp.Regexp{
		regexp.MustCompile(`(?is)<nav[^>]*>[\s\S]*?</nav>`),
		regexp.MustCompile(`(?is)<header[^>]*>[\s\S]*?</header>`),
		regexp.MustCompile(`(?is)<footer[^>]*>[\s\S]*?</footer>`),
		regexp.MustCompile(`(?is)<aside[^>]*>[\s\S]*?</aside>`),
	}
	reBR         = regexp.MustCompile(`(?i)<br\s*/?>`)
	reCloseBlock = regexp.MustCompile(`(?i)</(p|div|h[1-6]|li|tr|td|th|section|article|header|footer|nav|aside)>`)
	reURL        = regexp.MustCompile(`https?://[^\s<>"')]+`)
)

func createHTTPClient(proxyURL string, timeout time.Duration) (*http.Client, error) {
	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			MaxIdleConns:        10,
			IdleConnTimeout:     30 * time.Second,
			DisableCompression:  false,
			TLSHandshakeTimeout: 15 * time.Second,
		},
	}

	if proxyURL != "" {
		proxy, err := url.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy URL: %w", err)
		}
		scheme := strings.ToLower(proxy.Scheme)
		switch scheme {
		case "http", "https", "socks5", "socks5h":
		default:
			return nil, fmt.Errorf(
				"unsupported proxy scheme %q (supported: http, https, socks5, socks5h)",
				proxy.Scheme,
			)
		}
		if proxy.Host == "" {
			return nil, fmt.Errorf("invalid proxy URL: missing host")
		}
		client.Transport.(*http.Transport).Proxy = http.ProxyURL(proxy)
	} else {
		client.Transport.(*http.Transport).Proxy = http.ProxyFromEnvironment
	}

	return client, nil
}

type SearchProvider interface {
	Search(ctx context.Context, query string, count int) (SearchProviderResult, error)
}

type SearchProviderResult struct {
	Text  string `json:"text"`
	KeyID string `json:"key_id,omitempty"`
}

type apiKeyEntry struct {
	Key string
	ID  string
}

type apiKeyPool struct {
	keys []apiKeyEntry
	idx  atomic.Uint64
}

func makeKeyID(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(key))
	return "sha256:" + hex.EncodeToString(sum[:4])
}

func newAPIKeyPool(primary string, keys []string) *apiKeyPool {
	all := make([]string, 0, 1+len(keys))
	if strings.TrimSpace(primary) != "" {
		all = append(all, strings.TrimSpace(primary))
	}
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k != "" {
			all = append(all, k)
		}
	}

	seen := make(map[string]bool, len(all))
	entries := make([]apiKeyEntry, 0, len(all))
	for _, k := range all {
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		entries = append(entries, apiKeyEntry{Key: k, ID: makeKeyID(k)})
	}

	if len(entries) == 0 {
		return nil
	}
	return &apiKeyPool{keys: entries}
}

func (p *apiKeyPool) Next() (key string, keyID string, ok bool) {
	if p == nil || len(p.keys) == 0 {
		return "", "", false
	}
	i := p.idx.Add(1) - 1
	ent := p.keys[int(i)%len(p.keys)]
	return ent.Key, ent.ID, true
}

func stripTags(content string) string {
	return reTags.ReplaceAllString(content, "")
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
