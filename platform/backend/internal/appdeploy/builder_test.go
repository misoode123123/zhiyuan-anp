package appdeploy

import (
	"testing"
)

func TestDispatchBuilder_SelectsByKind(t *testing.T) {
	cases := []struct{ kind, wantName string }{
		{AppKindWeb, "WebBuilder"},
		{AppKindService, "WebBuilder"}, // 本期 service 等同 web
		{AppKindDesktop, "DesktopBuilder"},
		{AppKindMobile, "MobileBuilder"},
		{AppKindCLI, "CLIBuilder"},
	}
	for _, c := range cases {
		b, err := DispatchBuilder(c.kind, nil, nil)
		if err != nil {
			t.Fatalf("kind %s: %v", c.kind, err)
		}
		if b.Name() != c.wantName {
			t.Fatalf("kind %s: got %s, want %s", c.kind, b.Name(), c.wantName)
		}
	}
}

func TestDispatchBuilder_UnknownKind(t *testing.T) {
	if _, err := DispatchBuilder("unknown", nil, nil); err == nil {
		t.Fatal("want error for unknown kind")
	}
}
