import { Actions, reasonText } from "./capabilities"
import { useCapabilities } from "./capabilitiesContext"

type CommandTarget = { name: string; available: boolean; reason?: string }

export default function CommandExecuteButton({ command, busy = false, label = "执行", className = "primary-button", onClick }: {
  command: CommandTarget
  busy?: boolean
  label?: string
  className?: string
  onClick: () => void
}) {
  const { verdict } = useCapabilities()
  const execution = verdict(Actions.command, { serverAvailable: command.available })
  const reason = execution.allowed ? "" : [reasonText(execution.reason), command.reason].filter(Boolean).join("：")
  const aria = reason ? `${label} ${command.name}：${reason}` : `${label} ${command.name}`
  return <button className={className} disabled={busy || !execution.allowed} title={reason || undefined} aria-label={aria} onClick={onClick}>{label}</button>
}
