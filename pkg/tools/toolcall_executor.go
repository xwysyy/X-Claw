package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/auditlog"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/utils"
)

// ToolCallParallelConfig configures in-batch parallel execution for tool calls.
type ToolCallParallelConfig struct {
	Enabled        bool
	MaxConcurrency int
	Mode           string
	// ToolPolicyOverrides allows per-tool policy overrides.
	// Values: "serial_only" or "parallel_read_only".
	ToolPolicyOverrides map[string]string
}

// ToolCallExecutionOptions controls how tool calls are executed.
type ToolCallExecutionOptions struct {
	Channel  string
	ChatID   string
	SenderID string

	// PlanMode enables the "plan" permission mode (Plan Mode, ROADMAP.md:1225).
	// When true, restricted tools are denied (typically side-effect tools).
	PlanMode bool
	// PlanRestrictedTools are denied while PlanMode is true.
	PlanRestrictedTools []string
	// PlanRestrictedPrefixes are denied while PlanMode is true.
	PlanRestrictedPrefixes []string

	// Workspace is the agent workspace path used for optional on-disk tool tracing.
	// When empty, tracing falls back to Trace.Dir (if set) or is disabled.
	Workspace string
	// SessionKey is a stable identifier for grouping tool traces on disk.
	// In agent mode this is typically the session key; when empty we fallback
	// to channel/chatID.
	SessionKey string
	// RunID associates tool traces with one durable run trace (Phase E1/E2).
	// When empty, per-run policy ledger features (confirm/idempotency) are disabled.
	RunID string
	// IsResume indicates this tool batch belongs to a resume_last_task flow (Phase E2).
	// Used by tool policy confirmation gating mode "resume_only".
	IsResume bool

	// Policy applies centralized tool guardrails (Phase D2).
	Policy config.ToolPolicyConfig
	// PolicyTags are attached to tool trace events and snapshots.
	// When empty, no tags are recorded.
	PolicyTags map[string]string

	// Estop enables the global kill switch for tool execution (ROADMAP.md:1138).
	// It is evaluated before Plan Mode / tool policy and applies to all tools
	// (built-in + MCP) through this executor chokepoint.
	Estop config.EstopConfig

	Iteration int
	LogScope  string

	Parallel ToolCallParallelConfig

	Trace ToolTraceOptions

	// MaxResultChars truncates ToolResult.ForLLM/ForUser to cap memory usage.
	// 0 disables truncation.
	MaxResultChars int

	// ErrorTemplate optionally wraps tool errors into a structured, self-recoverable
	// template for the LLM (A3 in ROADMAP.md).
	//
	// This is executor-level (not tool-specific) so we can standardize error recovery
	// without changing each tool's implementation.
	ErrorTemplate ToolErrorTemplateOptions

	// AsyncCallbackForCall creates a callback for async-capable tools.
	// It may be nil when async callbacks are not needed.
	AsyncCallbackForCall func(call providers.ToolCall) AsyncCallback
}

// ToolCallExecution captures one tool call execution result.
type ToolCallExecution struct {
	ToolCall   providers.ToolCall
	Result     *ToolResult
	DurationMS int64
}

