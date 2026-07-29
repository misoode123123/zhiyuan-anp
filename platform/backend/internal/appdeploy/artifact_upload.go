package appdeploy

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
)

// ArtifactStorage 产物存储后端抽象（MinIO 或本地降级）。
// 生产由 MinIO 实现满足；Task 8 本地产物存储亦满足；单测可注入 fake。
type ArtifactStorage interface {
	// Put 上传 srcPath 到 storageKey，返回 size。contentType 用于元数据。
	Put(ctx context.Context, storageKey, srcPath, contentType string) (int64, error)
	// PresignedGet 生成下载用预签名 URL（本地降级返回空，由 handler 直接流式返回）。
	PresignedGet(ctx context.Context, storageKey string) (string, error)
	// Open 打开产物供流式下载（本地降级用）。
	Open(ctx context.Context, storageKey string) (io.ReadCloser, error)
	// Delete 删除产物实体。
	Delete(ctx context.Context, storageKey string) error
}

// UploadArtifacts 把 Builder 产出的产物算 sha256/size → 存 storage → 写 Artifact 记录。
// 单产物失败不阻塞其他；返回首个错误（若有），已成功的产物仍落库。
// 落库成功后删除构建容器内临时产物文件，避免占用磁盘。
func UploadArtifacts(ctx context.Context, app *Application, outs []ArtifactOutput, storage ArtifactStorage, as *ArtifactStore) error {
	var firstErr error
	for _, o := range outs {
		storageKey := fmt.Sprintf("artifacts/%s/%d/%s", app.ID, app.Version, o.Filename)
		size, err := storage.Put(ctx, storageKey, o.SrcPath, o.ContentType)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("upload %s: %w", o.Filename, err)
			}
			continue
		}
		sum, err := sha256File(o.SrcPath)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("sha256 %s: %w", o.Filename, err)
			}
			continue
		}
		art := &Artifact{
			ApplicationID: app.ID, BuildVersion: app.Version, AppKind: app.AppKind,
			Platform: o.Platform, Arch: o.Arch, Filename: o.Filename,
			SizeBytes: size, SHA256: sum, StorageKey: storageKey, ContentType: o.ContentType,
		}
		if err := as.Create(ctx, art); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("store %s: %w", o.Filename, err)
			}
			continue
		}
		// 落库成功，清理构建容器内临时产物（失败不阻塞返回）。
		_ = os.Remove(o.SrcPath)
	}
	return firstErr
}

// sha256File 计算文件 sha256 十六进制摘要。
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
