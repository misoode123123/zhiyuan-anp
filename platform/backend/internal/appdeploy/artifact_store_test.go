package appdeploy

import (
	"context"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

func setupArtifactTestDB(t *testing.T) *sqlx.DB {
	t.Helper()
	db, err := sqlx.Connect("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	for _, ddl := range []string{
		`CREATE TABLE appdeploy_application (id TEXT PRIMARY KEY)`,
		`CREATE TABLE appdeploy_artifact (id TEXT PRIMARY KEY, application_id TEXT, build_version INTEGER,
			app_kind TEXT, platform TEXT, arch TEXT, filename TEXT, size_bytes INTEGER, sha256 TEXT,
			storage_key TEXT, content_type TEXT, created_at DATETIME)`,
		`CREATE TABLE appdeploy_build_config (app_kind TEXT PRIMARY KEY, build_image TEXT, build_command TEXT,
			artifact_dir TEXT, scaffold TEXT, created_at DATETIME)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	db.Exec(`INSERT INTO appdeploy_application (id) VALUES ('app_1')`)
	return db
}

func TestArtifactStore_CreateAndList(t *testing.T) {
	db := setupArtifactTestDB(t)
	as := NewArtifactStore(db)
	a := &Artifact{ID: "art_1", ApplicationID: "app_1", BuildVersion: 3, AppKind: AppKindDesktop,
		Platform: "windows", Arch: "x64", Filename: "a.exe", SizeBytes: 42, SHA256: "abc",
		StorageKey: "artifacts/app_1/3/a.exe", ContentType: "application/x-msdownload"}
	if err := as.Create(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	list, err := as.ListByApp(context.Background(), "app_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Filename != "a.exe" {
		t.Fatalf("got %+v", list)
	}
}

func TestBuildConfigStore_GetMissing(t *testing.T) {
	db := setupArtifactTestDB(t)
	bc := NewBuildConfigStore(db)
	cfg, err := bc.Get(context.Background(), AppKindDesktop)
	if err == nil {
		t.Fatal("want error for missing config")
	}
	_ = cfg
}
