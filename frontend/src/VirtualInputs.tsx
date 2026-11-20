import { useCallback, useEffect, useRef, useState } from "react"
import { IonCardContent } from "@ionic/react"
import { nameOf, Node, request } from "./api"
import { EmsButton, EmsCard, EmsOverlay, InlineNotice, StatusBadge } from "./components/ems"

type Definition = {
  kind: "number" | "bool" | "enum"; unit: string; purpose: string
  min?: number; max?: number; choices?: string[]; validity: "constant" | "timed"
  ttl_seconds?: number; runtime_editable: boolean; value?: number | boolean | string
}
type Entry = { value: number | boolean | string; definition: Definition; actor: string; entered_at: string; expires_at?: string }
type Input = {
  id: string; parent: string; name: string; revision: number; source: "configuration" | "manual" | "simulation"
  draft?: Definition; applied?: Definition
  state: { source: string; quality: string; reason?: string; value?: number | boolean | string; retained?: Entry }
  permissions: { edit: boolean; apply: boolean; write: boolean }
  last_change?: { actor: string; at: string; action: string; before: unknown; after: unknown }
}
type Impact = { confirmation: string; references: { id: string; type: string; name: string; reason: string }[]; restricted: boolean; coverage: string }
type Pending = { input: Input; action: "apply" | "value"; payload: { revision: number; operation_id: string; value?: number | boolean | string }; impact: Impact }
const sourceNames = { configuration: "配置常量", manual: "人工输入", simulation: "模拟输入（非实测）" }
const reasons: Record<string, string> = { not_applied: "尚未应用", not_entered: "尚未录入或定义已变更", definition_changed: "定义已变更，需重新录入", pending_verification: "时间依据待核实，请获准人员重新录入", expired: "已过期" }
const initial: Definition = { kind: "number", unit: "1", purpose: "", min: 0, max: 100, validity: "constant", runtime_editable: false, value: 0 }
const display = (value: unknown) => value === undefined || value === null ? "—" : String(value)
const date = (value?: string) => value && !value.startsWith("0001-") ? new Date(value).toLocaleString() : "—"
function typedValue(kind: Definition["kind"], text: string) {
  if (kind === "bool") return text === "true"
  if (kind === "number") {
    if (text.trim() === "" || !Number.isFinite(Number(text))) throw new Error("请填写有限数值")
    return Number(text)
  }
  return text
}
function ValueField({ definition, value, onChange, label }: { definition: Definition; value: string; onChange: (v: string) => void; label: string }) {
  return <label>{label}{definition.kind === "number"
    ? <input aria-label={label} type="number" step="any" value={value} onChange={e => onChange(e.target.value)} />
    : <select aria-label={label} value={value} onChange={e => onChange(e.target.value)}>
      {definition.kind === "bool" ? <><option value="false">false</option><option value="true">true</option></>
        : <><option value="">请选择</option>{definition.choices?.map(v => <option key={v} value={v}>{v}</option>)}</>}
    </select>}</label>
}

