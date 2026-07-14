package standard

import (
	"context"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// newTestStore 建内存 SQLite + 仅 coding_standard 表（自包含，FK 默认不强制）。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sqlx.Connect("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.MustExec(`CREATE TABLE coding_standard (
  id TEXT PRIMARY KEY,
  project_space_id TEXT,
  name TEXT NOT NULL,
  category TEXT NOT NULL DEFAULT 'general',
  content TEXT NOT NULL,
  priority INTEGER NOT NULL DEFAULT 100,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	return NewStore(db)
}

func mustCreate(t *testing.T, s *Store, ps *string, name string, prio int, enabled bool) Standard {
	t.Helper()
	st := &Standard{ProjectSpaceID: ps, Name: name, Category: "general", Content: name, Priority: prio, Enabled: enabled}
	if err := s.Create(context.Background(), st); err != nil {
		t.Fatalf("create: %v", err)
	}
	return *st
}

func TestListEffective_MergesGlobalAndProject(t *testing.T) {
	s := newTestStore(t)
	ps := "ps_1"
	mustCreate(t, s, nil, "G1", 100, true)          // 全局
	mustCreate(t, s, &ps, "P1", 50, true)            // 项目级 ps_1
	mustCreate(t, s, &ps, "P2-disabled", 10, false)  // 项目级但禁用 → 不出现
	other := "ps_2"
	mustCreate(t, s, &other, "P-other", 1, true)     // 别的空间 → 不出现

	got, err := s.ListEffective(context.Background(), "ps_1")
	if err != nil {
		t.Fatalf("ListEffective: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("应返回 2 条(全局G1+项目P1)，得到 %d: %+v", len(got), got)
	}
	// priority 升序：P1(50) 在 G1(100) 前
	if got[0].Name != "P1" || got[1].Name != "G1" {
		t.Fatalf("顺序应为 P1,G1，得到 %s,%s", got[0].Name, got[1].Name)
	}
}

func TestListEffective_GlobalOnlyWhenNoProject(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, nil, "G1", 100, true)
	got, err := s.ListEffective(context.Background(), "ps_no_proj")
	if err != nil {
		t.Fatalf("ListEffective: %v", err)
	}
	if len(got) != 1 || got[0].Name != "G1" {
		t.Fatalf("无项目级时应只返回全局，得到 %+v", got)
	}
}
