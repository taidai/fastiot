<template>
  <view class="access-step">
    <view class="step-head"><b>第 3 步 · 接设备</b><span>型号 → 通道 → 点位 → 验证；已绑定 {{ boundBusinessCount }} / {{ businessDevices.length }}。</span></view>
    <section class="card glass step-card">
    <p v-if="directoryError" class="error" role="alert">{{ directoryError }}</p>
    <p v-if="error" class="error" role="alert">{{ error }}</p>
    <p v-if="notice" class="notice" role="status">{{ notice }}</p>
    <p v-if="!canConfigure" class="note">当前账号可查看接入状态，但没有配置权限；写操作已停用。</p>

    <view v-if="!businessDevices.length && !selectedUnit" class="empty-access">
      <view class="panel-head"><h3>还没有设备</h3></view><p class="note">先回第 2 步搭台账，把设备建成占位。</p><button class="ghost" @click="emit('navigate', 'tree')">回第 2 步 →</button>
    </view>
    <template v-else>
    <MemberRoles v-if="selectedUnit" :snapshot="snapshot" :node-id="selectedUnit.id" :read-only="!canConfigure" @refresh="emit('refresh')" />
    <template v-else>
    <BatchAccess v-if="batchOpen" :snapshot="snapshot" :channels="channels" :profiles="profiles" :selected-node-ids="batchSelection" :read-only="!canConfigure" @close="batchOpen = false" @refresh="reloadAfterBatch" />

    <view v-if="scopeMode && !batchOpen" class="ledger-panel">
      <view class="panel-head">
        <h3>{{ selectedScopeName }} 的设备</h3><span class="tag">{{ scopeBoundCount }} / {{ scopeRows.length }} 已接入</span>
        <span class="spacer"></span><button class="ghost" :disabled="directoryLoading" @click="reload">{{ directoryLoading ? "读取中…" : "刷新" }}</button><button class="ghost" :disabled="busy !== ''" @click="startNewUnit">接入新站号</button><button class="ghost" :disabled="!canConfigure" @click="openBatch">批量接入</button>
      </view>
      <div v-if="scopeRows.length" class="table-wrap">
        <table class="mini-table ledger-table">
          <thead><tr><th>设备</th><th>状态</th><th>型号</th><th>通道</th><th>点位</th><th></th></tr></thead>
          <tbody><tr v-for="row in scopeRows" :key="row.id" :class="{ selected: row.device && deviceKey(row.device) === selectedKey }">
            <td><strong>{{ row.name }}</strong><small v-if="row.device">{{ row.device.name }}</small></td>
            <td><span class="chip" :class="row.bound ? 'ok' : 'warn'">{{ row.bound ? "已接入" : "未绑定" }}</span></td>
            <td>{{ row.device?.profile_id ? `${row.device.vendor || ""} ${row.device.model || row.device.profile_id}` : "未选择" }}</td>
            <td>{{ row.device ? `${row.device.channel_name || row.device.channel_id} · 站号 ${row.device.server_id}` : "未选择" }}</td>
            <td>{{ scopePointText(row) }}</td>
            <td><button class="ghost" :disabled="busy !== ''" @click="openScopeRow(row)">打开绑定 →</button></td>
          </tr></tbody>
        </table>
      </div>
      <view v-else-if="!directoryLoading && !ledgerLoadFailed" class="empty">这一层还没有设备。从左侧树里选一台设备逐台接入，或回第 2 步先建占位。</view>
      <view v-else-if="ledgerLoadFailed" class="empty">设备台账暂不可用，请根据上方错误重试；当前状态不能视为无设备。</view>
    </view>

    <template v-if="!scopeMode && !batchOpen">
    <view class="panel-head"><h3>{{ activeDeviceName }}</h3><span class="tag">设备</span><span class="tag" :class="activeDeviceBound ? 'ok' : 'warn'">{{ activeDeviceBound ? '已接入' : '未绑定' }}</span><span class="src">{{ selectedProfile ? `${selectedProfile.vendor} ${selectedProfile.model}` : '未选择 Profile' }} · {{ selectedChannelID || '未选通道' }} · {{ mappedCount }}/{{ profilePoints.length }} 点位</span></view>
    <view class="pipeline" aria-label="设备接入步骤">
      <template v-for="(item, index) in stages" :key="item.key">
        <button :disabled="busy !== ''" :class="{ active: stage === item.key, ok: stageComplete(item.key), cur: stage === item.key }" @click="stage = item.key">
          {{ item.key === 'points' ? `核对点位 ${mappedCount}/${profilePoints.length}` : item.label }}
        </button><i v-if="index < stages.length - 1">→</i>
      </template>
    </view>

    <view v-if="modelLoading" class="empty">正在读取 {{ selectedChannelID }} / 站号 {{ selectedServerID }} 的设备模型…</view>

    <template v-else-if="stage === 'profile'">
      <view class="acc-grid">
        <view class="acc-card">
          <view class="card-title">1 · 选型号 <span>{{ profileID ? '待保存选择可预览差异' : '尚未选择' }}</span></view>
          <button v-for="profile in availableProfiles" :key="profile.id" class="pick" :class="{ active: profile.id === profileID }" :disabled="!canConfigure || busy !== ''" @click="selectProfile(profile.id)">
            <strong>{{ profile.vendor }} {{ profile.model }}</strong><small>{{ profile.point_specs?.length || 0 }} 点位 · {{ profile.command_specs?.length || 0 }} 命令{{ profile.custom ? " · 自定义" : "" }}{{ profile.verified ? "" : " · 未现场验证" }}</small>
          </button>
          <view v-if="!availableProfiles.length" class="empty">后端没有可用 Profile；不会用演示型号替代。</view>
          <view v-if="model?.profile_errors?.length" class="error">自定义 Profile 未加载：{{ model.profile_errors.join("；") }}</view>
          <view class="actions">
            <button class="ghost" :disabled="!canConfigure || !profileID || !selectedChannelID || busy !== ''" @click="previewProfile">{{ busy === 'preview' ? '预览中…' : '预览 Profile 匹配' }}</button>
            <button class="primary" :disabled="!canConfigure || !applyPreview || !selectedChannelID || busy !== ''" @click="applyProfile">{{ busy === 'apply' ? '应用中…' : '应用 Profile' }}</button>
          </view>
          <p class="note">先预览同名点匹配与语义差异，再应用。应用只保存 Profile 与映射，不代表验证通过。</p>
        </view>
        <view class="acc-card profile-summary">
          <view class="card-title">Profile 声明</view>
          <view class="row"><span>采集点</span><span class="src">{{ selectedProfile ? `${profilePoints.length} 个 · 必需 ${requiredSpecs.size}` : "—" }}</span></view>
          <view class="row"><span>命令</span><span class="src">{{ selectedProfile?.command_specs?.length || 0 }} 个</span></view>
          <view class="row"><span>安全写点</span><span class="src">{{ safeCommands.map(command => command.name).join("、") || "未声明" }}</span></view>
          <view class="row"><span>约束</span><span class="src">未声明可写的点一律只读</span></view>
          <view v-if="selectedProfile" class="row"><span>Profile 状态</span><span class="chip" :class="selectedProfile.verified ? 'ok' : 'warn'">{{ selectedProfile.verified ? "已现场验证" : "未现场验证" }}</span></view>
          <view class="row"><span>当前对象</span><span class="src">{{ activeDeviceName }} · {{ selectedChannelID || "—" }} / {{ selectedServerID || "—" }}</span></view>
          <view v-if="config.verification_required && !config.verified" class="warning-box">Profile 已更换，控制命令等待重新验证；保存前请核对差异。</view>
          <p v-if="selectedProfile?.verification" class="note">依据：{{ selectedProfile.verification }}</p>
          <p v-for="note in selectedProfile?.notes || []" :key="note" class="note">{{ note }}</p>
          <p class="note">自动匹配、批量套用与验证都读取这一份 Profile；改型号后要重新核对点位。</p>
          <div class="actions"><button class="primary" @click="stage = 'channel'">下一步：选通道 →</button><button class="ghost" :disabled="!canConfigure || !selectedProfile" @click="autoMatchAndNext">按型号自动匹配</button></div>
        </view>
      </view>
      <view v-if="applyPreview" class="preview-panel">
        <p v-if="previewResponse?.create_points?.length" class="note">应用时按 Profile 新建 {{ previewResponse.create_points.length }} 个采集点；预览不保存，不写设备。</p>
        <p v-if="applyPreview.mismatched || applyPreview.scaled" class="warning-box">已有物理点与 Profile 不一致，先修正下列差异再应用。</p>
        <view class="panel-head"><strong>匹配差异</strong><span class="src">完全 {{ applyPreview.exact }} · 可换算 {{ applyPreview.scaled }} · 不等价 {{ applyPreview.mismatched }} · 缺失 {{ applyPreview.missing.length }}</span></view>
        <div class="table-wrap"><table><thead><tr><th>Profile 变量</th><th>物理点</th><th>结论</th><th>差异 / 原因</th></tr></thead>
          <tbody><tr v-for="point in applyPreview.points" :key="point.name"><td>{{ point.name }}</td><td>{{ pointName(point.point_id) }}</td><td><span class="chip" :class="verdictClass(point.verdict)">{{ verdictText(point.verdict) }}</span></td><td>{{ point.reason || "—" }}</td></tr></tbody>
        </table></div>
        <view v-if="applyPreview.blocked.length" class="warning-box"><strong>应用后仍不可用的控制：</strong><view v-for="reason in applyPreview.blocked" :key="reason">{{ reason }}</view></view>
      </view>
    </template>

    <template v-else-if="stage === 'channel'">
      <view class="acc-grid">
        <view class="acc-card">
          <view class="card-title">2 · 选通道 <button class="link" :disabled="!canConfigure" @click="openChannelForm(false)">＋ 新增通道</button></view>
          <button v-for="channel in channels" :key="channel.id" class="pick" :disabled="busy !== ''" :class="{ active: channel.id === selectedChannelID }" @click="selectChannel(channel.id)">
            <strong>{{ channelName(channel.id) }}</strong>
            <small>{{ (channel.protocol || "tcp").toUpperCase() }} · {{ channel.address || "—" }} · {{ channel.devices?.length || 0 }} 台设备 · {{ channel.persisted === false ? "未持久化" : "已持久化" }}</small>
          </button>
          <view v-if="!channels.length && !directoryLoading && !channelLoadFailed" class="empty">还没有通道。请新增真实 Modbus 通道；不会生成演示连接。</view>
          <view v-else-if="channelLoadFailed" class="empty">通道列表暂不可用，请根据上方错误重试；当前状态不能视为无通道。</view>
          <view v-if="selectedChannelID" class="station-form">
            <label>站号（1–247）<input v-model.number="serverInput" type="number" min="1" max="247"></label>
            <button class="ghost" :disabled="busy !== ''" @click="readStation">读取此站号</button>
            <button class="ghost" :disabled="!canConfigure" @click="openChannelForm(true)">编辑当前通道</button>
          </view>
        </view>
        <view class="acc-card">
          <view class="card-title">本站连接</view>
          <view class="row"><span>协议 / 地址</span><span>{{ selectedChannel?.protocol?.toUpperCase() || "—" }} · {{ selectedChannel?.address || "—" }}</span></view>
          <view class="row"><span>轮询周期</span><span>{{ selectedChannel?.period_ms ? `${selectedChannel.period_ms} ms` : "—" }}</span></view>
          <view class="row"><span>当前设备通信</span><span class="chip" :class="healthClass(selectedDevice?.health)">{{ healthText(selectedDevice?.health) }}</span></view>
          <view class="row"><span>成功率 / 平均延迟</span><span>{{ selectedDevice && selectedDevice.success_rate > 0 ? `${selectedDevice.success_rate.toFixed(1)}% / ${Math.round(selectedDevice.avg_latency_ms)} ms` : "—" }}</span></view>
          <p class="note">线路诊断在“验证”段执行，只读，不写设备。运行期累计统计在进程重启后归零。</p>
          <div class="actions"><button class="primary" @click="stage = 'points'">下一步：核对点位 →</button></div>
        </view>
      </view>
      <view v-if="channelForm" class="editor-panel">
        <view class="card-title">{{ channelForm.editing ? "编辑通道" : "新增通道" }} <span>保存后由后端持久化并重建采集</span></view>
        <view class="form-grid">
          <label>通道名称<input v-model="channelForm.description"></label>
          <label>协议<select v-model="channelForm.protocol"><option value="TCP">Modbus TCP</option><option value="RTU">Modbus RTU</option></select></label>
          <label v-if="channelForm.protocol === 'TCP'">目标地址<input v-model="channelForm.uri" placeholder="127.0.0.1:1502"></label>
          <template v-else>
            <label>串口<input v-model="channelForm.port"></label>
            <label>波特率<input v-model="channelForm.baud"></label>
            <label>数据位<select v-model.number="channelForm.data_bits"><option :value="5">5</option><option :value="6">6</option><option :value="7">7</option><option :value="8">8</option></select></label>
            <label>校验位<select v-model="channelForm.parity"><option value="N">N</option><option value="E">E</option><option value="O">O</option></select></label>
            <label>停止位<select v-model.number="channelForm.stop_bits"><option :value="1">1</option><option :value="2">2</option></select></label>
          </template>
          <label>轮询周期（ms）<input v-model.number="channelForm.poll_period" type="number" min="100"></label>
          <label>超时（ms）<input v-model.number="channelForm.timeout" type="number" min="100"></label>
        </view>
        <view class="actions"><button class="primary" :disabled="busy !== ''" @click="saveChannel">{{ busy === 'channel' ? '保存中…' : '保存通道' }}</button><button class="ghost" @click="channelForm = null">取消</button></view>
      </view>
    </template>

    <template v-else-if="stage === 'points'">
      <view class="acc-card wide">
        <view class="card-title">3 · 核对点位 <span>当前站号 {{ serverPoints.length }} 个物理点 · Profile 已映射 {{ mappedCount }}/{{ profilePoints.length }} · 必需 {{ requiredSpecs.size }}</span></view>
        <view v-if="!profileID" class="empty">先在“选型号”段选择并应用 Profile。</view>
        <template v-else>
          <div class="table-wrap"><table class="mini-table"><thead><tr><th>点位</th><th>要求</th><th>地址</th><th>类型</th><th>倍率</th><th>单位</th><th>枚举</th><th>读写</th><th>状态</th></tr></thead>
            <tbody><tr v-for="spec in profilePoints" :key="spec.name">
              <td><strong>{{ spec.name }}</strong></td>
              <td>{{ requiredSpecs.has(spec.name) ? "必需" : "可选" }}</td>
              <td><select :value="config.point_map[spec.name] || ''" :disabled="!canConfigure || busy !== ''" @change="updateMapping(spec.name, inputValue($event))">
                <option value="">未映射</option>
                <option v-for="point in serverPoints" :key="point.id" :value="point.id">{{ point.address }} · {{ point.name }}</option>
              </select><small>Profile 地址 {{ spec.address ?? '—' }} · {{ spec.function || '—' }}</small></td>
              <td>{{ spec.format || "—" }}</td>
              <td>×{{ spec.scale ?? 1 }}{{ spec.offset ? ` + ${spec.offset}` : '' }}</td>
              <td>{{ spec.unit || "—" }}</td>
              <td>{{ Object.entries(spec.enum || {}).map(([key, label]) => `${key} = ${label}`).join('；') || '—' }}</td>
              <td>{{ spec.read_only === false ? "读写" : "只读" }}</td>
              <td><span class="chip" :class="config.point_map[spec.name] ? 'ok' : requiredSpecs.has(spec.name) ? 'warn' : ''">{{ config.point_map[spec.name] ? "已映射" : requiredSpecs.has(spec.name) ? "待匹配" : "可选未映射" }}</span></td>
            </tr></tbody>
          </table></div>
          <view v-if="model?.missing?.length" class="warning-box">后端报告命令所需点未映射：{{ model.missing.join("、") }}</view>
          <view class="actions"><button class="ghost" :disabled="!canConfigure || busy !== ''" @click="autoMatch">按 Profile 名称匹配</button><button class="primary" :disabled="!canConfigure || !selectedChannelID || busy !== ''" @click="saveMapping">{{ busy === 'mapping' ? '保存中…' : '保存点位映射' }}</button><button class="ghost" :disabled="!canConfigure || !selectedChannelID" @click="openPointForm">＋ 新增物理点</button><button class="primary" @click="stage = 'verify'">下一步：验证 →</button></view>
          <p class="note">自动匹配只修改本地草稿，点击保存后才写后端。命令必需点、回读点或读写方向不满足时，后端会阻断对应命令。</p>
        </template>
      </view>
      <view v-if="pointForm" class="editor-panel">
        <view class="card-title">新增物理点 <span>{{ selectedChannelID }} / 站号 {{ selectedServerID }}</span></view>
        <view class="form-grid">
          <label>点位名称<input v-model="pointForm.description"></label>
          <label>地址<input v-model.number="pointForm.address" type="number" min="0" max="65535"></label>
          <label>类型<select v-model="pointForm.io_type"><option value="modbusHoldingRegister">保持寄存器</option><option value="modbusInputRegister">输入寄存器</option><option value="modbusCoil">线圈</option><option value="modbusDiscreteInput">离散输入</option></select></label>
          <label>数据格式<select v-model="pointForm.data_format"><option value="uint16">uint16</option><option value="int16">int16</option><option value="uint32">uint32</option><option value="int32">int32</option><option value="float32">float32</option></select></label>
          <label>缩放<input v-model.number="pointForm.scale" type="number" step="any"></label>
          <label>偏移<input v-model.number="pointForm.offset" type="number" step="any"></label>
          <label>单位<input v-model="pointForm.units"></label>
          <label class="check-label"><checkbox :checked="pointForm.read_only" @click="pointForm.read_only = !pointForm.read_only" /> 只读（取消勾选表示允许受控写入）</label>
        </view>
        <view class="actions"><button class="primary" :disabled="busy !== ''" @click="savePoint">{{ busy === 'point' ? '保存中…' : '保存物理点' }}</button><button class="ghost" @click="pointForm = null">取消</button></view>
      </view>
    </template>

    <template v-else>
      <view class="verification-grid">
        <view class="acc-card">
          <view class="card-title">4 · 分级验证 <span>L1/L2 只读；L3 默认不执行</span></view>
          <view class="row"><span>当前通信</span><span class="chip" :class="healthClass(selectedDevice?.health)">{{ healthText(selectedDevice?.health) }}</span></view>
          <view class="row"><span>已映射点</span><span>{{ mappedSpecs.length }} 个</span></view>
          <view class="row"><span>当前无值或质量非正常</span><span :class="qualityIssues ? 'warn-text' : 'ok-text'">{{ mappedSpecs.length ? `${qualityIssues} 个` : "—" }}</span></view>
          <view class="actions"><button class="ghost" :disabled="!canConfigure || busy !== '' || !selectedChannelID" @click="runVerification('L1')">{{ busy === 'verify-L1' ? '验证中…' : 'L1 连通验证（只读）' }}</button><button class="ghost" :disabled="!canConfigure || busy !== '' || !selectedChannelID || !profileID" @click="runVerification('L2')">{{ busy === 'verify-L2' ? '验证中…' : 'L2 必需点验证（只读）' }}</button></view>
          <view v-if="verificationResult" class="line-result">
            <view><strong>{{ verificationResult.level }}：{{ verificationResult.passed ? "通过" : "未通过" }}</strong></view>
            <view v-if="verificationResult.record">{{ verificationResult.record.actor }} · {{ verificationResult.record.at }} · Profile {{ verificationResult.record.profile_version }}</view>
            <view v-for="reason in verificationResult.reasons" :key="reason">{{ reason }}</view>
            <view v-if="verificationResult.command">命令状态：{{ commandStateText(verificationResult.command.state) }}{{ verificationResult.command.reason ? ` · ${verificationResult.command.reason}` : "" }}</view>
            <view v-if="verificationResult.record_error" class="warn-text">验证已执行，但历史记录保存失败。</view>
          </view>
          <view class="warning-box l3-box">
            <strong>L3 安全点写回读</strong>
            <p>只提交 Profile 显式标记 L3_SAFE 的固定值、无参数且有回读的命令。普通启停与功率命令不会出现在这里。</p>
            <label>安全命令<select v-model="l3CommandID" :disabled="!canConfigure || !canCommand || busy !== '' || !safeCommands.length"><option v-for="command in safeCommands" :key="command.id" :value="command.id">{{ command.name }}</option></select></label>
            <p v-if="l3Command">{{ activeDeviceName }} · {{ l3Command.point }} → {{ l3Command.value }}；回读 {{ l3Command.readback }} = {{ l3Command.readback_value ?? 0 }}，容差 {{ l3Command.readback_tolerance ?? 0 }}，截止 {{ l3Command.timeout_ms || 5000 }} ms。</p>
            <label class="check-label"><checkbox :checked="l3Confirmed" :disabled="!canConfigure || !canCommand || !verificationBasis || busy !== '' || !safeCommands.length" @click="l3Confirmed = !l3Confirmed" /> 我确认本次允许向所选安全点写入 Profile 固定值并等待回读</label>
            <button class="ghost" :disabled="!canConfigure || !canCommand || !verificationBasis || !l3Confirmed || !l3CommandID || busy !== ''" @click="runVerification('L3')">{{ busy === 'verify-L3' ? '写回读中…' : '执行一次 L3 写回读' }}</button>
            <p v-if="!safeCommands.length">当前 Profile 没有声明可安全写回读的命令，L3 不可执行。</p><p v-else-if="!canConfigure || !canCommand">L3 需要配置及额外 Command 权限。</p>
            <p v-if="l3RequestID" class="l3-request">请求：{{ l3RequestID }} <button class="ghost" :disabled="busy !== ''" @click="queryL3">查询 L3 结果（不重发）</button></p>
            <p>每次确认只执行声明的单次写回读；结果未知时不自动重试或回退。刷新后可用原请求身份查询。</p>
          </view>
        </view>
        <view class="acc-card">
          <view class="card-title">现场验证记录</view>
          <label>验证人<input v-model="config.verifier" :disabled="!canConfigure"></label>
          <label>验证日期<input v-model="config.verified_at" type="date" :disabled="!canConfigure"></label>
          <label>设备序列号<input v-model="config.device_serial" :disabled="!canConfigure"></label>
          <label>验证说明<textarea v-model="config.verification" :disabled="!canConfigure" rows="3" placeholder="地址、缩放、字序、读写方向与真机核对结论"></textarea></label>
          <label class="check-label"><checkbox :checked="config.verified" :disabled="!canConfigure" @click="config.verified = !config.verified" /> 已完成现场逐点核对</label>
          <view class="actions"><button class="primary" :disabled="!canConfigure || !selectedChannelID || busy !== ''" @click="saveVerification">{{ busy === 'verification' ? '保存中…' : '保存验证记录' }}</button></view>
          <p class="note">保存的是人工核对结论，不会触发设备写入。模拟器或当前快照不能替代 M5 真机签收，也不代表项目可交付或已投运。</p>
        </view>
      </view>
      <view v-if="verificationResult?.points?.length" class="quality-panel"><view class="card-title">L2 必需点结果</view><div class="table-wrap"><table><thead><tr><th>Profile 变量</th><th>质量</th><th>观测时间</th><th>结论</th></tr></thead><tbody><tr v-for="point in verificationResult.points" :key="point.name"><td>{{ point.name }}</td><td>{{ point.quality || '—' }}</td><td>{{ point.source_time || '—' }}</td><td><span class="chip" :class="point.passed ? 'ok' : 'warn'">{{ point.passed ? '通过' : point.reason }}</span></td></tr></tbody></table></div></view>
      <view v-if="verificationHistory.length" class="quality-panel"><view class="card-title">自动验证历史 <span>与人工签收分开记录</span></view><div class="table-wrap"><table><thead><tr><th>时间</th><th>级别</th><th>操作者</th><th>结果</th><th>原因</th></tr></thead><tbody><tr v-for="record in verificationHistory.slice().reverse().slice(0, 5)" :key="`${record.at}-${record.level}`"><td>{{ record.at }}</td><td>{{ record.level }}</td><td>{{ record.actor }}</td><td><span class="chip" :class="record.current && record.passed ? 'ok' : 'warn'">{{ !record.current ? '已失效（配置已改变）' : record.passed ? '通过' : '未通过' }}</span></td><td>{{ record.reasons?.join('；') || '—' }}</td></tr></tbody></table></div></view>
      <view class="quality-panel">
        <view class="card-title">已映射点当前值与质量 <span>随工作台实时快照更新；无值显示 —</span></view>
        <div v-if="mappedSpecs.length" class="table-wrap"><table><thead><tr><th>Profile 变量</th><th>物理点</th><th>当前值</th><th>质量</th><th>观测时间</th></tr></thead>
          <tbody><tr v-for="spec in mappedSpecs" :key="spec.name"><td>{{ spec.name }}</td><td>{{ pointName(config.point_map[spec.name]) }}</td><td>{{ sampleText(config.point_map[spec.name]) }}</td><td><span class="chip" :class="sampleQuality(config.point_map[spec.name]) === 'good' ? 'ok' : 'warn'">{{ qualityText(sampleQuality(config.point_map[spec.name])) }}</span></td><td>{{ sampleTime(config.point_map[spec.name]) }}</td></tr></tbody>
        </table></div>
        <view v-else class="empty">还没有已保存的映射点；先完成 Profile 与点位保存。</view>
      </view>
    </template>
    </template>
    </template>
    <div class="actions"><button class="primary" @click="emit('navigate', 'strategy')">下一步：配策略 →</button></div>
    </template>
    <view class="tool-line"><span>数据从哪来</span><button class="ghost" @click="emit('tool', 'channels')">通道 →</button><button class="ghost" @click="emit('tool', 'points')">点位表 →</button><button class="ghost" @click="emit('tool', 'profiles')">型号库 →</button></view>
    <view class="tool-line"><span>接不进来时</span><button class="ghost" @click="emit('tool', 'health')">通信健康与报文 →</button></view>
    <view class="tool-line"><span>接完要确认</span><button class="ghost" @click="emit('tool', 'verify')">设备验证 →</button></view>
    </section>
  </view>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue"
