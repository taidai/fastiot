# 06: 原型 vs 实现视觉差异测量

**是什么**：一条命令把已批准的原型 HTML 和正在运行的实现页面放在同一个
1440×900 / deviceScaleFactor 1 视口里截图，用 `pixelmatch` 逐像素比对，产出
热图、并排图和可复核的数值。它回答"现在离原型还有多远、远在哪几栏"。

**不是什么**：它只测视觉。元素是否存在、文案、计数、交互行为都不归它管，
那些仍由 `scripts/prototype-parity.sh`（语义守卫）与既有测试负责。两者互补，
不能互相替代。

## 前置

- `frontend-uniapp` 依赖已安装（`pixelmatch`、`pngjs`、`jimp`）；`playwright-core`
  在 `frontend/node_modules` 或 `frontend-uniapp/node_modules` 均可被找到。
- 一个已种子化、正在运行的后端。默认目标
  `http://127.0.0.1:18505/uniapp/#/pages/overview/index`。
- 系统安装 Chrome 或 Edge（`playwright-core` 用 `channel` 启动，不另装浏览器）。

后端没在跑时，先用既有脚本起一个（本工具不修改它）：

```bash
bash scripts/prototype-parity.sh 18505
```

## 用法

```bash
# 最常用：默认原型 + 默认 18505 + 自动识别演示密码
node scripts/visual-diff.mjs --out .scratch/visual-baseline

# 显式指定
node scripts/visual-diff.mjs \
  --prototype "frontend/prototype/overview-hd/总览 · 玻璃磨砂.html" \
  --app http://127.0.0.1:18505/uniapp/#/pages/overview/index \
  --out .scratch/visual-baseline \
  --password <管理员密码>
```

参数：

| 参数 | 说明 |
| --- | --- |
| `--prototype <path>` | 原型 HTML，支持中文路径（内部用 `pathToFileURL`） |
| `--app <url>` | 实现页面；默认 `http://127.0.0.1:18505/uniapp/#/pages/overview/index` |
| `--out <dir>` | 输出目录，默认 `.scratch/visual-diff` |
| `--password <pw>` | 管理员密码；省略时依次尝试 `VISUAL_DIFF_PASSWORD` / `FASTEMS_PARITY_PASSWORD` / `FASTEMS_ADMIN_PASSWORD`，再尝试文档化演示密码 |
| `--port <n>` | 未给 `--app` 时用它拼默认 URL，默认 18505 |
| `--layer <name>` | 实现侧要打开的台账层；默认取原型 `<h1>` 文本，让两边看同一层 |

环境变量：`VISUAL_DIFF_MODULE_ROOTS`（额外 `node_modules` 目录，`path.delimiter`
分隔；在 git worktree 里依赖在主检出时用）、`VISUAL_DIFF_TOKEN`（复用已有 token，
不新建会话）、`VISUAL_DIFF_THRESHOLD`（pixelmatch 阈值，默认 0.1）、
`VISUAL_DIFF_BROWSER`（Chromium 可执行文件路径）。

**关于登录会话**：后端默认每账号最多 5 个并发会话、闲置 30 分钟才释放，且没有
登出接口。工具只调用一次 `/api/login` 拿到 token，然后把 token 写进浏览器存储，
不再走第二遍表单登录；`metrics.json` 的 `inputs.app.loginMethod` 会显示 `token`
或 `form`。反复跑仍会各留一个会话，达到上限时 `/api/login` 返回
`session_limit_reached`（HTTP 409），工具会直接报错并说明处理方式：重启演示、
调高 `FASTEMS_MAX_SESSIONS`、用 `VISUAL_DIFF_TOKEN` 复用已有会话，或等待闲置过期。

## 输出

| 文件 | 内容 |
| --- | --- |
| `prototype.png` | 原型截图，1440×900 |
| `app.png` | 实现截图，1440×900 |
| `diff.png` | 差异热图：红色为计入差异的像素，黄色为被判为抗锯齿、不计入的像素 |
| `side-by-side.png` | 左原型、右实现，中间 4px 分隔 |
| `metrics.json` | 全部数值与两侧关键 computed style |

`metrics.json` 关键字段：

- `overall`：`totalPixels` / `mismatchedPixels` / `mismatchRatio`（0–1）/ `percent`。
- `bands`：三栏 x 区间 `0–214` / `214–1104` / `1104–1440`，各自的不匹配比例。
  这三段对应 1440 宽下 `200px / 1fr / 320px`、页面内边距 16、列间距 14 的三栏。
- `regions`：`hero`（`.hero`）、`main`（`main.stage` 中栏）、`rail`（右栏）。
  区域取原型几何，实现侧矩形同表记录。注意 `hero` 是中栏的子集，区域之间有重叠，
  不要把它们相加。
