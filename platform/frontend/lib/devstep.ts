// 开发向导状态推断（纯函数）：据应用镜像 + 各环境实例状态，
// 推断「编码→测试→上线」当前该做哪步 + 引导文案。
// 刻意不依赖 React/框架，便于独立测试。

export type DevStepName = "code" | "test" | "prod" | "done";

export interface DevWizardInput {
  // 有镜像 = 构建过（代理「编码并构建过」；App 列表无 commit，用 image 代理）。
  image?: string;
  instances?: { env: string; status: string }[];
}

export interface DevWizardState {
  code: "todo" | "done";
  test: "todo" | "done";
  prod: "todo" | "done";
  current: DevStepName; // 当前该做的步（高亮）
  hint: string; // 引导文案
}

// devStep 据 image + instances 推断开发向导状态。
// 编码✅=有镜像；测试✅=test 实例 running；上线✅=prod 实例 running。
export function devStep(input: DevWizardInput): DevWizardState {
  const coded = !!input.image;
  const testRun = !!input.instances?.some((i) => i.env === "test" && i.status === "running");
  const prodRun = !!input.instances?.some((i) => i.env === "prod" && i.status === "running");

  // 从最高成就往下判定：已上线(prod) → 待上线(test 通过) → 待测试(已编码) → 待编码。
  // prod 上线视为整条流程走完(done)，即使无 test 实例。
  let current: DevStepName;
  let hint: string;
  if (!coded) {
    current = "code";
    hint = "先打开编码工作台写代码，再构建部署";
  } else if (prodRun) {
    current = "done";
    hint = "已上线 ✅，可继续编码迭代";
  } else if (testRun) {
    current = "prod";
    hint = "test 已跑通，点「🚀上线」到 prod";
  } else {
    current = "test";
    hint = "代码已构建，点「构建部署(test)」验证";
  }

  return {
    code: coded ? "done" : "todo",
    test: testRun || prodRun ? "done" : "todo",
    prod: prodRun ? "done" : "todo",
    current,
    hint,
  };
}
