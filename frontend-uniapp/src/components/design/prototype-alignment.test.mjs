import test from "node:test"
import assert from "node:assert/strict"
import { readFileSync, existsSync } from "node:fs"

const page = readFileSync(new URL("../../pages/design/index.vue", import.meta.url), "utf8")
const prototype = readFileSync(new URL("../../../../frontend/prototype/overview-hd/设计 · 玻璃磨砂.html", import.meta.url), "utf8")
const access = readFileSync(new URL("./AccessStep.vue", import.meta.url), "utf8")
const strategy = readFileSync(new URL("./StrategyStep.vue", import.meta.url), "utf8")
const ui = readFileSync(new URL("./UiStep.vue", import.meta.url), "utf8")
const deliveryURL = new URL("./DeliveryStep.vue", import.meta.url)
const delivery = existsSync(deliveryURL) ? readFileSync(deliveryURL, "utf8") : ""
const machine = readFileSync(new URL("./StateMachinePanel.vue", import.meta.url), "utf8")
const aggregate = readFileSync(new URL("./AggregationPanel.vue", import.meta.url), "utf8")

test("prototype tools remain reachable without inventing placeholder functionality", () => {
  const toolsURL = new URL("./PrototypeTools.vue", import.meta.url)
  assert.ok(existsSync(toolsURL), "global tool surface exists")
  const tools = readFileSync(toolsURL, "utf8")
  for (const key of ["channels", "points", "profiles", "templates", "verify", "aggregate", "calculations", "virtualInputs", "health", "alarmTypes", "retention"]) assert.ok(tools.includes(key), key)
  assert.match(tools, /这一页只验证位置，不实现内容。/)
  assert.match(tools, /emit\('back'\)/)
  for (const key of ["channels", "points", "profiles", "health", "verify"]) assert.ok(access.includes(`emit('tool', '${key}')`), key)
  for (const key of ["aggregate", "calculations", "virtualInputs", "alarmTypes"]) assert.ok(strategy.includes(`emit('tool', '${key}')`), key)
  assert.ok(delivery.includes("emit('tool', 'retention')"))
})

test("preview-only side cards and published version diff match final prototype", () => {
  for (const fixture of ["饮水线冲洗", "备用泵试转", "采暖预循环", "早高峰放电", "批量并网", "10:42 · 启动"]) assert.ok(ui.includes(fixture), fixture)
  assert.match(ui, /v-if="PREVIEW_MODE"[^>]*data-preview="today"/)
  assert.match(ui, /v-if="PREVIEW_MODE"[^>]*data-preview="oplog"/)
  assert.match(ui, /v-if="PREVIEW_MODE"[^>]*data-preview="trend"/)
  assert.match(ui, /后端未提供“今日执行”查询 API/)
  assert.match(ui, /尚未读取历史序列，不生成演示曲线。/)
  assert.match(delivery, /v-if="PREVIEW_MODE && versions.length"[\s\S]*?版本差异/)
  assert.match(delivery, /\+1 设备 · \+1 画面 · 规则 2 → 3/)
  assert.match(delivery, /previewChecks/)
})

test("state machine shows event simulation and a transition table before editors", () => {
  assert.match(machine, /class="chips machine-events"[\s\S]*?runEvent\(event\)/)
  assert.match(machine, /class="mini-table machine-table"[\s\S]*?<th>当前<\/th><th>事件<\/th><th>守卫<\/th><th>下一状态<\/th><th>动作<\/th>/)
  assert.match(machine, /class="event-panel"[\s\S]*?class="mini-table machine-table"[\s\S]*?<details class="definition-editor"[^>]*>/)
})

test("aggregation uses prototype two-column definition and result cards", () => {
  assert.match(aggregate, /class="two-col"[\s\S]*?class="acc-card aggregate-definition"[\s\S]*?class="acc-card aggregate-result"/)
  assert.match(aggregate, /class="chips"[\s\S]*?op = 'sum'/)
  assert.match(aggregate, /class="mini-table aggregate-members"[\s\S]*?<th>成员<\/th><th>值<\/th><th>质量<\/th>/)
})

