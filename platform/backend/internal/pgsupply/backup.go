package pgsupply

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Backuper 按 app 库做 pg_dump / pg_restore。
//
// 部署形态：backend 容器不含 postgresql-client（Dockerfile.backend 仅装 docker-cli），
// 故 pg_dump/pg_restore 经 `docker exec <pg容器>` 在 PG 容器内执行（pgvector 镜像自带）。
// 从 PG 容器内连本库用 localhost:5432 + 解析 admin_url_ref 得 postgres 密码（PGPASSWORD 注入）。
type Backuper struct {
	store      *Store
	backupRoot string // 如 /data/backups
	dumpFn     func(ctx context.Context, containerName, dbName, pwd string, out io.Writer) error
	restoreFn  func(ctx context.Context, containerName, dbName, dumpFile, pwd string) error
}

// NewBackuper 构造。backupRoot 形如 /data/backups。
func NewBackuper(store *Store, backupRoot string) *Backuper {
	b := &Backuper{store: store, backupRoot: backupRoot}
	b.dumpFn = b.dockerExecDump
	b.restoreFn = b.dockerExecRestore
	return b
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
	if ins.ContainerName == "" {
		return "", fmt.Errorf("实例 %s 无 container_name（迁移 000005 前的老实例，无法 docker exec）", ins.ID)
	}
	dir := filepath.Join(b.backupRoot, ad.ProjectSpaceID, appID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("建备份目录: %w", err)
	}
	now := time.Now().UTC()
	out := filepath.Join(dir, now.Format("20060102-150405")+".dump")
	f, err := os.Create(out)
	if err != nil {
		return "", fmt.Errorf("建备份文件: %w", err)
	}
	pwd := passwordFromAdminURL(ins.AdminURLRef)
	dumpErr := b.dumpFn(ctx, ins.ContainerName, ad.DBName, pwd, f)
	// 先关再判错：Windows 不能删正打开的文件；统一关闭逻辑避免遗漏。
	if cerr := f.Close(); cerr != nil && dumpErr == nil {
		dumpErr = fmt.Errorf("关备份文件: %w", cerr)
	}
	if dumpErr != nil {
		_ = os.Remove(out) // 失败产物无意义，清掉（Linux 容器内可正打开删，Windows 宿主测试需先 Close）
		return "", dumpErr
	}
	_ = b.store.SetAppDBBackup(ctx, ad.ID, now)
	return out, nil
}

// Restore 从 dump 文件恢复到某应用库（覆盖）。
// 把宿主 dump 文件 cp 进 PG 容器临时路径，再 docker exec pg_restore。
func (b *Backuper) Restore(ctx context.Context, appID, dumpFile string) error {
	ad, err := b.store.GetAppDBByApp(ctx, appID)
	if err != nil || ad == nil {
		return fmt.Errorf("应用 %s 无库记录", appID)
	}
	ins, err := b.store.GetInstance(ctx, ad.PGInstanceID)
	if err != nil || ins == nil {
		return fmt.Errorf("库所属实例不存在")
	}
	if ins.ContainerName == "" {
		return fmt.Errorf("实例 %s 无 container_name", ins.ID)
	}
	pwd := passwordFromAdminURL(ins.AdminURLRef)
	return b.restoreFn(ctx, ins.ContainerName, ad.DBName, dumpFile, pwd)
}

// BackupResult BackupAll 的累计结果。
type BackupResult struct {
	Total    int      `json:"total"`
	Success  int      `json:"success"`
	Failed   int      `json:"failed"`
	FailedID []string `json:"failed_app_ids,omitempty"`
}

// BackupAll 遍历所有应用库逐个 Dump。失败记日志不中断（一个库失败不影响其他）。
// 定时任务（main ticker）+ 手动 GET 触发都走这里。
func (b *Backuper) BackupAll(ctx context.Context) BackupResult {
	list, err := b.store.ListAppDBs(ctx, "")
	if err != nil {
		return BackupResult{}
	}
	r := BackupResult{Total: len(list)}
	for _, ad := range list {
		if ctx.Err() != nil {
			break // ctx 取消（优雅关闭），剩余跳过
		}
		if _, err := b.Dump(ctx, ad.AppID); err != nil {
			r.Failed++
			r.FailedID = append(r.FailedID, ad.AppID)
			continue
		}
		r.Success++
	}
	return r
}

