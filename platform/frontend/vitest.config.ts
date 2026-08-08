// Vitest 配置（Next 16 + React 19 项目）。
// 依据 node_modules/next/dist/docs/01-app/02-guides/testing/vitest.md：
//   - Vitest 暂不支持 async Server Components；
//   - 纯函数测试用 node 环境（最轻量、与 Next/DOM 解耦）；
//   - 组件测试用 jsdom + @vitejs/plugin-react + @testing-library/react（Next 16 官方指南）。
//
// 用 projects 拆分两类测试：lib/** 纯函数跑 node（无 setup、不污染 fetch）；
// app/**/*.test.tsx 组件跑 jsdom（带 setup 装 fetch mock + jest-dom 匹配器）。
// 每个项目显式声明 resolve.tsconfigPaths + plugins（projects 模式下顶层 resolve 不自动继承）。
//
// 注：Next 16 文档示例用 vite-tsconfig-paths 插件解析 @/* 别名；Vitest 4 / Vite 8+ 已
// 内置 tsconfig paths 解析（resolve.tsconfigPaths），故采用原生方案，少一个依赖。
import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

export default defineConfig({
  test: {
    projects: [
      {
        resolve: { tsconfigPaths: true },
        test: {
          name: "node",
          environment: "node",
          include: ["lib/**/*.test.ts", "__tests__/**/*.test.ts"],
        },
      },
      {
        plugins: [react()],
        resolve: { tsconfigPaths: true },
        test: {
          name: "components",
          environment: "jsdom",
          setupFiles: ["./vitest.setup.ts"],
          include: ["app/**/*.test.tsx", "__tests__/**/*.test.tsx"],
        },
      },
    ],
  },
});
