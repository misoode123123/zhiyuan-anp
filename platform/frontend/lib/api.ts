// 前端 API 客户端：Go 后端 (:8080) 与 AI 运行时 (:8001)
export const API_BASE_URL =
  process.env.NEXT_PUBLIC_API_BASE_URL || "http://localhost:8080/api/v1";
export const AGENT_RUNTIME_URL =
  process.env.NEXT_PUBLIC_AGENT_RUNTIME_URL || "http://localhost:8001";

export async function apiGet<T>(path: string): Promise<T> {
  const res = await fetch(`${API_BASE_URL}${path}`, { cache: "no-store" });
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
  return res.json() as Promise<T>;
}
