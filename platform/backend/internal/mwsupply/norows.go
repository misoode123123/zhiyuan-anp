package mwsupply

import "database/sql"

// isNoRows 判断是否「无行」错误（LookupBindExisting 用：无实例返回 nil,nil）。
func isNoRows(err error) bool { return err == sql.ErrNoRows }
