package pgsupply

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func TestPasswordFromAdminURL(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"postgres://postgres:secret@h:5432/postgres?sslmode=disable", "secret"},
		{"postgres://u:p%40ss@h:5432/db", "p@ss"}, // url 解码
		{"postgres://u@h:5432/db", ""},            // 无密码
		{"not-a-url", ""},                         // 解析失败兜底空
		{"postgres://postgres:abc@h:5432/postgres", "abc"},
	}
	for _, c := range cases {
		if got := passwordFromAdminURL(c.in); got != c.want {
			t.Fatalf("passwordFromAdminURL(%q):\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

// TestBackupAll_SuccessOrFailures 走 BackupAll 全流程：建 3 个应用库记录 + 1 个故意失败，
// 用 buffer dumper 替代 docker exec，断言：success/failed 计数、产物文件落盘、目录结构正确。
func TestBackupAll_SuccessOrFailures(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	ins := mkInstance("ps_1")
	ins.ContainerName = "pg-test-1"
	_ = s.CreateInstance(ctx, ins)

	// 建 3 个应用库；app_1 已由 newTestStore 建过（跳过）
	for i, appID := range []string{"app_1", "app_b1", "app_b2"} {
		// appdeploy_application FK：补 app_b1/b2
		if appID != "app_1" {
			_, _ = s.db.ExecContext(ctx, `INSERT INTO appdeploy_application (id, project_space_id, name, internal_port, status)
				VALUES ($1,'ps_1',$2,8080,'registered') ON CONFLICT DO NOTHING`, appID, appID)
		}
		ad := &AppDatabase{
			ID: "apdb_x" + itoa(i), AppID: appID, ProjectSpaceID: "ps_1",
			DBName: "app_db" + itoa(i), DBRole: "app_db" + itoa(i) + "_role", PGInstanceID: ins.ID,
			DBHost: "h", DBPort: 9500, Status: StatusReady, BackupEnabled: true,
		}
		if err := s.CreateAppDB(ctx, ad); err != nil {
			t.Fatalf("create appdb %s: %v", appID, err)
		}
	}

	tmp := t.TempDir()
	b := NewBackuper(s, tmp, 0) // retain=0：本 case 不测清理，禁用

	// 注入 dumper：3 个成功 + 1 个故意失败（app_db2）
	body := []byte("PGDMPCUSTOM_FAKE_BODY")
	b.dumpFn = func(ctx context.Context, container, db, pwd string, out io.Writer) error {
		if db == "app_db2" {
			return errString("simulated pg_dump failure")
		}
		_, _ = out.Write(body)
		return nil
	}

	r := b.BackupAll(ctx)
	if r.Total != 3 {
		t.Fatalf("Total 应=3，得到 %d", r.Total)
	}
	if r.Success != 2 || r.Failed != 1 {
		t.Fatalf("Success/Failed 应=2/1，得到 %d/%d", r.Success, r.Failed)
	}
	if len(r.FailedID) != 1 {
		t.Fatalf("FailedID 应有 1 个，得到 %d", len(r.FailedID))
	}

	// 成功的产物落盘 + 目录结构：backupRoot/ps_1/<appID>/*.dump
	for _, appID := range []string{"app_1", "app_b1"} {
		matches, err := filepath.Glob(filepath.Join(tmp, "ps_1", appID, "*.dump"))
		if err != nil || len(matches) != 1 {
			t.Fatalf("appID %s 应有 1 个 dump，得到 %v (err=%v)", appID, matches, err)
		}
		got, _ := os.ReadFile(matches[0])
		if !bytes.Equal(got, body) {
			t.Fatalf("产物内容不符: got %q want %q", got, body)
		}
	}

	// 失败的产物不落盘
	if matches, _ := filepath.Glob(filepath.Join(tmp, "ps_1", "app_b2", "*.dump")); len(matches) != 0 {
		t.Fatalf("失败的备份不应落盘，得到 %v", matches)
	}

	// ListBackups：返回 2 条，最近在前
	list, err := b.ListBackups()
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("ListBackups 应=2，得到 %d", len(list))
	}
	if !sort.SliceIsSorted(list, func(i, j int) bool { return list[i].ModifiedAt.After(list[j].ModifiedAt) }) {
		t.Fatal("ListBackups 应按 ModifiedAt 倒序")
	}
}

// TestListBackups_NoDir backupRoot 不存在时不报错（返回空切片）。
func TestListBackups_NoDir(t *testing.T) {
	b := NewBackuper(nil, "/this/path/definitely/does/not/exist/zzz_anp_test", 0)
	list, err := b.ListBackups()
	if err != nil {
		t.Fatalf("不存在 backupRoot 不应报错: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("空目录应返回空切片，得到 %d", len(list))
	}
}

// TestPruneAppDir 保留策略：按 mtime 倒序保留最新 N 份，删超 N 的旧 .dump。
// 验证：retain=2 删最旧 1 份；retain<=0 不删；非 .dump 文件不动；目录不存在不报错。
func TestPruneAppDir(t *testing.T) {
	dir := t.TempDir()

	// 写 3 个 .dump（mtime 递增：old/mid/new）+ 1 个非 .dump 文件（不应被删）
	names := []string{"old.dump", "mid.dump", "new.dump"}
	for i, n := range names {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", n, err)
		}
		// 设 mtime：old=now-2h, mid=now-1h, new=now（倒序排时 new>mid>old）
		mt := time.Now().Add(-time.Duration(2-i) * time.Hour)
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatalf("chtimes %s: %v", n, err)
		}
		_ = i
	}
	if err := os.WriteFile(filepath.Join(dir, "notadump.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write txt: %v", err)
	}

	b := NewBackuper(nil, dir, 0) // retain 通过 pruneAppDir 参数传，构造值不重要

	// retain=2 → 删最旧 1 个（old.dump）
	n, err := b.pruneAppDir(dir, 2)
	if err != nil {
		t.Fatalf("pruneAppDir: %v", err)
	}
	if n != 1 {
		t.Fatalf("retain=2 应删 1 个，得到 %d", n)
	}
	got, _ := os.ReadDir(dir)
	dumps := 0
	hasOld := false
	for _, e := range got {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".dump" {
			dumps++
		}
		if e.Name() == "old.dump" {
			hasOld = true
		}
	}
	if dumps != 2 {
		t.Fatalf("应剩 2 个 dump，得到 %d", dumps)
	}
	if hasOld {
		t.Fatal("old.dump 应被删")
	}

	// retain<=0 → 不删（兜底）
	n2, err := b.pruneAppDir(dir, 0)
	if err != nil || n2 != 0 {
		t.Fatalf("retain=0 应不删，得到 n=%d err=%v", n2, err)
	}

	// 目录不存在 → 不报错（Dump 首次备份场景）
	n3, err := b.pruneAppDir(filepath.Join(dir, "nope"), 5)
	if err != nil || n3 != 0 {
		t.Fatalf("目录不存在应返回 0,nil，得到 n=%d err=%v", n3, err)
	}
}

// TestDump_TriggersPrune Dump 成功后自动清旧：retain=2 时，连 Dump 3 次 → 只剩最近 2 份。
func TestDump_TriggersPrune(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	ins := mkInstance("ps_1")
	ins.ContainerName = "pg-test-prune"
	_ = s.CreateInstance(ctx, ins)

	ad := &AppDatabase{
		ID: "apdb_prune", AppID: "app_1", ProjectSpaceID: "ps_1",
		DBName: "app_prune", DBRole: "r", PGInstanceID: ins.ID,
		DBHost: "h", DBPort: 9500, Status: StatusReady, BackupEnabled: true,
	}
	if err := s.CreateAppDB(ctx, ad); err != nil {
		t.Fatalf("create appdb: %v", err)
	}

	tmp := t.TempDir()
	b := NewBackuper(s, tmp, 2) // retain=2：保留最近 2 份
	b.dumpFn = func(ctx context.Context, container, db, pwd string, out io.Writer) error {
		_, _ = out.Write([]byte("body"))
		return nil
	}

	// 连续 Dump 3 次（mtime 单调递增需间隔，否则同秒无法排序）
	for i := 0; i < 3; i++ {
		if _, err := b.Dump(ctx, "app_1"); err != nil {
			t.Fatalf("Dump %d: %v", i, err)
		}
		time.Sleep(1100 * time.Millisecond) // 产物名按秒精度的 timestamp，需 >1s 间隔确保名字 + mtime 不同
	}

	// 应剩 2 份（最新两个）
	matches, err := filepath.Glob(filepath.Join(tmp, "ps_1", "app_1", "*.dump"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("retain=2 应剩 2 份，得到 %d: %v", len(matches), matches)
	}
}
