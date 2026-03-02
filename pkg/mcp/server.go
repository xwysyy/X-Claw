package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"

	tmcp "trpc.group/trpc-go/trpc-mcp-go"
)

type ToolDef struct {
	Name        string
	Description string
	InputSchema map[string]any
}

type Server struct {
	cfg config.MCPServerConfig

	mu          sync.RWMutex
	client      tmcp.Connector
	initialized bool
	tools       []ToolDef
}

func NewServer(cfg config.MCPServerConfig) *Server {
	return &Server{cfg: cfg}
}

func (s *Server) Name() string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(s.cfg.Name)
}

func (s *Server) Timeout() time.Duration {
	if s == nil {
		return 0
	}
	if s.cfg.TimeoutSeconds <= 0 {
		return 0
	}
	return time.Duration(s.cfg.TimeoutSeconds) * time.Second
}

func (s *Server) ToolPrefix() string {
	if s == nil {
		return ""
	}
	p := strings.TrimSpace(s.cfg.ToolPrefix)
	if p == "" {
		p = "mcp_" + s.Name() + "_"
	}
	p = sanitizeToolName(p)
	if p != "" && !strings.HasSuffix(p, "_") {
		p += "_"
	}
	return p
}

func (s *Server) ensureConnected(ctx context.Context) error {
	if s == nil {
		return fmt.Errorf("mcp server is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	s.mu.RLock()
	ready := s.initialized && s.client != nil
	s.mu.RUnlock()
	if ready {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.initialized && s.client != nil {
		return nil
	}

	name := s.Name()
	if name == "" {
		return fmt.Errorf("mcp server name is empty")
	}

	transport := strings.ToLower(strings.TrimSpace(s.cfg.Transport))
	clientInfo := tmcp.Implementation{Name: "picoclaw", Version: "dev"}

	var client tmcp.Connector
	var err error

	switch transport {
	case "", "stdio":
		command := strings.TrimSpace(s.cfg.Command)
		if command == "" {
			return fmt.Errorf("mcp server %q: stdio transport requires command", name)
		}
		stdioCfg := tmcp.StdioTransportConfig{
			ServerParams: tmcp.StdioServerParameters{
				Command: command,
				Args:    s.cfg.Args,
			},
			Timeout: s.Timeout(),
		}
		client, err = tmcp.NewStdioClient(stdioCfg, clientInfo)

	case "sse":
		url := strings.TrimSpace(s.cfg.URL)
		if url == "" {
			return fmt.Errorf("mcp server %q: sse transport requires url", name)
		}
		var options []tmcp.ClientOption
		if len(s.cfg.Headers) > 0 {
			headers := http.Header{}
			for k, v := range s.cfg.Headers {
				headers.Set(k, v)
			}
			options = append(options, tmcp.WithHTTPHeaders(headers))
		}
		client, err = tmcp.NewSSEClient(url, clientInfo, options...)

	case "streamable", "streamable_http":
		url := strings.TrimSpace(s.cfg.URL)
		if url == "" {
			return fmt.Errorf("mcp server %q: streamable transport requires url", name)
		}
		var options []tmcp.ClientOption
		if len(s.cfg.Headers) > 0 {
			headers := http.Header{}
			for k, v := range s.cfg.Headers {
				headers.Set(k, v)
			}
			options = append(options, tmcp.WithHTTPHeaders(headers))
		}
		client, err = tmcp.NewClient(url, clientInfo, options...)

	default:
		return fmt.Errorf("mcp server %q: unsupported transport %q (supported: stdio, sse, streamable)", name, transport)
	}

	if err != nil {
		return fmt.Errorf("mcp server %q: connect failed: %w", name, err)
	}

	initCtx, cancel := s.withTimeout(ctx)
	defer cancel()
	if _, err := client.Initialize(initCtx, &tmcp.InitializeRequest{}); err != nil {
		_ = client.Close()
		return fmt.Errorf("mcp server %q: initialize failed: %w", name, err)
	}

	s.client = client
	s.initialized = true

	return nil
}

func (s *Server) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := s.Timeout()
	if timeout <= 0 {
		return ctx, func() {}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func (s *Server) ListTools(ctx context.Context) ([]ToolDef, error) {
	if err := s.ensureConnected(ctx); err != nil {
		return nil, err
	}

	s.mu.RLock()
	cached := append([]ToolDef(nil), s.tools...)
	client := s.client
	include := append([]string(nil), s.cfg.IncludeTools...)
	s.mu.RUnlock()

	if len(cached) > 0 {
		return cached, nil
	}
	if client == nil {
		return nil, fmt.Errorf("mcp server %q: client unavailable", s.Name())
	}

	listCtx, cancel := s.withTimeout(ctx)
	defer cancel()
	resp, err := client.ListTools(listCtx, &tmcp.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("mcp server %q: list_tools failed: %w", s.Name(), err)
	}

	includeSet := map[string]struct{}{}
	for _, name := range include {
		n := strings.TrimSpace(name)
		if n == "" {
			continue
		}
		includeSet[n] = struct{}{}
	}

	toolsOut := make([]ToolDef, 0, len(resp.Tools))
	for _, tool := range resp.Tools {
		original := strings.TrimSpace(tool.Name)
		if original == "" {
			continue
		}
		if len(includeSet) > 0 {
			if _, ok := includeSet[original]; !ok {
				continue
			}
		}

		params := map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
		if tool.InputSchema != nil {
			// Best-effort: convert MCP schema object into map[string]any for tools.Tool.Parameters().
			raw, err := json.Marshal(tool.InputSchema)
			if err == nil {
				var decoded map[string]any
				if err := json.Unmarshal(raw, &decoded); err == nil && decoded != nil {
					params = decoded
				} else if err != nil {
					logger.WarnCF("mcp", "Failed to decode MCP input schema (best-effort)", map[string]any{
						"server": s.Name(),
						"tool":   original,
						"error":  err.Error(),
					})
				}
			}
		}

		desc := strings.TrimSpace(tool.Description)
		if desc == "" {
			desc = original
		}

		toolsOut = append(toolsOut, ToolDef{
			Name:        original,
			Description: desc,
			InputSchema: params,
		})
	}
	sort.Slice(toolsOut, func(i, j int) bool { return toolsOut[i].Name < toolsOut[j].Name })

	s.mu.Lock()
	s.tools = toolsOut
	s.mu.Unlock()

	return toolsOut, nil
}

func (s *Server) CallTool(ctx context.Context, toolName string, args map[string]any) (*tmcp.CallToolResult, error) {
	if err := s.ensureConnected(ctx); err != nil {
		return nil, err
	}

	s.mu.RLock()
	client := s.client
	s.mu.RUnlock()
	if client == nil {
		return nil, fmt.Errorf("mcp server %q: client unavailable", s.Name())
	}

	toolCtx, cancel := s.withTimeout(ctx)
	defer cancel()
	req := &tmcp.CallToolRequest{}
	req.Params.Name = strings.TrimSpace(toolName)
	req.Params.Arguments = args
	resp, err := client.CallTool(toolCtx, req)
	if err != nil {
		return nil, fmt.Errorf("mcp server %q: call_tool %q failed: %w", s.Name(), toolName, err)
	}
	if resp == nil {
		return nil, fmt.Errorf("mcp server %q: call_tool %q returned nil", s.Name(), toolName)
	}
	return resp, nil
}

func (s *Server) Close() error {
	if s == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.client == nil {
		return nil
	}

	err := s.client.Close()
	s.client = nil
	s.initialized = false
	s.tools = nil

	if err != nil {
		return fmt.Errorf("mcp server %q: close failed: %w", s.Name(), err)
	}
	return nil
}
