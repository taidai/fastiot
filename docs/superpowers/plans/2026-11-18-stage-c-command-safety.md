# Stage C Command Safety Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Apply one safety verdict before every manual, named and automatic device write, with accepted/confirmed/failed/unknown results and no lost failures.

**Architecture:** A safety verdict module evaluates target and requested values. The command manager applies it before its existing dispatcher; product adapters reuse it instead of separate write paths.

**Tech Stack:** Go 1.27.3, existing EMS command/runtime/decision tests, local simulators.

**Spec:** `docs/superpowers/specs/2026-11-18-stage-c-command-safety-design.md`

## Global Constraints

- No Stage F deletion, real device connections or physical writes.
- Manual takeover blocks automatic output only, not authorized human commands.
- `submitted/superseded` remain legacy projections; accepted/confirmed/failed/unknown are canonical.
- No extra clamp for Modbus outputs without a declared envelope; no readback means submitted, not confirmed.

## Review Focus

- Out-of-range request is clamped only with a declared range and reason.
- Interlock/manual takeover/refusal prevents dispatcher writes.
- Disabled/read-only targets and missing required inputs are refused before writing.
- Named command failures retain reason and reach existing alarm/delivery evidence.
- Duplicate request IDs never write twice; no mapped readback yields submitted.

---

### Task 1: Safety verdict module

**Files:**
- Create: `internal/ems/safety/verdict.go`, `internal/ems/safety/verdict_test.go`
- Modify: `internal/ems/safety/safety.go` only if adapter code requires it

**Interfaces:**
- Produces: `safety.Request{Output string, Requested signal.Value, Current *float64, Spec *Output, Manual bool, Disabled bool, ReadOnly bool}` and `safety.Verdict{Decision, Value, Reason string}` where Decision is `allowed`, `clamped` or `refused`.

- [ ] **Step 1: Write failing verdict table tests**

Cover declared range clamping, missing range no extra clamp, manual takeover, disabled, read-only, missing required input, failed condition, unavailable required input, and unspecified output allowed. Assert decision, value and reason; manual must not disable an otherwise valid human command when the target itself is valid.

- [ ] **Step 2: Run tests and watch them fail**

Run: `go test ./internal/ems/safety -count=1`
Expected: verdict types missing.

- [ ] **Step 3: Implement the verdict**

Reuse existing range/condition semantics and freshness from signal.Sample. Refusal always preserves the original request; clamp only with a declared range or capability limit. No dispatcher calls belong in this module.

- [ ] **Step 4: Run tests and commit**

Run: `go test -p 1 -count=1 ./internal/ems/safety`
Expected: PASS.

```bash
git add docs/superpowers/specs/2026-11-18-stage-c-command-safety-design.md docs/superpowers/plans/2026-11-18-stage-c-command-safety.md internal/ems/safety
git commit -m "feat: decide command safety before writing"
```

### Task 2: Command manager and dispatcher safety seam

**Files:**
- Modify: `internal/ems/command/command.go`, `internal/ems/command/command_test.go`, `internal/ems/runtime/service.go`, `internal/ems/runtime/service_test.go`

**Interfaces:**
- `command.Manager.Guard func(command.GuardRequest) safety.Verdict` optional; nil preserves existing request semantics.
- `GuardRequest{DeviceID, Point string, Writes []device.Write, Automatic bool}` maps to safety.Request with target availability.

- [ ] **Step 1: Add failing manager tests**

With a guard that clamps or refuses, prove dispatcher is not called for refusal and accepted output uses the verdict value. Prove failed writes remain visible with reason, unknown remains unknown, and NoReadback remains submitted. Duplicate IDs never write twice.

- [ ] **Step 2: Run tests and watch them fail**

Run: `go test -p 1 -count=1 ./internal/ems/command ./internal/ems/runtime`
Expected: Guard missing and/or unsafe write reaches dispatcher.

- [ ] **Step 3: Implement the guard before dispatch**

Manager evaluates once after request normalization and before Writer.Write. Accepted writes are replaced with clamped writes when allowed; refused becomes failed without dispatch. Existing state/readback behavior remains.

`serviceDispatcher` provides the production guard for known Modbus points using safety.Request; other protocol adapters preserve existing behavior.

- [ ] **Step 4: Run command/runtime tests and commit**

Run: `go test -p 1 -count=1 ./internal/ems/command ./internal/ems/runtime`
Expected: PASS.

```bash
git add internal/ems/command internal/ems/runtime
git commit -m "feat: guard command dispatch with one safety verdict"
```

### Task 3: Automatic write migration, evidence and review

**Files:**
- Modify: `internal/decision/client.go`, `internal/decision/allocator_run.go`, `internal/decision/client_integration_test.go`, `internal/decision/allocator_integration_test.go`, `evidence/stage-c-command-safety.json`

- [ ] **Step 1: Add failing automatic write tests**

Prove automatic actions and allocator writes use the same guard and refusal produces no SIOT valueSet write. Authorized manual action remains allowed. Prove named-command failure retains its reason in the command/alarm evidence.

- [ ] **Step 2: Implement migration**

Replace direct automatic SIOT writes with the existing command manager seam where available; keep readback and allocation ordering. Do not change decision or allocator public interfaces.

- [ ] **Step 3: Run full regression**

Run: `go test -p 1 -count=1 ./...`
Run: frontend build plus navigation, consistency, observations, acceptance and capabilities checks.
Expected: all Go packages and frontend checks pass.

- [ ] **Step 4: Independent Standards/Spec review and evidence**

Review against issue #2 Stage C. Fix Critical/Important findings once with RED→GREEN tests. Record `evidence/stage-c-command-safety.json` with guard cases, no-write refusals, duplicate behavior, command failure evidence and remaining Stage C work. Commit evidence and review fixes with required attribution.
