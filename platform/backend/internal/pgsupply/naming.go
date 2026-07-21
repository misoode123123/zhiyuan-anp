package pgsupply

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

// genShortID 生成 12 位 hex 短 ID（crypto/rand，非 math/rand）。
func genShortID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand 失败极罕见；兜底用时间无关的固定态不可取，直接 panic 让上层感知
		panic("crypto/rand: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// InstanceName 项目 PG 实例的容器名前缀：pg-<project>(去 ps_ 前缀)。
func InstanceName(psID string) string { return "pg-" + trimPSPrefix(psID) }

// DBName 应用库名：app_<hex>（不复用 appID，保证合法 + 全局唯一）。
func DBName(appID string) string { return "app_" + genShortID() }

// RoleName 库专用 role 名 = 库名 + _role。
func RoleName(dbName string) string { return dbName + "_role" }

// genPassword 随机 32 位 hex 密码。
func genPassword() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// DSN 拼直连连接串（无 pgbouncer）。
func DSN(host string, port int, user, pwd, dbName string) string {
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable", user, pwd, host, port, dbName)
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

// trimPSPrefix 去 ps_ 前缀（容器名简洁）；无前缀原样。
func trimPSPrefix(psID string) string { return strings.TrimPrefix(psID, "ps_") }
