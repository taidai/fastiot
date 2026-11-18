# Stage B 第二片：计算点与虚拟输入适配设计

## 目的

计算点、虚拟输入和普通观测共享同一可用性 verdict，同时保留配置常量不过期和人工/模拟输入 Epoch、TTL、重启核实语义。规格来源：GitHub issue #2 阶段 B。

## 目标

- `observation.Input` 增加 `AllowNoExpiry`，仅配置常量和既有固定试算等明确场景使用。
- `state.Value` 携带统一计算的 `ExpiresAt`，计算运行时不重复执行 `SourceTime.Add(MaxAge)`。
- 虚拟输入通过适配器输出同一 verdict：`bad → invalid`、`stale → stale`、`uninitialized → unknown`；配置常量 `AllowNoExpiry=true`。
- 计算批次和 API 共用 verdict；前端 `CalculationPoints` 同步到期边界。
- **有意行为变化**：`now == valid_until` 时计算点仍可用，`now > valid_until` 才 expired。

## 非目标与约束

- 不实现链路健康，不改变虚拟输入公开数据结构、Epoch、TTL、单调时钟或重启核实规则。
- 不让普通 Modbus 观测绕过 `expiry_unconfigured`，不把配置常量误判为无有效期。
- 不改变 Stage A 授权、真实设备行为或计算图/公式语义。

## Approaches

- **批准：窄适配层。** observation 增加无到期白名单，state/计算/虚拟输入各自适配，保留领域语义。
- 所有输入强制要求 `expires_at`。会把配置常量变成不可用，拒绝。

## 接口与行为

`observation.Input.AllowNoExpiry` 仅对明确不过期的输入放行。`state.Value.ExpiresAt` 与 quality 同源。`calculation` 提供样本/结果 freshness 适配，appapi 不再独立判断到期。虚拟输入现有 `Document.Evaluate` 保持原公开行为，适配发生在计算与 verdict 边界。

## 验证

- 配置常量永久可用；人工/模拟输入到期、Epoch 不符和重启待核实保持原语义。
- 普通 Modbus 无声明有效期仍为 unknown。
- 计算结果在 `now == valid_until` 时 good，超过后 stale；组合公式取最早到期。
- Go/前端回归、真实 HTTP 与浏览器验证，独立 Standards/Spec 审查。
