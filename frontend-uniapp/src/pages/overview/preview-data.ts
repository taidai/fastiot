export type PreviewKpi = [label: string, value: string, unit: string, alert?: number]
export type PreviewLayer = {
  no: string
  crumb: string
  title: string
  online: string
  kpis: PreviewKpi[]
  chain?: { name: string; val: string; exp: string; live?: number }[]
  device?: boolean
  list?: "devices" | "members"
}
export type PreviewAlarm = { id: string; title: string; level: string; at: string; device: string; occurrences: number; recoveredAt?: string }

export const OVERVIEW_DATA: Record<string, PreviewLayer> = {
  site: { no: "01", crumb: "教学泵房", title: "教学泵房", online: "3 / 3",
    kpis: [["出口压力", "0.42", "MPa"], ["总功率", "12.4", "kW"], ["在线设备", "3 / 3", ""], ["今日电量", "124", "kWh"], ["告警", "2", "条", 1], ["通信成功率", "99.2", "%"]],
    chain: [{ name: "水箱", val: "68%", exp: "液位 2.4 m · 容积 20 m³" }, { name: "1号泵", val: "运行", exp: "0.42 MPa · 12.4 A · 6.2 h", live: 1 }, { name: "供水管网", val: "0.42 MPa", exp: "流量 36 m³/h · 今日 86 m³" }], list: "devices" },
  area: { no: "02", crumb: "教学泵房 / 泵房", title: "泵房", online: "3 / 3",
    kpis: [["出口压力", "0.42", "MPa"], ["总功率", "12.4", "kW"], ["在线设备", "3 / 3", ""], ["今日电量", "124", "kWh"], ["告警", "2", "条", 1], ["通信成功率", "99.2", "%"]],
    chain: [{ name: "水箱", val: "68%", exp: "液位 2.4 m · 容积 20 m³" }, { name: "1号泵", val: "运行", exp: "0.42 MPa · 12.4 A · 6.2 h", live: 1 }, { name: "供水管网", val: "0.42 MPa", exp: "流量 36 m³/h · 今日 86 m³" }], list: "devices" },
  d1: { no: "03", crumb: "教学泵房 / 泵房 / 1号泵", title: "1号泵", online: "在线",
    kpis: [["出口压力", "0.42", "MPa"], ["出口流量", "36", "m³/h"], ["运行状态", "运行", ""], ["今日运行", "6.2", "h"], ["电流", "12.4", "A"], ["最近变化", "2 分钟前", ""]], device: true },
  d2: { no: "04", crumb: "教学泵房 / 泵房 / 2号泵", title: "2号泵", online: "在线",
    kpis: [["出口压力", "0.00", "MPa"], ["出口流量", "0", "m³/h"], ["运行状态", "停止", ""], ["今日运行", "0.8", "h"], ["电流", "0.0", "A"], ["最近变化", "18 分钟前", ""]], device: true },
  t1: { no: "05", crumb: "教学泵房 / 泵房 / 水箱", title: "水箱", online: "在线",
    kpis: [["液位", "68", "%"], ["容积", "20", "m³"], ["补水状态", "关闭", ""], ["今日补水", "4.2", "m³"], ["温度", "18.6", "°C"], ["最近变化", "6 分钟前", ""]], device: true },
  unit: { no: "06", crumb: "教学泵房 / 供水单元", title: "供水单元", online: "3 / 3",
    kpis: [["供水压力", "0.42", "MPa"], ["瞬时流量", "36", "m³/h"], ["成员在线", "3 / 3", ""], ["今日供水", "86", "m³"], ["今日电量", "124", "kWh"], ["通信成功率", "99.2", "%"]],
    chain: [{ name: "水箱", val: "68%", exp: "液位 2.4 m · 容积 20 m³" }, { name: "1号泵", val: "运行", exp: "0.42 MPa · 12.4 A · 6.2 h", live: 1 }, { name: "供水管网", val: "0.42 MPa", exp: "流量 36 m³/h · 今日 86 m³" }], list: "members" },
}

