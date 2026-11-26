#!/usr/bin/env node
// Prototype-vs-implementation visual diff.
//
// Opens the approved overview prototype (file://) and the running H5 overview
// (default http://127.0.0.1:18505/uniapp/#/pages/overview/index) in a fixed
// 1440x900 viewport at deviceScaleFactor 1, disables motion, waits for the
// seeded data and fonts, then writes:
//   prototype.png, app.png   the two raw screenshots
//   diff.png                 pixelmatch diff (red = counted difference)
//   side-by-side.png         prototype | app
//   metrics.json             ratios overall / per column band / per region,
//                            plus the key computed styles on both sides
//
// It measures pixels only. It does not assert structure or copy; keep using
// the semantic guard (scripts/prototype-parity.sh) for that.
import { createRequire } from 'node:module'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import process from 'node:process'

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
// Loaded in main() so --help works without the frontend node_modules.
let chromium
let pixelmatch
let PNG
let options
const VIEWPORT = { width: 1440, height: 900 }
const DEVICE_SCALE = 1
// Column boundaries of the 200 / 1fr / 320 grid at 1440px (page padding 16,
// gaps 14). Kept as explicit numbers so the report is stable even if a layout
// drifts away from the intended grid.
const BANDS = [
  { key: 'left', xRange: [0, 214] },
  { key: 'main', xRange: [214, 1104] },
  { key: 'right', xRange: [1104, 1440] },
]
const DEMO_PASSWORDS = ['pumphouse-demo-2026', 'parity-demo-2026', 'demo-pass-2026']

function usage() {
  return `Usage: node scripts/visual-diff.mjs [options]

Options:
  --prototype <path>  prototype HTML (default: frontend/prototype/overview-hd/总览 · 玻璃磨砂.html)
  --app <url>         implementation page (default: http://127.0.0.1:18505/uniapp/#/pages/overview/index)
  --out <dir>         output directory (default: .scratch/visual-diff)
  --password <pw>     admin password; when omitted, VISUAL_DIFF_PASSWORD /
                      FASTEMS_PARITY_PASSWORD / FASTEMS_ADMIN_PASSWORD are tried,
                      then the documented demo passwords
  --port <n>          app port when --app is not given (default: 18505)
  --layer <name>      app ledger layer to open; default: the prototype's <h1> text
                      so both sides show the same layer
  --help              show this help

Environment:
  VISUAL_DIFF_MODULE_ROOTS  extra node_modules dirs (path.delimiter separated),
                            useful from a git worktree where deps live in the main checkout
  VISUAL_DIFF_THRESHOLD     pixelmatch threshold 0-1 (default 0.1)
  VISUAL_DIFF_BROWSER       executablePath for a Chromium browser (optional; by
                            default system Chrome then Edge is used)
  VISUAL_DIFF_TOKEN         reuse an existing bearer token (creates no session)
`
}

function parseArgs(argv) {
  const options = {
    prototype: path.join(repoRoot, 'frontend/prototype/overview-hd/总览 · 玻璃磨砂.html'),
    app: '',
    out: path.join(repoRoot, '.scratch/visual-diff'),
    password: '',
    port: 18505,
    layer: '',
  }
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i]
    const next = () => {
      const value = argv[++i]
      if (value === undefined) throw new Error(`${arg} needs a value`)
      return value
    }
    switch (arg) {
      case '--prototype': options.prototype = path.resolve(next()); break
      case '--app': options.app = next(); break
      case '--out': options.out = path.resolve(next()); break
      case '--password': options.password = next(); break
      case '--port': options.port = Number(next()); break
      case '--layer': options.layer = next(); break
      case '--help': case '-h': console.log(usage()); process.exit(0); break
      default: throw new Error(`unknown option ${arg}\n\n${usage()}`)
    }
  }
  if (!Number.isInteger(options.port) || options.port <= 0) throw new Error('--port must be a positive integer')
  if (!options.app) options.app = `http://127.0.0.1:${options.port}/uniapp/#/pages/overview/index`
  return options
}

