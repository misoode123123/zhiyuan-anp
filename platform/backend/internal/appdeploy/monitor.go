package appdeploy

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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

// collectNode 经 RemoteExecutor 采集单节点指标；node_local 走本地采集（读宿主 /host/proc）。
func (m *ServerMonitor) collectNode(ctx context.Context, n *DeployNode) error {
	if n.ID == "node_local" {
		return m.localCollectNode(ctx, n)
	}
	exec, err := NewOSExecutor(n) // 与 connect_type 解耦:有 OS 凭证才采
	if err != nil {
		return err
	}
	if exec == nil {
		return nil // 无 OS 凭证(docker_tcp 未配 SSH 等),跳过采集,不标 degraded
	}
	var metric ServerMetric
	metric.NodeID = n.ID
	metric.CapturedAt = time.Now()
	if n.OSType == "windows" {
		// WinRM 默认远端 shell 是 cmd.exe，须 powershell -NoProfile -Command 包装。
		// 合并成 1 条命令（1 次 WinRM 往返 + 1 次 PowerShell 启动），避免 3 条串行超 nginx 60s。
		// 输出格式：cpu|memTotal|memFree|diskTotal|diskFree
		cmd := `powershell -NoProfile -Command "$c=(Get-Counter '\Processor(_Total)\% Processor Time').CounterSamples.CookedValue;$m=Get-CimInstance Win32_OperatingSystem;$d=Get-Volume -DriveLetter C;Write-Output ($c.ToString()+'|'+$m.TotalVisibleMemorySize+'|'+$m.FreePhysicalMemory+'|'+$d.Size+'|'+$d.SizeRemaining)"`
		combined, _, _, runErr := exec.Run(ctx, cmd)
		// 关键：不要丢弃 runErr。WinRM 连不上（dial 超时/防火墙/鉴权失败）时 stdout 为空，
		// 旧版用 _ 丢掉 err 后 parseWindowsCombined("") 报「空输出」，掩盖真因（网络/鉴权）。
		// 透传 runErr，节点 degraded 日志直接显示「dial tcp :5985: i/o timeout」。
		if runErr != nil {
			return fmt.Errorf("winrm 采集 %s: %w", n.ID, runErr)
		}
		parsed, err := parseWindowsCombined(combined)
		if err != nil {
			return err
		}
		metric = parsed
		metric.NodeID = n.ID
		metric.CapturedAt = time.Now()
	} else {
		cpu, _, _, _ := exec.Run(ctx, `top -bn1 | grep "Cpu(s)"`)
		mem, _, _, _ := exec.Run(ctx, `free -k`)
		disk, _, _, _ := exec.Run(ctx, `df -kP /`)
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
	// docker_tcp 节点采容器数(docker ps -q 行数);非 docker 节点 ContainerCount 保持 0。
	if n.ConnectType == "docker_tcp" && n.DockerURL != "" {
		if out, e := runDockerOn(ctx, n.DockerURL, "ps", "-q"); e == nil {
			metric.ContainerCount = len(strings.Fields(strings.TrimSpace(out)))
		}
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

// Start 后台定期采集所有节点(无 OS 凭证的节点在 collectNode 内跳过)。阻塞调用方 goroutine,直到 ctx 取消。
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
					if err := m.collectNode(ctx, &n); err != nil {
						// I5 修复（spec §6.5）：采集失败标 degraded，前端看到节点异常。
						// 注意：成功采集后不覆盖 ready/provision 等状态（避免误把已 ready 节点刷回 online）。
						_ = m.nodeStore.SetNodeStatus(ctx, n.ID, "degraded", "采集失败: "+err.Error())
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
	// disk：df -kP / 输出（POSIX 可预测，无换行折行）。按行 split 跳过表头，
	// 取数据行第 2 列(1024-blocks=Total) 与第 3 列(Used)。
	// I3 修复：原正则 (\d+)[A-Za-z%]*\s+(\d+) 会先匹配设备名尾数字（/dev/vda1 的 1），
	// 导致 DiskTotal=1。改为按行 + 字段索引，可靠。
	dt, du, ok := parseDiskOut(diskOut)
	if !ok {
		return m, fmt.Errorf("parseLinuxMetrics: disk numbers not found in %q", diskOut)
	}
	m.DiskTotal = dt
	m.DiskUsed = du
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

// parseWindowsCombined 解析合并命令输出 `cpu|memTotal|memFree|diskTotal|diskFree`（| 分隔，单位 KB/字节）。
// memTotal/memFree 单位 KB（Win32_OperatingSystem），disk 单位字节（Get-Volume），统一存 KB→ metric 用 KB。
func parseWindowsCombined(out string) (ServerMetric, error) {
	var m ServerMetric
	parts := strings.Split(strings.TrimSpace(out), "|")
	if len(parts) < 5 {
		return m, fmt.Errorf("parseWindowsCombined: expect 5 fields, got %d in %q", len(parts), out)
	}
	m.CPUPercent = extractFloat(parts[0], `(\d+\.?\d*)`)
	memTotal := extractInt(parts[1], `(\d+)`) // KB
	memFree := extractInt(parts[2], `(\d+)`)  // KB
	m.MemTotal = memTotal
	m.MemUsed = memTotal - memFree
	diskTotal := extractInt(parts[3], `(\d+)`) // 字节
	diskFree := extractInt(parts[4], `(\d+)`)   // 字节
	m.DiskTotal = diskTotal / 1024              // → KB
	m.DiskUsed = (diskTotal - diskFree) / 1024
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

// parseDiskOut 解析 `df -kP /` 输出：跳过表头，取首个数据行第 2(1K-blocks=Total)、
// 第 3(Used) 列。df -kP 保证每挂载点一行、列名固定，避免旧正则误抓设备名尾数字。
func parseDiskOut(diskOut string) (total, used int64, ok bool) {
	lines := strings.Split(strings.TrimSpace(diskOut), "\n")
	if len(lines) < 2 {
		return 0, 0, false
	}
	for _, line := range lines[1:] { // 跳过表头 "Filesystem 1024-blocks ..."
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		t, e1 := strconv.ParseInt(fields[1], 10, 64) // 1024-blocks
		u, e2 := strconv.ParseInt(fields[2], 10, 64) // Used
		if e1 != nil || e2 != nil {
			continue
		}
		return t, u, true
	}
	return 0, 0, false
}

// localCollectNode 读宿主 /host/proc 采集本地节点（node_local）指标，不走 RemoteExecutor。
// 依赖 docker-compose 把宿主 /proc 只读挂载到容器 /host/proc。磁盘用 df -k /data（/data 挂自宿主 /opt/anp/data）。
func (m *ServerMonitor) localCollectNode(ctx context.Context, n *DeployNode) error {
	var metric ServerMetric
	metric.NodeID = n.ID
	metric.CapturedAt = time.Now()

	if b, err := os.ReadFile("/host/proc/stat"); err == nil {
		if cpu, ok := parseHostProcStat(string(b)); ok {
			metric.CPUPercent = cpu
		}
	}
	if b, err := os.ReadFile("/host/proc/meminfo"); err == nil {
		if total, avail, ok := parseHostProcMeminfo(string(b)); ok {
			metric.MemTotal = total
			metric.MemUsed = total - avail
		}
	}
	if b, err := os.ReadFile("/host/proc/loadavg"); err == nil {
		metric.LoadAvg = parseHostProcLoadavg(string(b))
	}
	if b, err := os.ReadFile("/host/proc/uptime"); err == nil {
		metric.Uptime = formatUptime(string(b))
	}
	// 磁盘：df -kP /data（容器内 /data 挂自宿主 /opt/anp/data，df 看宿主分区）
	if out, err := exec.CommandContext(ctx, "df", "-kP", "/data").Output(); err == nil {
		if t, u, ok := parseDiskOut(string(out)); ok {
			metric.DiskTotal = t
			metric.DiskUsed = u
		}
	}
	if m.nodeAppCount != nil {
		metric.AppCount, _ = m.nodeAppCount(ctx, n.ID)
	}
	return m.metricStore.Insert(ctx, &metric)
}

// parseHostProcStat 解析 /host/proc/stat 第一行 `cpu user nice system idle iowait ...`，
// 返回 CPU% = (1 - idle/total)*100（从启动累计平均，非实时，近似）。纯函数。
func parseHostProcStat(stat string) (float64, bool) {
	lines := strings.Split(stat, "\n")
	if len(lines) == 0 {
		return 0, false
	}
	fields := strings.Fields(lines[0])
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, false
	}
	var vals []int64
	for _, f := range fields[1:] {
		n, err := strconv.ParseInt(f, 10, 64)
		if err != nil {
			return 0, false
		}
		vals = append(vals, n)
	}
	var total, idle int64
	for i, v := range vals {
		total += v
		if i == 3 { // idle 在第 4 个值（index 3）
			idle = v
		}
	}
	if total == 0 {
		return 0, false
	}
	return (1 - float64(idle)/float64(total)) * 100, true
}

// parseHostProcMeminfo 解析 /host/proc/meminfo，返回 MemTotal 与 MemAvailable（KB）。纯函数。
func parseHostProcMeminfo(mem string) (total, avail int64, ok bool) {
	total = extractInt(mem, `MemTotal:\s*(\d+)`)
	avail = extractInt(mem, `MemAvailable:\s*(\d+)`)
	if avail == 0 { // 老内核无 MemAvailable，降级 MemFree
		avail = extractInt(mem, `MemFree:\s*(\d+)`)
	}
	return total, avail, total > 0
}

// parseHostProcLoadavg 解析 /host/proc/loadavg 第一列（1min 负载）。纯函数。
func parseHostProcLoadavg(load string) float64 {
	return extractFloat(load, `^(\d+\.?\d*)`)
}

// formatUptime 把 /host/proc/uptime 第一列秒数格式化为 "X days Yh Zm"。
func formatUptime(up string) string {
	fields := strings.Fields(up)
	if len(fields) == 0 {
		return ""
	}
	secs, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return ""
	}
	d := int(secs) / 86400
	h := (int(secs) % 86400) / 3600
	min := (int(secs) % 3600) / 60
	if d > 0 {
		return fmt.Sprintf("%d days %dh %dm", d, h, min)
	}
	return fmt.Sprintf("%dh %dm", h, min)
}
