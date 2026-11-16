# Ticket03 virtual inputs implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans. User approved inline execution; no repeated approval needed.

**Goal:** Typed, authorized nonphysical inputs with explicit activation and validity.
**Architecture:** A small pure virtualinput domain plus an existing-SIOT HTTP adapter. One serialized document per input; no generic value/valueSet publication or device dispatch. One UI component serves engineering and runtime roles.
**Tech Stack:** Existing Go/SIOT/NATS, React/Ionic/TypeScript. No new dependency.
**Spec:** docs/superpowers/specs/2026-11-16-ticket03-virtual-inputs-design.md; local ticket03.

## Global Constraints

- Base3091296; original branch feat/ticket03-virtual-inputs, then user explicitly authorized migration into isolated claude/happy-hodgkin-6a6407. Local commits only; parent files untouched. No push, GitHub issue or physical device.
- Preserve existing variable behavior; no migration or task04 computation/control integration.
- Configuration/manual/simulation provenance is immutable. Never fabricate a physical observation or credentials.
- Applied configuration survives restart; timed input does not automatically become valid after restart without continuity proof.
- Real HTTP/storage/UI evidence, not a fake UI. Retain unrelated assets.

## Review Focus

- Zero/false versus no value, and metadata changes that would reinterpret retained values: Task1.
- Expiry resurrection by reads, retries, wall-clock rollback or restart: Task2.
- Generic routes, copy/version rollback and raw snapshot/WS bypasses: Task2.
- Stale impact/revision confirmation and restricted-reference disclosure: Task2.
- Operator access without configure permission, including action whitelist overrides: Tasks2/3.

## Task 1: Typed draft/application lifecycle

**Files:** internal/virtualinput/virtualinput.go and virtualinput_test.go; internal/appapi/virtual_inputs.go and virtual_inputs_integration_test.go; handler.go.
**Interfaces:** Domain Definition, Document and Value use number/bool/enum and config/manual/simulation. Public New, SaveDraft, Apply, Evaluate transitions receive explicit clock/operation context. HTTP base /virtual-inputs; scoped list/detail, create, draft and apply. Persist a schema-versioned JSON document in a dedicated node point. No deviceID/source_time masquerade.

- [x] Add a public-domain test: create configuration100kWh (range0..200); current absent before application, save120 remains inactive, apply120 becomes current; next draft150 must leave120 active. Run focused test RED.
- [x] Implement only these transitions and prove GREEN.
- [x] Incrementally test missing versus0/false, enum membership, invalid range/unit/purpose, immutable source, stale revision and unchanged apply. Each RED then GREEN.
- [x] Add real authenticated HTTP/SIOT test for create/draft/apply, and rejected legacy value/manual paths; watch404/missing behavior RED before wiring routes.
- [x] Add separate application action using existing canAction/node scope; persist state plus necessary actor/time/before-after evidence in one acknowledged document. Serialize CAS and verify readback revision; an operation ID reconciles ambiguous retry without reapplying.
- [x] Verify HTTP state and absence of value/valueSet/lastCommand; run domain and appapi packages then full suite. Commit slice.

## Task 2: Timed inputs, authorization and lifecycle boundaries

**Files:** domain/adapter/tests above; handler.go, resources.go, reuse.go, project_version.go; existing WS projection and policy action registry/UI where necessary.
**Interfaces:** POST /virtual-inputs/{id}/value, impact preview and specialized read DTO; explicit permissions, definition version, entered_at/expires_at, quality/reason and retained value. Clock evidence is scoped to this domain, not a platform timer rewrite.

- [x] Test manual/simulation assignment only after application and engineering opening. Accepted5kW in range-10..10; reject11, wrong type and config runtime write without changing state. RED→GREEN.
- [x] Test original expiry, same operation retry, read-only evaluation, backward wall clock, monotonic expiry, no resurrection and definition invalidation. RED→GREEN, deterministic clock values.
- [x] Stop/rebuild actual isolated persistence server on the same directory: configuration120 survives; timed input is expired/pending verification with original timestamps, never a renewed lease. RED→GREEN.
- [x] Test explicit action whitelists, configure-only inability to apply, operator scope, viewer/cross-root rejection and closed input. Add virtual_input_apply/write to existing role defaults and policy validation; HTTP checks remain authoritative.
- [x] Add impact preview using known rule and screen references, permission-safe projection, proposed change/revision/dependency fingerprint. Test changing a dependency after preview rejects the old confirmation; keep required serialization narrow.
- [x] Test copy: fresh identity, draft-only, same simulation class, no active runtime value/open grant. Test version snapshot exclusion and unsupported rollback rejection before any writes. Implement hooks in reuse/version paths.
- [x] Test snapshot/WS cannot expose raw protected document/evidence; dedicated API exposes only scoped data. Keep existing target-type rejection, no control integration.
- [x] Run affected and full Go suites, preserve ticket01/02 regressions; commit slice.

## Task 3: One component, engineering/runtime entry points

**Files:** frontend/src/VirtualInputs.tsx, api.ts, PointsView.tsx, App.tsx; existing permission policy action list; evidence/ticket03-virtual-inputs.json.
**Interfaces:** /virtual-inputs APIs from Tasks1/2. Engineering mode offers create/draft/apply/open settings; customer mode offers scoped reading and permitted runtime changes only. Server permissions/quality are authoritative.

- [x] Reproduce absent task flow in actual isolated UI, then wire the minimal shared component using current EMS controls. No navigation redesign.
- [x] Create configuration capacity and manual input through forms; verify draft isolation/apply, permitted updates and rejected direct HTTP requests from local operator/viewer accounts.
- [x] Show explicit provenance, applied/draft revision, expiry/pending verification, last-change evidence, no-value dash and separately labelled retained value. Confirm impact and validity before runtime mutation, not device execution.
- [x] Verify restart via API and UI; verify copied simulation identity/provenance through real storage/API integration tests and simulation labelling in the UI. No browser project-copy workflow was claimed. No device writes or fabricated observation. Test failed queries and changing scopes without stale successful displays.
- [x] Run TypeScript/Vite, existing navigation/consistency checks, full go test -count=1 ./...; use preview tools for interaction/console/network and desktop/mobile proof.
- [x] Commit, run one final Standards/Spec review across the full branch; fix Important/Critical in one RED→GREEN pass, ledger minors, rerun suites. Stop fixtures, restore launch config, update local ticket/evidence. Do not push or start04.

## Execution notes

Completed locally on 2026-11-16; authoritative verification and limits: `evidence/ticket03-virtual-inputs.json`. Shared files across the slices were consolidated into one final local implementation commit after review fixes rather than separate unfinished API commits. Parent ticket/progress files remain untouched; this worktree's evidence and handover carry the completion state. Four independent review findings were reproduced and fixed; final full Go and frontend checks passed. Customer overview uses a visible drawer because its fixed layout collapsed an inserted card. Screenshot file copies were permission-denied; screenshots are visible in the session transcript, with credential-free results recorded in the evidence file.

Use cached golang:1.27.3 Docker and existing volumes when host Go is unavailable; MSYS_NO_PATHCONV=1. Validation containers use repository read-only and --network none except explicit isolated server fixtures. Backend/UI slices share lifecycle and API files and are dependent, so they stay sequential in this session. Domain tests use public transitions; HTTP tests reuse real SIOT persistence/account helpers. Never test private storage maps in place of public behavior.
