// 应用 tab 状态机（纯函数，视图层不直接改数组）：
// 开=去重+激活；选中=已打开才生效（入口统一走 openTab）；关=激活转移相邻（优先右侧）；
// prune=应用被删后清理失效 id。所有函数不修改入参。
export type TabState = { ids: string[]; activeId: string };

export const EMPTY_TABS: TabState = { ids: [], activeId: "" };

export function openTab(t: TabState, appId: string): TabState {
  if (t.ids.includes(appId)) return { ids: t.ids, activeId: appId };
  return { ids: [...t.ids, appId], activeId: appId };
}

export function selectTab(t: TabState, appId: string): TabState {
  if (!t.ids.includes(appId)) return t;
  return { ...t, activeId: appId };
}

export function closeTab(t: TabState, appId: string): TabState {
  const idx = t.ids.indexOf(appId);
  if (idx < 0) return t;
  const ids = t.ids.filter((id) => id !== appId);
  let activeId = t.activeId;
  if (t.activeId === appId) {
    activeId = ids[idx] ?? ids[idx - 1] ?? ""; // 优先右侧，其次左侧，全关回空
  }
  return { ids, activeId };
}

export function pruneTabs(t: TabState, liveIds: string[]): TabState {
  const ids = t.ids.filter((id) => liveIds.includes(id));
  if (ids.length === t.ids.length) return t;
  const activeId = ids.includes(t.activeId) ? t.activeId : (ids[ids.length - 1] ?? "");
  return { ids, activeId };
}
