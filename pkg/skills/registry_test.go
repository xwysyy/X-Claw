package skills

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xwysyy/X-Claw/pkg/utils"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// mockRegistry is a test double implementing SkillRegistry.
type mockRegistry struct {
	name          string
	searchResults []SearchResult
	searchErr     error
	meta          *SkillMeta
	metaErr       error
	installResult *InstallResult
	installErr    error
}

func (m *mockRegistry) Name() string { return m.name }

func (m *mockRegistry) Search(_ context.Context, _ string, _ int) ([]SearchResult, error) {
	return m.searchResults, m.searchErr
}

func (m *mockRegistry) GetSkillMeta(_ context.Context, _ string) (*SkillMeta, error) {
	return m.meta, m.metaErr
}

func (m *mockRegistry) DownloadAndInstall(_ context.Context, _, _, _ string) (*InstallResult, error) {
	return m.installResult, m.installErr
}

func TestRegistryManagerSearchAllSingle(t *testing.T) {
	mgr := NewRegistryManager()
	mgr.AddRegistry(&mockRegistry{
		name: "test",
		searchResults: []SearchResult{
			{Slug: "skill-a", Score: 0.9, RegistryName: "test"},
			{Slug: "skill-b", Score: 0.5, RegistryName: "test"},
		},
	})

	results, err := mgr.SearchAll(context.Background(), "test query", 10)
	assert.NoError(t, err)
	assert.Len(t, results, 2)
	assert.Equal(t, "skill-a", results[0].Slug)
}

func TestRegistryManagerSearchAllMultiple(t *testing.T) {
	mgr := NewRegistryManager()
	mgr.AddRegistry(&mockRegistry{
		name: "alpha",
		searchResults: []SearchResult{
			{Slug: "skill-a", Score: 0.8, RegistryName: "alpha"},
		},
	})
	mgr.AddRegistry(&mockRegistry{
		name: "beta",
		searchResults: []SearchResult{
			{Slug: "skill-b", Score: 0.95, RegistryName: "beta"},
		},
	})

	results, err := mgr.SearchAll(context.Background(), "test query", 10)
	assert.NoError(t, err)
	assert.Len(t, results, 2)
	// Should be sorted by score descending
	assert.Equal(t, "skill-b", results[0].Slug)
	assert.Equal(t, "skill-a", results[1].Slug)
}

func TestRegistryManagerSearchAllOneFailsGracefully(t *testing.T) {
	mgr := NewRegistryManager()
	mgr.AddRegistry(&mockRegistry{
		name:      "failing",
		searchErr: fmt.Errorf("network error"),
	})
	mgr.AddRegistry(&mockRegistry{
		name: "working",
		searchResults: []SearchResult{
			{Slug: "skill-a", Score: 0.8, RegistryName: "working"},
		},
	})

	results, err := mgr.SearchAll(context.Background(), "test query", 10)
	assert.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "skill-a", results[0].Slug)
}

func TestRegistryManagerSearchAllAllFail(t *testing.T) {
	mgr := NewRegistryManager()
	mgr.AddRegistry(&mockRegistry{
		name:      "fail-1",
		searchErr: fmt.Errorf("error 1"),
	})

	_, err := mgr.SearchAll(context.Background(), "test query", 10)
	assert.Error(t, err)
}

func TestRegistryManagerSearchAllNoRegistries(t *testing.T) {
	mgr := NewRegistryManager()
	_, err := mgr.SearchAll(context.Background(), "test query", 10)
	assert.Error(t, err)
}

func TestRegistryManagerGetRegistry(t *testing.T) {
	mgr := NewRegistryManager()
	mock := &mockRegistry{name: "clawhub"}
	mgr.AddRegistry(mock)

	got := mgr.GetRegistry("clawhub")
	assert.NotNil(t, got)
	assert.Equal(t, "clawhub", got.Name())

	got = mgr.GetRegistry("nonexistent")
	assert.Nil(t, got)
}

