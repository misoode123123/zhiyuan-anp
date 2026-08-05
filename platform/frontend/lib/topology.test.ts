// topology 纯函数测试（vitest node env）。跑法: pnpm --filter frontend test
import { describe, it, expect } from "vitest";
import { kindEnvLabel, appBoxes, buildDepBoxes, type AppInstanceLike } from "./topology";
import type { CatalogInstance, Dep } from "./deps-ui";

const ins = (o: Partial<AppInstanceLike> & { env: string }): AppInstanceLike => ({
  env: o.env,
  status: o.status ?? "running",
  url: o.url ?? "",
  host_port: o.host_port ?? 0,
  version: o.version ?? 0,
  image: o.image,
});

const cinst = (id: string, kind: string, host = "10.10.0.28", port = 5432): CatalogInstance => ({
  id,
  kind,
  name: id + "-name",
  supply_mode: "shared",
  host,
  port,
});

const dep = (kind: string, extra: Partial<Dep> = {}): Dep => ({
  kind,
  strategy: extra.strategy ?? "shared",
  status: extra.status ?? "bound",
  instance: extra.instance,
  token: extra.token,
  error: extra.error,
});

describe("kindEnvLabel", () => {
  it("已知 kind 命中 map", () => {
    expect(kindEnvLabel("pg")).toBe("DATABASE_URL");
    expect(kindEnvLabel("redis")).toBe("REDIS_ADDR");
    expect(kindEnvLabel("milvus")).toBe("MILVUS_ADDR");
  });
  it("未知 kind 兜底 KIND_ADDR", () => {
    expect(kindEnvLabel("foo")).toBe("FOO_ADDR");
  });
});

describe("appBoxes", () => {
  it("多 env 派生 + test 排在 prod 前", () => {
    const boxes = appBoxes([
      ins({
        env: "prod",
        host_port: 9200,
        url: "http://10.10.0.28:9200",
        version: 3,
        status: "running",
      }),
      ins({
        env: "test",
        host_port: 9100,
        url: "http://10.10.0.28:9100",
        version: 2,
        status: "building",
      }),
    ]);
    expect(boxes.map((b) => b.env)).toEqual(["test", "prod"]);
    expect(boxes[0]).toMatchObject({
      env: "test",
      port: 9100,
      version: 2,
      status: "building",
      host: "10.10.0.28:9100",
    });
  });
  it("url → host 解析；异常 url 兜底空串", () => {
    expect(appBoxes([ins({ env: "prod", url: "http://h:1" })])[0].host).toBe("h:1");
    expect(appBoxes([ins({ env: "prod", url: "not-a-url" })])[0].host).toBe("");
    expect(appBoxes([ins({ env: "prod", url: "" })])[0].host).toBe("");
  });
  it("空/undefined → []", () => {
    expect(appBoxes(undefined)).toEqual([]);
    expect(appBoxes([])).toEqual([]);
  });
  it("未列出的 env 排末（不崩）", () => {
    const boxes = appBoxes([ins({ env: "staging" }), ins({ env: "test" })]);
    expect(boxes.map((b) => b.env)).toEqual(["test", "staging"]);
  });
});

describe("buildDepBoxes", () => {
  it("bound dep 交叉引用 catalog 取 host/port/name", () => {
    const boxes = buildDepBoxes(
      [dep("pg", { instance: "sv1", status: "bound" })],
      [cinst("sv1", "pg", "10.10.0.28", 5432)]
    );
    expect(boxes[0]).toMatchObject({
      kind: "pg",
      status: "bound",
      envLabel: "DATABASE_URL",
      host: "10.10.0.28",
      port: 5432,
      name: "sv1-name",
      error: "",
    });
  });
  it("declared dep（无 instance）→ host 空/port 0", () => {
    const boxes = buildDepBoxes([dep("redis", { status: "declared" })], []);
    expect(boxes[0].host).toBe("");
    expect(boxes[0].port).toBe(0);
    expect(boxes[0].envLabel).toBe("REDIS_ADDR");
  });
  it("failed dep → 带 error", () => {
    const boxes = buildDepBoxes([dep("milvus", { status: "failed", error: "配额超限" })], []);
    expect(boxes[0].status).toBe("failed");
    expect(boxes[0].error).toBe("配额超限");
    expect(boxes[0].envLabel).toBe("MILVUS_ADDR");
  });
  it("dep.instance 找不到 catalog 项 → 空 host 不崩", () => {
    const boxes = buildDepBoxes(
      [dep("pg", { instance: "ghost", status: "bound" })],
      [cinst("other", "pg")]
    );
    expect(boxes[0].host).toBe("");
    expect(boxes[0].port).toBe(0);
  });
  it("空 deps → []", () => {
    expect(buildDepBoxes([], [cinst("sv1", "pg")])).toEqual([]);
  });
});