export const PREVIEW_LAYERS = [
  { key: "site", kind: "现场", name: "教学泵房" },
  { key: "area", kind: "区域", name: "泵房" },
  { key: "d1", kind: "设备", name: "1号泵" },
  { key: "d2", kind: "设备", name: "2号泵", alarm: 1 },
  { key: "t1", kind: "设备", name: "水箱", alarm: 1 },
  { key: "unit", kind: "单元", name: "供水单元" },
]
export const DEVICES = [["1号泵", "离心泵", "在线", "0.42 MPa", "正常", "2 分钟前"], ["2号泵", "离心泵", "在线", "停止", "正常", "2 分钟前"], ["水箱", "水箱", "在线", "68%", "正常", "2 分钟前"]]
export const POINTS = [["出口压力", "0.42", "MPa", "有效"], ["目标压力", "0.45", "MPa", "有效"], ["运行状态", "运行", "", "有效"], ["电流", "12.4", "A", "有效"], ["故障", "无", "", "有效"]]
export const PARAMS = [["来源模板", "离心泵 v1.3"], ["型号", "CDL-32"], ["点位数", "12"], ["单位", "kPa / MPa / A"], ["最近验证", "2026-11-19 · 张工"]]
export const MEMBERS = [["驱动泵（必需）", "1号泵", "0.42 MPa", "在线"], ["出口压力（必需）", "PT-01", "0.42 MPa", "在线"], ["液位反馈（必需）", "LT-01", "68%", "在线"]]
export const RECENT_CHANGES = [["1号泵 出口压力 0.44 → 0.42 MPa", "2 分钟前"], ["水箱 液位 71 → 68%", "6 分钟前"], ["2号泵 启动请求已回读", "18 分钟前"]]
// Final design prototype fixtures; shared data, never substituted for live reads.
export const SCREEN_PREVIEW_RUNS: Record<string, [at: string, name: string, result: string, reason: string][]> = {
  '教学泵房': [['14:00', '饮水线冲洗', '完成', '回读末端阀 · 30 秒'], ['09:00', '备用泵试转', '完成', '回读 5 秒'], ['07:00', '采暖预循环', '跳过', '回水 36℃，无需预循环']],
  '储能站 A': [['08:00', '早高峰放电', '完成', '回读 8.0 kW'], ['12:00', '光伏限功率', '跳过', '无弃光'], ['23:00', '谷电充电', '完成', 'SOC 76% → 100%']],
  '批量接入示例': [['08:00', '批量并网', '完成', '24 台 · 22 台回读'], ['12:00', 'SOC 校准', '跳过', '未到校准窗口'], ['23:00', '谷电充电', '完成', 'SOC 62% → 90%']],
}
export const SCREEN_PREVIEW_TREND_BARS = [42, 55, 48, 63, 70, 66, 74, 61, 54, 59, 65, 69]
export const TODAY_RUNS = [...SCREEN_PREVIEW_RUNS['教学泵房'].map(([at, name, result]) => [`${at} ${name}`, result]), [SCREEN_PREVIEW_RUNS['教学泵房'][2][3], '']]
export const INITIAL_ALARMS: PreviewAlarm[] = [
  { id: "a1", title: "2号泵 出口压力低", level: "预警", at: "3 分钟前", device: "2号泵", occurrences: 1 },
  { id: "a2", title: "水箱 液位偏低", level: "提示", at: "12 分钟前", device: "水箱", occurrences: 1 },
]
export const INITIAL_ALARM_HISTORY: PreviewAlarm[] = [{ id: "a3", title: "1号泵 振动偏高", level: "提示", at: "昨天 22:10", device: "1号泵", recoveredAt: "昨天 22:26", occurrences: 1 }]
export const TREND_BARS = [42, 50, 46, 58, 64, 60, 68, 72, 66, 58, 54, 60, 66, 70, 74, 68, 62, 56, 60, 64, 70, 66, 62, 58]
export const METRIC_ICONS = [
  "M4 12a8 8 0 1 1 16 0M12 12l4-4M12 4v2M4 12h2M18 12h2",
  "M13 2L5 13h6l-1 9 8-12h-6z",
  "M4 20V7l8-4 8 4v13M8 20v-6h8v6M8 9h2M14 9h2M8 12h2M14 12h2",
  "M4 7h15v11H4zM19 10h2v5h-2M8 10v5M12 10v5M16 10v5",
  "M6 16v-5a6 6 0 0 1 12 0v5l2 3H4zM10 21h4",
  "M4 18v-4M9 18V9M14 18V5M19 18V2",
]
