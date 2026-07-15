//go:build windows

package dev

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

// opencodeDir 保证传给 opencode 的 --dir 为纯 ASCII。
//
// 背景：opencode（Bun 编译）在 Windows 解析命令行 argv 时用 ANSI 代码页，
// 无法处理中文路径；但它在运行时用宽字符 API 读写中文路径是正常的。
// 故当 repo 目录含非 ASCII 字符时，在临时目录建一个 ASCII junction 指向它，
// 把 junction（ASCII）作为 --dir 传入 —— opencode 拿到 ASCII 路径不报错，
// 文件系统层透明解析 junction 到真实的中文目录。
//
// 返回：可传给 opencode 的目录 + 清理函数（调用方 defer）。
func opencodeDir(path string) (string, func(), error) {
	if isASCIIPath(path) {
		return path, func() {}, nil
	}
	link := filepath.Join(os.TempDir(), "anp-junc-"+strconv.FormatInt(time.Now().UnixNano(), 36))
	// 用 PowerShell 建 junction（.NET Unicode 原生），避开 cmd.exe 的 ANSI 代码页问题。
	cmd := exec.Command("powershell", "-NoProfile", "-Command",
		fmt.Sprintf("New-Item -ItemType Junction -Path %q -Target %q | Out-Null", link, path))
	if out, err := cmd.CombinedOutput(); err != nil {
		return path, func() {}, fmt.Errorf("为 opencode 建 ASCII junction 失败: %w: %s", err, string(out))
	}
	return link, func() { _ = os.Remove(link) }, nil
}

func isASCIIPath(p string) bool {
	for _, r := range p {
		if r > 0x7F {
			return false
		}
	}
	return true
}
