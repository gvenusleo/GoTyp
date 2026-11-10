//go:build unix

package typst

import (
	"context"
	"io"
	"os/exec"
	"syscall"
)

// watchScript 让 sh 守着标准输入：GoTyp 无论正常退出还是崩溃，内核都会关闭管道的写端，
// 守护子 shell 读到结尾后结束 typst，避免留下孤儿进程。typst 自己退出时 sh 随之退出，
// 守护子 shell 不持有 stderr，所以读取方能及时看到结尾。
const watchScript = `exec 3<&0
"$@" &
child=$!
(read _ <&3; kill $child) >/dev/null 2>&1 &
wait $child`

// watchCommand 在独立的进程组中启动 typst watch，取消时结束整个进程组。
func watchCommand(ctx context.Context, args []string) (*exec.Cmd, io.WriteCloser, error) {
	cmd := exec.CommandContext(ctx, "/bin/sh", append([]string{"-c", watchScript, "sh", "typst"}, args...)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	stdin, err := cmd.StdinPipe()
	return cmd, stdin, err
}

// processAlive 用 0 号信号探测进程是否存在；没有权限发送信号说明进程存在但属于他人。
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