func TestRegistryManagerSearchAllRespectLimit(t *testing.T) {
	mgr := NewRegistryManager()
	results := make([]SearchResult, 20)
	for i := range results {
		results[i] = SearchResult{Slug: fmt.Sprintf("skill-%d", i), Score: float64(20 - i)}
	}
	mgr.AddRegistry(&mockRegistry{
		name:          "test",
		searchResults: results,
	})

	got, err := mgr.SearchAll(context.Background(), "test", 5)
	assert.NoError(t, err)
	assert.Len(t, got, 5)
	// Top scores first
	assert.Equal(t, "skill-0", got[0].Slug)
}

func TestRegistryManagerSearchAllTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	time.Sleep(5 * time.Millisecond) // Let context expire.

	mgr := NewRegistryManager()
	mgr.AddRegistry(&mockRegistry{
		name:      "slow",
		searchErr: fmt.Errorf("context deadline exceeded"),
	})

	_, err := mgr.SearchAll(ctx, "test", 5)
	assert.Error(t, err)
}

func TestSortByScoreDesc(t *testing.T) {
	results := []SearchResult{
		{Slug: "c", Score: 0.3},
		{Slug: "a", Score: 0.9},
		{Slug: "b", Score: 0.5},
	}
	sortByScoreDesc(results)
	assert.Equal(t, "a", results[0].Slug)
	assert.Equal(t, "b", results[1].Slug)
	assert.Equal(t, "c", results[2].Slug)
}

func TestIsSafeSlug(t *testing.T) {
	assert.NoError(t, utils.ValidateSkillIdentifier("github"))
	assert.NoError(t, utils.ValidateSkillIdentifier("docker-compose"))
	assert.Error(t, utils.ValidateSkillIdentifier(""))
	assert.Error(t, utils.ValidateSkillIdentifier("../etc/passwd"))
	assert.Error(t, utils.ValidateSkillIdentifier("path/traversal"))
	assert.Error(t, utils.ValidateSkillIdentifier("path\\traversal"))
}

func TestSearchCacheExactHit(t *testing.T) {
	cache := NewSearchCache(10, 5*time.Minute)

	results := []SearchResult{
		{Slug: "github", Score: 0.9, RegistryName: "clawhub"},
		{Slug: "docker", Score: 0.7, RegistryName: "clawhub"},
	}
	cache.Put("github integration", results)

	got, hit := cache.Get("github integration")
	assert.True(t, hit)
	assert.Len(t, got, 2)
	assert.Equal(t, "github", got[0].Slug)
}

func TestSearchCacheExactHitCaseInsensitive(t *testing.T) {
	cache := NewSearchCache(10, 5*time.Minute)

	results := []SearchResult{{Slug: "github", Score: 0.9}}
	cache.Put("GitHub Integration", results)

	got, hit := cache.Get("github integration")
	assert.True(t, hit)
	assert.Len(t, got, 1)
}

func TestSearchCacheSimilarHit(t *testing.T) {
	cache := NewSearchCache(10, 5*time.Minute)

	results := []SearchResult{{Slug: "github", Score: 0.9}}
	cache.Put("github integration tool", results)

	// "github integration" is very similar to "github integration tool"
	got, hit := cache.Get("github integration")
	assert.True(t, hit)
	assert.Len(t, got, 1)
}

func TestSearchCacheDissimilarMiss(t *testing.T) {
	cache := NewSearchCache(10, 5*time.Minute)

	results := []SearchResult{{Slug: "github", Score: 0.9}}
	cache.Put("github integration", results)

	// Completely unrelated query
	_, hit := cache.Get("database management")
	assert.False(t, hit)
}

func TestSearchCacheTTLExpiration(t *testing.T) {
	cache := NewSearchCache(10, 50*time.Millisecond)

	results := []SearchResult{{Slug: "github", Score: 0.9}}
	cache.Put("github integration", results)

	// Immediately should hit
	_, hit := cache.Get("github integration")
	assert.True(t, hit)

	// Wait for expiration
	time.Sleep(100 * time.Millisecond)

	_, hit = cache.Get("github integration")
	assert.False(t, hit)
}

