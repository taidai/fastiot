# Ticket 02 Observation Consistency Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task in the current session. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 两个只读模拟设备及同设备不同点位拥有独立、可信观测，点位页和设备页共用质量与时间解释。

**Architecture:** 深化现有 Channel → runtime/state → `/ems/state`，不新增采集服务。逐点失败只更新状态，不制造新值；新旧校验后的有效观测才发布到节点及正常历史路径。两页面使用同一观测 DTO 与展示转换。

**Tech Stack:** Go 1.27.3、现有 Simple IoT v0.31.0、React/Ionic/TypeScript；不新增依赖。

**Spec:** `.scratch/fastiot-core-first-slices/issues/02-two-device-observation-quality.md`；已批准设计 `docs/superpowers/specs/2026-11-15-ticket02-observation-design.md`。基线 `d9e1d284936af07709368e5f2ff62836a4e20d26`。

## Global Constraints

- 仅任务02；不扩展 EMS 策略、通用 Unit、换机、历史回填、全平台计时、真实设备或外部 AI。
- 新值、最后有效值、观测时间、接收时间、尝试失败原因分开；无有效观测显示“—”，不填零。
- 只使用隔离只读模拟来源和临时账户；不泄露密码／令牌，不改已有未跟踪材料，不推送。
- 保留任务01配置零写入回归；测试与临时验证程序不能产生真实协议控制。
- 用户已指定在当前会话继续实施；不另开写代码的会话，按依赖顺序逐条红—绿，最后独立审查、本地提交。

## Review Focus

- 首次采集失败无旧值：不得产生默认0；Task 2 的 API 用例覆盖。
- 相同时间但矛盾值：不能靠收到更晚就认作可信新样本；Task 1 覆盖并说明不可确认。
- 无效未来样本之后可靠时间恢复：错误样本不得污染最后可信顺序基准；Task 1 覆盖。
- 一个块失败、另一个块持续成功：总线／设备诊断不得覆盖成功点；Task 2 覆盖。
- 页面切换或状态查询失败：不能沿用别的对象数据或把旧缓存标正常；Task 3 覆盖。

---

## 文件职责

- `internal/ems/channel/channel.go`、`channel_test.go`：成功观测及逐点失败描述；物理协议假 Bus 用于测试。
- `internal/ems/state/state.go`、`state_test.go`：当前值、时间顺序、质量与原因；原 `SetHealth` 保留诊断用途。
- `internal/ems/runtime/service.go`、`runtime.go` 及对应测试：共用观测接收与下游过滤，不把被拒绝输入重新发布。
- `internal/ems/bridge/bridge.go`、`nodesource.go` 及测试：向节点传递接受观测的原时间，不以转发时间续鲜。
- `internal/emsapi/emsapi.go`、`emsapi_test.go`：现有状态接口增加必要来源／原因信息，保留对象范围检查。
- `frontend/src/api.ts`，新 `frontend/src/observations.ts`，`PointsView.tsx`、`DeviceDetail.tsx`：同一类型、状态查询与展示逻辑。仅按需要接线，不重构导航和样式。
- 已有协议生产者如需填时间来源标签，仅补元数据，不改变协议功能或依赖。
- `evidence/ticket02-observation-quality.json`：隔离验证记录，实际运行后填写，不预写通过。

## Task 1: 接受可靠当前观测，拒绝乱序与续鲜

**Interfaces:** 保留 `Store.Get(now, deviceID, pointID, maxAge)`、`Samples`、`Snapshot`；`Store.Apply(now time.Time, results []channel.Result)` 改为返回 `[]channel.Result`，仅含新接受的有效观测。`state.Value` 增加 `Reason`、`TimeSource`、`LastAttemptTime`，原 SourceTime/ReceivedTime 代表所展示有效观测。失败／不可信尝试不改写最后可信数值及其时间。

- [ ] 写 `TestOlderAndDuplicateObservationsDoNotRefreshCurrent`：先接受 t0+5s 的值37，再给 t0+2s 的12和同一37/同时间重复；Get仍为37，原 SourceTime、ReceivedTime不变，Apply后两次返回空。随后同值37、t0+6s的新观测必须接受。
- [ ] 运行 `go test -count=1 -run TestOlderAndDuplicateObservationsDoNotRefreshCurrent ./internal/ems/state`，确认先失败。
- [ ] 最小实现顺序及重复判断；重复只在身份、时间、值等可识别依据一致时成立，不按数值相同去重。
- [ ] 重跑该测试，确认通过。
- [ ] 下一轮写 `TestUntrustedTimeDoesNotPoisonRecovery`：缺来源时间、超接收时刻的异常未来样本、同戳矛盾值不成为可信当前；无旧值仍无值，有旧值保留原值并可解释异常；后续可靠新观测恢复。旧来源时间按可确认较旧处理，不宣称能辨别所有真实设备回拨与传输乱序。
- [ ] 逐条红—绿后，令 Service.Observe、循环采集及旧 Engine 接收路径只将 Apply 返回的有效观测写入正常历史和 OnResults；测试 `TestRejectedObservationDoesNotRepublishOrRefreshHistory` 断言较旧／重复／异常样本不增加发布和历史。
- [ ] 更新 Bridge 的 `Source.PublishValue(ioNodeID string, value signal.Value, sourceTime time.Time) error` 及实现／测试假源；发布使用传入观测时间，拒绝值不会被改成当前时刻重发。
- [ ] 运行 `go test -count=1 ./internal/ems/state ./internal/ems/runtime ./internal/ems/bridge`，再跑任务01的点位回归，确认其配置隔离与显式写入不退步。

