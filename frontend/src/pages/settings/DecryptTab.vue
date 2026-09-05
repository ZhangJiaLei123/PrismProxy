<template>
  <section class="sec">
    <n-dynamic-input v-model:value="form.decryptRules" :on-create="() => ({ action: 'bypass', host: '' })">
      <template #default="{ value }">
        <div class="rule-row">
          <n-select v-model:value="value.action" :options="decryptActions" style="width: 120px" />
          <n-input v-model:value="value.host" placeholder="域名或 @组名" />
        </div>
      </template>
    </n-dynamic-input>
    <div class="hint">自上而下首条命中生效，未命中默认 MITM 解密；bypass = 盲透传（应对 SSL Pinning）。</div>
    <div class="hint break">可用域名组：{{ groupText || '无' }}</div>
  </section>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { NDynamicInput, NSelect, NInput } from 'naive-ui'
import { ListDomainGroups } from '../../../wailsjs/go/main/App'
import type { settings } from '../../../wailsjs/go/models'

const props = defineProps<{ form: settings.Settings }>()

const groupText = ref('')

onMounted(async () => {
  const groups = await ListDomainGroups()
  groupText.value = ((groups?.names as string[]) ?? []).map((n) => '@' + n).join(' ')
})

// ---- 选项常量 ----
const decryptActions = [
  { label: 'MITM 解密', value: 'mitm' },
  { label: 'bypass 透传', value: 'bypass' },
]
</script>

<style scoped>
.sec { font-size: 12px; }
.hint { opacity: 0.5; font-size: 11px; margin-top: 4px; }
.hint.break { word-break: break-all; }
.rule-row { display: flex; gap: 6px; width: 100%; }
</style>
