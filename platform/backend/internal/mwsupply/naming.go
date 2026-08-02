package mwsupply

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// dedicated redis 供给常量。
const (
	mwPortMin         = 9600              // redis dedicated 端口池下界（PG 占 9500-9599，避开）
	mwPortMax         = 9699              // 上界（100 槽；池满即配额超限 failed）
	redisImage        = "redis:7-alpine"
	redisInternalPort = 6379
	readyTimeout      = 15 * time.Second // 就绪检测轮询上限（严格场景）
	readyPingTimeout  = 5 * time.Second  // dedicated best-effort 就绪检测超时（.28 backend 拨不到时快速放行）
)

// dedicated milvus 供给常量（1:1 复刻 .28 yxt-milvus 配方）。
const (
	milvusPortMin      = 9700               // milvus dedicated 端口池下界（redis 占 9600-9699，避开）
	milvusPortMax      = 9799               // 上界（100 槽）
	milvusImage        = "milvusdb/milvus:v2.6.15"
	etcdImage          = "quay.io/coreos/etcd:v3.5.16"
	minioImage         = "minio:v20.2.5-2024.7.4"
	milvusGrpcPort     = 19530              // milvus gRPC（publish 到宿主）
	milvusHealthPort   = 9091               // milvus HTTP 健康/指标（就绪探针用，不 publish）
	etcdInternalPort   = 2379               // etcd 内部端口（milvus 经 ETCD_ENDPOINTS 访问）
	minioInternalPort  = 9000               // minio 内部端口（milvus 经 MINIO_ADDRESS 访问）
	milvusReadyTimeout = 120 * time.Second  // milvus 慢启动就绪探针上限
	readyAlpineImage   = "alpine:3.19"      // 就绪探针镜像（.28 已缓存）
)

// portRange 按 kind 给 dedicated 端口池上下界。
func portRange(kind string) (int, int) {
	if kind == "milvus" {
		return milvusPortMin, milvusPortMax
	}
	return mwPortMin, mwPortMax
}

// genShortID 生成 12 位 hex 短 ID（crypto/rand）。
func genShortID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// genPassword 随机 32 位 hex 密码（dedicated redis requirepass）。
func genPassword() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// allocPort 在 [min,max] 选首个未占用端口；无可用返回 0。纯函数，可单测。
func allocPort(used map[int]struct{}, min, max int) int {
	for p := min; p <= max; p++ {
		if _, ok := used[p]; !ok {
			return p
		}
	}
	return 0
}

// dedicatedContainerName 按 kind 前缀拼 dedicated 容器名：redis→mwredis-<short> / milvus→mwmilvus-<short>。
// redis 调用点传 kind="redis"，输出仍是 mwredis-<short>（零回归）。
func dedicatedContainerName(kind, short string) string {
	if kind == "milvus" {
		return "mwmilvus-" + short
	}
	return "mwredis-" + short
}

// milvus 栈命名：从 base（=container_name）确定性派生 sidecar 容器名与网络名。
func milvusEtcdName(base string) string  { return base + "-etcd" }
func milvusMinioName(base string) string { return base + "-minio" }
func milvusNetName(base string) string   { return base + "-net" }
