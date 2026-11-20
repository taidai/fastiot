# OpenRemote 能力吸收分析：哪些值得反哺 FastIOT

状态：研究文档，本轮只产出分析与方案，不改 FastIOT 生产代码。
核验日：2026-11-20。
对象版本：OpenRemote **1.34.1**（2026-11-17 发布，[R38]）。文档以官方 `documentation` 仓库固定提交 `0af6d26`（2026-11-17，[R39]）为准；源码行为核对固定于 `openremote/core` 提交 `9cab6ca`（2026-11-19）。

## 五行结论

1. OpenRemote 值得学的是**运营闭环**——告警到人、对象级可见性、只读共享、事件订阅、出站集成；不是它的 Java/Keycloak/多容器技术栈。[R1][R10][R18][R23]
2. 这些能力几乎都有硬前提：Keycloak/OIDC、SMTP/FCM、PostGIS、原生移动端或多实例 Manager。在“单 Go 服务 + 底座可独立交付 + 离线运行”的约束下，只能吸收模型，不能吸收实现。[R7][R17][R20][R23][R31]
3. FastIOT 在控制真值、项目包版本、审计、两工作台和“无自由脚本”上，设计与验收口径已经领先；照搬 Groovy/Flow、边缘互联、协议大目录会把产品拉回一个更重、更难验证的方向。[R12][R14][R20][R35]
4. 当前最直接的两个缺口是：**客户工作台只在 UI 层分层，数据层仍向客户角色下发工程节点**；**告警没有出站通道，只能等人看屏幕**。两者都能用加法解决，不动控制链路。[F2][F6]
5. 建议吸收 5 项，全部可选、默认安全、可审计；AGPL 源码零复制，只按公开文档与公开行为独立实现。[R37]

## 0. 证据边界

- **读了什么：** 官方文档站对应源码中的 34 份页面：介绍、总体架构、Manager 端点、安全检查、资产校验、位置跟踪、Apps/Consoles、资产/Agent/属性、Agent 总览、Manager API、规则四页、预报、GeoJSON 围栏、身份三页、边缘网关、自动注册、地理编码、通知、Services、ML 预报、EMS 领域、车队、异常检测、MCP、可观测性、Docker Profiles、系统管理、扩展、Manager UI。[R1]–[R34]
- **源码只核对行为，不复制：** `AgentLink.java` 与 `AbstractProtocol.java` 固定提交核对“写入即回填”语义仍在。[R35][R36]
- **没做什么：** 未部署或登录 OpenRemote 演示站，未运行其产品，未连接任何设备，未执行 FastIOT 全量测试。本文所有 OpenRemote 行为均来自官方文档或固定源码；所有 FastIOT 现状均来自当前工作树检索（附录 A）。
- **不采用的来源：** 二手博客、聚合站、未固定版本的 `main` 链接。

## 1. 旧调研的已确认事实与空白

仓库已有两份 OpenRemote 调研：[旧调研：可借鉴边界](research-openremote-2026-11-14.md)（下称 R-1114）与 [旧调研：建模专题](research-openremote-modeling-2026-11-14.md)（下称 M-1114）。本轮在其上推进，不重复建模细节。

| 旧结论 | 本轮复核（1.34.1 文档 / 固定提交） | 结论变化 |
| --- | --- | --- |
| Asset 中心、Manager 为 Java context broker，官方部署为多容器栈（Manager、PostgreSQL/PostGIS/TimescaleDB、Keycloak、HAProxy，可选 Map） | 一致；新增 Docker Compose profiles 说明 proxy/manager/postgresql/keycloak/map 五个服务。[R31] | 无 |
| Asset/Attribute/描述符/实例 meta 分工；类型模型官方文档明确“当前仅 Java 配置” | 一致，原文仍写明类型模型只能在 Java 中配置，Manager UI 配置是长期目标。[R8] | 无 |
| 资产校验要求值通过四个来源的约束（值类型、属性描述符、描述符 meta、实例 meta），实例不能静默放宽 | 一致；文档仍写明四来源累积生效、数组按元素递归校验。[R5] | 无 |
| Agent 是特殊 Asset，与协议实例一对一；`agentLink` 连接属性；入/出站转换 | 一致，并补充 `valueFilters`（regex/JSONPath/substring）、`%VALUE%`/`%TIME%` 注入、可执行属性 `REQUEST_START`。[R9] | 细节补齐 |
| 规则有 When-Then、Flow、Groovy 三种，可调度 | 一致；When-Then 动作明确包含通知、Webhook、生成告警；规则有频率与重复日程。[R11][R12][R34] | 无 |
| 历史在 `asset_datapoint`，Timescale 压缩与清理 | 一致；1.34.1 文档写明**默认无限期保留**，`OR_DATA_POINTS_MAX_AGE_WEEKS` 系统级、按 7 天 chunk 删除；Manager UI 配置表仍写“默认一个月”，两处表述冲突未裁定。[R8][R32][R34] | 冲突仍在 |
| 核心为 AGPL-3.0-or-later，依赖另有条款 | 1.34.1 的 `LICENSE.txt` 仍为 AGPL-3.0-or-later；FastIOT 自身为 Apache-2.0。[R37] | 无，但吸收边界更严格 |
| —（旧调研未覆盖） | 身份与访问：Realm、受限用户-资产链接、属性级 restricted read/write、公开读写、Service User、AD/LDAP、2FA。[R17][R18][R19][R34] | 本轮新增 |
| — | 通知：邮件/推送、通知总览、按资产关联用户路由、投递状态。[R23] | 本轮新增 |
| — | 边缘与量产：Edge Gateway 互联/隧道、X.509 自动注册、MQTT 客户端 API。[R10][R20][R21] | 本轮新增 |
| — | 空间：location 属性、geofence、GeoJSON 区域、地理编码、地图页。[R6][R16][R22][R34] | 本轮新增 |
| — | 预报/异常/Services/扩展/MCP：WEA 预报、预测点、ML 服务、异常检测（文档标注未实现）、Services 页、build-time Extension、MCP server（Beta）。[R24][R25][R28][R29][R33] | 本轮新增 |

**两份旧调研的结论未被推翻。** 本轮新增的是“身份/通知/边缘/空间/运营闭环”这些此前没覆盖的面，以及一组可以直接落地的吸收判断。

另注：仓库 [iot-platform-benchmark.md](iot-platform-benchmark.md) 记录了 OpenRemote 1.33.0 的能力索引，其中“FastIOT 告警未接线”的描述已落后于当前代码——当前工作树已有完整告警路由与前端页面（[authz/routes.go:201](../internal/authz/routes.go:201)）。本文以当前代码为准。

## 2. 能力清单：OpenRemote 提供的能力与 FastIOT 现状

每条给出“解决什么问题 / 依赖什么架构前提 / FastIOT 现状与检索依据”。检索命令与结果见附录 A。少数条目（如 Flow、Groovy）FastIOT 已有替代或主动不做，保留在此是为了说明判断依据，不代表都是缺口。

### 2.1 身份与访问

