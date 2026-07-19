// commitlint 配置：Conventional Commits，与《开发标准与规范》§3.2 对齐。
// 合法示例：feat(rule): 新增 block 规则 / fix(workspace): 修应用名不显示
export default {
  extends: ["@commitlint/config-conventional"],
  rules: {
    "type-enum": [
      2,
      "always",
      [
        "feat",
        "fix",
        "docs",
        "style",
        "refactor",
        "perf",
        "test",
        "build",
        "ci",
        "chore",
        "revert",
      ],
    ],
    // 允许中文 subject，不强制英文小写
    "subject-case": [0],
    "header-max-length": [2, "always", 100],
  },
};