import MemberRoles from "./MemberRoles.vue"
import BatchAccess from "./BatchAccess.vue"
import { ApiError, request } from "../../api/client"
import { commandStateText } from "../../pages/overview/overview"
import { nameOf, point, valueText } from "../../types/api"
import type { Snapshot, StateValue, Node } from "../../types/api"
import { automaticPointMap, requiredProfilePoints, canCommandAccess, canConfigureAccess, prepareModelSave, safeVerificationCommands, verificationError } from "./access"
import { boundBusinessIDs } from "./design"
import type { EmsChannel, LedgerDevice, Profile, ProfileApplyResponse, ProfileApplyResult, UnitModel, UnitModelConfig, UnitPoint } from "./access"

const props = withDefaults(defineProps<{ snapshot: Snapshot | null; values: StateValue[]; selectedNodeId?: string; batchNodeIds?: string[] }>(), { selectedNodeId: "", batchNodeIds: () => [] })
const emit = defineEmits<{
  (event: "refresh"): void
  (event: "status", value: { channels: number; devices: number; bound: number; verified: number }): void
  (event: "tool", value: string): void
  (event: "navigate", value: "tree" | "strategy"): void
}>()

type Stage = "profile" | "channel" | "points" | "verify"
type ChannelForm = { editing: string; parent: string; description: string; protocol: "TCP" | "RTU"; uri: string; port: string; baud: string; data_bits: number; parity: string; stop_bits: number; poll_period: number; timeout: number; disabled: boolean }
type PointForm = { parent: string; description: string; server_id: number; address: number; io_type: string; data_format: string; read_only: boolean; scale: number; offset: number; units: string; disabled: boolean }
type VerificationResult = {
  level: "L1" | "L2" | "L3"
  passed: boolean
  reasons: string[]
  recorded: boolean
  record_error?: string
  record?: VerificationRecord
  line?: { connected: boolean; stations: { server_id: number; responded: boolean; error: string }[] }
  points?: { name: string; point_id?: string; passed: boolean; quality?: string; source_time?: string; reason?: string }[]
  command?: { request_id: string; state: string; reason?: string }
}
type VerificationRecord = { at: string; actor: string; level: string; passed: boolean; reasons?: string[]; current?: boolean; profile_version?: string; basis?: string; request_id?: string }
type ScopeRow = { id: string; name: string; bound: boolean; device?: LedgerDevice }

