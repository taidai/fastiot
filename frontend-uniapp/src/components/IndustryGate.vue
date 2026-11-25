<template>
  <section class="card glass industry-gate" role="alert">
    <h3>项目依赖未满足</h3>
    <p>当前工程需要 EMS 行业包 {{ session.snapshot?.industry?.version }}，但本站未启用；相关配置与控制已阻断，工程依赖和数据仍保留。</p>
    <p>请在「交付 → 行业包」核实启用状态；无权限时请联系管理员。读取、诊断与系统修复仍可通过授权接口进行。</p>
    <p>启用不等于购买、部署或控制授权。</p>
  </section>
</template>
<script setup lang="ts">
import { session } from '../stores/session'
</script>
<style scoped>
.industry-gate { margin: 16px 0; padding: 20px; }
h3 { margin: 0 0 12px; }
p { line-height: 1.7; color: var(--muted); }
</style>
