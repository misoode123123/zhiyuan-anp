package mwsupply

import (
	"bufio"
	"context"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// startFakeRedis 起假 redis（真 net.Listen）：每收到一条 RESP 命令回 +OK，命令记入返回的切片。
// 返回监听地址、命令切片指针、closer（关监听并等 goroutine 退出）。
func startFakeRedis(t *testing.T) (addr string, got *[]string, closer func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var (
		mu       sync.Mutex
		commands []string
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		bw := bufio.NewWriter(conn)
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			if !strings.HasPrefix(line, "*") {
				continue
			}
			n, _ := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "*")))
			cmd := make([]string, 0, n)
			for i := 0; i < n; i++ {
				_, _ = br.ReadString('\n') // $len\r\n 头
				data, _ := br.ReadString('\n')
				cmd = append(cmd, strings.TrimRight(data, "\r\n"))
			}
			mu.Lock()
			commands = append(commands, strings.Join(cmd, " "))
			mu.Unlock()
			_, _ = bw.WriteString("+OK\r\n")
			_ = bw.Flush()
		}
	}()
	return ln.Addr().String(), &commands, func() {
		_ = ln.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}
}

func dialFlush(t *testing.T, addr, password string, db int) error {
	t.Helper()
	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	return NewRedisFlusher().FlushDB(context.Background(), host, port, password, db)
}

// TestRedisFlush_NoAuth 无密码 → 发 SELECT <db>、FLUSHDB（无 AUTH）。
func TestRedisFlush_NoAuth(t *testing.T) {
	addr, got, closer := startFakeRedis(t)
	defer closer()
	if err := dialFlush(t, addr, "", 7); err != nil {
		t.Fatalf("FlushDB: %v", err)
	}
	want := []string{"SELECT 7", "FLUSHDB"}
	if len(*got) != len(want) {
		t.Fatalf("应 %d 条命令，得 %v", len(want), *got)
	}
	for i, w := range want {
		if (*got)[i] != w {
			t.Errorf("cmd[%d] 想 %q 得 %q", i, w, (*got)[i])
		}
	}
}

// TestRedisFlush_WithAuth 有密码 → 首条 AUTH，再 SELECT、FLUSHDB。
func TestRedisFlush_WithAuth(t *testing.T) {
	addr, got, closer := startFakeRedis(t)
	defer closer()
	if err := dialFlush(t, addr, "secret", 3); err != nil {
		t.Fatalf("FlushDB: %v", err)
	}
	want := []string{"AUTH secret", "SELECT 3", "FLUSHDB"}
	if len(*got) != len(want) {
		t.Fatalf("应 %d 条命令（含 AUTH），得 %v", len(want), *got)
	}
	for i, w := range want {
		if (*got)[i] != w {
			t.Errorf("cmd[%d] 想 %q 得 %q", i, w, (*got)[i])
		}
	}
}

// TestRedisFlush_ServerError 假 redis 回 -ERR → FlushDB 返含错误信息的错。
func TestRedisFlush_ServerError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		// 先读掉客户端发的第一条命令（SELECT），清空接收缓冲，再回 -ERR：
		// 否则 Windows 下关闭带未读数据的 socket 发 RST，会抢占 -ERR 让客户端读到 reset。
		hdr, _ := br.ReadString('\n') // *N\r\n
		if strings.HasPrefix(hdr, "*") {
			n, _ := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(hdr, "*")))
			for i := 0; i < n; i++ {
				_, _ = br.ReadString('\n') // $len\r\n
				_, _ = br.ReadString('\n') // data\r\n
			}
		}
		_, _ = io.WriteString(c, "-ERR boom\r\n")
	}()
	err = dialFlush(t, ln.Addr().String(), "", 1)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("应收含 boom 的错，得 %v", err)
	}
}

// TestRedisPing_ok 拨假 redis 发 PING 读 +OK。
func TestRedisPing_ok(t *testing.T) {
	addr, _, closer := startFakeRedis(t) // 假 redis 每条命令回 +OK（含 PING）
	defer closer()
	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	if err := NewRedisFlusher().Ping(context.Background(), host, port, ""); err != nil {
		t.Fatalf("Ping 应成功（假 redis 回 +OK），得 %v", err)
	}
}

// TestRedisPing_withAuth 有密码时先 AUTH 再 PING。
func TestRedisPing_withAuth(t *testing.T) {
	addr, got, closer := startFakeRedis(t)
	defer closer()
	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	if err := NewRedisFlusher().Ping(context.Background(), host, port, "secret"); err != nil {
		t.Fatalf("Ping(有密码) 应成功，得 %v", err)
	}
	found := false
	for _, c := range *got {
		if c == "AUTH secret" {
			found = true
		}
	}
	if !found {
		t.Fatalf("应有 AUTH secret，得 %v", *got)
	}
}

// TestRedisPing_unreachable 不可达 → Ping 返错（轮询超时）。
func TestRedisPing_unreachable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	err := NewRedisFlusher().Ping(ctx, "127.0.0.1", 1, "") // port 1 不可达
	if err == nil {
		t.Fatal("不可达应收错")
	}
}
