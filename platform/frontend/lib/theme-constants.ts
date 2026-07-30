// 纯模块（无 "use client" 指令）：server 与 client 均可直接导入求值。
//
// 为什么不放 lib/theme.tsx：layout.tsx 是 server component，从 "use client" 模块
// 导入「非组件」的 const，App Router 在 server 侧拿到的是 undefined（client/server
// 边界只透出组件绑定，普通值不跨边界）。这曾导致 inline 主题脚本里
// localStorage.getItem(undefined)，刷新后用户主题偏好丢失。
// 放到本纯模块后，server 端 layout 拿到的是真实字符串 "anp.theme"。

export const THEME_STORAGE_KEY = "anp.theme";

// buildThemeInlineScript 生成防 FOUC 的内联脚本：SSR 时同步执行，读 localStorage + 系统偏好，
// 在 React hydration 前设好 <html>.dark。必须内联在 HTML 里同步跑（不能用 effect）。
// 纯函数，便于测试断言脚本里 key 是 "anp.theme" 而非 undefined。
export function buildThemeInlineScript(): string {
  return `(function(){try{var t=localStorage.getItem(${JSON.stringify(
    THEME_STORAGE_KEY
  )});var d=t?t==='dark':matchMedia('(prefers-color-scheme: dark)').matches;document.documentElement.classList.toggle('dark',d);}catch(e){}})();`;
}
