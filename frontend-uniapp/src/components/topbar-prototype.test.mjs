import test from "node:test"
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"

const topbar = readFileSync(new URL("./TopBar.vue", import.meta.url), "utf8")
const delivery = readFileSync(new URL("../pages/delivery/index.vue", import.meta.url), "utf8")
const design = readFileSync(new URL("../pages/design/index.vue", import.meta.url), "utf8")

test("workbench header exposes a real EMS switch and prototype project label", () => {
  assert.match(topbar, /class="ems-toggle"[^>]*role="switch"/)
  assert.match(topbar, /@click="emit\('toggle-ems'\)"/)
  assert.match(topbar, /:aria-checked="displayEmsInstalled === true"/)
  assert.match(topbar, /showEms\?: boolean/)
  assert.match(topbar, /<text v-else>：<\/text>/)
  assert.match(topbar, /class="project-caret">▾<\/text>/)
})

test("delivery connects the EMS switch to the real industry package API", () => {
  assert.match(delivery, /show-ems\s+:ems-installed="emsInstalled"/)
  assert.match(delivery, /@toggle-ems="toggleEms"/)
  assert.match(delivery, /async function toggleEms\(\)/)
  assert.match(delivery, /request\("\/industry\/packages", "POST", \{ id: "ems", version: emsVersion\.value \}\)/)
  assert.match(delivery, /request\("\/industry\/packages\/ems", "DELETE"\)/)
})

test("design connects the same EMS switch without a demo-only toggle", () => {
  assert.match(design, /show-ems\s+:ems-installed="PREVIEW_MODE \? previewWorkspace\.emsEnabled : emsInstalled"/)
  assert.match(design, /@toggle-ems="toggleEms"/)
  assert.match(design, /async function toggleEms\(\)/)
  assert.match(design, /request\("\/industry\/packages", "POST", \{ id: "ems", version: emsVersion\.value \}\)/)
  assert.match(design, /request\("\/industry\/packages\/ems", "DELETE"\)/)
})
