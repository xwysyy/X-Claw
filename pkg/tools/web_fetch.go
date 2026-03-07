package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/xwysyy/X-Claw/pkg/utils"
	"golang.org/x/sync/singleflight"
)

type WebFetchTool struct {
	maxChars        int
	proxy           string
	client          *http.Client
	fetchLimitBytes int64

	cacheEnabled       bool
	cacheTTL           time.Duration
	cacheMaxEntries    int
	cacheMaxEntryChars int

	cacheMu sync.Mutex
	cache   map[string]webFetchCacheEntry

	sf singleflight.Group
}

type webFetchFailure struct {
	Kind       string `json:"kind,omitempty"`
	Message    string `json:"message,omitempty"`
	HTTPStatus int    `json:"http_status,omitempty"`
}

type webFetchSource struct {
	URL           string `json:"url"`
	RetrievedAtMS int64  `json:"retrieved_at_ms,omitempty"`
	Status        int    `json:"status,omitempty"`
	ContentType   string `json:"content_type,omitempty"`
}

type webFetchQuote struct {
	SourceURL string `json:"source_url,omitempty"`
	Text      string `json:"text"`
}

type webFetchRawResult struct {
	URL         string
	Status      int
	ContentType string

	Extractor string
	Tried     []string

	SourceLength int
	Text         string

	RetrievedAtMS int64

	Failure *webFetchFailure
}

type webFetchCacheEntry struct {
	ExpiresAt time.Time
	Value     webFetchRawResult
}

func NewWebFetchTool(maxChars int, fetchLimitBytes int64) (*WebFetchTool, error) {
	// createHTTPClient cannot fail with an empty proxy string.
	return NewWebFetchToolWithProxy(maxChars, "", fetchLimitBytes)
}

func NewWebFetchToolWithProxy(maxChars int, proxy string, fetchLimitBytes int64) (*WebFetchTool, error) {
	if maxChars <= 0 {
		maxChars = 50000
	}

	client, err := createHTTPClient(proxy, 60*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP client for web fetch: %w", err)
	}

	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("stopped after 5 redirects")
		}
		return nil
	}

	if fetchLimitBytes <= 0 {
		fetchLimitBytes = 10 * 1024 * 1024 // Security Fallback
	}

	return &WebFetchTool{
		maxChars:           maxChars,
		proxy:              proxy,
		client:             client,
		fetchLimitBytes:    fetchLimitBytes,
		cacheEnabled:       true,
		cacheTTL:           120 * time.Second,
		cacheMaxEntries:    32,
		cacheMaxEntryChars: 80_000,
		cache:              make(map[string]webFetchCacheEntry),
	}, nil
}

// ConfigureCache configures the in-memory fetch cache for this tool.
// It is safe to call at startup (recommended) and is thread-safe.
func (t *WebFetchTool) ConfigureCache(enabled bool, ttl time.Duration, maxEntries int, maxEntryChars int) {
	if t == nil {
		return
	}
	if ttl <= 0 {
		ttl = 120 * time.Second
	}
	if maxEntries <= 0 {
		maxEntries = 32
	}
	if maxEntryChars <= 0 {
		maxEntryChars = 80_000
	}

	t.cacheMu.Lock()
	defer t.cacheMu.Unlock()

	t.cacheEnabled = enabled
	t.cacheTTL = ttl
	t.cacheMaxEntries = maxEntries
	t.cacheMaxEntryChars = maxEntryChars

	if !t.cacheEnabled {
		t.cache = nil
		return
	}
	if t.cache == nil {
		t.cache = make(map[string]webFetchCacheEntry)
	}
}

func (t *WebFetchTool) Name() string {
	return "web_fetch"
}

func (t *WebFetchTool) ParallelPolicy() ToolParallelPolicy {
	return ToolParallelReadOnly
}

func (t *WebFetchTool) Description() string {
	return "Fetch a URL and extract readable content (HTML to text). " +
		"Input: url (string, required), maxChars (integer, optional — max chars to extract). " +
		"Output: extracted text content with metadata (status code, content type, length). " +
		"Supports HTML pages, JSON APIs, and plain text. " +
		"Use this to read specific web pages, API endpoints, or articles found via 'web_search'."
}

