// 编码工作台左面板共享类型。
// 从 context-drawer.tsx 迁移需求/变更/发布，并补 git 变更与视图状态。

export type CommitInfo = {
  sha: string;
  author?: string;
  message: string;
  date: string;
};

export type FileChange = {
  path: string;
  status: string; // M/A/D/U/R/C
};

export type GitStatus = {
  worktree_exists: boolean;
  branch: string;
  changes: FileChange[];
  commits: CommitInfo[];
};

export type Req = {
  id: string;
  title: string;
  status: string;
  priority?: string;
  fixed_version?: string;
  tasks?: string;
  assignee?: string;
  description?: string;
  user_story?: string;
  acceptance_criteria?: string;
};

export type Chg = {
  id: string;
  kind: string;
  status: string;
  source_id: string;
  created_at: string;
  output?: string;
};

export type Rel = { id: string; version: string; status: string; created_at: string };

export type WorkspaceDetail = {
  application?: {
    name?: string;
    instances?: { env: string; status: string; url: string }[];
    last_error?: string;
  };
  requirements?: Req[];
  changes?: Chg[];
  releases?: Rel[];
  commits?: CommitInfo[]; // 备用（git 走独立 git-status 接口）
};

// 需求视图操作状态与回调（来自 WorkspaceFrame，透传到 RequirementsView）。
export type ReqState = {
  dispatching: boolean;
  testing: boolean;
  breaking: boolean;
  submitting: boolean;
  merging: boolean;
  taskMsg: string;
  testMsg: string;
  testResults:
    | {
        method?: string;
        path?: string;
        expected_status?: number;
        actual_status?: number;
      }[]
    | null;
  subtasks: { text: string; done: boolean }[];
  submitMsg: string;
};

export type ReqActions = {
  dispatch: (taskIdx?: number) => void;
  runAutoTest: () => void;
  breakdown: () => void;
  submit: () => void;
  merge: () => void;
  toggleSubtask: (i: number) => void;
};
