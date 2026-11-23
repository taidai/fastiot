# FastIOT uniapp PC 实施 tickets（2026-11-22）

## 基线

- 产品形态以 `frontend/prototype/overview-hd/` 最新玻璃磨砂原型为准。
- 技术栈为 uniapp + Vue 3 + TypeScript；先 PC（桌面与 10.1 寸触摸屏），小程序后续复用。
- 复用现有 Go 后端与同源 `/api`；不替换 React，不删除原型，不提交或推送，除非用户另行明确要求。
- 不用演示值冒充真实数据；无值显示 `—`，查询失败与无数据分开；后端没有的能力明确标为缺口。
- 共享工作目录。各会话只改 ticket 指定文件，不提交、不切分支、不安装新依赖；共享 API/store/types 如需修改，交给主会话统一处理。
- 开发服务：后端 `4409`，uniapp `5174`，入口 `/uniapp/`。账号 `admin / admin1234567` 仅用于本地开发实例。

## T1 基座（主会话，已完成基线）

真实登录、三工作台导航、同源 API、token 会话、snapshot/state/quality/alarms/history 轮询与类型基线。

验收：登录按钮真实发出 `/api/login` 并进入总览；`npm run type-check` 通过；无 401 重定向循环。

说明：2026-11-22 网络记录确认登录按钮可用，早期“按钮未发请求”是过早读取页面造成的误判，不做无根因修复。

## T2 总览与历史（并行）

文件归属：
- `frontend-uniapp/src/pages/overview/**`
- `frontend-uniapp/src/pages/history/**`
- `frontend-uniapp/src/components/{FlowScene,HistoryTable,AlarmCenter,LedgerTree}.vue`
- 可新增总览/历史专用组件与测试，不改其他 ticket 文件。

按原型补齐分层 SCADA、浮动台账、实时指标、设备清单、运行摘要、今日执行、告警中心、最近数据与整屏历史。只显示真实后端返回；没有“今日执行”API 时标缺口，不造数字。指标显示业务名称和单位，不能直接暴露裸 point id 作为主要标签。

验收：真实登录后切层、历史查询、告警入口可用；1280×800 无横向溢出；控制台无新增错误。

## T3 设计六步与接设备（并行）

文件归属：
- `frontend-uniapp/src/pages/design/**`
- `frontend-uniapp/src/components/design/AccessStep.vue` 及其新增专用组件/测试。

按原型铺六步，重点实现已有 API 支持的通道、点位、Profile、设备台账与验证。不得把通用 Site/Area/Unit/Device 新建、通用状态机或块编辑器伪装成可用。保持 T4 可插入独立策略组件，不改共享 API/store/types。

验收：六步可切换；接设备展示真实通道/Profile/验证状态；已有写操作真实调用后端并显示成功或失败；1024×768 可用。

## T4 策略组件（并行）

文件归属：
- `frontend-uniapp/src/components/design/StrategyStep.vue`
- 其新增专用组件与测试。
- 不编辑 `pages/design/index.vue`，由主会话在 T3/T4 完成后统一接入。

按原型实现通用决策表展示、草稿编辑、模拟、发布与启停，复用现有后端合同；有序首命中、条件、持续/冷却、动作只表达后端真实支持范围。通用状态机、动作窗口/序列不伪造。组件输入使用现有 snapshot/rules 数据，内部通过已有 `request` 调 API；需要新增共享 API 时在报告中说明。

验收：组件可用真实规则渲染；无规则为空态；模拟和发布结果来自后端；类型检查通过。

## T5 交付（并行）

文件归属：
- `frontend-uniapp/src/pages/delivery/**`
- `frontend-uniapp/src/components/delivery/**`（可新增）
- 交付专用测试，不改其他 ticket 文件。

按原型实现预检及门槛明细、冻结版本、项目包导入导出、回退限制、证据 ZIP、账号权限、审计与行业包。根项目与 site 参数按后端实际合同处理；冻结/导入不等于部署，回退只回工程配置。

验收：真实预检显示中文状态与门槛；失败原因可见；导出内容来自后端；无独立部署 API 时明确标缺口。

## T6 集成与验收（最后，主会话）

- 接入 T4 策略组件，处理各会话报告的共享合同调整。
- 全部页面类型检查、H5 构建、真实后端浏览器冒烟。
- 1440×900、1280×800、1024×768 与触摸目标检查；375×812 仅做基础不溢出检查。
- 验证 `/uniapp/` 生产托管与 React `/` 共存；小程序不在本批构建范围。
- 输出真实证据与未完成缺口，不把原型测试当成整体验收通过。

## 后端另列，不在本批假实现

通用 Site/Area/Unit/Device CRUD、通用状态机、块目录/声明式绑定/槽位/继承、独立部署记录、MQTT 凭据/ACL/下行命令及其余验收缺口。
