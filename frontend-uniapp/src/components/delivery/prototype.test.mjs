import test from "node:test"
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"

const read = name => readFileSync(new URL(name, import.meta.url), "utf8")
const page = read("../../pages/delivery/index.vue")
const accounts = read("./AccountsPanel.vue")
const audit = read("./AuditPanel.vue")
const industry = read("./IndustryPanel.vue")
const importGuide = read("./ImportGuide.vue")

test("delivery navigation and page headings use the final prototype structure", () => {
  assert.match(page, /<view class="navgroup"><h4>交付资产<\/h4>/)
  assert.match(page, /<view class="navgroup"><h4>系统<\/h4>/)
  assert.doesNotMatch(page, /nav-title|step-head page-heading/)
  assert.match(page, /<view class="variant-head"><b>项目包与版本<\/b><span>这份工程是哪个版本、依赖什么？<\/span><\/view>/)
  for (const [name, source, title, question] of [
    ["accounts", accounts, "账号与权限", "谁能做什么？"],
    ["audit", audit, "审计", "谁在什么时候做过什么？"],
    ["industry", industry, "行业包", "本站启用了什么行业能力？"],
  ]) {
    assert.match(source, new RegExp(`<template>\\s*<view class="variant-head"><b>${title}<\\/b><span>${question}<\\/span><\\/view>`), name)
  }
})

test("project package uses prototype tables and actions without demo controls", () => {
  const contents = page.match(/<section class="card glass package-card package-contents">([\s\S]*?)<\/section>/)?.[1] || ""
  assert.match(contents, /<h5>包内容/)
  assert.match(contents, /<table class="mini-table">/)
  for (const label of ["对象", "点位", "画面", "策略", "依赖"]) {
    assert.match(contents, new RegExp(`<td>${label}<\\/td>`), label)
  }
  assert.match(page, /busy === "publish" \? "发布中…" : hasPublished \? "重新发布" : "发布版本"/)
  assert.match(page, /busy === "evidence" \? "导出中…" : "导出验收证据"/)
  assert.match(page, /v-if="PREVIEW_MODE" class="actions" aria-label="原型模拟场景"/)
  assert.match(page, /模拟预检失败/)
  assert.match(page, /模拟依赖不匹配/)
})

test("blocked publish offers repair rather than a confirmation bypass", () => {
  for (const surface of [page, read("../design/DeliveryStep.vue")]) {
    assert.equal(/publish\(true\)|预检未通过，仍要发布/.test(surface), false, "ordinary publish must not offer bypass")
    assert.match(surface, /修复后重新预检/)
  }
})

test("import guide keeps all four prototype stages and only marks factual progress", () => {
  assert.match(page, /\{\{ importResult \? "跨实例导入" : "跨实例交付" \}\}/)
  assert.match(page, /在干净实例导入，再重新绑定现场/)
  const pipeline = importGuide.match(/<view class="pipeline"[\s\S]*?<\/view>/)?.[0] || ""
  for (const stage of ["导出", "导入", "现场重绑", "运行"]) assert.match(pipeline, new RegExp(`>${stage}<`), stage)
  assert.match(pipeline, /<span class="ok">导出<\/span><i>→<\/i><span class="ok">导入<\/span>/)
  assert.match(importGuide, /<span class="current">现场重绑<\/span><i>→<\/i><span>运行<\/span>/)
  assert.match(importGuide, /v-if="PREVIEW_MODE"/)
  assert.match(importGuide, /完成现场重绑/)
  assert.match(importGuide, /在新实例运行/)
  assert.doesNotMatch(pipeline, /运行中|已部署/)
})

test("deployment records keep the prototype table and expose the real backend gap", () => {
  assert.match(page, /<template v-else-if="current === 'deploy'">\s*<view class="variant-head"><b>部署记录<\/b><span>它现在跑在哪个现场？<\/span><\/view>/)
  for (const heading of ["目标", "版本", "时间", "人", "状态"]) assert.match(page, new RegExp(`<th>${heading}<\\/th>`), heading)
  assert.match(page, /<td colspan="5"[^>]*>[^<]*后端没有独立的“包版本 → 目标实例”部署记录 API/)
  assert.match(page, /本次页面导入状态/)
  assert.doesNotMatch(page, /v2026\.11\.19-1|11-19 09:58|待部署/)
})

