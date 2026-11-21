# 竞品界面参照（模块 → 产品 → 界面）

- 2026-11-20｜只抄信息结构（字段、分组、状态词、操作顺序），不抄皮肤、文案、图标、代码；AGPL/LGPL 只看文档与截图。

## 总表

| 模块 | 参照界面 | 可抄 | 不可抄 |
|---|---|---|---|
| 协议采集 | Neuron 设备卡/组/Tag | 状态分层、组周期、点五字段、Excel 往返 | 北向/订阅、插件许可 |
| EMS | OpenEMS LIVE/HISTORY/Channels | 环形能流+卡片、日历史+合计+导出、通道诊断 | Factory/Nature、市场指标 |
| 物模型 | Ignition UDT；TB Profile；OR 资产；HA 设备 | 定义/实例/参数/覆盖/嵌套、模板继承、资产树+描述符 | Tag Provider、TB 队列、HA 分类 |
| 策略 | Camunda DMN；OR when-then；Ignition SFC | 输入/输出/规则/命中策略、When/Then、步骤/转移/并行/监视 | FEEL、自由连线、SFC 脚本 |
| HMI | FUXA；Perspective | 视图列表+属性分组、主视图+停靠视图 | 拖拽编辑器、动画素材 |
| 历史报警 | Grafana/OpenEMS；TB/Ignition | 时间+查询+图/表+导出、报警列/筛选/指派/行样式 | 查询语言、通知渠道、多租户 |
| 项目包 | Ignition zip/gwbk；Node-RED；HA | 两级制品、内容清单、导入冲突 | 整体覆盖、密钥入包 |
| 行业包 | Ignition Modules+横幅 | 包列表+状态+未启用说明+横幅 | 密钥激活、试用计时、计费 |
| 权限审计 | OR 角色；TB 审计 | 功能×读写矩阵、受限可见性、对象审计页签 | Keycloak realm/AD |

**一句话结论**：可直接照搬结构的是 Neuron 采集、OpenEMS 能源总览、Ignition UDT/SFC、ThingsBoard 报警、Ignition 模块页；只能借一半的是 FUXA（不抄编辑器）、Camunda DMN（不抄 FEEL）、OpenRemote when-then（只做简单规则）；能力契约、Unit 角色绑定、验收预检、新建项目没有现成界面。

## 逐模块

