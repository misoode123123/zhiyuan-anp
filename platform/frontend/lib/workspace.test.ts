import { describe, it, expect } from "vitest";
import { parseDiff, statusColor, fileStatusColor, formatRelativeTime } from "./workspace";

describe("parseDiff", () => {
  it("按行首字符分 add/del/ctx/hunk/meta", () => {
    const diff = `diff --git a/f b/f
@@ -1,2 +1,2 @@
 ctx
-old
+new`;
    const lines = parseDiff(diff);
    expect(lines[0].type).toBe("meta");
    expect(lines[1].type).toBe("hunk");
    expect(lines[2].type).toBe("ctx");
    expect(lines[3].type).toBe("del");
    expect(lines[4].type).toBe("add");
  });
  it("空 diff 返回空数组", () => {
    expect(parseDiff("")).toEqual([]);
  });
});

describe("statusColor", () => {
  it("delivered/approved 绿、pending 黄、developing 蓝、draft 灰", () => {
    expect(statusColor("delivered")).toBe("#3fb950");
    expect(statusColor("approved")).toBe("#3fb950");
    expect(statusColor("pending")).toBe("#d29922");
    expect(statusColor("developing")).toBe("#007acc");
    expect(statusColor("draft")).toBe("#8b949e");
    expect(statusColor("unknown")).toBe("#8b949e");
  });
});

describe("fileStatusColor", () => {
  it("M 黄 / A 绿 / D 红 / U 紫", () => {
    expect(fileStatusColor("M")).toBe("#d29922");
    expect(fileStatusColor("A")).toBe("#2da44e");
    expect(fileStatusColor("D")).toBe("#cf222e");
    expect(fileStatusColor("U")).toBe("#8250df");
  });
});

describe("formatRelativeTime", () => {
  it("未来 2h 内显示『刚刚/N 分钟前』，更早显示原 ISO 日期串前段", () => {
    const just = new Date(Date.now() - 5 * 60 * 1000).toISOString();
    expect(formatRelativeTime(just)).toContain("分钟前");
  });
});
