// ModelSelect 的纯逻辑（与 React 解耦，便于单测）。

export type AIModel = {
  id: string; // compute_model.id（cmd_xxx）—— 全程模型标识符
  provider_id: string;
  name: string;
  display_name?: string;
  modality?: string;
  enabled?: boolean;
};

// 显示名：优先 display_name，否则 name。
export function modelLabel(m: AIModel): string {
  return m.display_name || m.name;
}

// 默认选中值：有授权取第一个；无授权回退该 task_type 路由的 primary（可能为空）。
export function pickDefaultModel(granted: AIModel[], routePrimaryId: string): string {
  if (granted.length > 0) return granted[0].id;
  return routePrimaryId;
}
