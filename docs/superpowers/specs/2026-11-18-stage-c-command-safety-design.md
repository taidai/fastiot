# Stage C 第一片：统一命令写入与安全判决

## 目的

手工命令、自动控制和命名命令在同一写入 seam 接受请求、执行安全判决，再交给协议适配器；失败与原因可进入报警和交付门禁。规格来源：GitHub issue #2 阶段 C。

## 目标

- 命令请求统一为 `accepted/confirmed/failed/unknown`，保留 `submitted/superseded` 作为旧读取方投影。
- 安全 verdict 输出 `allowed/clamped/refused + 原因`，覆盖范围、必要输入、条件、人工接管、只读/停用和输出归属。
- `command.Manager` 在 dispatcher 前调用安全判决；dispatcher 写入结果不丢弃。
- 自动决策表与 allocator 接入同一入口；manual takeover 只暂停自动控制，不禁止获准人工操作。
- 命名命令失败沿用现有报警和交付证据。

## 非目标与约束

- 不删除 Stage F 的旧实现，不重写命令生命周期或引入新数据库。
- 无映射回读仍为 `submitted`，不冒充设备已确认。
- 未声明安全配置的 Modbus 点位不做额外钳制。
- 不连接真实设备，不执行真实写入，只使用仿真与集成测试。

## Approaches

- **批准：在现有 command.Manager 前增加统一安全入口，再逐步迁移调用方。** 保留协议适配器和现有投影，最小变更。
- 每个 handler 各自补安全判断。会继续漂移，拒绝。

## 接口与行为

安全 verdict 接收请求值、当前值、声明范围/条件、互锁、人工接管和目标状态，输出允许/钳位/拒绝及原因。dispatcher 只在 verdict 非 refused 后执行。accepted 表示请求已受理，confirmed 表示已有真实回读，failed 表示明确失败，unknown 表示结果不可确认。

## 验证

- 安全表格：范围钳制、互锁拒绝、人工接管、只读/停用、必要输入和条件。
- command.Manager 集成：写入前后结果、重复请求、失败与未知、无回读 submitted。
- 自动决策表/allocator 经过同一入口，安全拒绝不产生协议写入。
- 全仓回归与独立 Standards/Spec 审查。
