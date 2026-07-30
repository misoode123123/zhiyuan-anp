package appdeploy

import (
	"context"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

func setupNodeTestDB(t *testing.T) *sqlx.DB {
	t.Helper()
	db, err := sqlx.Connect("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE deploy_node (
	id TEXT PRIMARY KEY, name TEXT, host TEXT, docker_url TEXT, ssh_user TEXT,
	status TEXT, max_apps INTEGER, description TEXT, created_at DATETIME,
	os_type TEXT, env TEXT, connect_type TEXT, ssh_port INTEGER, ssh_key TEXT,
	winrm_user TEXT, winrm_password TEXT, last_seen DATETIME)`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestNodeStore_Create_WithFields(t *testing.T) {
	db := setupNodeTestDB(t)
	s := NewNodeStore(db)
	n := &DeployNode{Name: "srv1", Host: "10.0.0.5", OSType: "windows", Env: "prod", ConnectType: "winrm", WinRMUser: "admin", WinRMPassword: "pass"}
	if err := s.Create(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(context.Background(), n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.OSType != "windows" || got.Env != "prod" || got.ConnectType != "winrm" || got.WinRMUser != "admin" {
		t.Fatalf("got %+v", got)
	}
}

func TestNodeStore_Create_DefaultsLinuxDev(t *testing.T) {
	db := setupNodeTestDB(t)
	s := NewNodeStore(db)
	n := &DeployNode{Name: "srv2", Host: "10.0.0.6"}
	s.Create(context.Background(), n)
	got, _ := s.Get(context.Background(), n.ID)
	if got.OSType != "linux" || got.Env != "dev" || got.ConnectType != "docker_tcp" || got.SSHPort != 22 {
		t.Fatalf("defaults wrong: %+v", got)
	}
}
