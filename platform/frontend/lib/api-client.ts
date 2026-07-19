// 类型化 API client（基于 OpenAPI 生成的 paths）。
// 渐进替换 lib/api.ts 的手写 fetch——新代码优先用 apiClient.GET/POST（路径+body 类型检查）。
// 鉴权与 api.ts 一致：Bearer token + X-User 兜底 + X-Project-Space-Id。
import createClient from "openapi-fetch";
import type { paths } from "./api-types";
import { API_BASE_URL, getAuthToken, currentUser, currentProjectSpace } from "./api";

export const apiClient = createClient<paths>({ baseUrl: API_BASE_URL });

// 鉴权中间件：每次请求自动注入身份头（与 api.ts installAuthInterceptor 行为一致）。
apiClient.use({
  async onRequest({ request }) {
    const token = getAuthToken();
    if (token) request.headers.set("Authorization", `Bearer ${token}`);
    // M1 兜底：未登录或 token 缺失时用 X-User 模拟（后续接 SSO 撤掉）
    request.headers.set("X-User", currentUser());
    const ps = currentProjectSpace();
    if (ps) request.headers.set("X-Project-Space-Id", ps);
    return request;
  },
});

// 示范用法（类型化路径 + body）：
//   const { data, error } = await apiClient.POST("/auth/login", { body: { name, password } });
// 其中 body 按 loginBody 类型校验（name/password required）。
// 注：response 暂为 map（swag @Success 用 map[string]interface{}），后续 OA-5 给 @Success
// 具体 struct 后，data.data.token 等也强类型。
