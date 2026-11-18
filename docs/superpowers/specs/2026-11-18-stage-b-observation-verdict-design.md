# Stage B 第一片：观测判决与展示

## 目的

同一观测在状态、规则、报警与前端页面中得到一致的可用性结论和原因，避免页面各自用 15 秒猜测新鲜度。规格来源：GitHub issue #2 阶段 B。

## 目标

- 一个 Go 模块输出 `usable/stale/invalid/unknown`、稳定原因、观测时间、接收时间和 `expires_at`。
- `state` 与 EMS `/state` 使用该模块，规则和报警通过 `State.Samples` 继承同一判决。
- 前端统一由 `describeObservation` 和服务端 `expires_at` 展示；删除 `ScreenView`、`App`、`EnergyFlow`、`AssistantPanel` 的本地 15 秒新鲜度判断。
- 未声明到期时间时显示 `unknown`，不声称正常或过期。
- 表格测试、真实 HTTP 和浏览器验证同一观测跨页面一致。

## 非目标与约束

- 不实现链路健康模块；它是后续独立切片。
- 不改变计算点的 `ValidUntil` 或虚拟输入 Epoch、TTL、单调时钟语义；后续用适配器接入。
- 不改变 Stage A 授权与范围行为，不连接真实设备，不执行真实写入。
- 保留已有质量原因；未来时间和明确无效输入仍为 invalid。

## Approaches

- **批准：先统一普通观测与展示。** 新模块服务 state/EMS/前端，计算点和虚拟输入后续适配。改动可独立验收。
- 一次迁移所有消费者和链路健康。范围更大，容易混淆普通观测与特殊时间模型，拒绝。

## 模块接口与行为

`internal/ems/observation` 接收观测质量、值、声明有效期、观测/接收时间和当前时间，返回包含状态、原因和时间依据的 verdict。`expires_at` 与状态使用同一有效期输入，不再分别计算。

`describeObservation` 只解释 verdict 和服务端时间字段；本地时钟仅用于估计当前时间相对服务端 `evaluated_at` 的偏移。没有 `expires_at` 时不根据图距或轮询周期推断过期。

## 验证

- Go：质量、未来时间、到期边界、未声明有效期、原因和 `expires_at` 一致性。
- HTTP：同一快照的值、质量、原因和到期字段一致。
- 前端检查与真实浏览器：总览和画面对同一观测显示相同标签；未声明到期时间不显示“正常”。
- 全仓 Go 与前端既有检查回归。
