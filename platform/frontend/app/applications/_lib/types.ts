// 应用部署页类型定义（从 page.tsx 迁出，原样保留）：API 信封与领域模型。
export type Envelope<T> = { code: number; data: T; message?: string };
export type PS = { id: string; name: string; slug: string };
export type Instance = {
  env: string;
  status: string;
  url: string;
  version: number;
  host_port: number;
  image: string;
  updated_at: string;
};
export type EnvVar = {
  id: string;
  key: string;
  value: string;
  is_secret: boolean;
  source?: string;
};
// 环境变量表单（use-app-actions 的 envForm 态；detail/expand 面板受控传入）
export type EnvForm = { key: string; value: string; is_secret: boolean };
export type AppStats = {
  health?: string;
  cpu?: string;
  mem?: string;
  deployed?: boolean;
  external?: boolean;
  url?: string;
};
export type App = {
  id: string;
  name: string;
  repo_dir: string;
  internal_port: number;
  image: string;
  container_name: string;
  host_port: number;
  url: string;
  version: number;
  status: string;
  last_error: string;
  build_log: string;
  deploy_mode: string; // managed(A类) / external(B类纳管外部)
  external_url: string; // external 模式时外部应用访问地址
  app_kind: string; // 应用类型 web/desktop/mobile/cli/service/headless
  network_mode: string; // bridge(默认) / host(需 gatekeeper/admin)
  import_source?: "" | "git" | "dir"; // 导入来源：''=平台建仓 / git=远程仓 / dir=本机zip或服务器目录
  import_ref?: string; // git=url / dir=来源标识
  imported_at?: string; // 导入完成时间，进行中空
  updated_at: string;
  instances?: Instance[]; // 各环境部署实例（test/prod）
};
// 中间件依赖声明（后端 appdeploy.DepDeclaration）：kind/strategy 用户编，status/instance/token 供给结果。
export type Dep = {
  kind: string;
  strategy: string;
  status: string;
  instance?: string;
  token?: string;
  error?: string;
};
// 依赖勾选器选项（后端 appdeploy.DepsCatalog）：kinds/strategies 固定，instances 为可见 active 实例。
export type DepsCatalog = {
  kinds: string[];
  strategies: { name: string; desc: string }[];
  instances: {
    id: string;
    kind: string;
    name: string;
    supply_mode: string;
    host: string;
    port: number;
  }[];
};
export type Req = { id: string; title: string; status: string; application_id: string };
export type Detail = {
  application: App;
  requirements: Req[];
  changes: {
    id: string;
    status: string;
    kind: string;
    source_id: string;
    created_at: string;
    output?: string;
  }[];
  releases: {
    id: string;
    version: string;
    status: string;
    change_id: string;
    created_at: string;
  }[];
  commits: { sha: string; message: string; date: string }[];
  // P1-c 全景维度（后端 AppFullView）
  instances: Instance[];
  sessions: {
    id: string;
    tool: string;
    started_at: string;
    ended_at?: string;
    prompt_count: number;
  }[];
  tasks: {
    id: string;
    kind: string;
    status: string;
    req_title?: string;
    change_id?: string;
    created_at: string;
  }[];
  routes: {
    env: string;
    app_code: string;
    upstream_host: string;
    upstream_port: number;
    status: string;
    external_url?: string;
  }[];
  deps: Dep[];
  deploy_needs?: {
    mounts: { src: string; dst: string; readonly?: boolean }[];
    env_keys: string[];
    ports: number[];
    command: string;
  };
  // P3：部署历史（后端 deploy_history，最近 20 条，时间线用）
  deploy_history: {
    id: number;
    env: string;
    version: number;
    engine: string; // fixed / ai
    result: string; // ''=在途 / success / failed
    operator: string;
    duration_sec?: number | null;
    error_summary?: string;
    notes?: string;
    created_at: string;
  }[];
};

// 部署节点（原 page.tsx 内联 useState 泛型提升命名）
export type NodeInfo = {
  id: string;
  name: string;
  host: string;
  status: string;
  app_count?: number;
  env?: string;
  os_type?: string;
  connect_type?: string;
};
// 变更摘要（appChanges 轮询行内类型提升命名；与 Detail.changes 字段子集兼容）
export type ChangeSummary = { id: string; status: string; output?: string; created_at?: string };
