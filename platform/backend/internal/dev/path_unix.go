//go:build !windows

package dev

// opencodeDir 在非 Windows 平台原样返回（Linux/macOS 路径为 UTF-8，opencode 原生支持）。
func opencodeDir(path string) (string, func(), error) {
	return path, func() {}, nil
}
