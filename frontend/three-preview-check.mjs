// 3D 画面的浏览器检查：在真实 Chrome 里把随项目打包的 Three.js 放进
// sandbox="allow-scripts" 的 iframe（与服务端相同的 CSP、没有 allow-same-origin），
// 验证 WebGL 真的画出设备、桥接事件把点位值送进场景，并且整个过程不发任何外部请求。
// 运行：node three-preview-check.mjs
import assert from "node:assert/strict"
import { createHash } from "node:crypto"
import { readFileSync } from "node:fs"
import { createServer } from "node:http"
import { chromium } from "playwright-core"

const read = (path) => readFileSync(new URL(path, import.meta.url), "utf8")
const bundle = read("../internal/genscreen/three.min.js")
const runtime = read("../internal/genscreen/runtime.js")
const inlineBundle = bundle.replace(/\r\n/g, " ").replace(/\n/g, " ")
const compactRuntime = runtime.replace(/\r\n/g, " ").replace(/\n/g, " ")

// The policy comes from the Go source of truth, so the check cannot drift from
// what the server actually serves.
function policyFromGo(source) {
  const match = source.match(/const contentSecurityPolicy = "([^"]+)"/)
  if (!match) throw new Error("cannot read contentSecurityPolicy from internal/genscreen/render.go")
  return match[1]
}
const policy = policyFromGo(read("../internal/genscreen/render.go"))
const nonce = createHash("sha256").update(`three-check-${Date.now()}`).digest("hex")

const frame = `<!doctype html>
<html><head><meta charset="utf-8"><title>3D</title>
<script nonce="${nonce}">${compactRuntime}</script>
<script nonce="${nonce}">${inlineBundle}</script>
<style>html,body{margin:0;background:#0b1220}canvas{display:block;width:320px;height:200px}</style>
</head><body>
<canvas id="scene" width="320" height="200"></canvas>
<span id="hud" data-fastiot-point="LEVEL" data-fastiot-format="number" data-fastiot-decimals="1">--</span>
<script nonce="${nonce}">
(function () {
  var canvas = document.getElementById("scene");
  var renderer = new THREE.WebGLRenderer({ canvas: canvas, antialias: false, preserveDrawingBuffer: true });
  renderer.setPixelRatio(1);
  renderer.setSize(320, 200, false);
  var scene = new THREE.Scene();
  scene.background = new THREE.Color(0x0b1220);
  var camera = new THREE.PerspectiveCamera(45, 320 / 200, 0.1, 100);
  camera.position.set(0, 0.6, 4);
  camera.lookAt(0, 0, 0);
  var tank = new THREE.Mesh(new THREE.BoxGeometry(1.6, 1.6, 1.6), new THREE.MeshBasicMaterial({ color: 0x22c55e }));
  scene.add(tank);
  var pump = new THREE.Mesh(new THREE.SphereGeometry(0.4, 16, 12), new THREE.MeshBasicMaterial({ color: 0x38bdf8 }));
  pump.position.set(1.8, 0, 0);
  scene.add(pump);
  scene.add(new THREE.Line(
    new THREE.BufferGeometry().setFromPoints([new THREE.Vector3(-0.8, 0, 0), new THREE.Vector3(1.4, 0, 0)]),
    new THREE.LineBasicMaterial({ color: 0xf59e0b })
  ));
  function report(message) { window.parent.postMessage(message, "*"); }
  var level = 0;
  function render() {
    tank.scale.y = 0.25 + level;
    renderer.render(scene, camera);
  }
  render();
  document.addEventListener("fastiot:values", function (event) {
    var entry = event.detail && event.detail.values ? event.detail.values.LEVEL : null;
    level = entry ? Number(entry.value) / 100 : 0;
    render();
    report({ type: "three:values", level: level });
  });
  var gl = renderer.getContext();
  var pixels = new Uint8Array(4);
  gl.readPixels(160, 100, 1, 1, gl.RGBA, gl.UNSIGNED_BYTE, pixels);
  report({
    type: "three:rendered",
    revision: THREE.REVISION,
    pixels: Array.prototype.slice.call(pixels),
    webgl2: typeof WebGL2RenderingContext !== "undefined" && gl instanceof WebGL2RenderingContext
  });
})();
</script></body></html>`

const host = `<!doctype html><html><body style="margin:0">
<iframe id="screen" sandbox="allow-scripts" src="/frame" width="360" height="240"></iframe>
<script>
  window.__messages = [];
  window.addEventListener("message", function (event) {
    var frame = document.getElementById("screen");
    if (frame && event.source === frame.contentWindow) window.__messages.push(event.data);
  });
</script></body></html>`

