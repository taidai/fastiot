// Exercises the trusted preview bridge (internal/genscreen/runtime.js) in a
// minimal DOM: bindings, simulated values, write requests, error capture and
// layout checks. The bridge runs in a sandboxed frame in production, so this
// check is the only place its message handling is executed without a browser.
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import vm from "node:vm"

const source = readFileSync(new URL("../internal/genscreen/runtime.js", import.meta.url), "utf8")

class FakeElement {
  constructor(tag, attrs = {}) {
    this.nodeType = 1
    this.tagName = tag.toUpperCase()
    this.attrs = { ...attrs }
    this.id = attrs.id || ""
    this.style = {}
    this.textContent = ""
    this.listeners = {}
    this.parentNode = null
    this.parentElement = null
    this.rect = { left: 0, top: 0, right: 100, bottom: 40, width: 100, height: 40 }
    this.scrollWidth = 100
    this.clientWidth = 100
    this.scrollHeight = 40
    this.clientHeight = 40
  }
  getAttribute(name) { return Object.prototype.hasOwnProperty.call(this.attrs, name) ? String(this.attrs[name]) : null }
  setAttribute(name, value) { this.attrs[name] = String(value) }
  removeAttribute(name) { delete this.attrs[name] }
  addEventListener(type, handler) { (this.listeners[type] ||= []).push(handler) }
  dispatchEvent(event) { (this.listeners[event.type] || []).forEach((handler) => handler(event)); return true }
  click() { (this.listeners.click || []).forEach((handler) => handler({ currentTarget: this })) }
  getBoundingClientRect() { return this.rect }
  scrollIntoView() {}
  get previousElementSibling() { return null }
}

