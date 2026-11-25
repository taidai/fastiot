# Device, alarm and daily-statistics integration — 2026-11-25

## Integrated graph and scope

Starting integration HEAD: `7f247f9060de337ed82eb1c3e55487b308fac41a`.

| Order | Incoming tip | Integration merge |
| --- | --- | --- |
| 05 staging (includes ticket05 `4542c6d`) | `01717ad05da5b396325c07d09a487b25af76c972` | `b633be0f9ef2127602369070a8977f7bfb5e4b1d` |
| 11 | `942acda3544551d39a512cac45a1ff0866c7c109` | `aac3628c1e295fdfc903bf3a4878475dcb1942a8` |
| 12 | `4886413e94aa885ab4f99f36cb35f1e66db8729b` | `c273cbc27d622c6199493192f320a146449d4ca1` |

All three were normal non-fast-forward merges. No conflict resolution or production-code adaptation was needed. Git automatically combined ticket11 and ticket12 edits to `LiveOverview.vue` and `handler.go`; the resulting source was tested together, not inferred from separate branch results. No reset, stash, checkout switching, README/status edits, ticket17/15 merge, or additional ticket work.

Focused review confirmed preservation of:

- 05's Profile/content and mapping-version invalidation, read-only L1/L2, required-point/quality evidence, and single-device preview/apply boundary. Legacy bulk apply remains for ticket06. No new L3/command work.
- 11's local active alarms versus globally authorized pending badge, independent observation checkpoints (not lifecycle notifications), bounded redundant-checkpoint compaction, durable occurrence identity and handling, and same-layer late-GET/newer-PUT plus cross-layer handover fences.
- 12's durable `dailyStats` configuration, typed partial/unavailable coverage, shared result/API helper, and counting once at each attempted transport call. The four daily figures coexist with the ticket11 alarm changes.
- Existing authentication/client, project-context and dependency boundaries. The shared client/project stores, dependency lockfiles, parent Simple IoT store ACK fixes and execution README have no diff from the starting integration HEAD. Authorization route changes are limited to ticket12's new scoped statistics routes.

## Fresh combined verification

Tested production source: `c273cbc27d622c6199493192f320a146449d4ca1`. The later evidence commit changes only this document.

### Frontend

- `node --test 'frontend-uniapp/src/**/*.test.mjs'`: **129 passed, 0 failed, 0 skipped**.
- `npm --prefix frontend-uniapp run type-check`: passed.
- `npm --prefix frontend-uniapp run build:h5`: passed; generated H5 assets were used by the browser checks and copied into the Go test source before its run.

### Complete Go repository

```sh
go version  # go1.27.3 linux/amd64
TMPDIR=/store go test -p 1 ./... -count=1
```

**First complete run passed, exit 0: 37 packages with tests passed.** Representative package durations: app **13.916s**, appapi **89.356s**, decision **18.150s**. No test/package exclusions and no diagnostic or green-seeking rerun. Default opt-in browser harnesses were separately enabled below.

Isolation and reproducibility:

- A full `git archive` of the combined HEAD copied all tracked source and fixture assets, rather than selectively copying Go directories. The newly built `internal/web/dist` was separately archived and copied before testing, so embedded/app fixture web files were present.
- Source: `fastiot-device-alarm-stats-source-20261125`; test temporary storage: `fastiot-device-alarm-stats-store-20261125`, with `TMPDIR=/store`.
- Reused available `fastiot-merge05-mod-20261125` and `fastiot-merge05-build-20261125` caches. No Windows source/store bind mount. Browser runs start after the complete Go run, avoiding concurrent browser/build load from this verification.
- The first container-create attempt was refused because Git Bash translated `/src` to a Windows path. Command-local `MSYS_NO_PATHCONV=1` corrected setup before any Go tests ran; no repository or machine settings were changed.
- Earlier ticket12 evidence records intermittent `TestPumpHouseSampleCommandLifecycle` and `TestOfflineRestoreKeepsHistoryAlarmsAndAudit` failures. Neither occurred in this combined first run; this does **not** establish that those pre-existing intermittent issues are fixed.

### Real Go browser and boundary smoke

The serial runner used the same combined source volume read-only, separate private named stores per fixture, and only loopback host ports. All three real-Go browser runs and their Go fixtures exited 0:

