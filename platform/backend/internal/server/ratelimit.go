package server

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// RateLimiter 简单内存限流（按 IP，每分钟 N 次）。
// 生产量大可换 Redis 版；MVP 内存够用（单实例）。
type RateLimiter struct {
	mu    sync.Mutex
	store map[string]*rateBucket
	rpm   int // requests per minute
}

type rateBucket struct {
	count    int
	resetAt  time.Time
}

// NewRateLimiter 构造（rpm = 每分钟允许请求数）。
func NewRateLimiter(rpm int) *RateLimiter {
	rl := &RateLimiter{store: map[string]*rateBucket{}, rpm: rpm}
	// 定期清理过期 bucket（每 5 分钟）
	go func() {
		for {
			time.Sleep(5 * time.Minute)
			rl.mu.Lock()
			now := time.Now()
			for ip, b := range rl.store {
				if now.After(b.resetAt) {
					delete(rl.store, ip)
				}
			}
			rl.mu.Unlock()
		}
	}()
	return rl
}

// Middleware gin 中间件。
func (rl *RateLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		rl.mu.Lock()
		b, ok := rl.store[ip]
		now := time.Now()
		if !ok || now.After(b.resetAt) {
			rl.store[ip] = &rateBucket{count: 1, resetAt: now.Add(time.Minute)}
			rl.mu.Unlock()
			c.Next()
			return
		}
		b.count++
		if b.count > rl.rpm {
			rl.mu.Unlock()
			c.Header("Retry-After", "60")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"code":    42901,
				"message": "请求过于频繁，请稍后重试",
				"limit":   rl.rpm,
				"window":  "60s",
			})
			return
		}
		rl.mu.Unlock()
		c.Next()
	}
}
