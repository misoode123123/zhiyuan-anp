import type { Metadata } from "next";
import "./globals.css";
import { Shell } from "./_components/shell";
import { ThemeProvider, THEME_STORAGE_KEY } from "@/lib/theme";

export const metadata: Metadata = {
  title: "智源 ANP",
  description: "企业 AI 原生研发平台",
};

// 防 FOUC：SSR 时同步执行，读 localStorage + 系统偏好，在 React hydration 前设好 <html>.dark。
// 必须内联在 HTML 里同步跑，不能用 effect（否则 hydration 前闪浅色）。
const THEME_INLINE_SCRIPT = `(function(){try{var t=localStorage.getItem(${JSON.stringify(
  THEME_STORAGE_KEY
)});var d=t?t==='dark':matchMedia('(prefers-color-scheme: dark)').matches;document.documentElement.classList.toggle('dark',d);}catch(e){}})();`;

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="zh-CN" className="h-full antialiased" suppressHydrationWarning>
      <head>
        <script dangerouslySetInnerHTML={{ __html: THEME_INLINE_SCRIPT }} />
      </head>
      <body className="min-h-full bg-bg text-text">
        <ThemeProvider>
          <Shell>{children}</Shell>
        </ThemeProvider>
      </body>
    </html>
  );
}