### 1 协议采集 → Neuron
状态分两层（节点生命周期/连接）；卡上放配置、统计、DEBUG、删除；组=Name+Interval；Tag=Name/Attribute/Type/Address/Decimal/Precision/Description；支持 Excel 往返。**Neuron 是 LGPL-3.0，不是 Apache-2.0。**
[设备/组](https://docs.emqx.com/en/emqx-neuron/latest/configuration/south-devices/south-devices.html)｜[导入导出](https://docs.emqx.com/en/emqx-neuron/latest/configuration/import-export/import-export.html)｜[监控](https://docs.emqx.com/en/emqx-neuron/latest/admin/monitoring.html)｜截图 [设备卡](https://docs.emqx.com/assets/southdevice_card.Bn9XSYl3.png)、[Tag](https://docs.emqx.com/assets/tags-add.BiJTRk94.png)｜[LICENSE](https://github.com/emqx/neuron/blob/master/LICENSE)

### 2 EMS → OpenEMS
LIVE=环形能流+Autarchy/Self Consumption/Storage/Grid/Production/Consumption/EVCS 卡；HISTORY=日期+多曲线+kWh 合计+导出；Channels 看组件通道值。抄固定总览与日历史，不抄 Factory/Nature。
[LIVE](https://openems.github.io/openems.io/openems/latest/_images/ui-live.png)｜[HISTORY](https://openems.github.io/openems.io/openems/latest/_images/ui-history.png)｜[EdgeConfig](https://openems.github.io/openems.io/openems/latest/edge/configuration.html)｜[Channels 源码](https://github.com/OpenEMS/openems/tree/main/ui/src/app/edge/settings/channels)

### 3 物模型 → Ignition UDT 为主
Definition 不采集、改动作用于所有 Instance；Instance 只覆盖参数/属性；编辑器左树右属性分组，Nesting 做 Unit。TB 把传输/规则链/告警/计算字段挂 Profile；OR 资产树+属性描述符。抄定义/实例/参数/嵌套与模板继承。
[UDT](https://www.docs.inductiveautomation.com/docs/8.3/platform/tags/user-defined-types-udts)｜[嵌套](https://www.docs.inductiveautomation.com/docs/8.3/platform/tags/user-defined-types-udts/udt-nesting)｜[TB Profile](https://thingsboard.io/docs/user-guide/device-profiles/)｜[OR 资产](https://docs.openremote.io/docs/user-guide/assets-agents-and-attributes)｜[HA 设备](https://developers.home-assistant.io/docs/device_registry_index/)｜[UDT 截图](https://www.docs.inductiveautomation.com/assets/images/img3-5a836367433f17f4b5d91de7f52e6356.png)

### 4 策略 → 决策表与状态机分开
DMN：Inputs→Outputs，每行 Rule，Hit Policy；“首命中”写 First hit。最接近状态机的是 Ignition SFC（顺序控制）：步骤/转移/并行，运行时 Monitor/Step/Continue；结构可借，语义要改成设备状态/事件/守卫。when-then 可借简洁条件动作；BPMN 是业务流程，不是设备状态，不采用。不抄 FEEL、Groovy、自由连线。
[DMN](https://docs.camunda.io/docs/components/modeler/dmn/decision-table/)｜[when-then](https://docs.openremote.io/docs/user-guide/rules-and-forecasting/when-then-rules)｜[SFC](https://www.docs.inductiveautomation.com/docs/8.3/ignition-modules/sequential-function-charts/SFC-basics)｜[BPMN](https://docs.camunda.io/docs/components/modeler/bpmn/)｜截图 [DMN](https://docs.camunda.io/assets/images/dish-table-8fddefbddf97d60eb60d5ff7fbacebe8.png)、[SFC](https://www.docs.inductiveautomation.com/assets/images/5-SFC-in-designer-6c58dd49ac0a783cd99de3d7d95a785b.png)

### 5 HMI → FUXA，Perspective 补结构
FUXA 左视图列表、中画布、右属性分组；Perspective 页面=主视图+停靠视图+App Bar。抄视图列表、属性分组、页面/停靠结构，不抄拖拽编辑器与动画素材。ThingsBoard 仪表板是租户自建 dashboard、Grafana 只适合历史图表，都不作现场画面参照。
[FUXA 截图](https://raw.githubusercontent.com/frangoteam/FUXA/master/screenshot/fuxa-editor.png)｜[FUXA 报警](https://frangoteam.github.io/FUXA/HowTo-setup-Alarms/)｜[Perspective Pages](https://www.docs.inductiveautomation.com/docs/2027/ignition-modules/perspective/pages-in-perspective)｜[TB 仪表板](https://thingsboard.io/docs/user-guide/dashboards/)｜[Grafana 仪表板](https://grafana.com/docs/grafana/latest/dashboards/)

### 6 历史与报警 → Grafana/OpenEMS + TB/Ignition
历史=Explore 时间范围+查询+图/表+导出，加 OpenEMS 日导航/图例合计。报警=TB 列 Created/Originator/Type/Severity/Assignee/Status，支持筛选/指派/评论；Ignition 补事件/优先级/来源/时间过滤。
[Explore](https://grafana.com/docs/grafana/latest/explore/)｜[TB 报警](https://thingsboard.io/docs/user-guide/alarms/)｜[Ignition 日志](https://docs.inductiveautomation.com/docs/8.3/platform/alarming/alarm-journal)｜[TB 截图](https://thingsboard.io/_astro/find-alarms-1-ce.CaecW2QE_Z229FVe.webp)

### 7 项目包 → Ignition 两级制品
项目 `.zip` 只含工程、不含网关配置；网关 `.gwbk` 全量、恢复整体覆盖。Node-RED 单 flows.json；HA 全量/部分。抄两级制品、内容清单、导入冲突（合并/覆盖）。
[项目导出](https://www.docs.inductiveautomation.com/docs/8.3/platform/projects/project-export-and-import)｜[网关备份](https://docs.inductiveautomation.com/docs/2027/platform/gateway/gateway-backup-and-restore)｜[Node-RED](https://nodered.org/docs/user-guide/editor/workspace/import-export)｜[HA 备份](https://www.home-assistant.io/integrations/backup/)

### 8 行业包 → Ignition 模块页
Modules 页状态 Trial/Activated/Free，未授权时顶部横幅+View Modules 说明。抄“列包+状态+说明+横幅”，不抄密钥激活、试用计时、计费。TB PE 为独立订阅，未见同类状态页。
[Ignition 许可](https://www.docs.inductiveautomation.com/docs/8.3/platform/licensing-and-activation)｜[横幅截图](https://www.docs.inductiveautomation.com/assets/images/perspective-trial-period-dbc863336e054c0f8a58803b7a3cbf42.png)｜[TB PE](https://thingsboard.io/products/thingsboard-pe/)

### 9 权限与审计 → OpenRemote + ThingsBoard
OR 用“功能×读/写”组合角色，Restricted user 只看关联资产及 restricted 属性；TB 在对象详情有审计页签，全局审计按用户/实体/动作筛选。抄矩阵、受限可见性、审计页签。
[OR 角色](https://docs.openremote.io/docs/user-guide/identity-and-security/realms-users-and-roles)｜[TB 审计](https://thingsboard.io/docs/user-guide/audit-log/)

## 没有可参照界面的模块

- **能力契约**（“为什么这个操作不可用”）：未找到可核验界面，只能自研“禁用动作+原因”。
- **Unit 成员绑定**：只有 UDT 嵌套/资产树可借形状，没有现成界面。
- **通用设备状态机**：没有完全等价界面；SFC 只提供顺序控制外壳，状态/事件/守卫语义需自研。
- **交付验收/预检**：未找到等价界面（Ignition 只有导入冲突）。
- **新建项目/现场**：本轮未核验到可直接抄的页面。
- **EMS 包“未启用”**：OpenEMS 无包/许可界面，用 Ignition 模块页代替。

## 许可证风险

| 产品 | 许可 | 边界 |
|---|---|---|
| Neuron | LGPL-3.0 | 可看文档/截图；改代码分发有义务，不拷源码 |
| OpenEMS / OpenRemote / Grafana | AGPL-3.0 | 只看交互与结构；拷代码/素材触发 AGPL |
| ThingsBoard | BSL 1.1（4.4.0+） | 生产使用有限制；不拷代码 |
| Ignition | 专有 | 只学信息结构，不拷界面/素材/代码 |
| FUXA | MIT | 可复用但保留版权声明；不抄皮肤 |
| HA / Node-RED / Keycloak | Apache-2.0 | 可复用并保留 NOTICE；不拷品牌素材 |
| Camunda Modeler | MIT | 可复用；Camunda 8 平台许可另计 |

许可依据：[Neuron](https://github.com/emqx/neuron/blob/master/LICENSE)、[OpenEMS](https://github.com/OpenEMS/openems/blob/main/LICENSE-AGPL-3.0)、[OpenRemote](https://github.com/openremote/openremote/blob/master/LICENSE.txt)、[ThingsBoard](https://github.com/thingsboard/thingsboard/blob/master/LICENSE)、[Ignition](https://www.docs.inductiveautomation.com/docs/8.3/platform/licensing-and-activation)、[FUXA](https://github.com/frangoteam/FUXA/blob/master/LICENSE)、[Grafana](https://github.com/grafana/grafana/blob/main/LICENSE)、[HA](https://github.com/home-assistant/core/blob/dev/LICENSE.md)、[Node-RED](https://github.com/node-red/node-red/blob/master/LICENSE)、[Camunda Modeler](https://github.com/camunda/camunda-modeler/blob/main/LICENSE)、[Keycloak](https://github.com/keycloak/keycloak/blob/main/LICENSE.txt)。

边界：信息结构不受版权保护；截图、图标、CSS、组件代码、文案受保护。