function loadModule(name) {
  const roots = [
    ...String(process.env.VISUAL_DIFF_MODULE_ROOTS || '').split(path.delimiter).filter(Boolean),
    path.join(repoRoot, 'frontend-uniapp/node_modules'),
    path.join(repoRoot, 'frontend/node_modules'),
    path.join(repoRoot, 'node_modules'),
  ]
  const failures = []
  for (const root of roots) {
    try {
      const req = createRequire(path.join(root, 'package.json'))
      return req(req.resolve(name))
    } catch (error) {
      failures.push(`${root}: ${error.code || error.message}`)
    }
  }
  throw new Error(`cannot resolve "${name}". Looked in:\n  ${failures.join('\n  ')}\n` +
    'Install frontend-uniapp deps, or point VISUAL_DIFF_MODULE_ROOTS at a node_modules directory.')
}

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms))
const round = (value, digits = 6) => Number(value.toFixed(digits))
const pct = (ratio) => round(ratio * 100, 3)
const escapeRegExp = (value) => value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')

function uniappRoot(appUrl) {
  const url = new URL(appUrl)
  const index = url.pathname.indexOf('/uniapp')
  if (index >= 0) return `${url.origin}${url.pathname.slice(0, index)}/uniapp`
  return `${url.origin}${url.pathname.replace(/\/[^/]*$/, '')}`
}

async function waitForHealth(baseUrl, timeoutMs = 30000) {
  const deadline = Date.now() + timeoutMs
  let lastError = ''
  while (Date.now() < deadline) {
    try {
      const response = await fetch(`${baseUrl}/api/health`)
      if (response.ok) return
      lastError = `HTTP ${response.status}`
    } catch (error) {
      lastError = error.message
    }
    await sleep(500)
  }
  throw new Error(`backend not reachable at ${baseUrl}/api/health (${lastError}).\n` +
    `Start a seeded demo first, e.g. bash scripts/prototype-parity.sh ${new URL(baseUrl).port || options.port}`)
}

function candidateLogins(explicit) {
  const candidates = []
  const add = (password, source) => {
    if (password && !candidates.some((entry) => entry.password === password)) candidates.push({ password, source })
  }
  if (explicit) {
    add(explicit, 'explicit')
  } else {
    const fromEnv = [
      ['VISUAL_DIFF_PASSWORD', process.env.VISUAL_DIFF_PASSWORD],
      ['FASTEMS_PARITY_PASSWORD', process.env.FASTEMS_PARITY_PASSWORD],
      ['FASTEMS_ADMIN_PASSWORD', process.env.FASTEMS_ADMIN_PASSWORD],
    ].find(([, value]) => value)
    if (fromEnv) add(fromEnv[1], `env:${fromEnv[0]}`)
    for (const password of DEMO_PASSWORDS) add(password, 'demo-detected')
  }
  return candidates
}

// Log in once through the API and reuse that token in the browser, so a run
// creates one session rather than two. The demo backend caps concurrent
// sessions (5 per account by default) and leaves them open for 30 idle minutes;
// set VISUAL_DIFF_TOKEN to reuse an existing one and create none.
async function obtainSession(baseUrl, explicit) {
  if (process.env.VISUAL_DIFF_TOKEN) {
    return { password: explicit || '', token: process.env.VISUAL_DIFF_TOKEN, source: 'env:VISUAL_DIFF_TOKEN' }
  }
  const candidates = candidateLogins(explicit)
  let limitReached = false
  for (const candidate of candidates) {
    let response
    try {
      response = await fetch(`${baseUrl}/api/login`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ email: 'admin', password: candidate.password }),
      })
    } catch {
      continue
    }
    if (response.ok) {
      const body = await response.json()
      if (body.token) return { ...candidate, token: body.token }
      continue
    }
    if (response.status === 409) limitReached = true
  }
  if (limitReached) {
    throw new Error('login was rejected with session_limit_reached: the demo already holds its ' +
      'maximum concurrent sessions (5 per account by default, 30 idle minutes). Restart the demo, ' +
      'set FASTEMS_MAX_SESSIONS higher, pass VISUAL_DIFF_TOKEN to reuse an existing session, or wait for idle expiry.')
  }
  return { password: candidates[0]?.password || '', token: '', source: 'fallback' }
}

