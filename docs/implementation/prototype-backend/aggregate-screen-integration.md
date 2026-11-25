# Aggregate and shared-screen integration — 2026-11-25

## Integrated graph and boundaries

Starting integration HEAD: `ff31c345fdc33ef6ef8751f279997d4eff575de0`.

| Order | Incoming tip | Normal merge commit |
| --- | --- | --- |
| 15 | `1cf9f73e0f35435c429c84d38920622e94946076` | `8d927abe4aaaec838cafa5f2ff63d37fbe69dc31` |
| 17 | `95d73974656ca0c6cd676de9723ca88660a8c3dc` | `1868cc3cba797d3bcbf961d59d9c4414d6ab10ee` |

Both merges were non-fast-forward merges with no conflicts. No production-code integration adaptation was necessary. All checks below use the actual combined source at `1868cc3`, not results inherited from the ticket branches. The evidence commit changes only this document.

The starting integration ancestry, including 05/11/12/04/19 and the Simple IoT store ACK fixes, remains present. The shared API client, project/session stores, `HandoverPanel.vue`, Simple IoT store and execution README have no diff from the starting HEAD. No auth-fix, 06 or 07 branch was merged; no ticket 18 derived protection or ticket 16 execution implementation was added. No reset, stash, original-checkout work, launch/credential/permission changes, push, issue or PR. Local merge/evidence commits have no AI attribution trailer, per user preference.

## Focused contract review

- **Runtime lifecycle and alarms:** `App.Start` starts the aggregate sampler on the existing readiness/cancellation boundary. `SetEMS` still restores durable alarm occurrences and registers lifecycle/checkpoint hooks before readiness. Sampling is independent of GET/PUT; cancellation stops its ticker/subscription. Existing alarm observation checkpoints and handling semantics are retained.
- **Aggregate evidence:** authoritative current value, quality, membership and observation time come from `easyemsAggregateResult`. The numeric cache's LWW storage clock is not source freshness. `visibleNodes` reconstructs a response-local projection from structured evidence, or leaves the result pending without a usable scalar.
- **Authorization and asset reuse:** snapshot and WebSocket readers share `visibleNodes`; current and historical aggregate evidence check current member visibility. History/display aggregation/CSV use the shared history query and retain member/window evidence. Runtime result/value points are excluded from copy/export/version assets while the definition is retained. The combined full suite exercises no-reader production, real storage restart, shutdown/subscription cleanup, source-time/quality changes, history/CSV, current-scope rejection, pagination and copy/export behavior. WebSocket use of the shared projection was source-reviewed; no new aggregate-specific WebSocket browser fixture was introduced.
- **Shared screen:** editor preview and customer overview use `ScreenRenderer`. The overview alarm slot keeps local active alarms (including acknowledged active alarms), while the bell counts globally authorized unacknowledged occurrences. Handover remains outside the removable block catalog.
- **Daily figures and bindings:** the renderer loads one `DailyStats | null` result per refresh for both automatic KPIs and summary, retaining partial/null/unknown states even when summary is removed. Generic `screenReferenceAllowed` remains usable by daily engineering declarations; `screenBindingReferenceAllowed` applies the extra structured-screen business-binding restriction. Version/preflight checks remain intact. `DevicePanel.allowedTabs` remains available for ticket 07.

## Fresh combined verification

### Frontend

- `node --test 'frontend-uniapp/src/**/*.test.mjs'`: **130 passed, 0 failed, 0 skipped**.
- `npm --prefix frontend-uniapp run type-check`: passed.
- `npm --prefix frontend-uniapp run build:h5`: passed. These actual generated assets were used by the browser checks and copied into the Go source volume.

### Full Go repository

```sh
go version  # go1.27.3 linux/amd64
TMPDIR=/store go test -p 1 ./... -count=1
```

**First actual complete test run passed, exit 0: 37 tested packages.** App 20.431s, appapi 107.792s, decision 18.363s. No package/test exclusions or green-seeking rerun. The opt-in browser fixtures were separately enabled after this run.

- Full `git archive` of merged HEAD, including tracked fixture assets, plus the freshly built `internal/web/dist` assets.
- Own named source volume `fastiot-aggregate-screen-source-20261125`, mounted read-only during tests; private temporary-store volume `fastiot-aggregate-screen-store-20261125` and `TMPDIR=/store`.
- Existing module/build caches `fastiot-merge05-mod-20261125` / `fastiot-merge05-build-20261125` reused. No Windows bind mount.
- The first **setup** attempt stopped before Go tests: `git archive` does not contain generated `internal/web/dist`, so asset extraction lacked its target directory. Created that directory in the private source volume and extracted the real assets. `setup-error.log` preserves this setup failure; it was not a failed or repeated Go test run.
- Previously recorded allocator, pump-house and recovery flakes did not occur in this run. This does not establish those intermittent issues are fixed.

### Browser and scope checks

The serial runner started after the full Go run, using the same combined source read-only, fresh private per-fixture store volumes and loopback-only published ports. Each fixture/browser below ran once and both processes exited 0:

| Check | Loopback host → container port | Observed result |
| --- | --- | --- |
| `TestScreenBrowserBackend` + `screen.real-browser.mjs` | 18107 → 18107 | Go 36.631s; save/refetch, shared slots/spans, dirty isolation, inheritance/unbound repair, concurrent conflict, same-kind versions, disabled tabs and four viewports passed. Same 13 kWh partial result in preview/customer KPI and summary, unconfigured runtime null, and KPI retained when draft summary is removed. |
| `TestSingleDeviceBrowserBackend` + `access.real-browser.mjs` | 18129 → 18085 | Go 4.211s; preview/apply boundary, Profile fields, real L1/L2, reload, mapping invalidation, missing required point and offline failure passed. |
| `TestAlarmHandoverBrowserBackend` + `alarm-handover.browser.mjs` | 18130 → 18101 | Go 14.137s; acknowledged alarm remains active, global/local counts, observation counts, recovery/retrigger/history, restart, exact locate/history point, persisted handover and delayed GET/PUT/cross-layer fences passed. |
| `TestDailyStatsBrowserBackend` + `daily-stats.browser.mjs` | 18128 → 18102 | Go 7.154s; 13 kWh / 0.33 h partial history, retention, 1/2 communication, observation freshness, refresh, missing binding/null, project gate and four viewports passed without business writes. |

`projects.browser.mjs` passed the two-context dirty-discard/delayed-response fixture. Separate real-Go scope smoke passed **5/5** in **2.344s**: accounts management, alarm/handover scope, industry dependency gate, tree isolation and foreign-project rejection. The real browser scripts' viewport overflow checks passed. The saved mobile screen and desktop alarm screenshots were inspected; this is not exhaustive visual certification.

### Retained acceptance failures and unverified portions

**Do not label this combined acceptance unconditionally green.** The browser runner exited **1**, and an independent remaining-preview runner also exited **1**.

1. **Original auth test stages passed this time; the complete auth/preview command failed later.** The unchanged `login.browser.mjs` completed routing/login, 403/500/offline behavior, 401 expiry, stopped polling, preview token isolation and zero-write assertions (41 requests). Its original 8000ms expiry and 2300ms stopped-polling bounds were not changed. The overview-preview child also passed. This one pass does not erase the earlier auth timeout or merge the separately confirmed synchronous-reentry fix.
2. **Design-preview contract failed.** The subsequent unchanged `design-preview.browser.mjs` child timed out after 30000ms at line 30 waiting for `.prototype-chart i`; its expected 12-bar chart was absent. Source tracing shows ticket 17 replaced `UiStep`'s prototype-specific chart/today-card branches with `ScreenRenderer`, while this browser still requires that old chart and three preview execution cards. This is a retained preview-contract gap, not an auth-expiry failure. No production patch, selector weakening or repeat-until-green run was attempted; later design-preview assertions were not reached.
3. **Delivery preview failed when run independently once.** Because the failed design child prevented the parent from reaching delivery, `delivery-preview.browser.mjs` was run once on a separate loopback static fixture. At line 74, immediately after selecting accounts, it expected three `.account-table tbody tr` rows but observed zero. Earlier publish/import/rebind/run and deployment assertions were reached; later permission/audit assertions were not. The cause of this separate failure was not established. Delivery code was not changed or retried.
4. **Workspace preview was blocked before execution.** `workspace.browser.mjs` imports the absent `frontend/node_modules/playwright-core/index.mjs`. The module-resolution failure happened before browser launch. No dependency installation or harness-path repair was made; this is not a passing workspace check. The independent static fixture logged zero API requests.

Detailed results: `browser-results.json`, the `screen`, `access`, `alarms` and `daily` pairs of `-browser.log` / `-go.log`, `auth-preview-browser.log`, `project-races-browser.log`, `scope-smoke.log`, `remaining-preview.log`, and `preview-source-diff.txt` under `.scratch/aggregate-screen/`. All first failures are retained.

## Evidence and limitations

Local untracked evidence and runners are under `.scratch/aggregate-screen/`; prior authentication evidence remains under `.scratch/device-alarm-stats/` and `.scratch/auth-diagnosis/`. These local logs are not committed.

- The earlier unchanged auth browser's first 8000ms timeout remains a historical failure; a later passing diagnostic never established its full cause. A separate synchronous-auth-reentry defect has been confirmed and its fix is being handled outside this merge. No authentication or Handover synchronization fix is claimed here.
- Disposable loopback software/simulator checks only: no physical devices/writes, commissioning, field sign-off, exhaustive pixel-perfect acceptance or power-loss durability claim.
- Aggregate late/missed windows retain gaps; no interpolation or zero fill. OPC-UA/MQTT are not newly adapted as trusted aggregate observations. Daily observation/communication retention remains process-local, with restart gaps exposed. Screen same-kind writes are preflighted/serialized, not crash-atomic multi-node transactions. Today execution remains an honest unavailable region pending ticket 16.