func (t *WebFetchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "URL to fetch",
			},
			"maxChars": map[string]any{
				"type":        "integer",
				"description": "Maximum characters to extract",
				"minimum":     100.0,
			},
			"llmMaxChars": map[string]any{
				"type":        "integer",
				"description": "Maximum characters included in LLM-facing content (defaults to maxChars)",
				"minimum":     100.0,
			},
		},
		"required": []string{"url"},
	}
}

func (t *WebFetchTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	urlStr, ok := args["url"].(string)
	if !ok {
		return ErrorResult("url is required")
	}
	urlStr = strings.TrimSpace(urlStr)

	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return ErrorResult(fmt.Sprintf("invalid URL: %v", err))
	}

	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return ErrorResult("only http/https URLs are allowed")
	}

	if parsedURL.Host == "" {
		return ErrorResult("missing domain in URL")
	}

	maxChars := t.maxChars
	if raw, ok := args["maxChars"]; ok {
		if mc, err := toInt(raw); err == nil && mc > 100 {
			maxChars = mc
		}
	}
	llmMaxChars := maxChars
	if raw, ok := args["llmMaxChars"]; ok {
		if mc, err := toInt(raw); err == nil && mc > 100 {
			llmMaxChars = mc
		}
	}

	// Cache fast-path.
	if cached, ok := t.getCached(urlStr); ok {
		return t.buildWebFetchResult(urlStr, cached, maxChars, llmMaxChars, true)
	}

	rawAny, _, _ := t.sf.Do(urlStr, func() (any, error) {
		if cached, ok := t.getCached(urlStr); ok {
			return cached, nil
		}
		raw := t.fetchAndExtract(ctx, urlStr)
		t.putCache(urlStr, raw)
		return raw, nil
	})
	raw, _ := rawAny.(webFetchRawResult)
	return t.buildWebFetchResult(urlStr, raw, maxChars, llmMaxChars, false)
}

func (t *WebFetchTool) getCached(urlStr string) (webFetchRawResult, bool) {
	if t == nil {
		return webFetchRawResult{}, false
	}

	urlStr = strings.TrimSpace(urlStr)
	if urlStr == "" {
		return webFetchRawResult{}, false
	}

	t.cacheMu.Lock()
	defer t.cacheMu.Unlock()

	if !t.cacheEnabled || t.cache == nil || t.cacheTTL < 0 {
		return webFetchRawResult{}, false
	}

	now := time.Now()
	// Opportunistic pruning.
	for k, v := range t.cache {
		if now.After(v.ExpiresAt) {
			delete(t.cache, k)
		}
	}

	ent, ok := t.cache[urlStr]
	if !ok {
		return webFetchRawResult{}, false
	}
	if now.After(ent.ExpiresAt) {
		delete(t.cache, urlStr)
		return webFetchRawResult{}, false
	}
	return ent.Value, true
}

func (t *WebFetchTool) putCache(urlStr string, raw webFetchRawResult) {
	if t == nil {
		return
	}

	urlStr = strings.TrimSpace(urlStr)
	if urlStr == "" {
		return
	}

	t.cacheMu.Lock()
	defer t.cacheMu.Unlock()

	if !t.cacheEnabled || t.cacheTTL < 0 {
		return
	}
	if t.cache == nil {
		t.cache = make(map[string]webFetchCacheEntry)
	}
	if t.cacheTTL <= 0 {
		t.cacheTTL = 120 * time.Second
	}
	if t.cacheMaxEntries <= 0 {
		t.cacheMaxEntries = 32
	}
	if t.cacheMaxEntryChars <= 0 {
		t.cacheMaxEntryChars = 80_000
	}

	if strings.TrimSpace(raw.Text) != "" && t.cacheMaxEntryChars > 0 {
		raw.Text = utils.Truncate(raw.Text, t.cacheMaxEntryChars)
	}

	now := time.Now()
	// Prune expired first.
	for k, v := range t.cache {
		if now.After(v.ExpiresAt) {
			delete(t.cache, k)
		}
	}
	// Evict oldest (earliest expiry) if needed.
	for len(t.cache) >= t.cacheMaxEntries {
		var oldestKey string
		var oldest time.Time
		for k, v := range t.cache {
			if oldestKey == "" || v.ExpiresAt.Before(oldest) {
				oldestKey = k
				oldest = v.ExpiresAt
			}
		}
		if oldestKey == "" {
			break
		}
		delete(t.cache, oldestKey)
	}

	t.cache[urlStr] = webFetchCacheEntry{
		ExpiresAt: now.Add(t.cacheTTL),
		Value:     raw,
	}
}

