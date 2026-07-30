package appdeploy

import (
	"context"
	"os"
	"path/filepath"
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
}

func (r *recordingPutExecutor) Run(_ context.Context, cmd string) (string, string, int, error) {
	*r.ran = append(*r.ran, cmd)
	return "", "", 0, nil
}
func (r *recordingPutExecutor) PutFile(_ context.Context, local, remote string) error {
	r.puts = append(r.puts, local+"->"+remote)
	return nil
}
func (r *recordingPutExecutor) TestConnection(_ context.Context) error { return nil }
