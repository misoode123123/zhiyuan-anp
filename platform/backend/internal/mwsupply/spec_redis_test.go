package mwsupply

import (
	"context"
	"testing"
)

func TestRedisSpec_FieldsAndEnv(t *testing.T) {
	s := redisSpec(nil, nil, nil, nil) // 纯字段测：deps 传 nil
	if s.Kind != "redis" || s.AddrEnv != "REDIS_ADDR" || s.Token != TokenDBNumber {
		t.Fatalf("redis 基本字段错: %+v", s)
	}
	if lo, hi := s.PortRange(); lo != mwPortMin || hi != mwPortMax {
		t.Fatalf("PortRange=%d-%d want %d-%d", lo, hi, mwPortMin, mwPortMax)
	}
	if got := s.ContainerName("1a2b3c"); got != "mwredis-1a2b3c" {
		t.Fatalf("ContainerName=%q want mwredis-1a2b3c", got)
	}
	envs := s.SharedEnv("3", &ServiceInstance{Kind: "redis"})
	if len(envs) != 1 || envs[0].Key != "REDIS_DB" || envs[0].Value != "3" {
		t.Fatalf("SharedEnv(db=3)=%+v want [REDIS_DB=3]", envs)
	}
	envs = s.SharedEnv("3", &ServiceInstance{Kind: "redis", AuthRef: "pw"})
	if len(envs) != 2 || envs[1].Key != "REDIS_PASSWORD" || !envs[1].IsSecret || envs[1].Value != "pw" {
		t.Fatalf("SharedEnv(+auth)=%+v want 含 REDIS_PASSWORD=pw(secret)", envs)
	}
	if d := s.DedicatedEnv("pw"); len(d) != 1 || d[0].Key != "REDIS_PASSWORD" || d[0].Value != "pw" {
		t.Fatalf("DedicatedEnv=%+v want [REDIS_PASSWORD=pw]", d)
	}
}

// TestRedisSpec_DedicatedNilDepsSafe 纯字段测确认 LaunchDedicated/CleanupDedicated/AllocSharedToken
// 闭包在被调用前不会因 nil deps panic（仅取值/构造 KindSpec 不执行闭包）。
func TestRedisSpec_DedicatedNilDepsSafe(t *testing.T) {
	s := redisSpec(nil, nil, nil, nil)
	if s.Kind != "redis" {
		t.Fatal("构造应不 panic（nil deps 仅在闭包调用时才用到）")
	}
	// ReadyDedicated 签名含 host：确认类型可赋值（编译期保证）。
	_ = s.ReadyDedicated
	_ = s.LaunchDedicated
	_ = s.CleanupDedicated
	_ = s.AllocSharedToken
	_ = context.Background()
}
