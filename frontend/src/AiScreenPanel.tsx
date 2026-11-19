import { useEffect, useState } from "react"
import { EmsButton, EmsOverlay, InlineNotice } from "./components/ems"
import { request } from "./api"

type Status = { configured: boolean; model: string }
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
  code: { instruction: string; context?: string }
  device_model: { name: string; type: string; unit?: string }[]
  screenshots: { name: string; media_type: string; bytes: number; width?: number; height?: number; annotation?: string; reviewed: boolean; sends_pixels: boolean; invalid?: boolean }[]
  findings: Finding[]
  block_reasons?: string[]
  blocked: boolean
}
type Shot = { name: string; media_type: string; data: string; annotation: string; reviewed: boolean }

const reasonText: Record<string, string> = {
  instruction_required: "请先填写画面要求",
  sensitive_content: "检测到疑似敏感的现场信息",
  screenshot_invalid: "截图数据无法读取",
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
  instruction: "画面要求",
  context: "现有画面配置",
  name: "名称",
  unit: "单位",
  annotation: "标注文字",
  metadata: "图片文字信息",
}
const categoryText: Record<string, string> = { code: "代码与上下文", device_model: "设备模型", screenshot: "截图" }

function readFile(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result || ""))
    reader.onerror = () => reject(reader.error || new Error("读取失败"))
    reader.readAsDataURL(file)
  })
}

