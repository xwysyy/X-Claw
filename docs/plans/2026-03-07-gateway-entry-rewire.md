# Gateway Entry Rewire Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Reconnect `x-claw gateway` to the existing full Gateway runtime so the slimmed mainline again exposes `/ready`, `/api/notify`, and `/console/` without re-expanding the command surface.

**Architecture:** Reuse the already-existing Gateway runtime in `cmd/x-claw/internal/gateway` instead of duplicating HTTP wiring in `internal/gateway`. Keep the slim command surface (`gateway` / `agent` / `version`) unchanged, but make the `gateway` command invoke the full runtime path that already owns channel startup, health/ready registration, notify/session HTTP APIs, and console assets.

**Tech Stack:** Go, Cobra, Docker Compose, existing `pkg/channels` / `pkg/health` / `pkg/httpapi` runtime.

---

### Task 1: Lock the regression with failing tests

**Files:**
- Modify: `cmd/x-claw/internal/gateway/command_test.go`
- Modify: `cmd/x-claw/internal/gateway/httpapi_test.go`
- Test: `cmd/x-claw/internal/gateway/command_test.go`

**Step 1: Write the failing test**

Add a test that proves the `gateway` command is wired to the full runtime entrypoint rather than the health-only core server path. Prefer a package-level injectable function variable so the test can observe which runner is invoked.

Add/extend a route registration test that explicitly guards the mainline routes:
- `/api/notify`
- `/api/resume_last_task`
- `/api/session_model`
- `/console/`
- `/api/console/`

**Step 2: Run test to verify it fails**

Run: `go test ./cmd/x-claw/internal/gateway -run 'TestNewGatewayCommand|TestBuildGatewayHTTPRegistrations_SlimSurface' -count=1`

Expected: failure showing the command still calls the wrong runtime path or lacks the new seam needed for verification.

**Step 3: Write minimal implementation**

Introduce only the smallest indirection needed for the command test and no behavior change yet beyond what the failing test requires.

**Step 4: Run test to verify it passes**

Run: `go test ./cmd/x-claw/internal/gateway -run 'TestNewGatewayCommand|TestBuildGatewayHTTPRegistrations_SlimSurface' -count=1`

Expected: PASS.

**Step 5: Commit**

Do not commit in this session unless explicitly requested.

### Task 2: Rewire the command entry to the full Gateway runtime

**Files:**
- Modify: `cmd/x-claw/internal/gateway/command.go`
- Modify: `cmd/x-claw/internal/gateway/helpers.go`
- Possibly modify: `internal/app/app.go`
- Test: `cmd/x-claw/internal/gateway/command_test.go`

**Step 1: Write/adjust the failing test**

If needed, refine the command test so it asserts the command's `RunE` reaches the package-local full runtime runner and no longer depends on `internal/app.RunGateway`.

**Step 2: Run test to verify it fails**

Run: `go test ./cmd/x-claw/internal/gateway -run TestNewGatewayCommand -count=1`

Expected: FAIL before rewiring.

**Step 3: Write minimal implementation**

Change `cmd/x-claw/internal/gateway/command.go` so `gateway` runs the existing full runtime path (`gatewayCmd(debug)` or an equivalent package-local runner). Keep CLI flags and aliases unchanged.

If `internal/app/app.go` becomes dead after this change, either:
- leave it unused if harmless, or
- remove/trim it only if required to keep the package tidy and compiling.

**Step 4: Run test to verify it passes**

Run: `go test ./cmd/x-claw/internal/gateway -run TestNewGatewayCommand -count=1`

Expected: PASS.

**Step 5: Commit**

Do not commit in this session unless explicitly requested.

### Task 3: Verify runtime behavior at the package level

**Files:**
- Modify if needed: `internal/gateway/server_test.go`
- Possibly create: `cmd/x-claw/internal/gateway/runtime_smoke_test.go`
- Test: `internal/gateway/server_test.go`

**Step 1: Write the failing test**

Add the smallest practical regression test that protects against the specific bug we just hit: the project can compile while the real `gateway` entry only serves `/health`.

This test can be command-level seam verification rather than a full live HTTP integration test if that is the most stable option.

**Step 2: Run test to verify it fails**

Run: `go test ./cmd/x-claw/internal/gateway ./internal/gateway -count=1`

Expected: FAIL before the regression guard is complete.

**Step 3: Write minimal implementation**

Implement only the supporting code necessary to make the regression guard stable.

**Step 4: Run test to verify it passes**

Run: `go test ./cmd/x-claw/internal/gateway ./internal/gateway -count=1`

Expected: PASS.

**Step 5: Commit**

Do not commit in this session unless explicitly requested.

### Task 4: Verify rebuilt container exposes the mainline endpoints again

**Files:**
- Modify only if required by verification fallout: `docker/docker-compose.yml`
- Verify against: `docker/Dockerfile`

**Step 1: Rebuild and restart the gateway container**

Run: `source ~/.zshrc && proxy_on && cd docker && docker compose -p x-claw --profile gateway up -d --build x-claw-gateway`

Expected: image rebuild succeeds and `x-claw-gateway` restarts.

**Step 2: Verify health and mainline endpoints**

Run:
- `curl -i -sS http://127.0.0.1:18790/health`
- `curl -i -sS http://127.0.0.1:18790/ready`
- `curl -i -sS -X POST http://127.0.0.1:18790/api/notify -H 'Content-Type: application/json' -d '{"content":"verify"}'`
- `curl -I -sS http://127.0.0.1:18790/console/`

Expected:
- `/health` returns `200`
- `/ready` is no longer `404`
- `/api/notify` is no longer `404` (status may be `200`, `400`, or `401` depending on config/auth)
- `/console/` is no longer `404`

**Step 3: Check logs and mounts**

Run:
- `docker logs --tail 120 x-claw-gateway`
- `docker inspect x-claw-gateway --format '{{json .Mounts}}'`

Expected: logs indicate the full runtime startup path, and the bind mount remains `/root/code/X-Claw/config/config.json`.

**Step 4: Commit**

Do not commit in this session unless explicitly requested.
