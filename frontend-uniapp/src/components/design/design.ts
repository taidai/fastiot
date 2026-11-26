import type { Node, ScreenConfig, Snapshot } from "../../types/api"

const businessTypes = new Set(["site", "siteRoot", "group", "area", "plant", "energyUnit", "energy_unit", "unit", "device", "modbus_device", "modbus"])

export function kindLabel(node: Node) {
  if (node.type === "site" || node.type === "siteRoot" || node.type === "plant") return "现场"
  if (node.type === "modbus") return "通道"
  if (["energyUnit", "energy_unit", "unit"].includes(node.type)) return "单元"
  if (["device", "modbus_device"].includes(node.type)) return "设备"
  return "区域"
}

export function flattenLedger(snapshot: Snapshot | null) {
  const result: { node: Node; depth: number; kind: string; name: string }[] = []
  if (!snapshot) return result
  const walk = (node: Node, depth: number) => {
    if (!businessTypes.has(node.type)) return
    result.push({ node, depth, kind: kindLabel(node), name: node.points.find(point => point.type === "description" && (point.tombstone || 0) % 2 === 0)?.text || node.id })
    snapshot.nodes.filter(child => child.parent === node.id).forEach(child => walk(child, depth + 1))
  }
  snapshot.roots.forEach(id => {
    const root = snapshot.nodes.find(node => node.id === id)
    if (root) walk(root, 0)
  })
  return result
}

export function summarizeLedger(nodes: Node[]) {
  return {
    areas: nodes.filter(node => kindLabel(node) === "区域" && businessTypes.has(node.type)).length,
    units: nodes.filter(node => kindLabel(node) === "单元" && businessTypes.has(node.type)).length,
    devices: nodes.filter(node => ["device", "modbus_device"].includes(node.type)).length,
    rules: nodes.filter(node => node.type === "decisionTable").length,
  }
}

export function screenSummary(screen: ScreenConfig | null) {
  return screen ? `${screen.title} · ${screen.columns} 列 · ${screen.point_ids.length} 个点位` : "未配置"
}

export function allowedChildTypes(parentType: string, parentIsRoot = false) {
  if (parentType === "siteRoot" || (parentType === "device" && parentIsRoot)) return ["site", "plant", "area", "unit", "energyUnit", "device"]
  if (parentType === "site") return ["plant", "area", "unit", "energyUnit", "device"]
  if (parentType === "plant") return ["area", "unit", "energyUnit", "device"]
  if (parentType === "area" || parentType === "group") return ["area", "unit", "energyUnit", "device"]
  if (parentType === "unit" || parentType === "energyUnit") return ["device"]
  return []
}

export type BusinessBindingState = { bound: boolean; verified: boolean }

export function businessBindingStates(nodes: Node[]) {
  const states = new Map<string, BusinessBindingState>()
  for (const node of nodes) {
    if (node.type !== "modbus") continue
    for (const point of node.points) {
      if (point.type !== "easyemsUnitModel" || (point.tombstone || 0) % 2 !== 0 || !point.text) continue
      try {
        const model = JSON.parse(point.text) as { business_node_id?: string; verified?: boolean }
        if (!model.business_node_id) continue
        const previous = states.get(model.business_node_id)
        states.set(model.business_node_id, {
          bound: true,
          verified: (previous?.verified || false) || model.verified === true,
        })
      } catch { /* Invalid persisted models remain unbound rather than guessed. */ }
    }
  }
  return states
}

export function boundBusinessIDs(nodes: Node[]) {
  return new Set(businessBindingStates(nodes).keys())
}

export function deviceBindingProgress(nodes: Node[]) {
  const progress = { devices: 0, bound: 0, verified: 0 }
  const states = businessBindingStates(nodes)
  for (const node of nodes) {
    if (node.type !== "device" || node.parent === "root") continue
    progress.devices++
    const state = states.get(node.id)
    if (!state) continue
    progress.bound++
    if (state.verified) progress.verified++
  }
  return progress
}

// ADR 0007: an existing project opens on the first unfinished step; a finished
// project opens on delivery instead of restarting at step one.
export function firstIncompleteStep(steps: readonly { key: string; done: boolean }[]) {
  return (steps.find(step => !step.done) || steps[steps.length - 1])?.key || ""
}
