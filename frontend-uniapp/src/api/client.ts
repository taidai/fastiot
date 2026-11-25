import { PREVIEW_MODE } from "./source"
import { ensureProjectContext, projectContext, resetProjectContext } from '../stores/projects'

export class ApiError extends Error {
  constructor(message: string, public status: number, public code = "") {
    super(message)
  }
}

const ERROR_TEXTS: Record<string, string> = {
  forbidden: "当前账号无权执行此操作",
  project_context_mismatch: "项目与当前运行实例不匹配，请刷新后重新选择",
  project_context_unavailable: "项目目录暂时无法读取，请稍后重试",
  project_versions_unavailable: "项目版本暂时无法读取，请稍后重试",
  no_project_access: "当前账号没有可访问的项目",
  admin_required: "需要管理员权限",
  read_only: "该点位只读，不能写入",
  operation_not_allowed: "当前状态下不允许此操作",
  command_not_accepted: "命令未被接受",
  preflight_failed: "预检存在阻断项，确认后才能冻结版本",
  dependency_incompatible: "依赖版本不匹配，已拒绝导入",
  invalid_package: "文件不是有效的项目包",
  history_export_limit_exceeded: "导出超过上限，请缩小点位或时间范围",
}

let redirectingToLogin = false

export async function request<T>(path: string, method = "GET", body?: unknown, responseType: 'text' | 'arraybuffer' = 'text'): Promise<T> {
  if (PREVIEW_MODE) {
    const { resolvePreview } = await import("./preview")
    const preview = await resolvePreview(path, method, body)
    if (preview.handled && (preview.status || 200) < 400) return preview.data as T
    const payload = preview.data as { error?: string } | undefined
    const backendCode = payload?.error || ""
    const code = preview.handled ? backendCode || "preview_request_failed" : "preview_unhandled"
    const message = preview.handled ? ERROR_TEXTS[backendCode] || backendCode || `预览操作失败：${method} ${path}` : `预览接口未实现：${method} ${path}`
    throw new ApiError(message, preview.status || 501, code)
  }
  const scoped = path !== '/login' && path !== '/projects' && path !== '/health'
  const entryGeneration = projectContext.generation
  const hadProject = !!projectContext.id
  if (scoped) {
    await ensureProjectContext()
    if (hadProject && entryGeneration !== projectContext.generation) throw new ApiError('项目上下文已改变，请重新读取', 409, 'project_context_changed')
  }
  const generation = projectContext.generation
  const token = path === "/login" ? "" : (uni.getStorageSync("fastems-token") || "") as string
  const response = await new Promise<UniApp.RequestSuccessCallbackResult>((resolve, reject) => {
    uni.request({
      url: `/api${path}`,
      method: method as any,
      responseType,
      data: body as Record<string, unknown> | undefined,
      header: {
        ...(scoped ? { 'X-Fastiot-Project': projectContext.id } : {}),
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
        ...(body === undefined ? {} : { "Content-Type": "application/json" }),
      },
      success: resolve,
      fail: reject,
    })
  })
  if (scoped && generation !== projectContext.generation && token === (uni.getStorageSync('fastems-token') || '')) {
    throw new ApiError('项目上下文已改变，请重新读取；已发出的写入请回到原项目核实', 409, 'project_context_changed')
  }
  let result = response.data as { error?: string }
  if (responseType === 'arraybuffer' && response.statusCode >= 400) {
    try { result = JSON.parse(new TextDecoder().decode(response.data as ArrayBuffer)) } catch { result = {} }
  }
  if (response.statusCode < 200 || response.statusCode >= 300) {
    if (response.statusCode === 401 && path !== "/login" && token === (uni.getStorageSync("fastems-token") || "") && !redirectingToLogin) {
      logout()
    }
    const code = result?.error || ""
    throw new ApiError(ERROR_TEXTS[code] || code || `请求失败（${response.statusCode}）`, response.statusCode, code)
  }
  if (path !== "/login" && token !== (uni.getStorageSync("fastems-token") || "")) {
    throw new ApiError("会话已改变，请重新读取", 401, "session_changed")
  }
  return response.data as T
}

export async function login(email: string, password: string) {
  const result = await request<{ token: string }>("/login", "POST", { email, password })
  if (!PREVIEW_MODE) {
    resetProjectContext()
    uni.setStorageSync("fastems-token", result.token)
  }
  redirectingToLogin = false
  return result.token
}

export function logout() {
  if (!PREVIEW_MODE) {
    resetProjectContext()
    uni.removeStorageSync("fastems-token")
  }
  uni.$emit("session-ended")
  if (redirectingToLogin) return
  redirectingToLogin = true
  uni.redirectTo({ url: "/pages/login/index" })
}

export function downloadText(filename: string, text: string, type = "application/json") {
  const blob = new Blob([text], { type })
  const url = URL.createObjectURL(blob)
  const link = document.createElement("a")
  link.href = url
  link.download = filename
  link.click()
  URL.revokeObjectURL(url)
}

export function nodeLabel(nodeType: string) {
  const labels: Record<string, string> = {
    site: "现场",
    group: "区域",
    variable: "区域",
    energyUnit: "业务单元",
    energy_unit: "业务单元",
    unit: "业务单元",
    device: "设备",
    modbus_device: "设备",
  }
  return labels[nodeType] || nodeType
}
