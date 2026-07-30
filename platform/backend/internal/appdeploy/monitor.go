package appdeploy

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ServerMonitor 周期性采集节点指标并写入 MetricStore。
type ServerMonitor struct {
	nodeStore    *NodeStore
	metricStore  *MetricStore
	nodeAppCount func(ctx context.Context, nodeID string) (int, error)
	interval     time.Duration
}

// NewServerMonitor 构造监控器。appCount 通常传 nodeStore.AppCount；为 nil 时跳过 app 计数。
func NewServerMonitor(nodeStore *NodeStore, metricStore *MetricStore, appCount func(context.Context, string) (int, error)) *ServerMonitor {
	return &ServerMonitor{nodeStore: nodeStore, metricStore: metricStore, nodeAppCount: appCount, interval: 60 * time.Second}
}

// collectNode 经 RemoteExecutor 采集单节点指标。
func (m *ServerMonitor) collectNode(ctx context.Context, n *DeployNode) error {
	exec, err := NewRemoteExecutor(n)
	if err != nil {
		return err
	}
	var metric ServerMetric
	metric.NodeID = n.ID
	metric.CapturedAt = time.Now()
	if n.OSType == "windows" {
		cpu, _, _, _ := exec.Run(ctx, `(Get-Counter '\Processor(_Total)\% Processor Time').CounterSamples.CookedValue`)
		mem, _, _, _ := exec.Run(ctx, `Get-CimInstance Win32_OperatingSystem | Select-Object TotalVisibleMemorySize,FreePhysicalMemory | ConvertTo-Json`)
		disk, _, _, _ := exec.Run(ctx, `(Get-Volume -DriveLetter C).Size,(Get-Volume -DriveLetter C).SizeRemaining`)
		parsed, err := parseWindowsMetrics(cpu, mem, disk, "")
		if err != nil {
			return err
		}
		metric = parsed
		metric.NodeID = n.ID
		metric.CapturedAt = time.Now()
	} else {
		cpu, _, _, _ := exec.Run(ctx, `top -bn1 | grep "Cpu(s)"`)
		mem, _, _, _ := exec.Run(ctx, `free -k`)
		disk, _, _, _ := exec.Run(ctx, `df -k /`)
		load, _, _, _ := exec.Run(ctx, `cat /proc/loadavg`)
		up, _, _, _ := exec.Run(ctx, `uptime -p`)
		parsed, err := parseLinuxMetrics(cpu, mem, disk, load, up)
		if err != nil {
			return err
		}
		metric = parsed
		metric.NodeID = n.ID
		metric.CapturedAt = time.Now()
	}
	if m.nodeAppCount != nil {
		metric.AppCount, _ = m.nodeAppCount(ctx, n.ID)
	}
	return m.metricStore.Insert(ctx, &metric)
}

// CollectOnce 手动触发单节点采集。
func (m *ServerMonitor) CollectOnce(ctx context.Context, nodeID string) error {
	n, err := m.nodeStore.Get(ctx, nodeID)
	if err != nil {
		return err
	}
	return m.collectNode(ctx, n)
}

// Start 后台定期采集所有非 docker_tcp 节点。阻塞调用方 goroutine，直到 ctx 取消。
func (m *ServerMonitor) Start(ctx context.Context) {
	ticker := time.NewTicker(m.interval)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				nodes, _ := m.nodeStore.List(ctx)
				for _, n := range nodes {
					if n.ConnectType == "docker_tcp" {
						continue // docker_tcp 节点不采（非 SSH/WinRM）
					}
					if err := m.collectNode(ctx, &n); err != nil {
						// 采集失败：标记 degraded（log）
						fmt.Printf("collect %s failed: %v\n", n.ID, err)
					}
				}
			}
		}
	}()
}