| # | 能力 | 解决的问题 | 架构前提 | FastIOT 现状 |
| --- | --- | --- | --- | --- |
| 1 | **受限用户按资产链接**：用户与资产多对多，与树位置无关；属性用 `Access restricted read/write` 控制具体读写。[R17][R18] | 同一现场里不同客户/住户只看自己的对象，且看不到别人的工程配置 | 用户目录 + 用户-资产链接表 + 统一读写路径上的属性级校验 | 只有“用户→根锚点”的子树范围与 4 个角色；快照接口按锚点过滤，但把 `modbus`、`opcua`、`decisionTable`、`deviceTemplate` 等配置类节点一并返回给所有角色（[handler.go:741](../internal/appapi/handler.go:741)）。无私点级/属性级可见性（附录 A S1） |
| 2 | **公开/匿名读写（逐属性）**：资产默认私有，可对单属性设 `Access public read/write`，用于公开仪表盘。[R18][R34] | 无账号大屏、对外展示 | 未认证请求路径 + 逐属性公开标志 + 公开资产过滤 | 认证前只放行 `/health`、`/login` 和生成画面 frame 的能力令牌（[handler.go:287](../internal/appapi/handler.go:287)）；`authz.Required` 对未声明路由默认拒绝（[authz/routes.go:304](../internal/authz/routes.go:304)）。无公开数据路由（附录 A S2） |
| 3 | **Service User / OAuth 2.0**：`client_credentials` 获取令牌，供 REST/WS/MQTT 程序化接入。[R10][R17] | MES/ERP、脚本、第三方看板等机器接入 | Keycloak 客户端 + token endpoint + 角色 | 本地账号 + Bearer 会话；无 OAuth/OIDC/JWT/API Token（附录 A S3） |
| 4 | **Realm 多租户**：每租户独立用户、资产、规则、UI 样式、locale；`master` 超管。[R17] | 一套实例服务多个客户组织 | Keycloak realm + 数据按 realm 分区 + 每 realm 配置 | 单项目多 Site，无租户层；用户只有锚点与角色（[handler.go:183](../internal/appapi/handler.go:183)）。不属于当前交付形态 |
| 5 | **企业身份**：AD/LDAP 联邦、2FA、找回密码、会话列表/注销。[R19][R34] | 企业账号治理 | Keycloak federation + SMTP | 管理员创建/改密、会话上限；无 SSO/2FA/自助找回（附录 A S3） |
| 6 | **功能区级角色**：`read:notifications`、`write:tunnels` 等组合角色。[R23][R20] | 按功能区而非固定角色授权 | 角色组合模型 | 14 个 action + 角色默认/白名单 + 目标范围（[authz/authz.go:6](../internal/authz/authz.go:6)、[routes.go:56](../internal/authz/routes.go:56)）。粒度与 OpenRemote 相当，且额外绑定对象范围 |

### 2.2 通知与事件

| # | 能力 | 解决的问题 | 架构前提 | FastIOT 现状 |
| --- | --- | --- | --- | --- |
| 7 | **邮件/推送通知 + 通知总览**：规则或 API 发送，按收件人逐条记录 Sent/Error；可发给用户、资产关联用户或全租户。[R23] | 告警到达不在屏幕前的人 | SMTP、FCM + 移动 console、用户联系方式、通知存储 | 告警有确认/关闭/维护窗口，但无收件人、无投递、无通知记录（[alarm.go:47](../internal/ems/alarm/alarm.go:47)、[alarmtype](../internal/ems/alarmtype/alarmtype.go:15)）；代码中 SMTP/Webhook 零命中（附录 A S4）。上游 Simple IoT 有 Notification/MsgService（SMTP/ntfy/Twilio，[上游通知文档](../third_party/simpleiot/docs/ref/notifications.md)），但 FastIOT 未注册（[app.go:198](../internal/app/app.go:198)） |
| 8 | **规则 Webhook 动作**：When-Then 的 Then 侧调用外部系统。[R12] | 对接 ERP/CRM/IM/工单 | 规则动作类型 + 出站 HTTP + 凭证管理 | 决策表动作只有写点/命令等本地动作；无出站 HTTP（附录 A S4） |
| 9 | **按资产关联用户路由通知**：收件人=资产链接的用户，而不是全租户广播。[R23] | 告警只找负责这台设备/这个区域的人 | 用户-资产链接 | 无资产级用户链接；只能用根锚点近似（[handler.go:1295](../internal/appapi/handler.go:1295)） |
| 10 | **事件订阅 API**：WS 以 `SUBSCRIBE:` 申请，服务端逐订阅鉴权并回 `SUBSCRIBED/UNAUTHORIZED`，事件以 `TRIGGERED` 推送；另有 MQTT topic API 与 last will。[R10] | 第三方实时集成、按需只看关心的点 | 事件总线 + 每订阅授权 + 对外 Broker | WS 每 2 秒推送整棵可见树，无订阅过滤（[websocket.go:21](../internal/appapi/websocket.go:21)）；前端实时数据仍以 `ems/state` 轮询为主（[useObservations.ts:1](../frontend/src/useObservations.ts:1)）；内嵌 NATS 只监听回环 |

### 2.3 规则与自动化

| # | 能力 | 解决的问题 | 架构前提 | FastIOT 现状 |
| --- | --- | --- | --- | --- |
| 11 | **When-Then + 调度器**：属性/时间/持续时长条件，AND/OR；日出日落带偏移；规则可按重复日程生效到指定日期。[R12][R34] | 低门槛事件规则 | 规则 UI + 调度器 + 太阳事件算法 | 决策表已有 HH:MM 时间窗与星期掩码（[timecond.go:15](../internal/decision/timecond.go:15)）；无日出日落/节假日/规则级生效日程（附录 A S5） |
| 12 | **Flow 可视化数据流**：属性转换、派生虚拟属性、属性联动。[R13] | 不写代码做数据加工 | Flow 节点模型 + 解释器 + 编辑器 | 计算点（受限声明式公式）+ 虚拟输入已覆盖同类需求，且可解释、可排错（规格：计算点、虚拟输入）。不照搬 |
| 13 | **Groovy 脚本**：任意逻辑与任意副作用。[R14] | 复杂算法 | JVM 脚本引擎 + 沙箱/审计 | 明确不支持自由脚本（产品规格 Out of Scope）。不吸收 |
| 14 | **Global vs Realm 规则**：Global 规则跨租户读属性。[R11][R34] | 多租户统一策略 | 租户层 | 无租户，不适用 |
| 15 | **预测数据点**：`Has predicted data points`、`Apply predicted data points`；WEA 预报；ML 服务（Prophet）作为可选单独部署。[R8][R15][R25] | 计划、趋势、预告 | 预测序列存储 + 定时任务 +（可选）ML 服务 | 无通用预测序列（附录 A S6）；EMS 只有光伏功率预测输入（[forecast_run.go:15](../internal/appapi/forecast_run.go:15)） |
| 16 | **地理围栏/位置规则**：location 属性、圆形/GeoJSON 围栏、进入/离开触发、移动端围栏 API。[R6][R16] | 移动资产/车队 | PostGIS + 地图 + 围栏适配器 + 移动端 | 无经纬度/地图/围栏（附录 A S7） |
| 17 | **属性链接与值过滤**：一条 Agent 消息经 regex/JSONPath/substring 过滤后写入多个属性；`%VALUE%`/`%TIME%` 动态注入。[R9] | 通用协议映射 | 通用 IO Agent 模型 | Profile 已覆盖 Modbus/OPC UA/MQTT 的声明式映射；无通用脚本式过滤。不吸收（见 §3） |

