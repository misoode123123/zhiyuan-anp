package quota

import (
	"errors"
	"fmt"
)

// QuotaExceededError 配额超限错误。携带维度 / 已用 / 上限 / 单位，便于前端友好提示。
type QuotaExceededError struct {
	Dimension string // apps / databases / capability_today / db_size
	Used      int    // 已用值（db_size 单位 MB）
	Limit     int    // 上限值
	Unit      string // 显示单位（"" / "MB" / "次"）
}

func (e *QuotaExceededError) Error() string {
	if e == nil {
		return "配额超限"
	}
	dimLabel := map[string]string{
		DimensionApps:            "应用数",
		DimensionDatabases:       "数据库数",
		DimensionCapabilityToday: "今日 AI 调用",
		DimensionDBSize:          "数据库总大小",
	}[e.Dimension]
	if dimLabel == "" {
		dimLabel = e.Dimension
	}
	return fmt.Sprintf("%s已达上限：%d%s / %d%s", dimLabel, e.Used, e.Unit, e.Limit, e.Unit)
}

// IsQuotaExceeded 判断错误是否为配额超限。
func IsQuotaExceeded(err error) bool {
	var qe *QuotaExceededError
	return errors.As(err, &qe)
}

// AsQuotaExceeded 取出 *QuotaExceededError（非超限错误返回 nil）。
func AsQuotaExceeded(err error) *QuotaExceededError {
	var qe *QuotaExceededError
	if errors.As(err, &qe) {
		return qe
	}
	return nil
}