// parseLinuxMetrics 解析 Linux 采集输出。纯函数，格式不匹配返回 error。
func parseLinuxMetrics(cpuOut, memOut, diskOut, loadOut, upOut string) (ServerMetric, error) {
	var m ServerMetric
	// CPU：%Cpu(s): 25.3 us, ... 69.6 id → 100 - id
	id := extractFloat(cpuOut, `(\d+\.?\d*)\s*id`)
	if id < 0 {
		return m, fmt.Errorf("parseLinuxMetrics: cpu 'id' not found in %q", cpuOut)
	}
	m.CPUPercent = 100 - id
	// Mem: total used ...（free -k 输出，单位 KB）
	memRe := regexp.MustCompile(`Mem:\s+(\d+)\s+(\d+)`)
	if sm := memRe.FindStringSubmatch(memOut); len(sm) >= 3 {
		m.MemTotal, _ = strconv.ParseInt(sm[1], 10, 64)
		m.MemUsed, _ = strconv.ParseInt(sm[2], 10, 64)
	} else {
		return m, fmt.Errorf("parseLinuxMetrics: mem 'Mem:' not found in %q", memOut)
	}
	// df -k /：1K-blocks  Used ... 行末挂载点 /。提取前两个整数（兼容带单位后缀如 20G）。
	diskRe := regexp.MustCompile(`(\d+)[A-Za-z%]*\s+(\d+)[A-Za-z%]*`)
	if sm := diskRe.FindStringSubmatch(diskOut); len(sm) >= 3 {
		m.DiskTotal, _ = strconv.ParseInt(sm[1], 10, 64)
		m.DiskUsed, _ = strconv.ParseInt(sm[2], 10, 64)
	} else {
		return m, fmt.Errorf("parseLinuxMetrics: disk numbers not found in %q", diskOut)
	}
	// loadavg: 0.50 0.40 0.30 ...
	if la := extractFloat(loadOut, `^(\d+\.?\d*)`); la >= 0 {
		m.LoadAvg = la
	}
	m.Uptime = strings.TrimSpace(upOut)
	return m, nil
}

// parseWindowsMetrics 解析 Windows 采集输出。纯函数，格式不匹配返回 error。
func parseWindowsMetrics(cpuOut, memJSON, diskOut, upOut string) (ServerMetric, error) {
	var m ServerMetric
	cpu := extractFloat(cpuOut, `(\d+\.?\d*)`)
	if cpu < 0 {
		return m, fmt.Errorf("parseWindowsMetrics: cpu value not found in %q", cpuOut)
	}
	m.CPUPercent = cpu
	// memJSON: {"TotalVisibleMemorySize":17000000,"FreePhysicalMemory":8000000}
	mt := extractInt(memJSON, `"TotalVisibleMemorySize"\s*:\s*(\d+)`)
	mf := extractInt(memJSON, `"FreePhysicalMemory"\s*:\s*(\d+)`)
	if mt <= 0 {
		return m, fmt.Errorf("parseWindowsMetrics: TotalVisibleMemorySize not found in %q", memJSON)
	}
	m.MemTotal = mt
	m.MemUsed = mt - mf
	// disk: "500000000000 250000000000" → total, remaining → used = total - remaining
	dt := extractInt(diskOut, `(\d+)`)
	ds := extractInt(strings.TrimSpace(diskOut), `(\d+)\s*$`)
	if dt <= 0 {
		return m, fmt.Errorf("parseWindowsMetrics: disk numbers not found in %q", diskOut)
	}
	m.DiskTotal = dt
	m.DiskUsed = dt - ds
	m.Uptime = strings.TrimSpace(upOut)
	return m, nil
}

// extractFloat 按正则取首个浮点数；未匹配返回 -1。
func extractFloat(s, pattern string) float64 {
	re := regexp.MustCompile(pattern)
	sm := re.FindStringSubmatch(s)
	if len(sm) < 2 {
		return -1
	}
	f, _ := strconv.ParseFloat(sm[1], 64)
	return f
}

// extractInt 按正则取首个整数；未匹配返回 0。
func extractInt(s, pattern string) int64 {
	re := regexp.MustCompile(pattern)
	sm := re.FindStringSubmatch(s)
	if len(sm) < 2 {
		return 0
	}
	i, _ := strconv.ParseInt(sm[1], 10, 64)
	return i
}
