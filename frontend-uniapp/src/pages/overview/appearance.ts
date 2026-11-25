export type AppearanceTheme = "light" | "dark" | "tint"

export type Appearance = {
  theme: AppearanceTheme
  alpha: number
}

export const DEFAULT_APPEARANCE: Appearance = { theme: "light", alpha: 42 }

export function normalizeAppearance(value: unknown): Appearance {
  if (!value || typeof value !== "object") return { ...DEFAULT_APPEARANCE }
  const input = value as { theme?: unknown; alpha?: unknown }
  const theme = input.theme === "dark" || input.theme === "tint" || input.theme === "light" ? input.theme : DEFAULT_APPEARANCE.theme
  const alpha = typeof input.alpha === "number" && Number.isFinite(input.alpha)
    ? Math.min(85, Math.max(20, Math.round(input.alpha)))
    : DEFAULT_APPEARANCE.alpha
  return { theme, alpha }
}

// applyAppearanceDocument 把玻璃透明度写到文档根 CSS 变量。uniapp 的 view
// 组件不透传 CSS 自定义属性（--glass）的内联 style，所以不能靠 :style 绑定，
// 只能直接写 document.documentElement，让 .glass 的 var(--glass) 解析到。
export function applyAppearanceDocument(appearance: Appearance) {
  if (typeof document === "undefined") return
  const alpha = appearance.alpha / 100
  let glass: string
  if (appearance.theme === "dark") glass = `rgba(18,30,40,${alpha.toFixed(2)})`
  else if (appearance.theme === "tint") glass = `rgba(190,240,235,${(alpha * 0.3).toFixed(2)})`
  else glass = `rgba(255,255,255,${alpha.toFixed(2)})`
  document.documentElement.style.setProperty("--glass", glass)
}
