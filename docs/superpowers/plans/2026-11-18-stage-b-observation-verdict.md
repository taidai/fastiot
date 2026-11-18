# Stage B Observation Verdict Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give ordinary observations one server freshness verdict and one frontend presentation, without local 15-second guesses.

**Architecture:** A new Go observation module owns quality/reason/expiry. State and EMS adapt it to existing APIs; frontend helpers consume the same `expires_at` and quality semantics, with no-declared-expiry represented as unknown rather than healthy.

**Tech Stack:** Go 1.27.3, TypeScript, React, existing Node check scripts and Browser preview tools.

**Spec:** `docs/superpowers/specs/2026-11-18-stage-b-observation-verdict-design.md`

## Global Constraints

- No link-health implementation, calculation `ValidUntil` changes, or virtual-input Epoch/TTL changes.
- No Stage A authorization changes, real device connections, or physical writes.
- Preserve existing quality reasons; `MaxAge <= 0` means `unknown` with `expiry_unconfigured`.
- Expiry uses `now.After(expiresAt)`: the exact boundary remains usable.
- Use existing public state/HTTP seams and real browser checks; do not add dependencies.

## Review Focus

- A real value without `expires_at` must be unknown, not “正常” or stale.
- Exact expiry boundary versus one millisecond later.
- Existing invalid/future reasons must not be overwritten by expiry logic.
- Client-side expiry must not leave reason “数据有效”.
- Missing/unconfigured observations must not be counted as stale in App or the assistant.

---

### Task 1: Go observation verdict and EMS state

**Files:**
- Create: `internal/ems/observation/observation.go`, `internal/ems/observation/observation_test.go`
- Modify: `internal/ems/state/state.go`, `internal/ems/state/state_test.go`, `internal/emsapi/emsapi.go`, `internal/emsapi/emsapi_test.go`

**Interfaces:**
- Produces: `observation.Input{Value signal.Value, Quality signal.Quality, Reason string, SourceTime, ReceivedTime time.Time, MaxAge time.Duration}`, `observation.Verdict{State string, Quality signal.Quality, Reason string, SourceTime, ReceivedTime, ExpiresAt time.Time}`, and `observation.Evaluate(input Input, now time.Time) Verdict`.
- State constants: `usable`, `stale`, `invalid`, `unknown`.

- [ ] **Step 1: Write failing verdict table tests**

Cover good before boundary, good exactly at boundary, stale one millisecond after boundary, future source time, explicit invalid reason, missing value, and `MaxAge=0`. Assert state, mapped `signal.Quality`, reason and `ExpiresAt`; `MaxAge=0` must be `unknown/expiry_unconfigured` with zero `ExpiresAt`.

- [ ] **Step 2: Run tests and watch them fail**

Run: `go test ./internal/ems/observation -count=1`
Expected: package does not exist.

- [ ] **Step 3: Implement `Evaluate` and adapt state/EMS**

`Evaluate` preserves explicit invalid reasons, maps missing/uninitialized to unknown, maps expiry to stale/`source_expired`, and computes `ExpiresAt` only for nonzero source time with `MaxAge>0`. `state.effective` maps verdict fields back to `state.Value`. EMS state uses the same verdict for `expires_at` instead of `SourceTime.Add(MaxAge)`.

Add a state test for `MaxAge=0` and an EMS test proving quality and `expires_at` come from the same input.

- [ ] **Step 4: Run package tests**

Run: `go test -p 1 -count=1 ./internal/ems/observation ./internal/ems/state ./internal/emsapi`
Expected: PASS, including existing state semantics except the intentional no-expiry unknown case.

- [ ] **Step 5: Commit the design, plan and Go slice**

```bash
git add docs/superpowers/specs/2026-11-18-stage-b-observation-verdict-design.md docs/superpowers/plans/2026-11-18-stage-b-observation-verdict.md internal/ems/observation internal/ems/state internal/emsapi
git commit -m "feat: resolve observation freshness in one module"
```

### Task 2: Frontend observation presentation and page migration

**Files:**
- Modify: `frontend/src/observations.ts`, `frontend/src/ScreenView.tsx`, `frontend/src/App.tsx`, `frontend/src/EnergyFlow.tsx`, `frontend/src/AssistantPanel.tsx`, `frontend/observations-check.mjs`

**Interfaces:**
- Consumes: server `quality`, `reason`, `source_time`, `received_time`, `expires_at`, `evaluated_at` and `useObservations().elapsedMs`.
- Produces: `observationForNode(node, source)` returning `describeObservation` for Modbus IO by `parent/serverID` and unknown for non-EMS nodes.

- [ ] **Step 1: Add failing presentation tests**

Extend `observations-check.mjs`: a good value with no `expires_at` is unknown with `expiry_unconfigured`; an expired value is stale and never says “数据有效”; exact boundary stays good. Add a Modbus IO node test for `observationForNode` and a non-EMS node test proving unknown.

- [ ] **Step 2: Run the check and watch it fail**

Run: `npm --prefix frontend run check:observations`
Expected: no-expiry currently reads as good/normal and helper is missing.

- [ ] **Step 3: Implement the shared presentation and migrate pages**

Add `expiry_unconfigured` text and `observationForNode`. `ScreenView` uses `useObservations` and the helper for tile quality. `App` uses it for `stalePoints` (only actual stale, not unknown) and passes observation state to `EnergyFlow` and `AssistantPanel`. `EnergyFlow` distinguishes bound/stale/unknown/unbound; the assistant distinguishes stale from unknown and removes 15-second copy. Remove all four local 15-second freshness judgments; transport timeout values are unchanged.

- [ ] **Step 4: Run frontend checks**

Run: `npm --prefix frontend run check:observations && npm --prefix frontend run check:capabilities && npm --prefix frontend run build`
Expected: all pass.

- [ ] **Step 5: Commit the frontend slice**

```bash
git add frontend/src/observations.ts frontend/src/ScreenView.tsx frontend/src/App.tsx frontend/src/EnergyFlow.tsx frontend/src/AssistantPanel.tsx frontend/observations-check.mjs
git commit -m "feat: show observation freshness from server verdicts"
```

### Task 3: Real-page acceptance, review and evidence

**Files:**
- Create: `evidence/stage-b-observation-verdict.json`

**Interfaces:**
- Uses the existing debug backend and `fastiot-ui-dev` preview; no production device or write.

- [ ] **Step 1: Verify real HTTP and browser behavior**

Start/reuse the debug backend and UI. Use a configured Modbus point with declared expiry and a non-EMS variable without `expires_at`. Verify state quality/reason/expiry, then compare overview and screen: stale is stale with the correct reason; no-expiry is unknown, not normal/stale. Check `EnergyFlow` and assistant unknown wording. Restore all fixture state and stop the UI server started for this task.

- [ ] **Step 2: Run full regression**

Run: `go test -p 1 -count=1 ./...`
Run: frontend build plus navigation, consistency, observations, acceptance and capabilities checks.
Expected: 34 Go packages and all frontend checks pass.

- [ ] **Step 3: Perform Standards/Spec review and fix Important findings**

Review the complete slice against the approved design and issue #2 Stage B. Each behavior fix gets a failing test first, then the full suite. Do not expand into link health, calculation expiry or virtual-input time semantics.

- [ ] **Step 4: Record evidence and commit**

Write `evidence/stage-b-observation-verdict.json` with exact tests, browser states and remaining Stage B work. Commit the evidence and any review fixes with the required attribution trailer.
