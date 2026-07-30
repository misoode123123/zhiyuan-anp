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
	m, err := parseLinuxMetrics(cpuOut, out, "/\n 20G  10G  50%", "0.50 0.40 0.30 1/100 12345", "up 3 days")
	if err != nil {
		t.Fatal(err)
	}
	if m.CPUPercent < 20 || m.MemTotal == 0 || m.DiskTotal == 0 {
		t.Fatalf("metric: %+v", m)
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
