package appdeploy

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/masterzen/winrm"
	"golang.org/x/crypto/ssh"
)

// RemoteExecutor 远程命令执行 + 文件传输抽象。SSH/WinRM 各实现一套。
type RemoteExecutor interface {
	Run(ctx context.Context, cmd string) (stdout, stderr string, exitCode int, err error)
	PutFile(ctx context.Context, localPath, remotePath string) error
	TestConnection(ctx context.Context) error
}

// NewRemoteExecutor 按 node.ConnectType 返回执行器。docker_tcp 不支持（走 docker -H）。
func NewRemoteExecutor(n *DeployNode) (RemoteExecutor, error) {
	switch n.ConnectType {
	case "ssh":
		return NewSSHExecutor(n)
	case "winrm":
		return NewWinRMExecutor(n)
	}
	return nil, fmt.Errorf("unsupported connect_type: %s（docker_tcp 节点不走 RemoteExecutor）", n.ConnectType)
}

// NewOSExecutor 按 OS 凭证返回监控用执行器(与 connect_type 解耦)。
// ssh 凭证(ssh_password/ssh_key)或 ssh 类型节点 → SSHExecutor(ssh 类型无显式 key 用默认 miscode key);
// winrm 凭证或 winrm 类型 → WinRMExecutor;
// node_local / 无 OS 凭证的 docker_tcp → (nil, nil):前者走 localCollectNode,后者跳过采集。
func NewOSExecutor(n *DeployNode) (RemoteExecutor, error) {
	if n.ID == "node_local" {
		return nil, nil
	}
	if n.SSHPassword != "" || n.SSHKey != "" || n.ConnectType == "ssh" {
		return NewSSHExecutor(n)
	}
	if n.WinRMPassword != "" || n.ConnectType == "winrm" {
		return NewWinRMExecutor(n)
	}
	return nil, nil
}

// SSHExecutor golang.org/x/crypto/ssh 实现。
type SSHExecutor struct {
	node *DeployNode
}

func NewSSHExecutor(n *DeployNode) (*SSHExecutor, error) { return &SSHExecutor{node: n}, nil }

// dial 建立带 context 的 SSH 连接。先 net.DialContext 拿到 TCP 连接，
// 再 ssh.NewClientConn 完成 SSH 握手。
func (e *SSHExecutor) dial(ctx context.Context) (*ssh.Client, error) {
	cfg := &ssh.ClientConfig{
		User: firstNonEmpty(e.node.SSHUser, "root"),
		// TODO(security): 生产环境换用 ssh.FixedHostKey + 读取 ~/.ssh/known_hosts 校验，
		// 防中间人攻击。本期为打通多机部署链路暂用 InsecureIgnoreHostKey（defer 真正修复）。
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}
	// 认证：优先密码（Windows OpenSSH、无 key 的 linux 都用密码）；无密码则回退 key。
	// Windows 采场景：go-ntlmssp 与现代 Windows Server NTLM 不兼容（WinRM type3 被拒），
	// 改走 OpenSSH 到 Windows + 密码认证。
	if e.node.SSHPassword != "" {
		cfg.Auth = []ssh.AuthMethod{ssh.Password(e.node.SSHPassword)}
	} else {
		keyPath := e.node.SSHKey
		if keyPath == "" {
			keyPath = filepath.Join(homeDir(), ".ssh", "miscode")
		}
		keyBytes, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, fmt.Errorf("read ssh key %s: %w", keyPath, err)
		}
		signer, err := ssh.ParsePrivateKey(keyBytes)
		if err != nil {
			return nil, fmt.Errorf("parse ssh key: %w", err)
		}
		cfg.Auth = []ssh.AuthMethod{ssh.PublicKeys(signer)}
	}
	addr := fmt.Sprintf("%s:%d", e.node.Host, sshPort(e.node))
	d := &net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial tcp %s: %w", addr, err)
	}
	// 注意：v0.53.0 的 ssh.NewClientConn 不收 ctx；上下文取消靠 DialContext
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("ssh handshake %s: %w", addr, err)
	}
	return ssh.NewClient(c, chans, reqs), nil
}

func (e *SSHExecutor) Run(ctx context.Context, cmd string) (string, string, int, error) {
	client, err := e.dial(ctx)
	if err != nil {
		return "", "", -1, err
	}
	defer client.Close()
	sess, err := client.NewSession()
	if err != nil {
		return "", "", -1, err
	}
	defer sess.Close()
	var stdout, stderr bytes.Buffer
	sess.Stdout = &stdout
	sess.Stderr = &stderr
	err = sess.Run(cmd)
	exitCode := 0
	if err != nil {
		if ee, ok := err.(*ssh.ExitError); ok {
			// 非零退出：返回非 nil error，调用方只判 err 即可（I1 修复）。
			// 携带 exit + stderr 摘要，避免上游 Provisioner/NativeDeployer 失败误报成功。
			exitCode = ee.ExitStatus()
			return stdout.String(), stderr.String(), exitCode, fmt.Errorf("exit %d: %s", exitCode, stderrOr(stdout, stderr))
		}
		return stdout.String(), stderr.String(), -1, err
	}
	return stdout.String(), stderr.String(), exitCode, nil
}

