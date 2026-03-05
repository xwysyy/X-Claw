# X-Claw Refactor Plan

## Goals

- Eliminate god files that mix orchestration, transport, persistence, and presentation.
- Keep external behavior stable while making future changes local and testable.
- Reduce cross-package coupling so `agent`, `tools`, `config`, `httpapi`, and `channels` can evolve independently.
- Prefer extraction and boundary cleanup over risky rewrites.

## Refactor Principles

- Preserve runtime behavior first; make architecture better without silently changing product semantics.
- Split by responsibility, not by arbitrary line count.
- Keep public APIs stable unless a dedicated migration is prepared.
- Add or keep focused tests around refactored seams.
- Land the work in slices that compile and validate independently.

## Current Hotspots

### 1. Agent orchestration is over-centralized

- `pkg/agent/loop.go` owns lifecycle, routing, permission mode, run preparation, LLM iteration, tool execution, handoff, token accounting, media handling, and notification flow.
- Risk: one change touches unrelated behavior and makes regressions hard to isolate.

### 2. Web tooling mixes too many layers

- `pkg/tools/web.go` bundles HTTP client setup, API key pooling, multiple search providers, content extraction, caching, fetch logic, and tool UX formatting.
- Risk: providers, transport, parsing, and result formatting are tightly coupled.

### 3. Config is still a monolith

- `pkg/config/config.go`, `pkg/config/defaults.go`, and `pkg/config/migration.go` still centralize schema, JSON shape, validation, defaults, and migrations.
- Risk: every new option increases merge conflicts and validation complexity.

### 4. Console/API boundaries are blurred

- `pkg/httpapi/console.go` mixes HTTP handlers, file access, session inspection, streaming, and local path validation.
- Risk: difficult to test transport logic separately from local I/O and domain queries.

### 5. Channel adapters repeat platform-specific plumbing

- `pkg/channels/manager.go` and several platform files duplicate lifecycle, event normalization, media download, and mention parsing patterns.
- Risk: bugs are fixed per platform instead of once in a reusable adapter layer.

## Target Architecture

### Agent layer

- `AgentLoop` remains the facade.
- Message routing, run preparation, LLM iteration, and run finalization live in separate files/types.
- Long-running state for a single run is stored in a dedicated internal runner struct, not scattered local variables.

### Tools layer

- Shared web transport helpers, search providers, fetch/cache logic, extraction helpers, and tool entrypoints live in distinct files.
- Provider-specific code stops depending on fetch formatting details.

### Config layer

- Split schema types, defaults, validation, and migrations into separate files.
- Keep loader entrypoints stable while reducing the blast radius of new settings.

### API layer

- Separate HTTP transport handlers from console/query services and filesystem access helpers.
- Keep path safety and streaming as reusable helpers rather than inline logic.

### Channels layer

- Pull common adapter behavior into reusable helpers/interfaces.
- Leave only provider-specific protocol differences in each channel implementation.

## Execution Phases

### Phase 1: Break the biggest god files

- Refactor `pkg/agent/loop.go` into smaller responsibility-focused files.
- Refactor `pkg/tools/web.go` into cohesive files without changing behavior.
- Validate `pkg/agent` and `pkg/tools` first.

### Phase 2: Stabilize boundaries around orchestration

- Introduce clearer internal types for inbound routing, run preparation, and iteration state.
- Reduce implicit coupling between session state, tracing, and tool execution.
- Add regression coverage around handoff, plan mode, tool loops, and history sanitization.

### Phase 3: Decompose config

- Split config schema, defaults, validation, and migration responsibilities.
- Preserve `LoadConfig`, `SaveConfig`, and existing JSON contracts.

### Phase 4: Split console/API responsibilities

- Extract services for session inspection, file browsing/tailing, and token/runtime diagnostics.
- Keep handlers thin and transport-focused.

### Phase 5: Unify channel adapter patterns

- Extract shared send/download/event normalization helpers.
- Reduce per-platform copy/paste in `pkg/channels`.

## This Change Set

- Document the full refactor roadmap.
- Immediately land Phase 1 work on the two highest-impact hotspots:
  - `pkg/agent` orchestration pipeline
  - `pkg/tools` web tooling structure

## Validation Strategy

- Package-level regression tests before broad repo-wide runs.
- Prioritize:
  - `go test ./pkg/agent -count=1`
  - `go test ./pkg/tools -count=1`
- Follow with broader validation only after hotspot packages are stable.

## Non-Goals

- No product behavior redesign in the same patch.
- No repo-wide renaming churn without architectural payoff.
- No speculative abstraction that is not justified by an existing hotspot.
