package mwsupply

import (
	"database/sql"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// isNoRows 判断是否「无行」错误（Lookup* 用：无实例/绑定返回 nil,nil）。
func isNoRows(err error) bool { return err == sql.ErrNoRows }

// isUniqueViolation 判断是否 PG 唯一约束冲突（错误码 23505）。
// shared token 并发兜底：partial unique index uq_svbind_inst_token 命中时调用方重试换号。
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}