## Task 2: 逐点失败与公共状态接口

**Interfaces:** `channel.Result` 增加 `Error` 和 `TimeSource`；成功结果标明平台采集时间，失败结果是该点读取／解码失败事件，不携带假数值。状态接口保留现有字段并增加 `reason`、`time_source`、`last_attempt_time`；无有效时间明确为空／未取得，不显示伪造采样时刻。

- [ ] 写 `TestReadFailureIsLocalToPoint`：两个设备，其中一设备有分离的两个读取块；只让一个块失败。成功点继续返回有效37，失败点报告读取失败，不产生0；另一设备正常，不受连带失效。
- [ ] 运行该 Channel 测试看到失败，然后在读取失败和解码失败分支生成对应点事件，保留健康统计但不抹掉成功结果；重跑通过。
- [ ] 写 `TestPointFailurePreservesLastObservation`：失败点有旧观测时保留旧值、原 SourceTime／ReceivedTime，质量／原因变为失败；首次失败无值；下一次成功恢复。设备整体 Offline 仅作诊断，不无条件推翻同次成功点。
- [ ] 先红再改 Store 的逐点失败处理与 effective 质量；替换旧的整设备强制 stale 测试，并保留真正观测超龄测试。
- [ ] 写 `/ems/state` 的 `TestStateExposesIndependentPointQualityAndTime`：通过实际采集／Observe输入取得 A/B 与局部失效，核对 DTO 值、双时间、来源／原因和权限过滤；随后最小补接口字段并重跑。
- [ ] 运行 `go test -count=1 ./internal/ems/channel ./internal/ems/state ./internal/ems/runtime ./internal/ems/bridge ./internal/emsapi`。结果中不允许新失败事件被旧消费者当正常数据发布。

## Task 3: 两个页面同一含义与隔离验收

**Interfaces:** 扩充现有 `EmsStateValue`，通过已有 `fetchEmsState(token)` 读取；`observations.ts` 提供共用显示值、质量文案、原因及时间转换。PointsView 对 Modbus 点以观测接口为数据依据；DeviceDetail 使用同一转换。节点保留配置和目标值，不再作为第二套实时质量真相。

- [ ] 在隔离界面复现旧分歧：一个站号／块失败而另一个成功，记录点位页和设备页不同质量；失败步骤和原始观测对应，不伪造截图。
- [ ] 最小接线共用状态读取和展示。未取得观测显示“—”；服务查询失败明确说明，不退回节点缓存并标正常；取消旧对象请求／卸载后的回包避免串对象。
- [ ] 运行 `npm --prefix frontend run build`，类型检查通过后再进行浏览器验证；不借机重做样式或导航。
- [ ] 使用 preview 工具启动临时真实界面／公共API／现有Bridge的隔离夹具，协议层只读假Bus或本地明确模拟器；从界面创建两来源并检查数值差异、恒值新采样、单点／整设备失败及恢复、乱序／重复／异常时间。分别注明软件模拟与实际协议覆盖，不冒充RTU／真机。
- [ ] 检查两个页面的值、单位、观测／接收时间、最后有效值及原因一致；注入查询失败和切换对象，确认无正常假象或错对象回包。检查控制台／网络并留下脱敏证据。
- [ ] 完成所有红—绿后运行 `go test -count=1 ./...` 和前端构建；记录失败或未覆盖项，不用测试文件存在代替执行。
- [ ] 调用 code-review 双轴审查，处理本票问题后重跑相应检查；停止隔离实例、恢复临时启动配置，只提交本票代码、测试、设计／计划和证据，不推送；本地任务02据实际验收更新状态。

## 执行环境

本机无 Go PATH 时使用已有 `golang:1.27.3` 容器和模块／构建缓存，运行验证时仓库只读挂载并关闭外部容器网络；Windows Git Bash 使用 `MSYS_NO_PATHCONV=1`。不运行会清理旧证据或连接既有现场的整套脚本。临时 UI 夹具启动只通过 preview 工具，凭据随机、只在临时环境使用，不进入日志、文档或导出。

## 自检

任务02的八项验收分别由 Task1 的顺序／时效、Task2 的独立观测／接口、Task3 的两页真实任务覆盖。无关控制算法与后续虚拟输入／计算能力未带入；跨调用路径过滤、旧测试语义和失败回包是审查重点。工程中的三个步骤共享同一观测契约和文件，不宜并行写入。
