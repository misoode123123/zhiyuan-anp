// Vitest 配置（Next 16 + React 19 项目）。
// 依据 node_modules/next/dist/docs/01-app/02-guides/testing/vitest.md：
//   - Vitest 暂不支持 async Server Components，组件测试需谨慎；
//   - 本项目当前只跑「纯函数」单元测试（不依赖 Next 运行时/DOM），
//     故 environment 用默认 node，未引入 @vitejs/plugin-react / jsdom / @testing-library。
//   - 后续要做组件测试时，再按官方文档补齐上述依赖并把 environment 改 jsdom。
//
// 注：Next 16 文档示例用 vite-tsconfig-paths 插件解析 @/* 别名；Vitest 4 / Vite 7+ 已
// 内置 tsconfig paths 解析（resolve.tsconfigPaths），故采用原生方案，少一个依赖。
import { defineConfig } from "vitest/config";

export default defineConfig({
  resolve: {
    // 让 @/* 等 tsconfig.paths 别名在测试中生效（读取本目录 tsconfig.json）。
    tsconfigPaths: true,
  },
  test: {
    // 纯函数测试无需 DOM；保持 node 环境最轻量、与 Next 运行时解耦。
    environment: "node",
    // 收集 lib 下 *.test.ts（兼容 __tests__/ 与 *.spec.ts 命名，便于后续扩展）。
    include: ["lib/**/*.test.ts", "__tests__/**/*.test.{ts,tsx}"],
  },
});