### 2.4 数据与历史

| # | 能力 | 解决的问题 | 架构前提 | FastIOT 现状 |
| --- | --- | --- | --- | --- |
| 18 | **Timescale 超表 + 压缩 + 系统级保留**：按周 chunk 删除，清理粒度决定“可能多留”；查询/周期/导出/预测点 API。[R32][R40] | 长期历史与容量可预期 | PostgreSQL + TimescaleDB + 压缩策略 | JetStream + 每点样本上限（1000–200000，[settings.go:41](../internal/appapi/settings.go:41)）；查询/聚合/导出已具备（[historyquery](../internal/ems/historyquery/query.go:1)）。保留口径不同，不照搬实现 |
| 19 | **异常检测**：Range/Change/Forecast 三种方法（**官方文档明确尚未实现**）。[R28] | 异常点标记 | 属性 meta + 检测算法 | 无；上游也未实现，不吸收 |
| 20 | **可观测性**：syslog 事件页、Metrics、Traces。[R30][R34] | 平台运维 | 日志/指标/追踪后端 | 有健康页、帧日志、审计查询/导出；无追踪。按需，不吸收 |

### 2.5 接入、边缘与量产

| # | 能力 | 解决的问题 | 架构前提 | FastIOT 现状 |
| --- | --- | --- | --- | --- |
| 21 | **协议大目录**：HTTP、MQTT、TCP、UDP、WS、SNMP、Modbus、KNX、Z-Wave、Velbus、LoRaWAN、BT Mesh、Email、天气、模拟器。[R9] | 接各种设备 | 每协议实现、测试与维护承诺 | Modbus（自建 bridge）、OPC UA、MQTT（SIOT client）已接入（[app.go:213](../internal/app/app.go:213)）。不追求目录宽度 |
| 22 | **协议发现/导入**：设备发现；Velbus `.vlp`、KNX `.etsproj` 导入。[R9] | 快速建点 | 协议专用解析器 | Modbus 扫描/识别、OPC UA 点列表、CSV 整批导入已有；无协议工程文件导入 |
| 23 | **边缘网关互联**：边缘 Manager 主动连中心，资产单向同步、限速、隧道；中心必须是 Keycloak。[R20] | 多级部署/集中运维 | 第二个 Manager + Keycloak + WS + 同步协议 | 单实例；SIOT 有 Sync 客户端但未注册（[app.go:198](../internal/app/app.go:198)）。不吸收 |
| 24 | **X.509 自动注册**：设备用证书挑战经 MQTT 注册 Service User 与资产模板。[R21] | 设备量产 | MQTT Broker + CA 校验 + 资产模板 | 无；当前不连接真实设备，不吸收 |
| 25 | **MQTT 客户端 API**：按主题订阅/写属性、last will、自定义 handler。[R10] | 第三方/设备实时接入 | 对外 Broker + 认证 + 主题权限 | 内嵌 NATS 只监听回环；浏览器 NATS 在产品模式关闭。不吸收 |

### 2.6 界面、交付与平台

| # | 能力 | 解决的问题 | 架构前提 | FastIOT 现状 |
| --- | --- | --- | --- | --- |
| 26 | **Insights 仪表盘**：9 类组件、按屏幕尺寸布局、私有/共享/公开、移动 console 外壳。[R34] | 终端展示与大屏 | 仪表盘存储 + 公开标志 + 移动端 | AI 生成画面 + 画面模板 + 客户工作台已覆盖主路径；无公开共享、无移动 App、无组件市场。只吸收“只读共享” |
| 27 | **地图页/地理编码/GeoJSON 图层**：实时位置、按属性着色、地址搜索。[R22][R34] | 空间视图 | location + 地图瓦片 + Nominatim | 无（附录 A S7）。不吸收 |
| 28 | **9 语言 + 每租户 locale**。[R34] | 多语言交付 | i18n 框架 + 翻译 | 中文硬编码，无 i18n 库（附录 A S8）。不吸收 |
| 29 | **告警分派/处理人/高严重级邮件/5 状态**：Open→Acknowledged→In progress→Resolved→Closed。[R34] | 告警责任闭环 | 用户目录 + 通知 | 4 状态（active/acknowledged/resolved/closed）+ 维护窗口 + 确认备注；无处理人、无分派、无邮件（[alarm.go:24](../internal/ems/alarm/alarm.go:24)） |
| 30 | **白标 Manager 配置/日志页**。[R3][R34] | 交付外观与排障 | 静态配置 + 日志后端 | 项目包/主题/健康/帧日志已有；日志页不吸收 |
| 31 | **Java Extension / Services**：build-time 扩展，Services 页嵌入外部应用。[R24][R33] | 领域扩展 | JVM 构建 + 独立容器 | 行业包（base/EMS）运行时启用/停用，边界更贴近商业分层（[industry.go:1](../internal/industry/industry.go:1)）；不吸收 |
| 32 | **MCP Server（Beta）**：AI 客户端以 Service User 调 REST。[R29] | AI 操作平台 | npm 包 + 服务账号 + REST | DeepSeek 组态生成已有；无 MCP。Beta 且新面，不吸收 |
| 33 | **事件驱动 context broker**：Camel + AttributeEvent + 拦截器链。[R2] | 统一事件管线 | Camel/事件总线 | NATS/JetStream + 决策表/观测管线，架构不同；只借鉴“事件先校验后分发”的顺序，不照搬 |

## 3. 适配判断

判断只对 FastIOT 当前约束负责：单 Go 服务与现有 App 生命周期、底座独立可交付、EMS 可选包、项目包是唯一可版本化资产、两工作台、离线、无自由脚本。