async function launchBrowser() {
  const errors = []
  if (process.env.VISUAL_DIFF_BROWSER) {
    return chromium.launch({ executablePath: process.env.VISUAL_DIFF_BROWSER, headless: true, timeout: 30000 })
  }
  for (const channel of ['chrome', 'msedge']) {
    try {
      return await chromium.launch({ channel, headless: true, timeout: 30000 })
    } catch (error) {
      errors.push(`${channel}: ${error.message.split('\n')[0]}`)
    }
  }
  throw new Error(`no Chromium browser could be launched (${errors.join('; ')}). ` +
    'Install Chrome or Edge, or set VISUAL_DIFF_BROWSER to an executable.')
}

async function newContext(browser) {
  return browser.newContext({
    viewport: VIEWPORT,
    deviceScaleFactor: DEVICE_SCALE,
    reducedMotion: 'reduce',
    colorScheme: 'light',
  })
}

// Freeze motion and caret before screenshotting. `page.screenshot` also passes
// animations:"disabled", which resets CSS animations to their end state.
async function freezeMotion(page) {
  await page.addStyleTag({
    content: '*,*::before,*::after{animation:none!important;transition:none!important;caret-color:transparent!important}html{scroll-behavior:auto!important}',
  }).catch(() => {})
  await page.evaluate(() => (document.fonts ? document.fonts.ready : null)).catch(() => {})
}

async function screenshot(page, file) {
  await page.screenshot({ path: file, type: 'png', animations: 'disabled', caret: 'hide', scale: 'css' })
}

// Read the key computed styles and region rectangles from one side. The
// selector sets differ between the prototype and the H5 build, so the spec is
// passed in from Node instead of hard-coded here.
async function readStyles(page, spec) {
  return page.evaluate((spec) => {
    const pick = (selector) => { try { return document.querySelector(selector) } catch { return null } }
    const rectOf = (element) => {
      if (!element) return null
      const rect = element.getBoundingClientRect()
      return { x: Math.round(rect.x), y: Math.round(rect.y), width: Math.round(rect.width), height: Math.round(rect.height) }
    }
    const panelOf = (element) => {
      if (!element) return null
      const cs = getComputedStyle(element)
      return {
        backgroundColor: cs.backgroundColor,
        backgroundImage: cs.backgroundImage,
        backdropFilter: cs.backdropFilter || cs.webkitBackdropFilter || 'none',
        border: cs.border,
        borderRadius: cs.borderRadius,
        boxShadow: cs.boxShadow,
      }
    }
    const result = { panels: {}, rects: {}, metricCard: null, heroTitle: null }
    for (const [key, selector] of Object.entries(spec.panels)) result.panels[key] = panelOf(pick(selector))
    for (const [key, selector] of Object.entries(spec.rects)) result.rects[key] = rectOf(pick(selector))
    const card = pick(spec.metric)
    if (card) {
      const cs = getComputedStyle(card)
      const value = pick(spec.metricValue)
      result.metricCard = {
        rect: rectOf(card),
        backgroundColor: cs.backgroundColor,
        border: cs.border,
        borderRadius: cs.borderRadius,
        padding: cs.padding,
        boxShadow: cs.boxShadow,
        valueFontSize: value ? getComputedStyle(value).fontSize : null,
        valueColor: value ? getComputedStyle(value).color : null,
      }
    }
    const title = pick(spec.title)
    if (title) {
      const cs = getComputedStyle(title)
      result.heroTitle = {
        text: (title.textContent || '').trim(),
        fontSize: cs.fontSize,
        fontWeight: cs.fontWeight,
        lineHeight: cs.lineHeight,
        fontFamily: cs.fontFamily,
        color: cs.color,
      }
    }
    return result
  }, spec)
}

const PROTOTYPE_SPEC = {
  panels: { hero: '.hero', daily: '.daily', rail: 'aside.side', ledger: 'nav.index' },
  rects: { hero: '.hero', main: 'main.stage', rail: 'aside.side' },
  metric: '.hero .metrics .metric',
  metricValue: '.hero .metrics .metric .v',
  title: '.hero h1',
}
const APP_SPEC = {
  panels: { hero: '.hero', daily: '.daily', rail: 'aside.side', ledger: 'aside.ledger' },
  rects: { hero: '.hero', main: 'main.stage', rail: 'aside.side' },
  metric: '.hero .metrics .metric',
  metricValue: '.hero .metrics .metric .v',
  title: '.hero h1',
}

