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
	readyTimeout      = 15 * time.Second // 就绪检测轮询上限
)

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

// dedicatedContainerName 拼 dedicated redis 容器名：mwredis-<short>。
func dedicatedContainerName(short string) string { return "mwredis-" + short }