const server = createServer((request, response) => {
  if (request.url === "/frame") {
    response.writeHead(200, {
      "Content-Type": "text/html; charset=utf-8",
      "Content-Security-Policy": policy.replace("%s", nonce),
      "Cache-Control": "no-store",
      "X-Content-Type-Options": "nosniff",
    })
    response.end(frame)
    return
  }
  if (request.url === "/") {
    response.writeHead(200, { "Content-Type": "text/html; charset=utf-8" })
    response.end(host)
    return
  }
  response.writeHead(404).end()
})
await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve))
const base = `http://127.0.0.1:${server.address().port}`

let browser
try {
  browser = await chromium.launch({ headless: true })
} catch (error) {
  server.close()
  console.error("three preview check needs Chrome or Edge (playwright-core):", error.message)
  process.exit(2)
}

const blocked = []
try {
  const context = await browser.newContext()
  // Anything that is not this local test server is an external request and
  // must never happen; the preview is supposed to be offline-complete.
  await context.route("**/*", (route) => {
    const url = route.request().url()
    if (url.startsWith(base)) return route.continue()
    blocked.push(url)
    return route.abort()
  })
  const page = await context.newPage()
  const requests = []
  page.on("request", (request) => requests.push(request.url()))
  page.on("console", (message) => {
    if (message.type() === "error") console.error("frame console:", message.text())
  })
  page.on("pageerror", (error) => console.error("frame page error:", error.message))

  await page.goto(`${base}/`, { waitUntil: "load" })
  await page.waitForFunction(() => window.__messages.some((message) => message.type === "three:rendered"))

  // Initialise the trusted bridge and feed a live value through it.
  await page.evaluate(() => document.getElementById("screen").contentWindow.postMessage({ type: "fastiot:init", token: "check-load" }, "*"))
  await page.evaluate(() => document.getElementById("screen").contentWindow.postMessage({
    type: "fastiot:values", token: "check-load", values: { LEVEL: { value: 64, quality: "good", time: new Date().toISOString() } },
  }, "*"))
  await page.waitForFunction(() => window.__messages.some((message) => message.type === "three:values"))

  const messages = await page.evaluate(() => window.__messages)
  const ready = messages.find((message) => message.type === "fastiot:ready")
  assert.ok(ready, "the trusted bridge never reported ready")
  assert.deepEqual([...ready.bindings], ["LEVEL"])
  assert.equal(ready.findings.length, 0, `layout findings: ${JSON.stringify(ready.findings)}`)

  const rendered = messages.find((message) => message.type === "three:rendered")
  assert.equal(rendered.revision, "159", "the frame did not run the pinned Three.js release")
  assert.ok(rendered.webgl2, "the scene did not get a WebGL 2 context")
  assert.ok(rendered.pixels[3] > 0 && rendered.pixels[1] > 60, `the canvas is blank: ${JSON.stringify(rendered.pixels)}`)

  // The sandbox gives the frame an opaque origin: the host cannot read it.
  const opaque = await page.evaluate(() => {
    const frame = document.getElementById("screen")
    try { return frame.contentDocument === null } catch { return true }
  })
  assert.ok(opaque, "the 3D frame must not be same-origin with the platform page")

  const first = messages.find((message) => message.type === "three:values")
  assert.equal(first.level, 0.64)

  // Cut the network entirely; the loaded screen still interacts.
  await context.setOffline(true)
  await page.evaluate(() => document.getElementById("screen").contentWindow.postMessage({
    type: "fastiot:values", token: "check-load", values: { LEVEL: { value: 18, quality: "good", time: new Date().toISOString() } },
  }, "*"))
  await page.waitForFunction(() => window.__messages.filter((message) => message.type === "three:values").length >= 2)
  const second = (await page.evaluate(() => window.__messages)).filter((message) => message.type === "three:values").at(-1)
  assert.equal(second.level, 0.18, "the scene stopped reacting when the network went away")
  await context.setOffline(false)

  assert.deepEqual(blocked, [], `external requests were attempted: ${blocked.join(", ")}`)
  const external = requests.filter((url) => !url.startsWith(base))
  assert.deepEqual(external, [], `the frame requested an external URL: ${external.join(", ")}`)

  console.log("three preview check passed: WebGL in a sandboxed frame, bridge values, zero external requests")
} finally {
  await browser.close()
  server.close()
}