const stages: { key: Stage; label: string }[] = [{ key: "profile", label: "选型号" }, { key: "channel", label: "选通道" }, { key: "points", label: "核对点位" }, { key: "verify", label: "验证" }]
const channelDirectory = ref<EmsChannel[]>([])
// Empty persisted channels are not yet part of the acquisition runtime.
const channels = computed<EmsChannel[]>(() => (props.snapshot?.nodes || []).filter(node => node.type === "modbus").map(node => ({
  id: node.id, node_id: node.id, persisted: true,
  protocol: point(node, "protocol")?.text || "TCP",
  address: point(node, "uri")?.text || point(node, "port")?.text || "",
  period_ms: point(node, "pollPeriod")?.value || 1000,
  ...channelDirectory.value.find(channel => channel.id === node.id),
})))
const profiles = ref<Profile[]>([])
const devices = ref<LedgerDevice[]>([])
const directoryLoading = ref(false)
const directoryError = ref("")
const channelLoadFailed = ref(false)
const ledgerLoadFailed = ref(false)
const batchOpen = ref(false)
const batchSelection = ref<string[]>([])
const stage = ref<Stage>("profile")
const selectedChannelID = ref("")
const selectedServerID = ref(1)
const serverInput = ref(1)
const model = ref<UnitModel | null>(null)
const config = ref<UnitModelConfig>({ name: "站号 1", profile_id: "", point_map: {}, verified: false })
const busy = ref("")
const error = ref("")
const notice = ref("")
const previewResponse = ref<ProfileApplyResponse | null>(null)
const channelForm = ref<ChannelForm | null>(null)
const pointForm = ref<PointForm | null>(null)
const verificationResult = ref<VerificationResult | null>(null)
const verificationHistory = ref<VerificationRecord[]>([])
const l3CommandID = ref("")
const l3Confirmed = ref(false)
const verificationBasis = ref("")
const l3RequestID = ref("")
const l3RequestKey = computed(() => `l3-request:${props.snapshot?.user.user_id || ''}:${selectedKey.value}`)
const l3Command = computed(() => safeCommands.value.find(command => command.id === l3CommandID.value))
let modelRequest = 0
let previewRequest = 0
let verificationRequest = 0