func TestSearchCacheLRUEviction(t *testing.T) {
	cache := NewSearchCache(3, 5*time.Minute)

	cache.Put("query-1", []SearchResult{{Slug: "a"}})
	cache.Put("query-2", []SearchResult{{Slug: "b"}})
	cache.Put("query-3", []SearchResult{{Slug: "c"}})

	assert.Equal(t, 3, cache.Len())

	// Adding a 4th should evict query-1 (oldest)
	cache.Put("query-4", []SearchResult{{Slug: "d"}})
	assert.Equal(t, 3, cache.Len())

	_, hit := cache.Get("query-1")
	assert.False(t, hit, "oldest entry should be evicted")

	got, hit := cache.Get("query-4")
	assert.True(t, hit)
	assert.Equal(t, "d", got[0].Slug)
}

func TestSearchCacheEmptyQuery(t *testing.T) {
	cache := NewSearchCache(10, 5*time.Minute)

	_, hit := cache.Get("")
	assert.False(t, hit)

	_, hit = cache.Get("   ")
	assert.False(t, hit)
}

func TestSearchCacheResultsCopied(t *testing.T) {
	cache := NewSearchCache(10, 5*time.Minute)

	original := []SearchResult{{Slug: "github", Score: 0.9}}
	cache.Put("test", original)

	// Mutate original after putting
	original[0].Slug = "mutated"

	got, hit := cache.Get("test")
	assert.True(t, hit)
	assert.Equal(t, "github", got[0].Slug, "cache should hold a copy, not a reference")
}

func TestBuildTrigrams(t *testing.T) {
	trigrams := buildTrigrams("hello")
	assert.Contains(t, trigrams, uint32('h')<<16|uint32('e')<<8|uint32('l'))
	assert.Contains(t, trigrams, uint32('e')<<16|uint32('l')<<8|uint32('l'))
	assert.Contains(t, trigrams, uint32('l')<<16|uint32('l')<<8|uint32('o'))
	assert.Len(t, trigrams, 3)
}

func TestJaccardSimilarity(t *testing.T) {
	a := buildTrigrams("github integration")
	b := buildTrigrams("github integration tool")

	sim := jaccardSimilarity(a, b)
	assert.Greater(t, sim, 0.5, "similar strings should have high sim")

	c := buildTrigrams("completely different query about databases")
	sim2 := jaccardSimilarity(a, c)
	assert.Less(t, sim2, 0.3, "dissimilar strings should have low sim")
}

func TestJaccardSimilarityEdgeCases(t *testing.T) {
	empty := buildTrigrams("")
	nonempty := buildTrigrams("hello")

	assert.Equal(t, 1.0, jaccardSimilarity(empty, empty))
	assert.Equal(t, 0.0, jaccardSimilarity(empty, nonempty))
	assert.Equal(t, 0.0, jaccardSimilarity(nonempty, empty))
}

func TestSearchCacheConcurrency(t *testing.T) {
	cache := NewSearchCache(50, 5*time.Minute)
	done := make(chan struct{})

	// Concurrent writes
	go func() {
		for i := range 100 {
			cache.Put("query-write-"+string(rune('a'+i%26)), []SearchResult{{Slug: "x"}})
		}
		done <- struct{}{}
	}()

	// Concurrent reads
	go func() {
		for range 100 {
			cache.Get("query-write-a")
		}
		done <- struct{}{}
	}()

	<-done
}