async function capturePrototype(context, file) {
  const page = await context.newPage()
  page.setDefaultTimeout(60000)
  await page.goto(pathToFileURL(file).href, { waitUntil: 'load' })
  await page.locator('.hero h1').waitFor({ timeout: 30000 })
  // The prototype builds its metric cards and 24 hourly bars from inline JS.
  await page.waitForFunction(() => document.querySelectorAll('.hero .metrics .metric').length > 0, null, { timeout: 15000 })
  await freezeMotion(page)
  await sleep(400)
  const styles = await readStyles(page, PROTOTYPE_SPEC)
  const screenshotPath = path.join(options.out, 'prototype.png')
  await screenshot(page, screenshotPath)
  await page.close()
  return { styles, title: styles.heroTitle?.text || '', screenshotPath }
}

async function captureApp(context, appUrl, login, layerOverride) {
  const page = await context.newPage()
  page.setDefaultTimeout(60000)
  const pageErrors = []
  page.on('pageerror', (error) => pageErrors.push(error.message))
  const base = uniappRoot(appUrl)
  let enteredBy = 'form'
  if (login.token) {
    // Strings are stored raw by uni.setStorageSync; the app reads fastems-token
    // at module init, so set it before the first page script runs.
    await page.addInitScript((token) => { try { localStorage.setItem('fastems-token', token) } catch { /* storage blocked */ } }, login.token)
    await page.goto(appUrl, { waitUntil: 'domcontentloaded' })
    try {
      await page.locator('.hero h1').waitFor({ timeout: 20000 })
      enteredBy = 'token'
    } catch {
      await page.goto(`${base}/#/pages/login/index`, { waitUntil: 'domcontentloaded' })
    }
  }
  if (enteredBy !== 'token') {
    if (!page.url().includes('/pages/login/index')) {
      await page.goto(`${base}/#/pages/login/index`, { waitUntil: 'domcontentloaded' })
    }
    await page.locator('.login input[type="text"]').fill('admin')
    await page.locator('.login input[type="password"]').fill(login.password)
    await page.keyboard.press('Enter')
  }
  try {
    await page.locator('.hero h1').waitFor({ timeout: 120000 })
  } catch (error) {
    const visible = await page.locator('body').innerText().catch(() => '')
    throw new Error(`overview did not open after login (${error.message}). Visible text: ${visible.slice(0, 500)}`)
  }

  // pumphouse answers /api/health before seeding finishes. The seeded screen is
  // readable once the teaching ledger carries the 水箱 layer and the hero has
  // its metric cards; reload while waiting, then reopen the target layer.
  const seedDeadline = Date.now() + 120000
  for (;;) {
    const tank = await page.locator('.ledger .node').filter({ hasText: '水箱' }).count().catch(() => 0)
    const metrics = await page.locator('.hero .metrics .metric').count().catch(() => 0)
    if (tank >= 1 && metrics >= 1) break
    if (Date.now() > seedDeadline) throw new Error('the seeded teaching project never became readable')
    await sleep(1500)
    await page.reload({ waitUntil: 'domcontentloaded' })
    await page.locator('.ledger .node').first().waitFor({ timeout: 60000 }).catch(() => {})
    await page.locator('.hero h1').waitFor({ timeout: 60000 }).catch(() => {})
  }

  // Both sides should show the same layer. Default to the prototype's own h1 so
  // the comparison is not conflating different screens; --layer overrides it.
  const target = layerOverride || ''
  if (target) {
    const row = page.locator('.ledger .node').filter({ hasText: new RegExp(`·\\s*${escapeRegExp(target)}`) }).first()
    if (await row.count().catch(() => 0)) {
      await row.click().catch(() => {})
      await page.waitForFunction(
        (name) => (document.querySelector('.hero h1')?.textContent || '').trim() === name,
        target, { timeout: 15000 },
      ).catch(() => {})
    }
  }

  await freezeMotion(page)
  // The 24-hour strip needs one history round-trip; wait for it when present.
  await page.waitForFunction(() => {
    const daily = document.querySelector('.daily')
    return !daily || document.querySelectorAll('.daily .hour-bar').length === 24
  }, null, { timeout: 10000 }).catch(() => {})
  await sleep(800)
  const styles = await readStyles(page, APP_SPEC)
  const screenshotPath = path.join(options.out, 'app.png')
  await screenshot(page, screenshotPath)
  const layer = await page.locator('.hero h1').innerText().catch(() => '')
  await page.close()
  return { styles, layer: layer.trim(), screenshotPath, pageErrors, enteredBy }
}