- `computedStyles`：两侧面板（hero / daily / rail / ledger）的
  `backgroundColor`、`backgroundImage`、`backdropFilter`、`border`、
  `borderRadius`、`boxShadow`；指标卡尺寸与圆角、值字号；`heroTitle` 字号与字体。
- `inputs.app.pageErrors`：实现页运行期 JS 错误；非空说明截图可能不可信。

## 怎么读阈值

以下阈值是经验起点，按项目实际收敛目标调整：

| 整体不匹配比例 | 判断 |
| --- | --- |
| `< 2%` | 接近；残余多为字体渲染与实时数值差异 |
| `2% – 6%` | 仍有可见结构性差异，逐栏定位 |
| `> 6%` | 需要处理；先看 `bands` / `regions` 里最高的那一段 |

分栏读法：

- **主区（main）通常最高**：三维场景、图表、表格与实时数值都在这里。三维场景
  缺失或有与无的差别会显著抬高主区比例，先确认这是否是本轮范围。
- **hero**：指标卡材质（有卡 vs 仅分隔线）、指标数量、标题与面包屑。
- **左右栏**：台账条目、右栏三块内容与告警数据；数据状态不同也会产生差异。
- 左右栏明显低于主区说明三栏骨架已对齐，差异集中在内容块。

## 基线实测（2026-11-26）

命令：

```bash
# 日常用默认 18505：node scripts/visual-diff.mjs --out .scratch/visual-baseline
# 下面这次验证另起了一份临时演示（见下），故显式给 --app：
VISUAL_DIFF_MODULE_ROOTS="<主检出>/frontend-uniapp/node_modules;<主检出>/frontend/node_modules" \
  node scripts/visual-diff.mjs \
    --app http://127.0.0.1:18506/uniapp/#/pages/overview/index \
    --out .scratch/visual-baseline
```

测量对象是 `cmd/pumphouse -project` 种子数据起的一份临时演示（与 18505 同一构建产物；
因为共享演示已到会话上限，验证时另起一份，跑完已清理）。两边都落在 `泵房` 层
（原型 `<h1>` 自动匹配），`loginMethod: token`，`pageErrors: []`。结果：

| 维度 | 不匹配比例 |
| --- | --- |
| 整体 | **8.633%** |
| 左栏 0–214 | 2.297% |
| 主区 214–1104 | 12.121% |
| 右栏 1104–1440 | 3.428% |
| hero | 8.392% |
| 主区（区域） | 13.575% |
| 右栏（区域） | 2.980% |

（同一构建上在 18505 另跑一次得到整体 8.661%，两次相差 0.03 个百分点，来自实时
数值与时钟文本，属正常抖动。）

关键 computed style 对比：

- **面板材质已一致**：hero / daily / rail / ledger 的背景均为
  `rgba(255,255,255,0.42)`，`backdrop-filter` 均为
  `blur(32px) saturate(1.55) contrast(1.02)`，边框 `2px solid rgba(255,255,255,0.78)`，
  圆角 `20px`，阴影同值。玻璃外观不是当前差异来源。
- **指标卡是主要差异**：原型有卡（`rgba(255,255,255,0.58)` 背景、`1px` 边框、
  `14px` 圆角、`10px 12px` 内边距、投影、值字号 `24px`）；实现 hero 指标是
  无卡分隔线（背景透明、无边框、无圆角、无投影、值字号 `25px`）。
- **标题一致**：两边 `h1` 均为 `34px / 700`、同一字体栈。
- 主区差异主要来自三维能流场景（原型有、实现为列表/表格块）与今日统计柱图数据
  未就绪，而不是三栏骨架。

## 已知限制

- **字体渲染**：Windows 上的 Chrome 与设计稿/其他平台字形不同；同一机器复测稳定，
  跨平台不要直接比绝对值。
- **实时数值**：实现页的观测值、时间戳每次都可能变；差异里包含这些文本抖动。
- **三维场景**：原型的三维等距厂区在实现中不是同一渲染，主区差异会被抬高；这是
  已知识别项，不是回归。
- **背景底板**：原型有模糊色斑与图纸背景，实现背景实现方式不同，四周边距也会有差异。
- **抗锯齿**：默认 `includeAA: false`，被判定为抗锯齿的差异不计入（但热图里标黄）。
- **滚动条与截断**：截图是视口，不滚动；超出 900px 的内容不参与比对。
- **区域重叠**：`hero` 属于 `main`，区域比例不能相加；三栏 `bands` 覆盖整幅图。
- **像素不等于正确**：像素接近不代表文案、计数、交互正确，反之亦然。

## 结论

用 `metrics.json` 的 `overall` 看趋势，用 `bands` / `regions` 定位问题，用
`computedStyles` 确认差异来自材质还是结构。视觉数字只用于收敛设计差距；语义与
行为正确性仍以 `scripts/prototype-parity.sh` 和既有 Node/Go 测试为准。