const canConfigure = computed(() => canConfigureAccess(props.snapshot))
const canCommand = computed(() => canCommandAccess(props.snapshot))
const selectedUnit = computed(() => props.snapshot?.nodes.find(node => node.id === props.selectedNodeId && (node.type === "unit" || node.type === "energyUnit")) || null)
const selectedBusinessNode = computed(() => props.snapshot?.nodes.find(node => node.id === props.selectedNodeId) || null)
const pendingBusinessID = ref("")
const selectedBusinessDevice = computed(() => selectedBusinessNode.value?.type === "device" ? selectedBusinessNode.value : props.snapshot?.nodes.find(node => node.id === pendingBusinessID.value && node.type === "device") || null)
const deviceDetailOpen = ref(false)
const scopeMode = computed(() => !selectedUnit.value && !selectedBusinessDevice.value && !deviceDetailOpen.value)
const selectedScopeName = computed(() => selectedBusinessNode.value ? nameOf(selectedBusinessNode.value) : "当前层")
const businessDevices = computed(() => (props.snapshot?.nodes || []).filter(node => node.type === "device" && node.parent !== "root"))
const persistedBoundIDs = computed(() => boundBusinessIDs(props.snapshot?.nodes || []))
const boundBusinessCount = computed(() => businessDevices.value.filter(node => isBusinessBound(node.id)).length)
const scopeRows = computed<ScopeRow[]>(() => {
  const businessIDs = new Set(businessDevices.value.map(node => node.id))
  const rows = businessDevices.value.filter(node => inSelectedScope(node)).map(node => {
    const device = devices.value.find(item => item.business_node_id === node.id)
    return { id: node.id, name: nameOf(node), bound: isBusinessBound(node.id), device }
  })
  if (selectedBusinessNode.value?.id === props.snapshot?.roots[0]) {
    for (const device of devices.value) {
      if (device.business_node_id && businessIDs.has(device.business_node_id)) continue
      rows.push({ id: deviceKey(device), name: device.business_node_name || device.name, bound: device.bound ?? !!device.profile_id, device })
    }
  }
  return rows
})
const scopeBoundCount = computed(() => scopeRows.value.filter(row => row.bound).length)
const activeDeviceName = computed(() => selectedBusinessDevice.value ? nameOf(selectedBusinessDevice.value) : selectedDevice.value?.business_node_name || config.value.name || `站号 ${selectedServerID.value}`)
const activeDeviceBound = computed(() => selectedBusinessDevice.value ? isBusinessBound(selectedBusinessDevice.value.id) : selectedDevice.value?.bound ?? !!selectedDevice.value?.profile_id)
const selectedChannel = computed(() => channels.value.find(channel => channel.id === selectedChannelID.value) || null)
const selectedDevice = computed(() => devices.value.find(device => device.channel_id === selectedChannelID.value && device.server_id === selectedServerID.value && (!selectedBusinessDevice.value || device.business_node_id === selectedBusinessDevice.value.id)) || null)
const selectedKey = computed(() => selectedChannelID.value ? `${selectedChannelID.value}/${selectedServerID.value}` : "")
const profileID = computed(() => config.value.profile_id)
const availableProfiles = computed(() => model.value?.profiles?.length ? model.value.profiles : profiles.value)
const selectedProfile = computed(() => availableProfiles.value.find(profile => profile.id === profileID.value) || null)
const safeCommands = computed(() => selectedProfile.value ? safeVerificationCommands(selectedProfile.value) : [])
const profilePoints = computed(() => selectedProfile.value?.point_specs || [])
const serverPoints = computed(() => (model.value?.points || []).filter(point => point.server_id === selectedServerID.value))
const mappedCount = computed(() => Object.values(config.value.point_map || {}).filter(Boolean).length)
const mappedSpecs = computed(() => profilePoints.value.filter(spec => config.value.point_map[spec.name]))
const requiredSpecs = computed(() => requiredProfilePoints(selectedProfile.value))
const missingRequired = computed(() => [...new Set([
  ...(model.value?.missing || []),
  ...[...requiredSpecs.value].filter(name => !config.value.point_map[name]),
])])
const applyPreview = computed<ProfileApplyResult | null>(() => {
  const result = previewResponse.value?.results?.[0]
  return result && result.profile_id === profileID.value && result.server === selectedServerID.value ? result : null
})
const modelLoading = computed(() => busy.value === "model")
const qualityIssues = computed(() => mappedSpecs.value.filter(spec => sampleQuality(config.value.point_map[spec.name]) !== "good").length)
const mappingDirty = computed(() => JSON.stringify(config.value.point_map || {}) !== JSON.stringify(model.value?.model?.point_map || {}))
const profileDirty = computed(() => config.value.profile_id !== (model.value?.model?.profile_id || ""))
const stationDirty = computed(() => serverInput.value !== selectedServerID.value)

