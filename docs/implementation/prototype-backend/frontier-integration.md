# Prototype backend frontier integration — 2026-11-25

Local integration only. Parent baseline `09a41dd2e6f725743412ff91fbc28eb482ddbb08`; verified code HEAD `b74d6e23ef8736c66746d23a38e1dbc9471399e2`. This evidence commit changes no implementation.

## Merges

Normal local merges, in the requested order:

| Ticket | Delivered SHA | Merge SHA |
| --- | --- | --- |
| 04 | `e90bd7e3afca9c5f9d96c9532862e327f9a33e7e` | `3ec9fdbf35cf9bf5249f5139724c216016a376b8` |
| 19 | `853d5a70d43c3155c0860742f633343d302bcd29` | `de84402da81b76d07491cb88b0c9b41f1687a396` |
| 10 | `1d398b08f2d51e23efbc960f4d659d2a620dd8ca` | `b39937961c6960f7c7d647a1a3d03d22d48ea003` |
| 09 | `16105eeb9352a36cb106f44ccb4f1114f5d8c1b1` | `b74d6e23ef8736c66746d23a38e1dbc9471399e2` |

All four delivered tips and the parent baseline were checked as ancestors of the verified code HEAD. Ticket 05 was not merged.

## Conflict resolutions and preserved boundaries

- `internal/appapi/handler.go`: ticket 19 extracts authorization into `routeAuthorization` for enforcement and read-only explanations. Ticket 04's runtime-root scope check for industry activation is retained inside that shared function, after the action check, returning the same status/reason. No duplicated dispatch or permission bypass. Explicit scoped-admin intersection and account grant/scope ceilings remain intact.
- `frontend-uniapp/src/pages/overview/LiveOverview.vue`: retain ticket 04's `IndustryGate`/`v-else` wrapper and ticket 09's screen error feedback inside the workbench body. Combine imports for the gate, project generation and unmount cleanup. Session error feedback remains outside the gated body.
- `session.ts` merged additively: snapshot-derived industry state plus runtime-error clearing, serial/session/project fences and polling invalidation. History DTO additions are retained. Delivery system/industry repair remains reachable while asset work is gated.
- Reviewed baseline-relative diffs: API client auth/project fencing is unchanged apart from industry error strings; project-copy ACK code is untouched (only the dependency classifier changes in `project_package.go`); upgrade ACK code and ticket 03 member-version/placement implementation remain intact.

## Fresh frontend verification

Executed against the combined code, not implementer logs:

```sh
node --test 'frontend-uniapp/src/**/*.test.mjs'
npm --prefix frontend-uniapp run type-check
npm --prefix frontend-uniapp run build:h5
```

All **126 Node tests passed**, zero failures. Type-check and production H5 build passed. `git diff --check` passed. The resolved Go handler matches Go 1.27.3 `gofmt` after CRLF normalization; no whole-file newline rewrite was made.

## Fresh Go verification: initial failure, then complete serialized run

Source was created with `git archive` of the verified code HEAD and extracted into named volume `fastiot-prototype-integration-tests`, private subdirectory `/src/frontier-merge`. Image: `golang:1.27.3` (`go version go1.27.3 linux/amd64`). Reused module/build caches `fastiot-ticket02-gomod` / `fastiot-ticket02-gocache`. No Windows source bind mount. Built H5 files were separately copied into this archived source for the account browser fixture.

The first full eight-package run, using default package concurrency, **failed**: `appapi` terminated at 12.978s with `listen tcp 127.0.0.1:9743: bind: address already in use`. The other seven packages passed. Evidence identifies a pre-existing cross-package fixture collision:

- `appapi.TestRichCapabilityFields`, `capability_rich_integration_test.go:25-26`: HTTP 9742 and NATS 9743.
- `decision.TestDecisionTableOverModbusRTU`, `rtu_linux_test.go:47-49`: NATS monitor 9742 and NATS WebSocket 9743.

The same complete package set was then run with package concurrency serialized; **no production/test code change, test exclusion or weakened assertion**:

```sh
go test -p 1 ./internal/authz ./internal/appapi ./internal/domain/access \
  ./internal/emsapi ./internal/ems/historyquery ./internal/ems/history \
  ./internal/industry ./internal/decision -count=1 -timeout=20m
```

All passed: authz 0.007s; appapi 92.561s; domain/access 0.007s; emsapi 0.055s; historyquery 0.022s; history 0.002s; industry 0.019s; decision 18.627s. This includes the parent copy/upgrade ACK regressions and the new account/industry/history/overview HTTP checks. Ticket 09's previously disclosed copy-test failure did not recur in this complete serialized run; that does not erase its earlier evidence or establish repeated-run stability.

## Fresh browser verification

All browser scripts used this worktree's newly built H5. Disposable Go fixtures used the same combined archive, separate containers/loopback ports, opt-in environment flags and temporary storage. No inherited launch configuration, original checkout data or physical devices were used.

| Check | Boundary | Result |
| --- | --- | --- |
| `login.browser.mjs` plus overview/design/delivery preview suites | Self-contained HTTP fixture; token isolation, keyboard login, 401/403/500/offline | Passed |
| `projects.browser.mjs` | Two-context HTTP fixture; dirty cancel/discard and delayed response | Passed |
| `projects.browser.mjs` | Real Go, `FASTIOT_CONTEXT_BROWSER=1`, `TestProjectContextBrowserBackend`, port 18082 | Browser passed; Go 6.812s |
| `ledger.browser.mjs` | Real Go, `FASTIOT_LEDGER_BROWSER=1`, `TestLedgerBrowserBackend`, port 18083 | Browser passed; Go 11.994s; includes member conflict and copy references |
| `industry.browser.mjs` | Real Go, `FASTIOT_INDUSTRY_BROWSER=1`, `TestIndustryGateBrowserBackend`, port 18084 | Browser passed; Go 5.325s; all workbench gates and recovery |
| `live-overview.browser.mjs` | Deterministic HTTP fault/race fixture | Passed |
| `live-overview.browser.mjs` | Real Go plus HTTP fault proxy, `FASTIOT_OVERVIEW_BROWSER=1`, `TestOverviewLiveBrowserBackend`, port 18089 | Browser passed; Go 14.960s |
| `history.browser.mjs` | Real Go, `FASTIOT_HISTORY_BROWSER=1`, `TestHistoryBrowserBackend`, port 18090 | Browser passed; Go 6.726s; raw/mean quality, CSV bounds, scope, pagination |
| `accounts.browser.mjs` | Real Go, `FASTIOT_ACCOUNT_BROWSER=1`, `TestAccountsBrowserFixture`, port 18099 | Browser passed; create/edit/reload, server explanation, scope denials, 1440/1280/1024/375px |

The account runner hardcodes its ticket-specific source volume. A scratch-only copy changed only source/cache mounts and working directory to `/src/frontier-merge`, and preserved module/relative-file resolution; its browser assertions were unchanged. Its long-lived Go fixture is intentionally stopped by the runner after browser completion, not reported as a normally completed Go test. Other five real-Go fixtures finished normally via their test-only finish endpoints. All owned fixture containers were removed; pre-existing containers were untouched.

## Evidence and limits

- Local logs and orchestration: `.scratch/frontier-merge/`, including `go-full.log` (initial failure), `go-full-serialized.log`, Node/type/build logs, separate browser/Go-fixture logs and `real-browser-results.json`. Screenshots remain uncommitted under the runners' scratch locations.
- Opt-in browser harnesses are skipped in ordinary package tests; the six relevant real-Go harnesses above were explicitly exercised separately. No skips were added. No broader `go test ./...`, race-detector campaign or physical commissioning claim.
- Browser evidence is self-contained Playwright, not Browser-pane or an exhaustive visual matrix. Existing launch settings were not used or modified.
- No reset/stash/checkout, original-checkout edits, credential/permission/launch edits, README status updates, ticket 05 merge, push, issue, PR or external comment. Merge commits are local with no coauthor trailers. The intended final worktree state is clean except `.scratch/`.
