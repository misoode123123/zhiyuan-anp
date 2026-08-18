// 总览格子推导测试：managed 双环境态 / failed / 无实例 / external / 版本显隐。
import { describe, it, expect } from "vitest";
import { buildOverviewCells } from "./overview";
import type { App } from "./types";

const base: App = {
  id: "app_a",
  name: "A",
  repo_dir: "/data/repos/a",
  internal_port: 8080,
  image: "img",
  container_name: "c",
  host_port: 9103,
  url: "http://x",
  version: 3,
  status: "running",
  last_error: "",
  build_log: "",
  deploy_mode: "managed",
  external_url: "",
  app_kind: "web",
  network_mode: "bridge",
  updated_at: "2026-08-19T00:00:00Z",
  instances: [
    {
      env: "test",
      status: "running",
      url: "u",
      version: 3,
      host_port: 9103,
      image: "i",
      updated_at: "t",
    },
    {
      env: "prod",
      status: "running",
      url: "u",
      version: 2,
      host_port: 9201,
      image: "i",
      updated_at: "t",
    },
  ],
};

describe("buildOverviewCells", () => {
  it("managed 双环境 running → ok/ok，版本显示", () => {
    const [c] = buildOverviewCells([base]);
    expect(c.test).toBe("ok");
    expect(c.prod).toBe("ok");
    expect(c.showVersion).toBe(true);
    expect(c.version).toBe(3);
    expect(c.isExternal).toBe(false);
  });
  it("test 实例存在但非 running → bad", () => {
    const app = {
      ...base,
      instances: base.instances!.map((i) => (i.env === "test" ? { ...i, status: "failed" } : i)),
    };
    expect(buildOverviewCells([app])[0].test).toBe("bad");
  });
  it("无实例 → none/none", () => {
    const app = { ...base, instances: undefined };
    const [c] = buildOverviewCells([app]);
    expect(c.test).toBe("none");
    expect(c.prod).toBe("none");
  });
  it("external：isExternal=true，版本不显示，环境态 none", () => {
    const app = { ...base, deploy_mode: "external", version: 0, instances: undefined };
    const [c] = buildOverviewCells([app]);
    expect(c.isExternal).toBe(true);
    expect(c.showVersion).toBe(false);
    expect(c.test).toBe("none");
  });
  it("importing：isImporting=true 且带进度提示", () => {
    const app = { ...base, status: "importing", last_error: "正在 clone..." };
    const [c] = buildOverviewCells([app]);
    expect(c.isImporting).toBe(true);
    expect(c.importingHint).toBe("正在 clone...");
  });
});
