package appdeploy

import (
	"bytes"
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
	os_type TEXT, env TEXT, connect_type TEXT, ssh_port INTEGER, ssh_key TEXT, ssh_password TEXT,
	winrm_user TEXT, winrm_password TEXT, winrm_port INTEGER, last_seen DATETIME, provision_log TEXT)`)
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

// TestNodeStore_Create_WinRMPortDefault 新建 WinRM 节点未填 winrm_port 时默认 5985。
func TestNodeStore_Create_WinRMPortDefault(t *testing.T) {
	db := setupNodeTestDB(t)
	s := NewNodeStore(db)
	n := &DeployNode{Name: "win-srv", Host: "10.0.0.20", OSType: "windows", ConnectType: "winrm", WinRMUser: "admin", WinRMPassword: "pass"}
	if err := s.Create(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(context.Background(), n.ID)
	if got.WinRMPort != 5985 {
		t.Fatalf("winrm_port default = %d, want 5985", got.WinRMPort)
	}
}

// TestNodeStore_Update 验证 Update 能改字段且保留 created_at/ID。
func TestNodeStore_Update(t *testing.T) {
	db := setupNodeTestDB(t)
	s := NewNodeStore(db)
	n := &DeployNode{Name: "srv-edit", Host: "10.0.0.5", OSType: "linux", Env: "dev", ConnectType: "ssh", SSHPort: 22}
	if err := s.Create(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	orig, _ := s.Get(context.Background(), n.ID)
	orig.Name = "srv-renamed"
	orig.Host = "10.0.0.55"
	orig.ConnectType = "winrm"
	orig.WinRMPort = 6985
	orig.WinRMUser = "admin"
	orig.WinRMPassword = "newpass"
	orig.MaxApps = 30
	orig.Description = "edited"
	if err := s.Update(context.Background(), orig); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(context.Background(), n.ID)
	if got.Name != "srv-renamed" || got.Host != "10.0.0.55" || got.WinRMPort != 6985 || got.MaxApps != 30 || got.Description != "edited" {
		t.Fatalf("update not applied: %+v", got)
	}
	if !got.CreatedAt.Equal(orig.CreatedAt) {
		t.Fatalf("created_at changed: got %v want %v", got.CreatedAt, orig.CreatedAt)
	}
}

// TestUpdateNode_Handler PUT /deploy-nodes/:nid 返回更新后的节点（凭证掩码）。
func TestUpdateNode_Handler(t *testing.T) {
	db := setupNodeTestDB(t)
	store := NewStore(db)
	h := NewHandler(store, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "", nil, nil)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h.Register(r.Group("/api/v1"))

	n := &DeployNode{Name: "h-srv", Host: "10.0.0.6", ConnectType: "winrm", WinRMUser: "admin", WinRMPassword: "secret", WinRMPort: 5985}
	if err := h.nodeStore.Create(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]interface{}{
		"name": "h-srv-2", "host": "10.0.0.66", "connect_type": "winrm",
		"winrm_user": "admin", "winrm_password": "newsecret", "winrm_port": 6985,
		"os_type": "windows", "env": "prod",
	})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/deploy-nodes/"+n.ID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Code int                    `json:"code"`
		Data map[string]interface{} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Code != 0 {
		t.Fatalf("code=%d", resp.Code)
	}
	if resp.Data["name"] != "h-srv-2" || resp.Data["host"] != "10.0.0.66" {
		t.Fatalf("update response wrong: %v", resp.Data)
	}
	if v, _ := resp.Data["winrm_password"].(string); v != "" {
		t.Errorf("winrm_password not masked in update response: %v", v)
	}
	got, _ := h.nodeStore.Get(context.Background(), n.ID)
	if got.WinRMPort != 6985 {
		t.Fatalf("winrm_port not persisted: %d", got.WinRMPort)
	}
}

// TestUpdateNode_PreserveEmptyPassword 前端编辑保存时回传掩码空密码，不应覆盖 DB 真实密码。
// 回归：ListNodes/UpdateNode 返回把 winrm_password 掩码成空，前端原样回传，
// 旧版 Update 无条件写空串 → 真密码被清空 → 后续 WinRM 采集鉴权失败。
func TestUpdateNode_PreserveEmptyPassword(t *testing.T) {
	db := setupNodeTestDB(t)
	store := NewStore(db)
	h := NewHandler(store, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "", nil, nil)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h.Register(r.Group("/api/v1"))

	n := &DeployNode{Name: "w-srv", Host: "10.0.0.7", ConnectType: "winrm", OSType: "windows",
		WinRMUser: "admin", WinRMPassword: "topsecret", WinRMPort: 5985,
		SSHUser: "admin", SSHPassword: "sshsecret", SSHPort: 22}
	if err := h.nodeStore.Create(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	// 模拟前端：掩码后回传空 winrm_password + 空 ssh_password
	body, _ := json.Marshal(map[string]interface{}{
		"name": "w-srv-2", "host": "10.0.0.77", "connect_type": "winrm",
		"winrm_user": "admin", "winrm_password": "", "winrm_port": 5985,
		"ssh_user": "admin", "ssh_password": "", "ssh_port": 22,
		"os_type": "windows", "env": "prod",
	})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/deploy-nodes/"+n.ID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	got, _ := h.nodeStore.Get(context.Background(), n.ID)
	if got.WinRMPassword != "topsecret" {
		t.Fatalf("空 winrm_password 回传覆盖了真实凭证: got %q want %q", got.WinRMPassword, "topsecret")
	}
	if got.SSHPassword != "sshsecret" {
		t.Fatalf("空 ssh_password 回传覆盖了真实凭证: got %q want %q", got.SSHPassword, "sshsecret")
	}
}
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

// TestListNodes_HasOSCreds 列表附 has_os_creds(不暴露凭证),前端据此启用采集按钮。
func TestListNodes_HasOSCreds(t *testing.T) {
	db := setupNodeTestDB(t)
	store := NewStore(db)
	h := NewHandler(store, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "", nil, nil)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h.Register(r.Group("/api/v1"))

	mk := func(name, ct, sshpw, winpw string) {
		n := &DeployNode{ID: name, Name: name, ConnectType: ct, Host: "10.0.0.1"}
		n.SSHPassword, n.WinRMPassword = sshpw, winpw
		if err := h.nodeStore.Create(context.Background(), n); err != nil {
			t.Fatal(err)
		}
	}
	mk("a-ssh", "ssh", "", "")            // ssh 类型 → has
	mk("b-docker-pw", "docker_tcp", "x", "") // docker_tcp 有 ssh pw → has
	mk("c-docker-none", "docker_tcp", "", "") // 无凭证 → 无
	mk("d-winrm", "winrm", "", "x")       // winrm 凭证 → has

	req := httptest.NewRequest(http.MethodGet, "/api/v1/deploy-nodes", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var resp struct {
		Data []map[string]interface{} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	got := map[string]bool{}
	for _, n := range resp.Data {
		got[n["name"].(string)] = n["has_os_creds"].(bool)
	}
	if !got["a-ssh"] || !got["b-docker-pw"] || got["c-docker-none"] || !got["d-winrm"] {
		t.Fatalf("has_os_creds wrong: %+v", got)
	}
}