export default function VirtualInputs({ token, nodes, engineering = false, customer = false }: { token: string; nodes: Node[]; engineering?: boolean; customer?: boolean }) {
  const text = customer
    ? {
        title: "模拟输入",
        intro: "模拟输入只参与计算，不会写设备，也不代表实测。配置保存为草稿，明确应用后才生效。",
        create: "新建模拟输入",
        loading: "正在读取模拟输入…",
        empty: "当前范围尚无模拟输入。",
        editDraft: "编辑模拟输入草稿",
        createTitle: "新建模拟输入",
        confirmTitle: "确认模拟输入变更",
      }
    : {
        title: "虚拟输入",
        intro: "独立非物理输入：不写设备，不代表实测。配置保存为草稿，明确应用后才生效。",
        create: "新建虚拟输入",
        loading: "正在读取虚拟输入…",
        empty: "当前范围尚无虚拟输入。",
        editDraft: "编辑虚拟输入草稿",
        createTitle: "新建虚拟输入",
        confirmTitle: "确认虚拟输入变更",
      }
  const parents = nodes.filter(n => n.type === "group" || n.parent === "root")
  const scope = parents.map(n => n.id).sort().join(",")
  const [inputs, setInputs] = useState<Input[]>([])
  const [ready, setReady] = useState(false)
  const [loadError, setLoadError] = useState("")
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const [editing, setEditing] = useState<Input | null | undefined>()
  const [name, setName] = useState("")
  const [parent, setParent] = useState("")
  const [source, setSource] = useState<Input["source"]>("configuration")
  const [definition, setDefinition] = useState<Definition>(initial)
  const [value, setValue] = useState("0")
  const [runtime, setRuntime] = useState<Input>()
  const [runtimeValue, setRuntimeValue] = useState("")
  const [pending, setPending] = useState<Pending>()
  const [refresh, setRefresh] = useState(0)
  const creationOperation = useRef("")
  const closeEditor = useCallback(() => setEditing(undefined), [])
  const closeRuntime = useCallback(() => setRuntime(undefined), [])
  const closeConfirmation = useCallback(() => setPending(undefined), [])
  useEffect(() => {
    let cancelled = false, inFlight = false
    const controller = new AbortController()
    setReady(false); setInputs([]); setLoadError(""); setEditing(undefined); setRuntime(undefined); setPending(undefined)
    async function load() {
      if (inFlight) return
      inFlight = true
      try {
        const result = await request<{ inputs: Input[] }>(token, "/virtual-inputs", "GET", undefined, controller.signal)
        if (!cancelled) { setInputs(result.inputs.filter(i => scope.split(",").includes(i.parent))); setReady(true); setLoadError("") }
      } catch (e) {
        if (!cancelled) { setInputs([]); setReady(false); setLoadError((e as Error).message) }
      } finally { inFlight = false }
    }
    void load()
    const timer = window.setInterval(load, 2000)
    return () => { cancelled = true; controller.abort(); window.clearInterval(timer) }
  }, [token, scope, refresh])
  async function run(action: () => Promise<void>) {
    setBusy(true); setError("")
    try { await action() } catch (e) { setError((e as Error).message) } finally { setBusy(false) }
  }
  function edit(input?: Input) {
    creationOperation.current = crypto.randomUUID()
    setError(""); setEditing(input || null); setName(input?.name || ""); setParent(input?.parent || parents[0]?.id || "")
    setSource(input?.source || "configuration")
    const draft = input?.draft || initial
    setDefinition(draft); setValue(String(draft.value ?? (draft.kind === "bool" ? false : draft.kind === "number" ? 0 : "")))
  }
  async function saveDraft() {
    const def = { ...definition, value: source === "configuration" ? typedValue(definition.kind, value) : undefined }
    const payload = editing
      ? { revision: editing.revision, operation_id: crypto.randomUUID(), definition: def }
      : { parent, name, source, operation_id: creationOperation.current, definition: def }
    await request<Input>(token, editing ? `/virtual-inputs/${editing.id}/draft` : "/virtual-inputs", "POST", payload)
    setEditing(undefined); setRefresh(v => v + 1)
  }
  async function preview(input: Input, action: "apply" | "value") {
    const payload = { revision: input.revision, operation_id: crypto.randomUUID(), ...(action === "value" ? { value: typedValue(input.applied!.kind, runtimeValue) } : {}) }
    const impact = await request<Impact>(token, `/virtual-inputs/${input.id}/impact`, "POST", { ...payload, action })
    setPending({ input, action, payload, impact })
  }
  async function confirm() {
    if (!pending) return
    await request<Input>(token, `/virtual-inputs/${pending.input.id}/${pending.action}`, "POST", { ...pending.payload, confirmation: pending.impact.confirmation })
    setPending(undefined); setRuntime(undefined); setRefresh(v => v + 1)
  }
  return <EmsCard className="virtual-inputs">
    <IonCardContent>
      <div className="flex flex-wrap items-center justify-between gap-3"><h2>{text.title}</h2>{engineering && <EmsButton disabled={busy || !ready || !parents.length} onClick={() => edit()}>{text.create}</EmsButton>}</div>
      <p>{text.intro}</p>
      {loadError && <InlineNotice tone="danger">读取失败：{loadError}；当前数据不可用。<EmsButton onClick={() => setRefresh(v => v + 1)}>重新读取</EmsButton></InlineNotice>}
      {error && <div role="alert" className="inline-error">{error}</div>}
      {!ready && !loadError && <p>{text.loading}</p>}
      {ready && inputs.length === 0 && <p>{text.empty}</p>}
      <div className="grid gap-3">
        {inputs.map(input => <article key={input.id} className="rounded-xl border border-ems-line p-4" data-input-id={input.id}>
          <div className="flex flex-wrap items-center justify-between gap-2"><h3>{input.name}</h3><StatusBadge tone={input.source === "simulation" ? "warning" : "neutral"}>{sourceNames[input.source]}</StatusBadge></div>
          <p>当前可用值：<strong>{display(input.state.value)}</strong> {input.state.value !== undefined ? input.applied?.unit : ""} · {input.state.quality === "good" ? "可用" : reasons[input.state.reason || ""] || "不可用"}</p>
          <p>用途：{input.applied?.purpose || input.draft?.purpose || "—"} · 版本 {input.revision} · {input.applied ? "有生效定义" : "未应用"}{input.draft && JSON.stringify(input.draft) !== JSON.stringify(input.applied) ? " · 有草稿" : ""}</p>
          {input.applied && <p>{input.applied.kind === "number" ? `允许范围 ${input.applied.min}～${input.applied.max} ${input.applied.unit}` : `类型 ${input.applied.kind}${input.applied.choices ? `：${input.applied.choices.join(" / ")}` : ""}`} · {input.source === "configuration" ? "配置常量，不按采样过期" : `有效期 ${input.applied.ttl_seconds} 秒；运行修改${input.applied.runtime_editable ? "已开放" : "关闭"}`}</p>}
          {input.state.retained && <p>{input.state.value === undefined ? "历史保留值" : "录入依据"}：{display(input.state.retained.value)} {input.state.retained.definition.unit} · 操作者 {input.state.retained.actor} · {date(input.state.retained.entered_at)}{input.source !== "configuration" && ` · 原到期 ${date(input.state.retained.expires_at)}`}</p>}
          {input.last_change && <details><summary>最近变更依据</summary><p>操作者 {input.last_change.actor} · {date(input.last_change.at)} · {input.last_change.action}</p><pre className="whitespace-pre-wrap break-all">{JSON.stringify({ before: input.last_change.before, after: input.last_change.after }, null, 2)}</pre></details>}
          <div className="flex flex-wrap gap-2">
            {engineering && input.permissions.edit && <EmsButton variant="secondary" disabled={busy} onClick={() => edit(input)}>编辑草稿</EmsButton>}
            {input.permissions.apply && input.draft && <EmsButton disabled={busy} onClick={() => void run(() => preview(input, "apply"))}>预览并应用</EmsButton>}
            {input.permissions.write && <EmsButton disabled={busy} onClick={() => { setError(""); setRuntime(input); setRuntimeValue(input.applied?.kind === "bool" ? "false" : "") }}>修改运行输入</EmsButton>}
          </div>
        </article>)}
      </div>
      <EmsOverlay open={editing !== undefined} title={editing ? text.editDraft : text.createTitle} onClose={closeEditor} busy={busy}
        actions={<><EmsButton variant="secondary" disabled={busy} onClick={closeEditor}>取消</EmsButton><EmsButton disabled={busy || !ready} onClick={() => void run(saveDraft)}>保存草稿（不生效）</EmsButton></>}>
        <div className="grid gap-3">
          <label>名称<input aria-label="输入名称" value={name} disabled={!!editing} onChange={e => setName(e.target.value)} /></label>
          <label>所属分组<select aria-label="输入分组" value={parent} disabled={!!editing} onChange={e => setParent(e.target.value)}>{parents.map(n => <option key={n.id} value={n.id}>{nameOf(n)}</option>)}</select></label>
          <label>来源（创建后不可变）<select aria-label="输入来源" value={source} disabled={!!editing} onChange={e => {
            const next = e.target.value as Input["source"]
            setSource(next); setDefinition({ ...definition, validity: next === "configuration" ? "constant" : "timed", ttl_seconds: next === "configuration" ? undefined : 60, runtime_editable: false })
          }}>{Object.entries(sourceNames).map(([key,label]) => <option key={key} value={key}>{label}</option>)}</select></label>
          <label>类型<select aria-label="输入类型" value={definition.kind} onChange={e => {
            const kind = e.target.value as Definition["kind"]
            setDefinition({ ...definition, kind, min: kind === "number" ? 0 : undefined, max: kind === "number" ? 100 : undefined, choices: kind === "enum" ? ["默认"] : undefined }); setValue(kind === "bool" ? "false" : kind === "number" ? "0" : "默认")
          }}><option value="number">数值</option><option value="bool">布尔</option><option value="enum">枚举</option></select></label>
          <label>单位（无量纲填 1）<input aria-label="输入单位" value={definition.unit} onChange={e => setDefinition({ ...definition, unit: e.target.value })} /></label>
          <label>用途<input aria-label="输入用途" value={definition.purpose} onChange={e => setDefinition({ ...definition, purpose: e.target.value })} /></label>
          {definition.kind === "number" && <div className="grid grid-cols-2 gap-3"><label>最小值<input aria-label="输入最小值" type="number" step="any" value={definition.min ?? ""} onChange={e => setDefinition({ ...definition, min: e.target.value === "" ? undefined : Number(e.target.value) })} /></label><label>最大值<input aria-label="输入最大值" type="number" step="any" value={definition.max ?? ""} onChange={e => setDefinition({ ...definition, max: e.target.value === "" ? undefined : Number(e.target.value) })} /></label></div>}
          {definition.kind === "enum" && <label>枚举选项（逗号分隔）<input aria-label="枚举选项" value={definition.choices?.join(",") || ""} onChange={e => setDefinition({ ...definition, choices: e.target.value.split(",").map(v => v.trim()) })} /></label>}
          {source === "configuration" ? <><p>明确作为配置常量：不受周期采样 TTL 影响。</p><ValueField definition={definition} value={value} onChange={setValue} label="配置值" /></>
            : <><label>有效期（秒）<input aria-label="输入有效期" type="number" min="1" step="1" value={definition.ttl_seconds ?? ""} onChange={e => setDefinition({ ...definition, ttl_seconds: Number(e.target.value) })} /></label><label className="!flex items-center gap-2"><input aria-label="开放运行修改" type="checkbox" className="!min-h-0 !h-4 !w-4" checked={definition.runtime_editable} onChange={e => setDefinition({ ...definition, runtime_editable: e.target.checked })} />应用后允许获准人员在范围内修改</label><p>应用定义不会生成数值；重启后需重新核实录入。</p></>}
          {error && <p role="alert" className="inline-error">{error}</p>}
        </div>
      </EmsOverlay>
      <EmsOverlay open={!!runtime} title="修改运行输入" onClose={closeRuntime} busy={busy}
        actions={<><EmsButton variant="secondary" disabled={busy} onClick={closeRuntime}>取消</EmsButton><EmsButton disabled={busy || !ready} onClick={() => runtime && void run(() => preview(runtime, "value"))}>预览修改影响</EmsButton></>}>
        {runtime?.applied && <><p>{runtime.name} · {sourceNames[runtime.source]} · {runtime.applied.purpose}</p><p>范围 {runtime.applied.min}～{runtime.applied.max} {runtime.applied.unit} · 有效期 {runtime.applied.ttl_seconds} 秒</p><ValueField definition={runtime.applied} value={runtimeValue} onChange={setRuntimeValue} label="新的运行输入" /></>}
        {error && <p role="alert" className="inline-error">{error}</p>}
      </EmsOverlay>
      <EmsOverlay open={!!pending} title={text.confirmTitle} onClose={closeConfirmation} busy={busy}
        actions={<><EmsButton variant="secondary" disabled={busy} onClick={closeConfirmation}>取消</EmsButton><EmsButton disabled={busy || !ready} onClick={() => void run(confirm)}>确认{pending?.action === "apply" ? "应用" : "修改"}（不写设备）</EmsButton></>}>
        {pending && <><p>{pending.input.name} · {sourceNames[pending.input.source]}</p><p>当前 {display(pending.input.state.value)} → {pending.action === "value" ? display(pending.payload.value) : pending.input.source === "configuration" ? display(pending.input.draft?.value) : "应用新定义，不产生运行值"}</p>
          <p>用途：{(pending.action === "apply" ? pending.input.draft : pending.input.applied)?.purpose} · 单位：{(pending.action === "apply" ? pending.input.draft : pending.input.applied)?.unit}</p>
          <p>{pending.input.source === "configuration" ? "配置常量，明确应用后生效。" : `原值不续期；本次新录入有效期 ${pending.input.applied?.ttl_seconds || pending.input.draft?.ttl_seconds} 秒。`}</p>
          <h3>已知下游影响</h3>{pending.impact.references.length ? <ul>{pending.impact.references.map((ref,i) => <li key={i}>{ref.name} · {ref.reason}</li>)}</ul> : <p>未发现当前账号可见的已知引用。</p>}
          {pending.impact.restricted && <p>另有权限范围外引用，名称与内容不披露。</p>}<p>{pending.impact.coverage}</p><p>将记录操作者、时间与变更；这不是设备命令或真机签收。</p></>}
        {error && <p role="alert" className="inline-error">{error}</p>}
      </EmsOverlay>
    </IonCardContent>
  </EmsCard>
}
