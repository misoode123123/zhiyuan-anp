import type { Metadata } from "next";
import "./globals.css";
import { Shell } from "./_components/shell";
import { ThemeProvider } from "@/lib/theme";
import { buildThemeInlineScript } from "@/lib/theme-constants";

export const metadata: Metadata = {
  title: "智源 ANP",
  description: "企业 AI 原生研发平台",
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="zh-CN" className="h-full antialiased" suppressHydrationWarning>
      <body className="min-h-full bg-bg text-text">
        <script dangerouslySetInnerHTML={{ __html: buildThemeInlineScript() }} />
        <ThemeProvider>
          <Shell>{children}</Shell>
        </ThemeProvider>
      </body>
    </html>
  );
}
