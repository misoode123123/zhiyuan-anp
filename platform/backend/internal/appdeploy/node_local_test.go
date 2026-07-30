package appdeploy

import (
	"strings"
	"testing"
)

func TestParseHostProcStat(t *testing.T) {
	// /host/proc/stat 第一行：cpu user nice system idle iowait ...
	stat := "cpu  158249 1234 45678 9876543 1234 0 1234 0 0\ncpu0 100 1 2 3 4\n"
	cpu, ok := parseHostProcStat(stat)
	if !ok {
		t.Fatal("parseHostProcStat 应成功")
	}
	// total = 158249+1234+45678+9876543+1234+0+1234+0+0 = 10039472; idle = 9876543
	// cpu% = (1 - 9876543/10039472)*100 = 1.626...
	if cpu < 1 || cpu > 3 {
		t.Fatalf("cpu%% = %v，期望 ~1.6", cpu)
	}
}

func TestParseHostProcStat_BadInput(t *testing.T) {
	if _, ok := parseHostProcStat("garbage"); ok {
		t.Fatal("垃圾输入应 ok=false")
	}
}

func TestParseHostProcMeminfo(t *testing.T) {
	mem := `MemTotal:       16384000 kB
MemFree:          800000 kB
MemAvailable:   12000000 kB
Buffers:          100000 kB
`
	total, avail, ok := parseHostProcMeminfo(mem)
	if !ok {
		t.Fatal("应成功")
	}
	if total != 16384000 || avail != 12000000 {
		t.Fatalf("total=%d avail=%d", total, avail)
	}
}

func TestParseHostProcMeminfo_NoAvailable(t *testing.T) {
	// 老内核无 MemAvailable，降级 MemFree
	mem := "MemTotal:  16384000 kB\nMemFree:   800000 kB\n"
	_, avail, ok := parseHostProcMeminfo(mem)
	if !ok {
		t.Fatal("应成功")
	}
	if avail != 800000 {
		t.Fatalf("avail=%d，应降级 MemFree=800000", avail)
	}
}

func TestParseHostProcLoadavg(t *testing.T) {
	load := "0.50 0.40 0.30 1/100 12345\n"
	if v := parseHostProcLoadavg(load); v != 0.50 {
		t.Fatalf("load=%v", v)
	}
}

func TestFormatUptime(t *testing.T) {
	// 3 天 2 小时 30 分 = 3*86400 + 2*3600 + 30*60 = 259200 + 7200 + 1800 = 268200
	up := "268200.67 87654.32 1/2\n"
	got := formatUptime(up)
	if !strings.Contains(got, "3 days") || !strings.Contains(got, "2h") {
		t.Fatalf("uptime=%q", got)
	}
}

func TestFormatUptime_Short(t *testing.T) {
	up := "3661.0 0.0\n" // 1h 1m
	got := formatUptime(up)
	if !strings.Contains(got, "1h") {
		t.Fatalf("uptime=%q", got)
	}
}
