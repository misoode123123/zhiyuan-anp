package mwsupply

import (
	"regexp"
	"testing"
	"time"
)

func TestGenShortID(t *testing.T) {
	a, b := genShortID(), genShortID()
	if len(a) != 12 || len(b) != 12 {
		t.Fatalf("genShortID 应 12 字符，得 %d/%d", len(a), len(b))
	}
	if a == b {
		t.Fatal("两次 genShortID 不应相同")
	}
}

func TestGenPassword(t *testing.T) {
	p := genPassword()
	if len(p) != 32 {
		t.Fatalf("genPassword 应 32 字符，得 %d", len(p))
	}
	for _, c := range p {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Fatalf("genPassword 应 hex，含 %q", c)
		}
	}
}

func TestAllocPort(t *testing.T) {
	// 空池 → min
	if p := allocPort(map[int]struct{}{}, mwPortMin, mwPortMax); p != mwPortMin {
		t.Fatalf("空池应得 %d，得 %d", mwPortMin, p)
	}
	// 占了 9600 → 9601
	used := map[int]struct{}{9600: {}}
	if p := allocPort(used, mwPortMin, mwPortMax); p != 9601 {
		t.Fatalf("占 9600 应得 9601，得 %d", p)
	}
	// 全占 → 0
	full := map[int]struct{}{}
	for p := mwPortMin; p <= mwPortMax; p++ {
		full[p] = struct{}{}
	}
	if p := allocPort(full, mwPortMin, mwPortMax); p != 0 {
		t.Fatalf("全占应 0，得 %d", p)
	}
}

func TestDedicatedContainerName(t *testing.T) {
	if n := dedicatedContainerName("redis", "abc123"); n != "mwredis-abc123" {
		t.Fatalf("redis 容器名应 mwredis-abc123，得 %q", n)
	}
	if n := dedicatedContainerName("milvus", "abc123"); n != "mwmilvus-abc123" {
		t.Fatalf("milvus 容器名应 mwmilvus-abc123，得 %q", n)
	}
}

func TestPortRange(t *testing.T) {
	if lo, hi := portRange("redis"); lo != mwPortMin || hi != mwPortMax {
		t.Fatalf("redis 端口池应 %d-%d，得 %d-%d", mwPortMin, mwPortMax, lo, hi)
	}
	if lo, hi := portRange("milvus"); lo != milvusPortMin || hi != milvusPortMax {
		t.Fatalf("milvus 端口池应 %d-%d，得 %d-%d", milvusPortMin, milvusPortMax, lo, hi)
	}
}

func TestMilvusStackNames(t *testing.T) {
	base := dedicatedContainerName("milvus", "abc123") // mwmilvus-abc123
	if milvusEtcdName(base) != "mwmilvus-abc123-etcd" {
		t.Fatalf("etcd 名错: %q", milvusEtcdName(base))
	}
	if milvusMinioName(base) != "mwmilvus-abc123-minio" {
		t.Fatalf("minio 名错: %q", milvusMinioName(base))
	}
	if milvusNetName(base) != "mwmilvus-abc123-net" {
		t.Fatalf("net 名错: %q", milvusNetName(base))
	}
}

func TestMilvusConstants(t *testing.T) {
	if milvusPortMin != 9700 || milvusPortMax != 9799 {
		t.Fatalf("milvus 端口池应 9700-9799，得 %d-%d", milvusPortMin, milvusPortMax)
	}
	if milvusImage != "milvusdb/milvus:v2.6.15" || etcdImage != "quay.io/coreos/etcd:v3.5.16" || minioImage != "minio:v20.2.5-2024.7.4" {
		t.Fatalf("milvus 栈镜像不符: %s/%s/%s", milvusImage, etcdImage, minioImage)
	}
	if milvusGrpcPort != 19530 || milvusHealthPort != 9091 || etcdInternalPort != 2379 || minioInternalPort != 9000 {
		t.Fatalf("milvus 端口不符: grpc=%d health=%d etcd=%d minio=%d", milvusGrpcPort, milvusHealthPort, etcdInternalPort, minioInternalPort)
	}
	if milvusReadyTimeout != 120*time.Second {
		t.Fatalf("milvusReadyTimeout 应 120s，得 %v", milvusReadyTimeout)
	}
	if readyAlpineImage != "alpine:3.19" {
		t.Fatalf("探针镜像应 alpine:3.19，得 %q", readyAlpineImage)
	}
}

func TestConstants(t *testing.T) {
	if mwPortMin != 9600 || mwPortMax != 9699 {
		t.Fatalf("端口池应 9600-9699，得 %d-%d", mwPortMin, mwPortMax)
	}
	if redisImage != "redis:7-alpine" || redisInternalPort != 6379 {
		t.Fatalf("镜像/端口不符: %s/%d", redisImage, redisInternalPort)
	}
	if readyTimeout != 15*time.Second {
		t.Fatalf("readyTimeout 应 15s，得 %v", readyTimeout)
	}
}

func TestGenMilvusPrefix(t *testing.T) {
	re := regexp.MustCompile(`^app[0-9a-f]{12}_$`)
	seen := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		p := genMilvusPrefix()
		if !re.MatchString(p) {
			t.Fatalf("前缀应匹配 ^app[0-9a-f]{12}_$，得 %q", p)
		}
		if p[0] != 'a' { // 首字符须字母（milvus collection 名规则）
			t.Fatalf("首字符应字母，得 %q", p)
		}
		if seen[p] {
			t.Fatalf("1000 次内不应碰撞：%q", p)
		}
		seen[p] = true
	}
}
