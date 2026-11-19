import { useState } from "react"
import type { MouseEvent } from "react"
import { EmsButton, InlineNotice } from "./components/ems"
import { request } from "./api"

type Region = { x: number; y: number; note: string }
type Shot = { name: string; media_type: string; data: string; annotation: string; reviewed: boolean; regions: Region[] }
type Finding = {
  id: string
  category: string
  item: number
  field: string
  kind: string
  position: number
  line: number
  preview: string
  blocking: boolean
}
type Manifest = {
  categories: { key: string; label: string; summary: string }[]
  mode?: string
  code: { instruction: string; context?: string }
  device_model: { id?: string; name: string; type: string; unit?: string }[]
  screenshots: { name: string; media_type: string; bytes: number; width?: number; height?: number; annotation?: string; regions?: Region[]; reviewed: boolean; sends_pixels: boolean; invalid?: boolean }[]
  findings: Finding[]
  block_reasons?: string[]
  blocked: boolean
}
type Change = { kind: string; selector: string; element?: string; line?: number; detail?: string }
type PreviewFinding = {
  code: string
  origin: string
  severity: string
  message: string
  binding?: string
  element?: string
  selector?: string
  line?: number
  column?: number
  detail?: Record<string, number>
}
type PreviewReport = { findings: PreviewFinding[]; ready: boolean; blocking: number; warnings: number }
type Applied = { revision: number; frame_url: string; changes: Change[]; report: PreviewReport }

const reasonText: Record<string, string> = {
  instruction_required: "请先填写修改要求",
  sensitive_content: "检测到疑似敏感的现场信息",
  screenshot_invalid: "截图数据无法读取",
  screenshot_annotation_invalid: "标注区域无效（坐标需在图片内，且每处都要填写说明）",
  screenshot_unreviewed: "截图尚未确认",
}
const kindText: Record<string, string> = {
  ip_address: "IP 地址",
  phone: "电话",
  account: "账号",
  credential: "凭据",
  serial: "序列号",
  address: "地址",
}
const fieldText: Record<string, string> = {
  instruction: "修改要求",
  context: "现有画面",
  name: "名称",
  unit: "单位",
  annotation: "标注文字",
  region: "标注区域",
  metadata: "图片文字信息",
}
const categoryText: Record<string, string> = { code: "代码与上下文", device_model: "设备模型", screenshot: "截图" }
const changeText: Record<string, string> = {
  added: "新增元素",
  removed: "删除元素",
  attribute: "属性变化",
  text: "文字变化",
  style: "样式变化",
  script: "脚本变化",
}

function readFile(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result || ""))
    reader.onerror = () => reject(reader.error || new Error("读取失败"))
    reader.readAsDataURL(file)
  })
}

