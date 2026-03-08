package providers

import (
	"encoding/json"
	"strings"

	"github.com/xwysyy/X-Claw/pkg/logger"
)

// --- Request building ---

type antigravityRequest struct {
	Contents     []antigravityContent     `json:"contents"`
	Tools        []antigravityTool        `json:"tools,omitempty"`
	SystemPrompt *antigravitySystemPrompt `json:"systemInstruction,omitempty"`
	Config       *antigravityGenConfig    `json:"generationConfig,omitempty"`
}

type antigravityContent struct {
	Role  string            `json:"role"`
	Parts []antigravityPart `json:"parts"`
}

type antigravityPart struct {
	Text                  string                       `json:"text,omitempty"`
	ThoughtSignature      string                       `json:"thoughtSignature,omitempty"`
	ThoughtSignatureSnake string                       `json:"thought_signature,omitempty"`
	FunctionCall          *antigravityFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse      *antigravityFunctionResponse `json:"functionResponse,omitempty"`
}

type antigravityFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

type antigravityFunctionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type antigravityTool struct {
	FunctionDeclarations []antigravityFuncDecl `json:"functionDeclarations"`
}

type antigravityFuncDecl struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

type antigravitySystemPrompt struct {
	Parts []antigravityPart `json:"parts"`
}

type antigravityGenConfig struct {
	MaxOutputTokens int     `json:"maxOutputTokens,omitempty"`
	Temperature     float64 `json:"temperature,omitempty"`
}

func (p *AntigravityProvider) buildRequest(
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
) antigravityRequest {
	req := antigravityRequest{}
	toolCallNames := make(map[string]string)

	// Build contents from messages
	for _, msg := range messages {
		switch msg.Role {
		case "system":
			req.SystemPrompt = &antigravitySystemPrompt{
				Parts: []antigravityPart{{Text: msg.Content}},
			}
		case "user":
			if msg.ToolCallID != "" {
				toolName := resolveToolResponseName(msg.ToolCallID, toolCallNames)
				// Tool result
				req.Contents = append(req.Contents, antigravityContent{
					Role: "user",
					Parts: []antigravityPart{{
						FunctionResponse: &antigravityFunctionResponse{
							Name: toolName,
							Response: map[string]any{
								"result": msg.Content,
							},
						},
					}},
				})
			} else {
				req.Contents = append(req.Contents, antigravityContent{
					Role:  "user",
					Parts: []antigravityPart{{Text: msg.Content}},
				})
			}
		case "assistant":
			content := antigravityContent{
				Role: "model",
			}
			if msg.Content != "" {
				content.Parts = append(content.Parts, antigravityPart{Text: msg.Content})
			}
			for _, tc := range msg.ToolCalls {
				toolName, toolArgs, thoughtSignature := normalizeStoredToolCall(tc)
				if toolName == "" {
					logger.WarnCF(
						"provider.antigravity",
						"Skipping tool call with empty name in history",
						map[string]any{
							"tool_call_id": tc.ID,
						},
					)
					continue
				}
				if tc.ID != "" {
					toolCallNames[tc.ID] = toolName
				}
				content.Parts = append(content.Parts, antigravityPart{
					ThoughtSignature:      thoughtSignature,
					ThoughtSignatureSnake: thoughtSignature,
					FunctionCall: &antigravityFunctionCall{
						Name: toolName,
						Args: toolArgs,
					},
				})
			}
			if len(content.Parts) > 0 {
				req.Contents = append(req.Contents, content)
			}
		case "tool":
			toolName := resolveToolResponseName(msg.ToolCallID, toolCallNames)
			req.Contents = append(req.Contents, antigravityContent{
				Role: "user",
				Parts: []antigravityPart{{
					FunctionResponse: &antigravityFunctionResponse{
						Name: toolName,
						Response: map[string]any{
							"result": msg.Content,
						},
					},
				}},
			})
		}
	}

	// Build tools (sanitize schemas for Gemini compatibility)
	if len(tools) > 0 {
		var funcDecls []antigravityFuncDecl
		for _, t := range tools {
			if t.Type != "function" {
				continue
			}
			params := sanitizeSchemaForGemini(t.Function.Parameters)
			funcDecls = append(funcDecls, antigravityFuncDecl{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				Parameters:  params,
			})
		}
		if len(funcDecls) > 0 {
			req.Tools = []antigravityTool{{FunctionDeclarations: funcDecls}}
		}
	}

	// Generation config
	config := &antigravityGenConfig{}
	if val, ok := options["max_tokens"]; ok {
		if maxTokens, ok := val.(int); ok && maxTokens > 0 {
			config.MaxOutputTokens = maxTokens
		} else if maxTokens, ok := val.(float64); ok && maxTokens > 0 {
			config.MaxOutputTokens = int(maxTokens)
		}
	}
	if temp, ok := options["temperature"].(float64); ok {
		config.Temperature = temp
	}
	if config.MaxOutputTokens > 0 || config.Temperature > 0 {
		req.Config = config
	}

	return req
}

func normalizeStoredToolCall(tc ToolCall) (string, map[string]any, string) {
	name := tc.Name
	args := tc.Arguments
	thoughtSignature := ""

	if name == "" && tc.Function != nil {
		name = tc.Function.Name
		thoughtSignature = tc.Function.ThoughtSignature
	} else if tc.Function != nil {
		thoughtSignature = tc.Function.ThoughtSignature
	}

	if args == nil {
		args = map[string]any{}
	}

	if len(args) == 0 && tc.Function != nil && tc.Function.Arguments != "" {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &parsed); err == nil && parsed != nil {
			args = parsed
		}
	}

	return name, args, thoughtSignature
}

func resolveToolResponseName(toolCallID string, toolCallNames map[string]string) string {
	if toolCallID == "" {
		return ""
	}

	if name, ok := toolCallNames[toolCallID]; ok && name != "" {
		return name
	}

	return inferToolNameFromCallID(toolCallID)
}

func inferToolNameFromCallID(toolCallID string) string {
	if !strings.HasPrefix(toolCallID, "call_") {
		return toolCallID
	}

	rest := strings.TrimPrefix(toolCallID, "call_")
	if idx := strings.LastIndex(rest, "_"); idx > 0 {
		candidate := rest[:idx]
		if candidate != "" {
			return candidate
		}
	}

	return toolCallID
}