test("rollback lists real versions before the prototype scope table", () => {
  assert.match(page, /<template v-else-if="current === 'rollback'">\s*<view class="variant-head"><b>版本回退<\/b><span>怎么回到上一版工程？<\/span><\/view>/)
  assert.match(page, /class="row version-row"/)
  assert.match(page, /rollbackConfirm === version\.id \? "确认回退（历史保留）" : "回退"/)
  assert.ok(page.indexOf('class="version-list"') < page.indexOf(">回退范围</h5>"))
  const scope = page.match(/>回退范围<\/h5>([\s\S]*?)<\/table>/)?.[0] || ""
  assert.match(scope, /<table class="mini-table">/)
  for (const label of ["恢复", "保留", "不做", "后端限制"]) assert.match(scope, new RegExp(`<td>${label}<\\/td>`), label)
})

test("accounts use a real account table and a separate permission matrix card", () => {
  assert.match(accounts, /<section class="card glass accounts-card">/)
  assert.match(accounts, /<table class="account-table">/)
  for (const heading of ["人员", "角色", "工程权限", "可操作对象"]) assert.match(accounts, new RegExp(`<th>${heading}<\\/th>`), heading)
  assert.match(accounts, /<button v-if="canManage" class="tag create-toggle"/)
  assert.match(accounts, /<section v-if="canManage && users.length" class="card glass matrix-card">/)
  assert.match(accounts, /<h5>角色 × 动作 × 对象<\/h5>/)
  assert.match(accounts, /v-if="PREVIEW_MODE"/)
  assert.match(accounts, /以李工身份检查/)
  assert.match(accounts, /不切换登录身份，不发送命令/)
})

test("audit uses prototype event rows backed by filtered API records", () => {
  assert.match(audit, /<view v-else class="audit-list">/)
  assert.match(audit, /v-for="\(item, index\) in records"[^>]*class="row audit-row"/)
  assert.match(audit, /\{\{ clock\(item\.time\) \}\} · \{\{ item\.actor \|\| "—" \}\} · \{\{ item\.action \|\| "—" \}\}/)
  assert.doesNotMatch(audit, /10:42 · 张工|09:58 · 张工/)
})

test("industry packages use prototype panel headings, capability rows and disabled banner", () => {
  assert.match(industry, /class="banner industry-banner"/)
  assert.match(industry, /class="panel-head"><h3>\{\{ pkg\.label \|\| pkg\.id \}\}<\/h3>/)
  assert.match(industry, /class="tag" :class="pkg\.installed \? 'ok' : ''"/)
  assert.match(industry, /class="tag">\{\{ pkg\.id \}\}@\{\{ pkg\.version \|\| "—" \}\}<\/span>/)
  assert.match(industry, /v-for="item in pkg\.provides \|\| \[\]"[^>]*class="row capability-row"/)
  assert.doesNotMatch(industry, /class="package-item"/)
})

test("delivery scoped CSS carries the final glass, table and control dimensions", () => {
  const css = page.match(/<style scoped>([\s\S]*?)<\/style>/)?.[1] || ""
  assert.match(css, /\.statebar\s*\{[^}]*padding: 6px 18px;[^}]*border: 0;[^}]*backdrop-filter: blur\(16px\);/)
  assert.doesNotMatch(css.match(/\.statebar\s*\{[^}]*\}/)?.[0] || "", /(?:^|[;{])\s*height:/)
  assert.match(css, /\.side\.glass\s*\{[^}]*backdrop-filter: blur\(28px\) saturate\(1\.4\);/)
  for (const rule of [
    /\.stage :deep\(\.card\.glass\)\s*\{[^}]*backdrop-filter: blur\(28px\) saturate\(1\.45\);[^}]*border: 2px solid rgba\(255,255,255,\.78\);[^}]*border-radius: 18px;/,
    /\.stage :deep\(th\)\s*\{[^}]*font-size: 11px;[^}]*padding: 8px 10px;/,
    /\.stage :deep\(td\)\s*\{[^}]*padding: 9px 10px;/,
    /\.stage :deep\(\.mini-table th\), \.stage :deep\(\.mini-table td\)\s*\{[^}]*padding: 7px 8px;/,
    /\.stage :deep\(\.primary\)\s*\{[^}]*padding: 8px 15px;[^}]*font-size: 13px;/,
    /\.stage :deep\(\.ghost\)\s*\{[^}]*padding: 7px 13px;[^}]*font-size: 12\.5px;/,
  ]) assert.match(css, rule)
})