function inputValue(event: Event) { return (event.target as HTMLInputElement | HTMLSelectElement).value }
function inSelectedScope(node: Node) {
  if (!selectedBusinessNode.value) return true
  const byID = new Map((props.snapshot?.nodes || []).map(item => [item.id, item]))
  let current: Node | undefined = node
  const seen = new Set<string>()
  while (current && !seen.has(current.id)) {
    if (current.id === selectedBusinessNode.value.id) return true
    seen.add(current.id)
    current = byID.get(current.parent)
  }
  return false
}
function stageComplete(key: Stage) {
  if (key === "profile") return !!profileID.value
  if (key === "channel") return !!selectedChannelID.value
  if (key === "points") return profilePoints.value.length > 0 && mappedCount.value === profilePoints.value.length
  const latest = verificationHistory.value.filter(record => record.level === 'L2').slice(-1)[0]
  return !mappingDirty.value && !profileDirty.value && !!latest?.passed && !!latest?.current
}
function isBusinessBound(id: string) {
  const device = devices.value.find(device => device.business_node_id === id)
  return device ? device.bound ?? !!device.profile_id : persistedBoundIDs.value.has(id)
}
function scopePointText(row: ScopeRow) { return row.device ? `${row.device.point_count} 个物理点` : "0 / 未映射" }
function openScopeRow(row: ScopeRow) {
  if (row.device) { void openDevice(row.device); return }
  if (row.bound) { void openBusinessBinding(row.id); return }
  beginUnbound(row.id)
}
function beginUnbound(id: string) {
  ++modelRequest; ++previewRequest; ++verificationRequest
  pendingBusinessID.value = id
  deviceDetailOpen.value = true
  batchOpen.value = false
  selectedChannelID.value = ""
  selectedServerID.value = serverInput.value = 1
  model.value = null
  config.value = { name: props.snapshot?.nodes.find(node => node.id === id)?.points.find(point => point.type === "description")?.text || "设备", profile_id: "", business_node_id: id, point_map: {}, verified: false }
  previewResponse.value = null
  verificationResult.value = null
  verificationHistory.value = []
  error.value = notice.value = busy.value = ""
  stage.value = "profile"
}
async function openBusinessBinding(id: string) {
  for (const channel of props.snapshot?.nodes || []) {
    if (channel.type !== "modbus") continue
    for (const item of channel.points) {
      if (item.type !== "easyemsUnitModel" || (item.tombstone || 0) % 2 !== 0 || !item.text) continue
      try {
        if ((JSON.parse(item.text) as { business_node_id?: string }).business_node_id !== id) continue
        const station = Number(item.key)
        if (!Number.isInteger(station) || station < 1 || station > 247) return
        deviceDetailOpen.value = true
        stage.value = "profile"
        selectedChannelID.value = channel.id
        selectedServerID.value = station
        serverInput.value = station
        model.value = null
        await loadModel()
        return
      } catch { /* Try another persisted binding instead of guessing a station. */ }
    }
  }
}
function autoMatchAndNext() { autoMatch(); stage.value = "points" }
function messageOf(cause: unknown) {
  const message = cause instanceof Error ? cause.message : "操作失败"
  return ({ profile_preview_required: "先重新预览并应用 Profile，再保存映射或验证记录", profile_preview_changed: "Profile、通道或映射已改变，请重新预览后应用", mapping_version_conflict: "映射已被其他编辑修改，请刷新后重新核对；当前草稿未覆盖后端", profile_points_differ: "已有物理点与 Profile 不一致，请先修正预览差异", station_already_bound: "此站号已有设备绑定，请选择空闲站号", device_already_bound: "此设备已有绑定，请打开原绑定修改" } as Record<string, string>)[message] || message
}
function blockStationChange() {
  if (!stationDirty.value) return false
  error.value = "站号已修改，请先读取此站号"
  return true
}
function requireChannel() {
  if (selectedChannelID.value) return false
  error.value = "先选择或新增通道"
  return true
}
function deviceKey(device: LedgerDevice) { return `${device.channel_id}/${device.server_id}` }
function channelName(id: string) { return props.snapshot?.nodes.find(node => node.id === id)?.points.find(item => item.type === "description")?.text || id }
function healthText(value?: string) { return ({ healthy: "在线", degraded: "不稳定", offline: "离线", unknown: "未知" } as Record<string, string>)[value || "unknown"] || value || "未知" }
function healthClass(value?: string) { return value === "healthy" ? "ok" : value === "offline" || value === "degraded" ? "warn" : "" }
function verdictText(value: string) { return ({ exact: "完全一致", scaled: "缩放/偏移可换算", mismatch: "语义不等价", missing: "缺失" } as Record<string, string>)[value] || value }
function verdictClass(value: string) { return value === "exact" ? "ok" : value === "mismatch" || value === "missing" ? "warn" : "" }
function pointName(id?: string) { const item = model.value?.points.find(point => point.id === id); return item ? `${item.name} · ${item.address}` : "—" }
function sample(pointID: string) { return props.values.find(value => value.point_id === pointID) }
function sampleQuality(pointID: string) { return sample(pointID)?.quality || "uninitialized" }
function qualityText(value: string) { return ({ good: "有效", stale: "过期", offline: "离线", invalid: "无效", uninitialized: "无值" } as Record<string, string>)[value] || value }
function sampleText(pointID: string) { const value = sample(pointID); const item = model.value?.points.find(point => point.id === pointID); return `${valueText(value)}${value && item?.unit ? ` ${item.unit}` : ""}` }
function sampleTime(pointID: string) { const time = sample(pointID)?.source_time; if (!time) return "—"; const date = new Date(time); return Number.isNaN(date.getTime()) ? "—" : date.toLocaleString("zh-CN", { hour12: false }) }

