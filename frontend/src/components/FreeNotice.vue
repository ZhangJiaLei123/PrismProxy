<template>
  <div class="free-notice">
    <div class="fn-title">
      本工具完全免费，为爱发电
      <svg class="fn-heart" viewBox="0 0 24 24" width="15" height="15" aria-hidden="true">
        <path d="M12 21s-7.5-4.9-10-9.3C.4 8.6 2.2 5 5.6 5c2 0 3.4 1.1 4.4 2.4C11 6.1 12.4 5 14.4 5c3.4 0 5.2 3.6 3.6 6.7C19.5 16.1 12 21 12 21z" />
      </svg>
    </div>
    <div class="fn-sub">
      开源地址：
      <a class="fn-link" :title="GITHUB_URL" @click.prevent="openRepo">{{ GITHUB_URL }}</a>
    </div>
  </div>
</template>

<script setup lang="ts">
const GITHUB_URL = 'https://github.com/ZhangJiaLei123/PrismProxy'

function openRepo() {
  // Wails 桌面端调系统默认浏览器；纯浏览器预览环境降级新标签页
  const wailsRuntime = (window as unknown as { runtime?: { BrowserOpenURL?: (url: string) => void } }).runtime
  if (wailsRuntime?.BrowserOpenURL) {
    wailsRuntime.BrowserOpenURL(GITHUB_URL)
  } else {
    window.open(GITHUB_URL, '_blank', 'noopener,noreferrer')
  }
}
</script>

<style scoped>
.free-notice {
  text-align: center;
  user-select: none;
}
.fn-title {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  font-size: 14px;
  font-weight: 600;
  letter-spacing: 0.5px;
  background: linear-gradient(90deg, #f2a9c8, #ff6b9a, #c792ea, #70c0e8, #f2a9c8);
  background-size: 200% auto;
  -webkit-background-clip: text;
  background-clip: text;
  color: transparent;
  animation: fn-shine 3.5s linear infinite;
}
@keyframes fn-shine {
  to { background-position: 200% center; }
}
.fn-heart {
  fill: #ff6b9a;
  flex: none;
  filter: drop-shadow(0 0 4px rgba(255, 107, 154, 0.5));
  transform-origin: center;
  animation: fn-heartbeat 1.4s ease-in-out infinite;
}
@keyframes fn-heartbeat {
  0%, 60%, 100% { transform: scale(1); filter: drop-shadow(0 0 4px rgba(255, 107, 154, 0.5)); }
  15% { transform: scale(1.35); filter: drop-shadow(0 0 8px rgba(255, 107, 154, 0.9)); }
  30% { transform: scale(1.12); }
  45% { transform: scale(1.28); filter: drop-shadow(0 0 7px rgba(255, 107, 154, 0.8)); }
}
.fn-sub { margin-top: 6px; font-size: 12px; color: rgba(255, 255, 255, 0.55); }
.fn-link {
  color: #70c0e8;
  cursor: pointer;
  text-decoration: underline;
  text-underline-offset: 3px;
  word-break: break-all;
}
.fn-link:hover { color: #9fd8f5; }
</style>