// ExecuteToolCalls executes tool calls with optional bounded parallelism while
// preserving output order exactly as provided in the input slice.
func ExecuteToolCalls(
	ctx context.Context,
	registry *ToolRegistry,
	toolCalls []providers.ToolCall,
	opts ToolCallExecutionOptions,
) []ToolCallExecution {
	if len(toolCalls) == 0 {
		return nil
	}
	batchStart := time.Now()

	scope := opts.LogScope
	if scope == "" {
		scope = "tool"
	}

	traceWriter := newToolTraceWriter(opts, scope)
	policy := newToolPolicy(opts.Workspace, opts.SessionKey, opts.RunID, opts.IsResume, opts.Policy)
	planGate := newPlanModeGate(opts.PlanMode, opts.PlanRestrictedTools, opts.PlanRestrictedPrefixes)
	var estopState EstopState
	estopEnabled := false
	estopLoadErr := error(nil)
	if opts.Estop.Enabled && strings.TrimSpace(opts.Workspace) != "" {
		estopEnabled = true
		estopState, estopLoadErr = LoadEstopState(opts.Workspace)
		if estopLoadErr != nil && opts.Estop.FailClosed {
			estopState = EstopState{
				Mode: EstopModeKillAll,
				Note: "fail-closed: " + estopLoadErr.Error(),
			}.Normalized()
			estopLoadErr = nil
		}
	}

	results := make([]ToolCallExecution, len(toolCalls))
	parallelCount := 0
	serialCount := 0
	mode := normalizeParallelMode(opts.Parallel.Mode)

	shouldParallelize := func(tc providers.ToolCall) bool {
		if registry == nil {
			return false
		}
		if !opts.Parallel.Enabled {
			return false
		}
		if opts.Parallel.MaxConcurrency == 1 {
			return false
		}
		if !registry.IsParallelInstanceSafe(tc.Name) {
			return false
		}
		if override, ok := getOverridePolicy(tc.Name, opts.Parallel.ToolPolicyOverrides); ok {
			return override == ToolParallelReadOnly
		}
		switch mode {
		case ParallelToolsModeAll:
			return true
		case ParallelToolsModeReadOnlyOnly:
			return registry.CanRunToolCallInParallel(tc.Name, ParallelToolsModeReadOnlyOnly)
		default:
			return false
		}
	}

	runOne := func(idx int) {
		tc := toolCalls[idx]
		argsJSON, _ := json.Marshal(tc.Arguments)
		redactedArgsJSON := argsJSON
		if policy != nil && !policy.policyDisabled() {
			redactedArgsJSON = policy.redactJSONBytes(argsJSON)
		}
		argsPreview := utils.Truncate(string(redactedArgsJSON), 200)
		logger.InfoCF(scope, fmt.Sprintf("Tool call: %s(%s)", tc.Name, argsPreview),
			map[string]any{
				"tool":      tc.Name,
				"iteration": opts.Iteration,
			})

		var asyncCallback AsyncCallback
		if opts.AsyncCallbackForCall != nil {
			asyncCallback = opts.AsyncCallbackForCall(tc)
		}

		start := time.Now()
		if traceWriter != nil {
			traceWriter.RecordStart(start, opts.Iteration, tc, redactedArgsJSON)
		}

		policyDecision := ""
		policyReason := ""
		policyTimeoutMS := 0
		idempotencyKey := ""

		var toolResult *ToolResult
		execCtx := withExecutionRunID(withExecutionSessionKey(withExecutionIsResume(ctx, opts.IsResume), opts.SessionKey), opts.RunID)
		var cancel context.CancelFunc = func() {}
		if policy != nil && !policy.policyDisabled() {
			execCtx, cancel, policyTimeoutMS = policy.toolTimeoutContext(execCtx)
		}
		defer cancel()

		// Estop (ROADMAP.md:1138): global kill switch / freeze layer.
		if toolResult == nil && estopEnabled && strings.ToLower(strings.TrimSpace(tc.Name)) != "tool_confirm" {
			if estopLoadErr != nil {
				policyDecision = "estop_deny"
				policyReason = "estop state load error: " + estopLoadErr.Error()
				toolResult = ErrorResult("ESTOP_DENY: " + policyReason)
			} else {
				allowed, reason := true, ""
				if denied, r := estopState.DeniesTool(tc.Name, tc.Arguments); denied {
					allowed, reason = false, r
				}
				if !allowed {
					policyDecision = "estop_deny"
					policyReason = reason
					toolResult = ErrorResult(
						fmt.Sprintf(
							"ESTOP_DENY: tool %q blocked by estop (%s). "+
								"If this is unexpected, disable estop via /api/estop or CLI.",
							tc.Name,
							strings.TrimSpace(reason),
						),
					)
				}
			}
		}

		// Plan Mode (ROADMAP.md:1225): deny side-effect tools during planning phase.
		if toolResult == nil && planGate != nil && planGate.Enabled() && strings.ToLower(strings.TrimSpace(tc.Name)) != "tool_confirm" {
			allowed, reason := planGate.Allows(tc.Name)
			if !allowed {
				policyDecision = string(toolPolicyDecisionDeny)
				policyReason = reason
				toolResult = planGate.DeniedResult(tc.Name, reason)
			}
		}

		// Phase D2: centralized tool policy layer (allow/deny, confirm, idempotency).
		if policy != nil && !policy.policyDisabled() && strings.ToLower(strings.TrimSpace(tc.Name)) != "tool_confirm" {
			allowed, reason := policy.isToolAllowed(tc.Name)
			if !allowed {
				policyDecision = string(toolPolicyDecisionDeny)
				policyReason = reason
				toolResult = policy.buildDeniedResult(tc.Name, reason)
			}
		}

		// Phase E2: idempotency (avoid repeated side effects on resume).
		//
		// We always compute the idempotency key (so the first attempt records it),
		// but we only replay cached outputs during resume flows.
		if toolResult == nil && policy != nil && !policy.policyDisabled() && policy.shouldBeIdempotent(tc.Name) && strings.TrimSpace(opts.RunID) != "" {
			idempotencyKey = toolIdempotencyKey(tc.Name, argsJSON)
			if policy.isResume && policy.store != nil && policy.idempotencyCacheResult {
				if cached, ok := policy.store.GetCachedExecution(idempotencyKey); ok {
					policyDecision = string(toolPolicyDecisionIdempotentReplay)
					toolResult = policy.buildIdempotentReplayResult(tc.Name, idempotencyKey, cached)
				}
			}
		}

		// Phase E2: confirmation gate for side-effect tools (two-phase commit).
		if toolResult == nil && policy != nil && !policy.policyDisabled() && policy.shouldRequireConfirmation(tc.Name) && strings.TrimSpace(opts.RunID) != "" {
			if idempotencyKey == "" {
				idempotencyKey = toolIdempotencyKey(tc.Name, argsJSON)
			}
			if policy.store != nil && policy.store.IsConfirmed(idempotencyKey) {
				// confirmed; proceed
			} else if policy.store == nil || !policy.store.enabled {
				policyDecision = string(toolPolicyDecisionConfirmRequired)
				policyReason = "confirmation gate is enabled but policy store is unavailable (missing run_id/session_key?)"
				toolResult = policy.buildConfirmRequiredResult(tc.Name, idempotencyKey, argsPreview)
			} else {
				policyDecision = string(toolPolicyDecisionConfirmRequired)
				toolResult = policy.buildConfirmRequiredResult(tc.Name, idempotencyKey, argsPreview)
			}
		}

		executed := false
		if toolResult == nil {
			if registry != nil {
				toolResult = registry.ExecuteWithContext(
					execCtx,
					tc.Name,
					tc.Arguments,
					opts.Channel,
					opts.ChatID,
					opts.SenderID,
					asyncCallback,
				)
				executed = true
			} else {
				toolResult = ErrorResult("No tools available")
			}
			if policyDecision == "" {
				policyDecision = string(toolPolicyDecisionAllow)
			}
		}

		if toolResult == nil {
			toolResult = ErrorResult(fmt.Sprintf("tool %q returned nil result", tc.Name)).
				WithError(fmt.Errorf("tool %q returned nil result", tc.Name))
		}

		if toolResult.IsError && opts.ErrorTemplate.Enabled && !shouldSkipErrorTemplate(toolResult.ForLLM) {
			applyToolErrorTemplate(registry, tc, redactedArgsJSON, toolResult, opts)
		}

		// Apply redaction (always for audit; optional for return).
		auditResult := toolResult
		returnedResult := toolResult
		if policy != nil && !policy.policyDisabled() {
			auditResult = policy.redactToolResultForAudit(toolResult)
			returnedResult = policy.redactToolResultForReturn(toolResult)
		}

		// Resource budget: cap result sizes to keep history/trace memory stable.
		if opts.MaxResultChars > 0 {
			truncateToolResult(auditResult, opts.MaxResultChars)
			truncateToolResult(returnedResult, opts.MaxResultChars)
		}

		// Phase E2: record idempotent execution output for replay on resume.
		if executed && policy != nil && !policy.policyDisabled() && policy.store != nil && policy.shouldBeIdempotent(tc.Name) && strings.TrimSpace(opts.RunID) != "" && idempotencyKey != "" && policyDecision != string(toolPolicyDecisionIdempotentReplay) {
			if err := policy.store.RecordExecution(opts.RunID, opts.SessionKey, tc.Name, tc.ID, idempotencyKey, argsPreview, auditResult); err != nil {
				logger.WarnCF(scope, "Tool policy: failed to record idempotency ledger", map[string]any{
					"tool": tc.Name,
					"err":  err.Error(),
				})
			}
		}

		duration := time.Since(start)
		if traceWriter != nil {
			traceWriter.RecordEnd(start.Add(duration), opts.Iteration, tc, redactedArgsJSON, auditResult, duration, policyDecision, policyReason, policyTimeoutMS, idempotencyKey)
		}

		// Phase H3 (ROADMAP_V2.md): append-only operational audit log (best-effort).
		if strings.TrimSpace(opts.Workspace) != "" {
			errText := ""
			if auditResult != nil && auditResult.Err != nil {
				errText = auditResult.Err.Error()
			}
			resultPreview := ""
			if auditResult != nil {
				resultPreview = utils.Truncate(strings.TrimSpace(auditResult.ForLLM), 400)
			}
			if errText == "" && auditResult != nil && auditResult.IsError {
				errText = resultPreview
			}
			auditlog.Record(opts.Workspace, auditlog.Event{
				Type: "tool.executed",

				Source: strings.TrimSpace(scope),

				RunID:      strings.TrimSpace(opts.RunID),
				SessionKey: strings.TrimSpace(opts.SessionKey),
				Channel:    strings.TrimSpace(opts.Channel),
				ChatID:     strings.TrimSpace(opts.ChatID),
				SenderID:   strings.TrimSpace(opts.SenderID),
				Iteration:  opts.Iteration,

				Tool:       strings.TrimSpace(tc.Name),
				ToolCallID: strings.TrimSpace(tc.ID),

				PolicyDecision:  strings.TrimSpace(policyDecision),
				PolicyReason:    utils.Truncate(strings.TrimSpace(policyReason), 400),
				PolicyTimeoutMS: policyTimeoutMS,
				IdempotencyKey:  strings.TrimSpace(idempotencyKey),

				DurationMS:    duration.Milliseconds(),
				IsError:       auditResult != nil && auditResult.IsError,
				Error:         utils.Truncate(strings.TrimSpace(errText), 1200),
				ArgsPreview:   utils.Truncate(strings.TrimSpace(argsPreview), 500),
				ResultPreview: resultPreview,
			})
		}

		results[idx] = ToolCallExecution{
			ToolCall:   tc,
			Result:     returnedResult,
			DurationMS: duration.Milliseconds(),
		}
	}

	runParallelBatch := func(batch []int) {
		if len(batch) == 0 {
			return
		}

		maxConc := opts.Parallel.MaxConcurrency
		if maxConc <= 0 || maxConc > len(batch) {
			maxConc = len(batch)
		}
		if maxConc <= 1 {
			for _, idx := range batch {
				runOne(idx)
			}
			return
		}

		logger.DebugCF(scope, "Executing parallel tool batch", map[string]any{
			"iteration":     opts.Iteration,
			"batch_size":    len(batch),
			"max_parallel":  maxConc,
			"parallel_mode": mode,
		})

		jobs := make(chan int)
		var wg sync.WaitGroup
		for i := 0; i < maxConc; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for idx := range jobs {
					runOne(idx)
				}
			}()
		}

		for _, idx := range batch {
			jobs <- idx
		}
		close(jobs)
		wg.Wait()
	}

	parallelBatch := make([]int, 0, len(toolCalls))
	flushParallelBatch := func() {
		if len(parallelBatch) == 0 {
			return
		}
		runParallelBatch(parallelBatch)
		parallelBatch = parallelBatch[:0]
	}

	for i, tc := range toolCalls {
		if shouldParallelize(tc) {
			parallelCount++
			parallelBatch = append(parallelBatch, i)
			continue
		}
		serialCount++
		flushParallelBatch()
		runOne(i)
	}
	flushParallelBatch()

	errorCount := 0
	durations := make([]int64, 0, len(results))
	for _, executed := range results {
		if executed.Result != nil && executed.Result.IsError {
			errorCount++
		}
		durations = append(durations, executed.DurationMS)
	}
	p50, p95, avg, max := summarizeDurations(durations)

	logger.InfoCF(scope, "Tool call batch summary", map[string]any{
		"iteration":                 opts.Iteration,
		"tool_parallel_enabled":     opts.Parallel.Enabled,
		"max_tool_concurrency":      opts.Parallel.MaxConcurrency,
		"parallel_tools_mode":       mode,
		"parallel_candidate_count":  parallelCount,
		"serial_count":              serialCount,
		"total":                     len(toolCalls),
		"error_count":               errorCount,
		"batch_duration_ms":         time.Since(batchStart).Milliseconds(),
		"tool_call_duration_p50_ms": p50,
		"tool_call_duration_p95_ms": p95,
		"tool_call_duration_avg_ms": avg,
		"tool_call_duration_max_ms": max,
	})

	return results
}

