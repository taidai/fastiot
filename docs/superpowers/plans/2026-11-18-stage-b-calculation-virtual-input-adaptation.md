# Stage B Calculation and Virtual-Input Adaptation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reuse one observation verdict for calculations and virtual inputs without breaking constants, Epoch, TTL or restart verification.

**Architecture:** Observation supports an explicit no-expiry allowance for domain-approved constants. State exposes the same expiry, while calculation and virtual-input adapters map existing semantics into the verdict instead of duplicating it.

**Tech Stack:** Go 1.27.3, TypeScript, React, existing test fixtures and Browser preview.

**Spec:** `docs/superpowers/specs/2026-11-18-stage-b-calculation-virtual-input-adaptation-design.md`

## Global Constraints

- `AllowNoExpiry` is limited to explicit constants/fixed trials; ordinary Modbus with `MaxAge=0` stays `expiry_unconfigured`.
- Keep virtual-input public shape, Epoch, TTL, monotonic clock and restart verification unchanged.
- `now == valid_until` is usable; only `now > valid_until` is expired.
- No link-health work, formula changes, real device connections or physical writes.

## Review Focus

- Configuration constants remain good without expiry, not `expiry_unconfigured`.
- Manual/simulation expiry and restart `pending_verification` survive adaptation.
- Exact calculation expiry boundary versus one millisecond later.
- Ordinary Modbus without declared expiry remains unknown.
- Combined calculations use the earliest leaf expiry and retain prior values without presenting them as current.

---

### Task 1: Explicit no-expiry allowance and state expiry

**Files:**
- Modify: `internal/ems/observation/observation.go`, `internal/ems/observation/observation_test.go`, `internal/ems/state/state.go`, `internal/ems/state/state_test.go`

**Interfaces:**
- Produces: `observation.Input.AllowNoExpiry bool` and `state.Value.ExpiresAt time.Time`.

- [ ] **Step 1: Add failing tests**

Add an observation case proving `AllowNoExpiry=true` is `usable/good` with zero `ExpiresAt`; the same input with false remains `unknown/expiry_unconfigured`. Add a state test proving `ExpiresAt` equals the verdict expiry.

- [ ] **Step 2: Run tests and watch them fail**

Run: `go test -p 1 -count=1 ./internal/ems/observation ./internal/ems/state`
Expected: unknown fields and/or the no-expiry case fails.

- [ ] **Step 3: Implement the allowance and state field**

Only bypass `expiry_unconfigured` when `AllowNoExpiry` is true. `state.effective` copies `verdict.ExpiresAt` to `state.Value.ExpiresAt` without changing existing quality behavior.

- [ ] **Step 4: Run tests and commit**

Run: `go test -p 1 -count=1 ./internal/ems/observation ./internal/ems/state ./internal/emsapi`
Expected: PASS.

```bash
git add docs/superpowers/specs/2026-11-18-stage-b-calculation-virtual-input-adaptation-design.md docs/superpowers/plans/2026-11-18-stage-b-calculation-virtual-input-adaptation.md internal/ems/observation internal/ems/state
git commit -m "feat: allow explicit non-expiring observation inputs"
```

### Task 2: Calculation and virtual-input adapters

**Files:**
- Create: `internal/calculation/freshness.go`, `internal/calculation/freshness_test.go`
- Modify: `internal/calculation/evaluate.go`, `internal/calculation/evaluate_test.go`, `internal/appapi/calculation_runtime.go`, `internal/appapi/calculations.go`, `internal/appapi/calculation_runtime_integration_test.go`, `frontend/src/CalculationPoints.tsx`, `frontend/src/observations.ts`, `frontend/observations-check.mjs`

**Interfaces:**
- Produces: `calculation.Freshness(sample Sample, now time.Time) observation.Verdict`, `(Result).Freshness(now time.Time) observation.Verdict`, and `isObservationExpired(validUntil, evaluatedAt, elapsedMs)` for the calculation page.
- Uses `state.Value.ExpiresAt`; calculation never re-derives it with `SourceTime.Add(MaxAge)`.

- [ ] **Step 1: Add failing calculation and runtime tests**

Prove `Freshness` is good at `ValidUntil` and stale one millisecond later. Add runtime tests for a configuration constant staying good and a timed virtual input becoming stale. Keep existing restart `pending_verification` tests as guards.

- [ ] **Step 2: Run tests and watch them fail**

Run: `go test -p 1 -count=1 ./internal/calculation ./internal/appapi -run 'Freshness|ConfigurationConstant|TimedVirtual'`
Expected: freshness function missing and/or boundary is stale at equality.

- [ ] **Step 3: Implement the adapters**

`calculation.Freshness` converts quality strings and `ValidUntil` into observation input with no-expiry allowed for configuration/fixed trials. `EvaluateBatch` uses it for input expiry. appapi calculation responses use `Result.Freshness`. Calculation runtime uses `state.Value.ExpiresAt` and maps virtual-input `bad/stale/uninitialized` to `invalid/stale/unknown` through `observation.Evaluate`, with `AllowNoExpiry` only for configuration constants.

`CalculationPoints.expired` delegates to `isObservationExpired`, which uses `>` instead of `>=`; add direct boundary checks in `observations-check.mjs`.

- [ ] **Step 4: Run package and frontend tests**

Run: `go test -p 1 -count=1 ./internal/calculation ./internal/appapi`
Run: `npm --prefix frontend run build && npm --prefix frontend run check:observations`
Expected: all pass, including existing virtual-input restart and Epoch tests.

- [ ] **Step 5: Commit the adapter slice**

```bash
git add internal/calculation internal/appapi/calculation_runtime.go internal/appapi/calculations.go internal/appapi/calculation_runtime_integration_test.go frontend/src/CalculationPoints.tsx frontend/src/observations.ts frontend/observations-check.mjs
git commit -m "feat: adapt calculations and virtual inputs to observation verdicts"
```

### Task 3: Acceptance, review and evidence

**Files:**
- Create: `evidence/stage-b-calculation-virtual-input-adaptation.json`

- [ ] **Step 1: Verify real behavior**

Use existing calculation/virtual-input integration fixtures and the debug backend. Confirm a configuration constant remains usable, a timed input expires, and a calculation is good at its exact `valid_until` but stale afterward. Verify the calculation page shows the same state and retained values are labeled non-current. Restore fixture data and stop temporary servers.

- [ ] **Step 2: Run full regression**

Run: `go test -p 1 -count=1 ./...`
Run: frontend build plus navigation, consistency, observations, acceptance and capabilities checks.
Expected: all 35 Go packages and all frontend checks pass.

- [ ] **Step 3: Perform independent Standards/Spec review**

Review against the approved design and issue #2 Stage B. Fix Critical/Important findings once with RED→GREEN tests; do not add link health or change Epoch/TTL/public shapes.

- [ ] **Step 4: Record evidence and commit**

Write `evidence/stage-b-calculation-virtual-input-adaptation.json` with exact boundary results, restart checks, browser evidence and remaining link-health work. Commit evidence and review fixes with the required attribution trailer.
