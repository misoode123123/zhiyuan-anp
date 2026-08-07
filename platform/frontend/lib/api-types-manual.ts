/**
 * 手动维护的 API 类型（不随 openapi-typescript 再生丢失）。
 *
 * `lib/api-types.ts` 由 CI（swag init + swagger2openapi + openapi-typescript）全自动生成，
 * 后端部分接口的 @Success 用 map[string]interface{} 无类型返回，swag 不会为这些 struct 生成
 * schema，故 openapi-typescript 产出的 api-types.ts 不含以下 interface。前端需要这些类型时，
 * 统一从这里导入，而非塞进生成文件（塞进去会被下次 regen 覆盖 → CI `git diff --exit-code` 漂移红）。
 *
 * 字段须与对应后端 struct 保持同步：
 *   Artifact         ← internal/appdeploy/model.go
 *   DeployNode       ← internal/appdeploy/node.go（列表接口已掩码敏感凭证）
 *   ServerMetric     ← internal/appdeploy/metric_store.go
 *   DeployNodeListItem ← handler.go ListNodes 的 nodeWithCount
 */

/**
 * 构建产物（非 web/service 应用的可下载产物，如桌面安装包/CLI 二进制/移动包）。
 */
export interface Artifact {
  id: string;
  application_id: string;
  build_version: number;
  app_kind: string;
  /** @description windows/macos/linux/android/ios/multi */
  platform: string;
  /** @description x64/arm64/x86/universal/multi */
  arch: string;
  filename: string;
  size_bytes: number;
  sha256: string;
  storage_key: string;
  content_type: string;
  created_at: string;
}

/**
 * 部署节点（.28 本地 / .30 远程）。
 * 敏感凭证（ssh_key/winrm_password）列表接口已掩码，故此处不暴露。
 */
export interface DeployNode {
  id: string;
  name: string;
  host: string;
  docker_url: string;
  ssh_user: string;
  status: string;
  max_apps: number;
  description?: string;
  created_at: string;
  /** @description linux/windows */
  os_type: string;
  /** @description dev/prod 等 */
  env: string;
  /** @description docker_tcp / ssh / winrm */
  connect_type: string;
  ssh_port: number;
  winrm_user?: string;
  /** @description WinRM 端口（默认 5985）；connect_type=winrm 时使用 */
  winrm_port?: number;
  last_seen?: string;
}

/**
 * 服务器指标单次采样。
 */
export interface ServerMetric {
  node_id: string;
  captured_at: string;
  cpu_percent: number;
  mem_total: number;
  mem_used: number;
  disk_total: number;
  disk_used: number;
  load_avg?: number;
  uptime?: string;
  app_count: number;
  container_count?: number;
}

/**
 * /deploy-nodes 列表项：DeployNode + 应用数 + 最新指标。
 * 对应 handler.go ListNodes 的 nodeWithCount。
 */
export interface DeployNodeListItem extends DeployNode {
  app_count: number;
  latest_metric?: ServerMetric;
  has_os_creds: boolean;
}
