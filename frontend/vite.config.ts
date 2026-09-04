import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  build: {
    rollupOptions: {
      output: {
        // 桌面应用本地加载，拆分 vendor 仅为消除单 chunk 超限警告
        manualChunks: {
          'naive-ui': ['naive-ui'],
          'vue-vendor': ['vue', 'pinia', '@vueuse/core'],
        },
      },
    },
  },
})