| # | 判断 | 理由（扣约束） |
| --- | --- | --- |
| 1 | **部分吸收** | 吸收“数据层对象/点位可见性分级”，服务两工作台与客户视图；**不做**属性级 read/write ACL——单现场交付里“谁能读哪个属性”的收益不足以抵消全链路校验成本。锚点子树已覆盖大部分场景，缺口是工程节点泄漏。 |
| 2 | **部分吸收** | 只做**只读共享画面**（令牌 + 点位白名单 + 禁写）；不做公开资产浏览、不做公开写。公开写会破坏“写值不等于设备到位”的控制真值边界。 |
| 3 | **部分吸收** | 做**默认只读的 API 令牌**（动作集由管理员选择，默认不含 command/write/configure；绑定锚点，可撤销、可审计）；不引入 OAuth/OIDC/Keycloak。令牌是单服务内的本地凭证，不增加容器。 |
| 4 | **不吸收** | Realm 多租户是 SaaS 形态；FastIOT 单项目多 Site 已满足交付。引入租户会重写所有存储与路由，违背“单服务、现有生命周期”。 |
| 5 | **不吸收（本轮）** | SSO/AD/2FA/找回密码都依赖 Keycloak 与 SMTP，且不是当前客户的第一阻塞项；企业客户出现明确需求再评估。 |
| 6 | **已覆盖** | FastIOT 的 action 词表 + 目标范围已覆盖等价粒度，且对象范围参与判定；不需要再引入其角色体系。 |
| 7 | **吸收** | 告警到人是客户工作台的硬需求；邮件可选、无推送。可在单服务内用出站 outbox 完成，离线时排队重试。 |
| 8 | **吸收** | Webhook（企业微信/钉钉/ERP）是国内工业交付的高频集成方式；成本低，只需一个受控出站动作与审计。 |
| 9 | **部分吸收** | 不做“资产链接用户”整套模型；按“告警对象的所在子树/角色/显式收件人”路由即可，复用现有锚点。 |
| 10 | **部分吸收** | 吸收“逐订阅授权 + 只推变化”；不做对外 MQTT Broker。先把 WS 从整树快照改成按身份过滤的增量，收益在带宽与规模，不动控制链路。 |
| 11 | **部分吸收** | 不吸收 When-Then 编辑器；只把“规则级生效窗口/日出日落”作为后续可选项。当前时间窗+星期已覆盖多数场景。 |
| 12 | **不吸收** | 计算点/虚拟输入已覆盖，且比 Flow 更可解释；Flow 的自由连线会削弱“首条命中、顺序执行”的验证优势。 |
| 13 | **不吸收** | 自由脚本与“交付人员不写项目代码、可验证”直接冲突；OpenRemote 文档自己也提示 Groovy 需要专业经验。[R14] |
| 14 | **不适用** | 无租户层。 |
| 15 | **部分吸收** | 只吸收“预测序列与观测序列分离、预测值不可冒充实测”的建模口径；WEA 可在本地实现，ML 服务（独立容器）不吸收。EMS 已有 PV 预测，通用化放到 EMS 包内。 |
| 16 | **不吸收** | 固定站房 SCADA 不需要围栏；移动资产/车队是另一个产品方向，且依赖地图与移动端。 |
| 17 | **不吸收** | 没有通用 IO Agent，就不需要通用 JSONPath/regex 过滤；Profile 的声明式映射更适合可验证交付。 |
| 18 | **部分吸收** | 吸收“保留策略必须系统级、清理粒度可能多留”的验收口径（旧调研已在 T32 写入）；不引入 Timescale/超表/压缩实现。FastIOT 的计数上限与时间保留是两种语义，需在文档中说清。 |
| 19 | **不吸收** | 上游自己标注未实现，不能作为吸收来源。 |
| 20 | **不吸收** | FastIOT 已有更贴合的健康/帧日志/审计；追踪不是当前阻塞。 |
| 21 | **不吸收** | 每个协议都是测试与维护承诺；FastIOT 的 Modbus/OPC UA/MQTT 已覆盖其样板，扩目录会把“未验证支持”带进产品。 |
| 22 | **部分吸收** | Modbus 扫描/CSV 导入已有；只借鉴“导入前展示变化与错误定位”的交互，不引入协议文件解析。 |
| 23 | **不吸收** | 多实例/隧道是另一个部署形态，且中心必须 Keycloak；与单服务、离线约束冲突，安全面也过大。 |
| 24 | **不吸收** | 依赖设备量产与证书体系；本轮不连接真实设备，产品也未承诺自有硬件。 |
| 25 | **不吸收** | 对外 Broker 会扩大攻击面；现有 REST/WS 足够，第三方接入用只读 API 令牌解决。 |
| 26 | **部分吸收** | 只吸收只读共享；不吸收组件化仪表盘。FastIOT 的 AI 组态 + 模板是差异点，不应退回手工拖拽。 |
| 27 | **不吸收** | 无地理模型；地图/地理编码依赖外部瓦片与地址服务，与离线交付冲突。 |
| 28 | **不吸收** | 当前市场与交付语言是中文；引入 i18n 是纯成本。 |
| 29 | **吸收** | 处理人与“我的告警”是低成本、高可用的责任闭环；与通知/升级天然配套。 |
| 30 | **不吸收** | 已有项目包主题与健康/帧日志；日志页不改变交付结果。 |
| 31 | **不吸收** | 行业包运行时启用比 build-time Java 扩展更贴合“底座/EMS 分层”；不需要再引入 Services 容器。 |
| 32 | **不吸收** | Beta、独立 npm 进程、以 Service User 写数据；与单服务/离线/最小攻击面不符。 |
| 33 | **不吸收** | 事件模型不同；只吸收“先校验、后更新、再分发”的顺序原则，代码不照搬。 |

## 4. 五项吸收方案（最小形态）

以下只给落地形状，不写完整实现；每项都不改控制链路、不新增容器、不引入外部云。

### 4.1 出站通知与集成投递层（对应 #7/#8/#9）

**放哪一层：** 新增 `internal/notify`（只做出站投递与投递记录）；触发点两处：`internal/ems/alarmwatch` 的 raise/resolve 转换，以及决策表动作执行器（`internal/decision/client.go`）。

**对现有模型的影响：**
- 告警对象不新增收件人字段；通知是派生产物。
- 站点级新增 `notification-policy`（管理员配置，存 `internal/datadir`，不进项目包）：渠道（webhook / SMTP）、最低严重级、收件范围（角色/用户/对象子树）、raise/resolve 开关、升级分钟数。
- 告警类型可选增加 `notify: bool`（在包内，可随项目复制）；URL、SMTP 口令等站点秘密一律在导入时剥离（复用 `reuseSecretTokens` 的现有清单，[reuse.go:67](../internal/appapi/reuse.go:67)）。

**需要新增的接口：**
- `GET/PUT /api/notification-settings`（管理员，`site_settings`）。
- `GET /api/notifications`（按身份范围过滤的投递记录，含 status/attempts/last_error）。
- 不新增“发送任意通知”的通用 API；只允许策略驱动的告警/规则触发，避免变成消息平台。

**复用现有 seam：**
- 上游 Simple IoT 已有 Notification/Message 点与 `msgService`（SMTP/ntfy/Twilio）投递层，但 FastIOT 未注册这些客户端（[app.go:198](../internal/app/app.go:198)）。先评估“只注册 MsgService 做投递、告警状态仍由 FastIOT 管理”的可行性；若上游 User 客户端会带来额外宿主副作用，则退回自建轻量 outbox。不要为了通知启用上游 Rule/User/Update 等整套客户端。
- 投递记录可复用 `settings.go` 的 datadir 持久化模式（[settings.go:41](../internal/appapi/settings.go:41)）。

**怎样验证：**
- 复用现有告警集成测试（`internal/app/alarm_integration_test.go`、`alarm_occurrences_integration_test.go`），加 httptest 假 webhook 与假 SMTP。
- 断言：未配置渠道时不外发且记录 `skipped`；失败按退避重试并记录 `error`；确认/关闭后停止升级；模拟/预览不产生任何出站；投递记录不包含口令与完整 URL 查询串。

