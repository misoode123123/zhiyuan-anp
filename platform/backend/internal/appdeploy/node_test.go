package appdeploy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
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

// TestNodeStore_SetNodeStatus 验证 status + last_seen 更新。
func TestNodeStore_SetNodeStatus(t *testing.T) {
	db := setupNodeTestDB(t)
	s := NewNodeStore(db)
	n := &DeployNode{Name: "srv3", Host: "10.0.0.7", ConnectType: "ssh"}
	if err := s.Create(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeStatus(context.Background(), n.ID, "provisioning", ""); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(context.Background(), n.ID)
	if got.Status != "provisioning" {
		t.Fatalf("status=%s want provisioning", got.Status)
	}
	if got.LastSeen == nil {
		t.Fatal("last_seen should be set")
	}
}

// TestProvisionNode_RejectDockerTCP docker_tcp 节点不应走 provision。
func TestProvisionNode_RejectDockerTCP(t *testing.T) {
	db := setupNodeTestDB(t)
	store := NewStore(db)
	h := NewHandler(store, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "", nil, nil)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h.Register(r.Group("/api/v1"))

	// 建一个 docker_tcp 节点（默认 connect_type）
	n := &DeployNode{Name: "local-docker", Host: "127.0.0.1"}
	if err := h.nodeStore.Create(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/deploy-nodes/"+n.ID+"/provision", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("docker_tcp provision should be rejected: status=%d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["code"].(float64) != 40010 {
		t.Fatalf("expect code 40010, got %v", resp["code"])
	}
}

// TestListNodes_MasksCredentials 列表 API 不回传敏感凭证。
func TestListNodes_MasksCredentials(t *testing.T) {
	db := setupNodeTestDB(t)
	store := NewStore(db)
	h := NewHandler(store, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "", nil, nil)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h.Register(r.Group("/api/v1"))

	n := &DeployNode{Name: "secret", Host: "10.0.0.9", ConnectType: "winrm", WinRMUser: "admin", WinRMPassword: "topsecret", SSHKey: "/tmp/key"}
	if err := h.nodeStore.Create(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/deploy-nodes", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Data []map[string]interface{} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Data) == 0 {
		t.Fatal("empty list")
	}
	for _, item := range resp.Data {
		if v, ok := item["winrm_password"].(string); ok && v != "" {
			t.Errorf("winrm_password not masked: %v", v)
		}
		if v, ok := item["ssh_key"].(string); ok && v != "" {
			t.Errorf("ssh_key not masked: %v", v)
		}
	}
}
