// 一致性检查：导航是否按任务组织、页面是否都可渲染、token 是否只有一处。
// 运行：cd frontend && npm run check:consistency
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { groupDefs, pageDefs } from "./src/navigation.ts"

const read = (path) => readFileSync(new URL(path, import.meta.url), "utf8")

// 1. 设计 token 只能来自 tokens.css；其他样式文件不得再声明 :root 设计块。
const tokenFiles = ["src/styles.css", "src/reference.css", "src/tailwind.css", "src/deviceWorkspace.css"]
for (const file of tokenFiles) {
  assert.ok(!/^:root\s*\{/m.test(read(file)), `${file} 仍在声明 :root 设计 token，应移到 tokens.css`)
}
const tokens = read("src/tokens.css")
for (const name of ["--ink", "--muted", "--faint", "--line", "--accent", "--mint", "--blue", "--amber", "--danger"]) {
  assert.ok(tokens.includes(`${name}:`), `tokens.css 缺少 ${name}`)
}
const main = read("src/main.tsx")
assert.ok(main.indexOf('"./tokens.css"') >= 0 && main.indexOf('"./tokens.css"') < main.indexOf('"./styles.css"'), "tokens.css 必须先于 styles.css 引入")

// 2. 所有注册页面都必须在 App 中渲染，避免“导航有入口、页面不存在”。
const app = read("src/App.tsx")
for (const def of pageDefs) {
  assert.ok(app.includes(`page === "${def.key}"`), `App.tsx 缺少页面渲染：${def.key}`)
}

// 3. 导航必须是任务 + 页签；不再保留下拉菜单。
assert.ok(!/topnav-dropdown/.test(app), "App.tsx 仍在使用旧的下拉导航")
assert.ok(!/topnav-dropdown/.test(read("src/reference.css")), "reference.css 仍保留下拉导航样式")
assert.ok(app.includes("navGroupsFor"), "App.tsx 未使用任务导航 navGroupsFor")

// 4. 每个任务都在任务合同里有归属。
const jobs = read("../docs/jobs.md")
for (const group of groupDefs) {
  assert.ok(jobs.includes(group.label), `docs/jobs.md 缺少任务：${group.label}`)
}
assert.ok(jobs.includes("功能准入"), "docs/jobs.md 缺少功能准入规则")

// 5. 查证页可从页面内到达（隐藏不等于不可达）。
for (const hidden of ["history", "dispatchTrace", "health"]) {
  assert.ok(new RegExp(`(?:setPage|openPage)\\([^)]*"${hidden}"`).test(app), `隐藏页 ${hidden} 没有页面内入口`)
}

// 6. App 通过唯一查询 hook 轮询；报警页与画面页不自行轮询。
const alarmOwners = ["src/App.tsx", "src/useAlarms.ts", "src/AlarmsView.tsx", "src/ScreenView.tsx"].filter((file) => read(file).includes("fetchAlarms"))
assert.deepEqual(alarmOwners, ["src/useAlarms.ts"], `报警轮询应只有一个所有者，当前：${alarmOwners.join(", ")}`)
assert.equal((app.match(/useAlarms\(/g) || []).length, 1, "App 必须只挂载一个报警查询")

// 7. 客户视图用词与结构：三张客户视图票（#17/#20/#22）的断言。
const shift = read("src/ShiftHandover.tsx")
const commands = read("src/CommonCommands.tsx")
const kpi = read("src/KpiStrip.tsx")
const virtualInputs = read("src/VirtualInputs.tsx")
const calculations = read("src/CalculationPoints.tsx")

// #17 指标：未绑定不占位，已绑定无数据才显示「—」；同一屏只保留一处解释。
assert.ok(kpi.includes("bound"), "KpiStrip 应区分已绑定与未绑定指标")
assert.ok(kpi.includes("尚未接入的数据暂不显示；如需接入设备，请联系交付人员。"), "KPI 空状态应指向交付人员")
assert.ok(!kpi.includes("先接入通道与点位"), "KPI 空状态不得再要求客户接入通道与点位")
assert.ok(!kpi.includes("去接入通道"), "KPI 空状态不得提供交付配置入口")
assert.ok(app.includes("未绑定来源的指标不显示；已绑定但尚未收到数据、或已过期的指标以“—”表示"), "总览应只保留一处「—」解释")
assert.ok(!app.includes("不生成模拟数据"), "总览仍保留重复的「—」解释")
assert.ok(!app.includes("数据来自已配置点位"), "页脚仍保留重复的「—」解释")

// #17 点位动作：分别说明后果，下发前确认。
for (const label of ["记录当前值（不写设备）", "写入设备并等待回读", "接管自动调度"]) {
  assert.ok(app.includes(label), `点位动作缺少后果说明：${label}`)
}
assert.ok(/kind: "record"/.test(app), "记录当前值也应走确认对话框")

// #20 抽屉用词：客户视图不出现内部术语。
assert.ok(commands.includes("当前没有可用命令；如需添加，请联系交付人员。"), "常用命令空状态未改为联系交付人员")
assert.ok(!commands.includes("Profile"), "常用命令抽屉仍出现 Profile")
assert.ok(!commands.includes("通道 → 端口配置"), "常用命令抽屉仍出现通道/端口配置")
assert.ok(virtualInputs.includes("模拟输入只参与计算，不会写设备，也不代表实测。"), "模拟输入缺少不影响设备的说明")
assert.ok(calculations.includes("计算值自动更新，不依赖页面打开；只用于展示和计算，不写设备。"), "计算值缺少自动更新、不写设备的说明")
assert.ok(app.includes("模拟输入与计算"), "客户抽屉仍使用「运行输入与计算」")

// #22 本机备忘：改名、设置类入口、说明本机保存，既有记录键不变。
assert.ok(shift.includes("本机备忘"), "交接班记录未改名本机备忘")
assert.ok(shift.includes("不随账号同步"), "本机备忘未说明不随账号同步")
assert.ok(shift.includes("fastems-shift-handovers"), "本机备忘的既有记录存储键被改动")
assert.ok(!shift.includes("班次交接"), "本机备忘仍出现「班次交接」")
assert.ok(!app.includes("班次交接"), "主导航仍出现「班次交接」")
assert.ok(app.includes(">本机备忘<") && app.includes("setShiftOpen(true)"), "本机备忘缺少设置类入口")

console.log("consistency-check passed:", {
  pages: pageDefs.length,
  groups: groupDefs.length,
  tokenSources: 1,
  alarmPollers: alarmOwners.length,
  customerViewTickets: 3,
})
