import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { fileURLToPath, URL } from 'node:url'

const r = (p: string) => fileURLToPath(new URL(p, import.meta.url))

export default defineConfig({
  plugins: [vue()],
  build: {
    rollupOptions: {
      // M12 多页（MPA）：主窗 index.html + 数据复盘页 review.html，共享 assets。
      // 红线（设计 §6.1 P1）：base 保持默认 '/' 绝对不动——改 base 会令主窗资源
      // 引用加前缀导致生产 exe 白屏；静态托管由 ctlapi 从 dist 根提供。
      input: {
        main: r('./index.html'),
        review: r('./review.html'),
      },
      output: {
        // 桌面应用本地加载，拆分 vendor 仅为消除单 chunk 超限警告
        manualChunks: {
          'naive-ui': ['naive-ui'],
          'vue-vendor': ['vue', 'pinia', '@vueuse/core'],
        },
      },
    },
  },
  server: {
    proxy: {
      // M12：wails dev / 浏览器预览复盘页时，/api 转发到 ctlapi（127.0.0.1:9595）
      '/api': {
        target: 'http://127.0.0.1:9595',
        changeOrigin: true,
      },
    },
  },
})
