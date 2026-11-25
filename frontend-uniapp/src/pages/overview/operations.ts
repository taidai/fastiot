import { request } from "../../api/client"
import type { LedgerDevice } from "./overview"

export type UnitParameter = {
  name: string
  label: string
  point_id: string
  point_name: string
  unit?: string
  kind?: string
  has_range?: boolean
  min: number
  max: number
  default: number
  available: boolean
  reason?: string
}

export type UnitCommand = {
  id: string
  name: string
  point_id?: string
  point_name: string
  value: number
  readback_point_id?: string
  readback_point_name?: string
  readback_value?: number
  timeout_ms?: number
  parameters?: UnitParameter[]
  available: boolean
  reason?: string
}

export type UnitModel = {
  channel_id: string
  server_id: number
  model?: {
    name: string
    profile_id: string
    business_node_id?: string
    point_map: Record<string, string>
    verified: boolean
    verifier?: string
    verified_at?: string
    device_serial?: string
  }
  points: import("./overview").UnitPoint[]
  commands: UnitCommand[]
  profile_errors?: string[]
  missing?: string[]
}

export type OperationRecord = {
  request_id: string
  device_id: string
  command_id: string
  actor: string
  state: string
  reason?: string
  started_at: string
  updated_at: string
}

export type ManualWriteResult = {
  request_id: string
  status: string
  target: number
  actor: string
  created_at: string
  reason?: string
}

export function unitBase(device: Pick<LedgerDevice, "channel_id" | "server_id">) {
  return `/modbus/${encodeURIComponent(device.channel_id)}/units/${device.server_id}`
}

export async function loadUnitModel(device: LedgerDevice) {
  return request<UnitModel>(`${unitBase(device)}/model`)
}

export async function loadOperationRecords(device: LedgerDevice) {
  const result = await request<{ commands: OperationRecord[] }>(`${unitBase(device)}/commands`)
  return result.commands || []
}

export async function submitUnitCommand(device: LedgerDevice, commandID: string, parameters: Record<string, number>) {
  const requestID = crypto.randomUUID()
  const outcome = await request<OperationRecord>(`${unitBase(device)}/commands`, "POST", {
    request_id: requestID,
    command_id: commandID,
    parameters,
  })
  return outcome
}

export async function loadUnitCommand(device: LedgerDevice, requestID: string) {
  return request<OperationRecord>(`${unitBase(device)}/commands/${encodeURIComponent(requestID)}`)
}

export async function writeUnitPoint(pointID: string, value: number) {
  return request<ManualWriteResult>(`/nodes/${encodeURIComponent(pointID)}/value`, "POST", {
    value,
    point_type: "valueSet",
    request_id: crypto.randomUUID(),
  })
}