test("delivery step uses real package, version and preflight APIs and exposes deployment gaps", () => {
  assert.match(prototype, /项目包里有什么/)
  assert.ok(delivery.length > 0, "design-specific DeliveryStep.vue must exist")
  assert.match(delivery, /<view class="step-head">[\s\S]*?<section class="card glass step-card">/)
  for (const label of ["当前版本", "依赖", "部署目标", "项目包里有什么", "交付前检查", "部署记录"]) assert.match(delivery, new RegExp(label))
  for (const api of ["/projects/versions", "/projects/export", "/projects/publish", "/delivery/gates", "/delivery/preflight"]) assert.ok(delivery.includes(api), `missing real API ${api}`)
  assert.match(delivery, /后端没有独立的“包版本 → 目标实例”部署记录 API/)
  assert.match(delivery, /导出项目包/)
  assert.match(delivery, /去交付页/)
})

test("UI step exposes real layer configuration status and prototype block geometry", () => {
  assert.match(prototype, /哪些层已配/)
  assert.match(ui, /给这一层选块、绑定对象；保存后客户看到的就是它。/)
  assert.match(ui, /class="bind-head"[\s\S]*?\{\{ selectedName \}\}/)
  assert.match(ui, /class="card glass layer-status-card"[\s\S]*?哪些层已配/)
  assert.match(ui, /screenStateText\(row\.node\.id\)/)
  assert.match(ui, /\.cfg-row\s*\{[^}]*gap:\s*5px;[^}]*padding:\s*5px 2px;[^}]*font-size:\s*12\.5px;/)
  assert.match(ui, /\.cfg-row \.src\s*\{[^}]*font-size:\s*10\.5px;/)
  assert.doesNotMatch(ui, /\.cfg-list\s*\{[^}]*min-height:\s*519px;/)
})

