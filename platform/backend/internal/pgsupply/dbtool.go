// 数据库工具层：连应用库（应用 role 的 DATABASE_URL）执行表/列查询与任意 SQL。
// 所有 SQL 执行的调用方（handler）负责记 db_action_log 审计。
package pgsupply

import (
	"context"
	"fmt"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib" // 驱动名 "pgx"
	"github.com/jmoiron/sqlx"
)

// TableInfo 应用库的表信息（information_schema.tables）。
type TableInfo struct {
	Name      string `json:"name" db:"table_name"`
	TableType string `json:"table_type" db:"table_type"`
}

// ColumnInfo 表的列信息（information_schema.columns + 列注释）。
type ColumnInfo struct {
	Name       string `json:"name" db:"name"`
	DataType   string `json:"data_type" db:"data_type"`
	IsNullable string `json:"is_nullable" db:"is_nullable"`
	Default    string `json:"column_default" db:"column_default"`
	Comment    string `json:"comment" db:"comment"`
}

// QueryResult SQL 执行结果。
type QueryResult struct {
	ActionType string           `json:"action_type"`       // SELECT/INSERT/UPDATE/DELETE/DDL/OTHER
	Columns    []string         `json:"columns,omitempty"` // SELECT 的列名
	Rows       []map[string]any `json:"rows,omitempty"`    // SELECT 的行（列名→值）
	RowCount   int64            `json:"row_count"`         // 影响行数(DML) / 返回行数(SELECT)
}

// connectAppDB 用应用 DSN 连应用库（应用 role，仅本库权限）。
func connectAppDB(ctx context.Context, dsn string) (*sqlx.DB, error) {
	db, err := sqlx.ConnectContext(ctx, "pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("连应用库: %w", err)
	}
	return db, nil
}

// ListTables 列应用库 public schema 的表。
func ListTables(ctx context.Context, dsn string) ([]TableInfo, error) {
	db, err := connectAppDB(ctx, dsn)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var list []TableInfo
	err = db.SelectContext(ctx, &list,
		`SELECT table_name, table_type FROM information_schema.tables
		 WHERE table_schema='public' ORDER BY table_name`)
	return list, err
}

// ListColumns 列某表的列。
func ListColumns(ctx context.Context, dsn, table string) ([]ColumnInfo, error) {
	db, err := connectAppDB(ctx, dsn)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var list []ColumnInfo
	err = db.SelectContext(ctx, &list,
		`SELECT column_name AS name, data_type, is_nullable,
		        COALESCE(column_default,'') AS column_default,
		        COALESCE(col_description((table_schema||'.'||table_name)::regclass, ordinal_position),'') AS comment
		 FROM information_schema.columns
		 WHERE table_schema='public' AND table_name=$1
		 ORDER BY ordinal_position`, table)
	return list, err
}

// ExecSQL 执行任意 SQL：SELECT 返回行+列，DDL/DML 返回影响行数。
// 调用方负责审计日志（不在本函数内记，避免连应用库失败时误记）。
func ExecSQL(ctx context.Context, dsn, statement string) (*QueryResult, error) {
	stmt := strings.TrimSpace(statement)
	if stmt == "" {
		return nil, fmt.Errorf("空 SQL")
	}
	db, err := connectAppDB(ctx, dsn)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	actionType := classifySQL(stmt)
	res := &QueryResult{ActionType: actionType}
	if actionType == "SELECT" {
		rows, err := db.QueryxContext(ctx, stmt)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		cols, _ := rows.Columns()
		res.Columns = cols
		for rows.Next() {
			row := map[string]any{}
			if err := rows.MapScan(row); err != nil {
				return nil, err
			}
			// []byte → string（JSON 友好；非文本二进制会乱码，但简单工具够用）
			for k, v := range row {
				if b, ok := v.([]byte); ok {
					row[k] = string(b)
				}
			}
			res.Rows = append(res.Rows, row)
		}
		res.RowCount = int64(len(res.Rows))
	} else {
		result, err := db.ExecContext(ctx, stmt)
		if err != nil {
			return nil, err
		}
		n, _ := result.RowsAffected()
		res.RowCount = n
	}
	return res, nil
}

// classifySQL 粗分类 SQL（首关键字，用于审计 action_type）。
func classifySQL(stmt string) string {
	s := strings.ToUpper(strings.TrimLeft(strings.TrimSpace(stmt), "( \t\n"))
	for _, kw := range []string{"SELECT", "WITH"} {
		if strings.HasPrefix(s, kw) {
			return "SELECT"
		}
	}
	for _, kw := range []string{"INSERT", "UPDATE", "DELETE"} {
		if strings.HasPrefix(s, kw) {
			return kw
		}
	}
	for _, kw := range []string{"CREATE", "ALTER", "DROP", "TRUNCATE", "GRANT", "REVOKE", "COMMENT", "INDEX"} {
		if strings.HasPrefix(s, kw) {
			return "DDL"
		}
	}
	return "OTHER"
}