function loadPng(file) {
  return PNG.sync.read(readFileSync(file))
}

function cropTo(image, width, height) {
  if (image.width === width && image.height === height) return image.data
  const data = Buffer.alloc(width * height * 4)
  for (let y = 0; y < height; y += 1) {
    image.data.copy(data, y * width * 4, y * image.width * 4, (y * image.width + width) * 4)
  }
  return data
}

function isRed(data, offset) {
  return data[offset] === 255 && data[offset + 1] === 0 && data[offset + 2] === 0
}

function countMask(mask, width, height, rect) {
  const x0 = Math.max(0, Math.min(width, Math.floor(rect.x)))
  const y0 = Math.max(0, Math.min(height, Math.floor(rect.y)))
  const x1 = Math.max(x0, Math.min(width, Math.ceil(rect.x + rect.width)))
  const y1 = Math.max(y0, Math.min(height, Math.ceil(rect.y + rect.height)))
  let mismatched = 0
  for (let y = y0; y < y1; y += 1) {
    for (let x = x0; x < x1; x += 1) mismatched += mask[y * width + x]
  }
  const total = (x1 - x0) * (y1 - y0)
  return { total, mismatched, ratio: total ? mismatched / total : 0 }
}

function bandStats(mask, width, height, xRange) {
  const [start, end] = xRange
  const x0 = Math.max(0, Math.min(width, start))
  const x1 = Math.max(x0, Math.min(width, end))
  return countMask(mask, width, height, { x: x0, y: 0, width: x1 - x0, height })
}

function blit(source, destination, destinationWidth, offsetX, offsetY) {
  for (let y = 0; y < source.height; y += 1) {
    const from = y * source.width * 4
    const to = ((offsetY + y) * destinationWidth + offsetX) * 4
    source.data.copy(destination.data, to, from, from + source.width * 4)
  }
}

function writeSideBySide(left, right, file) {
  const gap = 4
  const width = left.width + right.width + gap
  const height = Math.max(left.height, right.height)
  const output = new PNG({ width, height })
  for (let i = 0; i < output.data.length; i += 4) {
    output.data[i] = 32; output.data[i + 1] = 36; output.data[i + 2] = 40; output.data[i + 3] = 255
  }
  blit(left, output, width, 0, 0)
  blit(right, output, width, left.width + gap, 0)
  writeFileSync(file, PNG.sync.write(output))
}

function diffImages(prototypePath, appPath) {
  const prototypeImage = loadPng(prototypePath)
  const appImage = loadPng(appPath)
  const width = Math.min(prototypeImage.width, appImage.width)
  const height = Math.min(prototypeImage.height, appImage.height)
  const prototypeData = cropTo(prototypeImage, width, height)
  const appData = cropTo(appImage, width, height)
  const diff = new PNG({ width, height })
  const threshold = Number(process.env.VISUAL_DIFF_THRESHOLD || 0.1)
  const mismatched = pixelmatch(prototypeData, appData, diff.data, width, height, { threshold, includeAA: false })
  writeFileSync(path.join(options.out, 'diff.png'), PNG.sync.write(diff))
  // pixelmatch paints each counted difference pure red, so the mask is exact.
  const mask = new Uint8Array(width * height)
  let redCount = 0
  for (let i = 0; i < width * height; i += 1) {
    if (isRed(diff.data, i * 4)) { mask[i] = 1; redCount += 1 }
  }
  return {
    width, height, mismatched, redCount, mask, threshold,
    prototypeSize: { width: prototypeImage.width, height: prototypeImage.height },
    appSize: { width: appImage.width, height: appImage.height },
  }
}

