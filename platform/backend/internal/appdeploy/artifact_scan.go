package appdeploy

import (
	"os"
	"path/filepath"
	"strings"
)

// ScanArtifacts 扫描 dir 下产物文件，按文件名识别 platform/arch，返回产物描述。
// 识别规则：扩展名优先（.exe/.dmg/.apk/.AppImage），其次文件名内 os-arch 段（linux-arm64/darwin-x64/win-x64）。
// 无法识别为产物的文件（如 .log）忽略。
func ScanArtifacts(dir string) ([]ArtifactOutput, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var outs []ArtifactOutput
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if p, a, ok := detectPlatformArch(name); ok {
			outs = append(outs, ArtifactOutput{
				Platform:    p,
				Arch:        a,
				Filename:    name,
				ContentType: contentTypeFor(name),
				SrcPath:     filepath.Join(dir, name),
			})
		}
	}
	return outs, nil
}

// detectPlatformArch 按扩展名 + 文件名段识别平台架构。ok=false 表示非产物。
func detectPlatformArch(name string) (platform, arch string, ok bool) {
	low := strings.ToLower(name)
	switch {
	case strings.HasSuffix(low, ".exe"):
		return "windows", archFromName(name, "x64"), true
	case strings.HasSuffix(low, ".msi"):
		return "windows", archFromName(name, "x64"), true
	case strings.HasSuffix(low, ".dmg"):
		return "macos", archFromName(name, "universal"), true
	case strings.HasSuffix(low, ".apk"):
		return "android", "multi", true
	case strings.HasSuffix(low, ".appimage"):
		return "linux", archFromName(name, "x64"), true
	case strings.HasSuffix(low, ".deb"), strings.HasSuffix(low, ".rpm"):
		return "linux", archFromName(name, "x64"), true
	}
	// 无扩展名二进制：含 os-arch 段（mycli-linux-arm64 / mycli-darwin-x64）
	for _, seg := range []struct{ os, canon string }{
		{"linux", "linux"}, {"darwin", "macos"}, {"macos", "macos"}, {"win", "windows"}, {"windows", "windows"},
	} {
		if idx := strings.Index(low, "-"+seg.os+"-"); idx >= 0 {
			rest := low[idx+len(seg.os)+2:]
			a := archFromString(rest)
			if a != "" {
				return seg.canon, a, true
			}
		}
	}
	return "", "", false
}

// archFromName 从文件名找架构，找不到返回 def。
func archFromName(name, def string) string {
	for _, a := range []string{"arm64", "x64", "x86", "universal"} {
		if strings.Contains(strings.ToLower(name), "-"+a) || strings.Contains(strings.ToLower(name), "_"+a) {
			return a
		}
	}
	return def
}

func archFromString(s string) string {
	s = strings.ToLower(s)
	for _, a := range []string{"arm64", "x64", "x86", "universal"} {
		if strings.HasPrefix(s, a) {
			return a
		}
	}
	return ""
}

func contentTypeFor(name string) string {
	low := strings.ToLower(name)
	switch {
	case strings.HasSuffix(low, ".exe"), strings.HasSuffix(low, ".msi"):
		return "application/x-msdownload"
	case strings.HasSuffix(low, ".dmg"):
		return "application/x-apple-diskimage"
	case strings.HasSuffix(low, ".apk"):
		return "application/vnd.android.package-archive"
	default:
		return "application/octet-stream"
	}
}
