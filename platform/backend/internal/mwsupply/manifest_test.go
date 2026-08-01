package mwsupply

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDepsManifest_parses(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".anp"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, ".anp", "deps.yaml"),
		[]byte("services:\n  - kind: redis\n  - kind: milvus\n    strategy: bind_existing\n"), 0o644)

	m, err := LoadDepsManifest(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(m.Services) != 2 {
		t.Fatalf("应 2 个服务，得 %d", len(m.Services))
	}
	if m.Services[0].Kind != "redis" || m.Services[0].Strategy != "" {
		t.Fatalf("redis 解析错: %+v", m.Services[0])
	}
	if m.Services[1].Kind != "milvus" || m.Services[1].Strategy != "bind_existing" {
		t.Fatalf("milvus 解析错: %+v", m.Services[1])
	}
}

func TestLoadDepsManifest_missingIsEmpty(t *testing.T) {
	m, err := LoadDepsManifest(t.TempDir()) // 无 .anp/deps.yaml
	if err != nil {
		t.Fatalf("缺失清单应不报错: %v", err)
	}
	if m == nil || len(m.Services) != 0 {
		t.Fatalf("缺失应空清单，得 %+v", m)
	}
}

func TestLoadDepsManifest_badYAMLIsEmpty(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".anp"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, ".anp", "deps.yaml"), []byte("::: not ::: yaml"), 0o644)
	m, err := LoadDepsManifest(dir)
	if err != nil {
		t.Fatalf("坏 YAML 应不报错(按空清单): %v", err)
	}
	if len(m.Services) != 0 {
		t.Fatalf("坏 YAML 应空清单，得 %d", len(m.Services))
	}
}
