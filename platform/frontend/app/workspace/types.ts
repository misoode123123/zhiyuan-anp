// 编码工作台共享类型。
// 无需求化后：左侧需求/发布面板已移除，仅保留应用上下文类型。

export type WorkspaceDetail = {
  application?: {
    name?: string;
    instances?: { env: string; status: string; url: string }[];
    last_error?: string;
  };
};