### 4.2 点位/对象可见性分级（对应 #1）

**放哪一层：** 点位配置（`internal/ems/pointconfig`，Modbus CSV/API）与 OPC UA/MQTT 点配置增加 `visibility`；读取路径统一经现有 `visibleNodes` 与 `publicInputNodes`（[handler.go:741](../internal/appapi/handler.go:741)）。

**对现有模型的影响：**
- 一个枚举：`customer | engineering`。过程值默认 `customer`；通信配置、原始寄存器、工程参数默认 `engineering`，由交付人员显式调整。
- 客户角色（operator/viewer）只收到 `customer` 节点/点位；交付角色（implementer/admin）不受影响。
- 同一谓词必须同时用于：快照、历史查询/导出、告警列表、画面绑定校验。否则会出现“页面隐藏但 API 可读”。
- 节点级（Channel、Device、Template）也允许覆盖，避免只靠点位逐条设置。

**需要新增的接口：** 扩展点位配置读写与 CSV 列（中文表头沿用现有词表）；节点上增加一个可见性配置点；`/api/snapshot` 不改协议，只改过滤结果。

**复用现有 seam：** `identityForUser` 已把角色与锚点解析为 `Identity`（[handler.go:674](../internal/appapi/handler.go:674)），`allows` 是唯一子树判定点（[handler.go:1295](../internal/appapi/handler.go:1295)）；在此加可见性谓词即可，不改路由表。

**怎样验证：**
- 同一棵树下分别用 operator 与 implementer 取快照，断言客户响应中不含 `modbus`/`opcua`/`decisionTable` 等工程节点。
- 客户角色查询工程点位的历史必须返回 403；导出同样拒绝；告警列表不出现工程点位告警。
- 存量数据的默认分类按节点类型给出（过程点客户可见、配置类节点不可见），并提供一次“哪些客户将失去哪些节点”的预览，而不是声称行为完全不变。

### 4.3 告警指派与“我的告警”（对应 #29）

**放哪一层：** 告警引擎（`internal/ems/alarm`）+ 路由（`internal/appapi/alarmmaintenance.go`/alarms 路由）+ 前端 `AlarmsView.tsx`。

**对现有模型的影响：** 告警增加 `assignee`、`assigned_at`、`assigned_by`；指派/改派/取消指派是显式转换，写审计。运行态字段，不进项目包。

**需要新增的接口：** `POST /api/ems/alarms/*/assign`（复用 `alarm_ack` 动作或新增 `alarm_assign`，二选一但只能有一个词）；`GET /api/ems/alarms?assigned=me`。

**复用现有 seam：** 告警状态机与 `Maintenance` 已有明确的转换与审计；前端 `useAlarms` 已有订阅/刷新路径。验证复用告警集成测试，断言指派不改变设备状态、不改 `active`，且未授权角色不能指派。

### 4.4 只读共享画面（对应 #2/#26）

**放哪一层：** 复用生成画面的能力令牌模式（[genscreen.go:341](../internal/appapi/genscreen.go:341) 的 `ByCapability`）与画面存储；新增站点级共享记录。

**对现有模型的影响：** 共享记录 `{token, screen_id/version, node allowlist, expires_at, created_by, revoked}`，存 datadir；**只允许读**，绑定必须落在白名单内，未列入的绑定在保存/发布时拒绝。
- 共享端点只返回白名单内点位的当前值与画面定义，不返回资产树、配置、历史、审计、用户。
- 写操作、命令入口、工程页面在共享上下文中一律不存在（不是“按钮禁用”，而是路由不存在）。
- 令牌可撤销、可设过期；创建/撤销写审计。

**需要新增的接口：** `POST/DELETE /api/shares`（`configure`）、`GET /api/share/{token}/state` 与 `GET /api/share/{token}/frame`（未认证、令牌即能力）。共享读数端点要在 `ServeHTTP` 的认证前分支里显式加入，与 `/login`、生成画面 frame 同级；**不要**把共享令牌接进 `authz` 动作表，并用测试保证其他未声明路径仍然 fail-closed。

**复用现有 seam：** 生成画面 frame 的能力令牌与 CSP/Referrer-Policy 头已经存在；共享可复用其渲染与安全头，只把“能力范围”从生成会话换成共享白名单。

**怎样验证：** 新增 `ServeHTTP` 级测试，断言共享令牌不能调用任何写路由；快照只含白名单点位；过期/撤销后 404；画面不泄漏未列点位名称与工程节点。现有 `routes_action_test.go`、`routes_package_test.go` 继续保证其他路由 fail-closed。

### 4.5 API 令牌（默认只读）（对应 #3）

**放哪一层：** 用户/会话旁新增“机器凭证”，绑定到某个用户策略（动作集 + 锚点）；认证入口复用 `authenticate`（[handler.go:657](../internal/appapi/handler.go:657)）。

**对现有模型的影响：** 令牌记录 `{id, user_id, actions, anchors, created_by, expires_at, last_used, revoked}`；默认预设只含查看/历史/导出（导出仍受 `export` 动作约束），**不默认**授予 `command`/`write_value`/`configure`。令牌不进项目包，导入时剥离。
- 不引入 OAuth/OIDC；Bearer 令牌格式与现有会话兼容，但走独立校验分支。

**需要新增的接口：** `POST/GET/DELETE /api/tokens`（`account_manage` 或 `site_settings`）。只允许管理员为某个已存在用户创建；可选只读预设。

**复用现有 seam：** `authz.Resolve` 与 `allows` 已能把动作集与锚点变成有效权限；令牌只是另一种凭证来源。审计已有 API 路径记录（[audit.go:32](../internal/appapi/audit.go:32)），令牌使用应带 `actor=token:<id>`。

**怎样验证：** 断言令牌不能访问未授予的动作；撤销立即生效；过期拒绝；令牌不能创建用户/改配置；审计能看到令牌 ID 而非明文令牌。

### 4.6 部分吸收项的短方案

- **增量事件订阅（#10）：** 保留 2 秒整树快照作为兜底；WS 增加 `subscribe`（点位引用列表），服务端按 `Identity` 与可见性逐订阅授权，只推变化的观测（值/时间/质量）。不改模型，不改前端主路径，先用一个页面灰度。验证：两个不同锚点用户互不收到对方点位；未授权订阅返回拒绝而非静默。
- **预测序列（#15）：** 只在 EMS 包内增加“预测”类型序列，历史查询区分 `observation|prediction`，画面/趋势用虚线，永不参与告警与命令；WEA 用本地定时任务。验证：预测值不能写入观测流、不能触发规则、导出标注类型。
- **多锚点暴露（#1 的顺带）：** 用户模型已是 `RootNodeIDs []string`，创建接口只写单父级（[handler.go:1138](../internal/appapi/handler.go:1138)）；把接口与 UI 放开到多锚点即可，无存储迁移。验证：两个锚点的用户快照为并集，任一越权目标仍拒绝。
- **规则生效窗口/日出日落（#11）：** 若做，只作为决策表的附加时间条件，站点增加经纬度（本地算太阳事件，不联网）；不引入 When-Then。验证：与现有时间窗测试同一套匹配解释。

