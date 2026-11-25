# Batch access and isolated preview integration — 2026-11-25

## Integrated scope

Starting HEAD: `a392817a5c640cc13b2b331908eb5098709329df`.

| Delivery | Incoming final SHA | Normal merge commit |
| --- | --- | --- |
| 06 reviewed batch access | `cf2aa59c58cd4200651bed571af63433a8e95699` | `e068b257aebb825e6390d21c8515dd9beca751cf` |
| 17 isolated-preview repair | `30ed866cce5a65afc19e2334e5b8ce6f23086b9f` | `02220a1615f591c960c35a9f2b368fa75acb2372` |

Both merges completed without conflicts or integration code adaptations. Ticket 17 was merged only after the coordinator supplied its explicit final SHA. Ticket 07 and other features were not merged. The auth client/project publication ordering, Handover fences, industry gate, existing device verification, aggregate/statistics implementations and execution README were not edited. Local commits carry no AI attribution trailer.

## Contract checks

- The new `/access/batch/reviewed` Configure route requires a matching preview version before any apply; legacy `/access/batch` intentionally retains its compatibility contract. Whole-batch preflight, hidden-binding protection, duplicate-station blocking, shared Profile planning and actual per-device verification remain distinct from mapping application.
- Server bounds are 2 MiB JSON, 256 KiB CSV, 256 records, four columns and 256 bytes per field. The UI preserves the complete reviewed draft and reports saved/blocked/failed results, not an invented all-success state. Saved mapping is not L1/L2 evidence.
- The 17 repair restores shared prototype execution rows, twelve exact illustrative bars and history interaction only behind `PREVIEW_MODE`. Real mode keeps actual history and honest unavailable execution content. Original design assertions remain; added assertions check precise bar values, thirty history rows and absence of demo content in live mode.
- The original auth browser remains byte-for-byte unchanged from the starting HEAD, including all four `AUTH_SCENARIO` values, 8000 ms expiry and 2300 ms stopped-polling bounds. No auth, selector or timeout workaround was added.

## Fresh verification

Final combined source under verification: `02220a1615f591c960c35a9f2b368fa75acb2372`.

- Frontend: `node --test 'frontend-uniapp/src/**/*.test.mjs'` — **133 passed, zero failed/skipped**; type-check and H5 build passed. The preliminary 06-only frontend test/type/build also passed; both sets of logs are retained.
- Auth: the first run of each original `AUTH_SCENARIO` (`baseline`, `late-initial-runtime`, `state-first`, `alarms-first`) passed, each including all three overview/design/delivery preview children. Request counts were 41/40/41/41 respectively; expiry, stopped polling, token isolation and zero business writes passed without altered bounds.
- Project delayed-response/dirty-discard races passed. The repaired workspace harness and an additional isolated delivery run passed; their shared static fixture observed zero API requests.
- Final complete Go run: **PASS, exit 0, all 37 tested packages** with Go `1.27.3 linux/amd64`, `TMPDIR=/store go test -p 1 ./... -count=1`. App 24.475s, appapi 126.324s, decision 18.658s. No package exclusions or repeat-until-green run. The separate earlier failure below remains unresolved.

Each real-Go/browser pair below ran once against the final combined archive/assets. Both processes exited 0 in every pair; no failed browser was retried.

| Check | Loopback host → container port | Result |
| --- | --- | --- |
| Batch access | 18106 → 18106 | Go 5.042s; complete CSV, byte/row limits, conflict blocking, Profile diff, 2 saved + 1 blocked, real per-device L1/L2 success/failure, reload, idempotent retry, missing mapping and offline failure, four viewports passed; no physical-write endpoint calls. |
| Single device | 18129 → 18085 | Go 6.641s; preview/apply, Profile fields, L1/L2, persistence, required-point failure, evidence invalidation and offline checks passed. |
| Shared screen | 18107 → 18107 | Go 36.029s; save/refetch, slots/spans, dirty isolation, inheritance/unbound repair, conflicts/same-kind versions, partial statistics, tabs and four viewports passed; live mode has no illustrative chart or prototype execution content. |
| Alarm/handover | 18130 → 18101 | Go 11.426s; acknowledged-active semantics, global/layer scope, observation/recovery/retrigger/history, restart, locate/history, persisted handover and delayed-response fences passed. |
| Daily statistics | 18128 → 18102 | Go 4.522s; recorded energy/runtime, partial coverage/retention, communication/observation freshness, refresh, null/unavailable states, project gate and four viewports passed; no business writes. |

Focused real-Go regressions passed **22 tests in 12.762s**: five reviewed-batch tests, eight single-device tests, point-configuration consistency/no-write, Configure/direct-scope routes, accounts/tree/project/industry and alarm/handover scope. The opt-in single-device browser test was skipped in this unflagged focused run and separately enabled/passed in the table above.

The batch fixture intentionally lacks industry-package service and saved screens, so its log retains `packages_unavailable` (503) and `screen_not_found` (404); these are not claimed successful requests. The oversize-row 400 is an asserted rejection. Final combined checks pass, but the preliminary bootstrap failure below remains an unresolved stability finding, not an unconditional clean-sweep claim.

All raw logs/runners are local under `.scratch/batch-preview/`. Source is a full `git archive` plus actual newly built `internal/web/dist` assets, copied into named Docker volumes without a Windows bind mount. The final source volume is `fastiot-batch-preview-final-source-20261125`, read-only during tests; final temporary store is `fastiot-batch-preview-final-store-20261125`, with `TMPDIR=/store`. Existing Go module/build caches are reused. Browser fixtures use separate private store volumes and loopback-only published ports.

An earlier 06-only full Go run on `e068b25` started before the final 17 SHA arrived. It **failed, exit 1**, in `TestSingleDeviceProfileDefaultsReadOnlyAndValidatesEnums`: `point_config_integration_test.go:267: verify initialized administrator` (appapi 145.089s). The error originates in `Bootstrap`'s administrator readback, before this test's Profile assertions; immediately beforehand, the store logs that the bootstrap password point was ignored due to timestamps, then administrator sign-in fails. The underlying timestamp-ordering cause is not established and no fix/retry was attempted. This first failure remains in `go-full-first.log`; the already-planned final combination run uses a distinct fresh source/store and cannot erase it.

## Limits

Only disposable software fixtures and private TCP simulators are used: no physical devices, physical writes, commissioning or field sign-off. The saved final mobile screen and desktop alarm screenshots were inspected; this is not exhaustive visual certification. Batch apply is not a cross-device transaction; no new disk-full or mid-write NATS fault injection. Preview is not live execution evidence; ticket 16 remains pending. Existing history/observation retention and aggregate adapter limits are unchanged. Historical auth timeout and allocator/recovery flakes are not retroactively explained or declared fixed by a passing combination run. No reset/stash, original-checkout modification, launch/credential/permission change, push, public issue/PR or next-ticket work.
