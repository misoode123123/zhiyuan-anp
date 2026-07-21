package pgsupply

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Backuper 按 app 库做 pg_dump / pg_restore。
// 阶段1 简化：假设 backend 镜像含 pg_dump 客户端；若不含，部署时改用 docker exec <pg容器> pg_dump。
type Backuper struct {
	store        *Store
	backupRoot   string // 如 /data/backups
	pgDumpBin    string // 默认 pg_dump
	pgRestoreBin string // 默认 pg_restore
}

// NewBackuper 构造。backupRoot 形如 /data/backups。
func NewBackuper(store *Store, backupRoot string) *Backuper {
	return &Backuper{store: store, backupRoot: backupRoot, pgDumpBin: "pg_dump", pgRestoreBin: "pg_restore"}
}

// Dump 对某应用库做 pg_dump（custom 格式），返回产物路径。
func (b *Backuper) Dump(ctx context.Context, appID string) (string, error) {
	ad, err := b.store.GetAppDBByApp(ctx, appID)
	if err != nil || ad == nil {
		return "", fmt.Errorf("应用 %s 无库记录", appID)
	}
	ins, err := b.store.GetInstance(ctx, ad.PGInstanceID)
	if err != nil || ins == nil {
		return "", fmt.Errorf("库所属实例不存在")
	}
	dir := filepath.Join(b.backupRoot, ad.ProjectSpaceID, appID)
	now := time.Now().UTC()
	out := filepath.Join(dir, now.Format("20060102-150405")+".dump")
	appDSN := dsnForDB(ins.AdminURLRef, ad.DBName)
	cmd := exec.CommandContext(ctx, b.pgDumpBin, "--format=custom", "--file="+out, appDSN)
	if combined, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("pg_dump: %w: %s", err, combined)
	}
	_ = b.store.SetAppDBBackup(ctx, ad.ID, now)
	return out, nil
}

// Restore 从 dump 文件恢复到某应用库（覆盖）。
func (b *Backuper) Restore(ctx context.Context, appID, dumpFile string) error {
	ad, err := b.store.GetAppDBByApp(ctx, appID)
	if err != nil || ad == nil {
		return fmt.Errorf("应用 %s 无库记录", appID)
	}
	ins, err := b.store.GetInstance(ctx, ad.PGInstanceID)
	if err != nil || ins == nil {
		return fmt.Errorf("库所属实例不存在")
	}
	appDSN := dsnForDB(ins.AdminURLRef, ad.DBName)
	cmd := exec.CommandContext(ctx, b.pgRestoreBin, "--clean", "--if-exists", "--dbname="+appDSN, dumpFile)
	if combined, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("pg_restore: %w: %s", err, combined)
	}
	return nil
}

// dsnForDB 把 adminURL（指向 /postgres）的库名段换成目标库。
// adminURL 形如 postgres://u:p@h:port/postgres?sslmode=disable → /postgres 替为 /<dbName>。
// 用 strings.LastIndex 定位 /postgres（避免命中 userinfo 段 //postgres:），且确认其后是 '?' 或行尾（避免误匹配 /postgresql 等）。
func dsnForDB(adminURL, dbName string) string {
	const marker = "/postgres"
	idx := strings.LastIndex(adminURL, marker)
	if idx < 0 {
		return adminURL
	}
	after := idx + len(marker)
	if after < len(adminURL) && adminURL[after] != '?' {
		return adminURL
	}
	return adminURL[:idx] + "/" + dbName + adminURL[after:]
}