function makeDocument(elements, root) {
  function matches(element, selector) {
    const match = selector.trim().match(/^\[([a-z-]+)(?:="([^"]*)")?\]$/)
    if (!match) throw new Error(`unsupported selector ${selector}`)
    const value = element.getAttribute(match[1])
    return value !== null && (match[2] === undefined || value === match[2])
  }
  return {
    documentElement: { tagName: "HTML", clientWidth: 1000, scrollWidth: 1000 },
    body: root,
    listeners: {},
    querySelectorAll(selector) {
      const selectors = selector.split(",")
      return elements.filter((element) => selectors.some((item) => matches(element, item)))
    },
    querySelector(selector) {
      return elements.find((element) => matches(element, selector)) || null
    },
    addEventListener(type, handler) { (this.listeners[type] ||= []).push(handler) },
    dispatchEvent(event) { (this.listeners[event.type] || []).forEach((handler) => handler(event)); return true },
  }
}

function boot({ marker, levelRect } = {}) {
  const root = new FakeElement("div")
  const level = new FakeElement("span", { "data-fastiot-point": "level", "data-fastiot-format": "number", "data-fastiot-decimals": "1", "data-fastiot-el": marker || "e5" })
  if (levelRect) level.rect = levelRect
  const pump = new FakeElement("span", { "data-fastiot-point": "pump", "data-fastiot-format": "bool", "data-fastiot-labels": "运行,停止", "data-fastiot-el": "e6" })
  const start = new FakeElement("button", { "data-fastiot-write": "pump", "data-fastiot-value": "1", "data-fastiot-el": "e7" })
  const elements = [root, level, pump, start]
  for (const element of elements) { element.parentNode = root; element.parentElement = root }
  root.parentNode = null
  root.parentElement = null

  const messages = []
  const parent = { postMessage: (message) => messages.push(message) }
  const timers = []
  const windowListeners = {}
  const windowObject = {
    parent,
    addEventListener(type, handler) { (windowListeners[type] ||= []).push(handler) },
    setTimeout(handler) { timers.push(handler); return timers.length },
    clearTimeout() {},
    getComputedStyle: () => ({ display: "block", visibility: "visible" }),
  }
  const document = makeDocument(elements, root)
  const context = {
    window: windowObject,
    document,
    CustomEvent: class { constructor(type, init) { this.type = type; this.detail = init?.detail } },
    console,
  }
  vm.runInNewContext(source, context)

  function receive(data, from = parent) {
    ;(windowListeners.message || []).forEach((handler) => handler({ source: from, data }))
  }
  function flushTimers() { const pending = timers.splice(0); pending.forEach((handler) => handler()) }

  return { messages, receive, flushTimers, level, pump, start, windowListeners }
}

const bridge = boot()

// Nothing is sent before the host initialises the frame.
assert.equal(bridge.messages.length, 0)
bridge.receive({ type: "fastiot:init", token: "load-1" })
const ready = bridge.messages.find((message) => message.type === "fastiot:ready")
assert.ok(ready, "ready message missing")
assert.equal(ready.token, "load-1")
assert.equal([...ready.bindings].sort().join(","), "level,pump")
assert.equal(ready.findings.length, 0)

// Values render with the declared format; the number uses one decimal.
bridge.receive({ type: "fastiot:values", token: "load-1", values: {
  level: { value: 42.36, quality: "good", time: "2026-11-20T00:00:00Z" },
  pump: { value: 1, quality: "good", time: "2026-11-20T00:00:00Z" },
} })
assert.equal(bridge.level.textContent, "42.4")
assert.equal(bridge.level.attrs["data-fastiot-quality"], "good")
assert.equal(bridge.pump.textContent, "运行")

// A stale load token is ignored; the current one is honoured.
bridge.receive({ type: "fastiot:values", token: "load-0", values: { level: { value: 1 } } })
assert.equal(bridge.level.textContent, "42.4")

// Clicking a write binding asks the host; the host's decision is applied.
bridge.start.click()
const write = bridge.messages.find((message) => message.type === "fastiot:write")
assert.ok(write, "write request missing")
assert.equal(write.binding, "pump")
assert.equal(write.value, 1)
assert.equal(write.element, "e7")
assert.equal(bridge.start.attrs["data-fastiot-state"], "pending")
bridge.receive({ type: "fastiot:write-result", token: "load-1", requestId: write.requestId, status: "simulated", value: 1 })
assert.equal(bridge.start.attrs["data-fastiot-state"], "simulated")

// Runtime errors are reported with their line and column.
;(bridge.windowListeners.error || []).forEach((handler) => handler({ message: "level is not defined", lineno: 12, colno: 4 }))
const failure = bridge.messages.find((message) => message.type === "fastiot:error")
assert.ok(failure, "runtime error not reported")
assert.equal(failure.finding.code, "runtime_error")
assert.equal(failure.finding.line, 12)
assert.equal(failure.finding.column, 4)

// Layout checks locate an invisible binding.
const hidden = boot({ marker: "e9", levelRect: { left: 0, top: 0, right: 0, bottom: 0, width: 0, height: 0 } })
hidden.receive({ type: "fastiot:init", token: "load-2" })
const hiddenFinding = hidden.messages.find((message) => message.type === "fastiot:ready").findings.find((finding) => finding.code === "layout_invisible")
assert.ok(hiddenFinding, "invisible binding not reported")
assert.equal(hiddenFinding.element, "e9")
assert.equal(hiddenFinding.severity, "blocking")
assert.ok(hiddenFinding.selector.includes("span"))

// Locating an element marks it and answers the host.
hidden.receive({ type: "fastiot:locate", token: "load-2", element: "e9" })
assert.equal(hidden.level.style.outline, "3px solid #f97316")
assert.ok(hidden.messages.some((message) => message.type === "fastiot:locate-result" && message.found === true))

// Messages from another window are ignored.
hidden.receive({ type: "fastiot:check", token: "load-2" }, { postMessage() {} })
assert.equal(hidden.messages.filter((message) => message.type === "fastiot:findings").length, 0)

console.log("preview runtime check passed: bindings, simulated values and writes, error capture, layout findings and locate")
