# node_local 本地采集

> 日期:2026-07-30 ｜ 状态:待评审

## 1. 背景与目标

服务器管理板块的 node_local（.28 本机，docker_tcp 节点）当前采集返回 skipped——`docker_tcp` 不走 RemoteExecutor，所以看不到 .28 宿主的 CPU/内存/磁盘指标。但 .28 是生产服务器，资源监控是实际需求。

**目标**：给 node_local 加本地采集路径——backend 容器挂载宿主 /proc，读 /host/proc 采集宿主指标，让 /servers 看板显示 .28 实时资源。

## 2. 需求边界

| 维度     | 选择                                                                   |
| -------- | ---------------------------------------------------------------------- |
| 适用节点 | node_local（id == "node_local"，本地 docker_tcp 节点）                 |
| 采集方式 | 本地读 /host/proc（宿主 /proc 只读挂载），不走 RemoteExecutor          |
| CPU%     | 累计平均（100 - idle%，从 /host/proc/stat 第一行算；非实时，标注近似） |
| 远程节点 | 不受影响，仍走 SSH/WinRM RemoteExecutor                                |

## 3. 实现

### 3.1 docker-compose backend 加挂载

`deploy/docker-compose.prod.yml` backend volumes 加：

```yaml
- /proc:/host/proc:ro # 宿主 /proc 只读（本地节点采集 CPU/内存/负载/uptime）
```

### 3.2 monitor.go 加 localCollectNode

```go
// localCollectNode 读宿主 /host/proc 采集本地节点（node_local）指标。
func (m *ServerMonitor) localCollectNode(ctx context.Context, n *DeployNode) error
```

采集命令（容器内直接读文件，无 SSH）：

- CPU：读 `/host/proc/stat` 第一行 `cpu  user nice system idle iowait ...`，算 `idle / total * 100` → CPU% = `100 - idle%`（从启动累计平均，近似）
- 内存：读 `/host/proc/meminfo`，`MemTotal:` + `MemAvailable:`（或 MemFree+Buffers+Cached），mem_used = total - available
- 负载：读 `/host/proc/loadavg` 第 1 列
- uptime：读 `/host/proc/uptime` 第 1 秒数 → 格式化
- 磁盘：`df -k /data`（/data 挂自宿主 /opt/anp/data，看宿主分区），解析 1K-blocks/Used

解析纯函数 `parseHostProcMeminfo(string) (total, avail int64)` / `parseHostProcStat(string) (cpuPercent float64)` / `parseHostProcLoadavg(string) (load float64)`，可单测。

### 3.3 collectNode 分派

`monitor.go` collectNode 开头加：

```go
if n.ID == "node_local" {
    return m.localCollectNode(ctx, n)
}
exec, err := NewRemoteExecutor(n)  // 原远程路径
```

### 3.4 Start 循环不再跳 node_local

`Start` 里 `if n.ConnectType == "docker_tcp" { continue }` 改为：node_local 不跳（走本地采集），其他 docker_tcp（如 node_30）仍跳。

### 3.5 CollectNode handler 不再 skipped

`handler.go` CollectNode 删掉 `if n.ConnectType == "docker_tcp"` 的 skipped 分支（或仅对非 node_local 的 docker_tcp 跳过），node_local 走 collectNode（本地路径）。

## 4. 关键约束

- 只 node_local 走本地采集；其他 docker_tcp 节点（如 node_30 远程 docker tcp）仍不支持（无宿主 /proc 挂载）。
- CPU% 是从启动累计平均（非实时），看板标注"近似"或接受偏差。
- /host/proc 只读挂载，安全（容器不能写宿主 /proc）。
- 解析纯函数可单测（node 环境，同 parseLinuxMetrics）。
- 部署需重建 backend 容器（加挂载）+ 重启。

## 5. 验收

1. node_local 在 /servers 看板显示 CPU/内存/磁盘/负载条。
2. `POST /deploy-nodes/node_local/collect` 返回 200 collected（不再 skipped）。
3. Start 定期采集 node_local（60s）。
4. 远程 SSH/WinRM 节点采集不受影响。
5. `go build` + `go test ./internal/appdeploy/`（parse 纯函数测试）通过。

## 6. 部署

backend 加 /proc 挂载需重建容器（docker-compose up -d --build backend，加挂载会 recreate 容器）。