func TestSearchCacheLRUUpdateOnGet(t *testing.T) {
	// Capacity 3
	cache := NewSearchCache(3, time.Hour)

	// Fill cache: query-A, query-B, query-C
	// Use longer strings to ensure trigrams are generated and avoid false positive similarity
	cache.Put("query-A", []SearchResult{{Slug: "A"}})
	cache.Put("query-B", []SearchResult{{Slug: "B"}})
	cache.Put("query-C", []SearchResult{{Slug: "C"}})

	// Access query-A (should make it most recently used)
	if _, found := cache.Get("query-A"); !found {
		t.Fatal("query-A should be in cache")
	}

	// Add query-D. Should evict query-B (LRU) instead of query-A (which was refreshed)
	cache.Put("query-D", []SearchResult{{Slug: "D"}})

	// Check if query-A is still there
	if _, found := cache.Get("query-A"); !found {
		t.Fatalf("query-A was evicted! valid LRU should have kept query-A and evicted query-B.")
	}

	// Check if query-B is evicted
	if _, found := cache.Get("query-B"); found {
		t.Fatal("query-B should have been evicted")
	}
}

func newTestRegistry(serverURL, authToken string) *ClawHubRegistry {
	return NewClawHubRegistry(ClawHubConfig{
		Enabled:   true,
		BaseURL:   serverURL,
		AuthToken: authToken,
	})
}

func TestClawHubRegistrySearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/search", r.URL.Path)
		assert.Equal(t, "github", r.URL.Query().Get("q"))

		slug := "github"
		name := "GitHub Integration"
		summary := "Interact with GitHub repos"
		version := "1.0.0"

		json.NewEncoder(w).Encode(clawhubSearchResponse{
			Results: []clawhubSearchResult{
				{Score: 0.95, Slug: &slug, DisplayName: &name, Summary: &summary, Version: &version},
			},
		})
	}))
	defer srv.Close()

	reg := newTestRegistry(srv.URL, "")
	results, err := reg.Search(context.Background(), "github", 5)

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "github", results[0].Slug)
	assert.Equal(t, "GitHub Integration", results[0].DisplayName)
	assert.InDelta(t, 0.95, results[0].Score, 0.001)
	assert.Equal(t, "clawhub", results[0].RegistryName)
}

func TestClawHubRegistryGetSkillMeta(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/skills/github", r.URL.Path)

		json.NewEncoder(w).Encode(clawhubSkillResponse{
			Slug:        "github",
			DisplayName: "GitHub Integration",
			Summary:     "Full GitHub API integration",
			LatestVersion: &clawhubVersionInfo{
				Version: "2.1.0",
			},
			Moderation: &clawhubModerationInfo{
				IsMalwareBlocked: false,
				IsSuspicious:     true,
			},
		})
	}))
	defer srv.Close()

	reg := newTestRegistry(srv.URL, "")
	meta, err := reg.GetSkillMeta(context.Background(), "github")

	require.NoError(t, err)
	assert.Equal(t, "github", meta.Slug)
	assert.Equal(t, "2.1.0", meta.LatestVersion)
	assert.False(t, meta.IsMalwareBlocked)
	assert.True(t, meta.IsSuspicious)
}

func TestClawHubRegistryGetSkillMetaUnsafeSlug(t *testing.T) {
	reg := newTestRegistry("https://example.com", "")
	_, err := reg.GetSkillMeta(context.Background(), "../etc/passwd")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid slug")
}

