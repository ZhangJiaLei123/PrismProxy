<template>
  <section class="sec">
    <n-alert v-if="form.upstreamMode !== 'pool'" type="info" :bordered="false" class="mode-alert">
      当前上游代理为「{{ modeLabel }}」，代理池未生效：点击账号「使用」自动切换，或到「网络」页签将上游改为「动态代理池」。
    </n-alert>
    <proxy-pool-section :form="form" />
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { NAlert } from 'naive-ui'
import type { app } from '../../../wailsjs/go/models'
import ProxyPoolSection from './ProxyPoolSection.vue'

const props = defineProps<{ form: app.SettingsView }>()

// 与 NetworkTab upstreamModes 标签保持一致
const modeLabel = computed(() => {
  switch (props.form.upstreamMode) {
    case 'manual':
      return '手动代理'
    case 'system':
      return '跟随系统'
    case 'pool':
      return '动态代理池'
    default:
      return '直连'
  }
})
</script>

<style scoped>
.sec { font-size: 12px; }
.mode-alert { margin-bottom: 10px; }
</style>
