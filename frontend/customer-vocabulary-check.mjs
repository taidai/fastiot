// 客户视图用词检查：客户工作台页面与抽屉里出现内部术语即失败。
// 交付工作台合法使用同一批术语，所以按“客户工作台可达”收窄范围，而不是全仓禁词。
// 运行：cd frontend && npm run check:vocabulary
import assert from "node:assert/strict"
import { existsSync, readFileSync } from "node:fs"
import ts from "typescript"
import { groupDefs, pageDefs } from "./src/navigation.ts"

const forbidden = ["虚拟输入", "计算点", "Profile", "业务节点", "采样周期", "聚合", "决策表", "通道配置", "班次交接"]

const workbenchOf = (def) => groupDefs.find((group) => group.key === def.group)?.workbench
const customerPageKeys = pageDefs.filter((def) => workbenchOf(def) === "customer").map((def) => def.key)
const deliveryPageKeys = [...pageDefs.filter((def) => workbenchOf(def) === "delivery").map((def) => def.key), "delivery"]

// 客户工作台页面 → 该页面渲染的组件文件（含页面内抽屉与对话框）。
// 页面键来自 navigation.ts 的 customer 分组；新增客户页时这里必须同步，否则检查失败。
const customerSurface = {
  devices: [
    "src/App.tsx",
    "src/KpiStrip.tsx",
    "src/EnergyFlow.tsx",
    "src/EnergyUnitsStatus.tsx",
    "src/HealthSummary.tsx",
    "src/AssistantPanel.tsx",
    "src/KpiConfigPanel.tsx",
    "src/ShiftHandover.tsx",
    "src/CommonCommands.tsx",
    "src/VirtualInputs.tsx",
    "src/CalculationPoints.tsx",
    "src/ConfirmDialog.tsx",
    "src/observations.ts",
  ],
  screen: ["src/ScreenView.tsx", "src/ConfirmDialog.tsx"],
  // 类型管理是交付人员专属页签，不在此检查范围；统计与提醒客户可用。
  alarms: ["src/AlarmsView.tsx", "src/AlarmStatsPanel.tsx", "src/AckDialog.tsx"],
  history: ["src/HistoryView.tsx", "src/historyGaps.ts", "src/observations.ts"],
  dispatchTrace: ["src/DispatchTraceView.tsx"],
  health: ["src/HealthView.tsx"],
}

assert.deepEqual(
  Object.keys(customerSurface).sort(),
  [...customerPageKeys].sort(),
  "客户页清单与 navigation.ts 不一致：新增或删除客户页后必须更新本检查的覆盖范围",
)

const scannedFiles = new Set(Object.values(customerSurface).flat())
for (const file of scannedFiles) {
  assert.ok(existsSync(new URL(file, import.meta.url)), `客户视图检查列出的文件不存在：${file}`)
}

const read = (file) => readFileSync(new URL(file, import.meta.url), "utf8")
const parse = (file) => ts.createSourceFile(file, read(file), ts.ScriptTarget.Latest, true, file.endsWith(".tsx") ? ts.ScriptKind.TSX : ts.ScriptKind.TS)

const violations = []

function record(sf, file, node, text) {
  const copy = text.trim()
  if (!copy) return
  for (const term of forbidden) {
    if (!copy.includes(term)) continue
    const { line } = sf.getLineAndCharacterOfPosition(node.getStart(sf))
    violations.push({ file, line: line + 1, term, text: copy })
  }
}

function recordCopyNode(sf, file, node) {
  if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) record(sf, file, node, node.text)
  else if (ts.isTemplateHead(node) || ts.isTemplateMiddle(node) || ts.isTemplateTail(node)) record(sf, file, node, node.text)
  else if (ts.isJsxText(node)) record(sf, file, node, node.text)
}

// 普通组件：整文件扫描。两个工作台共用的抽屉组件另走 scanDrawer。
function scanFile(file) {
  const sf = parse(file)
  const visit = (node) => {
    recordCopyNode(sf, file, node)
    ts.forEachChild(node, visit)
  }
  visit(sf)
}

// 共用抽屉（VirtualInputs / CalculationPoints）：
// 只扫描客户模式会渲染的文案。`const text = customer ? {...} : {...}` 的交付分支跳过；
// CalculationPoints 的 sources 映射里 `calculation: "计算点"` 在客户模式下被 text.sourceLabel 覆盖，同样跳过。
function scanDrawer(file) {
  const sf = parse(file)
  const visit = (node) => {
    if (ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && node.initializer) {
      if (node.name.text === "text" && ts.isConditionalExpression(node.initializer)) {
        const test = node.initializer.condition
        assert.ok(ts.isIdentifier(test) && test.text === "customer", `${file}：找不到 customer 条件分支，无法判断客户视图文案`)
        visit(node.initializer.whenTrue)
        return
      }
      if (node.name.text === "sources" && ts.isObjectLiteralExpression(node.initializer)) {
        for (const property of node.initializer.properties) {
          const name = property.name && (ts.isIdentifier(property.name) || ts.isStringLiteral(property.name)) ? property.name.text : ""
          if (name !== "calculation") visit(property)
        }
        return
      }
    }
    recordCopyNode(sf, file, node)
    ts.forEachChild(node, visit)
  }
  visit(sf)
}

