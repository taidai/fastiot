# Task04 calculation chain implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:executing-plans. Approved inline execution; do not repeat design approval.

**Goal:** Independently running, coherent and traceable two-level read-only calculations.
**Architecture:** Pure calculation graph + existing-app persistence/source adapter and lifecycle loop; atomic memory batches, persisted definitions only.
**Tech Stack:** Existing Go/SIOT/NATS and React/Ionic/TypeScript; no new dependency.
**Spec:** docs/superpowers/specs/2026-11-16-ticket04-calculation-chain-design.md and local task04.

## Global Constraints

Base8dd12f5, local feat/ticket04-calculation-chain. Preserve task03-premerge-r26evcnm and stash1816cdb. No implicit worktree/new session/push/real devices. No free scripts, real-control binding, historical backfill or broader math language. Existing variable/aggregate semantics are not migrated.

## Review Focus

- Concurrent capture must not label fresh samples future; units/config identities must belong to the captured reads: Task2.
- Same-valued new observations propagate, draft-only revisions do not; expiry is never renewed by calculation: Task2.
- Downstream never sees mixed intermediate generations, including failed dependencies and changed definitions: Tasks1/2/3.
- Fixed trials never consult live fallback or mutate runtime; transitive simulation/time provenance cannot be lost: Tasks2/3.
- Restricted transitive/historical evidence, generic writes, copy/remap and rollback cannot bypass the dedicated contract: Tasks1/3.

## Task 1: Typed definitions and application boundary

**Files:** new internal/calculation/{calculation.go,calculation_test.go}; new internal/appapi/{calculations.go,calculations_integration_test.go}; handler.go.
**Interfaces:** Definition (operator add/divide, exactly two stable input IDs, numeric output/unit, optional skew); Metadata describes source kind/unit. ValidateGraph(definitions, sources) yields deterministic dependency order or localized validation error. Document separates revision/draft/applied/formula version. Dedicated /calculations routes for create/draft/apply/read, no generic result value points.

- [x] Write public graph validation test: A/B kW, capacity kW, total add kW, ratio divide1; order total before ratio. Run RED before implementing.
- [x] Add one case at a time: invalid arity/operator/type/unit/ref, self/indirect cycle and downstream incompatibility after upstream change. Each RED→GREEN; no arbitrary parser.
- [x] Add real HTTP create/draft/apply/read test RED, then route/persist the node. Creating/saving draft produces no result, application validates whole candidate graph, failed apply preserves old state, revision/op IDs prevent stale/repeated application.
- [x] Test configure-only versus calculation_apply and read-only generic write/manual rejection. Add action to existing registry/defaults and type-specific response projection.
- [x] Run domain/appapi and full Go suite, commit verified slice.

## Task 2: Acquisition to autonomous atomic batches

**Files:** channel.go, bridge/nodesource.go and construction/hash paths, state.go; new appapi/calculation_runtime.go; app/app.go; public domain/state/app lifecycle tests.
**Interfaces:** per-read unit/config stamp through Result/Value. State Capture(clock,maxAge) returns immutable values and capture time (clock inside lock); coalesced change wake includes invalid attempts. EvaluateBatch receives one immutable source snapshot and definitions and returns coherent results with direct/leaf evidence. Handler RunCalculations(ctx) belongs to app lifecycle and keeps private runtime cache.

- [x] RED→GREEN: late serial blocks carry actual capture unit/config stamp; config changes and edit/revert require matching new observation; preserve ticket02 ordering.
- [x] RED→GREEN: consistent capture time and both good/invalid wake notifications; no overwriting Bridge.OnResults.
- [x] RED→GREEN: A20/B30/capacity100 gives50/.5; A40 gives70/.7 in one batch, then failures/div0/overflow clear current value and retain labelled previous evidence.
- [x] RED→GREEN: transitive original leaf times/provenance, declared skew, constants excluded, exact expiry; same value/new timestamp changes result evidence without renewing leaf freshness; unaffected inputs and draft-only changes do not recompute.
- [x] Wire same-process loop after bootstrap to cancellation/run group; snapshot virtual values under existing mutex with the same inputClock context. Coalesce state/config/input wakes, schedule expiry and bounded fallback. GET never runs evaluation.
- [x] Prove via live application/public result API that background changes occur without page/GET; atomically publish completed batches and reject stale configuration generations.
- [x] Real persistence restart: only definitions survive; sensor-dependent results await new input, pure config can recompute, timed virtual values remain pending verification. Full suite and commit.

## Task 3: Isolated trials and boundary closure

**Files:** calculations.go, calculation_runtime.go, virtual_inputs.go, resources.go, reuse.go, project_version.go, snapshot/WS projection; integration tests.
**Interfaces:** trial takes a complete frozen leaf input set and isolated candidate graph; result tagged simulation with intermediates/errors. Known dependency and permission checks include calculations, without claiming a complete future graph transaction.

- [x] RED→GREEN: trial50/.5 and localized zero/missing-input errors; no live fallback, runtime cache/node/command effects or unlabelled trial provenance.
- [x] RED→GREEN: applying a semantic change invalidates/recalculates affected descendants; rejected application retains previous formula/results.
- [x] RED→GREEN: invisible immediate/transitive or historical dependencies expose only restricted status, including snapshot/WS; no hidden names/values/IDs in errors.
- [x] RED→GREEN: dependency-aware move/delete and impact changes; permission-safe known references.
- [x] RED→GREEN: copy creates draft-only identities and remaps included references; unresolved external inputs cannot activate. Snapshot excludes runtime state; unsupported rollback rejects before writes. No widening of device/control target types.
- [x] Full domain/appapi/Go suite and local commit.

## Task 4: Actual two-level UI workflow

**Files:** new frontend/src/CalculationPoints.tsx; PointsView.tsx, api.ts, permission action labels; evidence/ticket04-calculation-chain.json.
**Interfaces:** dedicated APIs, server permissions, immutable batch/formula/input evidence. Existing EMS controls and stable callbacks, no new navigation.

- [x] Reproduce missing workflow in isolated UI; implement minimal create/edit/trial/apply/result/dependency section.
- [x] Use real forms to choose A/B/capacity, apply total/ratio, verify50/.5 and70/.7. Percent switch displays50% but API and downstream keep.5.
- [x] Close/leave page, update isolated inputs, observe background API result before reopening. Check bad/expired/recovered input, draft isolation, trial, keyboard focus, failed queries and scoped read permissions.
- [x] Cross-check persisted definitions, API batch and actual page; no protocol writes. Verify desktop/mobile and console/network with preview tools.
- [x] TypeScript/Vite, observation/navigation/consistency/acceptance checks and full go test -count=1 ./...; one final Standards/Spec review, one important-fix RED→GREEN pass. Stop fixtures/restore launch config, record limits, update local task; local commits only.

## Execution

Each slice consumes preceding interfaces and touches shared files, so execute sequentially here. Use cached golang:1.27.3 with existing module/build volumes; MSYS_NO_PATHCONV=1, repository read-only and --network none for verification. Only isolated explicitly labelled fake protocol data; no simulator evidence becomes hardware sign-off.