// BackupFile 备份产物（GET /pgsupply/backups 返回单元）。
type BackupFile struct {
	AppID       string    `json:"app_id"`
	Name        string    `json:"name"`         // 文件名（20060102-150405.dump）
	Path        string    `json:"path"`         // backupRoot 下的相对路径
	Size        int64     `json:"size"`         // 字节
	ModifiedAt  time.Time `json:"modified_at"`  // 文件 mtime（UTC）
}

// ListBackups 扫描 backupRoot 下所有 .dump 文件，按 app_id 分组返回。
// 目录结构：backupRoot/<project_space_id>/<app_id>/<timestamp>.dump。
// app_id 解析自目录名（第二层）；扫描失败/目录不存在返回空切片。
func (b *Backuper) ListBackups() ([]BackupFile, error) {
	out := []BackupFile{}
	entries, err := os.ReadDir(b.backupRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	for _, psEntry := range entries {
		if !psEntry.IsDir() {
			continue
		}
		psDir := filepath.Join(b.backupRoot, psEntry.Name())
		appEntries, err := os.ReadDir(psDir)
		if err != nil {
			continue
		}
		for _, appEntry := range appEntries {
			if !appEntry.IsDir() {
				continue
			}
			appDir := filepath.Join(psDir, appEntry.Name())
			dumps, err := os.ReadDir(appDir)
			if err != nil {
				continue
			}
			for _, d := range dumps {
				if d.IsDir() || filepath.Ext(d.Name()) != ".dump" {
					continue
				}
				info, err := d.Info()
				if err != nil {
					continue
				}
				rel, _ := filepath.Rel(b.backupRoot, filepath.Join(appDir, d.Name()))
				out = append(out, BackupFile{
					AppID:      appEntry.Name(),
					Name:       d.Name(),
					Path:       filepath.ToSlash(rel),
					Size:       info.Size(),
					ModifiedAt: info.ModTime().UTC(),
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ModifiedAt.After(out[j].ModifiedAt)
	})
	return out, nil
}

// dockerExecDump 在 PG 容器内执行 pg_dump -Fc，输出写到 out（custom 二进制格式，stdout 流）。
// 容器内连本库：localhost:5432 + postgres user + PGPASSWORD 注入（admin_url_ref 解析）。
func (b *Backuper) dockerExecDump(ctx context.Context, containerName, dbName, pwd string, out io.Writer) error {
	args := []string{"exec", "-e", "PGPASSWORD=" + pwd, containerName,
		"pg_dump", "-Fc", "--host=localhost", "--port=5432", "--username=postgres", "--no-password", dbName}
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout = out
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker exec pg_dump: %w: %s", err, stderr.String())
	}
	return nil
}

// dockerExecRestore 把宿主 dumpFile cp 进 PG 容器 /tmp，再 docker exec pg_restore --clean --if-exists。
func (b *Backuper) dockerExecRestore(ctx context.Context, containerName, dbName, dumpFile, pwd string) error {
	// 容器内唯一临时路径（多恢复并发不撞）：/tmp/restore-<basename>
	inContainer := "/tmp/restore-" + filepath.Base(dumpFile)
	cp := exec.CommandContext(ctx, "docker", "cp", dumpFile, containerName+":"+inContainer)
	if cpOut, err := cp.CombinedOutput(); err != nil {
		return fmt.Errorf("docker cp dump into container: %w: %s", err, cpOut)
	}
	defer func() {
		_, _ = exec.CommandContext(ctx, "docker", "exec", containerName, "rm", "-f", inContainer).CombinedOutput()
	}()
	args := []string{"exec", "-e", "PGPASSWORD=" + pwd, containerName,
		"pg_restore", "--clean", "--if-exists", "--no-password",
		"--host=localhost", "--port=5432", "--username=postgres", "--dbname=" + dbName, inContainer}
	cmd := exec.CommandContext(ctx, "docker", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	// pg_restore --clean 对不存在对象会输出 warning 到 stderr 但仍返回成功；只在 Run 报错时判失败
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker exec pg_restore: %w: %s", err, stderr.String())
	}
	return nil
}

// passwordFromAdminURL 从 admin_url_ref（postgres://u:pwd@h:p/db）解析 postgres 用户密码。
// 用于 docker exec 注入 PGPASSWORD（容器内不持有 admin_url_ref）。
func passwordFromAdminURL(adminURL string) string {
	u, err := url.Parse(adminURL)
	if err != nil || u.User == nil {
		return ""
	}
	pwd, _ := u.User.Password()
	return pwd
}

// dsnForDB 把 adminURL（指向 /postgres）的库名段换成目标库（保留兼容，部分老路径用）。
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
