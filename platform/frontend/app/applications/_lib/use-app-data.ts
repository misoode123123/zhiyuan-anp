"use client";

// 数据 hook（自 page.tsx 平移，行为零变化）：spaces 在壳内（选空间属页面级），
// 这里管 apps/nodes/appStats/appChanges/deployMsg + 双轮询（3s building 刷新 / 30s stats+changes）。
// detail 三态留在壳（T7/T8 迁面板），refreshClosedLoop 以参数注入 detailFor/setDetail。
import { useEffect, useState } from "react";
import { API_BASE_URL } from "@/lib/api";
import { logger } from "@/lib/logger";
import { toast } from "@/lib/toast";
import type { App, AppStats, ChangeSummary, Detail, Envelope, NodeInfo } from "./types";

export function useAppData(psID: string) {
  const [apps, setApps] = useState<App[]>([]);
  const [nodes, setNodes] = useState<NodeInfo[]>([]);
  const [appStats, setAppStats] = useState<Record<string, AppStats>>({});
  const [appChanges, setAppChanges] = useState<Record<string, ChangeSummary[]>>({});
  const [deployMsg, setDeployMsg] = useState<Record<string, string>>({});

  const load = (id: string) => {
    if (!id) return;
    fetch(`${API_BASE_URL}/project-spaces/${id}/apps`)
      .then((r) => r.json())
      .then((r: Envelope<App[]>) => setApps(r.data ?? []));
    fetch(`${API_BASE_URL}/deploy-nodes`)
      .then((r) => r.json())
      .then((r: Envelope<typeof nodes>) => setNodes(r.data ?? []));
  };
  useEffect(() => {
    load(psID);
    // 有 building 中的应用时轮询
    const t = setInterval(() => {
      load(psID);
      // 清理已完成的部署进度提示
      setDeployMsg((prev) => {
        if (Object.keys(prev).length === 0) return prev;
        const next = { ...prev };
        for (const id of Object.keys(next)) {
          const app = apps.find((a) => a.id === id);
          if (app && app.status !== "building" && app.status !== "preparing") {
            toast.success(app.name + " → " + app.status);
            logger.info("app.deploy.done", { app: app.name, status: app.status });
            delete next[id];
          }
        }
        return next;
      });
    }, 3000);
    return () => clearInterval(t);
  }, [psID]);
  async function loadStats(id: string) {
    try {
      const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${id}/stats?env=prod`);
      const r = await res.json();
      if (r.code === 0) {
        const d = r.data;
        setAppStats((p) => ({
          ...p,
          [id]: {
            health: d.health,
            cpu: d.stats?.cpu_perc,
            mem: d.stats?.mem_usage,
            deployed: d.deployed,
            external: d.external,
            url: d.url,
          },
        }));
      }
    } catch {}
  }
  async function loadChanges(id: string) {
    try {
      const res = await fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${id}/detail`);
      const r = await res.json();
      if (r.code === 0 && r.data?.changes) {
        setAppChanges((p) => ({ ...p, [id]: r.data.changes }));
      }
    } catch {}
  }
  // 轮询已上线(prod running)应用的资源/健康（运维可观测）
  useEffect(() => {
    const poll = () =>
      apps.forEach((a) => {
        // external 应用无 instances，直接按 deploy_mode 探活；managed 看 prod running
        if (
          a.deploy_mode === "external" ||
          a.instances?.some((i) => i.env === "prod" && i.status === "running")
        )
          loadStats(a.id);
      });
    poll();
    apps.forEach((a) => loadChanges(a.id));
    const t = setInterval(poll, 30000);
    return () => clearInterval(t);
  }, [apps]);

  // 闭环操作后刷新：loadChanges 更新待上线徽标，详情已展开则重拉三栏。
  // detailFor/setDetail 由调用方注入（detail 态留在壳，T7/T8 迁 tab 面板）。
  function refreshClosedLoop(
    appID: string,
    detailFor: string,
    setDetail: (d: Detail | null) => void
  ) {
    loadChanges(appID);
    if (detailFor === appID) {
      fetch(`${API_BASE_URL}/project-spaces/${psID}/apps/${appID}/detail`)
        .then((r) => r.json())
        .then((r: Envelope<Detail>) => {
          if (r.code === 0) setDetail(r.data ?? null);
        });
    }
  }

  return {
    apps,
    nodes,
    appStats,
    appChanges,
    deployMsg,
    setDeployMsg, // act（use-app-actions）写进度横幅经此注入；态在此因 3s 清理轮询在此
    reload: load,
    loadChanges,
    refreshClosedLoop,
  };
}
