import type { Node } from "../../types/api"

export type MemberRole = { role: string; node_id?: string; required: boolean }

export function memberRolesOf(node: Node | null | undefined): MemberRole[] {
  const text = node?.points.find(point => point.type === "easyemsUnitMembers" && (point.tombstone || 0) % 2 === 0)?.text
  if (!text) return []
  try {
    const roles = JSON.parse(text) as MemberRole[]
    return Array.isArray(roles) ? roles.map(role => ({ role: String(role.role || ""), node_id: role.node_id || "", required: !!role.required })) : []
  } catch {
    return []
  }
}

export function missingRequiredMembers(members: MemberRole[]) {
  return members.filter(member => member.required && !member.node_id?.trim()).map(member => member.role)
}
