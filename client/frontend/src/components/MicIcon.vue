<script setup>
// 复用麦克风图标：muted=true 时多画一道斜杠表示禁用。
// 用在本地按钮、成员静音指示、popover 静音按钮三处。
defineProps({ muted: Boolean })
</script>

<template>
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
    <rect x="9" y="2" width="6" height="12" rx="3"/>
    <path d="M5 10v2a7 7 0 0 0 14 0v-2"/>
    <line x1="12" y1="19" x2="12" y2="22"/>
    <line x1="8" y1="22" x2="16" y2="22"/>
    <line v-if="muted" x1="4" y1="4" x2="20" y2="20"/>
  </svg>
</template>
