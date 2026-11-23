# FastIOT uniapp PC 集成验收记录（2026-11-23）

## 本批范围

T1–T5 已集成：真实登录与会话、总览/历史、设计六步与接设备、决策表策略、交付工作台。技术栈为 uniapp + Vue 3 + TypeScript，复用现有 Go 后端。未提交、未推送。

## 已执行验证

### 类型、逻辑与构建

- `npm run type-check`：通过，`vue-tsc --noEmit` 无错误。
- Node 逻辑测试：**29/29 通过**，覆盖接入、策略、交付、总览与历史。
- 接设备独立 Playwright 合同测试：通过；覆盖 6 个写请求、六步切换、Profile 预览/应用、点位映射、通道与点位、只读连通测试和验证记录；1024×768 无页面横向溢出。
- `npm run build:h5`：通过，产物位于 `internal/web/dist/uniapp`；React 的 `internal/web/dist/index.html` 保留。
- `third_party/simpleiot/api` Go 测试：通过。
- `fastiot/internal/app`、`fastiot/internal/appapi` Go 测试：通过。

### 生产托管

- Go 根路径 `/` 返回 React 入口。
- `/uniapp/` 返回 uniapp 入口；`/uniapp/index.html` 按 HTTP FileServer 规则重定向到 `/uniapp/`。
- 修复了 Go SPA 处理器不支持子目录 index、子页面内联脚本 CSP 哈希缺失的问题；两项均有先失败后通过的回归测试。
- 移除了 uni-h5 默认的外部阴影图片预加载，生产控制台不再出现外部 CDN/CSP 错误。

### 真实 Go 后端 + 生产浏览器

在 `http://localhost:4409/uniapp/` 验证：

- 真实登录成功，进入 `#/pages/overview/index`。
- 总览读取真实 snapshot/state/quality/alarms；无有效观测显示 `—`，今日执行等无 API 项目明确标缺口。
- 设计六步可切换；接设备读取真实通道、设备台账和 Profile；策略显示真实 decisionTable 节点。
- 交付读取真实 10 道预检门槛、中文状态、版本/Site 边界和审计相关信息。
- 历史页按真实点位打开；当前无数据时显示空态，不补值。
- 1280×800 与 1024×768 无页面横向溢出；1024 宽度下当前可见交互控件未发现高度小于 44px 的项。
- 上述生产页面检查无浏览器控制台错误。

## 未覆盖 / 不作为通过项

- 当前实例没有已绑定设备与有效实时观测，因此本批没有证明真机数据、控制回读或告警处理全流程。
- 当前实例没有真实 Site；冻结、项目包导出、导入、回退等真实 Site 写操作未执行。相关请求合同由独立浏览器测试覆盖，但不替代真实交付验收。
- 未修改真实账号权限或启停行业包；这些路径由合同测试覆盖。
- 通用 Site/Area/Unit/Device 建模、通用状态机、块目录/绑定/槽位/继承、独立部署记录、L1/L2/L3 分级执行与 L3 写回读、今日执行查询仍是后端缺口。
- 小程序版不在本批构建与验收范围。

结论：**uniapp PC 本批集成与生产托管通过；真机、真实 Site 写操作及上述后端能力仍需后续专项验收。**