test("strategy step keeps scope, state machine, four-part rules and simulation in one workspace", () => {
  assert.match(prototype, /<h5 style="margin:14px 0 6px">决策表 · 有序，首命中<\/h5>/)
  assert.match(strategy, /<view class="step-head">[\s\S]*?<section class="card glass step-card">/)
  assert.match(strategy, /作用范围/)
  assert.match(strategy, /决策表 · 有序，首命中/)
  assert.match(strategy, /<StateMachinePanel(?![^>]*v-else)/)
  assert.match(strategy, /<details[^>]*>[\s\S]*?聚合与计算[\s\S]*?<AggregationPanel/)
  for (const title of ["条件组", "持续与冷却", "时间窗", "动作序列"]) assert.match(strategy, new RegExp(`<strong>${title}</strong>`))
  assert.match(strategy, /class="mini-table rules-table/)
  assert.match(strategy, /下一步：配界面 →/)
})

test("access step keeps prototype device, unit and scope workflows with a four-stage pipeline", () => {
  assert.match(prototype, /<button class="ok" data-stage="profile">选型号<\/button><i>→<\/i>/)
  assert.match(prototype, /<th>点位<\/th><th>要求<\/th><th>地址<\/th><th>类型<\/th><th>倍率<\/th><th>单位<\/th><th>读写<\/th><th>状态<\/th>/)
  assert.match(access, /<view class="step-head">[\s\S]*?<section class="card glass step-card">/)
  assert.match(access, /class="pipeline"[\s\S]*?<i[^>]*>→<\/i>/)
  assert.match(access, /v-for="profile in availableProfiles"[\s\S]*?class="pick"/)
  assert.match(access, /Profile 声明/)
  for (const heading of ["点位", "要求", "地址", "类型", "倍率", "单位", "读写", "状态"]) assert.match(access, new RegExp(`<th>${heading}</th>`))
  assert.match(access, /下一步：配策略 →/)
  assert.match(access, /还没有设备[\s\S]*?回第 2 步 →/)
})

test("design shell exposes the prototype statebar, tree sidebar and completed-step icon contract", () => {
  assert.match(prototype, /<div class="statebar" id="statebar"><\/div>/)
  assert.match(prototype, /<div class="tree">[\s\S]*?<div class="tree-title">/)
  assert.match(prototype, /<i>\$\{status\[step.key\] \? '✓' : index \+ 1\}<\/i>/)

  assert.match(page, /<view class="statebar">[\s\S]*?工作台 <b>设计<\/b>[\s\S]*?页面 <b>\{\{ activeTool \|\| '台账树' \}\}<\/b>/)
  assert.match(page, /<aside class="side design-ledger glass">[\s\S]*?<view v-else class="tree">[\s\S]*?<view class="tree-title">/)
  assert.match(page, /<view class="stepper-wrap">[\s\S]*?<view class="stepper">/)
  assert.match(page, /\{\{ step\.done \? '✓' : index \+ 1 \}\}/)
})

test("site and ledger steps retain the prototype card structure and real-data columns", () => {
  assert.match(prototype, /从哪种骨架开始/)
  assert.match(prototype, /<th><\/th><th>对象<\/th><th>子对象<\/th><th>设备<\/th><th>未绑定<\/th><th>策略<\/th><th>总览<\/th>/)

  const siteStart = page.indexOf("v-if=\"current === 'site'\"")
  const treeStart = page.indexOf("v-else-if=\"current === 'tree'\"")
  const accessStart = page.indexOf("v-else-if=\"current === 'access'\"")
  const site = page.slice(siteStart, treeStart)
  const tree = page.slice(treeStart, accessStart)
  assert(siteStart >= 0 && treeStart > siteStart && accessStart > treeStart)
  assert.match(site, /先确定这是哪个现场，后面所有对象都挂在它下面。/)
  assert.match(site, /从哪种骨架开始/)
  assert.match(site, /骨架预览/)
  assert.match(site, /class="outline template-outline"[\s\S]*?class="row"/)
  assert.doesNotMatch(site, /metric-grid|site-outline/)
  assert.match(tree, /按区域、单元、设备把现场搭出来；设备可以先占位。/)
  assert.match(tree, /<h5[^>]*>台账<\/h5>/)
  for (const heading of ["子对象", "未绑定", "策略", "总览"]) assert.match(tree, new RegExp(`<th>${heading}</th>`))
  assert.match(tree, /搜索对象 \/ 设备 \/ 点位/)
  assert.match(tree, /matchReason\(row, treeQuery/)
  assert.match(tree, /screenText\(row\.node\.id\)/)
})

test("step completion follows prototype scope rather than only the selected screen", () => {
  assert.match(page, /key: "access"[\s\S]*?bound === accessSummary\.value\.devices/)
  assert.doesNotMatch(page, /key: "access"[^\n]*verified === accessSummary\.value\.devices/)
  assert.match(page, /key: "ui"[\s\S]*?everyLayerHasScreen\.value/)
})

test("design shell uses the final prototype geometry instead of an offset card layout", () => {
  assert.match(prototype, /\.layout\{gap:14px;padding:14px;grid-template-columns:272px 1fr\}/)
  assert.match(prototype, /\.side\{border:2px solid var\(--glass-border\);border-radius:20px;padding:14px 10px 30px;/)
  assert.match(page, /\.layout\s*\{[^}]*grid-template-columns:\s*272px minmax\(0,\s*1fr\);[^}]*gap:\s*14px;[^}]*margin-top:\s*14px;/)
  assert.doesNotMatch(page, /\.layout\s*\{[^}]*margin-top:\s*36px;/)
  assert.match(page, /\.statebar\s*\{[^}]*height:\s*24px;[^}]*padding:\s*0 18px;/)
  assert.match(page, /\.stepper-wrap\s*\{[^}]*position:\s*relative;[^}]*margin-bottom:\s*16px;/)
})
