// topology.ts — 应用部署拓扑拼装纯函数（v2：富参数盒 + 连接 env 值）。
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

// app.instances 元素的形状（与 page.tsx 的 Instance 兼容；只取拓扑所需字段）。
export type AppInstanceLike = {
  env: string;
  status: string;
  url: string;
  host_port: number;
  version: number;
  image?: string;
};

// 拓扑所需应用字段（与 page.tsx 的 App 兼容；只取拓扑所需字段）。
export type AppLike = {
  name: string;
  image?: string;
  container_name?: string;
  internal_port?: number;
  app_kind?: string;
  network_mode?: string;
  instances?: AppInstanceLike[];
};

// 应用容器盒（每个已部署 env 一个；带完整运行参数）。
export type AppBox = {
  appName: string;
  appKind: string;
  env: string; // test / prod
  status: string; // running / building / failed / stopped ...
  host: string; // 从 url 派生（无 url 则空）
  port: number; // host_port（宿主端口）
  internalPort: number; // 容器内端口
  version: number;
  image: string;
  containerName: string;
  networkMode: string;
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

// 从 app 派生应用盒（仅含已部署 env；按 env 排序 test 前 prod 后；带 app 级运行参数）。
export function buildAppBoxes(app: AppLike): AppBox[] {
  return (app.instances ?? [])
    .map((i) => ({
      appName: app.name,
      appKind: app.app_kind ?? "",
      env: i.env,
      status: i.status,
      host: hostFromURL(i.url),
      port: i.host_port,
      internalPort: app.internal_port ?? 0,
      version: i.version,
      image: i.image ?? app.image ?? "",
      containerName: app.container_name ?? "",
      networkMode: app.network_mode ?? "bridge",
    }))
    .sort((a, b) => envRank(a.env) - envRank(b.env));
}

// 中间件依赖实例盒（带连接参数）。
export type DepBox = {
  kind: string;
  strategy: string; // shared / bind_existing / dedicated
  status: string; // bound / declared / failed
  envLabel: string; // kindEnvLabel(kind)
  envValue: string; // 连接地址 host:port（bound 时；否则空）
  host: string;
  port: number;
  name: string; // 实例名（bound 时）
  token: string; // 隔离 token（shared：db号/前缀）
  error: string; // failed 时的 last_error
};

// 由 deps + catalog 拼依赖盒：dep.instance(id) 交叉引用 catalog.instances 取 host/port/name。
// bound → 有连接地址；declared/failed → envValue 空（failed 带 error）。
export function buildDepBoxes(deps: Dep[], instances: CatalogInstance[]): DepBox[] {
  return deps.map((d) => {
    const inst = d.instance ? instances.find((c) => c.id === d.instance) : undefined;
    const host = inst?.host ?? "";
    const port = inst?.port ?? 0;
    return {
      kind: d.kind,
      strategy: d.strategy,
      status: d.status,
      envLabel: kindEnvLabel(d.kind),
      envValue: host && port ? `${host}:${port}` : host,
      host,
      port,
      name: inst?.name ?? "",
      token: d.token ?? "",
      error: d.error ?? "",
    };
  });
}
