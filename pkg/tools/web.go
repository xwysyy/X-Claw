package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
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

	reDDGLink    = regexp.MustCompile(`<a[^>]*class="[^"]*result__a[^"]*"[^>]*href="([^"]+)"[^>]*>([\s\S]*?)</a>`)
	reDDGSnippet = regexp.MustCompile(`<a class="result__snippet[^"]*".*?>([\s\S]*?)</a>`)
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
