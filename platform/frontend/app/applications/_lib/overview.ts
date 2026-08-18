// 总览条格子推导（纯函数）：App+instances → 一格展示模型。
// 环境迷你态：ok=实例 running / bad=实例存在但非 running / none=无实例。
import type { App } from "./types";

export type CellEnvState = "ok" | "bad" | "none";

export type OverviewCell = {
  id: string;
  name: string;
  status: string; // App.status 原值（running/building/...）
  isExternal: boolean;
  isImporting: boolean;
  importingHint: string; // importing 时 last_error 进度文案
  version: number;
  showVersion: boolean; // external 或 version=0 不显示
  test: CellEnvState;
  prod: CellEnvState;
};

function envState(app: App, env: "test" | "prod"): CellEnvState {
  const ins = app.instances?.find((i) => i.env === env);
  if (!ins) return "none";
  return ins.status === "running" ? "ok" : "bad";
}

export function buildOverviewCells(apps: App[]): OverviewCell[] {
  return apps.map((a) => ({
    id: a.id,
    name: a.name,
    status: a.status,
    isExternal: a.deploy_mode === "external",
    isImporting: a.status === "importing",
    importingHint: a.last_error || "",
    version: a.version,
    showVersion: a.deploy_mode !== "external" && a.version > 0,
    test: envState(a, "test"),
    prod: envState(a, "prod"),
  }));
}