| Fixture / browser | Loopback host port | Fresh evidence |
| --- | --- | --- |
| `TestSingleDeviceBrowserBackend` / `access.real-browser.mjs` | 18125 → container 18085 | Go 6.868s; preview leaves storage unchanged, Profile apply/fields, real L1/L2, reload, mapping invalidation, missing required point and offline failure; no page errors or command/value requests |
| `TestAlarmHandoverBrowserBackend` / `alarm-handover.browser.mjs` | 18126 → container 18101 | Go 11.225s; global pending versus layer active counts, ack/count/restart, recovery/retrigger/history, exact locate/history point, persisted handover, late GET/newer PUT and cross-layer delayed save |
| `TestDailyStatsBrowserBackend` / `daily-stats.browser.mjs` | 18102 | Go 3.850s; recorded 13 kWh / 0.33 h with partial coverage, 1/2 = 50% communication, observation freshness, reload, null missing binding, foreign-project rejection and no business writes |

Desktop/mobile viewport overflow assertions passed in all three scripts. Daily desktop/mobile screenshots were also inspected; placeholders remain for absent engineering data, not substituted healthy values.

`projects.browser.mjs` passed its two-context HTTP fixture (dirty-discard and delayed-response isolation). A separate real-Go scope smoke passed **5/5** in **2.765s**, without exclusions inside that explicit smoke selection:

- `TestAccountsManagementScope`
- `TestAlarmHandoverScopedHTTP`
- `TestIndustryGateBlocksDependentWritesWithoutProjectHeader`
- `TestTreeScopeHTTPIsolation`
- `TestProjectContextRejectsForeignProjectBeforeReadOrWrite`

### Auth/preview first failure and bounded diagnostic

**Do not label the combined acceptance unconditionally green.** The first unchanged `login.browser.mjs` run timed out at line 103 waiting **8000ms** for `.login` after the fixture expired the session. The first failure is retained in `auth-preview-browser.log`; the serial runner therefore exited **1**, despite the real-Go browsers and scope checks passing.

One completed focused diagnostic repeated the auth/preview test with request/response, route and page-error logging, keeping the same 8000ms timeout and all business assertions. It passed, including all three preview child suites. Observed evidence:

- At fixture expiry + approximately **1.94s**, state/quality/alarms polling made real HTTP requests and received **401 `{"error":"unauthorized"}`**; a subsequent project-directory request also received 401.
- Original assertions verified navigation to login, removed token, no additional requests during the following **2300ms**, and successful login reload. No page errors were recorded.
- Preview overview/design/delivery checks passed, including Profile verification, live-token isolation and zero API requests from preview.
- The internal session event bus is not exposed as `window.uni.$on` in the production bundle, so the diagnostic cannot claim a direct `session-ended` observation. Token clearing, rendered login and stopped polling are the observed outcomes.
- Diagnostic setup attempts failed before the expiry assertion (shell quoting, then an unavailable debug event API). They did not constitute successful/failed expiry reproductions. The debug observer was guarded; no production file, timeout or assertion was modified. The setup error log is retained.

This one successful diagnostic does **not** fix or explain the first timeout, and does not establish a load-related cause. Auth browser stability remains unresolved. No repeat-until-green loop or unrelated lifecycle repair was performed.

## Evidence and limits

Local untracked evidence is under `.scratch/device-alarm-stats/`: `node.log`, `typecheck.log`, `build.log`, `go-full-first.log`, `go-full-exit.txt`, plus the browser/scope logs and runner. These logs are not committed.

- This is disposable software/simulator acceptance, not physical commissioning, field sign-off or power-loss durability evidence. No physical devices or physical writes were used.
- Daily statistics retain the upstream limitation: configuration survives restart; quality-bearing observations and communication windows are bounded process memory. Complete daily history across restart is not implemented. Missing metadata, gaps, resets and retention limits are exposed, not filled with zero or extrapolated.
- Alarm compaction bounds redundant checkpoints, not real lifecycle-history retention; no high-frequency persistence-throughput claim is made. Old journal counts absent from legacy evidence cannot be reconstructed.
- No original checkout, launch configuration, credentials or permission settings were changed. No push, public issue/PR/comment or other public action was performed. Merge/evidence commits are local and have no AI attribution trailer, per user preference.
