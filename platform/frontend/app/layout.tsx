import type { Metadata } from "next";
import "./globals.css";
import { Shell } from "./_components/shell";

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
    <html lang="zh-CN" className="h-full antialiased">
      <body className="min-h-full bg-neutral-50 text-neutral-900">
        <Shell>{children}</Shell>
      </body>
    </html>
  );
}