async function loadDirectory() {
  directoryLoading.value = true
  directoryError.value = ""
  const results = await Promise.allSettled([
    request<{ channels: EmsChannel[] }>("/ems/channels"),
    request<{ profiles: Profile[] }>("/ems/profiles"),
    request<{ devices: LedgerDevice[] }>("/device-ledger"),
  ])
  const errors: string[] = []
  if (results[0].status === "fulfilled") {
    channelDirectory.value = results[0].value.channels || []
    channelLoadFailed.value = false
  } else {
    channelLoadFailed.value = true
    errors.push(`通道读取失败：${messageOf(results[0].reason)}`)
  }
  if (results[1].status === "fulfilled") profiles.value = results[1].value.profiles || []
  else errors.push(`Profile 读取失败：${messageOf(results[1].reason)}`)
  if (results[2].status === "fulfilled") {
    devices.value = results[2].value.devices || []
    ledgerLoadFailed.value = false
  } else {
    ledgerLoadFailed.value = true
    errors.push(`设备台账读取失败：${messageOf(results[2].reason)}`)
  }
  directoryError.value = errors.join("；")
  directoryLoading.value = false
  emit("status", { channels: channels.value.length, devices: devices.value.length, bound: devices.value.filter(device => device.bound ?? !!device.profile_id).length, verified: devices.value.filter(device => device.verified).length })
}

async function readStation() {
  if (!Number.isInteger(serverInput.value) || serverInput.value < 1 || serverInput.value > 247) {
    error.value = "站号必须是 1–247 的整数"
    return
  }
  selectedServerID.value = serverInput.value
  await loadModel()
}

async function loadModel() {
  if (!selectedChannelID.value || !Number.isInteger(selectedServerID.value) || selectedServerID.value < 1 || selectedServerID.value > 247) {
    error.value = "先选择通道并填写 1–247 的站号"
    return
  }
  const requestID = ++modelRequest
  ++previewRequest
  ++verificationRequest
  const channelID = selectedChannelID.value
  const serverID = selectedServerID.value
  busy.value = "model"
  error.value = ""
  previewResponse.value = null
  verificationResult.value = null
  verificationHistory.value = []
  l3Confirmed.value = false
  try {
    const result = await request<UnitModel>(`/modbus/${encodeURIComponent(channelID)}/units/${serverID}/model`)
    if (requestID !== modelRequest || channelID !== selectedChannelID.value || serverID !== selectedServerID.value) return
    if (result.model && selectedBusinessDevice.value && result.model.business_node_id !== selectedBusinessDevice.value.id) {
      model.value = null
      error.value = "此站号已有其他设备配置，请选择空闲站号"
      return
    }
    model.value = result
    config.value = result.model ? { ...result.model, point_map: { ...result.model.point_map } } : { ...config.value, business_node_id: selectedBusinessDevice.value?.id, point_map: {}, verified: false, verifier: "", verified_at: "", device_serial: "", verification: "" }
    await loadVerificationHistory()
  } catch (cause) {
    if (requestID === modelRequest) error.value = `设备模型读取失败：${messageOf(cause)}`
  } finally {
    if (requestID === modelRequest) busy.value = ""
  }
}

async function ensureSelection() {
  if (selectedBusinessDevice.value && !isBusinessBound(selectedBusinessDevice.value.id)) return
  if (selectedChannelID.value && channels.value.some(channel => channel.id === selectedChannelID.value)) return
  const preferred = devices.value.find(device => device.business_node_id === props.selectedNodeId) || devices.value[0]
  selectedChannelID.value = preferred?.channel_id || channels.value[0]?.id || ""
  selectedServerID.value = preferred?.server_id || channels.value[0]?.devices?.find(device => device.server_id !== undefined)?.server_id || 1
  serverInput.value = selectedServerID.value
  if (selectedChannelID.value) await loadModel()
}

async function reload() {
  error.value = ""
  notice.value = ""
  await loadDirectory()
  await ensureSelection()
  if (selectedChannelID.value) await loadModel()
}

async function reloadAfterWrite(message: string) {
  await loadDirectory()
  await loadModel()
  notice.value = message
  emit("refresh")
}

function openBatch() {
  batchSelection.value = [...new Set([...props.batchNodeIds, props.selectedNodeId].filter(id => {
    const node = props.snapshot?.nodes.find(item => item.id === id)
    return node?.type === "device" && node.parent !== "root" && !boundBusinessIDs(props.snapshot?.nodes || []).has(id)
  }))]
  batchOpen.value = true
}

async function reloadAfterBatch() {
  await loadDirectory()
  if (selectedChannelID.value) await loadModel()
  emit("refresh")
}

async function openDevice(device: LedgerDevice) {
  deviceDetailOpen.value = true
  stage.value = "profile"
  selectedChannelID.value = device.channel_id
  selectedServerID.value = device.server_id
  serverInput.value = device.server_id
  model.value = null
  notice.value = ""
  await loadModel()
}

async function startNewUnit() {
  deviceDetailOpen.value = true
  selectedChannelID.value = channels.value[0]?.id || ""
  selectedServerID.value = 1
  serverInput.value = 1
  model.value = null
  config.value = { name: "站号 1", profile_id: "", point_map: {}, verified: false }
  stage.value = "profile"
  if (selectedChannelID.value) await loadModel()
}

async function selectChannel(id: string) {
  selectedChannelID.value = id
  selectedServerID.value = channels.value.find(channel => channel.id === id)?.devices?.find(device => device.server_id !== undefined)?.server_id || 1
  serverInput.value = selectedServerID.value
  channelForm.value = null
  await loadModel()
}

function selectProfile(id: string) {
  const profile = availableProfiles.value.find(item => item.id === id)
  config.value = {
    ...config.value,
    profile_id: id,
    point_map: profile ? automaticPointMap(profile, model.value?.points || [], selectedServerID.value) : {},
    verified: false,
  }
  previewResponse.value = null
  notice.value = ""
}

async function previewProfile() {
  if (requireChannel()) return
  if (!profileID.value || blockStationChange()) return
  const requestID = ++previewRequest
  const channelID = selectedChannelID.value
  const serverID = selectedServerID.value
  const selectedProfileID = profileID.value
  busy.value = "preview"
  error.value = ""
  notice.value = ""
  try {
    const result = await request<ProfileApplyResponse>(`/modbus/${encodeURIComponent(channelID)}/units/apply`, "POST", { profile_id: selectedProfileID, servers: [serverID], dry_run: true, ...(selectedBusinessDevice.value ? { create_points: true, business_node_id: selectedBusinessDevice.value.id } : {}) })
    if (requestID !== previewRequest || channelID !== selectedChannelID.value || serverID !== selectedServerID.value || selectedProfileID !== profileID.value) return
    previewResponse.value = result
    const preview = result.results[0]
    if (preview) config.value.point_map = Object.fromEntries(preview.points.filter(point => point.point_id).map(point => [point.name, point.point_id!]))
  } catch (cause) {
    if (requestID === previewRequest) error.value = `Profile 预览失败：${messageOf(cause)}`
  } finally {
    if (requestID === previewRequest) busy.value = ""
  }
}