function regionStats(mask, width, height, rect) {
  if (!rect || rect.width <= 0 || rect.height <= 0) return null
  const counted = countMask(mask, width, height, rect)
  return {
    selectorRect: rect,
    totalPixels: counted.total,
    mismatchedPixels: counted.mismatched,
    mismatchRatio: round(counted.ratio),
    percent: pct(counted.ratio),
  }
}

async function main() {
  const playwright = loadModule('playwright-core')
  chromium = playwright.chromium
  pixelmatch = loadModule('pixelmatch')
  PNG = loadModule('pngjs').PNG
  mkdirSync(options.out, { recursive: true })
  const baseUrl = new URL(options.app).origin
  await waitForHealth(baseUrl)
  const login = await obtainSession(baseUrl, options.password)
  const browser = await launchBrowser()
  try {
    const context = await newContext(browser)
    const prototype = await capturePrototype(context, options.prototype)
    const app = await captureApp(context, options.app, login, options.layer || prototype.title)
    await context.close()

    const diff = diffImages(prototype.screenshotPath, app.screenshotPath)
    const totalPixels = diff.width * diff.height
    const overallRatio = diff.mismatched / totalPixels
    const sideBySidePath = path.join(options.out, 'side-by-side.png')
    writeSideBySide(loadPng(prototype.screenshotPath), loadPng(app.screenshotPath), sideBySidePath)

    const bands = {}
    for (const band of BANDS) {
      const stats = bandStats(diff.mask, diff.width, diff.height, band.xRange)
      bands[band.key] = {
        xRange: band.xRange,
        totalPixels: stats.total,
        mismatchedPixels: stats.mismatched,
        mismatchRatio: round(stats.ratio),
        percent: pct(stats.ratio),
      }
    }
    // Regions are the intended geometry from the prototype; the app's own rects
    // are recorded alongside for reference. hero is a subset of main.
    const regions = {}
    for (const key of ['hero', 'main', 'rail']) {
      const rect = prototype.styles.rects[key] || app.styles.rects[key]
      regions[key] = regionStats(diff.mask, diff.width, diff.height, rect)
    }

    const metrics = {
      generatedAt: new Date().toISOString(),
      tool: 'scripts/visual-diff.mjs',
      viewport: { ...VIEWPORT, deviceScaleFactor: DEVICE_SCALE },
      pixelmatch: { threshold: diff.threshold, includeAA: false },
      inputs: {
        prototype: { path: options.prototype, url: pathToFileURL(options.prototype).href },
        app: { url: options.app, origin: baseUrl, layer: app.layer, passwordSource: login.source, loginMethod: app.enteredBy, pageErrors: app.pageErrors },
      },
      images: {
        comparedWidth: diff.width,
        comparedHeight: diff.height,
        prototype: diff.prototypeSize,
        app: diff.appSize,
      },
      overall: {
        totalPixels,
        mismatchedPixels: diff.mismatched,
        mismatchRatio: round(overallRatio),
        percent: pct(overallRatio),
      },
      bands,
      regions,
      computedStyles: { prototype: prototype.styles, app: app.styles },
      artifacts: {
        prototype: prototype.screenshotPath,
        app: app.screenshotPath,
        diff: path.join(options.out, 'diff.png'),
        sideBySide: sideBySidePath,
      },
    }
    const metricsPath = path.join(options.out, 'metrics.json')
    writeFileSync(metricsPath, `${JSON.stringify(metrics, null, 2)}\n`)
    console.log(JSON.stringify({
      output: options.out,
      overallPercent: metrics.overall.percent,
      bands: Object.fromEntries(Object.entries(bands).map(([key, value]) => [key, value.percent])),
      regions: Object.fromEntries(Object.entries(regions).map(([key, value]) => [key, value?.percent ?? null])),
      prototypeTitle: prototype.title,
      appLayer: app.layer,
    }, null, 2))
  } finally {
    await browser.close()
  }
}

let parsed
try {
  parsed = parseArgs(process.argv.slice(2))
} catch (error) {
  console.error(error.message)
  process.exit(2)
}
options = parsed
main().catch((error) => {
  console.error(error.stack || error.message)
  process.exit(1)
})
