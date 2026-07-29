package appdeploy

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// LocalArtifactStorage 本地文件产物存储（MinIO 未部署时降级）。storage_key 直接映射到 baseDir 下路径。
type LocalArtifactStorage struct{ base string }

func NewLocalArtifactStorage(base string) *LocalArtifactStorage {
	return &LocalArtifactStorage{base: base}
}

// safePath 防 traversal：storageKey 必须在 base 下。
func (s *LocalArtifactStorage) safePath(storageKey string) (string, error) {
	clean := filepath.Clean(storageKey)
	if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return "", fmt.Errorf("invalid storage key: %s", storageKey)
	}
	full := filepath.Join(s.base, clean)
	absBase, _ := filepath.Abs(s.base)
	absFull, _ := filepath.Abs(full)
	if !strings.HasPrefix(absFull, absBase) {
		return "", fmt.Errorf("storage key escapes base: %s", storageKey)
	}
	return full, nil
}

func (s *LocalArtifactStorage) Put(_ context.Context, storageKey, srcPath, contentType string) (int64, error) {
	dst, err := s.safePath(storageKey)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return 0, err
	}
	src, err := os.Open(srcPath)
	if err != nil {
		return 0, err
	}
	defer src.Close()
	st, err := src.Stat()
	if err != nil {
		return 0, err
	}
	out, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	defer out.Close()
	if _, err := io.Copy(out, src); err != nil {
		return 0, err
	}
	_ = contentType // 本地降级不存 content_type 元数据
	return st.Size(), nil
}

func (s *LocalArtifactStorage) Open(_ context.Context, storageKey string) (io.ReadCloser, error) {
	p, err := s.safePath(storageKey)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}

func (s *LocalArtifactStorage) PresignedGet(_ context.Context, _ string) (string, error) {
	return "", nil // 本地降级：handler 直接流式返回，不用预签名
}

func (s *LocalArtifactStorage) Delete(_ context.Context, storageKey string) error {
	p, err := s.safePath(storageKey)
	if err != nil {
		return err
	}
	return os.RemoveAll(p)
}
