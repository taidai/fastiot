# Stage B 最后一片：通道 link-health 设计

## 目的

通道、设备分配和报警监视共享同一链路健康判决，避免 unknown 被当成健康或故障。规格来源：GitHub issue #2 阶段 B。

## 目标

- 一个模块输出 `healthy/degraded/offline/unknown`、年龄和原因。
- `channel.Health` 委托该模块，交付、预检、验收和 EMS 自动继承。
- `alarmwatch`、旧 `alerts.Monitor`、`decision.busHealthy` 使用同一 verdict。
- 语义统一：创建宽限期内无观测为 unknown；宽限期后仍无成功为 `offline/no_success`；成功但过期为 `offline/source_expired`；有最近成功但失败为 degraded；未来时间异常为 `offline/invalid_time`。
- `busHealthy` 保持 healthy/degraded 可用于分配，offline/unknown 不可用于分配。

## 非目标与约束

- 不删除 Stage F 的旧实现，不改授权、公式、真实设备行为或观测 verdict。
- 不把 unknown 当 healthy，也不把 unknown 当 offline。
- 保留现有报警与分配的调用方式和状态词汇。

## Approaches

- **批准：健康模块加适配器。** 所有现有消费者复用同一判决，不重写分配器或报警引擎。
- 各 handler 继续独立判断。会保留阈值漂移，拒绝。

## 模块接口

`health.Evaluate` 接收请求次数、连续失败、最后成功时间、poll、timeout、MaxAge 和创建时间，返回 state、age 和 reason。MaxAge 未显式提供时使用 `max(5s, 5×poll, 3×timeout)`。无成功且请求次数未知时保持 unknown；已有请求但从未成功按离线处理。报警适配器用请求次数 1 表示已配置的轮询通道，并保留创建宽限期。

## 验证

- unknown/创建宽限期、无成功、过期、连续失败、未来时间和分配行为表格测试。
- channel/alarmwatch/busHealthy 既有测试回归。
- 全仓 Go/前端检查、真实通道行为验证和独立 Standards/Spec 审查。