// ScreenRevision continues the generation session: a follow-up instruction or
// an annotated screenshot modifies the screen already in the isolated preview.
// Annotations stay local as coordinates plus text; pixels are never sent.
export default function ScreenRevision({ token, sessionID, revision, onApplied, onLocate }: {
  token: string
  sessionID: string
  revision: number
  onApplied: (applied: Applied) => void
  onLocate: (element: string) => void
}) {
  const [instruction, setInstruction] = useState("")
  const [shots, setShots] = useState<Shot[]>([])
  const [acknowledged, setAcknowledged] = useState<string[]>([])
  const [preview, setPreview] = useState<Manifest | null>(null)
  const [confirmed, setConfirmed] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [changes, setChanges] = useState<Change[]>([])
  const [appliedAt, setAppliedAt] = useState(0)

  const annotated = shots.every((shot) => shot.regions.some((region) => region.note.trim() !== ""))
  const canPreview = instruction.trim() !== "" && annotated

  function body(ack: string[], withConfirm: boolean) {
    return {
      instruction,
      screenshots: shots.map((shot) => ({
        name: shot.name, media_type: shot.media_type, data: shot.data,
        annotation: shot.annotation, reviewed: shot.reviewed, regions: shot.regions,
      })),
      acknowledged: ack,
      ...(withConfirm ? { confirmed: true } : {}),
    }
  }

  async function runPreview(ack = acknowledged) {
    setBusy(true)
    setError("")
    try {
      const result = await request<{ manifest: Manifest }>(token, `/generated-screens/${sessionID}/revision/preview`, "POST", body(ack, false))
      setPreview(result.manifest)
      setConfirmed(false)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "预览失败")
    } finally {
      setBusy(false)
    }
  }

  async function apply() {
    setBusy(true)
    setError("")
    try {
      const result = await request<Applied>(token, `/generated-screens/${sessionID}/revision`, "POST", body(acknowledged, true))
      setChanges(result.changes || [])
      setAppliedAt(result.revision)
      setPreview(null)
      setConfirmed(false)
      setInstruction("")
      setShots([])
      onApplied(result)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "修改失败")
    } finally {
      setBusy(false)
    }
  }

  async function addFiles(files: FileList | null) {
    if (!files) return
    const next: Shot[] = []
    for (const file of Array.from(files)) {
      try {
        next.push({ name: file.name, media_type: file.type || "image/png", data: await readFile(file), annotation: "", reviewed: false, regions: [] })
      } catch {
        setError(`无法读取截图 ${file.name}`)
      }
    }
    setShots((current) => [...current, ...next])
    setPreview(null)
  }

  function updateShot(index: number, patch: Partial<Shot>) {
    setShots((current) => current.map((shot, at) => at === index ? { ...shot, ...patch } : shot))
    setPreview(null)
  }

  function addRegion(index: number, event: MouseEvent<HTMLImageElement>) {
    const rect = event.currentTarget.getBoundingClientRect()
    if (rect.width === 0 || rect.height === 0) return
    const x = Math.min(1, Math.max(0, (event.clientX - rect.left) / rect.width))
    const y = Math.min(1, Math.max(0, (event.clientY - rect.top) / rect.height))
    setShots((current) => current.map((shot, at) => at === index
      ? { ...shot, regions: [...shot.regions, { x, y, note: "" }] }
      : shot))
    setPreview(null)
  }

  function updateRegion(shotIndex: number, regionIndex: number, note: string) {
    setShots((current) => current.map((shot, at) => at === shotIndex
      ? { ...shot, regions: shot.regions.map((region, index) => index === regionIndex ? { ...region, note } : region) }
      : shot))
    setPreview(null)
  }

  function removeRegion(shotIndex: number, regionIndex: number) {
    setShots((current) => current.map((shot, at) => at === shotIndex
      ? { ...shot, regions: shot.regions.filter((_, index) => index !== regionIndex) }
      : shot))
    setPreview(null)
  }

  function acknowledgeAll() {
    if (!preview) return
    const ids = preview.findings.map((finding) => finding.id)
    setAcknowledged(ids)
    void runPreview(ids)
  }

  return <section className="flex flex-col gap-3 rounded-lg border border-ems-line p-3 text-sm">
    <div className="flex flex-wrap items-center justify-between gap-2">
      <strong>继续修改画面</strong>
      <span className="text-xs text-ems-muted">追加指令或标注截图；修改后仍是同一个隔离预览，需重新检查才能发布{appliedAt > 0 ? `（已修改 ${appliedAt} 次）` : ""}</span>
    </div>

    {error && <InlineNotice tone="danger">{error}</InlineNotice>}

    <label className="flex flex-col gap-1">修改要求
      <textarea rows={3} value={instruction} placeholder="例如：把水泵卡片改成红色，标题改为“泵房 V2”。"
        onChange={(event) => { setInstruction(event.target.value); setPreview(null) }} />
    </label>

    <div className="flex flex-col gap-2">
      <strong>标注截图（可选）</strong>
      <p className="m-0 text-xs text-ems-muted">在图片上点击要修改的位置，并为每个标记写一句说明。只发送标注文字和相对坐标，图片像素不发送。</p>
      <input type="file" accept="image/png,image/jpeg" multiple onChange={(event) => { void addFiles(event.target.files); event.target.value = "" }} />
      {shots.map((shot, shotIndex) => <div key={`${shot.name}-${shotIndex}`} className="flex flex-col gap-2 rounded-lg border border-ems-line p-3">
        <div className="flex items-center justify-between gap-2">
          <span>{shot.name}</span>
          <button type="button" className="text-xs" onClick={() => { setShots((current) => current.filter((_, at) => at !== shotIndex)); setPreview(null) }}>移除</button>
        </div>
        <div className="relative self-start" style={{ maxWidth: "100%" }}>
          <img src={shot.data} alt={shot.name} onClick={(event) => addRegion(shotIndex, event)}
            style={{ display: "block", maxWidth: "100%", maxHeight: 280, cursor: "crosshair", border: "1px solid var(--color-ems-line)", borderRadius: 6 }} />
          {shot.regions.map((region, regionIndex) => <span key={regionIndex}
            style={{
              position: "absolute", left: `${region.x * 100}%`, top: `${region.y * 100}%`,
              transform: "translate(-50%, -50%)", minWidth: 20, height: 20, borderRadius: 10,
              background: "#d97706", color: "#fff", fontSize: 11, lineHeight: "20px", textAlign: "center",
              pointerEvents: "none",
            }}>{regionIndex + 1}</span>)}
        </div>
        {shot.regions.length === 0 && <span className="text-xs text-ems-amber">还没有标注位置，点击图片添加</span>}
        {shot.regions.map((region, regionIndex) => <div key={regionIndex} className="flex items-center gap-2">
          <span className="text-xs text-ems-muted">区域 {regionIndex + 1}（{Math.round(region.x * 100)}%，{Math.round(region.y * 100)}%）</span>
          <input value={region.note} placeholder="这里要怎么改" className="flex-1"
            onChange={(event) => updateRegion(shotIndex, regionIndex, event.target.value)} />
          <button type="button" className="text-xs" onClick={() => removeRegion(shotIndex, regionIndex)}>删除</button>
        </div>)}
        <label className="flex items-center gap-2 text-xs">
          <input type="checkbox" checked={shot.reviewed} onChange={(event) => updateShot(shotIndex, { reviewed: event.target.checked })} />
          我已检查该截图不含现场地址、账号、凭据、IP 或序列号
        </label>
      </div>)}
    </div>

    {changes.length > 0 && <div className="flex flex-col gap-1 rounded border border-ems-line p-2">
      <strong>上次修改了哪里</strong>
      {changes.map((change, index) => <div key={`${change.kind}-${change.selector}-${index}`} className="flex flex-wrap items-center justify-between gap-2 text-xs">
        <span>
          {changeText[change.kind] || change.kind} · {change.selector}
          {change.line ? ` · 第 ${change.line} 行` : ""}
          {change.detail ? ` · ${change.detail}` : ""}
        </span>
        {change.element && <button type="button" className="text-xs" onClick={() => onLocate(change.element || "")}>在画面中定位</button>}
      </div>)}
    </div>}

    {preview && <div className="flex flex-col gap-3">
      <div className="flex items-center justify-between gap-2">
        <strong>出站清单</strong>
        <span className="text-xs text-ems-muted">{preview.blocked ? "已阻止发送" : "可以发送"}</span>
      </div>
      {preview.blocked && preview.block_reasons?.length ? <InlineNotice tone="danger">
        {preview.block_reasons.map((reason) => reasonText[reason] || reason).join("；")}
      </InlineNotice> : null}

      {preview.categories.map((category) => <section key={category.key} className="rounded-lg border border-ems-line p-3">
        <div className="flex items-center justify-between gap-2"><strong>{category.label}</strong><span className="text-xs text-ems-muted">{category.summary}</span></div>
        {category.key === "code" && <div className="mt-2 flex flex-col gap-1">
          <pre className="m-0 whitespace-pre-wrap break-words text-xs">{preview.code.instruction}</pre>
          {preview.code.context && <details><summary className="text-xs text-ems-muted">现有画面 HTML（{preview.code.context.length} 字）</summary><pre className="m-0 whitespace-pre-wrap break-words text-xs">{preview.code.context}</pre></details>}
        </div>}
        {category.key === "device_model" && <ul className="m-0 mt-2 flex list-none flex-col gap-1 p-0">
          {preview.device_model.length === 0 && <li className="text-ems-muted">没有点位</li>}
          {preview.device_model.map((point, pointIndex) => <li key={pointIndex}>{point.name} · {point.type}{point.unit ? ` · ${point.unit}` : ""}{point.id ? <span className="text-xs text-ems-muted"> · id {point.id}</span> : null}</li>)}
        </ul>}
        {category.key === "screenshots" && <ul className="m-0 mt-2 flex list-none flex-col gap-1 p-0">
          {preview.screenshots.length === 0 && <li className="text-ems-muted">未附加截图</li>}
          {preview.screenshots.map((shot, shotIndex) => <li key={shotIndex}>
            {shot.name} · {shot.width && shot.height ? `${shot.width}×${shot.height} · ` : ""}{shot.bytes} 字节 · 像素不发送{shot.invalid ? " · 标注无效" : ""}
            {(shot.regions || []).map((region, regionIndex) => <div key={regionIndex} className="text-xs text-ems-muted">
              区域 {regionIndex + 1}（{Math.round(region.x * 100)}%，{Math.round(region.y * 100)}%）：{region.note}
            </div>)}
          </li>)}
        </ul>}
      </section>)}

      {preview.findings.length > 0 && <section className="rounded-lg border border-ems-line p-3">
        <strong>疑似敏感内容</strong>
        <ul className="m-0 mt-2 flex list-none flex-col gap-1 p-0">
          {preview.findings.map((finding) => <li key={finding.id}>
            {categoryText[finding.category] || finding.category}{finding.category !== "code" ? ` #${finding.item + 1}` : ""} · {fieldText[finding.field] || finding.field} 第 {finding.line} 行 · {kindText[finding.kind] || finding.kind}：{finding.preview}
          </li>)}
        </ul>
        {preview.findings.some((finding) => finding.blocking) && <EmsButton variant="secondary" disabled={busy} className="mt-2" onClick={acknowledgeAll}>
          我已核实以上标记不是现场秘密，仍要发送
        </EmsButton>}
      </section>}

      {!preview.blocked && <label className="flex items-center gap-2">
        <input type="checkbox" checked={confirmed} onChange={(event) => setConfirmed(event.target.checked)} />
        我已核对上述出站内容，确认发送给 DeepSeek
      </label>}
    </div>}

    <div className="flex flex-wrap items-center gap-2">
      <EmsButton variant="secondary" disabled={busy || !canPreview} onClick={() => void runPreview()}>预览将发送的内容</EmsButton>
      <EmsButton disabled={busy || !preview || preview.blocked || !confirmed} busy={busy} busyLabel="修改中…" onClick={() => void apply()}>确认并修改</EmsButton>
      {revision > 0 && <span className="text-xs text-ems-muted">当前为第 {revision} 次修改后的画面</span>}
    </div>
  </section>
}
