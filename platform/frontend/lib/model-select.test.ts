import { describe, it, expect } from "vitest";
import { modelLabel, pickDefaultModel, type AIModel } from "./model-select";

const m = (over: Partial<AIModel> = {}): AIModel => ({
  id: "cmd_x",
  provider_id: "csp_x",
  name: "glm-5.1",
  ...over,
});

describe("modelLabel", () => {
  it("优先 display_name", () => {
    expect(modelLabel(m({ name: "glm-5.1", display_name: "GLM 5.1" }))).toBe("GLM 5.1");
  });
  it("无 display_name 回退 name", () => {
    expect(modelLabel(m({ name: "glm-5.1", display_name: undefined }))).toBe("glm-5.1");
  });
});

describe("pickDefaultModel", () => {
  it("授权列表非空取第一个 id", () => {
    const list = [m({ id: "cmd_a" }), m({ id: "cmd_b" })];
    expect(pickDefaultModel(list, "cmd_route")).toBe("cmd_a");
  });
  it("授权列表为空回退路由 primary", () => {
    expect(pickDefaultModel([], "cmd_route")).toBe("cmd_route");
  });
  it("都为空返回空串（后端走 route）", () => {
    expect(pickDefaultModel([], "")).toBe("");
  });
});
