package appdeploy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanArtifacts_Desktop(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"myapp-1.0.0-win-x64.exe":        "x",
		"myapp-1.0.0-mac-universal.dmg":  "x",
		"myapp-1.0.0-linux-x64.AppImage": "x",
		"build.log":                      "x", // 非产物，应忽略
	}
	for f, c := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(c), 0644); err != nil {
			t.Fatal(err)
		}
	}
	outs, err := ScanArtifacts(dir)
	if err != nil {
		t.Fatal(err)
	}
	byFile := map[string]ArtifactOutput{}
	for _, o := range outs {
		byFile[o.Filename] = o
	}
	cases := []struct{ file, platform, arch string }{
		{"myapp-1.0.0-win-x64.exe", "windows", "x64"},
		{"myapp-1.0.0-mac-universal.dmg", "macos", "universal"},
		{"myapp-1.0.0-linux-x64.AppImage", "linux", "x64"},
	}
	for _, c := range cases {
		o, ok := byFile[c.file]
		if !ok {
			t.Fatalf("missing artifact %s", c.file)
		}
		if o.Platform != c.platform || o.Arch != c.arch {
			t.Fatalf("%s: got %s/%s, want %s/%s", c.file, o.Platform, o.Arch, c.platform, c.arch)
		}
	}
	if _, ok := byFile["build.log"]; ok {
		t.Fatal("build.log should be ignored")
	}
}

func TestScanArtifacts_Mobile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "app-release.apk"), []byte("x"), 0644)
	outs, _ := ScanArtifacts(dir)
	if len(outs) != 1 || outs[0].Platform != "android" || outs[0].Arch != "multi" {
		t.Fatalf("got %+v", outs)
	}
}

func TestScanArtifacts_CLI(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "mycli-linux-arm64"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(dir, "mycli-darwin-x64"), []byte("x"), 0644)
	outs, _ := ScanArtifacts(dir)
	byFile := map[string]ArtifactOutput{}
	for _, o := range outs {
		byFile[o.Filename] = o
	}
	if o := byFile["mycli-linux-arm64"]; o.Platform != "linux" || o.Arch != "arm64" {
		t.Fatalf("linux-arm64: got %s/%s", o.Platform, o.Arch)
	}
	if o := byFile["mycli-darwin-x64"]; o.Platform != "macos" || o.Arch != "x64" {
		t.Fatalf("darwin-x64: got %s/%s", o.Platform, o.Arch)
	}
}
