// DevPage（手动提交编码）组件测试：
// - ModelSelect 已替换原 free-text input，渲染模型下拉；
// - POST /code 的 body：model 有值时带 cmd_xxx；未选（空）时 model 为 undefined（被 stringify 丢弃）。
import { describe, it, expect } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import DevPage from "../page";
import { installFetchMock, ok } from "@/lib/test-utils";

async function dispatch(prompt: string) {
  // 等项目空间加载（psID 置位后派发按钮才启用）
  await waitFor(() => {
    expect((screen.getByText("提交编码任务") as HTMLButtonElement).disabled).toBe(false);
  });
  fireEvent.change(screen.getByPlaceholderText(/创建 hello\.py/), { target: { value: prompt } });
  fireEvent.click(screen.getByText("提交编码任务"));
}

function findCodeBody(fetchMock: ReturnType<typeof installFetchMock>) {
  const call = fetchMock.mock.calls.find(
    (c) => String(c[0]).endsWith("/code") && (c[1] as RequestInit)?.method === "POST"
  );
  if (!call) throw new Error("未捕获 POST /code");
  return JSON.parse(String((call[1] as RequestInit).body));
}

describe("DevPage 手动提交编码", () => {
  it("渲染 ModelSelect（模型下拉取代 free-text input）", async () => {
    const fetchMock = installFetchMock();
    fetchMock.mockResolvedValue(ok([{ id: "cmd_glm51", provider_id: "p", name: "glm-5.1" }]));
    render(<DevPage />);
    expect(screen.getByText("模型")).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByRole("option", { name: "glm-5.1" })).toBeInTheDocument()
    );
  });

  it("授权模型 → POST /code body.model = 选中模型（cmd_xxx）", async () => {
    const fetchMock = installFetchMock();
    fetchMock.mockImplementation(async (url: string | URL, init?: RequestInit) => {
      const u = String(url);
      const method = init?.method ?? "GET";
      if (u.endsWith("/project-spaces")) return ok([{ id: "ps1", name: "默认", slug: "default" }]);
      if (u.includes("/code-tasks")) return ok([]);
      if (u.endsWith("/users/me/models"))
        return ok([{ id: "cmd_glm51", provider_id: "p", name: "glm-5.1" }]);
      if (u.endsWith("/compute/routes")) return ok([]);
      if (u.endsWith("/code") && method === "POST") return ok({ task_id: "task_1" });
      return ok({});
    });

    render(<DevPage />);
    // 等 ModelSelect 把授权模型 seed 进 state（option 出现即 onChange 已触发 setModel）
    await waitFor(() =>
      expect(screen.getByRole("option", { name: "glm-5.1" })).toBeInTheDocument()
    );

    await dispatch("创建 hello.py 打印 hello world");

    await waitFor(() => {
      expect(findCodeBody(fetchMock).model).toBe("cmd_glm51");
    });
  });

  it("未授权且无路由 → POST /code body 不含 model（undefined）", async () => {
    const fetchMock = installFetchMock();
    fetchMock.mockImplementation(async (url: string | URL, init?: RequestInit) => {
      const u = String(url);
      const method = init?.method ?? "GET";
      if (u.endsWith("/project-spaces")) return ok([{ id: "ps1", name: "默认", slug: "default" }]);
      if (u.includes("/code-tasks")) return ok([]);
      if (u.endsWith("/users/me/models")) return ok([]);
      if (u.endsWith("/compute/routes")) return ok([]);
      if (u.endsWith("/code") && method === "POST") return ok({ task_id: "task_1" });
      return ok({});
    });

    render(<DevPage />);
    await dispatch("独立编码任务");

    await waitFor(() => {
      expect(findCodeBody(fetchMock).model).toBeUndefined();
    });
  });
});
