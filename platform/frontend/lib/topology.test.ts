// topology 纯函数测试（vitest node env）。跑法: pnpm --filter frontend test
import { describe, it, expect } from "vitest";
import { kindEnvLabel, buildAppBoxes, buildDepBoxes, type AppLike } from "./topology";
import type { CatalogInstance, Dep } from "./deps-ui";

const app = (o: Partial<AppLike> & { name?: string }): AppLike => ({
  name: o.name ?? "myapp",
  image: o.image,
  container_name: o.container_name,
  internal_port: o.internal_port,
  app_kind: o.app_kind,
  network_mode: o.network_mode,
  instances: o.instances,
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

describe("buildAppBoxes", () => {
  it("多 env 派生 + test 排在 prod 前 + 带 app 级参数", () => {
    const boxes = buildAppBoxes(
      app({
        name: "svc",
        image: "svc:v9",
        container_name: "appdeploy-svc-prod-v9",
        internal_port: 8080,
        app_kind: "web",
        network_mode: "bridge",
        instances: [
          {
            env: "prod",
            status: "running",
            url: "http://10.10.0.28:9200",
            host_port: 9200,
            version: 9,
          },
          {
            env: "test",
            status: "building",
            url: "http://10.10.0.28:9100",
            host_port: 9100,
            version: 8,
          },
        ],
      })
    );
    expect(boxes.map((b) => b.env)).toEqual(["test", "prod"]);
    expect(boxes[0]).toMatchObject({
      env: "test",
      port: 9100,
      internalPort: 8080,
      version: 8,
      status: "building",
      host: "10.10.0.28:9100",
      appName: "svc",
      appKind: "web",
      networkMode: "bridge",
      image: "svc:v9",
      containerName: "appdeploy-svc-prod-v9",
    });
  });
  it("实例缺 image 时回退 app.image", () => {
    const boxes = buildAppBoxes(
      app({
        image: "fallback:v1",
        instances: [{ env: "test", status: "running", url: "", host_port: 0, version: 1 }],
      })
    );
    expect(boxes[0].image).toBe("fallback:v1");
  });
  it("url → host 解析；异常 url 兜底空串", () => {
    const boxes = buildAppBoxes(
      app({
        instances: [{ env: "prod", status: "running", url: "not-a-url", host_port: 1, version: 0 }],
      })
    );
    expect(boxes[0].host).toBe("");
  });
  it("空/undefined instances → []", () => {
    expect(buildAppBoxes(app({}))).toEqual([]);
    expect(buildAppBoxes(app({ instances: [] }))).toEqual([]);
  });
  it("未列出的 env 排末（不崩）", () => {
    const boxes = buildAppBoxes(
      app({
        instances: [
          { env: "staging", status: "running", url: "", host_port: 0, version: 0 },
          { env: "test", status: "running", url: "", host_port: 0, version: 0 },
        ],
      })
    );
    expect(boxes.map((b) => b.env)).toEqual(["test", "staging"]);
  });
});

describe("buildDepBoxes", () => {
  it("bound dep 交叉引用 catalog 取 host/port/name + envValue=host:port", () => {
    const boxes = buildDepBoxes(
      [dep("pg", { instance: "sv1", status: "bound", token: "app_abc" })],
      [cinst("sv1", "pg", "10.10.0.28", 5432)]
    );
    expect(boxes[0]).toMatchObject({
      kind: "pg",
      status: "bound",
      envLabel: "DATABASE_URL",
      envValue: "10.10.0.28:5432",
      host: "10.10.0.28",
      port: 5432,
      name: "sv1-name",
      token: "app_abc",
      error: "",
    });
  });
  it("declared dep（无 instance）→ envValue 空/port 0", () => {
    const boxes = buildDepBoxes([dep("redis", { status: "declared" })], []);
    expect(boxes[0].envValue).toBe("");
    expect(boxes[0].port).toBe(0);
    expect(boxes[0].envLabel).toBe("REDIS_ADDR");
  });
  it("failed dep → 带 error、envValue 空", () => {
    const boxes = buildDepBoxes([dep("milvus", { status: "failed", error: "配额超限" })], []);
    expect(boxes[0].status).toBe("failed");
    expect(boxes[0].error).toBe("配额超限");
    expect(boxes[0].envValue).toBe("");
    expect(boxes[0].envLabel).toBe("MILVUS_ADDR");
  });
  it("dep.instance 找不到 catalog 项 → envValue 空 不崩", () => {
    const boxes = buildDepBoxes(
      [dep("pg", { instance: "ghost", status: "bound" })],
      [cinst("other", "pg")]
    );
    expect(boxes[0].envValue).toBe("");
    expect(boxes[0].port).toBe(0);
  });
  it("空 deps → []", () => {
    expect(buildDepBoxes([], [cinst("sv1", "pg")])).toEqual([]);
  });
});
