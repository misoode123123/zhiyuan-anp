package pgsupply

import (
	"regexp"
	"strings"
	"testing"
)

func TestInstanceName(t *testing.T) {
	if got := InstanceName("ps_default"); got != "pg-default" {
		t.Fatalf("应去 ps_ 前缀，得到 %s", got)
	}
	if got := InstanceName("custom"); got != "pg-custom" {
		t.Fatalf("无前缀原样，得到 %s", got)
	}
}

func TestDBNameAndRole(t *testing.T) {
	db := DBName("app_abc")
	if !strings.HasPrefix(db, "app_") || len(db) <= len("app_") {
		t.Fatalf("库名应 app_<短ID>，得到 %s", db)
	}
	if !regexp.MustCompile(`^app_[0-9a-f]+$`).MatchString(db) {
		t.Fatalf("库名应 app_<hex>，得到 %s", db)
	}
	if got := RoleName(db); got != db+"_role" {
		t.Fatalf("角色名 = 库名+_role，得到 %s", got)
	}
}

func TestGenPassword(t *testing.T) {
	p1 := genPassword()
	p2 := genPassword()
	if len(p1) != 32 || p1 == p2 {
		t.Fatalf("密码应 32 位 hex 且每次不同：%s %s", p1, p2)
	}
}

func TestDSN(t *testing.T) {
	got := DSN("10.10.0.28", 9500, "app_x_role", "pwd", "app_x")
	want := "postgres://app_x_role:pwd@10.10.0.28:9500/app_x?sslmode=disable"
	if got != want {
		t.Fatalf("DSN 不匹配:\n got %s\nwant %s", got, want)
	}
}

func TestAllocPort(t *testing.T) {
	used := map[int]struct{}{9500: {}, 9501: {}}
	if p := allocPort(used, 9500, 9599); p != 9502 {
		t.Fatalf("应跳过已占用选 9502，得到 %d", p)
	}
	full := map[int]struct{}{}
	for p := 9500; p <= 9599; p++ {
		full[p] = struct{}{}
	}
	if p := allocPort(full, 9500, 9599); p != 0 {
		t.Fatalf("全满应返回 0，得到 %d", p)
	}
}
