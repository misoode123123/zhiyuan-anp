package logsvc

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"zhiyuan-anp/platform/backend/internal/testutil"
)

// TestLog_WarnEntersDB WARN 级应入库（修复前仅 ERROR/FATAL 入）。
func TestLog_WarnEntersDB(t *testing.T) {
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "platform_log")
	dl := NewDualLogger(zap.NewNop(), NewStore(db))

	dl.Log(LogEntryInput{Level: "WARN", Module: "test", Msg: "warn case", TraceID: "t1"})

	var n int
	_ = db.GetContext(context.Background(), &n,
		`SELECT COUNT(*) FROM platform_log WHERE level='WARN' AND trace_id='t1'`)
	if n != 1 {
		t.Fatalf("WARN 应入库 1 条，得到 %d", n)
	}
}

// TestLog_InfoNotInDB INFO 不入库（只 zap）。
func TestLog_InfoNotInDB(t *testing.T) {
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "platform_log")
	dl := NewDualLogger(zap.NewNop(), NewStore(db))

	dl.Log(LogEntryInput{Level: "INFO", Module: "test", Msg: "info case"})

	var n int
	_ = db.GetContext(context.Background(), &n, `SELECT COUNT(*) FROM platform_log WHERE level='INFO'`)
	if n != 0 {
		t.Fatalf("INFO 不应入库，得到 %d", n)
	}
}
