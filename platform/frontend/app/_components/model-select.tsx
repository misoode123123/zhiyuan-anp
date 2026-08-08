"use client";
import { useEffect, useState } from "react";
import { apiGet } from "@/lib/api";
import { modelLabel, pickDefaultModel, type AIModel } from "@/lib/model-select";

type Envelope<T> = { code: number; message?: string; data: T };
type Route = { task_type: string; primary_model_id: string };

/**
 * ModelSelect — 当前用户已授权模型下拉（受控）。
 * 数据源 GET /users/me/models；空授权时回退到该 task_type 路由的 primary（取 /compute/routes 列表过滤）。
 * value/onChange 由父组件持有；taskType 决定空授权回退的默认模型来源。
 * option value = Model.id（cmd_xxx），与后端 Gateway/授权表标识符一致。
 */
export function ModelSelect({
  value,
  onChange,
  taskType,
  className,
}: {
  value: string;
  onChange: (v: string) => void;
  taskType: string;
  className?: string;
}) {
  const [models, setModels] = useState<AIModel[]>([]);
  const [fallback, setFallback] = useState(false);

  useEffect(() => {
    let cancelled = false;
    apiGet<Envelope<AIModel[]>>("/users/me/models")
      .then(async (r) => {
        if (cancelled) return;
        const granted = r.data ?? [];
        if (granted.length > 0) {
          setModels(granted);
          setFallback(false);
          if (!value) onChange(pickDefaultModel(granted, ""));
          return;
        }
        // 空授权：回退该 task_type 路由 primary
        setFallback(true);
        const rr = await apiGet<Envelope<Route[]>>("/compute/routes");
        if (cancelled) return;
        const primary =
          (rr.data ?? []).find((x) => x.task_type === taskType)?.primary_model_id ?? "";
        setModels(primary ? [{ id: primary, provider_id: "", name: primary }] : []);
        if (!value) onChange(primary);
      })
      .catch(() => setModels([]));
    return () => {
      cancelled = true;
    };
    // 仅 taskType 变化时重载；value 由父组件控制，不作为依赖。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [taskType]);

  return (
    <div className={className}>
      <label className="text-xs text-text-muted">模型</label>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="w-full rounded-md border border-border px-2 py-1.5 text-sm"
      >
        {models.length === 0 && <option value="">— 平台默认 —</option>}
        {models.map((m) => (
          <option key={m.id} value={m.id}>
            {modelLabel(m)}
          </option>
        ))}
      </select>
      {fallback && <span className="mt-0.5 block text-xs text-warn">未授权模型，使用平台默认</span>}
    </div>
  );
}