async function applyProfile() {
  if (requireChannel()) return
  if (!applyPreview.value || blockStationChange()) return
  busy.value = "apply"
  error.value = ""
  notice.value = ""
  try {
    const result = await request<ProfileApplyResponse>(`/modbus/${encodeURIComponent(selectedChannelID.value)}/units/apply`, "POST", { profile_id: profileID.value, servers: [selectedServerID.value], dry_run: false, ...(selectedBusinessDevice.value ? { create_points: true, business_node_id: selectedBusinessDevice.value.id, preview_version: previewResponse.value?.preview_version } : {}) })
    await reloadAfterWrite(`Profile 已应用到 ${result.applied} 个站号；请继续核对点位与验证`)
  } catch (cause) { error.value = `Profile 应用失败：${messageOf(cause)}` } finally { busy.value = "" }
}

function updateMapping(name: string, id: string) {
  const next = { ...config.value.point_map }
  if (id) next[name] = id
  else delete next[name]
  config.value = { ...config.value, point_map: next, verified: false }
  notice.value = ""
}

function autoMatch() {
  if (!selectedProfile.value) return
  config.value = { ...config.value, point_map: automaticPointMap(selectedProfile.value, model.value?.points || [], selectedServerID.value), verified: false }
  notice.value = `已按名称匹配 ${mappedCount.value} 个点位，尚未保存`
}

async function saveMapping() {
  if (requireChannel() || blockStationChange()) return
  if (!profileID.value) { error.value = "先选择设备 Profile"; return }
  if (selectedBusinessDevice.value && (!model.value?.model || profileDirty.value)) { error.value = "先预览并应用 Profile，再保存点位映射"; return }
  busy.value = "mapping"
  error.value = ""
  notice.value = ""
  try {
    const result = await request<UnitModel>(`/modbus/${encodeURIComponent(selectedChannelID.value)}/units/${selectedServerID.value}/model`, "PUT", prepareModelSave(config.value))
    model.value = result
    config.value = result.model ? { ...result.model, point_map: { ...result.model.point_map } } : config.value
    await reloadAfterWrite("点位映射已保存")
  } catch (cause) { error.value = `点位映射保存失败：${messageOf(cause)}` } finally { busy.value = "" }
}

function openChannelForm(edit: boolean) {
  const channel = edit ? selectedChannel.value : null
  const node = channel ? props.snapshot?.nodes.find(item => item.id === channel.id) : null
  channelForm.value = {
    editing: channel?.id || "",
    parent: node?.parent || props.snapshot?.roots[0] || "",
    description: node ? nameOf(node) : "",
    protocol: (node ? point(node, "protocol")?.text : channel?.protocol)?.toUpperCase() === "RTU" ? "RTU" : "TCP",
    uri: node ? point(node, "uri")?.text || channel?.address || "127.0.0.1:1502" : "127.0.0.1:1502",
    port: node ? point(node, "port")?.text || "COM1" : "COM1",
    baud: node ? point(node, "baud")?.text || String(point(node, "baud")?.value || 9600) : "9600",
    data_bits: node ? point(node, "dataBits")?.value || 8 : 8,
    parity: node ? point(node, "parity")?.text || "N" : "N",
    stop_bits: node ? point(node, "stopBits")?.value || 1 : 1,
    poll_period: node ? point(node, "pollPeriod")?.value || 1000 : 1000,
    timeout: node ? point(node, "timeout")?.value || 1000 : 1000,
    disabled: node ? (point(node, "disabled")?.value || 0) !== 0 : false,
  }
}

async function saveChannel() {
  const form = channelForm.value
  if (!form) return
  if (!form.description.trim()) { error.value = "通道名称不能为空"; return }
  if (form.protocol === "TCP" && !form.uri.trim()) { error.value = "TCP 目标地址不能为空"; return }
  if (form.protocol === "RTU" && !form.port.trim()) { error.value = "RTU 串口不能为空"; return }
  busy.value = "channel"
  error.value = ""
  notice.value = ""
  try {
    const { editing, ...payload } = form
    const result = await request<{ id?: string; node_id?: string }>(editing ? `/modbus/${encodeURIComponent(editing)}` : "/modbus", editing ? "PUT" : "POST", { ...payload, description: payload.description.trim(), baud: String(payload.baud) })
    selectedChannelID.value = editing || result.id || result.node_id || selectedChannelID.value
    channelForm.value = null
    await reloadAfterWrite("通道已保存")
  } catch (cause) { error.value = `通道保存失败：${messageOf(cause)}` } finally { busy.value = "" }
}

function openPointForm() {
  if (requireChannel()) return
  pointForm.value = { parent: selectedChannelID.value, description: "", server_id: selectedServerID.value, address: 0, io_type: "modbusHoldingRegister", data_format: "uint16", read_only: true, scale: 1, offset: 0, units: "", disabled: false }
}

async function savePoint() {
  if (requireChannel() || blockStationChange()) return
  const form = pointForm.value
  if (!form) return
  if (!form.description.trim()) { error.value = "点位名称不能为空"; return }
  if (!Number.isInteger(form.address) || form.address < 0 || form.address > 65535) { error.value = "点位地址必须是 0–65535 的整数"; return }
  if (!Number.isFinite(form.scale) || form.scale === 0) { error.value = "缩放必须是非零数值"; return }
  busy.value = "point"
  error.value = ""
  notice.value = ""
  try {
    await request(`/modbus/${encodeURIComponent(form.parent)}/io`, "POST", { ...form, description: form.description.trim() })
    pointForm.value = null
    await reloadAfterWrite("物理点已创建；请核对 Profile 映射并保存")
  } catch (cause) { error.value = `物理点保存失败：${messageOf(cause)}` } finally { busy.value = "" }
}

async function loadVerificationHistory() {
  if (!selectedChannelID.value) return
  const channelID = selectedChannelID.value
  const serverID = selectedServerID.value
  try {
    const result = await request<{ records: VerificationRecord[]; basis: string }>(`/modbus/${encodeURIComponent(channelID)}/units/${serverID}/verify`)
    if (channelID === selectedChannelID.value && serverID === selectedServerID.value) {
      verificationHistory.value = result.records || []
      verificationBasis.value = result.basis || ""
    }
  } catch {
    if (channelID === selectedChannelID.value && serverID === selectedServerID.value) verificationBasis.value = ""
  }
}

async function queryL3() {
  const channelID = selectedChannelID.value, serverID = selectedServerID.value, id = l3RequestID.value
  if (!id || !channelID) return
  const sequence = ++verificationRequest
  busy.value = "query-L3"
  error.value = ""
  try {
    const command = await request<NonNullable<VerificationResult['command']>>(`/modbus/${encodeURIComponent(channelID)}/units/${serverID}/commands/${encodeURIComponent(id)}`)
    await loadVerificationHistory()
    if (sequence !== verificationRequest || channelID !== selectedChannelID.value || serverID !== selectedServerID.value) return
    const record = verificationHistory.value.find(record => record.request_id === id)
    verificationResult.value = { level: 'L3', passed: command.state === 'confirmed' && !!record?.current, reasons: record?.current ? record.reasons || [] : ['历史命令结果不代表当前配置验证通过'], recorded: !!record, record, command }
  } catch (cause) { if (sequence === verificationRequest) error.value = `L3 结果查询不可用：${messageOf(cause)}。未重发操作。` }
  finally { if (sequence === verificationRequest) busy.value = "" }
}

