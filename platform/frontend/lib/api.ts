// 前端 API 客户端：Go 后端 (:8080) 与 AI 运行时 (:8001)
export const API_BASE_URL = process.env.NEXT_PUBLIC_API_BASE_URL || "http://localhost:8080/api/v1";
export const AGENT_RUNTIME_URL =
  process.env.NEXT_PUBLIC_AGENT_RUNTIME_URL || "http://localhost:8001";

// ---- M1 模拟登录：当前用户 / 项目空间（localStorage 持久化） ----
// 后端 RBAC 强制后，写/危险操作需 X-User 头标识用户；后续接 OIDC/SSO 时替换此处。
const USER_KEY = "anp.current_user";
const PS_KEY = "anp.current_project_space";
const DEFAULT_USER = "admin"; // 与后端 SeedBootstrapAdmin 对齐，保证默认可用

export function currentUser(): string {
  if (typeof window === "undefined") return DEFAULT_USER;
  return window.localStorage.getItem(USER_KEY) || DEFAULT_USER;
}
export function setCurrentUser(u: string): void {
  if (typeof window === "undefined") return;
  window.localStorage.setItem(USER_KEY, u);
  // 通知切换器等组件刷新
  window.dispatchEvent(new Event("anp:user-changed"));
}

export function currentProjectSpace(): string {
  if (typeof window === "undefined") return "";
  return window.localStorage.getItem(PS_KEY) || "";
}
export function setCurrentProjectSpace(ps: string): void {
  if (typeof window === "undefined") return;
  window.localStorage.setItem(PS_KEY, ps);
  window.dispatchEvent(new Event("anp:ps-changed"));
}

// ---- 真实登录：token（Bearer）持久化；未登录时回退 X-User 模拟 ----
const TOKEN_KEY = "anp.auth_token";

export function getAuthToken(): string | null {
  if (typeof window === "undefined") return null;
  return window.localStorage.getItem(TOKEN_KEY);
}
export function setAuthToken(token: string, user: string): void {
  if (typeof window === "undefined") return;
  window.localStorage.setItem(TOKEN_KEY, token);
  setCurrentUser(user);
}
export function clearAuthToken(): void {
  if (typeof window === "undefined") return;
  window.localStorage.removeItem(TOKEN_KEY);
  window.dispatchEvent(new Event("anp:user-changed"));
}
export function isLoggedIn(): boolean {
  return !!getAuthToken();
}

// ---- 全局 fetch 拦截：跨域 API 调用自动带 X-User / X-Project-Space-Id ----
// 集中注入，避免逐页面改 fetch；仅拦截发往后端的请求，其余原样放行。
let interceptorInstalled = false;
export function installAuthInterceptor(): void {
  if (interceptorInstalled || typeof window === "undefined" || !window.fetch) return;
  interceptorInstalled = true;
  const origFetch = window.fetch.bind(window);
  window.fetch = (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === "string" ? input : input.toString();
    if (url.includes(API_BASE_URL) || url.includes(AGENT_RUNTIME_URL)) {
      const headers = new Headers(init?.headers);
      // 已登录 → 带 Authorization Bearer token；无 token 不再回退 X-User(撤模拟回退,2026-07-20)
      // 未登录请求会 401 → 登录守卫(Shell)跳 /login
      const token = getAuthToken();
      if (token && !headers.has("Authorization")) {
        headers.set("Authorization", "Bearer " + token);
      }
      const ps = currentProjectSpace();
      if (ps && !headers.has("X-Project-Space-Id")) headers.set("X-Project-Space-Id", ps);
      return origFetch(input, { ...init, headers });
    }
    return origFetch(input, init);
  };
}

export async function apiGet<T>(path: string): Promise<T> {
  const res = await fetch(`${API_BASE_URL}${path}`, { cache: "no-store" });
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
  return res.json() as Promise<T>;
}