func TestClawHubRegistryDownloadAndInstall(t *testing.T) {
	// Create a valid ZIP in memory.
	zipBuf := createTestZip(t, map[string]string{
		"SKILL.md":  "---\nname: test-skill\ndescription: A test\n---\nHello skill",
		"README.md": "# Test Skill\n",
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/skills/test-skill":
			// Metadata endpoint.
			json.NewEncoder(w).Encode(clawhubSkillResponse{
				Slug:          "test-skill",
				DisplayName:   "Test Skill",
				Summary:       "A test skill",
				LatestVersion: &clawhubVersionInfo{Version: "1.0.0"},
			})
		case "/api/v1/download":
			assert.Equal(t, "test-skill", r.URL.Query().Get("slug"))
			w.Header().Set("Content-Type", "application/zip")
			w.Write(zipBuf)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	tmpDir := t.TempDir()
	targetDir := filepath.Join(tmpDir, "test-skill")

	reg := newTestRegistry(srv.URL, "")
	result, err := reg.DownloadAndInstall(context.Background(), "test-skill", "1.0.0", targetDir)

	require.NoError(t, err)
	assert.Equal(t, "1.0.0", result.Version)
	assert.False(t, result.IsMalwareBlocked)

	// Verify extracted files.
	skillContent, err := os.ReadFile(filepath.Join(targetDir, "SKILL.md"))
	require.NoError(t, err)
	assert.Contains(t, string(skillContent), "Hello skill")

	readmeContent, err := os.ReadFile(filepath.Join(targetDir, "README.md"))
	require.NoError(t, err)
	assert.Contains(t, string(readmeContent), "# Test Skill")
}

func TestClawHubRegistryAuthToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		assert.Equal(t, "Bearer test-token-123", authHeader)
		json.NewEncoder(w).Encode(clawhubSearchResponse{Results: nil})
	}))
	defer srv.Close()

	reg := newTestRegistry(srv.URL, "test-token-123")
	_, _ = reg.Search(context.Background(), "test", 5)
}

func TestClawHubRegistryUsesProxyFromEnvironment(t *testing.T) {
	reg := newTestRegistry("https://example.com", "")

	transport, ok := reg.client.Transport.(*http.Transport)
	require.True(t, ok)
	require.NotNil(t, transport.Proxy)
	assert.Equal(
		t,
		reflect.ValueOf(http.ProxyFromEnvironment).Pointer(),
		reflect.ValueOf(transport.Proxy).Pointer(),
	)
}

func TestExtractZipPathTraversal(t *testing.T) {
	// Create a ZIP with a path traversal entry.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// Malicious entry trying to escape directory.
	w, err := zw.Create("../../etc/passwd")
	require.NoError(t, err)
	w.Write([]byte("malicious"))

	zw.Close()

	// Write to temp file for extractZipFile.
	tmpZip := filepath.Join(t.TempDir(), "bad.zip")
	require.NoError(t, os.WriteFile(tmpZip, buf.Bytes(), 0o644))

	tmpDir := t.TempDir()
	err = utils.ExtractZipFile(tmpZip, tmpDir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsafe path")
}

func TestExtractZipWithSubdirectories(t *testing.T) {
	zipBuf := createTestZip(t, map[string]string{
		"SKILL.md":           "root file",
		"scripts/helper.sh":  "#!/bin/bash\necho hello",
		"examples/demo.yaml": "key: value",
	})

	// Write to temp file for extractZipFile.
	tmpZip := filepath.Join(t.TempDir(), "test.zip")
	require.NoError(t, os.WriteFile(tmpZip, zipBuf, 0o644))

	tmpDir := t.TempDir()
	targetDir := filepath.Join(tmpDir, "my-skill")

	err := utils.ExtractZipFile(tmpZip, targetDir)
	require.NoError(t, err)

	// Verify nested file.
	data, err := os.ReadFile(filepath.Join(targetDir, "scripts", "helper.sh"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "#!/bin/bash")
}

func TestClawHubRegistrySearchHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal Server Error"))
	}))
	defer srv.Close()

	reg := newTestRegistry(srv.URL, "")
	_, err := reg.Search(context.Background(), "test", 5)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestClawHubRegistrySearchNullableFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		validSlug := "valid-slug"
		validSummary := "valid summary"

		// Return results with various null/empty fields
		json.NewEncoder(w).Encode(clawhubSearchResponse{
			Results: []clawhubSearchResult{
				// Case 1: Null Slug -> Skip
				{Score: 0.1, Slug: nil, DisplayName: nil, Summary: nil, Version: nil},
				// Case 2: Valid Slug, Null Summary -> Skip
				{Score: 0.2, Slug: &validSlug, DisplayName: nil, Summary: nil, Version: nil},
				// Case 3: Valid Slug, Valid Summary, Null Name -> Keep, Name=Slug
				{Score: 0.8, Slug: &validSlug, DisplayName: nil, Summary: &validSummary, Version: nil},
			},
		})
	}))
	defer srv.Close()

	reg := newTestRegistry(srv.URL, "")
	results, err := reg.Search(context.Background(), "test", 5)

	require.NoError(t, err)
	require.Len(t, results, 1, "should only return 1 valid result")

	r := results[0]
	assert.Equal(t, "valid-slug", r.Slug)
	assert.Equal(t, "valid-slug", r.DisplayName, "should fallback name to slug")
	assert.Equal(t, "valid summary", r.Summary)
}