async function runVerification(level: "L1" | "L2" | "L3") {
  if (requireChannel() || blockStationChange()) return
  if (profileDirty.value || mappingDirty.value) { error.value = "先应用 Profile 并保存点位映射，再验证当前配置"; return }
  if (level === "L3") {
    if (!canConfigure.value || !canCommand.value) { error.value = "L3 需要配置及额外 Command 权限"; return }
    if (!verificationBasis.value || !l3Confirmed.value || !l3CommandID.value) { error.value = "L3 必须读取当前配置依据、选择安全命令并明确确认"; return }
  }
  const requestID = ++verificationRequest
  const channelID = selectedChannelID.value
  const serverID = selectedServerID.value
  busy.value = `verify-${level}`
  error.value = ""
  verificationResult.value = null
  try {
    let input: Record<string, unknown> = { level }
    if (level === 'L3') {
      l3RequestID.value = crypto.randomUUID()
      sessionStorage.setItem(l3RequestKey.value, l3RequestID.value)
      input = { level, command_id: l3CommandID.value, confirm: true, request_id: l3RequestID.value, basis: verificationBasis.value }
    }
    const result = await request<VerificationResult>(`/modbus/${encodeURIComponent(channelID)}/units/${serverID}/verify`, "POST", input)
    if (requestID === verificationRequest && channelID === selectedChannelID.value && serverID === selectedServerID.value) verificationResult.value = result
    await loadVerificationHistory()
  } catch (cause) {
    if (requestID === verificationRequest) {
      const uncertain = level === 'L3' && !(cause instanceof ApiError && cause.status >= 400 && cause.status < 500)
      error.value = uncertain ? `L3 未取得确认，结果未知：${messageOf(cause)}。请用原请求查询，不会自动重发。` : `${level} 验证被拒绝：${messageOf(cause)}`
    }
  } finally {
    if (requestID === verificationRequest) busy.value = ""
    if (level === "L3") l3Confirmed.value = false
  }
}

async function saveVerification() {
  if (requireChannel() || blockStationChange()) return
  const validation = verificationError(config.value, missingRequired.value)
  if (validation) { error.value = validation; return }
  if (profileDirty.value || mappingDirty.value) { error.value = "先保存 Profile 和点位映射，再记录验证"; return }
  busy.value = "verification"
  error.value = ""
  notice.value = ""
  try {
    await request<UnitModel>(`/modbus/${encodeURIComponent(selectedChannelID.value)}/units/${selectedServerID.value}/model`, "PUT", prepareModelSave(config.value))
    await reloadAfterWrite("验证记录已保存")
  } catch (cause) { error.value = `验证记录保存失败：${messageOf(cause)}` } finally { busy.value = "" }
}

watch(l3RequestKey, key => {
  l3RequestID.value = sessionStorage.getItem(key) || ''
  verificationBasis.value = ''
  l3Confirmed.value = false
}, { immediate: true })
watch([l3CommandID, verificationBasis], () => { l3Confirmed.value = false })
watch(safeCommands, commands => {
  if (!commands.some(command => command.id === l3CommandID.value)) l3CommandID.value = commands[0]?.id || ""
  l3Confirmed.value = false
}, { immediate: true })
watch(() => props.selectedNodeId, id => {
  const device = devices.value.find(item => item.business_node_id === id)
  const businessDevice = props.snapshot?.nodes.some(node => node.id === id && node.type === "device" && node.parent !== "root")
  if (!businessDevice) deviceDetailOpen.value = false
  if (device && deviceKey(device) !== selectedKey.value) void openDevice(device)
  if (!device && businessDevice) {
    if (persistedBoundIDs.value.has(id)) void openBusinessBinding(id)
    else beginUnbound(id)
  }
}, { immediate: true })
watch(() => props.batchNodeIds, ids => { if (ids.length) openBatch() }, { immediate: true })

onMounted(async () => { await reload() })
</script>

<style scoped>
.tool-line { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; margin-top: 12px; color: var(--faint); font-size: 12px; }
.tool-line uni-button { margin: 0; min-height: 25px; padding: 2px 8px; font-size: 11px; }
.access-step { display: flex; flex-direction: column; gap: 12px; min-width: 0; }
.l3-box { display: flex; flex-direction: column; gap: 8px; align-items: flex-start; }
.l3-box p { margin: 0; }
.l3-box select { min-width: 220px; }
.step-head, .panel-head { display: flex; gap: 10px; flex-wrap: wrap; }
.step-head { align-items: baseline; margin-bottom: 12px; }
.panel-head { align-items: center; margin-bottom: 12px; }
.step-head b { font-size: 16px; }
.step-head > span:not(.spacer), .panel-head .src { color: var(--faint); font-size: 12.5px; }
.ledger-panel { min-width: 0; }
.preview-panel, .editor-panel, .quality-panel { padding: 12px; border: 1px solid rgba(255,255,255,.72); border-radius: 12px; background: rgba(255,255,255,.28); }
.ledger-table td strong, td small { display: block; }
td small { color: var(--faint); font-size: 10.5px; margin-top: 2px; }
tr.selected td { background: rgba(230,243,238,.45); }
.pipeline { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; margin: 10px 0; }
.pipeline button { border: 1px solid var(--line); border-radius: 999px; padding: 3px 9px; background: rgba(255,255,255,.48); color: var(--faint); font-size: 11.5px; }
.pipeline button.active, .pipeline button.cur { border-color: var(--accent); color: var(--accent); }
.pipeline button.ok { border-color: #cfe6dc; background: #e6f3ee; color: var(--mint); }
.pipeline i { color: #c3d0da; font-style: normal; }
.acc-grid, .verification-grid { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); gap: 10px; margin-top: 12px; align-items: start; }
.acc-card { min-width: 0; padding: 11px 12px; border: 1px solid rgba(255,255,255,.72); border-radius: 10px; background: linear-gradient(135deg,rgba(255,255,255,.34),rgba(255,255,255,.06)),rgba(255,255,255,.42); box-shadow: 0 8px 22px rgba(45,83,110,.08),inset 0 1px 0 rgba(255,255,255,.75); }
.acc-card.wide { grid-column: 1 / -1; }
.card-title { margin: 0 0 8px; color: var(--faint); font-size: 11px; font-weight: 600; letter-spacing: .06em; }
.card-title > span { font-weight: 400; letter-spacing: 0; }
.acc-card > label, .verification-grid .acc-card > label { display: flex; flex-direction: column; gap: 5px; margin-bottom: 10px; color: var(--muted); font-size: 12px; }
input, select, textarea { width: 100%; border: 1px solid rgba(255,255,255,.82); border-radius: 8px; padding: 7px 9px; background: rgba(255,255,255,.62); font-size: 12px; }
select { min-height: 36px; }
textarea { resize: vertical; }
.pick { display: flex; flex-direction: column; gap: 4px; width: 100%; text-align: left; border: 1px solid rgba(255,255,255,.72); border-radius: 9px; padding: 8px 10px; margin-bottom: 6px; background: rgba(255,255,255,.42); }
.pick.active { border-color: var(--accent); background: rgba(240,250,249,.72); }
.pick strong { font-size: 12.5px; }
.pick small { color: var(--faint); font-size: 10.5px; }
.station-form { display: flex; align-items: end; gap: 8px; flex-wrap: wrap; margin-top: 12px; }
.station-form label { width: 130px; color: var(--muted); font-size: 12px; }
.form-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 10px; }
.form-grid label { display: flex; flex-direction: column; gap: 5px; color: var(--muted); font-size: 12px; }
.check-label { display: flex !important; flex-direction: row !important; align-items: center; gap: 7px !important; }
.check-label input { width: auto; }
.notice { margin: 0; padding: 9px 12px; border: 1px solid rgba(35,112,95,.25); border-radius: 10px; background: rgba(230,243,238,.72); color: var(--mint); font-size: 12.5px; }
.warning-box { margin-top: 10px; padding: 9px 11px; border: 1px solid rgba(164,99,27,.25); border-radius: 9px; background: rgba(253,247,234,.75); color: #6f4a12; font-size: 12px; line-height: 1.7; }
.warn-text { color: var(--amber); }
.ok-text { color: var(--mint); }
.line-result { margin-top: 10px; color: var(--muted); font-size: 12px; line-height: 1.8; }
.acc-card.profile-summary { margin-top: 0; }
@media (max-width: 760px) {
  .acc-grid, .verification-grid { grid-template-columns: 1fr; }
  .form-grid { grid-template-columns: 1fr; }
  .step-head .spacer { display: none; }
}
</style>
