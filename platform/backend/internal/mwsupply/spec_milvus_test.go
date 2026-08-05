package mwsupply

import "testing"

func TestMilvusSpec_FieldsAndEnv(t *testing.T) {
	s := milvusSpec(nil, nil)
	if s.Kind != "milvus" || s.AddrEnv != "MILVUS_ADDR" || s.Token != TokenCollectionPrefix {
		t.Fatalf("milvus 基本字段错: %+v", s)
	}
	if lo, hi := s.PortRange(); lo != milvusPortMin || hi != milvusPortMax {
		t.Fatalf("PortRange=%d-%d want %d-%d", lo, hi, milvusPortMin, milvusPortMax)
	}
	if got := s.ContainerName("1a2b3c"); got != "mwmilvus-1a2b3c" {
		t.Fatalf("ContainerName=%q want mwmilvus-1a2b3c", got)
	}
	if envs := s.SharedEnv("app1a2b_", &ServiceInstance{Kind: "milvus"}); len(envs) != 1 || envs[0].Key != "MILVUS_COLLECTION_PREFIX" || envs[0].Value != "app1a2b_" {
		t.Fatalf("SharedEnv=%+v want [MILVUS_COLLECTION_PREFIX=app1a2b_]", envs)
	}
	if len(s.DedicatedEnv("")) != 0 {
		t.Fatalf("milvus DedicatedEnv 应空，得 %+v", s.DedicatedEnv(""))
	}
}
