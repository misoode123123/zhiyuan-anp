package appdeploy

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalArtifactStorage_PutAndOpen(t *testing.T) {
	base := t.TempDir()
	s := NewLocalArtifactStorage(base)
	// 造源文件
	src := filepath.Join(base, "src.exe")
	os.WriteFile(src, []byte("hello"), 0644)
	size, err := s.Put(context.Background(), "artifacts/app_1/1/src.exe", src, "application/x-msdownload")
	if err != nil {
		t.Fatal(err)
	}
	if size != 5 {
		t.Fatalf("size = %d, want 5", size)
	}
	rc, err := s.Open(context.Background(), "artifacts/app_1/1/src.exe")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	if string(b) != "hello" {
		t.Fatalf("content = %q", b)
	}
}

func TestLocalArtifactStorage_RejectTraversal(t *testing.T) {
	base := t.TempDir()
	s := NewLocalArtifactStorage(base)
	src := filepath.Join(base, "src")
	os.WriteFile(src, []byte("x"), 0644)
	if _, err := s.Put(context.Background(), "../escape.exe", src, ""); err == nil {
		t.Fatal("want error for traversal key")
	}
}
