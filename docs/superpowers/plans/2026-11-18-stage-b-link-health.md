# Stage B Link-Health Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give channels, delivery, allocation and alarm monitors one healthy/degraded/offline/unknown verdict with age and reason.

**Architecture:** A small health module owns threshold and state decisions. Channel and legacy adapters delegate to it instead of maintaining separate poll/timeout/MaxAge calculations.

**Tech Stack:** Go 1.27.3, existing channel/alarmwatch/decision tests, Browser preview for one real channel.

**Spec:** `docs/superpowers/specs/2026-11-18-stage-b-link-health-design.md`

## Global Constraints

- No Stage F deletion, authorization/formula changes, real device connections or physical writes.
- Unknown is neither healthy nor offline; after a configured grace period, no successful poll is `offline/no_success`.
- Allocation accepts healthy/degraded and rejects offline/unknown, preserving existing member behavior.
- Keep public state names healthy/degraded/offline/unknown and existing API shapes.

## Review Focus

- No observation during the creation grace period is unknown, not healthy or offline.
- Configured channels with no success after grace become offline, not permanently unknown.
- A recent success followed by failures is degraded, while expiry after success is offline.
- Future timestamps return `offline/invalid_time` instead of healthy.
- Allocation rejects offline/unknown and accepts degraded without duplicating health rules.

---

### Task 1: Health module and channel adapter

**Files:**
- Create: `internal/ems/health/health.go`, `internal/ems/health/health_test.go`
- Modify: `internal/ems/channel/channel.go`, `internal/ems/channel/channel_test.go`

**Interfaces:**
- Produces: `health.Input{Requests, ConsecutiveFailures, OfflineAfter int, LastSuccess time.Time, PollPeriod, Timeout, MaxAge, CreatedAt time.Time}` and `health.Verdict{State, Reason string, Age time.Duration}` with `health.Evaluate(input, now)`.

- [ ] **Step 1: Write failing verdict table tests**

Cover creation grace unknown, no observation unknown, no success after grace offline, healthy, degraded after failures, expiry offline, and future timestamp offline. Assert state, reason and age; explicit MaxAge overrides the default `max(5s, 5×poll, 3×timeout)`.

- [ ] **Step 2: Run tests and watch them fail**

Run: `go test ./internal/ems/health -count=1`
Expected: package does not exist.

- [ ] **Step 3: Implement and delegate `Channel.Health`**

Default OfflineAfter to 3. `channel.Health` maps existing Stats and config into the module without changing its public signature. Update only tests that intentionally relied on the old no-success degraded behavior.

- [ ] **Step 4: Run channel/health tests and commit**

Run: `go test -p 1 -count=1 ./internal/ems/health ./internal/ems/channel`
Expected: PASS.

```bash
git add docs/superpowers/specs/2026-11-18-stage-b-link-health-design.md docs/superpowers/plans/2026-11-18-stage-b-link-health.md internal/ems/health internal/ems/channel
git commit -m "feat: resolve channel health in one module"
```

### Task 2: Alarm and allocation adapters

**Files:**
- Modify: `internal/ems/alarmwatch/alarmwatch.go`, `internal/ems/alarmwatch/alarmwatch_test.go`, `internal/alerts/monitor.go`, `internal/decision/allocator.go`, `internal/decision/distribute_test.go`

**Interfaces:**
- Alarm adapters use Requests=1 for configured polling channels and preserve creation grace.
- `decision.busHealthy` returns true only for healthy/degraded and never derives its own threshold.

- [ ] **Step 1: Add failing adapter tests**

Alarmwatch: no success within grace has no finding; after grace it emits bus-offline/no_success. Allocation: a degraded member is eligible, unknown/offline is excluded. Ensure existing alarm/allocator tests still describe the intended behavior.

- [ ] **Step 2: Run tests and watch them fail**

Run: `go test -p 1 -count=1 ./internal/ems/alarmwatch ./internal/decision ./internal/alerts`
Expected: health package dependency missing and/or grace behavior is wrong.

- [ ] **Step 3: Implement adapters**

Alarmwatch and old alerts.Monitor call `health.Evaluate` and raise only offline findings. `decision.busHealthy` maps lastGoodPoll/poll/timeout/created into the same verdict and accepts only healthy/degraded. Do not change alarm or allocation interfaces.

- [ ] **Step 4: Run adapter tests and commit**

Run: `go test -p 1 -count=1 ./internal/ems/alarmwatch ./internal/decision ./internal/alerts`
Expected: PASS.

```bash
git add internal/ems/alarmwatch internal/alerts internal/decision
git commit -m "feat: share channel health with alarms and allocation"
```

### Task 3: Real-channel acceptance, review and evidence

**Files:**
- Create: `evidence/stage-b-link-health.json`

- [ ] **Step 1: Verify real channel behavior**

Use a local simulator: fresh polling is healthy, a failed poll is degraded, stopping success past expiry is offline, and a newly configured channel without observation is unknown during grace. Verify `/ems/channels`, preflight and alarm output agree. Restore fixture data and stop temporary servers.

- [ ] **Step 2: Run full regression**

Run: `go test -p 1 -count=1 ./...`
Run: frontend build plus navigation, consistency, observations, acceptance and capabilities checks.
Expected: 36 Go packages and all frontend checks pass.

- [ ] **Step 3: Perform independent Standards/Spec review**

Review against the approved design and issue #2 Stage B. Fix Critical/Important findings once with RED→GREEN tests; do not add Stage F deletion work.

- [ ] **Step 4: Record evidence and commit**

Write `evidence/stage-b-link-health.json` with verdict cases, adapter tests, real-channel evidence and cleanup. Commit evidence and review fixes with the required attribution trailer.
