// devStep 纯函数测试（vitest）。
//   跑法: pnpm --filter frontend test   （或根目录 pnpm fe:test）
// 历史曾为 standalone（tsx 手跑 + console.assert），现纳入 vitest 框架。
import { describe, it, expect } from "vitest";
import { devStep, type DevWizardInput } from "./devstep";

const I = (
  image: string | undefined,
  instances: { env: string; status: string }[] | undefined
): DevWizardInput => ({ image, instances });

describe("devStep - 原 6 个 case（迁移自 standalone 测试）", () => {
  it("全新无镜像 → 引导编码", () => {
    const g = devStep(I("", []));
    expect(g.current).toBe("code");
    expect(g.hint).toBe("先打开编码工作台写代码，再构建部署");
  });

  it("有镜像无 test → 引导部署到 test", () => {
    const g = devStep(I("img:v1", []));
    expect(g.current).toBe("test");
    expect(g.hint).toBe("代码已构建，点「构建部署(test)」验证");
  });

  it("test running 无 prod → 引导上线", () => {
    const g = devStep(I("img:v1", [{ env: "test", status: "running" }]));
    expect(g.current).toBe("prod");
    expect(g.hint).toBe("test 已跑通，点「🚀上线」到 prod");
  });

  it("prod running 全绿 → done", () => {
    const g = devStep(I("img:v1", [{ env: "prod", status: "running" }]));
    expect(g.current).toBe("done");
    expect(g.hint).toBe("已上线 ✅，可继续编码迭代");
  });

  it("test 非 running 不算完成", () => {
    const g = devStep(I("img:v1", [{ env: "test", status: "building" }]));
    expect(g.current).toBe("test");
    expect(g.hint).toBe("代码已构建，点「构建部署(test)」验证");
  });

  it("test stopped 不算完成", () => {
    const g = devStep(I("img:v1", [{ env: "test", status: "stopped" }]));
    expect(g.current).toBe("test");
    expect(g.hint).toBe("代码已构建，点「构建部署(test)」验证");
  });
});

describe("devStep - 完整返回结构 & 边界 case（新增示范）", () => {
  it("code/test/prod 状态字段随实例状态正确流转（编码完成 + test running）", () => {
    const g = devStep(I("img:v1", [{ env: "test", status: "running" }]));
    expect(g).toEqual({
      code: "done", // 有镜像
      test: "done", // test 实例 running
      prod: "todo", // 无 prod 实例
      current: "prod",
      hint: "test 已跑通，点「🚀上线」到 prod",
    });
  });

  it("全新状态：code/test/prod 全 todo", () => {
    const g = devStep(I(undefined, undefined));
    expect(g.code).toBe("todo");
    expect(g.test).toBe("todo");
    expect(g.prod).toBe("todo");
    expect(g.current).toBe("code");
  });

  it("空字符串镜像视为未编码（falsy 判定）", () => {
    const g = devStep(I("", []));
    expect(g.code).toBe("todo");
    expect(g.current).toBe("code");
  });

  it("prod running 视为整条流程走完（test 字段一并置 done）", () => {
    const g = devStep(I("img:v1", [{ env: "prod", status: "running" }]));
    expect(g.test).toBe("done"); // prod 上线后 test 不再算待办
    expect(g.prod).toBe("done");
    expect(g.current).toBe("done");
  });

  it("prod 非 running 状态（building/stopped）不视为上线", () => {
    const building = devStep(I("img:v1", [{ env: "prod", status: "building" }]));
    expect(building.current).toBe("test"); // 退而求其次：继续在 test 阶段
    expect(building.prod).toBe("todo");

    const stopped = devStep(I("img:v1", [{ env: "prod", status: "stopped" }]));
    expect(stopped.current).toBe("test");
    expect(stopped.prod).toBe("todo");
  });

  it("多实例混合：只要存在 running 的 test/prod 即据此判定（取最高成就）", () => {
    // 同时有 test stopped + prod running：应判 done（prod 优先于 test 失败）
    const g = devStep(
      I("img:v1", [
        { env: "test", status: "stopped" },
        { env: "prod", status: "running" },
      ])
    );
    expect(g.current).toBe("done");
    expect(g.test).toBe("done");
    expect(g.prod).toBe("done");
  });

  it("未列出的 env（如 dev/staging）不影响判定", () => {
    const g = devStep(
      I("img:v1", [
        { env: "dev", status: "running" },
        { env: "staging", status: "running" },
      ])
    );
    // dev/staging 既不算 test 也不算 prod，故仍停留在 test 阶段
    expect(g.current).toBe("test");
    expect(g.test).toBe("todo");
    expect(g.prod).toBe("todo");
  });
});
