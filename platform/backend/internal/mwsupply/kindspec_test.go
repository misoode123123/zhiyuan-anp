package mwsupply

import "testing"

func TestRegisterAndLookupKind(t *testing.T) {
	resetRegistry(t)
	k := "testkind-x"
	RegisterKind(KindSpec{Kind: k, AddrEnv: "TESTKIND_ADDR", DisplayName: "Test"})

	got, ok := LookupKind(k)
	if !ok {
		t.Fatalf("LookupKind(%q) ok=false，应已注册", k)
	}
	if got.AddrEnv != "TESTKIND_ADDR" {
		t.Fatalf("AddrEnv=%q want TESTKIND_ADDR", got.AddrEnv)
	}
	if _, ok := LookupKind("not-registered"); ok {
		t.Fatal("未注册 kind 应 ok=false")
	}
}

// resetRegistry 清空 registry（测试隔离用）。
func resetRegistry(t *testing.T) {
	t.Helper()
	kindRegistry = map[string]KindSpec{}
}
