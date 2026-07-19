import { defineConfig, globalIgnores } from "eslint/config";
import nextVitals from "eslint-config-next/core-web-vitals";
import nextTs from "eslint-config-next/typescript";
import prettier from "eslint-config-prettier";

const eslintConfig = defineConfig([
  ...nextVitals,
  ...nextTs,
  prettier, // 放最后：关闭与 prettier 冲突的格式化规则
  {
    rules: {
      // Next.js 16 / react-hooks 6 新规则：现有组件初始化模式（effect 内同步 setState、
      // 声明前访问）系统性触发，重构有 regression 风险。暂降级为 warn，列为技术债专项
      // （见 docs/2026-07-19-工程化落地执行计划.md P0-4），后续配合测试基线逐步修复。
      "react-hooks/set-state-in-effect": "warn",
      "react-hooks/immutability": "warn",
      // 中文文案含引号常见，逐处转义繁琐且价值低，关闭。
      "react/no-unescaped-entities": "off",
    },
  },
  // Override default ignores of eslint-config-next.
  globalIgnores([
    // Default ignores of eslint-config-next:
    ".next/**",
    "out/**",
    "build/**",
    "next-env.d.ts",
  ]),
]);

export default eslintConfig;
