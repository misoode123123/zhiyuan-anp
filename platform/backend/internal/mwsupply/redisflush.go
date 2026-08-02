package mwsupply

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// DBFlusher 清空指定 redis db（shared 重分配时保证干净隔离位）。
type DBFlusher interface {
	FlushDB(ctx context.Context, host string, port int, password string, db int) error
}

// ReadyChecker 探测 redis 是否就绪（dedicated 起容器后轮询 AUTH+PING 至通）。
type ReadyChecker interface {
	Ping(ctx context.Context, host string, port int, password string) error
}

// NewRedisFlusher 构造裸 RESP 实现（net.Dial，不引 go-redis/redigo）；同时满足 DBFlusher + ReadyChecker。
func NewRedisFlusher() *redisFlusher { return &redisFlusher{} }

type redisFlusher struct{}

// FlushDB 连 redis（dialRedis 含可选 AUTH），发 SELECT <db>、FLUSHDB。
func (f *redisFlusher) FlushDB(ctx context.Context, host string, port int, password string, db int) error {
	conn, err := dialRedis(ctx, host, port, password)
	if err != nil {
		return err
	}
	defer conn.Close()
	return flushConn(conn, db)
}

// Ping 轮询 dialRedis+pingConn 至成功或 ctx 超时（dedicated 就绪检测）。
// 不可达（如 .28 backend 拨不到 dedicated 容器）→ 返 ctx 超时错，调用方据此判 failed。
func (f *redisFlusher) Ping(ctx context.Context, host string, port int, password string) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	// 首次立即试
	if err := pingOnce(ctx, host, port, password); err == nil {
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("redis 就绪检测超时: %w", ctx.Err())
		case <-ticker.C:
			if err := pingOnce(ctx, host, port, password); err == nil {
				return nil
			}
		}
	}
}

// pingOnce 拨一次 + AUTH(可选) + PING。
func pingOnce(ctx context.Context, host string, port int, password string) error {
	conn, err := dialRedis(ctx, host, port, password)
	if err != nil {
		return err
	}
	defer conn.Close()
	return pingConn(conn)
}

// dialRedis 建立 TCP 连接（带 ctx 超时）+ 可选 AUTH。返回已认证的连接。
func dialRedis(ctx context.Context, host string, port int, password string) (net.Conn, error) {
	d := net.Dialer{Timeout: 3 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("dial redis: %w", err)
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	if password != "" {
		bw := bufio.NewWriter(conn)
		br := bufio.NewReader(conn)
		if err := writeCmd(bw, "AUTH", password); err != nil {
			conn.Close()
			return nil, err
		}
		if err := readOK(br); err != nil {
			conn.Close()
			return nil, fmt.Errorf("AUTH: %w", err)
		}
	}
	return conn, nil
}

// flushConn 在已建连（已 AUTH）上发 SELECT <db>、FLUSHDB。
func flushConn(rw io.ReadWriter, db int) error {
	bw := bufio.NewWriter(rw)
	br := bufio.NewReader(rw)
	if err := writeCmd(bw, "SELECT", strconv.Itoa(db)); err != nil {
		return err
	}
	if err := readOK(br); err != nil {
		return fmt.Errorf("SELECT %d: %w", db, err)
	}
	if err := writeCmd(bw, "FLUSHDB"); err != nil {
		return err
	}
	if err := readOK(br); err != nil {
		return fmt.Errorf("FLUSHDB: %w", err)
	}
	return nil
}

// pingConn 在已建连（已 AUTH）上发 PING，读 +PONG（+OK 也算成功，假 redis 用）。
func pingConn(rw io.ReadWriter) error {
	bw := bufio.NewWriter(rw)
	if err := writeCmd(bw, "PING"); err != nil {
		return err
	}
	br := bufio.NewReader(rw)
	line, err := br.ReadString('\n')
	if err != nil {
		return fmt.Errorf("读 PING 回包: %w", err)
	}
	if len(line) == 0 || (line[0] != '+' && line[0] != '-') {
		return fmt.Errorf("非预期 PING 回包: %q", line)
	}
	if line[0] == '-' {
		return fmt.Errorf("redis PING: %s", strings.TrimSpace(line[1:]))
	}
	return nil // +PONG / +OK 均视作就绪
}

// writeCmd 写一条 RESP 命令：*N\r\n + N×($len\r\n arg \r\n)。
func writeCmd(bw *bufio.Writer, args ...string) error {
	if _, err := fmt.Fprintf(bw, "*%d\r\n", len(args)); err != nil {
		return err
	}
	for _, a := range args {
		if _, err := fmt.Fprintf(bw, "$%d\r\n%s\r\n", len(a), a); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// readOK 读一个 RESP 回包，要求 simple-string +OK；-ERR / 其他 → 错。
func readOK(br *bufio.Reader) error {
	line, err := br.ReadString('\n')
	if err != nil {
		return fmt.Errorf("读回包: %w", err)
	}
	if len(line) == 0 {
		return fmt.Errorf("空回包")
	}
	switch line[0] {
	case '+':
		return nil
	case '-':
		return fmt.Errorf("redis: %s", strings.TrimSpace(line[1:]))
	default:
		return fmt.Errorf("非预期回包首字节 %q: %q", string(line[0]), line)
	}
}
