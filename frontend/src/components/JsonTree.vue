<template>
  <div class="json-node" :style="{ paddingLeft: depth > 0 ? '14px' : '0' }">
    <template v-if="isExpandable">
      <span class="toggle" @click="open = !open">{{ open ? '▾' : '▸' }}</span>
      <span v-if="name !== ''" class="key">{{ name }}:</span>
      <span class="type">{{ isArray ? '[' + len + ']' : '{' + len + '}' }}</span>
      <template v-if="open">
        <json-tree
          v-for="(v, k) in data"
          :key="k"
          :name="String(k)"
          :data="v"
          :depth="depth + 1"
        />
      </template>
    </template>
    <template v-else>
      <span class="toggle"></span>
      <span v-if="name !== ''" class="key">{{ name }}:</span>
      <span class="value" :class="valueClass">{{ display }}</span>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'

const props = withDefaults(defineProps<{ name?: string; data: unknown; depth?: number }>(), {
  name: '',
  depth: 0,
})
const open = ref(props.depth < 2)

const isArray = computed(() => Array.isArray(props.data))
const isExpandable = computed(
  () => props.data !== null && typeof props.data === 'object'
)
const len = computed(() => (isExpandable.value ? Object.keys(props.data as object).length : 0))

const display = computed(() => {
  const v = props.data
  if (v === null) return 'null'
  if (typeof v === 'string') return JSON.stringify(v)
  return String(v)
})
const valueClass = computed(() => {
  const t = typeof props.data
  if (props.data === null) return 'v-null'
  if (t === 'string') return 'v-str'
  if (t === 'number') return 'v-num'
  if (t === 'boolean') return 'v-bool'
  return ''
})
</script>

<style scoped>
.json-node { font-family: Consolas, 'Courier New', monospace; font-size: 12px; line-height: 1.6; }
.toggle { display: inline-block; width: 14px; cursor: pointer; color: rgba(255, 255, 255, 0.5); user-select: none; }
.key { color: #9cdcfe; margin-right: 4px; }
.type { color: rgba(255, 255, 255, 0.4); }
.v-str { color: #ce9178; } .v-num { color: #b5cea8; } .v-bool { color: #569cd6; } .v-null { color: #808080; }
</style>
