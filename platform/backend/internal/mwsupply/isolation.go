package mwsupply

import (
	"encoding/json"
	"strconv"
)

// ParseDBRange 解析 isolation JSONB 的 db_range（如 {"db_range":[1,15]}）。
// 返回 [lo,hi]（含）。无 db_range / 非法 → ok=false。
// 兼容 PG jsonb::text 的带空格输出（json.Unmarshal 忽略空白）。
func ParseDBRange(isolation string) (lo, hi int, ok bool) {
	if isolation == "" {
		return 0, 0, false
	}
	var m struct {
		DBRange []int `json:"db_range"`
	}
	if err := json.Unmarshal([]byte(isolation), &m); err != nil {
		return 0, 0, false
	}
	if len(m.DBRange) != 2 {
		return 0, 0, false
	}
	lo, hi = m.DBRange[0], m.DBRange[1]
	if lo < 0 || hi < lo {
		return 0, 0, false
	}
	return lo, hi, true
}

// pickLowestFree 返回 [lo,hi] 内不在 allocated 的最小号（字符串形式）。
// 全占用 → ("", false)。用于 shared redis db 号分配。
func pickLowestFree(lo, hi int, allocated []string) (string, bool) {
	taken := make(map[string]bool, len(allocated))
	for _, t := range allocated {
		taken[t] = true
	}
	for n := lo; n <= hi; n++ {
		if !taken[strconv.Itoa(n)] {
			return strconv.Itoa(n), true
		}
	}
	return "", false
}
