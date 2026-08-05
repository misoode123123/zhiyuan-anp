// topology.ts 应用部署拓扑拼装的纯函数（kind→envLabel、应用盒、依赖盒）。
// 后端契约：appdeploy.DepDeclaration / CatalogInstance（见 ./deps-ui）。
// 测试见 topology.test.ts；组件消费见 app/applications/page.tsx (TopologySection)。
import type { CatalogInstance, Dep } from "./deps-ui";

// kind → 注入的 env 变量名（mwsupply 注入源，单源 map；新增 kind 同步此处）。
export const KIND_ENV_LABEL: Record<string, string> = {
  pg: "DATABASE_URL",
  redis: "REDIS_ADDR",
  milvus: "MILVUS_ADDR",
};

export function kindEnvLabel(kind: string): string {
  return KIND_ENV_LABEL[kind] ?? kind.toUpperCase() + "_ADDR";
}

// 应用容器盒（每个已部署 env 一个）。
export type AppBox = {
  env: string; // test / prod
  status: string; // running / building / failed / stopped ...
  host: string; // 从 url 派生（无 url 则空）
  port: number; // host_port
  version: number;
  image: string;
};

// app.instances 的形状（与 page.tsx 的 Instance 兼容；只取拓扑所需字段）。
export type AppInstanceLike = {
  env: string;
  status: string;
  url: string;
  host_port: number;
  version: number;
  image?: string;
};

const ENV_ORDER = ["test", "prod"];
function envRank(env: string): number {
  const i = ENV_ORDER.indexOf(env);
  return i === -1 ? ENV_ORDER.length : i;
}

function hostFromURL(url: string): string {
  if (!url) return "";
  try {
    return new URL(url).host;
  } catch {
    return "";
  }
}

// 从 app.instances 派生应用盒（仅含已部署 env；按 env 排序 test 前 prod 后）。
export function appBoxes(instances: AppInstanceLike[] | undefined): AppBox[] {
  return (instances ?? [])
    .map((i) => ({
      env: i.env,
      status: i.status,
      host: hostFromURL(i.url),
      port: i.host_port,
      version: i.version,
      image: i.image ?? "",
    }))
    .sort((a, b) => envRank(a.env) - envRank(b.env));
}

// 中间件依赖实例盒。
export type DepBox = {
  kind: string;
  strategy: string; // shared / bind_existing / dedicated
  status: string; // bound / declared / failed
  envLabel: string; // kindEnvLabel(kind)
  host: string; // 解析到的实例 host（bound 时从 catalog；无则空）
  port: number;
  name: string; // 实例名（bound 时）
  error: string; // failed 时的 last_error
};

// 由 deps + catalog 拼依赖盒：dep.instance(id) 交叉引用 catalog.instances 取 host/port/name。
// bound → 有实例信息；declared/failed → host/port 空盒（failed 带 error）。
export function buildDepBoxes(deps: Dep[], instances: CatalogInstance[]): DepBox[] {
  return deps.map((d) => {
    const inst = d.instance ? instances.find((c) => c.id === d.instance) : undefined;
    return {
      kind: d.kind,
      strategy: d.strategy,
      status: d.status,
      envLabel: kindEnvLabel(d.kind),
      host: inst?.host ?? "",
      port: inst?.port ?? 0,
      name: inst?.name ?? "",
      error: d.error ?? "",
    };
  });
}
