package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/xwysyy/X-Claw/pkg/logger"
)

const (
	antigravityBaseURL      = "https://cloudcode-pa.googleapis.com"
	antigravityDefaultModel = "gemini-3-flash"
	antigravityUserAgent    = "antigravity"
	antigravityXGoogClient  = "google-cloud-sdk vscode_cloudshelleditor/0.1"
	antigravityVersion      = "1.15.8"
	antigravityFetchTimeout = 15 * time.Second
)

var antigravityFetchClient = &http.Client{Timeout: antigravityFetchTimeout}

// AntigravityProvider implements LLMProvider using Google's Cloud Code Assist (Antigravity) API.
// This provider authenticates via Google OAuth and provides access to models like Claude and Gemini
// through Google's infrastructure.
type AntigravityProvider struct {
	tokenSource func() (string, string, error) // Returns (accessToken, projectID, error)
	httpClient  *http.Client
}

// NewAntigravityProvider creates a new Antigravity provider using stored auth credentials.
func NewAntigravityProvider() *AntigravityProvider {
	return &AntigravityProvider{
		tokenSource: createAntigravityTokenSource(),
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// Chat implements LLMProvider.Chat using the Cloud Code Assist v1internal API.
// The v1internal endpoint wraps the standard Gemini request in an envelope with
// project, model, request, requestType, userAgent, and requestId fields.
func (p *AntigravityProvider) Chat(
	ctx context.Context,
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
) (*LLMResponse, error) {
	accessToken, projectID, err := p.tokenSource()
	if err != nil {
		return nil, fmt.Errorf("antigravity auth: %w", err)
	}

	if model == "" || model == "antigravity" || model == "google-antigravity" {
		model = antigravityDefaultModel
	}
	// Strip provider prefixes if present
	model = strings.TrimPrefix(model, "google-antigravity/")
	model = strings.TrimPrefix(model, "antigravity/")

	logger.DebugCF("provider.antigravity", "Starting chat", map[string]any{
		"model":     model,
		"project":   projectID,
		"requestId": fmt.Sprintf("agent-%d-%s", time.Now().UnixMilli(), randomString(9)),
	})

	// Build the inner Gemini-format request
	innerRequest := p.buildRequest(messages, tools, model, options)

	// Wrap in v1internal envelope (matches pi-ai SDK format)
	envelope := map[string]any{
		"project":     projectID,
		"model":       model,
		"request":     innerRequest,
		"requestType": "agent",
		"userAgent":   antigravityUserAgent,
		"requestId":   fmt.Sprintf("agent-%d-%s", time.Now().UnixMilli(), randomString(9)),
	}

	bodyBytes, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	// Build API URL — uses Cloud Code Assist v1internal streaming endpoint
	apiURL := fmt.Sprintf("%s/v1internal:streamGenerateContent?alt=sse", antigravityBaseURL)

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	// Headers matching the pi-ai SDK antigravity format
	clientMetadata, _ := json.Marshal(map[string]string{
		"ideType":    "IDE_UNSPECIFIED",
		"platform":   "PLATFORM_UNSPECIFIED",
		"pluginType": "GEMINI",
	})
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("User-Agent", fmt.Sprintf("antigravity/%s linux/amd64", antigravityVersion))
	req.Header.Set("X-Goog-Api-Client", antigravityXGoogClient)
	req.Header.Set("Client-Metadata", string(clientMetadata))

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("antigravity API call: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		logger.ErrorCF("provider.antigravity", "API call failed", map[string]any{
			"status_code": resp.StatusCode,
			"response":    string(respBody),
			"model":       model,
		})

		return nil, p.parseAntigravityError(resp.StatusCode, respBody)
	}

	// Response is always SSE from streamGenerateContent — each line is "data: {...}"
	// with a "response" wrapper containing the standard Gemini response
	llmResp, err := p.parseSSEResponse(string(respBody))
	if err != nil {
		return nil, err
	}

	// Check for empty response (some models might return valid success but empty text)
	if llmResp.Content == "" && len(llmResp.ToolCalls) == 0 {
		return nil, fmt.Errorf(
			"antigravity: model returned an empty response (this model might be invalid or restricted)",
		)
	}

	return llmResp, nil
}

// GetDefaultModel returns the default model identifier.
func (p *AntigravityProvider) GetDefaultModel() string {
	return antigravityDefaultModel
}

// --- Response parsing ---

type antigravityJSONResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text                  string                   `json:"text,omitempty"`
				ThoughtSignature      string                   `json:"thoughtSignature,omitempty"`
				ThoughtSignatureSnake string                   `json:"thought_signature,omitempty"`
				FunctionCall          *antigravityFunctionCall `json:"functionCall,omitempty"`
			} `json:"parts"`
			Role string `json:"role"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		TotalTokenCount      int `json:"totalTokenCount"`
	} `json:"usageMetadata"`
}

func (p *AntigravityProvider) parseSSEResponse(body string) (*LLMResponse, error) {
	var contentParts []string
	var toolCalls []ToolCall
	var usage *UsageInfo
	var finishReason string

	scanner := bufio.NewScanner(strings.NewReader(body))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		// v1internal SSE wraps the Gemini response in a "response" field
		var sseChunk struct {
			Response antigravityJSONResponse `json:"response"`
		}
		if err := json.Unmarshal([]byte(data), &sseChunk); err != nil {
			continue
		}
		resp := sseChunk.Response

		for _, candidate := range resp.Candidates {
			for _, part := range candidate.Content.Parts {
				if part.Text != "" {
					contentParts = append(contentParts, part.Text)
				}
				if part.FunctionCall != nil {
					argumentsJSON, _ := json.Marshal(part.FunctionCall.Args)
					toolCalls = append(toolCalls, ToolCall{
						ID:        fmt.Sprintf("call_%s_%d", part.FunctionCall.Name, time.Now().UnixNano()),
						Name:      part.FunctionCall.Name,
						Arguments: part.FunctionCall.Args,
						Function: &FunctionCall{
							Name:      part.FunctionCall.Name,
							Arguments: string(argumentsJSON),
							ThoughtSignature: extractPartThoughtSignature(
								part.ThoughtSignature,
								part.ThoughtSignatureSnake,
							),
						},
					})
				}
			}
			if candidate.FinishReason != "" {
				finishReason = candidate.FinishReason
			}
		}

		if resp.UsageMetadata.TotalTokenCount > 0 {
			usage = &UsageInfo{
				PromptTokens:     resp.UsageMetadata.PromptTokenCount,
				CompletionTokens: resp.UsageMetadata.CandidatesTokenCount,
				TotalTokens:      resp.UsageMetadata.TotalTokenCount,
			}
		}
	}

	mappedFinish := "stop"
	if len(toolCalls) > 0 {
		mappedFinish = "tool_calls"
	}
	if finishReason == "MAX_TOKENS" {
		mappedFinish = "length"
	}

	return &LLMResponse{
		Content:      strings.Join(contentParts, ""),
		ToolCalls:    toolCalls,
		FinishReason: mappedFinish,
		Usage:        usage,
	}, nil
}

func extractPartThoughtSignature(thoughtSignature string, thoughtSignatureSnake string) string {
	if thoughtSignature != "" {
		return thoughtSignature
	}
	if thoughtSignatureSnake != "" {
		return thoughtSignatureSnake
	}
	return ""
}

// --- Schema sanitization ---

// Google/Gemini doesn't support many JSON Schema keywords that other providers accept.
var geminiUnsupportedKeywords = map[string]bool{
	"patternProperties":    true,
	"additionalProperties": true,
	"$schema":              true,
	"$id":                  true,
	"$ref":                 true,
	"$defs":                true,
	"definitions":          true,
	"examples":             true,
	"minLength":            true,
	"maxLength":            true,
	"minimum":              true,
	"maximum":              true,
	"multipleOf":           true,
	"pattern":              true,
	"format":               true,
	"minItems":             true,
	"maxItems":             true,
	"uniqueItems":          true,
	"minProperties":        true,
	"maxProperties":        true,
}

func sanitizeSchemaForGemini(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}

	result := make(map[string]any)
	for k, v := range schema {
		if geminiUnsupportedKeywords[k] {
			continue
		}
		// Recursively sanitize nested objects
		switch val := v.(type) {
		case map[string]any:
			result[k] = sanitizeSchemaForGemini(val)
		case []any:
			sanitized := make([]any, len(val))
			for i, item := range val {
				if m, ok := item.(map[string]any); ok {
					sanitized[i] = sanitizeSchemaForGemini(m)
				} else {
					sanitized[i] = item
				}
			}
			result[k] = sanitized
		default:
			result[k] = v
		}
	}

	// Ensure top-level has type: "object" if properties are present
	if _, hasProps := result["properties"]; hasProps {
		if _, hasType := result["type"]; !hasType {
			result["type"] = "object"
		}
	}

	return result
}

type AntigravityModelInfo struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	IsExhausted bool   `json:"is_exhausted"`
}

// --- Helpers ---

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// randomString is only used for request correlation IDs and does not need
// cryptographic strength.
func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}
