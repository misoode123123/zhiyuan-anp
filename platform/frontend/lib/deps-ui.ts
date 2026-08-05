// deps-ui.ts 依赖勾选器相关类型 + 只读实例分组的纯函数。
// 后端契约：appdeploy.DepDeclaration / CatalogInstance / DepsCatalog。
// 测试见 deps-ui.test.ts；组件消费见 app/applications/page.tsx (DepsSection)。

// 依赖声明（后端 appdeploy.DepDeclaration）：kind/strategy 用户编，status/instance/token 供给结果。
export type Dep = {
  kind: string;
  strategy: string;
  status: string;
  instance?: string;
  token?: string;
  error?: string;
};

// catalog 实例（后端 appdeploy.CatalogInstance）。
export type CatalogInstance = {
  id: string;
  kind: string;
  name: string;
  supply_mode: string;
  host: string;
  port: number;
};

// 依赖勾选器 catalog（后端 appdeploy.DepsCatalog）。
export type DepsCatalog = {
  kinds: string[];
  strategies: { name: string; desc: string }[];
  instances: CatalogInstance[];
};

// groupInstancesByKind 按 kind 分组实例，保持 catalog 出现顺序（Map 保插入序）。
// UI 只读「可绑定实例」面板用：每组渲染一个 kind 区块（含 pg）。
export function groupInstancesByKind(instances: CatalogInstance[]): Map<string, CatalogInstance[]> {
  const out = new Map<string, CatalogInstance[]>();
  for (const ins of instances) {
    const arr = out.get(ins.kind);
    if (arr) {
      arr.push(ins);
    } else {
      out.set(ins.kind, [ins]);
    }
  }
  return out;
}
