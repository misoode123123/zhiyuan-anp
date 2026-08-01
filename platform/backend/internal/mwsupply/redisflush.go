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
// 由 NewReconciler 注入；测试传 fake。FLUSHDB 幂等。
type DBFlusher interface {
	FlushDB(ctx context.Context, host string, port int, password string, db int) error
}

// NewRedisFlusher 构造裸 RESP 实现（net.Dial，不引 go-redis/redigo）。
func NewRedisFlusher() DBFlusher { return &redisFlusher{} }

type redisFlusher struct{}

// FlushDB 连 redis，依次发（可选 AUTH）、SELECT <db>、FLUSHDB，每条读 +OK。
func (f *redisFlusher) FlushDB(ctx context.Context, host string, port int, password string, db int) error {
	d := net.Dialer{Timeout: 3 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("dial redis: %w", err)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	return flushConn(conn, password, db)
}

// flushConn 在已建连上发 AUTH(可选)/SELECT/FLUSHDB。抽出便于用 fake 连接单测。
func flushConn(rw io.ReadWriter, password string, db int) error {
	bw := bufio.NewWriter(rw)
	br := bufio.NewReader(rw)
	if password != "" {
		if err := writeCmd(bw, "AUTH", password); err != nil {
			return err
		}
		if err := readOK(br); err != nil {
			return fmt.Errorf("AUTH: %w", err)
		}
	}
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
