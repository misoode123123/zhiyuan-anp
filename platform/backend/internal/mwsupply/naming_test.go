package mwsupply

import (
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
	if n := dedicatedContainerName("abc123"); n != "mwredis-abc123" {
		t.Fatalf("容器名应 mwredis-abc123，得 %q", n)
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
