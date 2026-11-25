<template>
  <svg class="station" viewBox="0 0 800 300" preserveAspectRatio="xMidYMid meet" role="img" aria-label="浅色等距厂区工艺示意图">
    <defs>
      <linearGradient id="flow-roof" x1="0" y1="0" x2="1" y2="1"><stop stop-color="#f7fbfd"/><stop offset="1" stop-color="#d4e2e9"/></linearGradient>
      <linearGradient id="flow-wall" x1="0" y1="0" x2="0" y2="1"><stop stop-color="#fbfdfd"/><stop offset="1" stop-color="#dce8ee"/></linearGradient>
      <linearGradient id="flow-tank" x1="0" y1="0" x2="1" y2="0"><stop stop-color="#c2d4de"/><stop offset=".4" stop-color="#f7fbfd"/><stop offset="1" stop-color="#b2c7d4"/></linearGradient>
      <filter id="flow-soft" x="-20%" y="-20%" width="140%" height="140%"><feDropShadow dx="0" dy="4" stdDeviation="4" flood-color="#56788b" flood-opacity=".18"/></filter>
    </defs>
    <polygon points="48,205 398,112 760,202 410,283" fill="#d9e7ed" stroke="#a4bdc9" stroke-width="1.2"/>
    <polygon points="48,205 48,214 410,292 410,283" fill="#b8ccd7"/>
    <polygon points="410,283 760,202 760,211 410,292" fill="#a9c1ce"/>
    <path d="M80 214 L400 125 730 205 405 274 Z" fill="none" stroke="#a8c1ce" stroke-dasharray="4 4"/>
    <g v-for="tree in trees" :key="`${tree[0]}-${tree[1]}`" :transform="`translate(${tree[0]} ${tree[1]}) scale(${tree[2]})`">
      <ellipse cx="0" cy="4" rx="12" ry="4" fill="#7897a4" opacity=".16"/><path d="M0 0 V-26" stroke="#6b7778" stroke-width="3"/>
      <ellipse cx="0" cy="-29" rx="12" ry="17" fill="#4c967d"/><ellipse cx="-5" cy="-35" rx="8" ry="11" fill="#63aa89"/><ellipse cx="5" cy="-29" rx="7" ry="12" fill="#3b876e"/>
    </g>
    <g filter="url(#flow-soft)">
      <polygon points="138,110 292,69 354,96 200,139" fill="url(#flow-roof)" stroke="#a9bfcb"/>
      <polygon points="138,110 200,139 200,189 138,158" fill="#e4edf2" stroke="#aec3cf"/>
      <polygon points="200,139 354,96 354,145 200,189" fill="url(#flow-wall)" stroke="#aec3cf"/>
      <polygon v-for="(points, index) in panels" :key="index" :points="points" fill="#2f78ad" stroke="#76b3d2" stroke-width=".7"/>
      <path d="M200 189 L354 145" stroke="#a9c0cd"/>
    </g>
    <g filter="url(#flow-soft)">
      <ellipse cx="108" cy="164" rx="30" ry="10" fill="#d4e3eb" stroke="#9cb8c8"/>
      <path d="M78 164 V207 C78 213 92 218 108 218 C124 218 138 213 138 207 V164" fill="url(#flow-tank)" stroke="#9cb8c8"/>
      <ellipse cx="108" cy="207" rx="30" ry="10" fill="#c3d7e1" stroke="#9cb8c8"/>
      <ellipse cx="108" cy="164" rx="30" ry="10" fill="#eef6f9" stroke="#9cb8c8"/>
      <path d="M85 190 Q108 197 131 190" fill="none" stroke="#56a5b4" stroke-width="2"/>
      <path d="M85 195 Q108 202 131 195" fill="none" stroke="#56a5b4" stroke-width="1" opacity=".6"/>
    </g>
    <g filter="url(#flow-soft)">
      <polygon points="365,135 455,110 512,133 422,160" fill="#f7fbfd" stroke="#a9bfcb"/>
      <polygon points="365,135 422,160 422,198 365,173" fill="#e1ebf0" stroke="#a9bfcb"/>
      <polygon points="422,160 512,133 512,171 422,198" fill="url(#flow-wall)" stroke="#a9bfcb"/>
      <path d="M365 135 L422 160 512 133 455 110 Z" fill="#eaf2f6" stroke="#a9bfcb"/>
      <g stroke="#5c96b3" stroke-width="2"><path d="M440 165 v16 M456 160 v16 M472 155 v16"/></g>
      <path d="M389 150 v20 M402 156 v20" stroke="#8eb4c6" stroke-width="5"/>
    </g>
    <g filter="url(#flow-soft)">
      <g v-for="index in containers" :key="index" :transform="`translate(${index * 44} ${index * 11})`">
        <polygon points="250,176 286,166 312,177 276,188" fill="#f7fbfd" stroke="#adc2ce"/>
        <polygon points="250,176 276,188 276,216 250,204" fill="#dde9ef" stroke="#adc2ce"/>
        <polygon points="276,188 312,177 312,205 276,216" fill="#f2f8fa" stroke="#adc2ce"/>
        <path d="M282 190 v22 M290 187 v22 M298 184 v22" stroke="#b4c9d4"/>
      </g>
    </g>
    <g fill="none" stroke-linecap="round">
      <path d="M138 198 C190 214 238 225 276 222M400 222 C440 215 462 196 468 175M512 156 C580 155 622 174 680 188" stroke="#fff" stroke-width="5"/>
      <path d="M138 198 C190 214 238 225 276 222M400 222 C440 215 462 196 468 175M512 156 C580 155 622 174 680 188" stroke="#4ca8b3" stroke-width="2" stroke-dasharray="5 4"/>
    </g>
    <g fill="#fff" stroke="#4ca8b3" stroke-width="2"><circle cx="138" cy="198" r="5"/><circle cx="400" cy="222" r="5"/><circle cx="512" cy="156" r="5"/><circle cx="680" cy="188" r="5"/></g>
    <g filter="url(#flow-soft)">
      <polygon points="628,158 662,148 690,159 656,170" fill="#f6fafc" stroke="#a9bfcb"/>
      <polygon points="628,158 656,170 656,196 628,184" fill="#dce8ee" stroke="#a9bfcb"/>
      <polygon points="656,170 690,159 690,185 656,196" fill="#eff6f9" stroke="#a9bfcb"/>
      <path d="M642 150 v-18 M654 146 v-18 M666 151 v-18M636 132 h12 M648 128 h12 M660 133 h12" stroke="#5d7788" stroke-width="2"/>
    </g>
  </svg>
</template>

<script setup lang="ts">
const trees = [[66,214,.8],[96,176,.7],[125,122,.75],[176,85,.65],[252,70,.7],[352,72,.65],[422,90,.8],[514,78,.7],[598,120,.85],[690,163,.8],[732,218,.85],[660,251,.75],[548,260,.65],[475,270,.7],[342,260,.8],[225,250,.65],[124,244,.8],[72,244,.75]]
const panels = Array.from({ length: 5 }, (_, row) => Array.from({ length: 7 }, (_, column) => {
  const x = 170 + column * 24 + row * 4
  const y = 80 + row * 13 - column * 1.3
  return `${x},${y} ${x + 22},${y - 5} ${x + 32},${y + 1} ${x + 10},${y + 6}`
})).flat()
const containers = [0, 1, 2]
</script>
