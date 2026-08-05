// groupInstancesByKind 纯函数测试（vitest node env）。
// 跑法: pnpm --filter frontend test
import { describe, it, expect } from "vitest";
import { groupInstancesByKind, type CatalogInstance } from "./deps-ui";

const inst = (kind: string, name: string, port = 6379): CatalogInstance => ({
  id: `${kind}-${name}`,
  kind,
  name,
  supply_mode: "bind_existing",
  host: "10.10.0.28",
  port,
});

describe("groupInstancesByKind", () => {
  it("多 kind 混合按 kind 分组", () => {
    const groups = groupInstancesByKind([
      inst("redis", "a"),
      inst("pg", "x", 5432),
      inst("milvus", "m", 19530),
      inst("redis", "b"),
    ]);
    expect(groups.get("redis")?.map((i) => i.name)).toEqual(["a", "b"]);
    expect(groups.get("pg")?.map((i) => i.name)).toEqual(["x"]);
    expect(groups.get("milvus")?.map((i) => i.name)).toEqual(["m"]);
  });

  it("保持 catalog 首次出现顺序（Map 保插入序，UI 分组依此渲染）", () => {
    const groups = groupInstancesByKind([
      inst("milvus", "m1", 19530),
      inst("pg", "x1", 5432),
      inst("redis", "r1"),
    ]);
    expect([...groups.keys()]).toEqual(["milvus", "pg", "redis"]);
  });

  it("空数组 → 空 Map", () => {
    expect(groupInstancesByKind([]).size).toBe(0);
  });

  it("同 kind 多实例归同组", () => {
    const groups = groupInstancesByKind([
      inst("pg", "x1", 5432),
      inst("pg", "x2", 5433),
      inst("pg", "x3", 5434),
    ]);
    expect(groups.size).toBe(1);
    expect(groups.get("pg")?.length).toBe(3);
  });

  it("实例原对象引用保留（不深拷贝，UI 直接用 host/port 等字段）", () => {
    const a = inst("redis", "a");
    const groups = groupInstancesByKind([a]);
    expect(groups.get("redis")?.[0]).toBe(a);
  });
});