func truncateToolResult(result *ToolResult, maxChars int) {
	if result == nil || maxChars <= 0 {
		return
	}
	if strings.TrimSpace(result.ForLLM) != "" {
		result.ForLLM = utils.Truncate(result.ForLLM, maxChars)
	}
	if strings.TrimSpace(result.ForUser) != "" {
		result.ForUser = utils.Truncate(result.ForUser, maxChars)
	}
}

func normalizeParallelMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", ParallelToolsModeReadOnlyOnly:
		return ParallelToolsModeReadOnlyOnly
	case ParallelToolsModeAll:
		return ParallelToolsModeAll
	default:
		return ""
	}
}

func getOverridePolicy(toolName string, overrides map[string]string) (ToolParallelPolicy, bool) {
	if len(overrides) == 0 {
		return "", false
	}
	raw, ok := overrides[toolName]
	if !ok {
		raw, ok = overrides[strings.ToLower(strings.TrimSpace(toolName))]
	}
	if !ok {
		return "", false
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(ToolParallelSerialOnly):
		return ToolParallelSerialOnly, true
	case string(ToolParallelReadOnly):
		return ToolParallelReadOnly, true
	default:
		return "", false
	}
}

func summarizeDurations(durations []int64) (p50, p95, avg, max int64) {
	if len(durations) == 0 {
		return 0, 0, 0, 0
	}

	sorted := append([]int64(nil), durations...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	total := int64(0)
	for _, d := range sorted {
		total += d
	}
	avg = total / int64(len(sorted))
	max = sorted[len(sorted)-1]
	p50 = percentileInt64(sorted, 0.50)
	p95 = percentileInt64(sorted, 0.95)
	return p50, p95, avg, max
}

func percentileInt64(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 1 {
		return sorted[len(sorted)-1]
	}
	// Nearest-rank percentile: rank = ceil(p*n), index = rank-1.
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
