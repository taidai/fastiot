import { reactive } from 'vue'
import { INITIAL_ALARMS, INITIAL_ALARM_HISTORY } from './preview-data'
import type { PreviewAlarm } from './preview-data'

function createState(projectId: string) {
  const pump = projectId === 'site-pump'
  return reactive({
    current: pump ? 'area' : projectId,
    deviceTab: 'live' as 'live' | 'params',
    alarms: (pump ? INITIAL_ALARMS : []).map(item => ({ ...item })) as PreviewAlarm[],
    alarmHistory: (pump ? INITIAL_ALARM_HISTORY : []).map(item => ({ ...item })) as PreviewAlarm[],
    acked: {} as Record<string, boolean>,
    opScenario: 'success', operationResult: '', historyRange: '15分钟', historyGran: '5秒', historyView: '数据表',
  })
}
const projects = new Map<string, ReturnType<typeof createState>>()
export function overviewPreviewState(projectId: string) {
  if (!projects.has(projectId)) projects.set(projectId, createState(projectId))
  return projects.get(projectId)!
}