export default function AiScreenPanel({ token, pointIDs, context, onClose }: {
  token: string
  pointIDs: string[]
  context: string
  onClose: () => void
}) {
  const [status, setStatus] = useState<Status | null>(null)
  const [instruction, setInstruction] = useState("")
  const [shots, setShots] = useState<Shot[]>([])
  const [acknowledged, setAcknowledged] = useState<string[]>([])
  const [preview, setPreview] = useState<Manifest | null>(null)
  const [confirmed, setConfirmed] = useState(false)
  const [content, setContent] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")

  useEffect(() => {
    let cancelled = false
    request<Status>(token, "/deepseek/status")
      .then((result) => { if (!cancelled) setStatus(result) })
      .catch(() => { if (!cancelled) setStatus({ configured: false, model: "" }) })
    return () => { cancelled = true }
  }, [token])

  function body(ack: string[], withConfirm: boolean) {
    return {
      instruction,
      context,
      point_ids: pointIDs,
      screenshots: shots.map((shot) => ({ name: shot.name, media_type: shot.media_type, data: shot.data, annotation: shot.annotation, reviewed: shot.reviewed })),
      acknowledged: ack,
      ...(withConfirm ? { confirmed: true } : {}),
    }
  }

  async function runPreview(ack = acknowledged) {
    setBusy(true)
    setError("")
    try {
      const result = await request<{ configured: boolean; manifest: Manifest }>(token, "/deepseek/preview", "POST", body(ack, false))
      setPreview(result.manifest)
      setConfirmed(false)
      setContent("")
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "预览失败")
    } finally {
      setBusy(false)
    }
  }

  async function generate() {
    setBusy(true)
    setError("")
    try {
      const result = await request<{ content: string }>(token, "/deepseek/generate", "POST", body(acknowledged, true))
      setContent(result.content || "模型没有返回内容")
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "生成失败")
    } finally {
      setBusy(false)
    }
  }

  async function addFiles(files: FileList | null) {
    if (!files) return
    const next: Shot[] = []
    for (const file of Array.from(files)) {
      try {
        next.push({ name: file.name, media_type: file.type || "image/png", data: await readFile(file), annotation: "", reviewed: false })
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

  function acknowledgeAll() {
    if (!preview) return
    const ids = preview.findings.map((finding) => finding.id)
    setAcknowledged(ids)
    void runPreview(ids)
  }

  const canGenerate = Boolean(status?.configured && preview && !preview.blocked && confirmed)

  return <EmsOverlay open title="AI 生成画面" variant="drawer" busy={busy} onClose={onClose}
    actions={<>
      <EmsButton variant="secondary" disabled={busy} onClick={onClose}>关闭</EmsButton>
      <EmsButton variant="secondary" disabled={busy || !instruction.trim()} onClick={() => void runPreview()}>预览将发送的内容</EmsButton>
      <EmsButton disabled={!canGenerate} busy={busy} busyLabel="生成中…" onClick={() => void generate()}>确认并生成</EmsButton>
    </>}>
    <div className="flex flex-col gap-4">
      {status && (status.configured
        ? <InlineNotice tone="info">使用自备 DeepSeek 凭据（模型 {status.model || "deepseek-chat"}），调用费用由你的凭据承担；平台不内置、不代付。</InlineNotice>
        : <InlineNotice tone="danger">尚未配置 DeepSeek 凭据。请在该实例的环境变量中设置 <code>DEEPSEEK_API_KEY</code>（可选 <code>DEEPSEEK_MODEL</code>、<code>DEEPSEEK_BASE_URL</code>）后重启。凭据不写入页面、日志或项目包。</InlineNotice>)}

      {error && <InlineNotice tone="danger">{error}</InlineNotice>}

      <label className="flex flex-col gap-1 text-sm">画面要求
        <textarea rows={4} value={instruction} placeholder="例如：生成泵房监控画面，显示水箱液位和出口压力，单位分别为 m 和 kPa。"
          onChange={(event) => { setInstruction(event.target.value); setPreview(null); setContent("") }} />
      </label>

      <div className="flex flex-col gap-2 text-sm">
        <strong>标注截图（可选）</strong>
        <p className="m-0 text-xs text-ems-muted">图片像素不会发送给模型，只发送文件名和下面的标注文字；请先自行核对。</p>
        <input type="file" accept="image/png,image/jpeg" multiple onChange={(event) => { void addFiles(event.target.files); event.target.value = "" }} />
        {shots.map((shot, index) => <div key={`${shot.name}-${index}`} className="flex flex-col gap-2 rounded-lg border border-ems-line p-3">
          <div className="flex items-center justify-between gap-2">
            <span>{shot.name}</span>
            <button type="button" className="text-xs" onClick={() => { setShots((current) => current.filter((_, at) => at !== index)); setPreview(null) }}>移除</button>
          </div>
          <textarea rows={2} value={shot.annotation} placeholder="把这张图里要改的地方写成文字（会随请求发送）"
            onChange={(event) => updateShot(index, { annotation: event.target.value })} />
          <label className="flex items-center gap-2 text-xs">
            <input type="checkbox" checked={shot.reviewed} onChange={(event) => updateShot(index, { reviewed: event.target.checked })} />
            我已检查该截图不含现场地址、账号、凭据、IP 或序列号
          </label>
        </div>)}
      </div>

      {preview && <div className="flex flex-col gap-3">
        <div className="flex items-center justify-between gap-2">
          <strong>出站清单</strong>
          <span className="text-xs text-ems-muted">{preview.blocked ? "已阻止发送" : "可以发送"}</span>
        </div>
        {preview.blocked && preview.block_reasons?.length ? <InlineNotice tone="danger">
          {preview.block_reasons.map((reason) => reasonText[reason] || reason).join("；")}
        </InlineNotice> : null}

        {preview.categories.map((category) => <section key={category.key} className="rounded-lg border border-ems-line p-3 text-sm">
          <div className="flex items-center justify-between gap-2"><strong>{category.label}</strong><span className="text-xs text-ems-muted">{category.summary}</span></div>
          {category.key === "code" && <div className="mt-2 flex flex-col gap-1">
            <pre className="m-0 whitespace-pre-wrap break-words">{preview.code.instruction}</pre>
            {preview.code.context && <details><summary className="text-xs text-ems-muted">现有画面配置（{preview.code.context.length} 字）</summary><pre className="m-0 whitespace-pre-wrap break-words text-xs">{preview.code.context}</pre></details>}
          </div>}
          {category.key === "device_model" && <ul className="m-0 mt-2 flex flex-col gap-1 p-0">
            {preview.device_model.length === 0 && <li className="text-ems-muted">没有选择点位</li>}
            {preview.device_model.map((point, index) => <li key={index}>{point.name} · {point.type}{point.unit ? ` · ${point.unit}` : ""}</li>)}
          </ul>}
          {category.key === "screenshots" && <ul className="m-0 mt-2 flex flex-col gap-1 p-0">
            {preview.screenshots.length === 0 && <li className="text-ems-muted">未附加截图</li>}
            {preview.screenshots.map((shot, index) => <li key={index}>
              {shot.name} · {shot.width && shot.height ? `${shot.width}×${shot.height} · ` : ""}{shot.bytes} 字节 · 像素不发送{shot.invalid ? " · 无法读取" : ""}
              {shot.annotation && <div className="text-xs text-ems-muted">标注：{shot.annotation}</div>}
            </li>)}
          </ul>}
        </section>)}

        {preview.findings.length > 0 && <section className="rounded-lg border border-ems-line p-3 text-sm">
          <strong>疑似敏感内容</strong>
          <ul className="m-0 mt-2 flex flex-col gap-1 p-0">
            {preview.findings.map((finding) => <li key={finding.id}>
              {categoryText[finding.category] || finding.category}{finding.category !== "code" ? ` #${finding.item + 1}` : ""} · {fieldText[finding.field] || finding.field} 第 {finding.line} 行 · {kindText[finding.kind] || finding.kind}：{finding.preview}
            </li>)}
          </ul>
          {preview.findings.some((finding) => finding.blocking) && <EmsButton variant="secondary" disabled={busy} className="mt-2" onClick={acknowledgeAll}>
            我已核实以上标记不是现场秘密，仍要发送
          </EmsButton>}
        </section>}

        {!preview.blocked && <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={confirmed} onChange={(event) => setConfirmed(event.target.checked)} />
          我已核对上述出站内容，确认发送给 DeepSeek
        </label>}
      </div>}

      {content && <section className="rounded-lg border border-ems-line p-3 text-sm">
        <strong>模型返回</strong>
        <pre className="m-0 mt-2 max-h-80 overflow-auto whitespace-pre-wrap break-words text-xs">{content}</pre>
      </section>}
    </div>
  </EmsOverlay>
}