// App.tsx 同时承载两个工作台：只扫描客户页面的组件函数，并跳过交付页条件、
// 管理员专属分支、交付页标题表和登录页。
function scanWorkbenchApp(file) {
  const sf = parse(file)
  const roleIdentifiers = new Set(["isAdmin", "canConfigure", "deliveryAvailable"])

  const containsRole = (node) => {
    let found = false
    const check = (current) => {
      if (found) return
      if (ts.isIdentifier(current) && roleIdentifiers.has(current.text)) { found = true; return }
      ts.forEachChild(current, check)
    }
    check(node)
    return found
  }

  // true 表示条件成立时进入交付专属分支；false 表示条件成立时仍在客户视图。
  const deliveryDirection = (test) => {
    if (ts.isPrefixUnaryExpression(test) && test.operator === ts.SyntaxKind.ExclamationToken && containsRole(test.operand)) return false
    if (containsRole(test)) return true
    if (ts.isBinaryExpression(test)) {
      const operator = test.operatorToken.kind
      const subject = test.left.getText(sf)
      const compared = subject === "page" || subject === "workbench" ? test.right : undefined
      if (compared && ts.isStringLiteral(compared)) {
        const key = compared.text
        const equality = operator === ts.SyntaxKind.EqualsEqualsEqualsToken
        const inequality = operator === ts.SyntaxKind.ExclamationEqualsEqualsToken
        if (!equality && !inequality) return null
        if (test.left.getText(sf) === "workbench" && key === "delivery") return equality
        if (deliveryPageKeys.includes(key)) return equality
        if (customerPageKeys.includes(key)) return !equality
      }
    }
    return null
  }

  const findFunction = (name) => {
    let found
    const visit = (node) => {
      if (ts.isFunctionDeclaration(node) && node.name?.text === name) { found = node; return }
      ts.forEachChild(node, visit)
    }
    visit(sf)
    return found
  }

  const scanFunction = (name) => {
    const fn = findFunction(name)
    assert.ok(fn, `${file}：找不到客户页面组件 ${name}`)
    const visit = (node) => {
      if (ts.isIfStatement(node) && node.expression.getText(sf) === "!token") {
        if (node.elseStatement) visit(node.elseStatement)
        return
      }
      if (ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && node.name.text === "pageInfo" && node.initializer && ts.isObjectLiteralExpression(node.initializer)) {
        for (const property of node.initializer.properties) {
          const key = property.name && (ts.isIdentifier(property.name) || ts.isStringLiteral(property.name)) ? property.name.text : ""
          if (customerPageKeys.includes(key)) visit(property)
        }
        return
      }
      if (ts.isConditionalExpression(node)) {
        const direction = deliveryDirection(node.condition)
        if (direction === true) { visit(node.whenFalse); return }
        if (direction === false) { visit(node.whenTrue); return }
      }
      if (ts.isBinaryExpression(node)) {
        const operator = node.operatorToken.kind
        if (operator === ts.SyntaxKind.AmpersandAmpersandToken || operator === ts.SyntaxKind.BarBarToken) {
          const direction = deliveryDirection(node.left)
          if (direction === true) return
          if (direction === false) { visit(node.right); return }
        }
      }
      recordCopyNode(sf, file, node)
      ts.forEachChild(node, visit)
    }
    visit(fn)
  }

  scanFunction("App")
  scanFunction("DevicesView")
}

for (const file of scannedFiles) {
  if (file === "src/App.tsx") scanWorkbenchApp(file)
  else if (file === "src/VirtualInputs.tsx" || file === "src/CalculationPoints.tsx") scanDrawer(file)
  else scanFile(file)
}

assert.equal(
  violations.length,
  0,
  "客户视图出现内部术语（交付工作台不受影响；请改用客户措辞或把交付专属文案放进交付分支）：\n"
    + violations.map((item) => `  ${item.file}:${item.line}  ${item.term}  「${item.text}」`).join("\n"),
)

console.log("Customer vocabulary check passed:", {
  customerPages: customerPageKeys.length,
  scannedFiles: scannedFiles.size,
  forbiddenTerms: forbidden.length,
})
