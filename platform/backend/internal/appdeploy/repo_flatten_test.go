package appdeploy

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFlattenSingleWrapper 导入 zip 常带顶层"包装目录"（如 `yxt_eino_v2 - 客服机器人/`），
// 不剥掉会把源码嵌进子目录 → buildpack 只看仓库根检测不到 go.mod → 误判 static 生成 nginx。
// flattenSingleWrapper 须：单一包装目录→上提到根；多顶层条目/扁平仓→不动。
func TestFlattenSingleWrapper(t *testing.T) {
	t.Run("单一包装目录上提到根", func(t *testing.T) {
		target := t.TempDir()
		// 模拟 zip 解压出单一包装目录（含中文+空格的包装名、.git、子目录）
		wrap := filepath.Join(target, "yxt_eino_v2 - 客服机器人")
		_ = os.MkdirAll(filepath.Join(wrap, "cmd/bot"), 0755)
		_ = os.WriteFile(filepath.Join(wrap, "go.mod"), []byte("module x"), 0644)
		_ = os.WriteFile(filepath.Join(wrap, "cmd", "bot", "main.go"), []byte("package main"), 0644)
		_ = os.MkdirAll(filepath.Join(wrap, ".git"), 0755) // 包装目录内带 .git

		if err := flattenSingleWrapper(target); err != nil {
			t.Fatalf("flatten 失败: %v", err)
		}
		mustExist := func(p string) {
			if _, err := os.Stat(filepath.Join(target, p)); err != nil {
				t.Errorf("期望 %s 已上提到仓库根: %v", p, err)
			}
		}
		mustExist("go.mod")
		mustExist("cmd/bot/main.go")
		mustExist(".git")
		if _, err := os.Stat(wrap); !os.IsNotExist(err) {
			t.Errorf("包装目录应已移除，stat err=%v", err)
		}
	})

	t.Run("多个顶层条目不动", func(t *testing.T) {
		target := t.TempDir()
		_ = os.MkdirAll(filepath.Join(target, "src"), 0755)
		_ = os.WriteFile(filepath.Join(target, "README.md"), []byte("hi"), 0644)
		_ = flattenSingleWrapper(target)
		if _, err := os.Stat(filepath.Join(target, "src")); err != nil {
			t.Errorf("多顶层条目不应展平，src 应仍在: %v", err)
		}
	})

	t.Run("扁平仓库不动", func(t *testing.T) {
		target := t.TempDir()
		_ = os.WriteFile(filepath.Join(target, "go.mod"), []byte("module x"), 0644)
		_ = os.WriteFile(filepath.Join(target, "main.go"), []byte("package main"), 0644)
		_ = flattenSingleWrapper(target)
		if _, err := os.Stat(filepath.Join(target, "go.mod")); err != nil {
			t.Errorf("扁平仓库不应被改动: %v", err)
		}
	})

	t.Run("仅根 .git 不当作包装", func(t *testing.T) {
		target := t.TempDir()
		_ = os.MkdirAll(filepath.Join(target, ".git"), 0755)
		_ = flattenSingleWrapper(target) // 只有 .git，非包装目录，不报错不改动
		if _, err := os.Stat(filepath.Join(target, ".git")); err != nil {
			t.Errorf("仅 .git 时不应改动: %v", err)
		}
	})
}