## 5. 排序、不照搬与 FastIOT 已更好

### 5.1 价值 ÷ 成本排序

| 顺序 | 吸收项 | 价值 | 成本 | 判断依据 |
| --- | --- | --- | --- | --- |
| 1 | 出站通知与集成投递层 | 高 | 中 | 直接补“告警到人/对外集成”；可选、可默认关闭；不动控制链路 |
| 2 | 点位/对象可见性分级 | 高 | 中 | 直接服务“客户视图不出现内部术语”；但需统一快照/历史/导出/告警四个读路径 |
| 3 | 告警指派与“我的告警” | 中 | 低 | 模型与路由都是加法；与通知/升级配套 |
| 4 | 只读共享画面 | 中 | 中 | 复用能力令牌与画面渲染；新增未认证路径需要严格白名单与测试 |
| 5 | API 令牌（默认只读） | 中 | 中低 | 复用动作集/锚点；风险在凭证治理，不在技术 |

**执行建议：** 先做 1 与 2（它们决定客户工作台是否成立），再做 3（便宜且立即改善责任闭环），最后做 4 与 5（都需要新的外部边界，单独评审）。

### 5.2 明确不建议照搬的三项

1. **Groovy / Flow 自由逻辑。** 它们把“可解释、可模拟、首条命中”的决策表优势换成了任意代码与任意连线；OpenRemote 文档自己提示 Groovy 需要专业经验、容易出错。[R14] FastIOT 的立身之本是“交付人员不写项目代码”，照搬等于拆掉验证与责任边界。
2. **Keycloak + PostgreSQL/TimescaleDB + Realm 的身份/租户栈。** 它解决的是多租户 SaaS 与企业账号治理，代价是至少三个容器、外部身份服务与迁移成本；与“单 Go 服务、离线、底座可独立交付”直接冲突。[R31][R17] 需要的是“数据层可见性 + 可撤销凭证”，不是整套 IdP。
3. **原生移动 Console + FCM 推送 + 地图/地理围栏。** 这是车队/园区产品的形态，依赖 Google/Apple 云、移动应用发布周期与地图瓦片；与工业站房的离线交付、国内网络环境都不匹配。[R7][R23][R6] 告警到达人可以用本地 webhook/邮件解决，不必引入移动端。

### 5.3 其余不吸收项（一句话理由）

协议大目录与通用 IO Agent（每个都是维护承诺，且削弱可验证性）；边缘网关/隧道（多实例 + Keycloak + 过大攻击面）；X.509 自动注册（设备量产，不在当前产品边界）；ML 预报服务（独立容器）；MCP Server（Beta、新外部面）；Java 类型模型 SPI（FastIOT 模板/项目包更适合无代码）；公开写（破坏控制真值）；Global 规则（无租户）；异常检测（上游未实现）；Timescale 实现（换数据库不成立）；i18n（当前市场中文）；白标日志页（已有替代）。

### 5.4 FastIOT 已经做得更好、不该照搬的部分

| 维度 | FastIOT 现状 | OpenRemote 对照 |
| --- | --- | --- |
| 控制真值 | 命令生命周期、反馈、未知/超时、人工接管、仲裁与安全；写入不冒充到位（规格 T31） | `AgentLink.updateOnWrite` 明确允许“写入即本地回填”，[R35][R36]；页面值变化不等于设备到位 |
| 逻辑可验证 | 决策表顺序/首条命中/多输出、状态机、模拟与回放、影响分析（`rules/*/test|scenarios|diff|deps`，[routes.go:137](../internal/authz/routes.go:137)） | Groovy/Flow 可表达任意逻辑，但难以逐步解释与验收 |
| 交付资产 | 项目包发布/版本/回滚/升级预览/导出/导入/复用（[project_package.go:79](../internal/appapi/project_package.go:79)、[routes.go:86](../internal/authz/routes.go:86)） | 文档只描述 clean install 的 provisioning JSON，无版本回滚与项目级差异 |
| 审计 | 审计查询、导出、轮换与按天保留（[audit.go](../internal/appapi/audit.go:1)、[routes.go:62](../internal/authz/routes.go:62)） | 所读文档未见可查询的用户操作审计页/API；源码中存在审计日志配置（`logging.properties`），属运行日志级别 |
| 两工作台 | 交付/客户视图与措辞分离，客户视图不出现内部术语（产品规格、GLOSSARY） | 单一 Manager UI，靠角色与 Insights 应用区分 |
| 离线与部署 | 单容器/单进程，内嵌 NATS/JetStream、前端与运行时 | 多容器 + Keycloak + 可选地图 + 外部 FCM/SMTP |
| 无代码模型 | 模板、实例覆盖、Profile、项目包内模型 | 类型模型需 Java；实例复制不等同模板发布 |

## 6. 许可与来源

### 6.1 许可结论

- **FastIOT：Apache License 2.0**（仓库 `LICENSE`）。
- **OpenRemote 核心：AGPL-3.0-or-later**（1.34.1 固定提交 `LICENSE.txt`，[R37]）。
- **不兼容方向：** AGPL 代码不能并入 Apache-2.0 项目而不改变整个组合作品的许可；AGPL 的“网络使用”条款会要求向通过网络交互的用户提供对应源码。**因此不得复制、逐行翻译、改写成 Go，或把其 Java/JS/配置/模板直接搬入 FastIOT。**
- **文档仓库：** `openremote/documentation` 根目录未见 `LICENSE` 文件（固定提交文件清单核对）；按默认版权处理。本文只引用事实、行为与链接，不复制大段文字、代码或图表。
- **依赖条款单列：** OpenRemote 的 `LICENSE.txt` 还收录 Timescale 等第三方条款；FastIOT 不引入 Timescale，故这些条款不随本报告适用。但若从 OpenRemote 复制 SQL/schema/查询，同样属于 AGPL 范围，禁止。
- **干净实现建议：** 把本报告当作行为规格交给实现者；不让实现者逐行阅读或翻译 Java 源码；保留“只依据公开文档/公开行为”的记录。

### 6.2 来源

**OpenRemote 官方文档（固定提交 `0af6d26`，2026-11-17）**