func (t *WebFetchTool) fetchAndExtract(ctx context.Context, urlStr string) webFetchRawResult {
	now := time.Now()
	out := webFetchRawResult{
		URL:           strings.TrimSpace(urlStr),
		RetrievedAtMS: now.UnixMilli(),
	}
	if ctx == nil {
		ctx = context.Background()
	}

	if t == nil || t.client == nil {
		out.Failure = &webFetchFailure{
			Kind:    "internal",
			Message: "web_fetch is not configured",
		}
		return out
	}

	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		out.Failure = &webFetchFailure{
			Kind:    "invalid_url",
			Message: err.Error(),
		}
		return out
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/json,text/plain,*/*")

	resp, err := t.client.Do(req)
	if err != nil {
		out.Failure = classifyWebFetchError(err)
		return out
	}
	defer resp.Body.Close()

	out.Status = resp.StatusCode
	out.ContentType = strings.TrimSpace(resp.Header.Get("Content-Type"))

	limit := t.fetchLimitBytes
	if limit <= 0 {
		limit = 10 * 1024 * 1024
	}

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if readErr != nil {
		out.Failure = &webFetchFailure{
			Kind:       "read_error",
			Message:    readErr.Error(),
			HTTPStatus: resp.StatusCode,
		}
		return out
	}
	if int64(len(body)) > limit {
		out.Failure = &webFetchFailure{
			Kind:       "oversize",
			Message:    fmt.Sprintf("size exceeded %d bytes limit", limit),
			HTTPStatus: resp.StatusCode,
		}
		return out
	}

	out.SourceLength = len(body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		out.Failure = classifyWebFetchHTTPStatus(resp.StatusCode, string(body))
	}

	text, extractor, tried, extractErr := extractWebContent(string(body), out.ContentType)
	out.Text = text
	out.Extractor = extractor
	out.Tried = tried
	if extractErr != nil && out.Failure == nil {
		out.Failure = &webFetchFailure{
			Kind:       "extract_error",
			Message:    extractErr.Error(),
			HTTPStatus: resp.StatusCode,
		}
	}

	return out
}

func classifyWebFetchHTTPStatus(status int, bodyPreview string) *webFetchFailure {
	bodyPreview = strings.TrimSpace(bodyPreview)
	if len(bodyPreview) > 600 {
		bodyPreview = utils.Truncate(bodyPreview, 600)
	}

	switch status {
	case http.StatusUnauthorized:
		return &webFetchFailure{Kind: "unauthorized", Message: "http 401 unauthorized", HTTPStatus: status}
	case http.StatusForbidden:
		return &webFetchFailure{Kind: "forbidden", Message: "http 403 forbidden", HTTPStatus: status}
	case http.StatusTooManyRequests:
		return &webFetchFailure{Kind: "rate_limited", Message: "http 429 rate limited", HTTPStatus: status}
	case http.StatusNotFound:
		return &webFetchFailure{Kind: "not_found", Message: "http 404 not found", HTTPStatus: status}
	default:
		msg := fmt.Sprintf("http status %d", status)
		if bodyPreview != "" {
			msg = msg + ": " + bodyPreview
		}
		return &webFetchFailure{Kind: "http_error", Message: msg, HTTPStatus: status}
	}
}

func classifyWebFetchError(err error) *webFetchFailure {
	if err == nil {
		return &webFetchFailure{Kind: "network_error", Message: "unknown error"}
	}
	if errors.Is(err, context.Canceled) {
		return &webFetchFailure{Kind: "canceled", Message: err.Error()}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &webFetchFailure{Kind: "timeout", Message: err.Error()}
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return &webFetchFailure{Kind: "timeout", Message: err.Error()}
	}

	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "no such host"):
		return &webFetchFailure{Kind: "dns", Message: err.Error()}
	case strings.Contains(msg, "tls"):
		return &webFetchFailure{Kind: "tls", Message: err.Error()}
	default:
		return &webFetchFailure{Kind: "network_error", Message: err.Error()}
	}
}

func extractWebContent(body string, contentType string) (text string, extractor string, tried []string, err error) {
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	bodyTrim := strings.TrimSpace(body)

	isJSON := strings.Contains(contentType, "application/json") ||
		strings.Contains(contentType, "+json") ||
		strings.Contains(contentType, "text/json")
	isHTML := strings.Contains(contentType, "text/html") || strings.Contains(contentType, "application/xhtml+xml")
	isText := strings.HasPrefix(contentType, "text/") || strings.Contains(contentType, "application/xml") || strings.Contains(contentType, "text/xml")

	if !isJSON && !isHTML && !isText {
		// Sniff when server does not provide useful content-type.
		if strings.HasPrefix(strings.ToLower(bodyTrim), "<!doctype html") || strings.HasPrefix(strings.ToLower(bodyTrim), "<html") {
			isHTML = true
		} else if strings.HasPrefix(bodyTrim, "{") || strings.HasPrefix(bodyTrim, "[") {
			isJSON = true
		} else if contentType == "" {
			isText = true
		}
	}

	switch {
	case isJSON:
		tried = []string{"json.pretty", "json.raw"}
		var buf bytes.Buffer
		if json.Indent(&buf, []byte(bodyTrim), "", "  ") == nil {
			return buf.String(), "json.pretty", tried, nil
		}
		return bodyTrim, "json.raw", tried, nil
	case isHTML:
		tried = []string{"html.readability_like", "html.strip_tags", "text.raw"}
		if candidate := extractHTMLReadabilityLike(body); strings.TrimSpace(candidate) != "" {
			return candidate, "html.readability_like", tried, nil
		}
		if candidate := extractHTMLStripTags(body); strings.TrimSpace(candidate) != "" {
			return candidate, "html.strip_tags", tried, nil
		}
		return bodyTrim, "text.raw", tried, nil
	case isText:
		tried = []string{"text.raw"}
		return bodyTrim, "text.raw", tried, nil
	default:
		return "", "", nil, fmt.Errorf("unsupported content type: %q", contentType)
	}
}

func extractHTMLReadabilityLike(htmlContent string) string {
	// Super lightweight readability-ish extraction:
	// - remove script/style/noscript blocks
	// - drop common layout blocks (nav/header/footer/aside)
	// - preserve newlines for common block tags
	s := htmlContent

	// Remove non-content blocks first.
	s = reScript.ReplaceAllString(s, "")
	s = reStyle.ReplaceAllString(s, "")

	s = reNoScript.ReplaceAllString(s, "")
	for _, re := range reLayoutTags {
		s = re.ReplaceAllString(s, "")
	}

	s = normalizeHTMLNewlines(s)
	s = stripTags(s)
	s = html.UnescapeString(s)

	s = strings.TrimSpace(s)
	s = reWhitespace.ReplaceAllString(s, " ")
	s = reBlankLines.ReplaceAllString(s, "\n\n")

	lines := strings.Split(s, "\n")
	cleanLines := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Ignore very short "chrome" lines.
		if len([]rune(line)) < 3 {
			continue
		}
		cleanLines = append(cleanLines, line)
	}

	out := strings.Join(cleanLines, "\n")
	if len([]rune(out)) < 40 {
		// Too small to be useful; likely missed. Let caller fall back.
		return ""
	}
	return out
}

func extractHTMLStripTags(htmlContent string) string {
	s := htmlContent
	s = reScript.ReplaceAllString(s, "")
	s = reStyle.ReplaceAllString(s, "")
	s = normalizeHTMLNewlines(s)
	s = stripTags(s)
	s = html.UnescapeString(s)

	s = strings.TrimSpace(s)
	s = reWhitespace.ReplaceAllString(s, " ")
	s = reBlankLines.ReplaceAllString(s, "\n\n")

	lines := strings.Split(s, "\n")
	cleanLines := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			cleanLines = append(cleanLines, line)
		}
	}
	return strings.Join(cleanLines, "\n")
}

func normalizeHTMLNewlines(s string) string {
	s = reBR.ReplaceAllString(s, "\n")
	s = reCloseBlock.ReplaceAllString(s, "\n")

	return s
}

func (t *WebFetchTool) buildWebFetchResult(urlStr string, raw webFetchRawResult, maxChars int, llmMaxChars int, cacheHit bool) *ToolResult {
	if t == nil {
		return ErrorResult("web_fetch tool is not configured")
	}
	if maxChars <= 0 {
		maxChars = t.maxChars
	}
	if llmMaxChars <= 0 {
		llmMaxChars = maxChars
	}

	fullText := strings.TrimSpace(raw.Text)
	userText := utils.Truncate(fullText, maxChars)
	llmText := utils.Truncate(userText, llmMaxChars)

	truncated := false
	if len([]rune(fullText)) > len([]rune(llmText)) {
		truncated = true
	}

	payload := struct {
		Kind            string           `json:"kind"`
		URL             string           `json:"url"`
		OK              bool             `json:"ok"`
		Status          int              `json:"status,omitempty"`
		ContentType     string           `json:"content_type,omitempty"`
		RetrievedAtMS   int64            `json:"retrieved_at_ms,omitempty"`
		Extractor       string           `json:"extractor,omitempty"`
		TriedExtractors []string         `json:"tried_extractors,omitempty"`
		SourceLength    int              `json:"source_length,omitempty"`
		Text            string           `json:"text,omitempty"`
		TextChars       int              `json:"text_chars,omitempty"`
		MaxChars        int              `json:"max_chars,omitempty"`
		LLMMaxChars     int              `json:"llm_max_chars,omitempty"`
		Truncated       bool             `json:"truncated,omitempty"`
		CacheHit        bool             `json:"cache_hit,omitempty"`
		Failure         *webFetchFailure `json:"failure,omitempty"`
		Sources         []webFetchSource `json:"sources,omitempty"`
		Quotes          []webFetchQuote  `json:"quotes,omitempty"`
	}{
		Kind:            "web_fetch_result",
		URL:             urlStr,
		OK:              raw.Failure == nil && raw.Status >= 200 && raw.Status < 300,
		Status:          raw.Status,
		ContentType:     raw.ContentType,
		RetrievedAtMS:   raw.RetrievedAtMS,
		Extractor:       raw.Extractor,
		TriedExtractors: raw.Tried,
		SourceLength:    raw.SourceLength,
		Text:            llmText,
		TextChars:       len([]rune(fullText)),
		MaxChars:        maxChars,
		LLMMaxChars:     llmMaxChars,
		Truncated:       truncated,
		CacheHit:        cacheHit,
		Failure:         raw.Failure,
		Sources: []webFetchSource{
			{
				URL:           urlStr,
				RetrievedAtMS: raw.RetrievedAtMS,
				Status:        raw.Status,
				ContentType:   raw.ContentType,
			},
		},
		Quotes: buildWebFetchQuotes(urlStr, llmText),
	}

	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to encode web_fetch result: %v", err))
	}

	userSummary := fmt.Sprintf(
		"Fetched %s (status=%d, bytes=%d, extractor=%s, truncated=%v, cache_hit=%v)",
		urlStr,
		raw.Status,
		raw.SourceLength,
		strings.TrimSpace(raw.Extractor),
		truncated,
		cacheHit,
	)

	res := &ToolResult{
		ForLLM:  string(encoded),
		ForUser: userSummary,
		IsError: raw.Failure != nil || raw.Status >= 400 || raw.Status == 0,
	}
	if raw.Failure != nil && strings.TrimSpace(raw.Failure.Message) != "" {
		res.ForUser = fmt.Sprintf("web_fetch failed (%s): %s", raw.Failure.Kind, raw.Failure.Message)
	}
	return res
}

func buildWebFetchQuotes(urlStr string, text string) []webFetchQuote {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	lines := strings.Split(text, "\n")
	out := make([]webFetchQuote, 0, 3)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, webFetchQuote{
			SourceURL: urlStr,
			Text:      utils.Truncate(line, 240),
		})
		if len(out) >= 3 {
			break
		}
	}
	if len(out) == 0 {
		out = append(out, webFetchQuote{SourceURL: urlStr, Text: utils.Truncate(text, 240)})
	}
	return out
}

func (t *WebFetchTool) extractText(htmlContent string) string {
	return extractHTMLStripTags(htmlContent)
}
