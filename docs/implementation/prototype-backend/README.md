# Prototype backend integration — execution graph

Branch: `integration/prototype-backend-20261125`

Specification: [latest prototype backend integration](../../latest-prototype-backend-integration-spec.md).

These 25 approved-scope local ticket drafts track implementation while GitHub publication remains separately authorized. They are not GitHub issues and no tracker items have been closed.

Confirmed replica rules: remove non-prototype enhancements from the replica surface; preserve explicit prototype placeholders; share preview state across workbenches and reset to a consistent baseline on reload.

## Baseline

The worktree starts at the current committed source plus only task-related pending source copied from the original checkout. The original checkout is untouched. Scratch files, executables, screenshots, local credentials and launch settings are excluded from the new checkpoint. Existing history still contains the previously reported debug credential; no history rewrite or publication is authorized. Mocked-login test fixtures use an inert string instead of the local instance password.

## Ticket graph

| Ticket | Deliverable | Blocked by | Status |
|---|---|---|---|
| 01 | [真实登录与预览隔离](01.md) | None (can start immediately) | integrated — 3374d57 |
| 02 | [项目选择与三工作台上下文统一](02.md) | 01 | integrated — 8106978 |
| 03 | [从模板建立并维护真实台账](03.md) | 02 | integrated — 3dceb63 |
| 04 | [行业包依赖门禁](04.md) | 02 | integrated — 3ec9fdb |
| 05 | [单设备 Profile 接入与只读验证](05.md) | 03 | in progress — isolated worktree |
| 06 | [批量接入与 CSV 逐台结果](06.md) | 05 | pending |
| 07 | [操作抽屉与命令回读闭环](07.md) | 05 | pending |
| 08 | [L3 安全写回读验证](08.md) | 07 | pending |
| 09 | [按层总览与可信实时数据](09.md) | 02 | integrated — b74d6e2 |
| 10 | [历史查询、断线曲线与 CSV](10.md) | 02 | integrated — b399379 |
| 11 | [告警生命周期与交接持久化](11.md) | 09, 10 | queued — frontier open |
| 12 | [今日电量、运行时长与健康统计](12.md) | 09, 10 | queued — frontier open |
| 13 | [四段式规则与显式作用范围](13.md) | 07 | pending |
| 14 | [状态机定义、模拟与受控执行](14.md) | 07 | pending |
| 15 | [持续运行的聚合点](15.md) | 09, 10 | queued — frontier open |
| 16 | [今日执行统一查询](16.md) | 13, 14 | pending |
| 17 | [配界面保存到客户总览](17.md) | 09 | queued — frontier open |
| 18 | [派生输入与已配置画面的联动验收](18.md) | 13, 14, 15, 17 | pending |
| 19 | [账号、对象范围与只读权限演练](19.md) | 02 | integrated — de84402 |
| 20 | [真实预检、不可变发布与包导出](20.md) | 06, 13, 14, 17 | pending |
| 21 | [跨实例导入与现场重绑](21.md) | 20 | pending |
| 22 | [真实部署记录与运行检查](22.md) | 21 | pending |
| 23 | [工程版本回退](23.md) | 20 | pending |
| 24 | [当前版本的审计与验收证据](24.md) | 19, 20, 22, 23 | pending |
| 25 | [原型全状态与真实后端联合验收](25.md) | 04, 08, 11, 12, 16, 18, 24 | pending |

## Execution log

- Baseline checkpoint: `55b40dc`; 110 frontend tests and type-check passed. Go authz, decision, appapi and domain/access suites passed from an isolated Docker named volume (appapi 106.5s).
- Ticket 01 runs on `refs/heads/ticket/prototype-backend-01`, explicitly based on the integration checkpoint. The automatic agent worktree initially chose a different base; that mismatch was detected before editing and replaced with the correctly based worktree.
- Ticket 01 integrated by fast-forward at `3374d57`. Merger independently passed lockfile install, type-check, six authentication tests and diff whitespace checks; broader browser/Go evidence is in ticket 01, not rerun by merger.
- Parent independently reran `test:auth:browser` on the integrated branch: production build, default live routing, real keyboard login, 403/500/offline without fallback, bearer session, 401 poll cancellation, explicit preview, token isolation and three preview workbench suites passed. This browser boundary used an isolated HTTP simulator, not a full Go stack; physical writes were zero.
- Ticket 02 integrated at `8106978`. Merger independently passed type-check, 11 auth/context tests, build, isolated two-context browser fixture and diff check. Implementer real-Go/restart evidence remains in ticket 02 and was not rerun by merger.
- Frontier is now 03, 04, 09, 10 and 19; each receives an isolated worktree from the same integrated baseline.
- Fresh integrated Go regression failed `TestProjectUpgradeRefusesUnresolvedConflictUntilSomeoneChooses`: expected the chosen template asset code but immediately read the old value. Authz, decision and domain/access passed. Ten unchanged isolated repetitions passed, so retry success is not treated as a fix.
- Root cause: upgrade used fire-and-forget configuration point writes before freezing its after-version. A deterministic NATS acknowledgement regression failed before the fix; the implementation now requests store acknowledgement and the test also checks frozen-version contents. All `TestProjectUpgrade*` tests passed ten repetitions (34.2s). Fresh integrated authz, decision, appapi and domain/access suites then passed (`appapi` 96.7s).
- Ticket 03 integrated and independently verified on `3dceb63`: 121 frontend tests/types/build, Go appapi/authz/domain-access, real-Go ledger browser and auth/preview regressions passed. Members and their edit version are separate acknowledged writes; crash-atomicity between them is not established.
- The integration tip was held at `3dceb63` while 04/09/10/19 finished, avoiding repeated rebases of their final verification. All four commits are now delivered and queued; ticket 05 is running separately.
- Ticket 09 exposed an existing intermittent `TestCalculationProjectCopyRemapsDraftReferences` failure (only two copied definitions visible immediately). Its unchanged three-repeat rerun passed but was not treated as a fix. Parent traced the same unacknowledged rewrite pattern in project reuse; deterministic acknowledgement assertion failed, then passed with a one-line store-ACK fix. Ten repetitions passed, followed by full appapi/authz/domain-access (`appapi` 76.8s).
- Completed frontier integration at `c766f46` (verified code `b74d6e2`): 04/19/10/09 merged with combined guards and fences; 126 frontend tests/types/build, all eight Go packages using `-p 1`, and six real-Go browser fixtures passed. The first parallel Go run failed due to fixed-port 9743 collision, retained in [integration evidence](frontier-integration.md); serial rerun excluded no tests.
- Next frontier: 11, 12, 15, 17; ticket 05 continues and will consume this stable baseline. Integrated count: 7/25.
- Local baseline and per-ticket verification must be recorded before marking a ticket complete.