// PutFile 用 base64 + cat 管道上传小文件（简单可靠，无需 SFTP 依赖）。
func (e *SSHExecutor) PutFile(ctx context.Context, localPath, remotePath string) error {
	client, err := e.dial(ctx)
	if err != nil {
		return err
	}
	defer client.Close()
	data, err := os.ReadFile(localPath)
	if err != nil {
		return err
	}
	b64 := base64.StdEncoding.EncodeToString(data)
	sess, err := client.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	// M3 修复：remotePath 单引号包裹，防 deploy.yaml 不可信时的 shell 注入。
	// 单引号内再内联转义出现的单引号（' → '\''）。
	return sess.Run(fmt.Sprintf("echo %s | base64 -d > '%s'", b64, sshQuote(remotePath)))
}

func (e *SSHExecutor) TestConnection(ctx context.Context) error {
	_, _, _, err := e.Run(ctx, "echo ok")
	return err
}

// WinRMExecutor github.com/masterzen/winrm 实现。
type WinRMExecutor struct {
	node *DeployNode
}

func NewWinRMExecutor(n *DeployNode) (*WinRMExecutor, error) {
	return &WinRMExecutor{node: n}, nil
}

func (e *WinRMExecutor) endpoint() *winrm.Endpoint {
	port := e.node.WinRMPort
	if port <= 0 {
		port = 5985
	}
	return &winrm.Endpoint{Host: e.node.Host, Port: port, HTTPS: false, Insecure: true}
}

// Run 用 masterzen/winrm 的 RunWithContext 执行命令。
// API: RunWithContext(ctx, command, stdout, stderr) (exitCode int, err error)。
func (e *WinRMExecutor) Run(ctx context.Context, cmd string) (string, string, int, error) {
	// 关键：必须用 NTLM transport，不能用 DefaultParameters 的默认 transport。
	// 默认 clientRequest.Post 用 HTTP Basic 鉴权（req.SetBasicAuth），而 Windows WinRM
	// 默认禁止 HTTP 上的 Basic（仅 HTTPS/或禁用）→ 服务器返 401，且 401 响应体是 text/html
	// 错误页（非 application/soap+xml）→ body() 报 "invalid content type" → 错误信息
	// "http response error: 401 - invalid content type"。改用 ClientNTLM（NTLMv2，等同 curl --ntlm）。
	params := *winrm.DefaultParameters // copy，避免改包级全局
	params.TransportDecorator = func() winrm.Transporter { return &winrm.ClientNTLM{} }
	c, err := winrm.NewClientWithParameters(e.endpoint(), e.node.WinRMUser, e.node.WinRMPassword, &params)
	if err != nil {
		return "", "", -1, err
	}
	var stdout, stderr bytes.Buffer
	exitCode, runErr := c.RunWithContext(ctx, cmd, &stdout, &stderr)
	// I2 修复：非零退出转成非 nil error（masterzen/winrm 仅对连接层错误返回 err）。
	// 调用方只判 err 即可，Provisioner/NativeDeployer 失败不再被吞。
	if runErr == nil && exitCode != 0 {
		runErr = fmt.Errorf("exit %d: %s", exitCode, stderrOr(stdout, stderr))
	}
	return stdout.String(), stderr.String(), exitCode, runErr
}

// PutFile WinRM 上传：小文件 base64 + PowerShell 解码写盘。
func (e *WinRMExecutor) PutFile(ctx context.Context, localPath, remotePath string) error {
	data, err := os.ReadFile(localPath)
	if err != nil {
		return err
	}
	b64 := base64.StdEncoding.EncodeToString(data)
	// M3 修复：remotePath 用单引号包裹进 PowerShell 字符串，防注入。
	// PowerShell 单引号字符串内 ' 转义为 ''。
	ps := fmt.Sprintf(`$b=[Convert]::FromBase64String("%s"); [IO.File]::WriteAllBytes('%s',$b)`, b64, psQuote(remotePath))
	_, _, _, err = e.Run(ctx, ps)
	return err
}

func (e *WinRMExecutor) TestConnection(ctx context.Context) error {
	_, _, _, err := e.Run(ctx, "echo ok")
	return err
}

// 辅助
func sshPort(n *DeployNode) int {
	if n.SSHPort > 0 {
		return n.SSHPort
	}
	return 22
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// homeDir 返回当前用户家目录（跨平台）。
func homeDir() string {
	if h, _ := os.UserHomeDir(); h != "" {
		return h
	}
	// 兜底：Windows %USERPROFILE% / Unix $HOME
	return os.Getenv("HOME")
}

// stderrOr 返回 stderr（非空）或 stdout 的截断摘要，用于非零退出的错误信息。
func stderrOr(stdout, stderr bytes.Buffer) string {
	if s := stderr.String(); s != "" {
		return truncateMsg(s, 200)
	}
	return truncateMsg(stdout.String(), 200)
}

func truncateMsg(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// sshQuote 把 remotePath 转成 bash 单引号安全字面量（'…'），' 转义为 '\''。
// 用于 SSH PutFile 的重定向目标，防 deploy.yaml 不可信时的 shell 注入。
func sshQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// psQuote 把 remotePath 转成 PowerShell 单引号字符串字面量，' 转义为 ''。
// 用于 WinRM PutFile 的 [IO.File]::WriteAllBytes 路径参数，防注入。
func psQuote(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}
