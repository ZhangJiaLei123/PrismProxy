<template>
  <div class="header-table">
    <div v-if="!entries.length" class="ht-empty">—</div>
    <table v-else>
      <tr v-for="([k, v], i) in entries" :key="k + '-' + i">
        <td class="ht-k">{{ k }}</td>
        <td class="ht-v">{{ v }}</td>
      </tr>
    </table>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'

// 通用 Headers 只读表格：请求/响应详情与 Composer 响应区共用
const props = defineProps<{ header?: Record<string, string[]> }>()

const entries = computed(() =>
  Object.entries(props.header ?? {}).flatMap(([k, vs]) => (vs ?? []).map((v) => [k, v] as const)),
)
</script>

<style scoped>
.ht-empty { color: rgba(255, 255, 255, 0.35); font-size: 12px; }
table { border-collapse: collapse; font-size: 12px; width: 100%; }
.ht-k { padding: 2px 8px 2px 0; color: #9cdcfe; white-space: nowrap; vertical-align: top; }
.ht-v { padding: 2px 0; word-break: break-all; color: rgba(255, 255, 255, 0.85); }
</style>
