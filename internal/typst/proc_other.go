//go:build !unix

package typst

import (
	"context"
	"io"
	"os/exec"
)

// watchCommand 直接启动 typst watch。非 Unix 平台没有守护脚本，应用崩溃时可能留下进程。
func watchCommand(ctx context.Context, args []string) (*exec.Cmd, io.WriteCloser, error) {
	cmd := exec.CommandContext(ctx, "typst", args...)
	stdin, err := cmd.StdinPipe()
	return cmd, stdin, err
}

// processAlive 在非 Unix 平台上无法可靠探测，保守地认为进程仍在运行，不清理。
func processAlive(int) bool { return true }
