package appdeploy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeDeployer_Deploy_PutFileAndRun(t *testing.T) {
	dir := t.TempDir()
	// 造产物文件
	if err := os.WriteFile(filepath.Join(dir, "app.bin"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	desc := &DeployDesc{
		Target: TargetDesc{OS: "linux", Dir: "/opt/app"},
		Steps: []StepDesc{
			{Transfer: &TransferStep{From: dir + "/*", To: "/opt/app/"}},
			{Run: &RunStep{Cmd: "chmod +x /opt/app/app.bin", Cwd: "/opt/app"}},
		},
	}
	var ranCmds []string
	fakePut := &recordingPutExecutor{ran: &ranCmds}
	d := &NativeDeployer{}
	res, err := d.Deploy(context.Background(), &Application{ID: "app_1", RepoDir: dir}, &DeployNode{OSType: "linux"}, fakePut, desc)
	if err != nil {
		t.Fatal(err)
	}
	if len(fakePut.puts) == 0 {
		t.Fatal("应 PutFile 传输产物")
	}
	if len(ranCmds) == 0 {
		t.Fatal("应 Run 脚本")
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit code want 0, got %d", res.ExitCode)
	}
}

// recordingPutExecutor 记录 PutFile + Run，不连真实服务器。
type recordingPutExecutor struct {
	ran  *[]string
	puts []string
	tl   []string // timeline：RUN:/PUT: 交错记录，便于断言先后
}

func (r *recordingPutExecutor) Run(_ context.Context, cmd string) (string, string, int, error) {
	*r.ran = append(*r.ran, cmd)
	r.tl = append(r.tl, "RUN:"+cmd)
	return "", "", 0, nil
}
func (r *recordingPutExecutor) PutFile(_ context.Context, local, remote string) error {
	r.puts = append(r.puts, local+"->"+remote)
	r.tl = append(r.tl, "PUT:"+local+"->"+remote)
	return nil
}
func (r *recordingPutExecutor) TestConnection(_ context.Context) error { return nil }

func TestNativeDeployer_Deploy_CreatesTransferDirBeforePut(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.ps1"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	desc := &DeployDesc{
		Target: TargetDesc{OS: "windows", Dir: `C:\anp\x`},
		Steps:  []StepDesc{{Transfer: &TransferStep{From: dir + "/*", To: `C:\anp\x`}}},
	}
	fake := &recordingPutExecutor{ran: new([]string)}
	if _, err := (&NativeDeployer{}).Deploy(
		context.Background(),
		&Application{ID: "a", RepoDir: dir},
		&DeployNode{OSType: "windows"},
		fake, desc,
	); err != nil {
		t.Fatalf("Deploy err: %v", err)
	}
	// 第一条须是 RUN（New-Item 建目录），且早于任何 PUT
	if len(fake.tl) < 2 || !strings.HasPrefix(fake.tl[0], "RUN:") || !strings.Contains(fake.tl[0], "New-Item") {
		t.Fatalf("应先 Run 建目录再 PutFile, timeline=%v", fake.tl)
	}
}

func TestNativeDeployer_Deploy_WindowsPathJoin(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.ps1"), []byte("write marker"), 0644); err != nil {
		t.Fatal(err)
	}
	desc := &DeployDesc{
		Target: TargetDesc{OS: "windows", Dir: `C:\anp\app`},
		Steps: []StepDesc{
			// To 不带尾分隔符，验证 joinRemotePath 补 \
			{Transfer: &TransferStep{From: dir + "/*", To: `C:\anp\app`}},
		},
	}
	fake := &recordingPutExecutor{ran: new([]string)}
	_, err := (&NativeDeployer{}).Deploy(
		context.Background(),
		&Application{ID: "app_1", RepoDir: dir},
		&DeployNode{OSType: "windows"},
		fake, desc,
	)
	if err != nil {
		t.Fatalf("Deploy err: %v", err)
	}
	// 远程路径应为 C:\anp\app\hello.ps1（补了 \）
	want := `->C:\anp\app\hello.ps1`
	found := false
	for _, p := range fake.puts {
		if strings.HasSuffix(p, want) {
			found = true
		}
	}
	if !found {
		t.Errorf("未拼出 Windows 远程路径 %q, puts=%v", want, fake.puts)
	}
}
