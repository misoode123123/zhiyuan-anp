#!/usr/bin/env python3
# 将 Go 源码中 SQL 字符串里的 ? 占位符转为 PG 的 $N（按每个字符串内出现顺序编号）。
# 仅处理反引号 raw string 与双引号字符串；仅处理含 SQL 关键字的串；跳过 _test.go。
# 用法: python scripts/rebind_placeholders.py [--apply]   (默认 dry-run 只统计)
import glob, re, sys

APPLY = "--apply" in sys.argv
ROOT = "platform/backend/internal"
SQL_KW = re.compile(r"\b(SELECT|INSERT|UPDATE|DELETE|FROM|WHERE|VALUES|INTO|SET|CREATE|ALTER|TABLE|INDEX)\b", re.I)


def rebind_str(s: str) -> tuple[str, int]:
    """s 是带外层引号的字符串字面量；返回(新串, 替换数)。"""
    quote = s[0]
    inner = s[1:-1]
    cnt = 0

    def rep(_m):
        nonlocal cnt
        cnt += 1
        return "$" + str(cnt)

    new_inner = re.sub(r"\?", rep, inner)
    return quote + new_inner + quote, cnt


def process(text: str) -> tuple[str, int, int]:
    out = []
    i, n = 0, len(text)
    total_files_changed = 0
    total_q = 0
    while i < n:
        c = text[i]
        if c == "`":
            j = text.find("`", i + 1)
            if j < 0:
                out.append(text[i:])
                break
            s = text[i : j + 1]
            if SQL_KW.search(s):
                ns, q = rebind_str(s)
                total_q += q
                out.append(ns)
            else:
                out.append(s)
            i = j + 1
        elif c == '"':
            j = i + 1
            while j < n:
                if text[j] == "\\":
                    j += 2
                    continue
                if text[j] == '"':
                    break
                j += 1
            s = text[i : j + 1]
            if SQL_KW.search(s):
                ns, q = rebind_str(s)
                total_q += q
                out.append(ns)
            else:
                out.append(s)
            i = j + 1
        else:
            out.append(c)
            i += 1
    return "".join(out), total_q


def main():
    grand_q = 0
    files_touched = 0
    for f in sorted(glob.glob(ROOT + "/**/*.go", recursive=True)):
        if f.endswith("_test.go"):
            continue
        src = open(f, encoding="utf-8").read()
        new, q = process(src)
        if q > 0:
            files_touched += 1
            grand_q += q
            print(f"  {f}: {q} 个 ? -> $N")
            if APPLY and new != src:
                open(f, "w", encoding="utf-8").write(new)
    mode = "APPLY" if APPLY else "DRY-RUN(未写入)"
    print(f"\n[{mode}] 共 {files_touched} 文件, {grand_q} 个占位符待转换")


if __name__ == "__main__":
    main()
