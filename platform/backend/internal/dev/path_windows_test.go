//go:build windows

package dev

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOpencodeDir_JunctionForNonASCII 验证：中文路径会被转成 ASCII junction，
// junction 路径纯 ASCII、能解析到真实目录、清理后被移除。
func TestOpencodeDir_JunctionForNonASCII(t *testing.T) {
	// 建一个中文目录 + 一个标记文件
	base, err := os.MkdirTemp("", "anp-asc")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(base)
	chinese := filepath.Join(base, "测试目录", "子目录")
	if err := os.MkdirAll(chinese, 0o755); err != nil {
		t.Fatalf("MkdirAll chinese: %v", err)
	}
	marker := filepath.Join(chinese, "marker.txt")
	if err := os.WriteFile(marker, []byte("ok"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	got, cleanup, err := opencodeDir(chinese)
	if err != nil {
		t.Fatalf("opencodeDir: %v", err)
	}
	defer cleanup()

	if got == chinese {
		t.Fatalf("中文路径应被替换为 junction，得到原路径")
	}
	if !isASCIIPath(got) {
		t.Fatalf("junction 路径应纯 ASCII，得到 %q", got)
	}
	// junction 应能解析到真实目录（读到 marker）
	data, err := os.ReadFile(filepath.Join(got, "marker.txt"))
	if err != nil || string(data) != "ok" {
		t.Fatalf("junction 未正确解析到真实目录: read=%q err=%v", data, err)
	}
	// 清理后 junction 应被移除（但真实目录仍在）
	cleanup()
	if _, err := os.Stat(got); !os.IsNotExist(err) {
		t.Fatalf("清理后 junction 应被移除，stat err=%v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("清理不应删除真实目录文件，stat err=%v", err)
	}
}

// TestOpencodeDir_ASCIIPathUnchanged 纯 ASCII 路径原样返回、无清理动作。
func TestOpencodeDir_ASCIIPathUnchanged(t *testing.T) {
	d, err := os.MkdirTemp("", "ascii-dir")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(d)
	got, cleanup, err := opencodeDir(d)
	if err != nil {
		t.Fatalf("opencodeDir: %v", err)
	}
	defer cleanup()
	if got != d {
		t.Fatalf("ASCII 路径应原样返回，得到 %q want %q", got, d)
	}
}