// --- helpers ---

func createTestZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	for name, content := range files {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, err = w.Write([]byte(content))
		require.NoError(t, err)
	}

	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func TestSkillsInfoValidate(t *testing.T) {
	testcases := []struct {
		name        string
		skillName   string
		description string
		wantErr     bool
		errContains []string
	}{
		{
			name:        "valid-skill",
			skillName:   "valid-skill",
			description: "a valid skill description",
			wantErr:     false,
		},
		{
			name:        "empty-name",
			skillName:   "",
			description: "description without name",
			wantErr:     true,
			errContains: []string{"name is required"},
		},
		{
			name:        "empty-description",
			skillName:   "skill-without-description",
			description: "",
			wantErr:     true,
			errContains: []string{"description is required"},
		},
		{
			name:        "empty-both",
			skillName:   "",
			description: "",
			wantErr:     true,
			errContains: []string{"name is required", "description is required"},
		},
		{
			name:        "name-with-spaces",
			skillName:   "skill with spaces",
			description: "invalid name with spaces",
			wantErr:     true,
			errContains: []string{"name must be alphanumeric with hyphens"},
		},
		{
			name:        "name-with-underscore",
			skillName:   "skill_underscore",
			description: "invalid name with underscore",
			wantErr:     true,
			errContains: []string{"name must be alphanumeric with hyphens"},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			info := SkillInfo{
				Name:        tc.skillName,
				Description: tc.description,
			}
			err := info.validate()
			if tc.wantErr {
				assert.Error(t, err)
				for _, msg := range tc.errContains {
					assert.ErrorContains(t, err, msg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestExtractFrontmatter(t *testing.T) {
	sl := &SkillsLoader{}

	testcases := []struct {
		name           string
		content        string
		expectedName   string
		expectedDesc   string
		lineEndingType string
	}{
		{
			name:           "unix-line-endings",
			lineEndingType: "Unix (\\n)",
			content:        "---\nname: test-skill\ndescription: A test skill\n---\n\n# Skill Content",
			expectedName:   "test-skill",
			expectedDesc:   "A test skill",
		},
		{
			name:           "windows-line-endings",
			lineEndingType: "Windows (\\r\\n)",
			content:        "---\r\nname: test-skill\r\ndescription: A test skill\r\n---\r\n\r\n# Skill Content",
			expectedName:   "test-skill",
			expectedDesc:   "A test skill",
		},
		{
			name:           "classic-mac-line-endings",
			lineEndingType: "Classic Mac (\\r)",
			content:        "---\rname: test-skill\rdescription: A test skill\r---\r\r# Skill Content",
			expectedName:   "test-skill",
			expectedDesc:   "A test skill",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			// Extract frontmatter
			frontmatter := sl.extractFrontmatter(tc.content)
			assert.NotEmpty(t, frontmatter, "Frontmatter should be extracted for %s line endings", tc.lineEndingType)

			// Parse YAML to get name and description (parseSimpleYAML now handles all line ending types)
			yamlMeta := sl.parseSimpleYAML(frontmatter)
			assert.Equal(
				t,
				tc.expectedName,
				yamlMeta["name"],
				"Name should be correctly parsed from frontmatter with %s line endings",
				tc.lineEndingType,
			)
			assert.Equal(
				t,
				tc.expectedDesc,
				yamlMeta["description"],
				"Description should be correctly parsed from frontmatter with %s line endings",
				tc.lineEndingType,
			)
		})
	}
}

// createSkillDir creates a skill directory with a SKILL.md file containing the given frontmatter.
func createSkillDir(t *testing.T, base, dirName, name, description string) {
	t.Helper()
	dir := filepath.Join(base, dirName)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	content := "---\nname: " + name + "\ndescription: " + description + "\n---\n\n# " + name
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644))
}

func TestListSkillsWorkspaceOverridesGlobal(t *testing.T) {
	tmp := t.TempDir()
	ws := filepath.Join(tmp, "workspace")
	global := filepath.Join(tmp, "global")

	createSkillDir(t, filepath.Join(ws, "skills"), "my-skill", "my-skill", "workspace version")
	createSkillDir(t, global, "my-skill", "my-skill", "global version")

	sl := NewSkillsLoader(ws, global, "")
	skills := sl.ListSkills()

	assert.Len(t, skills, 1)
	assert.Equal(t, "workspace", skills[0].Source)
	assert.Equal(t, "workspace version", skills[0].Description)
}

func TestListSkillsGlobalOverridesBuiltin(t *testing.T) {
	tmp := t.TempDir()
	ws := filepath.Join(tmp, "workspace")
	global := filepath.Join(tmp, "global")
	builtin := filepath.Join(tmp, "builtin")

	createSkillDir(t, global, "my-skill", "my-skill", "global version")
	createSkillDir(t, builtin, "my-skill", "my-skill", "builtin version")

	sl := NewSkillsLoader(ws, global, builtin)
	skills := sl.ListSkills()

	assert.Len(t, skills, 1)
	assert.Equal(t, "global", skills[0].Source)
	assert.Equal(t, "global version", skills[0].Description)
}

func TestListSkillsMetadataNameDedup(t *testing.T) {
	tmp := t.TempDir()
	ws := filepath.Join(tmp, "workspace")
	global := filepath.Join(tmp, "global")

	// Different directory names but same metadata name
	createSkillDir(t, filepath.Join(ws, "skills"), "dir-a", "shared-name", "workspace version")
	createSkillDir(t, global, "dir-b", "shared-name", "global version")

	sl := NewSkillsLoader(ws, global, "")
	skills := sl.ListSkills()

	assert.Len(t, skills, 1)
	assert.Equal(t, "shared-name", skills[0].Name)
	assert.Equal(t, "workspace", skills[0].Source)
}

func TestListSkillsMultipleDistinctSkills(t *testing.T) {
	tmp := t.TempDir()
	ws := filepath.Join(tmp, "workspace")
	global := filepath.Join(tmp, "global")
	builtin := filepath.Join(tmp, "builtin")

	createSkillDir(t, filepath.Join(ws, "skills"), "skill-a", "skill-a", "desc a")
	createSkillDir(t, global, "skill-b", "skill-b", "desc b")
	createSkillDir(t, builtin, "skill-c", "skill-c", "desc c")

	sl := NewSkillsLoader(ws, global, builtin)
	skills := sl.ListSkills()

	assert.Len(t, skills, 3)
	names := map[string]string{}
	for _, s := range skills {
		names[s.Name] = s.Source
	}
	assert.Equal(t, "workspace", names["skill-a"])
	assert.Equal(t, "global", names["skill-b"])
	assert.Equal(t, "builtin", names["skill-c"])
}

func TestListSkillsInvalidSkillSkipped(t *testing.T) {
	tmp := t.TempDir()
	ws := filepath.Join(tmp, "workspace")
	global := filepath.Join(tmp, "global")

	// Invalid name (underscore)
	createSkillDir(t, filepath.Join(ws, "skills"), "bad_skill", "bad_skill", "desc")
	// Valid skill
	createSkillDir(t, global, "good-skill", "good-skill", "desc")

	sl := NewSkillsLoader(ws, global, "")
	skills := sl.ListSkills()

	assert.Len(t, skills, 1)
	assert.Equal(t, "good-skill", skills[0].Name)
}

func TestListSkillsEmptyAndNonexistentDirs(t *testing.T) {
	tmp := t.TempDir()
	ws := filepath.Join(tmp, "workspace")
	emptyDir := filepath.Join(tmp, "empty")
	require.NoError(t, os.MkdirAll(emptyDir, 0o755))

	sl := NewSkillsLoader(ws, emptyDir, filepath.Join(tmp, "nonexistent"))
	skills := sl.ListSkills()

	assert.Empty(t, skills)
}

func TestListSkillsDirWithoutSkillMD(t *testing.T) {
	tmp := t.TempDir()
	ws := filepath.Join(tmp, "workspace")
	global := filepath.Join(tmp, "global")

	// Directory exists but has no SKILL.md
	require.NoError(t, os.MkdirAll(filepath.Join(global, "no-skillmd"), 0o755))
	// Valid skill alongside
	createSkillDir(t, global, "real-skill", "real-skill", "desc")

	sl := NewSkillsLoader(ws, global, "")
	skills := sl.ListSkills()

	assert.Len(t, skills, 1)
	assert.Equal(t, "real-skill", skills[0].Name)
}

func TestStripFrontmatter(t *testing.T) {
	sl := &SkillsLoader{}

	testcases := []struct {
		name            string
		content         string
		expectedContent string
		lineEndingType  string
	}{
		{
			name:            "unix-line-endings",
			lineEndingType:  "Unix (\\n)",
			content:         "---\nname: test-skill\ndescription: A test skill\n---\n\n# Skill Content",
			expectedContent: "# Skill Content",
		},
		{
			name:            "windows-line-endings",
			lineEndingType:  "Windows (\\r\\n)",
			content:         "---\r\nname: test-skill\r\ndescription: A test skill\r\n---\r\n\r\n# Skill Content",
			expectedContent: "# Skill Content",
		},
		{
			name:            "classic-mac-line-endings",
			lineEndingType:  "Classic Mac (\\r)",
			content:         "---\rname: test-skill\rdescription: A test skill\r---\r\r# Skill Content",
			expectedContent: "# Skill Content",
		},
		{
			name:            "unix-line-endings-without-trailing-newline",
			lineEndingType:  "Unix (\\n) without trailing newline",
			content:         "---\nname: test-skill\ndescription: A test skill\n---\n# Skill Content",
			expectedContent: "# Skill Content",
		},
		{
			name:            "windows-line-endings-without-trailing-newline",
			lineEndingType:  "Windows (\\r\\n) without trailing newline",
			content:         "---\r\nname: test-skill\r\ndescription: A test skill\r\n---\r\n# Skill Content",
			expectedContent: "# Skill Content",
		},
		{
			name:            "no-frontmatter",
			lineEndingType:  "No frontmatter",
			content:         "# Skill Content\n\nSome content here.",
			expectedContent: "# Skill Content\n\nSome content here.",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			result := sl.stripFrontmatter(tc.content)
			assert.Equal(
				t,
				tc.expectedContent,
				result,
				"Frontmatter should be stripped correctly for %s",
				tc.lineEndingType,
			)
		})
	}
}

func TestSkillRootsTrimsWhitespaceAndDedups(t *testing.T) {
	tmp := t.TempDir()
	workspace := filepath.Join(tmp, "workspace")
	global := filepath.Join(tmp, "global")
	builtin := filepath.Join(tmp, "builtin")

	sl := NewSkillsLoader(workspace, "  "+global+"  ", "\t"+builtin+"\n")
	roots := sl.SkillRoots()

	assert.Equal(t, []string{
		filepath.Join(workspace, "skills"),
		global,
		builtin,
	}, roots)
}
