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
| 01 | [真实登录与预览隔离](01.md) | None (can start immediately) | pending |
| 02 | [项目选择与三工作台上下文统一](02.md) | 01 | pending |
| 03 | [从模板建立并维护真实台账](03.md) | 02 | pending |
| 04 | [行业包依赖门禁](04.md) | 02 | pending |
| 05 | [单设备 Profile 接入与只读验证](05.md) | 03 | pending |
| 06 | [批量接入与 CSV 逐台结果](06.md) | 05 | pending |
| 07 | [操作抽屉与命令回读闭环](07.md) | 05 | pending |
| 08 | [L3 安全写回读验证](08.md) | 07 | pending |
| 09 | [按层总览与可信实时数据](09.md) | 02 | pending |
| 10 | [历史查询、断线曲线与 CSV](10.md) | 02 | pending |
| 11 | [告警生命周期与交接持久化](11.md) | 09, 10 | pending |
| 12 | [今日电量、运行时长与健康统计](12.md) | 09, 10 | pending |
| 13 | [四段式规则与显式作用范围](13.md) | 07 | pending |
| 14 | [状态机定义、模拟与受控执行](14.md) | 07 | pending |
| 15 | [持续运行的聚合点](15.md) | 09, 10 | pending |
| 16 | [今日执行统一查询](16.md) | 13, 14 | pending |
| 17 | [配界面保存到客户总览](17.md) | 09 | pending |
| 18 | [派生输入与已配置画面的联动验收](18.md) | 13, 14, 15, 17 | pending |
| 19 | [账号、对象范围与只读权限演练](19.md) | 02 | pending |
| 20 | [真实预检、不可变发布与包导出](20.md) | 06, 13, 14, 17 | pending |
| 21 | [跨实例导入与现场重绑](21.md) | 20 | pending |
| 22 | [真实部署记录与运行检查](22.md) | 21 | pending |
| 23 | [工程版本回退](23.md) | 20 | pending |
| 24 | [当前版本的审计与验收证据](24.md) | 19, 20, 22, 23 | pending |
| 25 | [原型全状态与真实后端联合验收](25.md) | 04, 08, 11, 12, 16, 18, 24 | pending |

## Execution log

- Frontier starts at 01; 02 follows. Independent branches open after 02 passes integration.
- Local baseline and per-ticket verification must be recorded before marking a ticket complete.
