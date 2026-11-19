# 生成画面的隔离预览

日期：2026-11-20。对应票 #4（父规格 #1 的 US 11、14、18–19）。

## 流程

1. 「AI 生成」拿到模型返回的网页代码。出站清单里的设备模型只多一个不透明点位 id（绑定用），不含地址、实时值、历史或凭据。
2. 「打开隔离预览」把代码交给 `POST /api/generated-screens/preview`。服务端重新提取 HTML、解析绑定并返回检查结果与一个预览会话（`id` + 能力串）。
3. 运行版页面把 `frame_url` 放进 `sandbox="allow-scripts"` 的 iframe。该 iframe 没有 `allow-same-origin`，文档来源为不透明来源。
4. 运行版页面通过 `postMessage` 提供模拟值、转发写请求；iframe 只能通过 `data-fastiot-*` 声明绑定。
5. 浏览器检查（运行错误、布局）汇总回 `POST /api/generated-screens/{id}/diagnostics`。
6. 只有静态检查与浏览器检查都没有阻断项、且浏览器检查确实跑过时，`POST .../confirm` 才把源码保存为节点上的工程草稿。发布到运行版属于后续票。

## 隔离边界

- iframe 只授予 `allow-scripts`：不能读取宿主 DOM、`sessionStorage`、`localStorage` 或 `fastems-token`；不能弹窗、提交表单、跳转顶层或移除 sandbox。
- 响应头 CSP（不是 meta）由服务端固定：`default-src 'none'`、`script-src 'nonce-<每次响应随机>'`、`connect-src 'none'`、`object-src/frame-src/form-action/base-uri 'none'`、`frame-ancestors 'self'`。生成代码不能放宽它。
- 预览路由由一次性能力串认证，iframe 永远拿不到平台 Bearer token；能力串随预览会话过期失效。
- 桥接消息逐条校验：必须来自当前 iframe 窗口、必须带本次加载的 token（重载后旧 token 失效）、只接受 `read/write/diagnostic/locate` 四种形状。
- 静态检查会阻断 `eval`、`new Function`、`fetch`、`XMLHttpRequest`、`WebSocket`、`document.cookie`、浏览器存储、`window.parent/top`、`window.open`、外部资源、内联事件属性与 meta refresh，并给出元素标记与行列。

## 模拟设备

预览会话自带一个模拟设备：读数由点位 ID 与时间确定，写操作只改变这份内存状态并记入写入日志。`/generated-screens/{id}/write` 没有任何通往节点写入、命令运行时或设备通道的代码路径，所以"预览不产生真实设备写入"由结构保证，而不是靠一个可以忘记检查的开关。

## 检查项与定位

| 类别 | 检查 | 定位 |
| --- | --- | --- |
| 绑定 | 点位不存在 / 无权访问 / 不在本次点位范围 / 绑定为空 | 元素标记 + 选择器 + 行列 |
| 写操作 | 只读点位、缺失或非数值写值、写在非按钮元素上 | 同上 |
| 安全 | 禁止的 API、外部资源、内联事件、meta refresh、绑定过多 | 脚本或元素的行列 |
| 运行错误 | iframe 内 `error` / `unhandledrejection` | 行列；服务端按行回填最近元素标记 |
| 布局 | 页面横向溢出、绑定元素不可见 / 内容裁切 / 移出可视区 | 元素标记 + 选择器 + 尺寸 |

## 证据边界

- 服务端检查、模拟写入不落设备、确认门槛由 `internal/genscreen` 与 `internal/appapi/genscreen_integration_test.go` 覆盖。
- 桥接脚本（绑定、格式化、写请求、错误捕获、布局检查、定位）由 `frontend/preview-runtime-check.mjs` 在最小 DOM 中执行验证；`npm run check:preview-runtime`。
- 另在真实 fastiot 实例（`cmd/pumphouse`）上用 stub 模型走完"生成 → 预览 → frame → 模拟读/写 → 诊断 → 确认"，并检查了响应头、元素标记与写入日志；未落真实节点值。
- 运行错误与布局检查在浏览器内执行；服务端只校验其上报格式并合并结果。修改过的客户端可以不报这些结果——服务端静态检查与写路径仍然是强制的。
- 本轮没有在浏览器里操作 React 预览面板（工作区 5180 端口被其它会话的 dev server 占用，`launch.json` 按红线未改动），面板通过 `tsc`、构建与检查脚本验证。
