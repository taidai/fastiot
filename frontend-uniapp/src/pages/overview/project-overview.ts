import type { Snapshot } from '../../types/api'
import type { PreviewLayer, PreviewKpi } from './preview-data'

export function projectOverview(snapshot: Snapshot | null, name: string, energy: boolean) {
  const nodes = snapshot?.nodes || []
  const containers = nodes.filter(n => ['site','area','unit','energyUnit','device'].includes(n.type))
  const label = (id: string) => nodes.find(n => n.id === id)?.points.find(p => p.type === 'description')?.text || name
  const devices = containers.filter(n => n.type === 'device')
  const bound = new Set<string>()
  for (const node of nodes.filter(n => n.type === 'modbus')) for (const p of node.points) {
    if (p.type !== 'easyemsUnitModel' || !p.text) continue
    try { const model = JSON.parse(p.text); if (model.business_node_id) bound.add(model.business_node_id) } catch { /* malformed preview mapping remains unbound */ }
  }
  const layers = containers.map((n,index) => ({ key: n.id, name: label(n.id), kind: n.type === 'device' ? '设备' : n.type === 'site' ? '现场' : n.type === 'area' ? '区域' : '单元', alarm: 0, no: String(index + 1).padStart(2,'0') }))
  const data: Record<string, PreviewLayer> = {}
  for (const layer of layers) {
    const node = nodes.find(n => n.id === layer.key)!
    const online = `${devices.filter(d => bound.has(d.id)).length} / ${devices.length}`
    const kpis: PreviewKpi[] = energy ? node.type === 'device'
      ? [['功率','8.0','kW'],['SOC','76','%'],['运行状态','运行',''],['今日充放','52','kWh'],['温度','32.4','°C'],['最近变化','2 分钟前','']]
      : [['总功率','20.4','kW'],['今日发电','62','kWh'],['储能 SOC','76','%'],['在线设备',online,''],['活动告警','2','条'],['通信成功率','99.2','%']]
      : [['在线设备',online,''],['未接入',String(devices.length - bound.size),'台'],['活动告警','0','条'],['总功率','—','kW'],['今日电量','—','kWh'],['通信成功率','—','%']]
    data[layer.key] = { no: layer.no, crumb: layer.key === snapshot?.roots[0] ? name : `${name} / ${layer.name}`, title: layer.name, online, kpis, device: node.type === 'device', list: 'devices' }
  }
  return { layers, data, devices: devices.map(d => [label(d.id), d.id.startsWith('bms') ? '电池管理' : '储能变流器', bound.has(d.id) ? '在线' : '未绑定', bound.has(d.id) ? d.id.startsWith('bms') ? 'SOC 76%' : '8.0 kW' : '—', bound.has(d.id) ? '正常' : '未接入', bound.has(d.id) ? '2 分钟前' : '—']), flow: [
    { label: '光伏区', value: '12.4', unit: 'kW', detail: '今日发电 62 kWh' },
    { label: '并网点 PCC', value: '20.4', unit: 'kW', detail: '负载 20.4 kW · 购电 8.0 kW' },
    { label: '储能区', value: '8.0', unit: 'kW', detail: 'SOC 76% · 可充 2.1 h' },
  ] }
}
