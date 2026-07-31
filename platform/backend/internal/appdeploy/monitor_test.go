package appdeploy

import (
	"testing"
)

func TestParseLinuxMetrics(t *testing.T) {
	out := `              total        used        free      shared  buff/cache   available
Mem:        16384000     4000000    8000000     100000     4000000    12000000
Swap:              0           0           0`
	// CPU：top 输出示例
	cpuOut := `%Cpu(s): 25.3 us,  5.1 sy,  0.0 ni, 69.6 id`
	// 真实 `df -kP /` 输出：表头 + /dev/vda1 数据行。旧正则会把 /dev/vda1 的尾 1 当成 DiskTotal。
	diskOut := `Filesystem     1024-blocks      Used Available Capacity Mounted on
/dev/vda1        51474016   4096000  44793216       9% /`
	m, err := parseLinuxMetrics(cpuOut, out, diskOut, "0.50 0.40 0.30 1/100 12345", "up 3 days")
	if err != nil {
		t.Fatal(err)
	}
	if m.CPUPercent < 20 || m.MemTotal == 0 || m.DiskTotal == 0 {
		t.Fatalf("metric: %+v", m)
	}
	// I3：确认取的是 1K-blocks(51474016) 而非设备名尾数字(1)
	if m.DiskTotal != 51474016 {
		t.Fatalf("DiskTotal want 51474016, got %d（可能误抓设备名尾数字）", m.DiskTotal)
	}
	if m.DiskUsed != 4096000 {
		t.Fatalf("DiskUsed want 4096000, got %d", m.DiskUsed)
	}
}

func TestParseWindowsMetrics(t *testing.T) {
	// Get-Counter 输出首行是计数器路径，次行是数值；此处传数值给解析。
	m, err := parseWindowsMetrics("25.5", `{"TotalVisibleMemorySize": 17000000, "FreePhysicalMemory": 8000000}`, "500000000000 250000000000", "2026-07-30T08:00:00")
	if err != nil {
		t.Fatal(err)
	}
	if m.CPUPercent < 20 || m.MemTotal == 0 {
		t.Fatalf("metric: %+v", m)
	}
}

func TestNewOSExecutor(t *testing.T) {
	cases := []struct {
		name string
		n    *DeployNode
		want string // "ssh" | "winrm" | "skip"
	}{
		{"ssh 类型(默认 key)", &DeployNode{ID: "n1", ConnectType: "ssh", SSHUser: "root"}, "ssh"},
		{"docker_tcp 有 ssh_password", &DeployNode{ID: "n2", ConnectType: "docker_tcp", SSHPassword: "pw"}, "ssh"},
		{"docker_tcp 有 ssh_key", &DeployNode{ID: "n3", ConnectType: "docker_tcp", SSHKey: "/k"}, "ssh"},
		{"winrm 类型", &DeployNode{ID: "n4", ConnectType: "winrm", WinRMPassword: "pw"}, "winrm"},
		{"docker_tcp 无 OS 凭证", &DeployNode{ID: "n5", ConnectType: "docker_tcp"}, "skip"},
		{"node_local", &DeployNode{ID: "node_local"}, "skip"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := NewOSExecutor(c.n)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if c.want == "skip" {
				if got != nil {
					t.Fatalf("want nil, got %T", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("want %s executor, got nil", c.want)
			}
			_, isSSH := got.(*SSHExecutor)
			_, isWin := got.(*WinRMExecutor)
			if c.want == "ssh" && !isSSH {
				t.Fatalf("want *SSHExecutor, got %T", got)
			}
			if c.want == "winrm" && !isWin {
				t.Fatalf("want *WinRMExecutor, got %T", got)
			}
		})
	}
}