- [R1] [Introduction](https://github.com/openremote/documentation/blob/0af6d2640fe1c15d02fb0ad9e364d77536a5a2b4/docs/10-introduction.md)
- [R2] [Overall architecture](https://github.com/openremote/documentation/blob/0af6d2640fe1c15d02fb0ad9e364d77536a5a2b4/docs/architecture/10-overall-architecture.md)
- [R3] [Manager endpoints and file paths](https://github.com/openremote/documentation/blob/0af6d2640fe1c15d02fb0ad9e364d77536a5a2b4/docs/architecture/20-manager-endpoints-and-file-paths.md)
- [R4] [Security](https://github.com/openremote/documentation/blob/0af6d2640fe1c15d02fb0ad9e364d77536a5a2b4/docs/architecture/30-security.md)
- [R5] [Asset validation](https://github.com/openremote/documentation/blob/0af6d2640fe1c15d02fb0ad9e364d77536a5a2b4/docs/architecture/40-asset-validation.md)
- [R6] [Asset location tracking](https://github.com/openremote/documentation/blob/0af6d2640fe1c15d02fb0ad9e364d77536a5a2b4/docs/architecture/50-asset-location-tracking.md)
- [R7] [Apps and consoles](https://github.com/openremote/documentation/blob/0af6d2640fe1c15d02fb0ad9e364d77536a5a2b4/docs/architecture/60-apps-and-consoles.md)
- [R8] [Assets, Agents and Attributes](https://github.com/openremote/documentation/blob/0af6d2640fe1c15d02fb0ad9e364d77536a5a2b4/docs/user-guide/030-assets-agents-and-attributes.md)
- [R9] [Agents & protocols overview](https://github.com/openremote/documentation/blob/0af6d2640fe1c15d02fb0ad9e364d77536a5a2b4/docs/user-guide/040-agents-protocols/010-overview.md)
- [R10] [Manager APIs](https://github.com/openremote/documentation/blob/0af6d2640fe1c15d02fb0ad9e364d77536a5a2b4/docs/user-guide/050-manager-apis.md)
- [R11] [Create rules](https://github.com/openremote/documentation/blob/0af6d2640fe1c15d02fb0ad9e364d77536a5a2b4/docs/user-guide/060-rules-and-forecasting/10-create-rules.md)
- [R12] [When-Then rules](https://github.com/openremote/documentation/blob/0af6d2640fe1c15d02fb0ad9e364d77536a5a2b4/docs/user-guide/060-rules-and-forecasting/20-when-then-rules.md)
- [R13] [Flow rules](https://github.com/openremote/documentation/blob/0af6d2640fe1c15d02fb0ad9e364d77536a5a2b4/docs/user-guide/060-rules-and-forecasting/30-flow-rules.md)
- [R14] [Groovy rules](https://github.com/openremote/documentation/blob/0af6d2640fe1c15d02fb0ad9e364d77536a5a2b4/docs/user-guide/060-rules-and-forecasting/40-groovy-rules.md)
- [R15] [Forecasting](https://github.com/openremote/documentation/blob/0af6d2640fe1c15d02fb0ad9e364d77536a5a2b4/docs/user-guide/060-rules-and-forecasting/50-forecasting.md)
- [R16] [GeoJSON geofencing](https://github.com/openremote/documentation/blob/0af6d2640fe1c15d02fb0ad9e364d77536a5a2b4/docs/user-guide/060-rules-and-forecasting/60-geojson-geofencing.md)
- [R17] [Realms, users and roles](https://github.com/openremote/documentation/blob/0af6d2640fe1c15d02fb0ad9e364d77536a5a2b4/docs/user-guide/070-identity-and-security/10-realms-users-and-roles.md)
- [R18] [Asset security](https://github.com/openremote/documentation/blob/0af6d2640fe1c15d02fb0ad9e364d77536a5a2b4/docs/user-guide/070-identity-and-security/20-asset-security.md)
- [R19] [Linking to Active Directory](https://github.com/openremote/documentation/blob/0af6d2640fe1c15d02fb0ad9e364d77536a5a2b4/docs/user-guide/070-identity-and-security/30-linking-to-active-directory.md)
- [R20] [Edge gateway](https://github.com/openremote/documentation/blob/0af6d2640fe1c15d02fb0ad9e364d77536a5a2b4/docs/user-guide/080-gateways-and-devices/10-edge-gateway.md)
- [R21] [Auto provisioning](https://github.com/openremote/documentation/blob/0af6d2640fe1c15d02fb0ad9e364d77536a5a2b4/docs/user-guide/080-gateways-and-devices/20-auto-provisioning.md)
- [R22] [Geocoding](https://github.com/openremote/documentation/blob/0af6d2640fe1c15d02fb0ad9e364d77536a5a2b4/docs/user-guide/020-manager-ui/40-geocoding.md)
- [R23] [Notifications](https://github.com/openremote/documentation/blob/0af6d2640fe1c15d02fb0ad9e364d77536a5a2b4/docs/user-guide/020-manager-ui/50-notifications.md)
- [R24] [Services](https://github.com/openremote/documentation/blob/0af6d2640fe1c15d02fb0ad9e364d77536a5a2b4/docs/user-guide/100-services/10-services.md)
- [R25] [ML Forecasting Service](https://github.com/openremote/documentation/blob/0af6d2640fe1c15d02fb0ad9e364d77536a5a2b4/docs/user-guide/100-services/20-service-ml-forecast.md)
- [R26] [Energy Management](https://github.com/openremote/documentation/blob/0af6d2640fe1c15d02fb0ad9e364d77536a5a2b4/docs/user-guide/090-domains/10-create-your-energy-management-system.md)
- [R27] [Fleet Management](https://github.com/openremote/documentation/blob/0af6d2640fe1c15d02fb0ad9e364d77536a5a2b4/docs/user-guide/090-domains/20-fleet-management.md)
- [R28] [Anomaly Detection](https://github.com/openremote/documentation/blob/0af6d2640fe1c15d02fb0ad9e364d77536a5a2b4/docs/user-guide/110-anomaly-detection.md)
- [R29] [MCP Server](https://github.com/openremote/documentation/blob/0af6d2640fe1c15d02fb0ad9e364d77536a5a2b4/docs/user-guide/130-ai-mcp-server.md)
- [R30] [Metrics](https://github.com/openremote/documentation/blob/0af6d2640fe1c15d02fb0ad9e364d77536a5a2b4/docs/user-guide/120-observability/20-metrics.md)
- [R31] [Docker Compose profiles](https://github.com/openremote/documentation/blob/0af6d2640fe1c15d02fb0ad9e364d77536a5a2b4/docs/developer-guide/030-docker-compose-profiles.md)
- [R32] [System Administration](https://github.com/openremote/documentation/blob/0af6d2640fe1c15d02fb0ad9e364d77536a5a2b4/docs/developer-guide/100-system-administration.md)
- [R33] [Extensions](https://github.com/openremote/documentation/blob/0af6d2640fe1c15d02fb0ad9e364d77536a5a2b4/docs/developer-guide/080-extensions.md)
- [R34] [Manager UI](https://github.com/openremote/documentation/blob/0af6d2640fe1c15d02fb0ad9e364d77536a5a2b4/docs/user-guide/020-manager-ui/10-manager-ui.md)

**OpenRemote 源码、发布与 REST 文档（固定提交/版本）**

- [R35] [AgentLink.java](https://github.com/openremote/core/blob/9cab6cac24a30cc0a257f150bb46c1f24e0114d4/model/src/main/java/org/openremote/model/asset/agent/AgentLink.java)：`updateOnWrite` 的“不等待协议响应、立即更新属性”语义（95 行）。
- [R36] [AbstractProtocol.java](https://github.com/openremote/core/blob/9cab6cac24a30cc0a257f150bb46c1f24e0114d4/agent/src/main/java/org/openremote/agent/protocol/AbstractProtocol.java)：`processLinkedAttributeWrite` 写路径（146 行起）。
- [R37] [LICENSE.txt](https://github.com/openremote/core/blob/9cab6cac24a30cc0a257f150bb46c1f24e0114d4/LICENSE.txt)：AGPL-3.0-or-later。
- [R38] [Release 1.34.1](https://github.com/openremote/core/releases/tag/1.34.1)（2026-11-17）。
- [R39] documentation 仓库提交 [0af6d26](https://github.com/openremote/documentation/commit/0af6d2640fe1c15d02fb0ad9e364d77536a5a2b4)（2026-11-17）。
- [R40] REST API 页面：[datapoint period](https://github.com/openremote/documentation/blob/0af6d2640fe1c15d02fb0ad9e364d77536a5a2b4/docs/rest-api/get-datapoint-period.api.mdx)、[datapoints](https://github.com/openremote/documentation/blob/0af6d2640fe1c15d02fb0ad9e364d77536a5a2b4/docs/rest-api/get-datapoints.api.mdx)、[notifications](https://github.com/openremote/documentation/tree/0af6d2640fe1c15d02fb0ad9e364d77536a5a2b4/docs/rest-api)。

**FastIOT 本地**

- [F1] [authz/routes.go](../internal/authz/routes.go)、[authz/authz.go](../internal/authz/authz.go)：动作词表、路由声明、未声明即拒绝。
- [F2] [appapi/handler.go](../internal/appapi/handler.go)：认证入口、`identityForUser`、`visibleNodes`、`allows`。
- [F3] [appapi/websocket.go](../internal/appapi/websocket.go)：2 秒整树快照。
- [F4] [appapi/audit.go](../internal/appapi/audit.go)：审计记录与导出。
- [F5] [appapi/project_package.go](../internal/appapi/project_package.go)：项目包模型与导入结果。
- [F6] [ems/alarm/alarm.go](../internal/ems/alarm/alarm.go)：告警状态与维护窗口。
- [F7] [ems/alarmtype](../internal/ems/alarmtype/alarmtype.go)：告警类型配置。
- [F8] [ems/pointconfig](../internal/ems/pointconfig/pointconfig.go)：点位配置与 CSV 词表。
- [F9] [appapi/settings.go](../internal/appapi/settings.go)：历史/审计保留设置。
- [F10] [app/app.go](../internal/app/app.go)：已注册的客户端与协议。
- [F11] [decision](../internal/decision)：决策表、时间条件、目标校验。
- [F12] [genscreen](../internal/genscreen) 与 [appapi/genscreen.go](../internal/appapi/genscreen.go)：能力令牌与画面渲染。
- [F13] [industry/industry.go](../internal/industry/industry.go)：行业包启用模型。
- [F14] [产品规格](specs/2026-11-13-open-scada-paid-ems.md)、[通用物模型](specs/2026-11-13-generic-thing-model.md)、[GLOSSARY](../GLOSSARY.md)。

## 7. 未确认与后续验证

1. **值假设未验证：** 通知、共享画面、API 令牌的实际使用频率与付费意愿未被用户/现场验证；本文的价值判断来自产品约束与交付场景，不是实测。
2. **快照数据暴露是否为有意设计：** 代码显示客户角色可读到工程节点（§4.2），但未找到对应测试或规格说明；吸收前应先确认这是缺陷还是有意为之。
3. **上游 MsgService 复用可行性：** 未注册、未运行；是否启用上游 User 客户端、是否引入额外宿主副作用，需要在实现前单独评审。
4. **文档口径冲突：** 历史保留“默认无限期”与 Manager UI“默认一个月”并存；本文不裁定，只引用系统管理页的精确描述。
5. **版本对齐：** 文档提交与 1.34.1 发布同日（相差约 20 分钟），未逐页核对是否为 1.34.1 的精确快照；结论不依赖版本差异。
6. **未覆盖的 OpenRemote 面：** 自定义部署/AWS/K8s/Balena、OTA/hawkbit、ChirpStack/LoRaWAN 等协议页、完整 REST Swagger、分布式/HA 行为；这些不影响本轮吸收判断。

## 附录 A：FastIOT 检索记录

在 `C:\Users\chent\Desktop\2026成果\AAAAA开源\easyems\.claude\worktrees\openremote-absorption-8a9711` 执行；排除 `_test.go` 时另行标注。

| 编号 | 检索 | 结果 |
| --- | --- | --- |
| S1 | `grep -rn "visibleNodes\|publicInputNodes" internal/appapi` | `visibleNodes` 白名单包含 `modbus`、`modbusIO`、`opcua`、`opcuaPoint`、`decisionTable`、`deviceTemplate`、`mqtt`，无角色过滤；`publicInputNodes` 只清空 virtual/calculation 的值 |
| S1b | `grep -rn "assignee\|assigned" --include=*.go internal` | 无告警处理人字段；仅一条无关注释 |
| S2 | `grep -rn "public\|anonymous" internal/authz internal/appapi/handler.go` | 认证前仅 `health`、`login`、`generated-screens/*/frame`；无公开数据路由 |
| S3 | `grep -rli "oidc" / "oauth" / "jwt"`（`internal service frontend/src`） | 0 命中；`openid` 仅为前端局部状态变量名 |
| S4 | `grep -rni "smtp\|webhook\|sendmail\|wecom\|dingtalk"`（`internal service cmd frontend/src deploy scripts`） | 0 命中 |
| S5 | `grep -rni "sunrise\|sunset\|solar" --include=*.go internal` | 0 命中；`internal/decision/timecond.go` 仅 HH:MM + 星期掩码 |
| S6 | `grep -rni "predicted\|prediction" --include=*.go internal service` | 0 命中；EMS 只有 `pvForecastKW` 预测输入（`internal/appapi/forecast_run.go`） |
| S7 | `grep -rni "latitude\|longitude\|geofence\|geojson\|mapbox\|leaflet"`（`internal frontend/src`） | 0 命中 |
| S8 | `grep -rli "i18next\|react-intl"`；`grep "toLocaleString"` | 0 命中；前端使用 `toLocaleString("zh-CN")` 与中文硬编码 |
| S9 | `grep -rn "clients.Add" internal/app/app.go` | 仅决策表客户端与 MQTT 客户端；Modbus 走自建 bridge，OPC UA 独立 manager；无 MsgService/User/SMTP |
| S10 | `grep -rn "func (h \*Handler) allows"` | 子树锚点判定；无属性/点位级 ACL |
| S11 | `grep -rn "RootNodeIDs\|UserAnchors"` | 权限模型支持多锚点；用户创建接口只写单父级 |
| S12 | `internal/appapi/websocket.go` | 每 2 秒推送整树，无订阅过滤；`CheckOrigin` 恒真 |
| S13 | `grep -rn "audit" internal/authz/routes.go internal/appapi/audit.go` | 审计查询/导出/保留设置存在 |
| S14 | `grep -rn "reuseSecretTokens"` | 项目包复用时的秘密剥离清单存在 |
| S15 | `grep -rli "sso"` | 命中均为测试/无关字符串，无 SSO 实现 |

> 说明：`grep -rni "map"` 在 Go 代码中命中数百处，均为 `map[...]` 类型，不构成地图能力证据；地图判断以 S7 为准。
